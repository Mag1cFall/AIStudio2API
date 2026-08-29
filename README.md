<div align="center">

# AI Studio to OpenAI, Anthropic & Gemini Compatible API

<p align="center">
  <a href="README.md"><b>中文</b></a>
  &nbsp;|&nbsp;
  <a href="README_en.md">English</a>
</p>

<p>
  <b>一个基于 Go 的高性能代理服务</b><br>
  将 Google AI Studio 网页协议转换为 OpenAI、Responses、Anthropic 和 Gemini 兼容 API
</p>

<p>
  多账户轮询 &nbsp;•&nbsp;
  Nano Banana 图片生成 &nbsp;•&nbsp;
  Google 工具<br>
  Veo 视频生成 &nbsp;•&nbsp;
  Gemini TTS 语音生成
</p>

</div>

---

## 特性

- **四套 API 协议**: 支持 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 和 Gemini GenerateContent
- **多账户运行**: 识别 Free、Pro、Ultra 与 Plus 权益，按模型实际资格、首事件速度和并发槽位调度
- **原生流式响应**: 实时输出正文、思考摘要、函数调用、Google 工具、媒体和 usage
- **TTS 语音生成**: 支持 Gemini TTS 模型的单/多说话人音频生成
- **图片生成**: 支持 Nano Banana 图片生成
- **视频生成**: 支持 Veo 视频生成和图片转视频
- **YouTube 输入**: 粘贴视频 URL 即可作为外部视频附件读取
- **智能模型切换**: 从 AI Studio 实时发现模型并按 `model` 字段路由
- **Google 工具**: 支持 Search、Image Search、URL Context、Code Execution 和 Maps
- **Files 与 Transcribe**: 支持文件上传、查询、内容读取、删除和音频转录
- **Live 与 Robotics**: 通过 WebSocket 支持文本、音频、JPEG、媒体结束、工具调用、恢复和中断
- **反指纹检测**: 使用 Camoufox 持有官方 WAA 生命周期，并为每个账户固定浏览器指纹与出口
- **图形界面启动器**: 通过网页管理账户、服务启停、实时日志、模型、请求和配置
- **模块化架构**: Go 负责协议、调度、API 与管理端，Camoufox 负责 WAA 运行时和隔离登录

## 系统要求

- **Windows Release 运行**: Windows 10 或更高版本、`aistudio2api.exe` 和 `start.bat`
- **源码运行**: Go 1.26、Node.js 24 和 npm
- **操作系统**: Windows、macOS、Linux
- **内存**: 单账户建议 2GB+ 可用内存，每个常驻预热账户约增加 0.6GB
- **网络**: 稳定的互联网连接访问 Google AI Studio

## 安装步骤

### 方式一：Windows 一键启动（推荐）

```powershell
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
copy .env.example .env
```

然后双击运行 `start.bat`。Windows PowerShell 也可以直接执行：

```powershell
.\start.bat
```

已有 `aistudio2api.exe` 时脚本立即运行；源码目录缺少可执行文件时，脚本自动安装前端依赖并构建前端与 Go 程序。

首次启动会自动下载当前平台的 Camoufox 到 `runtime/camoufox/`。也可以通过环境变量 `CAMOUFOX_PATH` 指定已有可执行文件。

### 方式二：Linux 与 macOS 源码构建

#### 1. 安装依赖

- Go 1.26
- Node.js 24 与 npm

#### 2. 克隆项目

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
cp .env.example .env
```

#### 3. 构建并运行

```bash
cd web
npm ci
npm run build
cd ..
go build -o aistudio2api ./cmd/aistudio2api
chmod +x ./aistudio2api
./aistudio2api
```

Linux 与 macOS 首次运行同样会自动准备对应平台的 Camoufox。

## 快速开始

### 首次使用（需要认证）

1. **准备首个账户**:

   Windows 可以导入本机 Chrome 账户：

   ```powershell
   start.bat setup
   ```

   Linux 与 macOS 使用隔离 Camoufox 登录：

   ```bash
   ./aistudio2api setup --login --label name@gmail.com
   ```

   `--label` 填写登录的 Google 邮箱。账户保存到 `.env` 中 `AISTUDIO_AUTH_STATES` 指向的目录；语言和时区默认读取当前电脑设置，也可以通过 `--locale`、`--timezone` 指定。

2. **启动图形界面**:
   - Windows 双击 `start.bat`
   - Linux 与 macOS 运行 `./aistudio2api`
   - 浏览器自动打开 `http://127.0.0.1:2048`
   - 页面初始状态为 `STOPPED`，默认显示“日志”页面

