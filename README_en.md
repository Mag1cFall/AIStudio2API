<div align="center">

# AI Studio to OpenAI, Anthropic & Gemini Compatible API

<p align="center">
  <a href="README.md">中文</a>
  &nbsp;|&nbsp;
  <a href="README_en.md"><b>English</b></a>
</p>

<p>
  <b>A High-Performance Go Proxy Server</b><br>
  Converts the Google AI Studio web protocol into OpenAI, Responses, Anthropic, and Gemini compatible APIs
</p>

<p>
  Multi-Account Rotation &nbsp;•&nbsp;
  Nano Banana Image Generation &nbsp;•&nbsp;
  Google Tools<br>
  Veo Video Generation &nbsp;•&nbsp;
  Gemini TTS Speech Synthesis
</p>

</div>

---

## Features

- **Four API Protocols**: OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini GenerateContent
- **Multi-Account Runtime**: Detects Free, Pro, Ultra, and Plus benefits and schedules by verified model access, first-event latency, and concurrency slots
- **Native Streaming**: Text, reasoning summaries, function calls, Google tools, media, and usage
- **TTS Speech Generation**: Gemini TTS models for single-speaker and multi-speaker audio
- **Image Generation**: Nano Banana image generation
- **Video Generation**: Veo video generation and image-to-video
- **YouTube Input**: Paste a video URL to attach and read the external video
- **Smart Model Switching**: Discover models from AI Studio and route through the `model` field
- **Google Tools**: Search, Image Search, URL Context, Code Execution, and Maps
- **Anti-Fingerprinting**: Camoufox holds the official WAA lifecycle with a stable browser fingerprint and network exit per account
- **GUI Launcher**: Manage accounts, service controls, live logs, models, requests, and configuration in the web UI
- **Modular Architecture**: Go handles protocols, scheduling, APIs, and management; Camoufox hosts WAA and isolated login

## System Requirements

- **Windows Release Runtime**: Windows 10 or later, `aistudio2api.exe`, and `start.bat`
- **Source Build**: Go 1.26, Node.js 24, and npm
- **Operating System**: Windows, macOS, Linux
- **Memory**: 2GB+ available memory for one account; each resident prewarmed account adds about 0.6GB
- **Network**: Stable internet connection to Google AI Studio

## Installation

### Method 1: Windows One-Click Start (Recommended)

```powershell
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
copy .env.example .env
```

Then double-click `start.bat`. You can also run it from PowerShell:

```powershell
.\start.bat
```

The script runs an existing `aistudio2api.exe` immediately. In a source checkout without the executable, it installs frontend dependencies and builds the frontend and Go program.

The first launch downloads Camoufox for the current platform to `runtime/camoufox/`. Set `CAMOUFOX_PATH` to use an existing executable instead.

### Method 2: Linux and macOS Source Build

#### 1. Install Dependencies

- Go 1.26
- Node.js 24 and npm

#### 2. Clone the Project

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
cp .env.example .env
```

#### 3. Build and Run

```bash
cd web
npm ci
npm run build
cd ..
go build -o aistudio2api ./cmd/aistudio2api
chmod +x ./aistudio2api
./aistudio2api
```

The first Linux or macOS launch also prepares the matching Camoufox build automatically.

## Quick Start

### First-Time Use (Authentication Required)

1. **Prepare the first account**:

   Windows can import a local Chrome account:

   ```powershell
   start.bat setup
   ```

   Linux and macOS use an isolated Camoufox login:

   ```bash
   ./aistudio2api setup --login --label name@gmail.com
   ```

   Set `--label` to the Google email used for the login. The account is saved under the path configured by `AISTUDIO_AUTH_STATES` in `.env`. Locale and timezone default to the current computer and can be set with `--locale` and `--timezone`.

2. **Start the management UI**:
   - Double-click `start.bat` on Windows
   - Run `./aistudio2api` on Linux or macOS
   - The browser opens `http://127.0.0.1:2048`
   - The initial state is `STOPPED`, with Logs open by default

