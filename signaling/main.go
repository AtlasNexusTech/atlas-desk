// NexDesk Signaling Server — matches agents to clients, relays WebRTC SDP/ICE
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Peer struct {
	ID   string
	Role string // "agent" or "client"
	Conn *websocket.Conn
	mu   sync.Mutex
}

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Target  string          `json:"target,omitempty"`
	From    string          `json:"from,omitempty"`
}

type RegisterPayload struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

var (
	agents  = sync.Map{} // id -> *Peer
	clients = sync.Map{} // id -> *Peer
)

func main() {
	http.HandleFunc("/ws", handleWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	port := ":8800"
	log.Printf("NexDesk signaling server on %s", port)
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
			peer = &Peer{ID: p.ID, Role: p.Role, Conn: conn}
			if p.Role == "agent" {
				agents.Store(p.ID, peer)
				log.Printf("agent registered: %s", p.ID)
			} else {
				clients.Store(p.ID, peer)
				log.Printf("client registered: %s", p.ID)
			}

			// Notify agent that a client wants to connect
			if p.Role == "client" && msg.Target != "" {
				if target, ok := agents.Load(msg.Target); ok {
					notify := Message{Type: "client_hello", From: p.ID}
					sendJSON(target.(*Peer), notify)
				}
			}

		case "offer", "answer", "ice_candidate":
			// Relay to target peer
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
			log.Printf("agent disconnected: %s", peer.ID)
		} else {
			clients.Delete(peer.ID)
			log.Printf("client disconnected: %s", peer.ID)
		}
	}
}

func relayTo(targetID string, msg Message) {
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
