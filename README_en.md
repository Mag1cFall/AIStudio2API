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
  Playground + Build Dual Quota Channels &nbsp;•&nbsp;
  Build Code Assistant Chat Channel &nbsp;•&nbsp;
  High-Concurrency Multi-Account<br>
  Camoufox and Pure Go WAA Backends &nbsp;•&nbsp;
  Claude Code, Codex, and Other Agent Clients &nbsp;•&nbsp;
  Nano Banana, Veo, TTS, and Omni
</p>

</div>

<table>
  <tr>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/accounts-dark.webp"><img src="docs/images/accounts-light.webp" alt="Accounts"></picture><br><b>Accounts</b>: Tier, egress and state of every account; add, sign in and refresh from here</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/usage-dark.webp"><img src="docs/images/usage-light.webp" alt="Usage"></picture><br><b>Usage</b>: Requests, success rate, tokens, estimated cost and first-token latency, filtered by model, account or channel</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/logs-dark.webp"><img src="docs/images/logs-light.webp" alt="Logs"></picture><br><b>Logs</b>: Model, account, tokens, speed and first-token time of every request</td>
  </tr>
  <tr>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/models-dark.webp"><img src="docs/images/models-light.webp" alt="Model catalog"></picture><br><b>Model catalog</b>: Models and capabilities merged from each account’s live catalog</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/settings-dark.webp"><img src="docs/images/settings-light.webp" alt="Settings"></picture><br><b>Settings</b>: Every <code>.env</code> option can be changed in the UI</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/image-dark.webp"><img src="docs/images/image-light.webp" alt="Image generation"></picture><br><b>Image generation</b>: Nano Banana text-to-image and editing with the same run settings as AI Studio</td>
  </tr>
  <tr>
    <td colspan="3"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/chat-dark.webp"><img src="docs/images/chat-light.webp" alt="API playground: chat with thinking, math tables and code blocks" width="100%"></picture><br><b>API playground</b>: The AI Studio chat page with thinking, math tables and code blocks</td>
  </tr>
</table>