3. **Add another account**:
   - Open Accounts and click "Add account"
   - Enter the Google email, proxy, locale, and timezone
   - Submitting opens an isolated Camoufox window; sign in to Google and enter AI Studio there
   - The account is saved when login completes

4. **Start the API**:
   - Click "Start service" to start the data plane
   - The state advances through `LAUNCHING` to `RUNNING`; "Stop service" cancels an in-progress launch
   - Use Logs to confirm account, model, and request status
   - The API listens on `http://127.0.0.1:2048` by default

Account actions depend on state:

| Account state | Available actions |
| --- | --- |
| `ready` | Edit, disable, verify, delete |
| `disabled` | Edit, enable, delete |
| `auth_required` | Edit, disable, log in again, verify, delete |

"Log in again" appears only when the account state is `auth_required`.

### Daily Use (With Existing Authentication)

1. Double-click `start.bat` on Windows; run `./aistudio2api` on Linux or macOS
2. Click "Start service" to enable the APIs
3. "Stop service" cancels an in-progress launch or active requests and closes WAA workers while the management UI and Logs remain available
4. Click "Start service" again to resume the APIs

Press `Ctrl+C` in the launch window or close that window to exit the manager. Closing the browser tab does not stop the manager.

### Quick Start

`start.bat`: Starts the manager and opens the web UI.

`start.bat -open-ui=false`: Starts the manager without opening the web UI.

`start.bat setup`: Scans local Chrome accounts. Use `--email` or `--profile` to select a Chrome account. Run `start.bat setup --login --label name@gmail.com` for an isolated login, or `start.bat setup --storage-state <file> --label name@gmail.com` to import a file.

## API Usage

### OpenAI Compatible Interface

After starting the service, call OpenAI Chat Completions directly:

```bash
curl http://127.0.0.1:2048/v1/chat/completions \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.7-flash",
    "messages": [{"role": "user", "content": "Hello, world!"}],
    "stream": true
  }'
```

### Client Configuration Example

| Protocol | Base URL | API key |
| --- | --- | --- |
| OpenAI Chat / Responses | `http://127.0.0.1:2048/v1` | `PROXY_API_KEY` from `.env` |
| Anthropic Messages | `http://127.0.0.1:2048` | `PROXY_API_KEY` from `.env` |
| Gemini | `http://127.0.0.1:2048` | `PROXY_API_KEY` from `.env` |

Read model names from `GET /v1/models` or `GET /v1beta/models`.

For Cherry Studio:

1. Open Cherry Studio settings
2. Add an OpenAI-compatible provider
3. Set the API host to `http://127.0.0.1:2048/v1`
4. Set the API key to `PROXY_API_KEY` from `.env`
5. Load models from `/v1/models`, or add `gemini-3.6-flash` and `gemini-3.7-flash` manually

Main endpoints:

| Capability | Endpoint |
| --- | --- |
| Models | `GET /v1/models`, `GET /v1beta/models` |
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Anthropic | `POST /v1/messages`, `POST /v1/messages/count_tokens` |
| Gemini | `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens` |
| Images | `POST /v1/images/generations` |
| Speech | `POST /v1/audio/speech` |
| Music | Gemini `generateContent` with `responseModalities: ["AUDIO"]` |
| Video | `POST /v1/videos`, `GET /v1/videos/{id}`, `GET /v1/videos/{id}/content` |

### TTS Speech Generation

```bash
curl http://127.0.0.1:2048/v1/audio/speech \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-tts-preview",
    "input": "Hello, this is a test.",
    "voice": "Kore",
    "response_format": "wav"
  }' \
  --output speech.wav
```

Multi-speaker speech is available through Gemini `generateContent` with `multiSpeakerVoiceConfig`.

