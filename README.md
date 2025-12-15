<div align="center">

# AI Studio to OpenAI Compatible API

<p align="center">
  <a href="README.md"><b>中文</b></a>
  &nbsp;|&nbsp;
  <a href="README_en.md">English</a>
</p>

<p>
  <b>一个基于 Python 的高性能代理服务</b><br>
  将 Google AI Studio 网页界面转换为 OpenAI 兼容格式 API
</p>

<p>
  🔄 多Worker并发 &nbsp;•&nbsp;
  🖼️ Imagen 3 图片生成 &nbsp;•&nbsp;
  🎨 Nano Banana 图片生成<br>
  🎬 Veo 2 视频生成 &nbsp;•&nbsp;
  🎤 Gemini 2.5 TTS 语音生成
</p>

<img src="docs/img/demo.gif" alt="Demo GIF" width="100%" />

<!-- <p align="center">
  <img src="docs/img/多worker并发和媒体模型支援.png" alt="多Worker并发与媒体模型支援" width="80%" />
</p> -->

</div>

---

## 🚀 特性

- **OpenAI 兼容 API**: 完全兼容 OpenAI 格式的 `/v1/chat/completions` 端点
- **多 Worker 并发**: 支持多账号并发处理，提升吞吐量和稳定性
- **TTS 语音生成**: 支持 Gemini 2.5 TTS 模型的单/多说话人音频生成
- **图片生成**: 支持 Imagen 3 和 Gemini 2.5 Flash (Nano Banana) 图片生成
- **视频生成**: 支持 Veo 2 视频生成，包含图片转视频功能
- **智能模型切换**: 通过 `model` 字段动态切换 AI Studio 中的模型
- **反指纹检测**: 使用 Camoufox 浏览器降低被检测风险
- **图形界面启动器**: 功能丰富的 **网页** 启动器，简化配置和管理
- **Ollama 兼容层**: 内置 `llm.py` 提供 Ollama 格式 API 兼容
- **模块化架构**: 清晰的模块分离设计，易于维护
- **现代化工具链**: uv 依赖管理 + 完整类型支持

## 📋 系统要求

