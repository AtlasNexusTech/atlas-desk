// Atlas Desk Agent v0.7 — Tray icon + auto-reconnect + aliases + settings
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
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
	signalURL   = flag.String("signal", "ws://localhost:8800/ws", "Signaling server URL")
	agentIDFlag = flag.String("id", "", "Agent ID (generated if empty)")
	fps         = flag.Int("fps", 15, "Capture framerate")
	jpegQuality = flag.Int("quality", 65, "JPEG quality (1-100)")
	password    = flag.String("pass", "", "Connection password (overrides config)")
	alias       = flag.String("alias", "", "Display alias (e.g. 'PC Bureau')")
)

// ── Config ───────────────────────────────────────────────────

type Config struct {
	ID          string       `json:"id"`
	Password    string       `json:"password,omitempty"` // SHA-256 of password, or empty = no auth
	Alias       string       `json:"alias,omitempty"`    // Display name
	TurnServers []TurnServer `json:"turn_servers,omitempty"`
}

type TurnServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
}

func loadConfig() *Config {
	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".atlas-desk", "config.json")
	cfg := &Config{}

	if data, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(data, cfg)
	}

	if cfg.ID == "" {
		cfg.ID = generateNumericID()
		saveConfig(cfg)
	}
	if *agentIDFlag != "" {
		cfg.ID = *agentIDFlag
	}
	if *password != "" {
		cfg.Password = hashPassword(*password)
		saveConfig(cfg)
	}
	if *alias != "" {
		cfg.Alias = *alias
		saveConfig(cfg)
	}

	return cfg
}

func saveConfig(cfg *Config) {
	home, _ := os.UserHomeDir()
	cfgDir := filepath.Join(home, ".atlas-desk")
	os.MkdirAll(cfgDir, 0700)
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600)
}

func generateNumericID() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900_000_000))
	if err != nil {
		panic(err)
	}
	id := fmt.Sprintf("%09d", n.Int64()+100_000_000)
	log.Printf("🆔 Generated new ID: %s", id)
	return id
}

func hashPassword(pass string) string {
	h := sha256.Sum256([]byte(pass))
	return hex.EncodeToString(h[:])
}

// ── Signaling Messages ───────────────────────────────────────

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

type AuthChallenge struct {
	Token string `json:"token"`
}

type AuthResponse struct {
	Proof string `json:"proof"`
}

type FrameMeta struct {
	W     int   `json:"w"`
	H     int   `json:"h"`
	Frame uint64 `json:"f"`
}

// ── Main ───────────────────────────────────────────────────────

func main() {
	flag.Parse()

	cfg := loadConfig()

	log.Printf("◆ Atlas Desk Agent v0.7  ID: %s", cfg.ID)
	if cfg.Alias != "" {
		log.Printf("   Alias: %s", cfg.Alias)
	}
	if cfg.Password != "" {
		log.Printf("🔒 Password: set")
	}

	// Connect to signaling
	conn, _, err := websocket.DefaultDialer.Dial(*signalURL, nil)
	if err != nil {
		log.Fatalf("Signal error: %v", err)
	}
	defer conn.Close()

	regPayload := map[string]string{"id": cfg.ID, "role": "agent"}
	if cfg.Alias != "" {
		regPayload["alias"] = cfg.Alias
	}
	if cfg.Password != "" {
		regPayload["has_password"] = "true"
	}

	sendSignal(conn, SignalMsg{
		Type:    "register",
		Payload: mustJSON(regPayload),
	})
	log.Printf("Registered to signaling server")

	bounds := screenshot.GetDisplayBounds(0)
	log.Printf("Display: %dx%d @ %d FPS  JPEG Q=%d", bounds.Dx(), bounds.Dy(), *fps, *jpegQuality)

	// Build ICE servers list
	iceServers := []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
	}
	for _, ts := range cfg.TurnServers {
		iceServers = append(iceServers, webrtc.ICEServer{
			URLs:       ts.URLs,
			Username:   ts.Username,
			Credential: ts.Credential,
		})
	}
	if len(cfg.TurnServers) > 0 {
		log.Printf("🔄 TURN: %d server(s) configured", len(cfg.TurnServers))
	}

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
			go handleSession(conn, msg.From, cfg, bounds, iceServers)
		case "pong":
		}
	}
}

// ── P2P Session ────────────────────────────────────────────────