```bash
curl http://127.0.0.1:2048/v1beta/models/gemini-2.5-flash-preview-tts:generateContent \
  -H "x-goog-api-key: 123" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{"parts": [{"text": "Joe: How are you?\nJane: I am fine, thanks!"}]}],
    "generationConfig": {
      "responseModalities": ["AUDIO"],
      "speechConfig": {
        "multiSpeakerVoiceConfig": {
          "speakerVoiceConfigs": [
            {"speaker": "Joe", "voiceConfig": {"prebuiltVoiceConfig": {"voiceName": "Kore"}}},
            {"speaker": "Jane", "voiceConfig": {"prebuiltVoiceConfig": {"voiceName": "Puck"}}}
          ]
        }
      }
    }
  }' --output speech.json
```

Available voices are returned by `capability_options.voices` in the live model catalog.

### Image Generation (Nano Banana)

```bash
curl http://127.0.0.1:2048/v1/images/generations \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-image",
    "prompt": "A cute cat wearing a tiny hat",
    "n": 1,
    "size": "1024x1024"
  }'
```

### Video Generation (Veo)

```bash
curl http://127.0.0.1:2048/v1/videos \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "veo-3.1-fast-generate-preview",
    "prompt": "A drone flying over a forest"
  }'
```

After creating the operation, poll `GET /v1/videos/{id}` and download the result from `GET /v1/videos/{id}/content`.

## Models

The model directory follows AI Studio updates and is available from `/v1/models` and `/v1beta/models`. The current directory contains:

| Model ID | Display name | Input | Output | Methods |
| --- | --- | ---: | ---: | --- |
| `antigravity-preview-05-2026` | Antigravity Agent Preview | 131072 | 65536 | `countTokens, generateContent` |
| `gemini-2.5-flash` | Gemini 2.5 Flash | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-2.5-flash-image` | Nano Banana | 32768 | 32768 | `batchGenerateContent, countTokens, generateContent` |
| `gemini-2.5-flash-lite` | Gemini 2.5 Flash-Lite | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-2.5-flash-preview-tts` | Gemini 2.5 Flash Preview TTS | 8192 | 16384 | `countTokens, generateContent` |
| `gemini-2.5-pro` | Gemini 2.5 Pro | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-2.5-pro-preview-tts` | Gemini 2.5 Pro Preview TTS | 8192 | 16384 | `batchGenerateContent, countTokens, generateContent` |
| `gemini-3-flash-preview` | Gemini 3 Flash Preview | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-3-pro-image` | Nano Banana Pro | 131072 | 32768 | `batchGenerateContent, countTokens, generateContent` |
| `gemini-3.1-flash-image` | Nano Banana 2 | 65536 | 65536 | `batchGenerateContent, countTokens, generateContent` |
| `gemini-3.1-flash-lite` | Gemini 3.1 Flash Lite | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-3.1-flash-lite-image` | Nano Banana 2 Lite | 65536 | 65536 | `batchGenerateContent, countTokens, generateContent` |
| `gemini-3.1-flash-tts-preview` | Gemini 3.1 Flash TTS Preview | 8192 | 16384 | `batchGenerateContent, countTokens, generateContent` |
| `gemini-3.1-pro-preview` | Gemini 3.1 Pro Preview | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-3.5-flash` | Gemini 3.5 Flash | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-3.5-flash-lite` | Gemini 3.5 Flash Lite | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-3.6-flash` | Gemini 3.6 Flash | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-3.7-flash` | Gemini 3.7 Flash | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-flash-latest` | Gemini Flash Latest | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-flash-lite-latest` | Gemini Flash-Lite Latest | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-omni-flash-preview` | Gemini Omni Flash Preview | 131072 | 65536 | `countTokens, generateContent` |
| `gemini-pro-latest` | Gemini Pro Latest | 1048576 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-robotics-er-1.6-preview` | Gemini Robotics-ER 1.6 Preview | 131072 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemini-robotics-er-2-preview` | Gemini Robotics-ER 2 Preview | 131072 | 65536 | `batchGenerateContent, countTokens, createCachedContent, generateContent` |
| `gemma-4-26b-a4b-it` | Gemma 4 26B A4B IT | 262144 | 32768 | `countTokens, generateContent` |
| `gemma-4-31b-it` | Gemma 4 31B IT | 262144 | 32768 | `countTokens, generateContent` |
| `lyria-3-clip-preview` | Lyria 3 Clip Preview | 1048576 | 65536 | `countTokens, generateContent` |
| `lyria-3-pro-preview` | Lyria 3 Pro Preview | 1048576 | 65536 | `countTokens, generateContent` |
| `veo-3.1-fast-generate-preview` | Veo 3.1 fast | 480 | 8192 | `predictLongRunning` |
| `veo-3.1-generate-preview` | Veo 3.1 | 480 | 8192 | `predictLongRunning` |
| `veo-3.1-lite-generate-preview` | Veo 3.1 lite | 480 | 8192 | `predictLongRunning` |