- **Python**: 3.12 (推荐)
- **依赖管理**: [uv](https://docs.astral.sh/uv/)
- **操作系统**: Windows, macOS, Linux
- **内存**: 建议 2GB+ 可用内存
- **网络**: 稳定的互联网连接访问 Google AI Studio

## 🛠️ 安装步骤

### 方式一：一键安装（推荐）

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

然后双击运行 `setup.bat`，脚本将自动完成所有安装步骤。

Windows (PowerShell):
```powershell
.\setup.bat
```

Linux:
```bash
chmod +x setup.sh
./setup.sh
```

### 方式二：手动安装

#### 1. 安装 uv

Windows (PowerShell):
```powershell
powershell -c "irm https://astral.sh/uv/install.ps1 | iex"
```

macOS / Linux:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

预期输出：
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
请按照您的路径将其添加到环境变量。

#### 2. 克隆项目

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

#### 3. 安装依赖

```bash
uv sync
uv run camoufox fetch
uv run playwright install firefox
```

**注意**: 安装过程中会自动下载和安装 Camoufox 浏览器（约 600MB），这是项目的核心组件，用于反指纹检测。首次安装可能需要较长时间，请耐心等待。

***

## 🚀 快速开始

### 首次使用（需要认证）

1. **启动图形界面**:
   ```bash
   uv run python src/app_launcher.py
   ```

2. **配置代理**（建议）:
   - 在 GUI 中勾选"启用浏览器代理"
   - 输入您的代理地址（如`http://127.0.0.1:7890`）

3. **启动有头模式进行认证**:
   - 点击"启动有头模式 (新终端)"
   - **命令行终端**内输入`N`，获取新的认证文件
   - 命令行终端指`start_webui.bat`启动的终端，或者您运行`uv run python app_launcher.py`的终端
   - 浏览器会自动打开并导航到 AI Studio
   - 手动登录您的 Google 账号
   - 确保进入 AI Studio 主页
   - 在命令行终端按回车键保存认证信息
   - 认证文件保存情况会在日志里输出，命令行内不会输出内容

4. **认证完成后**:
   - 认证信息会自动保存
   - 可以关闭有头模式的浏览器和终端

### 日常使用（已有认证）

认证保存后，可以使用无头模式：

1. 启动图形界面:
   ```bash
   uv run python src/app_launcher.py
   ```

2. 点击「启动无头模式」或 「虚拟显示模式」

3. API 服务将在后台运行，默认端口 `2048`

### 快速启动

`start_cmd.bat`：命令行直接启动。
```
 - --- 请选择启动模式 (未通过命令行参数指定) ---
  请输入启动模式 ([1] 无头模式, [2] 调试模式; 默认: 1 headless模式，15秒超时):
```

`start_webui.bat`：
启动前端界面，自动跳转或访问`http://127.0.0.1:9000`进行后续使用，推荐。

等待出现`ℹ️  INFO    | --- 队列 Worker 已启动 ---`后，即可开始使用API。


## 📡 API 使用

### OpenAI 兼容接口

服务启动后，可以使用 OpenAI 兼容的 API：

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

### 客户端配置示例

以 Cherry Studio 为例：

1. 打开 Cherry Studio 设置
2. 在"连接"部分添加新模型:
   - **API 主机地址**: `http://127.0.0.1:2048/v1/`
   - **模型名称**: `gemini-2.5-pro` (或其他 AI Studio 支持的模型)
   - **API 密钥**: 留空或输入任意字符，如`123`

### TTS 语音生成

支持 Gemini 2.5 Flash/Pro TTS 模型进行单说话人或多说话人音频生成：

#### 单说话人示例

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

#### 多说话人示例

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

**可用语音**: Zephyr, Puck, Charon, Kore, Fenrir, Leda, Orus, Aoede, Callirrhoe, Autonoe, Enceladus, Iapetus 等 30 种。

**端点**:
- `POST /generate-speech`
- `POST /v1beta/models/{model}:generateContent` (兼容官方 API)

**返回格式**: 音频数据以 Base64 编码的 WAV 格式在 `candidates[0].content.parts[0].inlineData.data` 中返回。

### 图片生成 (Imagen 3)

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

**端点**: `POST /generate-image`

### 视频生成 (Veo 2)

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

**端点**: `POST /generate-video`

### Nano Banana (Gemini 图片生成)

```bash
curl -X POST http://localhost:2048/nano/generate \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-image",
    "contents": [{"parts": [{"text": "A cute cat wearing a tiny hat"}]}]
  }'
```

**端点**: `POST /nano/generate`

**详细文档**: 参见 [媒体生成指南](docs/media-generation-guide.md)

### Ollama 兼容层

项目还提供 Ollama 格式的 API 兼容：

```bash
# 启动 Ollama 兼容服务
uv run python src/app_launcher.py
# 在 GUI 的配置页面中点击"启动本地LLM模拟服务"

# 使用 Ollama 格式 API
curl http://localhost:11434/api/tags
curl -X POST http://localhost:11434/api/chat \
  -d '{"model": "gemini", "messages": [{"role": "user", "content": "Hello"}]}'
```

## 🏗️ 项目架构

```
AIStudio2API/
├── src/                         # 源码目录
│   ├── app_launcher.py          # 图形界面启动器
│   ├── launch_camoufox.py       # 命令行启动器
│   ├── server.py                # 主服务器
│   ├── manager.py               # WebUI 管理器
│   ├── api/                     # API 处理模块
│   ├── browser/                 # 浏览器自动化模块
│   ├── config/                  # 配置管理
│   ├── models/                  # 数据模型
│   ├── tts/                     # TTS 语音生成模块
│   ├── media/                   # 媒体生成模块 (Imagen/Veo/Nano)
│   ├── proxy/                   # 流式代理
│   ├── worker/                  # 多Worker管理模块
│   ├── gateway.py               # 多Worker负载均衡网关
│   └── static/                  # 静态资源
├── data/                        # 运行时数据目录
│   ├── auth_profiles/           # 认证文件
│   ├── certs/                   # 证书文件
│   └── key.txt                  # API 密钥
├── llm/                         # Ollama 兼容层
├── camoufox/                    # Camoufox 脚本
├── docker/                      # Docker 配置
├── docs/                        # 详细文档
├── logs/                        # 日志文件
├── start_webui.bat              # WebUI 启动脚本
├── start_cmd.bat                # 命令行启动脚本
├── setup.bat                    # Windows 安装脚本
└── setup.sh                     # Linux/macOS 安装脚本
```

## ⚙️ 配置说明

### 环境变量配置

复制并编辑环境配置文件：

```bash
cp .env.example .env
# 编辑 .env 文件进行自定义配置
```

### 端口配置

- **FastAPI 服务**: 默认端口 `2048`
- **Camoufox 调试**: 默认端口 `9222`
- **流式代理**: 默认端口 `3120`
- **Ollama 兼容**: 默认端口 `11434`

## 🔧 高级功能

### 代理配置

支持通过代理访问 AI Studio：

1. 在 GUI 中启用"浏览器代理"
2. 输入代理地址（如 `http://127.0.0.1:7890`）
3. 点击"测试"按钮验证代理连接

### 认证文件管理

- 认证文件存储在 `data/auth_profiles/` 目录
- 支持多个认证文件的保存和切换
- 通过 GUI 的"管理认证文件"功能进行管理

## 📚 详细文档

- [安装指南](docs/installation-guide.md)
- [环境变量配置](docs/environment-configuration.md)
- [认证设置](docs/authentication-setup.md)
- [API 使用指南](docs/api-usage.md)
- [多Worker并发模式](docs/multi-worker-guide.md)
- [故障排除](docs/troubleshooting.md)

## ⚠️ 重要提示

### 关于 Camoufox

本项目使用 [Camoufox](https://camoufox.com/) 浏览器来避免被检测为自动化脚本。Camoufox 基于 Firefox，通过修改底层实现来伪装设备指纹，提供更好的隐蔽性。

### 使用限制

- **客户端管理历史**: 代理不支持 UI 内编辑，客户端需要维护完整的聊天记录
- **参数支持**: 支持 `temperature`、`max_output_tokens`、`top_p`、`stop` 等参数
- **认证有效期**: 认证文件可能会过期，需要重新进行认证流程

## 🔍 故障排除

### Windows 端口被系统保留

如果启动时出现 `端口 30XX (主机 0.0.0.0) 当前被占用` 但任务管理器中找不到占用进程，这通常是 Windows 的 Hyper-V/WSL2/Docker 的 NAT 服务随机保留了端口段。

> ⚠️ **以下所有指令需要在管理员权限的 PowerShell 或 CMD 中运行**

#### 1. 查看被 Windows 保留的端口范围

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

如果 Worker 使用的端口（如 3001-3008）落在输出的 `Start Port` 和 `End Port` 范围内，即为此问题。

#### 2. 临时解决（重启 WinNAT 服务）

```powershell
net stop winnat
net start winnat
```

重启后再次执行步骤1查看，端口范围通常会变化并释放您需要的端口。

#### 3. 永久解决（将常用端口加入保留白名单）

趁端口空闲时，将开发常用端口永久标记为管理员保留，防止 Windows 再次占用：

```powershell
netsh int ipv4 add excludedportrange protocol=tcp startport=3000 numberofports=20 store=persistent
```

成功后列表中会出现带 `*` 标记的条目，表示该范围受永久保护。

更多问题排除方案请参阅 [故障排除文档](docs/troubleshooting.md)。

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📅 开发计划

- ✅ **TTS 支持**: 已适配 `gemini-2.5-flash/pro-preview-tts` 语音生成模型
- ✅ **媒体生成**: 已支持 Imagen 3、Veo 2、Nano Banana 图片/视频生成
- **点击逻辑统一**: 将 `_safe_click` 方法提取到全局 `operations.py`，统一所有控制器的点击操作
- **文档完善**: 更新并优化 `docs/` 目录下的详细使用文档与 API 规范
- **一键部署**: 提供 Windows/Linux/macOS 的全自动化安装与启动脚本
- **Docker 支持**: 提供标准 Dockerfile 及 Docker Compose 编排文件，简化部署流程
- **Go 语言重构**: 将核心代理服务迁移至 Go 以提升并发性能与降低资源占用
- **CI/CD 流水线**: 建立 GitHub Actions 自动化测试与构建发布流程
- **单元测试**: 增加核心模块（特别是浏览器自动化部分）的测试覆盖率
- ✅ **多Worker负载均衡**: 支持多 Google 账号轮询池，提高并发限额与稳定性 (这项或许不可能实现) (fix:2025/12/09 这项已实现)