// Atlas Desk Agent v0.2 — WebRTC P2P streaming + cross-platform input

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

var (
	signalURL = flag.String("signal", "ws://localhost:8800/ws", "Signaling server URL")
	agentID   = flag.String("id", "", "Agent ID")
	fps       = flag.Int("fps", 15, "Capture framerate")
)

type SignalingMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Target  string          `json:"target,omitempty"`
	From    string          `json:"from,omitempty"`
}

type SDPMessage struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type ICEMessage struct {
	Candidate string `json:"candidate"`
	SDPMid    string `json:"sdpMid"`
}

func main() {
	flag.Parse()
	if *agentID == "" {
		host, _ := os.Hostname()
		*agentID = host
	}

	log.Printf("◆ Atlas Desk Agent v0.2 [%s]", *agentID)

	// Connect signaling
	conn, _, err := websocket.DefaultDialer.Dial(*signalURL, nil)
	if err != nil {
		log.Fatalf("Signal error: %v", err)
	}
	defer conn.Close()

	sendSignal(conn, SignalingMessage{
		Type:    "register",
		Payload: mustJSON(map[string]string{"id": *agentID, "role": "agent"}),
	})
	log.Printf("Registered: %s", *agentID)

	bounds := screenshot.GetDisplayBounds(0)
	log.Printf("Display: %dx%d", bounds.Dx(), bounds.Dy())

	// Wait for client connection, then set up WebRTC
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Fatal("Signal disconnected:", err)
		}
		var msg SignalingMessage
		json.Unmarshal(raw, &msg)

		switch msg.Type {
		case "client_hello":
			go handleClient(conn, msg.From, bounds)
		case "pong":
		}
	}
}

func handleClient(conn *websocket.Conn, clientID string, bounds image.Rectangle) {
	log.Printf("🔗 Client: %s — setting up WebRTC", clientID)

	// Create peer connection
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

	// Create video track
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "atlas-desk",
	)
	if err != nil {
		log.Printf("Track error: %v", err)
		return
	}
	if _, err := pc.AddTrack(videoTrack); err != nil {
		log.Printf("AddTrack error: %v", err)
		return
	}

	// Data channel for input
	inputReady := make(chan struct{})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() { close(inputReady) })
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			var input map[string]interface{}
			json.Unmarshal(msg.Data, &input)
			handleInput(input)
		})
	})

	// ICE candidate → signaling
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil { return }
		sendSignal(conn, SignalingMessage{
			Type: "ice_candidate",
			Target: clientID,
			Payload: mustJSON(ICEMessage{Candidate: c.ToJSON().Candidate}),
		})
	})

	// Create offer
	offer, err := pc.CreateOffer(nil)
	if err != nil { return }
	pc.SetLocalDescription(offer)

	sendSignal(conn, SignalingMessage{
		Type: "offer",
		Target: clientID,
		Payload: mustJSON(SDPMessage{SDP: offer.SDP, Type: "offer"}),
	})

	// Receive answer
	answerCh := make(chan webrtc.SessionDescription)
	iceCh := make(chan webrtc.ICECandidateInit)

	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil { return }
			var msg SignalingMessage
			json.Unmarshal(raw, &msg)
			switch msg.Type {
			case "answer":
				var sdp SDPMessage
				json.Unmarshal(msg.Payload, &sdp)
				answerCh <- webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp.SDP}
			case "ice_candidate":
				var ice ICEMessage
				json.Unmarshal(msg.Payload, &ice)
				iceCh <- webrtc.ICECandidateInit{Candidate: ice.Candidate}
			}
		}
	}()

	select {
	case answer := <-answerCh:
		pc.SetRemoteDescription(answer)
	}

	// Process ICE candidates
	go func() {
		for ice := range iceCh {
			pc.AddICECandidate(ice)
		}
	}()

	// Wait for data channel
	<-inputReady
	log.Printf("✅ P2P connected — streaming %dx%d @ %d FPS", bounds.Dx(), bounds.Dy(), *fps)

	// Screen capture → video track
	ticker := time.NewTicker(time.Second / time.Duration(*fps))
	defer ticker.Stop()

	for range ticker.C {
		img, err := screenshot.CaptureRect(bounds)
		if err != nil { continue }

		var buf bytes.Buffer
		png.Encode(&buf, img)
		videoTrack.WriteSample(media.Sample{
			Data:     buf.Bytes(),
			Duration: time.Second / time.Duration(*fps),
		})
	}
}

func sendSignal(conn *websocket.Conn, msg SignalingMessage) {
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

func mustJSON(v interface{}) json.RawMessage {
	d, _ := json.Marshal(v)
	return d
}
