// Atlas Desk Agent v0.9 — H.264 + multi-monitor + single-reader dispatch + password auth
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
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	h264Mode    = flag.Bool("h264", true, "Use H.264 hardware encoding (fallback: JPEG diff)")
	displayIdx  = flag.Int("display", 0, "Display index to capture (0=primary)")
	captureAll  = flag.Bool("all", false, "Capture all displays as one virtual desktop")

	sendMu sync.Mutex // protects conn.WriteMessage (one writer gorilla constraint)
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

// ── Network Detection ────────────────────────────────────────

func detectLocalIPs() []string {
	// Priority: real LAN/WiFi IPs, skip VPNs, Docker, WSL, virtual adapters
	interfaces, _ := net.Interfaces()
	var ips []string
	for _, iface := range interfaces {
		// Skip loopback, down, virtual adapters
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.String()
			// Skip VPNs (Proton, Tailscale), Docker bridges, WSL, APIPA
			if strings.HasPrefix(ip, "10.2.") || strings.HasPrefix(ip, "172.") ||
				strings.HasPrefix(ip, "169.254.") || strings.HasPrefix(ip, "100.") {
				continue
			}
			// Accept: 10.158.x.x (WiFi), 192.168.x.x (LAN)
			if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

// ── Main ───────────────────────────────────────────────────────

func main() {
	flag.Parse()

	cfg := loadConfig()

	log.Printf("◆ Atlas Desk Agent v0.9  ID: %s", cfg.ID)
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

	// Display selection
	ndisplays := screenshot.NumActiveDisplays()
	bounds := getCaptureBounds(*displayIdx, *captureAll, ndisplays)
	log.Printf("Display: %dx%d @ %d FPS  JPEG Q=%d  (%d display(s) available)",
		bounds.Dx(), bounds.Dy(), *fps, *jpegQuality, ndisplays)
	if *captureAll {
		log.Printf("   Multi-monitor: composited %d display(s) into %dx%d", ndisplays, bounds.Dx(), bounds.Dy())
	} else {
		log.Printf("   Capturing display %d/%d", *displayIdx, ndisplays)
	}

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

	// Single reader: dispatch messages to active session channels
	sessions := sync.Map{} // clientID → chan SignalMsg
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Fatal("Signal disconnected:", err)
		}
		var msg SignalMsg
		json.Unmarshal(raw, &msg)

		switch msg.Type {
		case "client_hello":
			ch := make(chan SignalMsg, 32)
			sessions.Store(msg.From, ch)
			go handleSession(conn, msg.From, cfg, bounds, iceServers, ndisplays, ch)
		case "auth_response", "answer", "ice_candidate":
			if ch, ok := sessions.Load(msg.From); ok {
				select {
				case ch.(chan SignalMsg) <- msg:
				default:
					log.Printf("⚠ session channel full for %s, dropping %s", msg.From, msg.Type)
				}
			}
		case "pong":
		}
	}
}

// ── P2P Session ────────────────────────────────────────────────

