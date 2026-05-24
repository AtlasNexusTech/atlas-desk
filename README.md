# NexDesk — Open Source Remote Desktop

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
# 1. Start signaling server
cd signaling && go run main.go

# 2. Start agent on target PC (Linux with xdotool)
cd agent && go run main.go -signal ws://SIGNAL_IP:8800/ws

# 3. Open client in browser
open client/index.html  # Enter Agent ID, click Connect
```

## Build

```bash
# Signaling server
cd signaling && go build -o nexdesk-signaling .

# Agent (Linux — requires xdotool)
cd agent && go build -o nexdesk-agent .

# Agent (Windows — requires CGO + MinGW)
cd agent && GOOS=windows CGO_ENABLED=1 go build -o nexdesk-agent.exe .
```

## Protocol

| Message | Direction | Purpose |
|---------|-----------|---------|
| `register` | Both → Signal | Register as agent or client |
| `client_hello` | Signal → Agent | Viewer wants to connect |
| `frame` | Agent → Client | PNG screen frame |
| `input` | Client → Agent | Mouse/keyboard events |

## License

MIT — Atlas Nexus Tech
