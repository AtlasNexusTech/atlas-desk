# ◆ Atlas Desk — Open Source Remote Desktop

Lightweight AnyDesk alternative. Binary agent on the host PC, web-based viewer (zero install).

## Architecture

```
┌─────────────┐     WebSocket      ┌──────────────┐     WebSocket      ┌──────────────┐
│  Agent (Go) │ ◄──────────────► │  Signaling    │ ◄──────────────► │  Client (Web)│
│  Screen cap  │                   │  Server (Go)  │                   │  Browser      │
│  Input sim   │                   │  Port :8800    │                   │  HTML5        │
└─────────────┘                   └──────────────┘                   └──────────────┘
```

## Quick Start

```bash
# 1. Start signaling server (any machine)
cd signaling && go run main.go
# → Listening on :8800

# 2. Start agent on target PC
# Linux:
cd agent && go run main.go -signal ws://SIGNAL_IP:8800/ws
# Windows:
cd agent && go build -o atlas-desk-agent.exe . && atlas-desk-agent.exe -signal ws://SIGNAL_IP:8800/ws

# 3. Open client in browser
open client/index.html
# Enter Agent ID (hostname), click Connect
```

## Build

```bash
# Signaling server (Linux/Windows/Mac)
cd signaling && go build -o atlas-desk-signaling .

# Agent — Linux (requires xdotool: apt install xdotool)
cd agent && go build -o atlas-desk-agent .

# Agent — Windows (native WinAPI, zero deps)
cd agent && GOOS=windows go build -o atlas-desk-agent.exe .
```

## Protocol

| Message | Direction | Purpose |
|---------|-----------|---------|
| `register` | Both → Signal | Register as agent or client |
| `client_hello` | Signal → Agent | Viewer wants to connect |
| `frame` | Agent → Client | PNG screen frame |
| `input` | Client → Agent | Mouse/keyboard events |

## Roadmap

- [x] Signaling server (Go)
- [x] Agent — Linux (xdotool)
- [x] Agent — Windows (WinAPI)
- [x] Web client (HTML5)
- [ ] WebRTC P2P (replace WebSocket relay)
- [ ] H.264 hardware encoding
- [ ] File transfer
- [ ] Clipboard sync
- [ ] NAT traversal (STUN/TURN)
- [ ] Authentication

## License

MIT — Atlas Nexus Tech