3. **添加其他账户**:
   - 打开“账户”页面并点击“新增账户”
   - 填写 Google 邮箱、代理、语言和时区
   - 提交后会打开独立 Camoufox 窗口，在其中登录 Google 并进入 AI Studio
   - 登录完成后账户自动保存

4. **启动 API**:
   - 点击“启动服务”启动数据面
   - 状态依次显示 `LAUNCHING` 和 `RUNNING`；`LAUNCHING` 期间可以点击“停止服务”取消启动
   - 在“日志”页面确认账户、模型和请求状态
   - API 默认监听 `http://127.0.0.1:2048`

账户操作随状态显示：

| 账户状态 | 可用操作 |
| --- | --- |
| `ready` | 编辑、停用、验证、删除 |
| `disabled` | 编辑、启用、删除 |
| `auth_required` | 编辑、停用、重新登录、验证、删除 |

“重新登录”只在账户状态为 `auth_required` 时显示。

### 日常使用（已有认证）

1. Windows 双击 `start.bat`；Linux 与 macOS 运行 `./aistudio2api`
2. 点击“启动服务”启用 API
3. 点击“停止服务”会取消正在进行的启动或活动请求并关闭 WAA Worker，管理页面与日志保持可用
4. 再次点击“启动服务”即可恢复 API

停止后再次启动会读取最新 `.env` 生成服务配置；管理页面地址和 `PROXY_API_KEY` 在管理进程重启后生效。

在启动窗口按 `Ctrl+C` 或关闭窗口会退出整个管理进程。关闭浏览器标签页不会停止管理进程。

### 快速启动

`start.bat`：启动管理进程并自动打开网页。

`start.bat -open-ui=false`：启动管理进程但不自动打开网页。

`start.bat setup`：扫描本机 Chrome 账户；也可使用 `--email` 或 `--profile` 选择明确的 Chrome 账户。隔离登录使用 `start.bat setup --login --label name@gmail.com`；文件导入使用 `start.bat setup --storage-state <file> --label name@gmail.com`。

## API 使用

### OpenAI 兼容接口

服务启动后，可以直接使用 OpenAI Chat Completions：

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

### 客户端配置示例

| 协议 | Base URL | API key |
| --- | --- | --- |
| OpenAI Chat / Responses | `http://127.0.0.1:2048/v1` | `.env` 中的 `PROXY_API_KEY` |
| Anthropic Messages | `http://127.0.0.1:2048` | `.env` 中的 `PROXY_API_KEY` |
| Gemini | `http://127.0.0.1:2048` | `.env` 中的 `PROXY_API_KEY` |

模型名称从 `GET /v1/models` 或 `GET /v1beta/models` 读取。

以 Cherry Studio 为例：

1. 打开 Cherry Studio 设置
2. 新增 OpenAI 兼容提供商
3. API 主机地址填写 `http://127.0.0.1:2048/v1`
4. API 密钥填写 `.env` 中的 `PROXY_API_KEY`
5. 从 `/v1/models` 获取模型，或手动添加 `gemini-3.6-flash`、`gemini-3.7-flash`

主要端点：

| 能力 | 端点 |
| --- | --- |
| 模型 | `GET /v1/models`、`GET /v1beta/models` |
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Files | `POST /v1/files`、`GET /v1/files/{id}`、`GET /v1/files/{id}/content`、`DELETE /v1/files/{id}` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens` |
| 图片 | `POST /v1/images/generations` |
| 语音 | `POST /v1/audio/speech` |
| 转录 | `POST /v1/audio/transcriptions` |
| 音乐 | Gemini `generateContent` + `responseModalities: ["AUDIO"]` |
| 视频 | `POST /v1/videos`、`GET /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Gemini 视频 | `POST /v1beta/models/{model}:predictLongRunning`、`GET /v1beta/operations/{id}` |
| Live / Robotics | `GET /v1/live`、`GET /v1/robotics/stream` |

四套生成接口均可按各自协议字段启用 Search、Image Search、URL Context、Code Execution 和 Maps。Files、Transcribe、Live、Robotics 的请求与事件格式见 [Google AI Studio 协议规范](docs/protocol.md)。

### TTS 语音生成

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

多说话人语音可以通过 Gemini `generateContent` 的 `multiSpeakerVoiceConfig` 配置。

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

可用语音由实时模型目录中的 `capability_options.voices` 返回。

### 图片生成 (Nano Banana)

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

### 视频生成 (Veo)

