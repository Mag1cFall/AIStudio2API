<div align="center">

# AI Studio to OpenAI Compatible API

<p align="center">
  <a href="README.md">中文</a>
  &nbsp;|&nbsp;
  <a href="README_en.md"><b>English</b></a>
</p>

<p>
  <b>A High-Performance Python Proxy Server</b><br>
  Converts the Google AI Studio web interface into an OpenAI-compatible API
</p>

<p>
  🔄 Multi-Worker Concurrency &nbsp;•&nbsp;
  🖼️ Imagen 3 Image Generation &nbsp;•&nbsp;
  🎨 Nano Banana Image Generation<br>
  🎬 Veo 2 Video Generation &nbsp;•&nbsp;
  🎤 Gemini 2.5 TTS Speech Synthesis
</p>

<img src="docs/demo.gif" alt="Demo GIF" width="100%" />

<p align="center">
  <img src="docs/多worker并发和媒体模型支援.png" alt="Multi-Worker Concurrency & Media Model Support" width="80%" />
</p>

</div>

---

## 🚀 Features

- **OpenAI Compatible API**: Fully compatible with OpenAI format `/v1/chat/completions` endpoint
- **Multi-Worker Concurrency**: Supports multi-account concurrent processing for improved throughput and stability
- **TTS Speech Generation**: Supports Gemini 2.5 TTS models for single/multi-speaker audio generation
- **Image Generation**: Supports Imagen 3 and Gemini 2.5 Flash (Nano Banana) image generation
- **Video Generation**: Supports Veo 2 video generation, including image-to-video
- **Smart Model Switching**: Dynamically switch models in AI Studio via the `model` field
- **Anti-Fingerprint Detection**: Uses Camoufox browser to reduce detection risk
- **GUI Launcher**: Feature-rich **web** launcher for simplified configuration and management
- **Ollama Compatibility Layer**: Built-in `llm.py` provides Ollama format API compatibility
- **Modular Architecture**: Clear module separation design for easy maintenance
- **Modern Toolchain**: uv dependency management + full type support

## 📋 System Requirements

