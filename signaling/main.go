// Atlas Desk Signaling Server v0.9 — alias resolution + password auth + embedded client + single-reader
package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Peer struct {
	ID          string
	Role        string // "agent" or "client"
	Alias       string
	HasPassword bool
	Conn        *websocket.Conn
	mu          sync.Mutex
}

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Target  string          `json:"target,omitempty"`
	From    string          `json:"from,omitempty"`
}

type RegisterPayload struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Alias       string `json:"alias,omitempty"`
	HasPassword string `json:"has_password,omitempty"`
}

var (
	agents     = sync.Map{} // id → *Peer
	clients    = sync.Map{} // id → *Peer
	aliasIndex = sync.Map{} // alias → id
)

//go:embed index.html
var clientHTML string

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(clientHTML))
	})
	http.HandleFunc("/ws", handleWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	http.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		list := []map[string]string{}
		agents.Range(func(k, v any) bool {
			p := v.(*Peer)
			entry := map[string]string{"id": p.ID}
			if p.Alias != "" {
				entry["alias"] = p.Alias
			}
			list = append(list, entry)
			return true
		})
		json.NewEncoder(w).Encode(list)
	})

	port := ":8800"
	log.Printf("◆ Atlas Desk signaling v0.9 on %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}
	defer conn.Close()

	var peer *Peer

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "register":
			var p RegisterPayload
			json.Unmarshal(msg.Payload, &p)
			hasPass := p.HasPassword == "true"
			peer = &Peer{ID: p.ID, Role: p.Role, Alias: p.Alias, HasPassword: hasPass, Conn: conn}

			if p.Role == "agent" {
				agents.Store(p.ID, peer)
				if p.Alias != "" {
					aliasIndex.Store(p.Alias, p.ID)
					log.Printf("◆ agent registered: %s (alias: %s)%s", p.ID, p.Alias, passIcon(hasPass))
				} else {
					log.Printf("◆ agent registered: %s%s", p.ID, passIcon(hasPass))
				}
			} else {
				clients.Store(p.ID, peer)
				log.Printf("  client connected: %s", p.ID)
			}

			// Resolve target: try alias first, then numeric ID
			if p.Role == "client" && msg.Target != "" {
				targetID := resolveTarget(msg.Target)
				if target, ok := agents.Load(targetID); ok {
					tp := target.(*Peer)
					notify := Message{Type: "client_hello", From: p.ID}
					sendJSON(tp, notify)
					log.Printf("  ➜ routed: %s → %s", p.ID, targetID)
				} else {
					log.Printf("  ✗ target not found: %s (resolved to %s)", msg.Target, targetID)
					sendJSON(peer, Message{
						Type:    "error",
						Payload: mustJSON(map[string]string{"reason": "target_not_found", "target": msg.Target}),
					})
				}
			}

		case "offer", "answer", "ice_candidate", "auth_challenge", "auth_response", "auth_failed":
			if peer != nil {
				msg.From = peer.ID
			}
			relayTo(msg.Target, msg)

		case "ping":
			sendJSON(peer, Message{Type: "pong"})
		}
	}

	// Cleanup on disconnect
	if peer != nil {
		if peer.Role == "agent" {
			agents.Delete(peer.ID)
			if peer.Alias != "" {
				aliasIndex.Delete(peer.Alias)
			}
			log.Printf("  agent disconnected: %s", peer.ID)
		} else {
			clients.Delete(peer.ID)
			log.Printf("  client disconnected: %s", peer.ID)
		}
	}
}

func resolveTarget(target string) string {
	// If it's a 9-digit number, use as-is
	if len(target) >= 9 {
		if _, err := strconv.Atoi(target); err == nil {
			return target
		}
	}
	// Try alias lookup
	if resolved, ok := aliasIndex.Load(target); ok {
		return resolved.(string)
	}
	// Fallback: try as raw ID
	return target
}

func relayTo(targetID string, msg Message) {
	targetID = resolveTarget(targetID)
	if target, ok := agents.Load(targetID); ok {
		sendJSON(target.(*Peer), msg)
		return
	}
	if target, ok := clients.Load(targetID); ok {
		sendJSON(target.(*Peer), msg)
	}
}

func sendJSON(peer *Peer, msg Message) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	data, _ := json.Marshal(msg)
	peer.Conn.WriteMessage(websocket.TextMessage, data)
}

func passIcon(has bool) string {
	if has {
		return " 🔒"
	}
	return ""
}

func mustJSON(v interface{}) json.RawMessage {
	d, _ := json.Marshal(v)
	return d
}