```bash
curl http://127.0.0.1:2048/v1/videos \
  -H "Authorization: Bearer 123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "veo-3.1-fast-generate-preview",
    "prompt": "A drone flying over a forest"
  }'
```

创建操作后通过 `GET /v1/videos/{id}` 查询状态，通过 `GET /v1/videos/{id}/content` 下载结果。

## 模型

模型目录会随 AI Studio 更新，客户端从 `/v1/models` 或 `/v1beta/models` 读取当前值。下表保留目录结构示例，模型 ID、限制和方法以运行时结果为准：

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

公开端点实现标准 `generateContent`、`countTokens` 和 `predictLongRunning`。`/v1/models` 与 `/v1beta/models` 原样汇总各账户实时上游目录；调度按目录明确提供的模型 ID、方法、能力字段和账户当前运行状态选择账户。

## 项目架构

```text
AIStudio2API/
├── cmd/aistudio2api/        # 薄入口
├── internal/app/            # 命令、管理监听、生成服务生命周期与调度
├── internal/setup/          # 账户导入和独立登录 CLI
├── internal/aistudio/       # AI Studio 协议、认证、模型与媒体
├── internal/api/            # OpenAI、Responses、Anthropic 与 Gemini 适配
├── internal/chromeauth/     # Windows Chrome 与 DBSC 导入
├── internal/camoufoxnative/ # Camoufox BiDi、登录与 WAA Worker
├── internal/webui/          # 内嵌前端产物
├── web/                     # Vue 3 + TypeScript 管理页面
├── docs/                    # 开发文档与协议规范
└── start.bat                # Windows 一键启动入口
```

## 配置说明

### 环境变量配置

复制并编辑环境配置文件：

```bash
cp .env.example .env
```

| 变量 | 默认值 | 作用 |
| --- | --- | --- |
| `AISTUDIO_AUTH_STATES` | `auth` | 账户文件、目录或多个逗号分隔路径 |
| `LISTEN_ADDR` | `127.0.0.1:2048` | 管理页面与 API 监听地址 |
| `PROXY_API_KEY` | 空 | 公开 API key |
| `PROXY` | 空 | Chrome 导入、登录和账户默认使用的 HTTP、HTTPS 或 SOCKS5 代理 |
| `INIT_TIMEOUT` | `2m` | 单账户 WAA 初始化超时 |
| `REQUEST_TIMEOUT` | `5m` | 单次请求最大执行时间 |
| `WARM_WORKER_LIMIT` | `5` | 常驻预热账户数 |
| `MAX_ACTIVE_WORKERS` | `10` | 高峰期最多同时运行的 Worker 数 |
| `WARM_STARTUP_CONCURRENCY` | `2` | 同时初始化的预热账户数 |
| `PER_ACCOUNT_CONCURRENCY` | `2` | 单账号同时执行的请求数 |
| `TEMPORARY_CHAT` | `false` | WAA 预热页是否使用临时对话 |

服务启动时会载入 `AISTUDIO_AUTH_STATES` 中的全部账户；`WARM_WORKER_LIMIT` 控制常驻预热规模，`MAX_ACTIVE_WORKERS` 控制峰值 Worker 上限，`WARM_STARTUP_CONCURRENCY` 控制启动预热并发，`PER_ACCOUNT_CONCURRENCY` 控制单账户请求槽位。

### 端口配置

- **管理页面与 API**: 默认端口 `2048`
- **Camoufox**: 由程序动态分配本机端口

## 高级功能

### 代理配置

支持通过无认证信息的 HTTP、HTTPS 或 SOCKS5 代理访问 AI Studio：

1. 在“服务配置”中设置全局代理
2. 在“账户”页面编辑单个账户时可以设置账户专用代理
3. 账户代理同时用于登录、WAA 与业务请求

### 认证文件管理

认证文件默认存储在 `auth/` 目录：

| 路径 | 内容 |
| --- | --- |
| `auth/<Google 邮箱>/account.json` | 账户邮箱、代理、语言、时区和启用状态 |
| `auth/<Google 邮箱>/storage-state.json` | Google Cookie 与认证续签材料 |
| `auth/<Google 邮箱>/runtime-state.json` | 权益等级、模型资格、冷却状态与资源所属账户 |
| `auth/.leases/<Google 邮箱>.lock` | 同一账户目录的跨进程占用锁 |
| `[用户缓存]/AIStudio2API/runtime-leases/<Google 邮箱>.lock` | 当前电脑上该邮箱的 WAA Worker 占用锁 |

