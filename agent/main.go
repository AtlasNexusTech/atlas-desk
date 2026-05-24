// Atlas Desk Agent v0.3 — JPEG streaming + numeric IDs + file transfer
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
	"github.com/pion/webrtc/v4"
)

var (
	signalURL  = flag.String("signal", "ws://localhost:8800/ws", "Signaling server URL")
	agentID    = flag.String("id", "", "Agent ID (generated if empty)")
	fps        = flag.Int("fps", 15, "Capture framerate")
	jpegQuality = flag.Int("quality", 65, "JPEG quality (1-100)")
)

// ── Protocol messages ──────────────────────────────────────

type SignalMsg struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Target  string          `json:"target,omitempty"`
	From    string          `json:"from,omitempty"`
}

type SDPMsg struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type ICEMsg struct {
	Candidate string `json:"candidate"`
	SDPMid    string `json:"sdpMid"`
}

type FrameMeta struct {
	W     int   `json:"w"`
	H     int   `json:"h"`
	Frame uint64 `json:"f"`
}

// ── Main ────────────────────────────────────────────────────

func main() {
	flag.Parse()

	// Load or generate numeric ID
	id := loadOrGenerateID()
	if *agentID != "" {
		id = *agentID
	}
	log.Printf("◆ Atlas Desk Agent v0.3  ID: %s", id)

	// Connect to signaling
	conn, _, err := websocket.DefaultDialer.Dial(*signalURL, nil)
	if err != nil {
		log.Fatalf("Signal error: %v", err)
	}
	defer conn.Close()

	sendSignal(conn, SignalMsg{
		Type:    "register",
		Payload: mustJSON(map[string]string{"id": id, "role": "agent"}),
	})
	log.Printf("Registered to signaling server")

	bounds := screenshot.GetDisplayBounds(0)
	log.Printf("Display: %dx%d @ %d FPS  JPEG Q=%d", bounds.Dx(), bounds.Dy(), *fps, *jpegQuality)

	// Wait for client, then set up P2P
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Fatal("Signal disconnected:", err)
		}
		var msg SignalMsg
		json.Unmarshal(raw, &msg)

		switch msg.Type {
		case "client_hello":
			go handleSession(conn, msg.From, id, bounds)
		case "pong":
		}
	}
}

// ── P2P Session ─────────────────────────────────────────────

func handleSession(conn *websocket.Conn, clientID, myID string, bounds image.Rectangle) {
	log.Printf("🔗 Client: %s — establishing P2P", clientID)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Printf("PC error: %v", err)
		return
	}
	defer pc.Close()

	// ── Data channels ──
	// "input" — mouse/keyboard from client
	// "screen" — JPEG frames to client
	// "files" — file push to client

	screenReady := make(chan *webrtc.DataChannel, 1)
	inputReady := make(chan struct{})
	filesReady := make(chan *webrtc.DataChannel, 1)

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "input":
			dc.OnOpen(func() { close(inputReady) })
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				var input map[string]interface{}
				json.Unmarshal(msg.Data, &input)
				handleInput(input)
			})
		case "screen":
			// We create this one (agent → client)
			dc.OnOpen(func() { screenReady <- dc })
		case "files":
			dc.OnOpen(func() { filesReady <- dc })
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				handleFileReceive(msg.Data, dc)
			})
		}
	})

	// Create screen + files channels (agent → client)
	screenDC, err := pc.CreateDataChannel("screen", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("screen DC error: %v", err)
		return
	}
	var gotScreen *webrtc.DataChannel
	screenDC.OnOpen(func() { gotScreen = screenDC })

	fileDC, err := pc.CreateDataChannel("files", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("files DC error: %v", err)
		return
	}
	fileDC.OnOpen(func() {})

	// ── ICE relay ──
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		sendSignal(conn, SignalMsg{
			Type:    "ice_candidate",
			Target:  clientID,
			Payload: mustJSON(ICEMsg{Candidate: c.ToJSON().Candidate}),
		})
	})

	// ── Create offer ──
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("Offer error: %v", err)
		return
	}
	pc.SetLocalDescription(offer)
	sendSignal(conn, SignalMsg{
		Type:    "offer",
		Target:  clientID,
		Payload: mustJSON(SDPMsg{SDP: offer.SDP, Type: "offer"}),
	})

	// ── Receive answer + ICE ──
	answerCh := make(chan webrtc.SessionDescription)
	iceCh := make(chan webrtc.ICECandidateInit)

	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg SignalMsg
			json.Unmarshal(raw, &msg)
			switch msg.Type {
			case "answer":
				var sdp SDPMsg
				json.Unmarshal(msg.Payload, &sdp)
				answerCh <- webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp.SDP}
			case "ice_candidate":
				var ice ICEMsg
				json.Unmarshal(msg.Payload, &ice)
				iceCh <- webrtc.ICECandidateInit{Candidate: ice.Candidate}
			}
		}
	}()

	select {
	case answer := <-answerCh:
		pc.SetRemoteDescription(answer)
	}

	go func() {
		for ice := range iceCh {
			pc.AddICECandidate(ice)
		}
	}()

	// ── Wait for data channels ──
	<-inputReady
	log.Printf("✅ P2P connected — streaming %dx%d @ %d FPS", bounds.Dx(), bounds.Dy(), *fps)

	// ── Frame capture loop ──
	ticker := time.NewTicker(time.Second / time.Duration(*fps))
	defer ticker.Stop()

	var frameCount uint64
	for range ticker.C {
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			continue
		}

		// Encode as JPEG
		var jpgBuf bytes.Buffer
		jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: *jpegQuality})

		// Build length-prefixed frame: [4B metaLen][JSON meta][4B jpgLen][JPEG]
		meta := FrameMeta{W: bounds.Dx(), H: bounds.Dy(), Frame: frameCount}
		metaJSON, _ := json.Marshal(meta)

		var pkt bytes.Buffer
		binary.Write(&pkt, binary.BigEndian, uint32(len(metaJSON)))
		pkt.Write(metaJSON)
		binary.Write(&pkt, binary.BigEndian, uint32(jpgBuf.Len()))
		pkt.Write(jpgBuf.Bytes())

		if gotScreen != nil && gotScreen.ReadyState() == webrtc.DataChannelStateOpen {
			gotScreen.Send(pkt.Bytes())
		}

		frameCount++
	}
}

