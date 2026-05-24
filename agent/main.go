// Atlas Desk Agent — cross-platform screen capture + WebRTC streaming + input
// Build: go build -o atlas-desk-agent .
// Linux: requires xdotool for input simulation
// Windows: requires robotgo (CGO) — build with: GOOS=windows CGO_ENABLED=1 go build

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
)

var (
	signalURL = flag.String("signal", "ws://localhost:8800/ws", "Signaling server URL")
	agentID   = flag.String("id", "", "Agent ID (default: hostname)")
	fps       = flag.Int("fps", 15, "Capture framerate")
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Target  string          `json:"target,omitempty"`
	From    string          `json:"from,omitempty"`
}

func main() {
	flag.Parse()

	if *agentID == "" {
		host, _ := os.Hostname()
		*agentID = host
	}

	log.Printf("═══ Atlas Desk Agent v0.1 ═══")
	log.Printf("ID: %s | OS: %s | Arch: %s", *agentID, runtime.GOOS, runtime.GOARCH)
	log.Printf("Signal: %s | FPS: %d", *signalURL, *fps)

	conn, _, err := websocket.DefaultDialer.Dial(*signalURL, nil)
	if err != nil {
		log.Fatalf("❌ Cannot connect to signaling: %v", err)
	}
	defer conn.Close()

	reg := map[string]string{"id": *agentID, "role": "agent"}
	sendJSON(conn, Message{Type: "register", Payload: mustJSON(reg)})
	log.Printf("✅ Registered as agent: %s", *agentID)

	// Keepalive
	go func() {
		tick := time.NewTicker(15 * time.Second)
		for range tick.C {
			sendJSON(conn, Message{Type: "ping"})
		}
	}()

	// Auto-detect display
	nDisplays := screenshot.NumActiveDisplays()
	log.Printf("🖥️  %d display(s) detected", nDisplays)
	bounds := screenshot.GetDisplayBounds(0)
	log.Printf("   Primary: %dx%d", bounds.Dx(), bounds.Dy())

	clientConnected := false
	imgCh := make(chan []byte, 5)

	// Capture goroutine
	go func() {
		interval := time.Second / time.Duration(*fps)
		tick := time.NewTicker(interval)
		for range tick.C {
			if !clientConnected {
				continue
			}
			img, err := screenshot.CaptureRect(bounds)
			if err != nil {
				continue
			}
			var buf bytes.Buffer
			png.Encode(&buf, img)
			frame := map[string]interface{}{
				"width":  bounds.Dx(),
				"height": bounds.Dy(),
				"format": "png",
				"data":   buf.Bytes(),
			}
			data, _ := json.Marshal(Message{
				Type:    "frame",
				Payload: mustJSON(frame),
			})
			select {
			case imgCh <- data:
			default:
			}
		}
	}()

	// Send goroutine
	go func() {
		for data := range imgCh {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}()

	// Read loop
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Disconnected: %v", err)
			break
		}
		var msg Message
		json.Unmarshal(raw, &msg)

		switch msg.Type {
		case "client_hello":
			clientConnected = true
			log.Printf("🔗 Client connected: %s", msg.From)

		case "input":
			var input map[string]interface{}
			json.Unmarshal(msg.Payload, &input)
			handleInput(input)

		case "pong":
			// alive
		}
	}
}

func handleInput(input map[string]interface{}) {
	action, _ := input["action"].(string)

	switch action {
	case "mouse_move":
		x, _ := input["x"].(float64)
		y, _ := input["y"].(float64)
		moveMouse(int(x), int(y))

	case "mouse_click":
		btn, _ := input["button"].(string)
		clickMouse(btn)

	case "key":
		key, _ := input["key"].(string)
		pressKey(key)

	case "type":
		text, _ := input["text"].(string)
		typeText(text)

	case "scroll":
		dy, _ := input["dy"].(float64)
		scrollMouse(int(dy))
	}
}

// Platform-specific input implementations
func moveMouse(x, y int) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdotool", "mousemove", fmt.Sprint(x), fmt.Sprint(y)).Run()
	case "windows":
		// robotgo.Move(x, y) — requires CGO
	case "darwin":
		// CGO required
	}
}

func clickMouse(btn string) {
	b := "1"
	if btn == "right" {
		b = "3"
	}
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdotool", "click", b).Run()
	}
}

func pressKey(key string) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdotool", "key", key).Run()
	}
}

func typeText(text string) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdotool", "type", text).Run()
	}
}

func scrollMouse(dy int) {
	btn := "4" // up
	if dy < 0 {
		btn = "5"          // down
		dy = -dy
	}
	for i := 0; i < dy; i++ {
		switch runtime.GOOS {
		case "linux":
			exec.Command("xdotool", "click", btn).Run()
		}
	}
}

func sendJSON(conn *websocket.Conn, msg Message) {
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func init() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		os.Exit(0)
	}()
}
