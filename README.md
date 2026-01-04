# Moonparty

Multiplayer web streaming for Sunshine game streaming server. Fan out your desktop stream to multiple viewers with gamepad support for up to 4 players.

## Features

- **WebRTC Streaming**: Low-latency video/audio streaming via Pion WebRTC
- **Multi-peer Fan-out**: Single Sunshine stream broadcast to multiple connected clients
- **Multiplayer Input**: Up to 4 players with independent gamepad mapping
- **Host Controls**: First player is host with ability to enable/disable keyboard for other players
- **Spectator Mode**: Additional viewers can watch without controlling
- **Single Page UI**: Clean interface with collapsible control panel
- **Touch Support**: Virtual gamepad for mobile browsers

## Architecture

```
┌─────────────────────┐
│  Sunshine Server    │  (Game PC running Sunshine)
│  - Desktop/Game     │
│  - H.264/H.265      │
│  - Opus Audio       │
└─────────┬───────────┘
          │ Moonlight Protocol
          ▼
┌─────────────────────┐
│  Moonparty Server   │  (This project)
│  - Protocol Client  │
│  - WebRTC Fan-out   │
│  - Input Routing    │
└─────────┬───────────┘
          │ WebRTC (DTLS/SCTP)
          ▼
┌─────────────────────────────────────────────┐
│           Browser Clients                    │
│  ┌─────┐  ┌─────┐  ┌─────┐  ┌─────┐        │
│  │ P1  │  │ P2  │  │ P3  │  │ P4  │ + ...  │
│  │Host │  │Pad1 │  │Pad2 │  │Pad3 │Spectate│
│  └─────┘  └─────┘  └─────┘  └─────┘        │
└─────────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

- [Sunshine](https://github.com/LizardByte/Sunshine) installed and running on your game PC
- Go 1.21+ (for building from source)

### Installation

```bash
# Clone the repository
git clone https://github.com/zalo/moonparty.git
cd moonparty

# Build and run on Linux (connects to local Sunshine)
go build -o moonparty ./cmd/moonparty
./moonparty --host localhost --listen :8080

# Build and run on Windows
go build -o moonparty.exe ./cmd/moonparty
./moonparty.exe --host localhost --listen :8080
```

### Usage

1. Start Moonparty pointing to your Sunshine server:
   ```bash
   ./moonparty --host 192.168.1.100 --listen :8080
   ```

2. Open `http://localhost:8080` in your browser

3. The first user becomes the **Host** (Player 1) and the stream starts automatically

4. Additional users visiting the page join as **Spectators**

5. Spectators can click **"Join Game"** to become active players (up to 4 total)

6. The Host can toggle keyboard/mouse permissions for other players via the control panel

### Command Line Options

```
Usage: moonparty [options]

Options:
  -host string
        Sunshine host address (default "localhost")
  -port int
        Sunshine HTTP port (default 47990)
  -listen string
        Web server listen address (default ":8080")
  -config string
        Path to configuration file (default "config.json")
```

### Configuration File

Create `config.json` for advanced configuration:

```json
{
  "listen_addr": ":8080",
  "sunshine_host": "192.168.1.100",
  "sunshine_port": 47990,
  "max_players": 4,
  "ice_servers": [
    "stun:stun.l.google.com:19302"
  ],
  "stream_settings": {
    "width": 1920,
    "height": 1080,
    "fps": 60,
    "bitrate": 20000,
    "codec": "h264",
    "audio_channels": 2
  }
}
```

## Player Roles

| Role | Input Permissions | Description |
|------|-------------------|-------------|
| **Host** | Keyboard, Mouse, Gamepad (slot 0) | First user to connect, full control |
| **Player 2-4** | Gamepad only (slots 1-3) | Can be granted keyboard by host |
| **Spectator** | None (watch only) | Can request to become player |

## Input Mapping

- **Gamepad**: Browser Gamepad API → Moonlight protocol
  - Each player's first gamepad maps to their assigned slot
  - Standard mapping (Xbox-style): A/B/X/Y, triggers, sticks, D-pad

- **Keyboard/Mouse**: Only enabled for Host by default
  - Host can grant keyboard access to other players
  - Mouse capture requires clicking on the video

## Browser Support

| Browser | Video | Audio | Gamepad | Keyboard Lock |
|---------|-------|-------|---------|---------------|
| Chrome 90+ | ✅ | ✅ | ✅ | ✅ (HTTPS) |
| Firefox 90+ | ✅ | ✅ | ✅ | ❌ |
| Safari 15+ | ✅ | ✅ | ⚠️ | ❌ |
| Edge 90+ | ✅ | ✅ | ✅ | ✅ (HTTPS) |

## Network Requirements

- **WebRTC**: UDP ports for media (configured via ICE servers)
- **STUN**: For NAT traversal (default: Google STUN servers)
- **TURN**: Optional relay server for restrictive networks

For LAN streaming, no external servers are needed. For internet streaming, consider:
- Port forwarding
- Setting up a TURN server (e.g., coturn)
- Using Cloudflare TURN

## Development

### Project Structure

```
moonparty/
├── cmd/moonparty/         # Main entry point
├── internal/
│   ├── server/            # HTTP/WebSocket server
│   ├── webrtc/            # Pion WebRTC management
│   ├── moonlight/         # Sunshine protocol client
│   └── session/           # Player/session management
├── web/static/            # Web UI (HTML/CSS/JS)
└── pkg/protocol/          # Protocol definitions
```

### Building from Source

```bash
# Get dependencies
go mod tidy

# Build
go build -o moonparty ./cmd/moonparty

# Run in development
go run ./cmd/moonparty --host localhost
```

## Related Projects

- [Sunshine](https://github.com/LizardByte/Sunshine) - Open-source Nvidia GameStream host
- [Moonlight](https://moonlight-stream.org/) - GameStream client (native apps)
- [moonlight-web-stream](https://github.com/MrCreativ3001/moonlight-web-stream) - Original browser client (Rust)
- [Pion WebRTC](https://github.com/pion/webrtc) - Go WebRTC implementation

## License

MIT License - See [LICENSE](LICENSE) for details.

## Contributing

Contributions welcome! Please open an issue to discuss changes before submitting PRs.

### Areas for Contribution

- [ ] Full Moonlight protocol implementation
- [ ] H.265/AV1 codec support
- [ ] TURN server integration
- [ ] Mobile touch controls
- [ ] Stream recording
- [ ] Chat system