Public endpoints implement standard `generateContent`, `countTokens`, and `predictLongRunning`. `batchGenerateContent` and `createCachedContent` remain live model metadata, while bidi-only and private Interaction models are omitted from public model lists.

## Project Architecture

```text
AIStudio2API/
├── cmd/aistudio2api/        # Program entry point, management, and runtime
├── internal/aistudio/       # AI Studio protocol, authentication, models, and media
├── internal/api/            # OpenAI, Responses, Anthropic, and Gemini adapters
├── internal/chromeauth/     # Windows Chrome and DBSC import
├── internal/camoufoxnative/ # Camoufox BiDi, login, and WAA workers
├── internal/webui/          # Embedded frontend build
├── web/                     # Vue 3 and TypeScript management UI
├── docs/                    # Development guide and protocol specification
└── start.bat                # Windows one-click launcher
```

## Configuration

### Environment Variables

Copy and edit the environment file:

```bash
cp .env.example .env
```

| Variable | Default | Purpose |
| --- | --- | --- |
| `AISTUDIO_AUTH_STATES` | `auth` | Account file, directory, or comma-separated paths |
| `LISTEN_ADDR` | `127.0.0.1:2048` | Management UI and API listen address |
| `PROXY_API_KEY` | empty | Public API key |
| `PROXY` | empty | HTTP, HTTPS, or SOCKS5 proxy used by Chrome import, login, and accounts without an override |
| `INIT_TIMEOUT` | `2m` | Per-account WAA initialization timeout |
| `REQUEST_TIMEOUT` | `5m` | Maximum request execution time |
| `WARM_WORKER_LIMIT` | `5` | Number of resident prewarmed accounts |
| `WARM_STARTUP_CONCURRENCY` | `2` | Accounts initialized concurrently during prewarming |
| `PER_ACCOUNT_CONCURRENCY` | `2` | Concurrent requests allowed per account |
| `TEMPORARY_CHAT` | `false` | Use Temporary Chat for the WAA prewarm page |

### Port Configuration

- **Management UI and APIs**: Default port `2048`
- **Camoufox**: Local ports are allocated dynamically

## Advanced Features

### Proxy Configuration

HTTP, HTTPS, and SOCKS5 proxies without embedded credentials are supported:

1. Set the global proxy under Service Configuration
2. Edit an account to set an account-specific proxy
3. The account proxy is used for login, WAA, and business requests

### Authentication File Management

Authentication files are stored in `auth/` by default:

| Path | Contents |
| --- | --- |
| `auth/<Google email>/account.json` | Account email, proxy, locale, timezone, and enabled state |
| `auth/<Google email>/storage-state.json` | Google cookies and authentication renewal material |
| `auth/<Google email>/runtime-state.json` | Benefit tier, model eligibility, cooldowns, and resource ownership |
| `auth/.leases/<Google email>.lock` | Cross-process lease for the account directory |
| `[user cache]/AIStudio2API/runtime-leases/<Google email>.lock` | WAA Worker lease for that email on the current computer |