func handleSession(conn *websocket.Conn, clientID string, cfg *Config, bounds image.Rectangle, iceServers []webrtc.ICEServer) {
	log.Printf("🔗 Client: %s — establishing P2P", clientID)

	// Password challenge if configured
	if cfg.Password != "" {
		token := fmt.Sprintf("%x-%d", sha256.Sum256([]byte(fmt.Sprintf("%s-%d", clientID, time.Now().UnixNano()))), time.Now().Unix())
		sendSignal(conn, SignalMsg{
			Type:   "auth_challenge",
			Target: clientID,
			Payload: mustJSON(AuthChallenge{Token: token}),
		})
		log.Printf("🔒 Sent auth challenge to %s", clientID)

		// Wait for auth response
		var authOk bool
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg SignalMsg
			json.Unmarshal(raw, &msg)
			if msg.Type == "auth_response" && msg.From == clientID {
				var resp AuthResponse
				json.Unmarshal(msg.Payload, &resp)
				expectedHash := hashPassword(cfg.Password + token)
				// Proof = SHA256(stored_hash + token) sent from client
				if resp.Proof == expectedHash {
					authOk = true
					log.Printf("✅ Auth OK from %s", clientID)
				} else {
					log.Printf("❌ Auth FAILED from %s", clientID)
					sendSignal(conn, SignalMsg{
						Type:   "auth_failed",
						Target: clientID,
						Payload: mustJSON(map[string]string{"reason": "wrong_password"}),
					})
				}
				break
			}
		}
		if !authOk {
			return
		}
	}

	config := webrtc.Configuration{ICEServers: iceServers}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Printf("PC error: %v", err)
		return
	}
	defer pc.Close()

	// Data channels
	inputReady := make(chan struct{})

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
			dc.OnOpen(func() { /* kept for compatibility */ })
		case "files":
			dc.OnOpen(func() {})
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				handleFileReceive(msg.Data)
			})
		case "clipboard":
			dc.OnOpen(func() {})
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				handleClipboardReceive(msg.Data)
			})
		}
	})

	// Create screen channel (agent → client)
	screenDC, err := pc.CreateDataChannel("screen", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("screen DC error: %v", err)
		return
	}
	var gotScreen *webrtc.DataChannel
	screenDC.OnOpen(func() { gotScreen = screenDC })

	// Create files channel
	fileDC, err := pc.CreateDataChannel("files", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("files DC error: %v", err)
		return
	}
	fileDC.OnOpen(func() {})

	// Create clipboard channel (agent → client for clipboard sync)
	clipDC, err := pc.CreateDataChannel("clipboard", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("clipboard DC error: %v", err)
		return
	}
	var gotClipboard *webrtc.DataChannel
	clipDC.OnOpen(func() { gotClipboard = clipDC })

	// ICE relay
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

	// Create offer
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

	// Receive answer + ICE
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

	<-inputReady
	log.Printf("✅ P2P connected — streaming %dx%d @ %d FPS", bounds.Dx(), bounds.Dy(), *fps)

	// Clipboard polling (every 500ms, only send on change)
	cb := newClipboard()
	lastClip := ""
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			text := cb.GetText()
			if text != "" && text != lastClip && gotClipboard != nil && gotClipboard.ReadyState() == webrtc.DataChannelStateOpen {
				msg, _ := json.Marshal(map[string]string{"type": "clipboard", "text": text})
				gotClipboard.Send(msg)
				lastClip = text
			}
		}
	}()

	// Frame capture loop with diff encoding
	ticker := time.NewTicker(time.Second / time.Duration(*fps))
	defer ticker.Stop()

	encoder := NewDiffEncoder(*jpegQuality)
	var frameCount uint64

	for range ticker.C {
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			continue
		}

		pkt := encoder.EncodeDiff(img, bounds, frameCount)

		if gotScreen != nil && gotScreen.ReadyState() == webrtc.DataChannelStateOpen {
			gotScreen.Send(pkt)
		}
		frameCount++
	}
}

// ── File Receive ────────────────────────────────────────────

var activeReceives = make(map[string]*os.File)

func handleFileReceive(data []byte) {
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
		if f, ok := activeReceives[fileID]; ok {
			if offsetF, ok := meta["offset"].(float64); ok {
				offset := int64(offsetF)
				f.WriteAt(payload, offset)
			} else {
				f.Write(payload)
			}
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

// ── Clipboard Receive ──────────────────────────────────────

func handleClipboardReceive(data []byte) {
	var msg map[string]string
	json.Unmarshal(data, &msg)
	if msg["type"] == "clipboard" && msg["text"] != "" {
		cb := newClipboard()
		cb.SetText(msg["text"])
		log.Printf("📋 Clipboard synced: %d chars", len(msg["text"]))
	}
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