// ── File Receive ────────────────────────────────────────────

type FileMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	ID   string `json:"id"`
}

var activeReceives = make(map[string]*os.File)

func handleFileReceive(data []byte, dc *webrtc.DataChannel) {
	// First 4 bytes: msg type length
	if len(data) < 4 {
		return
	}
	metaLen := binary.BigEndian.Uint32(data[:4])
	if int(4+metaLen) > len(data) {
		return
	}
	metaJSON := data[4 : 4+metaLen]
	payload := data[4+metaLen:]

	var meta map[string]interface{}
	json.Unmarshal(metaJSON, &meta)

	msgType, _ := meta["type"].(string)
	switch msgType {
	case "file_start":
		name, _ := meta["name"].(string)
		sizeF, _ := meta["size"].(float64)
		fileID, _ := meta["id"].(string)
		size := int64(sizeF)
		log.Printf("📥 Receiving: %s (%d bytes)", name, size)

		dir := filepath.Join(os.Getenv("HOME"), "Downloads", "AtlasDesk")
		os.MkdirAll(dir, 0755)
		outPath := filepath.Join(dir, name)

		f, err := os.Create(outPath)
		if err != nil {
			log.Printf("File create error: %v", err)
			return
		}
		activeReceives[fileID] = f

	case "file_chunk":
		fileID, _ := meta["id"].(string)
		f, ok := activeReceives[fileID]
		if !ok {
			return
		}
		if offsetF, ok := meta["offset"].(float64); ok {
			offset := int64(offsetF)
			f.WriteAt(payload, offset)
		} else {
			f.Write(payload)
		}

	case "file_end":
		fileID, _ := meta["id"].(string)
		if f, ok := activeReceives[fileID]; ok {
			f.Close()
			delete(activeReceives, fileID)
			log.Printf("✅ Received: %s → ~/Downloads/AtlasDesk/", meta["name"])
		}

	case "file_error":
		fileID, _ := meta["id"].(string)
		if f, ok := activeReceives[fileID]; ok {
			f.Close()
			delete(activeReceives, fileID)
		}
		log.Printf("❌ File error: %v", meta["error"])
	}
}

// ── ID Management ───────────────────────────────────────────

func loadOrGenerateID() string {
	home, _ := os.UserHomeDir()
	idFile := filepath.Join(home, ".atlas-desk", "id")

	if data, err := os.ReadFile(idFile); err == nil {
		return string(bytes.TrimSpace(data))
	}

	// Generate random 9-digit numeric ID
	n, err := rand.Int(rand.Reader, big.NewInt(900_000_000))
	if err != nil {
		panic(err)
	}
	id := fmt.Sprintf("%09d", n.Int64()+100_000_000)

	os.MkdirAll(filepath.Dir(idFile), 0700)
	os.WriteFile(idFile, []byte(id), 0600)
	log.Printf("🆔 Generated new ID: %s", id)
	return id
}

// ── Helpers ─────────────────────────────────────────────────

func sendSignal(conn *websocket.Conn, msg SignalMsg) {
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

func mustJSON(v interface{}) json.RawMessage {
	d, _ := json.Marshal(v)
	return d
}