func handleSession(conn *websocket.Conn, clientID string, cfg *Config, bounds image.Rectangle, iceServers []webrtc.ICEServer, ndisplays int, recvCh <-chan SignalMsg) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("💥 PANIC in session %s: %v", clientID, r)
		}
	}()
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

		// Wait for auth response (via shared recvCh, not conn.ReadMessage)
		var authOk bool
		for msg := range recvCh {
			if msg.Type == "auth_response" && msg.From == clientID {
				var resp AuthResponse
				json.Unmarshal(msg.Payload, &resp)
				expectedHash := hashPassword(cfg.Password + token)
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

	// Use SettingEngine to force the correct network interface. ICE-TCP needs a
	// real listener: passing nil to NewICETCPMux compiles but breaks candidate
	// gathering at runtime, so keep TCP optional and fall back to UDP-only if the
	// listener cannot be created.
	s := webrtc.SettingEngine{}
	networkTypes := []webrtc.NetworkType{webrtc.NetworkTypeUDP4}
	if tcpListener, err := net.Listen("tcp4", ":0"); err == nil {
		defer tcpListener.Close()
		s.SetICETCPMux(webrtc.NewICETCPMux(nil, tcpListener, 8))
		networkTypes = append(networkTypes, webrtc.NetworkTypeTCP4)
	} else {
		log.Printf("⚠ ICE-TCP disabled: %v", err)
	}
	s.SetNetworkTypes(networkTypes)
	// Detect the real WiFi/LAN IP (skip VPNs, Docker, WSL virtual adapters)
	detectIPs := detectLocalIPs()
	if len(detectIPs) > 0 {
		s.SetNAT1To1IPs(detectIPs, webrtc.ICECandidateTypeHost)
		log.Printf("🌐 ICE bound to: %v", detectIPs)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	config := webrtc.Configuration{ICEServers: iceServers}
	pc, err := api.NewPeerConnection(config)
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

	// Create screen channel (agent → client) — JPEG diff fallback
	screenDC, err := pc.CreateDataChannel("screen", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("screen DC error: %v", err)
		return
	}
	var gotScreen *webrtc.DataChannel
	screenDC.OnOpen(func() { gotScreen = screenDC })

	// H.264 video track (primary streaming path)
	var videoTrack *webrtc.TrackLocalStaticSample
	var h264enc *H264Encoder
	if *h264Mode {
		videoTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
			"video", "atlas-desk",
		)
		if err != nil {
			log.Printf("⚠ H.264 track error: %v — falling back to JPEG", err)
			*h264Mode = false
		} else {
			if _, err := pc.AddTrack(videoTrack); err != nil {
				log.Printf("⚠ H.264 addTrack error: %v — falling back to JPEG", err)
				*h264Mode = false
			} else {
				h264enc, err = NewH264Encoder(bounds.Dx(), bounds.Dy(), *fps)
				if err != nil {
					log.Printf("⚠ H.264 encoder init error: %v — falling back to JPEG", err)
					*h264Mode = false
				}
			}
		}
	}
	if *h264Mode {
		log.Printf("🎥 H.264 video track active (%dx%d @ %d FPS)", bounds.Dx(), bounds.Dy(), *fps)
	} else {
		log.Printf("🖼 JPEG diff fallback active (Q=%d)", *jpegQuality)
	}

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

	// Create terminal channel (bidirectional: client input → PTY, PTY output → client)
	termDCNew, err := pc.CreateDataChannel("terminal", &webrtc.DataChannelInit{
		Ordered: func(b bool) *bool { return &b }(true),
	})
	if err != nil {
		log.Printf("terminal DC error: %v", err)
		return
	}
	termDCNew.OnOpen(func() {
		log.Println("🖥 Terminal channel open (created by agent)")
		termDC = termDCNew
		_ = spawnTerminal(termDCNew)
	})
	termDCNew.OnMessage(func(msg webrtc.DataChannelMessage) {
		handleTerminalMessage(msg)
	})

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

	// Receive answer + ICE from shared recvCh (single-reader dispatch)
	answerCh := make(chan webrtc.SessionDescription)
	iceCh := make(chan webrtc.ICECandidateInit)

	go func() {
		for msg := range recvCh {
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

	// Frame capture loop
	ticker := time.NewTicker(time.Second / time.Duration(*fps))
	defer ticker.Stop()

	// Start H.264 streaming goroutine (reads from FFmpeg stdout → RTP)
	var stopStream chan struct{}
	if *h264Mode && h264enc != nil && videoTrack != nil {
		stopStream = make(chan struct{})
		go h264enc.StreamToTrack(videoTrack, stopStream)
		defer func() { close(stopStream); h264enc.Close() }()
	}

	encoder := NewDiffEncoder(*jpegQuality)
	var frameCount uint64

	for range ticker.C {
		var img image.Image
		var err error

		// Multi-monitor compositing or single display capture
		if *captureAll && ndisplays > 1 {
			img, err = captureAllDisplays(bounds)
		} else {
			img, err = screenshot.CaptureRect(bounds)
		}
		if err != nil {
			continue
		}

		// H.264 path: send raw frame to FFmpeg encoder (async pipe write)
		if *h264Mode && h264enc != nil {
			if err := h264enc.Encode(img); err != nil {
				log.Printf("H.264 encode error: %v", err)
			}
		}

		// JPEG diff fallback (always sent for clients without H.264 support)
		pkt := encoder.EncodeDiff(img, bounds, frameCount)
		if gotScreen != nil && gotScreen.ReadyState() == webrtc.DataChannelStateOpen {
			gotScreen.Send(pkt)
		}
		frameCount++
	}
}

// ── File Receive ────────────────────────────────────────────

var activeReceives = make(map[string]*os.File)
var activeReceivesMu sync.Mutex

func handleFileReceive(data []byte) {
	if len(data) == 0 {
		return
	}

	// Browser DataChannels send small control messages (file_start/file_end)
	// as UTF-8 JSON strings, while file chunks use the binary packet format:
	// [4B metaLen][JSON meta][payload]. Support both shapes so file transfer is
	// actually bidirectional instead of silently ignoring browser control frames.
	if data[0] == '{' {
		var meta map[string]interface{}
		if err := json.Unmarshal(data, &meta); err != nil {
			return
		}
		handleFileMeta(meta, nil)
		return
	}

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
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		log.Printf("File receive error: invalid metadata: %v", err)
		return
	}
	handleFileMeta(meta, payload)
}

func handleFileMeta(meta map[string]interface{}, payload []byte) {
	msgType, _ := meta["type"].(string)
	switch msgType {
	case "file_start":
		name, _ := meta["name"].(string)
		name = filepath.Base(name)
		if name == "." || name == string(filepath.Separator) || name == "" {
			log.Printf("File receive error: invalid filename")
			return
		}
		sizeF, _ := meta["size"].(float64)
		fileID, _ := meta["id"].(string)
		size := int64(sizeF)
		log.Printf("📥 Receiving: %s (%d bytes)", name, size)

		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			log.Printf("Home directory error: %v", err)
			return
		}
		dir := filepath.Join(home, "Downloads", "AtlasDesk")
		os.MkdirAll(dir, 0755)
		outPath := filepath.Join(dir, name)
		f, err := os.Create(outPath)
		if err != nil {
			log.Printf("File create error: %v", err)
			return
		}
		activeReceivesMu.Lock()
		activeReceives[fileID] = f
		activeReceivesMu.Unlock()

	case "file_chunk":
		if len(payload) == 0 {
			return
		}
		fileID, _ := meta["id"].(string)
		activeReceivesMu.Lock()
		f, ok := activeReceives[fileID]
		activeReceivesMu.Unlock()
		if ok {
			if offsetF, ok := meta["offset"].(float64); ok {
				offset := int64(offsetF)
				f.WriteAt(payload, offset)
			} else {
				f.Write(payload)
			}
		}

	case "file_end":
		fileID, _ := meta["id"].(string)
		activeReceivesMu.Lock()
		f, ok := activeReceives[fileID]
		if ok {
			delete(activeReceives, fileID)
		}
		activeReceivesMu.Unlock()
		if ok {
			f.Close()
			log.Printf("✅ Received: %s → ~/Downloads/AtlasDesk/", meta["name"])
		}

	case "file_error":
		fileID, _ := meta["id"].(string)
		activeReceivesMu.Lock()
		f, ok := activeReceives[fileID]
		if ok {
			delete(activeReceives, fileID)
		}
		activeReceivesMu.Unlock()
		if ok {
			f.Close()
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

// ── Multi-Monitor ──────────────────────────────────────────

// getCaptureBounds returns the rectangle to capture based on display flags.
func getCaptureBounds(idx int, all bool, ndisplays int) image.Rectangle {
	if all && ndisplays > 1 {
		// Compute bounding box of all displays
		minX, minY := 0, 0
		maxX, maxY := 0, 0
		for i := 0; i < ndisplays; i++ {
			b := screenshot.GetDisplayBounds(i)
			if b.Min.X < minX {
				minX = b.Min.X
			}
			if b.Min.Y < minY {
				minY = b.Min.Y
			}
			if b.Max.X > maxX {
				maxX = b.Max.X
			}
			if b.Max.Y > maxY {
				maxY = b.Max.Y
			}
		}
		return image.Rect(minX, minY, maxX, maxY)
	}
	// Single display
	if idx >= 0 && idx < ndisplays {
		return screenshot.GetDisplayBounds(idx)
	}
	return screenshot.GetDisplayBounds(0)
}

// captureAllDisplays captures all active displays composited into one image.
func captureAllDisplays(bounds image.Rectangle) (image.Image, error) {
	// Create composite canvas
	composite := image.NewRGBA(bounds)
	ndisplays := screenshot.NumActiveDisplays()
	for i := 0; i < ndisplays; i++ {
		img, err := screenshot.CaptureDisplay(i)
		if err != nil {
			continue
		}
		db := screenshot.GetDisplayBounds(i)
		// Draw this display into the composite at its offset position
		drawImageAt(composite, img, db.Min.X-bounds.Min.X, db.Min.Y-bounds.Min.Y)
	}
	return composite, nil
}

// drawImageAt copies src into dst at the given offset.
func drawImageAt(dst *image.RGBA, src image.Image, offsetX, offsetY int) {
	srcBounds := src.Bounds()
	for y := 0; y < srcBounds.Dy(); y++ {
		for x := 0; x < srcBounds.Dx(); x++ {
			dst.Set(offsetX+x, offsetY+y, src.At(srcBounds.Min.X+x, srcBounds.Min.Y+y))
		}
	}
}

// ── Helpers ─────────────────────────────────────────────────

func sendSignal(conn *websocket.Conn, msg SignalMsg) {
	sendMu.Lock()
	defer sendMu.Unlock()
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

func mustJSON(v interface{}) json.RawMessage {
	d, _ := json.Marshal(v)
	return d
}
