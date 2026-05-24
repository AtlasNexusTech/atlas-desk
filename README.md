# ◆ Atlas Desk — Open-Source Remote Desktop (AnyDesk Alternative)

**P2P remote desktop with WebRTC.** No servers routing your video. Free. Open source.

## v0.7 — Tray Icon + Auto-Reconnect + Settings

| Feature | Status |
|---------|--------|
| P2P WebRTC (STUN) | ✅ |
| TURN server (NAT fallback) | ✅ Configurable |
| JPEG screen streaming | ✅ 15 FPS, Q65 |
| Mouse/keyboard control | ✅ Cross-platform |
| Numeric 9-digit IDs | ✅ AnyDesk-style |
| Connection aliases | ✅ "PC Bureau" instead of "437 192 805" |
| File transfer | ✅ Bidirectional |
| Connection password | ✅ SHA-256 challenge-response |
| Clipboard sync | ✅ Auto-sync both directions |
| Desktop app (Tauri) | ✅ Tray icon, minimize to tray |
| Auto-reconnect | ✅ On connection loss |
| Settings panel | ✅ Signal server URL |
| Encrypted sessions | ✅ WebRTC DTLS |
| Mobile client | ❌ Future |

## Architecture

```
┌──────────────┐         WebRTC P2P         ┌──────────────┐
│   Agent      │◄──────────────────────────►│   Client      │
│  (Go binary) │   screen: JPEG frames      │  (Web browser) │
│              │   input: mouse/keyboard    │               │
│  Linux/Win   │   files: file transfer     │  Zero install  │
└──────┬───────┘                            └───────────────┘
       │ WebSocket (signaling only)
       ▼
┌──────────────┐
│  Signaling   │  Go + Gorilla WebSocket
│   Server     │  Relays SDP/ICE only
└──────────────┘
```

## Quick Start

### 1. Signaling Server
```bash
cd signaling
go run main.go -addr :8800
```

### 2. Agent (remote PC)
```bash
cd agent
go run . -signal ws://YOUR_SIGNAL_IP:8800/ws
# Shows 9-digit ID on first run
```

### 3. Client (your PC)
Open `client/index.html` in a browser, enter the agent's 9-digit ID, click Connect.

### TURN Server (optional but recommended)
Edit `~/.atlas-desk/config.json` on the agent:
```json
{
  "id": "437192805",
  "password": "sha256hash...",
  "turn_servers": [
    {
      "urls": ["turn:your-turn-server.com:3478?transport=udp"],
      "username": "your-username",
      "credential": "your-credential"
    }
  ]
}
```
Set password via CLI: `./atlas-desk-agent -pass "mysecret"`

## Data Channels

| Channel | Direction | Protocol |
|---------|-----------|----------|
| `screen` | Agent → Client | [4B metaLen][JSON meta{w,h,f}][4B jpgLen][JPEG] |
| `input` | Client → Agent | JSON: `{action, x, y, key, button, dy}` |
| `files` | Bidirectional | [4B metaLen][JSON meta{type,name,size,id,offset}][data] |

## Roadmap

- [x] TURN server for restrictive NATs
- [x] Connection password
- [x] Clipboard sync
- [x] Desktop app (Tauri via CI)
- [x] Connection aliases (name instead of ID)
- [ ] Mobile client
- [ ] H.264 hardware encoding (lower bandwidth)

## License

MIT — Atlas Nexus Tech (2026)
