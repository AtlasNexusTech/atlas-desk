# ◆ Atlas Desk — Open-Source Remote Desktop (AnyDesk Alternative)

**P2P remote desktop with WebRTC.** No servers routing your video. Free. Open source.

## v0.3 — JPEG Streaming + Numeric IDs + File Transfer

| Feature | Status |
|---------|--------|
| P2P WebRTC (STUN) | ✅ |
| JPEG screen streaming | ✅ 15 FPS, Q65 |
| Mouse/keyboard control | ✅ Cross-platform |
| Numeric 9-digit IDs | ✅ AnyDesk-style |
| File transfer | ✅ Bidirectional |
| Clipboard sync | ❌ |
| TURN server (NAT fallback) | ❌ |
| Connection password | ❌ |
| Encrypted sessions | ✅ WebRTC DTLS |

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

## Data Channels

| Channel | Direction | Protocol |
|---------|-----------|----------|
| `screen` | Agent → Client | [4B metaLen][JSON meta{w,h,f}][4B jpgLen][JPEG] |
| `input` | Client → Agent | JSON: `{action, x, y, key, button, dy}` |
| `files` | Bidirectional | [4B metaLen][JSON meta{type,name,size,id,offset}][data] |

## Roadmap

- [ ] TURN server for restrictive NATs
- [ ] H.264 hardware encoding (lower bandwidth)
- [ ] Clipboard sync
- [ ] Connection password
- [ ] Desktop app (Electron/Tauri)
- [ ] Mobile client

## License

MIT — Atlas Nexus Tech (2026)