账户邮箱同时作为目录名、管理页面标识和日志来源，统一使用小写形式。`.leases` 协调账户目录读写，用户缓存中的 runtime lease 保证同一邮箱在当前电脑上只有一个 WAA Worker。

新增账户会启动隔离 Camoufox 登录。`ready` 账户可以编辑、停用、验证和删除，`auth_required` 账户可以重新登录。

## 详细文档

- [开发与贡献](docs/development.md)
- [Google AI Studio 协议规范](docs/protocol.md)
- [运行日志说明](docs/logging.md)
- [可复用逆向开发指南](docs/reverse-engineering.md)

## 重要提示

### 关于 Camoufox

本项目使用 [Camoufox](https://camoufox.com/) 浏览器来降低被检测为自动化脚本的风险。Camoufox 基于 Firefox，通过修改底层实现来保持真实的设备指纹。

Go 直接处理业务请求，业务传输使用与 Camoufox 对齐的 Firefox TLS/HTTP2 配置；Camoufox 用于官方 WAA 初始化、fresh proof 和隔离账户登录。

### 使用限制

- **客户端管理历史**: Chat、Anthropic 和 Gemini 请求由客户端提交完整对话上下文
- **AI Studio 历史**: API 请求不保存到官网历史；`TEMPORARY_CHAT=true` 还会关闭 WAA 预热页的自动保存
- **Responses 会话**: `previous_response_id` 仅在当前进程内保存，重启后不会保留
- **认证有效期**: Chrome 导入账户保留 DBSC 续签材料；隔离登录账户失效后在账户页重新登录

## 故障排除

### Windows 端口被系统保留

如果启动时提示 `LISTEN_ADDR` 配置的端口被占用，任务管理器中又找不到占用进程，可能是 Hyper-V、WSL2 或 Docker 的 NAT 服务保留了端口段。

以下命令需要在管理员权限的 PowerShell 或 CMD 中运行。

#### 1. 查看被 Windows 保留的端口范围

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

如果 `2048` 落在输出的 `Start Port` 和 `End Port` 范围内，可以修改 `LISTEN_ADDR`，或重启 WinNAT 服务后再次检查：

```powershell
net stop winnat
net start winnat
```

端口空闲后，也可以将 `2048` 加入持久保留：

```powershell
netsh int ipv4 add excludedportrange protocol=tcp startport=2048 numberofports=1 store=persistent
```

常见运行状态：

| 状态 | 处理方法 |
| --- | --- |
| 页面未自动打开 | 手动打开 `.env` 中 `LISTEN_ADDR` 对应的地址 |
| `service_stopped` | 在管理页面点击“启动服务” |
| 没有可用账户 | 在账户页新增、启用或重新登录账户 |
| Camoufox 准备失败 | 检查 GitHub Release 访问，或设置 `CAMOUFOX_PATH` |

## 贡献

欢迎提交 Issue 和 Pull Request！

## 开发计划

- ✅ **TTS 支持**: 已适配 `gemini-2.5-flash/pro-preview-tts` 语音生成模型
- ✅ **媒体生成**: 已支持 Imagen 3、Veo 2、Nano Banana 图片/视频生成
- ✅ **文档完善**: 更新并优化 `docs/` 目录下的详细使用文档与 API 规范
- **一键部署**: 提供 Windows/Linux/macOS 的全自动化安装与启动脚本
- **Docker 支持**: 提供标准 Dockerfile 及 Docker Compose 编排文件，简化部署流程
- ✅ **Go 语言重构**: 将核心代理服务迁移至 Go 以提升并发性能与降低资源占用
- ✅ **多Worker负载均衡**: 支持多 Google 账号轮询池，提高并发限额与稳定性

### 纯协议 WAA 运行时

目标是完整逆向并复现 WAA VM，使用 Go 执行 dynamic program、interpreter、challenge、persistent state、snapshot 与 proof 全链路。最终生产运行期只保留 Go 协议实现，无 Camoufox 进程、DOM 环境和 AI Studio 前端 bundle 依赖。

| 阶段 | 交付内容 |
| --- | --- |
| 协议固化 | 归档 dynamic program、challenge、状态迁移、snapshot 与 proof 的完整输入输出，建立可重复验证的协议样本 |
| Go 执行器 | 实现 dynamic program 加载、interpreter、challenge 求值、persistent state、snapshot 恢复与 proof 生成，并与协议样本逐项一致 |
| 运行时切换 | 将账户初始化和 proof 刷新接入 Go 原生运行时，通过全部已知 challenge 验证后删除 Camoufox、DOM 与前端 bundle 运行链路 |