**Get started in three steps**: download a [release](https://github.com/Mag1cFall/AIStudio2API/releases) and launch it → add a Google account on the Accounts page → click "Start service" and use `http://127.0.0.1:2048` as the Base URL of your OpenAI, Anthropic or Gemini client

---

## Core Capabilities

- **Dual Quota Channels**: Every account has separate Playground and Build app proxy quotas, and `UPSTREAM_CHANNELS` enables either or both; when one channel hits its limit, the same account continues on the other
- **Build Chat Models**: Every model selectable in the Build chat panel is also listed as `<model>-build` (for example `gemini-3.8-flash-build`), answered by the Build code assistant with its system prompt and billed to the Build chat quota (free requests or the Google AI subscription). ⚠️ Every request to these models carries the Build code assistant's own injected system prompt, which shapes the answer's style, format, and persona; evaluate whether that fits your use
- **High-Concurrency Multi-Account**: Detects Free, Pro, Ultra, and Plus benefits and routes across accounts by the live model catalog with round-robin or fill-first
- **Two WAA Backends**: Camoufox holds the official WAA lifecycle by default; with `WAA_BACKEND=go`, pure Go generates the official proof and no browser is downloaded or launched at runtime
- **Four API Protocols**: OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini GenerateContent; Responses supports context compaction through `/v1/responses/compact` and `compaction` and `compaction_trigger` input items
- **Mainstream Agent Clients**: Works with Claude Code, Codex, OpenCode, pi, omp, OpenClaw, and Hermes, including file read and write tool calls; native web search works in Claude Code, Codex, and omp

## Features

- **Native Streaming**: Text, reasoning summaries, function calls, Google tools, media, and usage
- **Function Tools**: Tool calls, continuation, selection, and strict argument validation across all four protocols, including union types and open parameter schemas
- **TTS Speech Generation**: Gemini TTS models for single-speaker and multi-speaker audio
- **Image Generation**: Nano Banana image generation
- **Video Generation**: Veo video generation and image-to-video; Gemini Omni accepts text, image, and video input and returns text and MP4 video through the four generation APIs
- **Deep Research**: Pro and Ultra accounts can run Deep Research through the four generation APIs and the Interactions `agent` field, returning the research process, a cited report, and charts
- **YouTube Input**: Paste a video URL to attach and read the external video
- **Smart Model Switching**: Discover models from AI Studio and route through the `model` field
- **Google Tools**: Search, Image Search, URL Context, Code Execution, and Maps
- **Files and Transcribe**: File upload, metadata, content, deletion, and audio transcription
- **Live and Robotics**: WebSocket text, audio, JPEG images, media end, tool calls, resumption, and interruption
- **Anti-Fingerprinting**: Camoufox holds the official WAA lifecycle with a stable browser fingerprint and network exit per account
- **GUI Launcher**: Manage accounts, service controls, live logs, models, requests, usage statistics, and configuration in the web UI, with dark and light color themes
- **Desktop App**: Windows, macOS, and Linux desktop apps show the management UI in a window and keep running in the system tray after the window is closed
- **Online Update**: The Version page shows the latest release and its notes; the Windows and macOS desktop apps and the Linux and macOS command-line builds download, verify, and restart into the new version in one click, while the Linux desktop app points to the new installer
- **Modular Architecture**: Go handles protocols, scheduling, APIs, and management; Camoufox hosts WAA and isolated login

## System Requirements

- **Windows Release Runtime**: Windows 10 or later; on systems without the WebView2 Runtime, the desktop app opens the management UI in the default browser
- **macOS Desktop App**: macOS 12 or later, universal for Apple silicon and Intel
- **Linux Desktop App**: GTK 4 and WebKitGTK 6.0, such as Ubuntu 24.04, Debian 13, or later; the deb installs these and the Camoufox libraries, while the AppImage also needs the Camoufox libraries listed on the next line
- **Linux Release Runtime**: Extract `linux-amd64.tar.gz` and run `./aistudio2api`; Camoufox needs the Firefox system libraries, on Debian/Ubuntu run `sudo apt install libgtk-3-0 libasound2 libnss3 libdbus-glib-1-2 libxtst6 libxrandr2 libgbm1 libxkbcommon0 libpango-1.0-0 libcairo2 libxcomposite1 libxdamage1 libxfixes3 xvfb`
- **Source Build**: Go 1.26.0+, Node.js 22.18+, 24.11+, or 26+, and its bundled npm
- **Operating System**: Windows, macOS, Linux
- **Memory**: 2GB+ available memory for one account; each resident prewarmed account adds about 0.6GB
- **Network**: Stable internet connection to Google AI Studio

## Installation

### Method 1: Windows One-Click Start (Recommended)

Download the `windows-amd64.zip` package from [Releases](https://github.com/Mag1cFall/AIStudio2API/releases), extract it, and double-click `aistudio2api.exe`:

- The management UI opens in a desktop window; on first launch the window shows a startup screen until Camoufox finishes downloading
- Closing the window keeps the app running in the system tray; the tray menu shows the window, opens the management UI in the browser, or quits
- Double-clicking `aistudio2api.exe` again brings back the running window

To start from source:

```powershell
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
copy .env.example .env
cd web
npm ci
npm run build
cd ..
go build -o aistudio2api.exe ./cmd/aistudio2api
.\aistudio2api.exe
```

The first launch downloads Camoufox for the current platform to `runtime/camoufox/`:

- The download goes through the first proxy in `PROXY`, or the system proxy when it is unset
- An interrupted download resumes from the downloaded part on the next launch
- `CAMOUFOX_PATH` selects an existing executable of the same version; a different version stops startup with an error naming the required version

### Method 2: macOS and Linux Desktop App

On macOS, download `darwin-universal.dmg` from Releases, open it, and drag `AIStudio2API.app` into Applications. The app is ad-hoc signed; when the system blocks the first launch, click "Open Anyway" in System Settings → Privacy & Security, or run:

```bash
xattr -dr com.apple.quarantine /Applications/AIStudio2API.app
```

On Linux, make `linux-amd64.AppImage` executable and run it, or install `linux-amd64.deb` and start AIStudio2API from the application menu:

```bash
chmod +x aistudio2api-<version>-linux-amd64.AppImage
./aistudio2api-<version>-linux-amd64.AppImage
sudo apt install ./aistudio2api-<version>-linux-amd64.deb
```

The desktop app keeps `.env`, `auth/`, and `runtime/` in:

- macOS: `~/Library/Application Support/AIStudio2API/`
- Linux: `~/.config/AIStudio2API/`

Run `setup` and other commands with the desktop executable in a terminal, such as `/Applications/AIStudio2API.app/Contents/MacOS/aistudio2api setup --login`; the deb installs the executable as `aistudio2api`.

### Method 3: Linux and macOS Source Build

#### 1. Install Dependencies

- Go 1.26.0 or later
- Node.js 22.18+, 24.11+, or 26+, with its bundled npm

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

### Method 4: Docker

```bash
mkdir aistudio2api && cd aistudio2api
curl -fsSLO https://raw.githubusercontent.com/Mag1cFall/AIStudio2API/main/docker-compose.yml
mkdir data
cat > data/.env <<'EOF'
PROXY_API_KEY=<API key>
ADMIN_AUTH_ENABLED=true
ADMIN_PASSWORD=<console password>
EOF
docker compose up -d
```

The image supports linux/amd64 and linux/arm64, and the service listens on `0.0.0.0:2048` inside the container. `data/` holds `.env`, `auth/`, and `runtime/`; with the Camoufox backend, the first start downloads Camoufox to `data/runtime/camoufox/`. To migrate an existing installation, copy these three items into `data/`. The container runs as root, so files the service writes in `data/` are owned by root; edit them with `sudo` on Linux hosts.

- Set `PROXY_API_KEY` when the port is reachable from other machines. Opening the console from outside the container requires `ADMIN_AUTH_ENABLED=true` and a console password; the default username is `admin`.
- Add accounts: choose "Sign in with browser" in "Add account" on the console and sign in to Google in the sign-in window that opens in the console; you can also paste cookies, or copy `auth/<Google email>/` from a computer that can sign in to Google into `data/auth/`.
- `PROXY` and account proxies are reached from inside the container; when the proxy runs on the host, use the host's LAN address and allow LAN connections on the proxy.
- Run `docker compose restart` after editing `data/.env`, `docker compose pull && docker compose up -d` to update the image, and `docker compose up -d --build` in the source directory to build from source.

## Quick Start

### First-Time Use (Authentication Required)

1. **Prepare the first account**:

   Windows can import a local Chrome account:

   ```powershell
   cmd /c start /wait aistudio2api.exe setup
   ```

   Linux and macOS use an isolated Camoufox login:

   ```bash
   ./aistudio2api setup --login
   ```

   The Google email is read from AI Studio after login, and Google Drive is authorized for the account. The account is saved under the path configured by `AISTUDIO_AUTH_STATES` in `.env`. Locale and timezone default to the current computer and can be set with `--locale` and `--timezone`.

2. **Start the management UI**:
   - Double-click `aistudio2api.exe` on Windows
   - Start the macOS or Linux desktop app from the application menu, or run `./aistudio2api` for the command-line build
   - The desktop app opens the management UI in its window; the command-line build opens `http://127.0.0.1:2048` in the browser
   - The initial state is `STOPPED`, with Logs open by default

3. **Add another account**:
   - Open Accounts and click "Add account" to pick a method
   - "Import from Chrome" supports selecting multiple local Chrome accounts
   - "Paste cookies" takes the JSON exported on aistudio.google.com by a cookie extension (such as Cookie-Editor), a Cookie request header line, or a Netscape cookies.txt file, and the service detects the account email
   - "Import from an antidetect browser" lists the windows signed in to AI Studio in local antidetect browsers and Chromium with their language and time zone; select them and import, and each account keeps the language and time zone of its browser. The browser needs remote debugging enabled
   - "Sign in with browser" opens an isolated Camoufox window, detects the email after login, authorizes Google Drive, and saves the account; when Google asks to verify your identity, confirm it in that window or on your phone
   - When the server has no graphical display, or the console is opened from another computer or through a reverse proxy, Camoufox for "Sign in with browser" and "Sign in again" runs headless on the server and its screen appears in a full-screen sign-in window in the console, where you click, type and paste; Cancel ends the sign-in
   - Account proxy, locale and time zone are under "More settings" in the dialog

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

1. Double-click `aistudio2api.exe` or start the desktop app from the application menu; run `./aistudio2api` for the command-line build
2. Click "Start service" to enable the APIs; with `AUTO_START=true`, the service starts together with the manager
3. "Stop service" cancels an in-progress launch or active requests and closes WAA workers while the management UI and Logs remain available
4. Click "Start service" again to resume the APIs

Starting again loads the latest data-plane settings from `.env`; the account selection strategy and worker capacity apply as soon as they are saved. Changes to `LISTEN_ADDR` or `PROXY_API_KEY` require restarting the management process.

Closing the desktop window keeps the app running in the system tray; choose Quit in the tray menu to exit the manager. For the command-line build, press `Ctrl+C` in the launch window or close that window to exit the manager. Closing the browser tab does not stop the manager.

### Quick Start

On Windows, run these commands for the desktop app as `cmd /c start /wait aistudio2api.exe <arguments>`; the command line returns after the program exits.

`aistudio2api`: Starts the manager and opens the management UI, in a window for the desktop app or in the browser for the command-line build.

`aistudio2api -open-ui=false`: Starts the manager without opening a window or the web UI; the desktop app shows only its tray icon.

`aistudio2api setup`: Imports accounts.

- Without options it scans local Chrome accounts; `--email` or `--profile` selects a Chrome account
- Isolated login: `aistudio2api setup --login`, which needs a graphical display; without one, start the service and sign in with "Sign in with browser" in the console
- File import: `aistudio2api setup --storage-state <file>`
- Cookie import: `aistudio2api setup --cookie-file <file>`, `aistudio2api setup --cookie "<Cookie request header>"`, or `aistudio2api setup --cdp <debugging address>`; the account email is detected from the cookies, and an account imported with `--cdp` keeps the language and time zone of that browser

`aistudio2api setup --remote https://<remote-instance>`: Adds accounts to a remote instance. Click "Add remotely" on the remote instance's Accounts page to get a pairing token, then run the command on a computer that can sign in to Google and paste the token. It combines with options such as `--login`.

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

Read model names from `GET /v1/models` or `GET /v1beta/models`. When `PROXY_API_KEY` is empty, only pages on this machine can call the API from a browser; web clients and some desktop clients need `PROXY_API_KEY` set. Separate several keys with commas; any of them is accepted.

For Cherry Studio:

1. Open Cherry Studio settings
2. Add an OpenAI-compatible provider
3. Set the API host to `http://127.0.0.1:2048/v1`
4. Set the API key to `PROXY_API_KEY` from `.env`
5. Load models from `/v1/models`, or add `gemini-3.6-flash` and `gemini-3.7-flash` manually

[Claude Code](https://github.com/anthropics/claude-code) uses the Anthropic endpoint. Subagents pick models by the opus, sonnet, and haiku tiers; the variables below map them to AI Studio models. WebSearch runs on Google Search:

```powershell
$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:2048"
$env:ANTHROPIC_API_KEY = "<PROXY_API_KEY>"
$env:ANTHROPIC_MODEL = "gemini-3.8-flash"
$env:ANTHROPIC_DEFAULT_OPUS_MODEL = "gemini-3.1-pro-preview"
$env:ANTHROPIC_DEFAULT_SONNET_MODEL = "gemini-3.8-flash"
$env:ANTHROPIC_DEFAULT_HAIKU_MODEL = "gemini-3.5-flash-lite"
```

[Codex](https://github.com/openai/codex) uses the Responses endpoint. Add a provider to `~/.codex/config.toml` and put `PROXY_API_KEY` in the `AISTUDIO2API_KEY` environment variable. Codex's `web_search` tool runs on Google Search:

```toml
model = "gemini-3.8-flash"
model_provider = "aistudio"

[model_providers.aistudio]
name = "AIStudio2API"
base_url = "http://127.0.0.1:2048/v1"
env_key = "AISTUDIO2API_KEY"
wire_api = "responses"
```

[omp](https://github.com/can1357/oh-my-pi) runs its `web_search` tool with the model of the `web` role. Add a Gemini provider that points to this service in omp's `models.yml` (`apiKey` is the name of the environment variable holding `PROXY_API_KEY`), then set the `web` role to that model in `config.yml`:

```yaml
# models.yml
providers:
  aistudio-gemini:
    baseUrl: http://127.0.0.1:2048/v1beta
    api: google-generative-ai
    apiKey: AISTUDIO2API_KEY
    models:
      - id: gemini-3.8-flash
```

```yaml
# config.yml
modelRoles:
  web: aistudio-gemini/gemini-3.8-flash
```

Main endpoints:

| Capability | Endpoint |
| --- | --- |
| Models | `GET /v1/models`, `GET /v1/models/{model}`, `GET /v1beta/models`, `GET /v1beta/models/{model}` |
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses`, `POST /v1/responses/input_tokens`, `POST /v1/responses/compact`, `GET`/`DELETE /v1/responses/{id}`, `GET /v1/responses/{id}/input_items`, `POST /v1/responses/{id}/cancel` |
| Files | `POST /v1/files`, `GET /v1/files`, `GET /v1/files/{id}`, `GET /v1/files/{id}/content`, `DELETE /v1/files/{id}` (OpenAI and Anthropic formats); `POST /upload/v1beta/files`, `GET /v1beta/files`, `GET`/`DELETE /v1beta/files/{id}` |
| Anthropic | `POST /v1/messages`, `POST /v1/messages/count_tokens` |
| Gemini | `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens` |
| Embeddings | `POST /v1/embeddings`, `POST /v1beta/models/{model}:embedContent`, `:batchEmbedContents` |
| Images | `POST /v1/images/generations`, `POST /v1/images/edits` |
| Speech | `POST /v1/audio/speech` |
| Transcription and translation | `POST /v1/audio/transcriptions`, `POST /v1/audio/translations` |
| Music | Gemini `generateContent` with `responseModalities: ["AUDIO"]` |
| Video | `POST /v1/videos`, `GET /v1/videos`, `GET`/`DELETE /v1/videos/{id}`, `GET /v1/videos/{id}/content` |
| Gemini Video | `POST /v1beta/models/{model}:predictLongRunning`, `GET /v1beta/operations/{id}` |
| Live (including live translation, live transcription, and live music) / Robotics | `GET /v1/live`, `GET /v1/robotics/stream` |

All four generation APIs can enable Search, Image Search, URL Context, Code Execution, and Maps through their protocol fields. Request and event formats for Files, Transcribe, Live, and Robotics are documented in the [Google AI Studio protocol specification](docs/protocol.md).

Inline attachments in generation requests are preferentially uploaded as temporary Drive files and cleaned up when the request ends. If the account has not granted Drive access, the original inline data is sent instead. Images, audio, video, PDFs, and other inputs must be supported by the selected model. Upload reusable attachments once through the Files API and reuse their file IDs.

Gemini attachments and video image inputs accept `inlineData` / `inline_data`, `fileData` / `file_data`, `mimeType` / `mime_type`, and `fileUri` / `file_uri`. Base64 media data supports standard and URL-safe alphabets, padded and unpadded forms, and the `data:<MIME>;base64,` prefix. Markdown images in OpenAI assistant history also support URL-safe Base64 and CR/LF line breaks. Inline GIFs and GIFs uploaded through video multipart requests are converted to PNG using the first frame, preserving the logical canvas, frame position, and transparency.

### TTS Speech Generation

TalkifyTTS and the Google Gen AI SDK can connect to `http://127.0.0.1:2048/v1beta/interactions`; the stable endpoint is `/v1/interactions`. Authenticate with `x-goog-api-key`. Both `gemini-3.8-flash-tts` and `gemini-3.8-flash-lite-tts` support single-speaker and multi-speaker synthesis.

```python
from google import genai

client = genai.Client(api_key="123", http_options={"base_url": "http://127.0.0.1:2048"})
stream = client.interactions.create(
    model="gemini-3.8-flash-tts",
    input="Hello, this is a test.",
    response_format={"type": "audio"},
    generation_config={"speech_config": [{"voice": "Kore"}]},
    stream=True,
)
for event in stream:
    if event.event_type == "step.delta" and event.delta.type == "audio":
        print(event.delta.data)
```

Streaming defaults to Base64-encoded 24 kHz, 16-bit little-endian mono PCM. Non-streaming defaults to a complete WAV file, available through `interaction.output_audio.data`. Set `response_format.mime_type` to `audio/l16` or `audio/wav` to select the format. See the [Interactions protocol](docs/protocol.md#gemini-interactions) for style annotations and multi-speaker input.

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

Available voices are returned by `capability_options.voices` in the live model catalog. Models with the `speech_metadata` capability, such as `gemini-3.8-flash-tts`, accept the same `Speaker: line` script, and each text part can also set `speechMetadata.speaker` and `speechMetadata.style`, with `multiSpeakerVoiceConfig.mode` selecting `VERBATIM` or `CONVERSATIONAL`; OpenAI `instructions` become the speech style on these models.

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

The model catalog follows AI Studio updates; clients read the current values from `/v1/models` or `/v1beta/models`. The table below is a catalog-shape example; runtime results are authoritative for model IDs, limits, and methods:

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
| `gemini-nano-banana-2.1` | Nano Banana 2.1 | 65536 | 65536 | `batchGenerateContent, countTokens, generateContent` |
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

Public endpoints implement `generateContent`, `countTokens`, `embedContent`, `batchEmbedContents`, and `predictLongRunning`; `bidiGenerateContent` and `bidiGenerateMusic` models are called through `/v1/live`. `/v1/models` and `/v1beta/models` preserve the live upstream catalogs across accounts; scheduling uses explicit model ID, method, and capability fields plus current account runtime state.

Models that no enabled account's subscription covers (such as models open only to Google AI Ultra) are still listed without `channels`, and calls return 403 naming the required subscription. Models with the `output_continuation` capability (such as Gemini 4 Argon) accept an output limit up to `input_token_limit`, as on the official site; beyond 32768 tokens the response continues automatically in multiple rounds and is returned as one response. Model names accept a thinking suffix such as `gemini-4-argon(low)`: the value in parentheses is `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, `auto`, or a thinking budget in tokens, and it overrides thinking parameters in the request body. Model names fixed in a client can be mapped to catalog models with `MODEL_MAPPING`, for example `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash`; each alias is listed in the model catalog with the fields of its target.

## Project Architecture

```text
AIStudio2API/
├── cmd/aistudio2api/        # Thin entry point
├── internal/app/            # Commands, management listener, data-plane lifecycle, and scheduling
├── internal/setup/          # Account import and isolated-login CLI
├── internal/aistudio/       # AI Studio protocol, authentication, models, and media
├── internal/api/            # OpenAI, Responses, Anthropic, and Gemini adapters
├── internal/chromeauth/     # Windows Chrome and DBSC import
├── internal/camoufoxnative/ # Camoufox BiDi, login, and WAA workers
├── internal/webui/          # Embedded frontend build
├── internal/desktop/        # Desktop window and system tray
├── web/                     # Vue 3 and TypeScript management UI
├── docs/                    # Development guide and protocol specification
└── build/                   # App icon and per-platform packaging configuration
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
| `AUTO_START` | `false` | Start the generation service when the manager starts |
| `REQUEST_BODY_LOG` | `false` | Store public API request and response bodies, each truncated to 64 KiB, keeping the latest 1000 |
| `LOG_FILE` | `true` | Also write service logs to `runtime/logs/`, rotating at 10 MB per file |
| `LOG_MAX_TOTAL_MB` | `500` | Total size limit of the log directory; the oldest files are removed beyond it; at least 20 |
| `ADMIN_AUTH_ENABLED` | `false` | Enable username/password login for the console |
| `ADMIN_USERNAME` | `admin` | Administrator username |
| `ADMIN_PASSWORD` | empty | Administrator password, required when login is enabled |
| `PROXY` | empty | HTTP, HTTPS, or SOCKS5 proxy used by Chrome import, login, and accounts without an override; separate several proxies with commas to form a proxy pool; the system proxy is used when empty |
| `INIT_TIMEOUT` | `2m` | Per-account WAA initialization timeout |
| `REQUEST_TIMEOUT` | `30m` | Maximum request execution time, multiplied by the number of rounds for auto-continued requests |
| `WARM_WORKER_LIMIT` | `5` | Number of resident prewarmed accounts |
| `MAX_ACTIVE_WORKERS` | `10` | Maximum workers active during peak load |
| `WARM_STARTUP_CONCURRENCY` | `5` | Camoufox workers cold-started concurrently, shared by prewarming and on-demand scaling; unlimited with `WAA_BACKEND=go` |
| `PER_ACCOUNT_CONCURRENCY` | `2` | Concurrent requests allowed per account |
| `ROUTING_STRATEGY` | `round-robin` | `round-robin` rotates accounts; `fill-first` reuses the first available account |
| `UPSTREAM_CHANNELS` | `playground,build` | Upstream channels for generation requests; either one can be used alone |
| `MODEL_MAPPING` | empty | Client model names mapped to catalog models, each item `alias:model ID`, comma separated |
| `MODEL_CHANNELS` | empty | Limit a model to one channel, each item `model ID:playground` or `model ID:build`; also selectable in model details on the Model catalog page, applied on save |
| `BUILD_NATIVE_NONSTREAM` | `true` | Prefer native Build unary calls for non-streaming requests; log any fallback to stream collection |
| `WAA_BACKEND` | `camoufox` | `camoufox` runs WAA in a Camoufox page; `go` runs WAA inside the service process and neither downloads nor starts Camoufox |
| `TEMPORARY_CHAT` | `false` | Use Temporary Chat for the WAA prewarm page |
| `MEDIA_OUTPUT` | `inline` | How Chat Completions and Messages include generated images and audio: `inline` embeds a data URI; `url` saves the file under `runtime/media/`, named by its content, and links to it at `/media/<file>` on this service, a path that does not check the API key |
| `MEDIA_BASE_URL` | empty | Service address used in media URLs, such as `https://ai.example.com`; empty uses the address of each request and follows `X-Forwarded-Proto` behind a reverse proxy |

The service loads every account from `AISTUDIO_AUTH_STATES`. `WARM_WORKER_LIMIT` sets the resident warm pool, `MAX_ACTIVE_WORKERS` caps peak worker count, `WARM_STARTUP_CONCURRENCY` controls how many Camoufox workers cold-start at once, and `PER_ACCOUNT_CONCURRENCY` controls request slots per account. Saving these four in the console applies them to scheduling right away: lowering the peak limit closes the extra idle workers, and raising the resident count prewarms more.

### Port Configuration

The console allows passwordless loopback access by default. For remote management:

- Set `ADMIN_AUTH_ENABLED=true`, configure the administrator username and password, restart the process, and sign in on the separate `/login` page
- A session lasts 12 hours; with "Stay signed in for 30 days" checked it lasts 30 days and survives restarts
- Sign-out is supported; administrators share the instance's account pool, configuration, and logs

Public APIs use the separate `PROXY_API_KEY`.

Use an HTTPS reverse proxy that preserves `Host` and sets `X-Forwarded-Proto: https`. Console login settings can also be saved from the settings page and take effect after restarting the process.

- **Management UI and APIs**: Default port `2048`
- **Camoufox**: Local ports are allocated dynamically

## Advanced Features

### Proxy Configuration

HTTP, HTTPS, and SOCKS5 proxies are supported. For proxies that require authentication, include the credentials in the URL, for example `socks5://user:password@host:1080`:

1. Set the global proxy under Service Configuration. When it is empty, the system proxy is used: the `HTTPS_PROXY` environment variable first, then the Windows system proxy setting. The egress the running generation service actually uses is shown below the proxy field
2. The global proxy accepts several comma-separated proxies that form a proxy pool: each account without its own proxy is bound to the available proxy with the fewest accounts, and the binding is stored in the account state and survives restarts. When a proxy has 3 consecutive network errors or fails the periodic check, its accounts move to another available proxy, and it takes part in assignment again once a check succeeds. Service Configuration lists each proxy with its status and account count, and each account card shows that account's egress
3. Edit an account to set an account-specific proxy
4. The account proxy is used for login, WAA, and business requests
5. For authenticated proxies, the program gives Camoufox a credential-free SOCKS5 relay on the loopback address; the credentials are not written to the browser profile. While the relay runs, other local processes can also use the proxy through it

### Authentication File Management

Authentication files are stored in `auth/` by default:

| Path | Contents |
| --- | --- |
| `auth/<Google email>/account.json` | Account email, proxy, locale, timezone, and enabled state |
| `auth/<Google email>/storage-state.json` | Google cookies and authentication renewal material |
| `auth/<Google email>/runtime-state.json` | Benefit tier, model eligibility, cooldowns, and resource ownership |
| `auth/<Google email>/camoufox-cache/` | Web cache of that account's browser; can be deleted while the service is stopped |
| `auth/.leases/<Google email>.lock` | Cross-process lease for the account directory |
| `[user cache]/AIStudio2API/runtime-leases/<Google email>.lock` | WAA Worker lease for that email on the current computer |

The lowercase Google email is the account directory, management UI identity, and log source. `.leases` coordinates account-directory access, while the runtime lease in the user cache allows one WAA Worker per email on the current computer.

"Add account" on the Accounts page supports Chrome import, pasted cookies, antidetect browser import, isolated Camoufox login, and remote adding:

- Isolated Camoufox login opens a visible local window; when the server has no graphical display or the console is opened from another computer, the sign-in screen appears in a sign-in window in the console, and a reverse proxy must forward the WebSocket at `/api/login-view/`
- `ready` accounts can be edited, disabled, refreshed, and deleted; `auth_required` accounts can log in again
- Refresh all reads the benefit tier and model catalog of every enabled account immediately; the service also reads them every 10 minutes
- An account whose catalog has never been read shows an unknown tier and the reason for the last failed read
- When a read reports that AI Studio does not support the current egress region or account, accounts with an existing catalog show that reason as well

## Documentation

- [Development and contribution](docs/development.md)
- [Google AI Studio protocol specification](docs/protocol.md)
- [WAA implementation](docs/waa.md)
- [Build channel](docs/build.md)
- [Runtime logging](docs/logging.md)
- [Reusable reverse-engineering development guide](docs/reverse-engineering.md)

## Important Notes

### About Camoufox

This project uses [Camoufox](https://camoufox.com/) to reduce automation detection. Camoufox is based on Firefox and changes lower-level browser behavior to retain a realistic device fingerprint.

Go handles encoding, scheduling, streaming decode, and public protocols. WAA-protected `GenerateContent` requests are sent by the account's fingerprinted Camoufox page, preserving the native Firefox TLS/HTTP2 stack, headers, cookies, and page fingerprint.

With `WAA_BACKEND=go`, WAA runs inside the service process, emulates the Firefox page environment from the account fingerprint, and sends requests with Firefox request headers; Camoufox is neither downloaded nor started at runtime. Browser login on the Accounts page still uses Camoufox and prepares it on first login.

### Limitations

- **Client-Managed History**: Clients submit complete conversation context for Chat, Anthropic, and Gemini requests
- **AI Studio History**: API requests are not saved to website history; `TEMPORARY_CHAT=true` also disables autosave for the WAA prewarm page
- **Responses Sessions**: `previous_response_id` is stored only in the current process and is cleared on restart
- **Authentication Expiry**: Chrome imports retain OAuth renewal material and renew on Windows; when a Chrome import on Linux, macOS, or Docker expires, copy it from Windows again. When another account expires and the local Chrome is signed in to the same email, the service renews it with that email's material from Chrome and renews it as a Chrome import from then on; otherwise log in again on the Accounts page

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
| `service_starting` | The generation service is starting; retry shortly |
| No account is available | Add, enable, or log in to an account from Accounts |
| Camoufox preparation fails | Check access to GitHub Releases or set `CAMOUFOX_PATH` |
| Linux account warmup `exit status 255` | Install the Camoufox runtime libraries, see the apt command in “System Requirements” |
| Account shows “AI Studio 不支持当前出口地区或账户” (region or account not supported) | Use a proxy that exits in an [AI Studio supported region](https://aistudio.google.com/docs/available-regions) for that account or globally |

## Contributing

Issues and Pull Requests are welcome!

## Development Roadmap

- ✅ **TTS Support**: Adapted `gemini-2.5-flash/pro-preview-tts` speech generation models
- ✅ **Media Generation**: Supports Imagen 3, Veo 2, Nano Banana image/video generation
- ✅ **Documentation**: Update and optimize documentation in `docs/` directory
- ✅ **Go Refactoring**: Migrate core proxy service to Go for improved concurrency and reduced resource usage
- ✅ **Multi-Worker Load Balancing**: Support multi-Google account rotation pool for higher concurrency limits
- ✅ **Desktop App**: Windows, macOS, and Linux desktop apps with a window, system tray, and online update
- ✅ **API Playground**: chat, image, video, speech, music, and live pages with the same entry points and routes as AI Studio

### Pure Go WAA Runtime

- ✅ **Pure Go backend**: `WAA_BACKEND=go` runs the official interpreter and program inside the service process, emulating the Firefox page environment from each account fingerprint; it neither downloads nor starts Camoufox at runtime, while account login still uses Camoufox
- ✅ **Firefox engine details**: implement `Intl` formatting, the global resolution timing of regular-expression literals, and the Symbol key order of `RegExp.prototype`
