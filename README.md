# ◆ Atlas Desk — Open-Source Remote Desktop (AnyDesk Alternative)

**P2P remote desktop with WebRTC.** No servers routing your video. Free. Open source.

## v0.7 — Diff Encoding + CI Pipeline + Auto-Start

| Feature | Status |
|---------|--------|
| P2P WebRTC (STUN) | ✅ |
| TURN server (NAT fallback) | ✅ Configurable |
| Screen streaming (JPEG diff) | ✅ 15 FPS, Q65, 32×32 block diffs |
| Mouse/keyboard control | ✅ Cross-platform |
| Numeric 9-digit IDs | ✅ AnyDesk-style |
| Connection aliases | ✅ "PC Bureau" instead of "437 192 805" |
| File transfer | ✅ Bidirectional |
| Connection password | ✅ SHA-256 challenge-response |
| Clipboard sync | ✅ Auto-sync both directions |
| Remote terminal (PTY shell) | ✅ 💻 Bouton terminal → shell bash/cmd sur le PC cible |
| Desktop app (Tauri) | ✅ Tray icon, minimize to tray |
| Auto-reconnect | ✅ On connection loss |
| Settings panel | ✅ Signal server URL |
| Connection stats | ✅ FPS, bandwidth, frame type |
| Linux auto-start | ✅ systemd service |
| Encrypted sessions | ✅ WebRTC DTLS |
| Mobile client | ❌ Future |

## Architecture

```
┌──────────────┐         WebRTC P2P         ┌──────────────┐
│   Agent      │◄──────────────────────────►│   Client      │
│  (Go binary) │   screen: JPEG diff frames │  (Web browser) │
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

### Auto-Start (Linux systemd)
```bash
# Copy binary
cp agent/atlas-desk-agent ~/.local/bin/

# Install service
mkdir -p ~/.config/systemd/user
cp deploy/atlas-desk-agent.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now atlas-desk-agent

# Check
systemctl --user status atlas-desk-agent
journalctl --user -u atlas-desk-agent -f
```

### Diff Encoding (v0.7)
Screen frames use block-based diff encoding. Only changed 32×32 blocks are transmitted, giving 50-80% bandwidth savings. Full keyframes sent every 30 frames for recovery.

| Channel | Direction | Protocol |
|---------|-----------|----------|
| `screen` | Agent → Client | Full: `[0x00][4B meta][JSON][4B len][JPEG]` — Diff: `[0x01][4B meta][JSON][2B n][blocks...]` |
| `input` | Client → Agent | JSON: `{action, x, y, key, button, dy}` |
| `files` | Bidirectional | [4B metaLen][JSON meta][data] |

## Roadmap

- [x] TURN server for restrictive NATs
- [x] Connection password
- [x] Clipboard sync
- [x] Desktop app (Tauri via CI)
- [x] Connection aliases (name instead of ID)
- [x] Screen diff encoding (50-80% less bandwidth)
- [x] CI release pipeline (Go binaries + signaling)
- [x] Linux auto-start (systemd)
- [ ] Mobile client
- [ ] H.264 hardware encoding (lower bandwidth)

## License

MIT — Atlas Nexus Tech (2026)