- **Python**: 3.12 (recommended)
- **Dependency Management**: [uv](https://docs.astral.sh/uv/)
- **Operating System**: Windows, macOS, Linux
- **Memory**: 2GB+ available memory recommended
- **Network**: Stable internet connection to access Google AI Studio

## 🛠️ Installation

### Method 1: One-Click Install (Recommended)

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

Then double-click `setup.bat` to run it. The script will automatically complete all installation steps.


Windows (PowerShell):
```powershell
.\setup.bat
```

Linux:
```bash
chmod +x setup.sh
./setup.sh
```

### Method 2: Manual Installation

#### 1. Install uv

Windows (PowerShell):
```powershell
powershell -c "irm https://astral.sh/uv/install.ps1 | iex"
```

macOS / Linux:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

Expected output:
```
PS C:\Users\2\Desktop\AIStudio2API> powershell -c "irm https://astral.sh/uv/install.ps1 | iex"
Downloading uv 0.9.11 (x86_64-pc-windows-msvc)
Installing to C:\Users\2\.local\bin
  uv.exe
  uvx.exe
  uvw.exe
everything's installed!

To add C:\Users\2\.local\bin to your PATH, either restart your shell or run:

    set Path=C:\Users\2\.local\bin;%Path%   (cmd)
    $env:Path = "C:\Users\2\.local\bin;$env:Path"   (powershell)
```
Please add it to your environment variables according to your path.

#### 2. Clone the Project

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

#### 3. Install Dependencies

```bash
uv sync
uv run camoufox fetch
uv run playwright install firefox
```

**Note**: The Camoufox browser (approximately 600MB) will be automatically downloaded during installation. This is a core component for anti-fingerprint detection. First-time installation may take some time, please be patient.

***

## 🚀 Quick Start

### First-time Use (Authentication Required)

1. **Start the GUI**:
   ```bash
   uv run python src/app_launcher.py
   ```

2. **Configure Proxy** (recommended):
   - Check "Enable Browser Proxy" in the GUI
   - Enter your proxy address (e.g., `http://127.0.0.1:7890`)

3. **Start Headed Mode for Authentication**:
   - Click "Start Headed Mode (New Terminal)"
   - Type `N` in the terminal to get a new authentication file
   - The browser will automatically open and navigate to AI Studio
   - Manually log in to your Google account
   - Ensure you're on the AI Studio homepage
   - Press Enter in the terminal to save authentication info

4. **After Authentication**:
   - Authentication info will be saved automatically
   - You can close the headed mode browser and terminal

### Daily Use (With Existing Authentication)

After authentication is saved, you can use headless mode:

1. Start the GUI:
   ```bash
   uv run python src/app_launcher.py
   ```

2. Click "Start Headless Mode" or "Virtual Display Mode"

3. The API service will run in the background, default port `2048`

### Quick Start Scripts

`start_cmd.bat`: Direct command-line startup.

`start_webui.bat`: Starts the web interface, auto-redirects or visit `http://127.0.0.1:9000`.

Wait for `ℹ️  INFO    | --- Queue Worker Started ---` to appear before using the API.


## 📡 API Usage

### OpenAI Compatible Interface

After starting the service, use the OpenAI-compatible API:

```bash
curl -X POST http://localhost:2048/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-pro",
    "messages": [
      {"role": "user", "content": "Hello, world!"}
    ]
  }'
```

### Client Configuration Example

Using Cherry Studio as an example:

1. Open Cherry Studio settings
2. Add a new model in the "Connection" section:
   - **API Host**: `http://127.0.0.1:2048/v1/`
   - **Model Name**: `gemini-2.5-pro` (or other AI Studio supported models)
   - **API Key**: Leave empty or enter any character like `123`

### TTS Speech Generation

Supports Gemini 2.5 Flash/Pro TTS models for single-speaker or multi-speaker audio generation:

#### Single-Speaker Example

```bash
curl -X POST http://localhost:2048/generate-speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-preview-tts",
    "contents": "Hello, this is a test.",
    "generationConfig": {
      "responseModalities": ["AUDIO"],
      "speechConfig": {
        "voiceConfig": {
          "prebuiltVoiceConfig": {"voiceName": "Kore"}
        }
      }
    }
  }'
```

#### Multi-Speaker Example

```bash
curl -X POST http://localhost:2048/generate-speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-preview-tts",
    "contents": "Joe: How are you?\nJane: I am fine, thanks!",
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
  }'
```

**Available Voices**: Zephyr, Puck, Charon, Kore, Fenrir, Leda, Orus, Aoede, Callirrhoe, Autonoe, Enceladus, Iapetus, and 18 more voices.

**Endpoints**:
- `POST /generate-speech`
- `POST /v1beta/models/{model}:generateContent` (compatible with official API)

**Response Format**: Audio data is returned as Base64-encoded WAV format in `candidates[0].content.parts[0].inlineData.data`.

### Image Generation (Imagen 3)

```bash
curl -X POST http://localhost:2048/generate-image \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A beautiful sunset over mountains",
    "model": "imagen-3.0-generate-002",
    "number_of_images": 1,
    "aspect_ratio": "16:9"
  }'
```

**Endpoint**: `POST /generate-image`

### Video Generation (Veo 2)

```bash
curl -X POST http://localhost:2048/generate-video \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A drone flying over a forest",
    "model": "veo-2.0-generate-001",
    "aspect_ratio": "16:9",
    "duration_seconds": 5
  }'
```

**Endpoint**: `POST /generate-video`

### Nano Banana (Gemini Image Generation)

```bash
curl -X POST http://localhost:2048/nano/generate \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-image",
    "contents": [{"parts": [{"text": "A cute cat wearing a tiny hat"}]}]
  }'
```

**Endpoint**: `POST /nano/generate`

**Detailed Documentation**: See [Media Generation Guide](docs/media-generation-guide.md)

### Ollama Compatibility Layer

The project also provides Ollama format API compatibility:

```bash
# Start Ollama compatible service
uv run python src/app_launcher.py
# Click "Start Local LLM Simulation Service" in the GUI config page

# Use Ollama format API
curl http://localhost:11434/api/tags
curl -X POST http://localhost:11434/api/chat \
  -d '{"model": "gemini", "messages": [{"role": "user", "content": "Hello"}]}'
```

## 🏗️ Project Architecture

```
AIStudio2API/
├── src/                         # Source code directory
│   ├── app_launcher.py          # GUI launcher
│   ├── launch_camoufox.py       # Command-line launcher
│   ├── server.py                # Main server
│   ├── manager.py               # WebUI manager
│   ├── api/                     # API processing modules
│   ├── browser/                 # Browser automation modules
│   ├── config/                  # Configuration management
│   ├── models/                  # Data models
│   ├── tts/                     # TTS Speech Generation modules
│   ├── media/                   # Media Generation modules (Imagen/Veo/Nano)
│   ├── proxy/                   # Streaming proxy
│   ├── worker/                  # Multi-Worker management module
│   ├── gateway.py               # Multi-Worker load balancing gateway
│   └── static/                  # Static resources
├── data/                        # Runtime data directory
│   ├── auth_profiles/           # Authentication files
│   ├── certs/                   # Certificate files
│   └── key.txt                  # API keys
├── llm/                         # Ollama compatibility layer
├── camoufox/                    # Camoufox scripts
├── docker/                      # Docker configuration
├── docs/                        # Detailed documentation
├── logs/                        # Log files
├── start_webui.bat              # WebUI startup script
├── start_cmd.bat                # Command-line startup script
├── setup.bat                    # Windows installation script
└── setup.sh                     # Linux/macOS installation script
```

## ⚙️ Configuration

### Environment Variables

Copy and edit the environment configuration file:

```bash
cp .env.example .env
# Edit .env file for custom configuration
```

### Port Configuration

- **FastAPI Service**: Default port `2048`
- **Camoufox Debug**: Default port `9222`
- **Streaming Proxy**: Default port `3120`
- **Ollama Compatible**: Default port `11434`

## 🔧 Advanced Features

### Proxy Configuration

Supports accessing AI Studio through proxy:

1. Enable "Browser Proxy" in the GUI
2. Enter proxy address (e.g., `http://127.0.0.1:7890`)
3. Click "Test" button to verify proxy connection

### Authentication File Management

- Authentication files are stored in `data/auth_profiles/` directory
- Supports saving and switching multiple authentication files
- Manage through the "Manage Auth Files" feature in the GUI

## 📚 Documentation

- [Installation Guide](docs/installation-guide.md)
- [Environment Configuration](docs/environment-configuration.md)
- [Authentication Setup](docs/authentication-setup.md)
- [API Usage Guide](docs/api-usage.md)
- [Multi-Worker Concurrency Mode](docs/multi-worker-guide.md)
- [Troubleshooting](docs/troubleshooting.md)

## ⚠️ Important Notes

### About Camoufox

This project uses [Camoufox](https://camoufox.com/) browser to avoid detection as an automation script. Camoufox is based on Firefox and disguises device fingerprints by modifying the underlying implementation.

### Limitations

- **Client-Managed History**: Proxy doesn't support in-UI editing; clients need to maintain full chat history
- **Parameter Support**: Supports `temperature`, `max_output_tokens`, `top_p`, `stop` parameters
- **Authentication Expiry**: Authentication files may expire; re-authentication required

## 🤝 Contributing

Issues and Pull Requests are welcome!

## 📅 Development Roadmap

- ✅ **TTS Support**: Adapted `gemini-2.5-flash/pro-preview-tts` speech generation models
- ✅ **Media Generation**: Supports Imagen 3, Veo 2, Nano Banana image/video generation
- **Unified Click Logic**: Extract `_safe_click` method to global `operations.py`, unify click operations across all controllers
- **Documentation**: Update and optimize documentation in `docs/` directory
- **One-Click Deployment**: Provide fully automated install and launch scripts for Windows/Linux/macOS
- **Docker Support**: Provide standard Dockerfile and Docker Compose orchestration files
- **Go Refactoring**: Migrate core proxy service to Go for improved concurrency and reduced resource usage
- **CI/CD Pipeline**: Establish GitHub Actions automated testing and build release process
- **Unit Testing**: Increase test coverage for core modules (especially browser automation)
- ✅ **Multi-Worker Load Balancing**: Support multi-Google account rotation pool for higher concurrency limits