The lowercase Google email is the account directory, management UI identity, and log source. `.leases` coordinates account-directory access, while the runtime lease in the user cache allows one WAA Worker per email on the current computer.

Adding an account starts an isolated Camoufox login. `ready` accounts can be edited, disabled, verified, and deleted; `auth_required` accounts can log in again.

## Documentation

- [Development and contribution](docs/development.md)
- [Google AI Studio protocol specification](docs/protocol.md)
- [Runtime logging](docs/logging.md)

## Important Notes

### About Camoufox

This project uses [Camoufox](https://camoufox.com/) to reduce automation detection. Camoufox is based on Firefox and changes lower-level browser behavior to retain a realistic device fingerprint.

Go handles business requests with a Firefox TLS/HTTP2 profile aligned with Camoufox. Camoufox provides official WAA initialization, fresh proofs, and isolated account login.

### Limitations

- **Client-Managed History**: Clients submit complete conversation context for Chat, Anthropic, and Gemini requests
- **AI Studio History**: API requests are not saved to website history; `TEMPORARY_CHAT=true` also disables autosave for the WAA prewarm page
- **Responses Sessions**: `previous_response_id` is stored only in the current process and is cleared on restart
- **Authentication Expiry**: Chrome imports retain DBSC renewal material; isolated-login accounts must log in again after authentication expires

## Troubleshooting

### Windows Port Reserved by System

If startup reports that the port configured by `LISTEN_ADDR` is unavailable while Task Manager shows no owning process, Hyper-V, WSL2, or Docker NAT may have reserved the port range.

Run the following commands from an elevated PowerShell or CMD window.

#### 1. Inspect Reserved Port Ranges

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

If `2048` falls inside a reserved range, change `LISTEN_ADDR`, or restart WinNAT and inspect the range again:

```powershell
net stop winnat
net start winnat
```

When the port is free, it can be reserved persistently:

```powershell
netsh int ipv4 add excludedportrange protocol=tcp startport=2048 numberofports=1 store=persistent
```

Common runtime states:

| State | Resolution |
| --- | --- |
| The page does not open automatically | Open the address configured by `LISTEN_ADDR` in `.env` |
| `service_stopped` | Click "Start service" in the management UI |
| No account is available | Add, enable, or log in to an account from Accounts |
| Camoufox preparation fails | Check access to GitHub Releases or set `CAMOUFOX_PATH` |

## Contributing

Issues and Pull Requests are welcome!

## Development Roadmap

- ✅ **TTS Support**: Adapted `gemini-2.5-flash/pro-preview-tts` speech generation models
- ✅ **Media Generation**: Supports Imagen 3, Veo 2, Nano Banana image/video generation
- ✅ **Documentation**: Update and optimize documentation in `docs/` directory
- **One-Click Deployment**: Provide fully automated install and launch scripts for Windows/Linux/macOS
- **Docker Support**: Provide standard Dockerfile and Docker Compose orchestration files
- ✅ **Go Refactoring**: Migrate core proxy service to Go for improved concurrency and reduced resource usage
- ✅ **Multi-Worker Load Balancing**: Support multi-Google account rotation pool for higher concurrency limits

### Pure-Protocol WAA Runtime

The target is a complete reverse-engineered WAA VM implemented in Go, covering the dynamic program, interpreter, challenge, persistent state, snapshot, and proof pipeline. The final production runtime contains only the Go protocol implementation, with no Camoufox process, DOM environment, or AI Studio frontend bundle dependency.

| Stage | Deliverable |
| --- | --- |
| Protocol fixtures | Archive complete inputs and outputs for the dynamic program, challenges, state transitions, snapshots, and proofs as reproducible protocol fixtures |
| Go executor | Implement dynamic-program loading, interpretation, challenge evaluation, persistent state, snapshot restoration, and proof generation with fixture-level parity |
| Runtime cutover | Move account initialization and proof refresh to the native Go runtime, validate every known challenge, then remove the Camoufox, DOM, and frontend-bundle runtime path |
