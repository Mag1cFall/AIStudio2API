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
  Playground + Build 双额度通道 &nbsp;•&nbsp;
  Build 代码助手对话通道 &nbsp;•&nbsp;
  多账户高并发<br>
  Camoufox 与纯 Go 双 WAA 后端 &nbsp;•&nbsp;
  Claude Code、Codex 等 agent 客户端 &nbsp;•&nbsp;
  Nano Banana、Veo、TTS 与 Omni
</p>

</div>

<table>
  <tr>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/accounts-dark.webp"><img src="docs/images/accounts-light.webp" alt="账户"></picture><br><b>账户</b>：多账户的权益、出口与状态，添加、登录、刷新都在这里</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/usage-dark.webp"><img src="docs/images/usage-light.webp" alt="用量"></picture><br><b>用量</b>：请求数、成功率、token、费用估算与首字延迟，按模型、账户、通道筛选</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/logs-dark.webp"><img src="docs/images/logs-light.webp" alt="日志"></picture><br><b>日志</b>：每个请求的模型、账户、token、速度与首字时间</td>
  </tr>
  <tr>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/models-dark.webp"><img src="docs/images/models-light.webp" alt="模型目录"></picture><br><b>模型目录</b>：按各账户实时目录合并的模型与能力</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/settings-dark.webp"><img src="docs/images/settings-light.webp" alt="设置"></picture><br><b>设置</b>：<code>.env</code> 的配置项都能在页面修改</td>
    <td width="33%"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/image-dark.webp"><img src="docs/images/image-light.webp" alt="图片生成"></picture><br><b>图片生成</b>：Nano Banana 文生图与改图，运行设置与官网一致</td>
  </tr>
  <tr>
    <td colspan="3"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/chat-dark.webp"><img src="docs/images/chat-light.webp" alt="API 试用页：对话、思考过程、公式表格与代码块" width="100%"></picture><br><b>API 试用页</b>：官网同款对话页，思考过程、公式表格与代码块</td>
  </tr>
</table>

**三步开始使用**：下载 [Releases](https://github.com/Mag1cFall/AIStudio2API/releases) 并启动 → 在“账户”页添加 Google 账户 → 点击“启动服务”，把 `http://127.0.0.1:2048` 作为 OpenAI、Anthropic 或 Gemini 客户端的 Base URL

---

## 核心能力

- **双额度通道**: 每个账户同时拥有 Playground 与 Build 应用代理两份独立额度，`UPSTREAM_CHANNELS` 可单独或同时启用；一个通道触发限额后，同一账户由另一个通道继续
- **Build 对话模型**: Build 对话面板可选的模型各提供一个 `<模型>-build`（如 `gemini-3.8-flash-build`），由 Build 写代码助手连同其系统提示词回复，用量计入 Build 对话额度（免费请求或 Google AI 订阅额度）。⚠️ 这类模型的每次请求都带有 Build 写代码助手自带的系统提示词注入，回答风格、格式与身份会受其影响，请按用途自行评估
- **多账户高并发**: 识别 Free、Pro、Ultra 与 Plus 权益，按实时模型目录在账户间轮询或优先复用
- **两种 WAA 后端**: 默认由 Camoufox 持有官方 WAA 生命周期；设置 `WAA_BACKEND=go` 后由纯 Go 生成官方 proof，运行时不下载、不启动浏览器
- **四套 API 协议**: OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 与 Gemini GenerateContent；Responses 支持 `/v1/responses/compact` 与 `compaction`、`compaction_trigger` 输入项的上下文压缩
- **主流 agent 客户端**: 支持 Claude Code、Codex、OpenCode、pi、omp、OpenClaw、Hermes 的文件读写工具调用，Claude Code、Codex、omp 的原生联网搜索可直接使用

## 特性

- **原生流式响应**: 实时输出正文、思考摘要、函数调用、Google 工具、媒体和 usage
- **函数工具**: 四协议支持工具调用与续接、工具选择和严格参数校验；支持联合类型与开放参数定义
- **TTS 语音生成**: 支持 Gemini TTS 模型的单/多说话人音频生成
- **图片生成**: 支持 Nano Banana 图片生成
- **视频生成**: 支持 Veo 视频生成和图片转视频；Gemini Omni 通过四套生成接口接收文本、图片与视频输入，输出文本与 MP4 视频
- **Deep Research**: Pro 与 Ultra 账户可通过四套生成接口与 Interactions 的 `agent` 字段调用 Deep Research，输出研究过程、带引用的报告与图表
- **YouTube 输入**: 粘贴视频 URL 即可作为外部视频附件读取
- **智能模型切换**: 从 AI Studio 实时发现模型并按 `model` 字段路由
- **Google 工具**: 支持 Search、Image Search、URL Context、Code Execution 和 Maps
- **Files 与 Transcribe**: 支持文件上传、查询、内容读取、删除和音频转录
- **Live 与 Robotics**: 通过 WebSocket 支持文本、音频、JPEG、媒体结束、工具调用、恢复和中断
- **反指纹检测**: 使用 Camoufox 持有官方 WAA 生命周期，并为每个账户固定浏览器指纹与出口
- **图形界面启动器**: 通过网页管理账户、服务启停、实时日志、模型、请求、用量统计和配置，界面提供深色与浅色两种配色
- **桌面版**: Windows、macOS 与 Linux 桌面应用在窗口中显示管理页面，关闭窗口后在系统托盘继续运行
- **在线更新**: 管理端“版本”页显示最新发行版本与更新说明，Windows 与 macOS 桌面版、Linux 与 macOS 命令行版一键下载、校验并重启到新版本，Linux 桌面版提示下载新安装包
- **模块化架构**: Go 负责协议、调度、API 与管理端，Camoufox 负责 WAA 运行时和隔离登录

## 系统要求

- **Windows Release 运行**: Windows 10 或更高版本；系统没有 WebView2 运行时时，桌面版在默认浏览器中打开管理页面
- **macOS 桌面版**: macOS 12 或更高版本，Apple 芯片与 Intel 通用
- **Linux 桌面版**: GTK 4 与 WebKitGTK 6.0，例如 Ubuntu 24.04、Debian 13 或更新版本；deb 安装时一并安装这些依赖与 Camoufox 运行库，AppImage 另需下一行的 Camoufox 运行库
- **Linux Release 运行**: 解压 `linux-amd64.tar.gz` 后运行 `./aistudio2api`，Camoufox 需要 Firefox 系运行库，Debian/Ubuntu 执行 `sudo apt install libgtk-3-0 libasound2 libnss3 libdbus-glib-1-2 libxtst6 libxrandr2 libgbm1 libxkbcommon0 libpango-1.0-0 libcairo2 libxcomposite1 libxdamage1 libxfixes3 xvfb`
- **源码运行**: Go 1.26.0+、Node.js 22.18+、24.11+ 或 26+，以及配套 npm
- **操作系统**: Windows、macOS、Linux
- **内存**: 单账户建议 2GB+ 可用内存，每个常驻预热账户约增加 0.6GB
- **网络**: 稳定的互联网连接访问 Google AI Studio

## 安装步骤

### 方式一：Windows 一键启动（推荐）

从 [Releases](https://github.com/Mag1cFall/AIStudio2API/releases) 下载 `windows-amd64.zip` 发布包，解压后双击 `aistudio2api.exe`：

- 管理页面在桌面窗口中打开；首次启动先显示启动画面，Camoufox 下载完成后进入管理页面
- 关闭窗口后程序在系统托盘继续运行，托盘菜单可以显示窗口、在浏览器中打开管理页面或退出
- 再次双击 `aistudio2api.exe` 唤回已运行的窗口

从源码启动时：

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

首次启动会自动下载当前平台的 Camoufox 到 `runtime/camoufox/`：

- 下载经 `PROXY` 的第一个代理发出，未设置时使用系统代理
- 下载中断后，下次启动从已下载的部分继续
- 环境变量 `CAMOUFOX_PATH` 可以指定已有的同版本可执行文件，版本不符时启动报错并给出所需版本

### 方式二：macOS 与 Linux 桌面版

macOS 从 Releases 下载 `darwin-universal.dmg`，打开后把 `AIStudio2API.app` 拖入“应用程序”。应用使用 ad-hoc 签名，首次打开被系统拦截时，在“系统设置 → 隐私与安全性”中点击“仍要打开”，或执行：

```bash
xattr -dr com.apple.quarantine /Applications/AIStudio2API.app
```

Linux 下载 `linux-amd64.AppImage` 赋予执行权限后运行，或安装 `linux-amd64.deb` 后从应用菜单启动 AIStudio2API：

```bash
chmod +x aistudio2api-<版本>-linux-amd64.AppImage
./aistudio2api-<版本>-linux-amd64.AppImage
sudo apt install ./aistudio2api-<版本>-linux-amd64.deb
```

桌面版的 `.env`、`auth/` 与 `runtime/` 位置：

- macOS：`~/Library/Application Support/AIStudio2API/`
- Linux：`~/.config/AIStudio2API/`

`setup` 等命令在终端中运行桌面版的可执行文件，例如 `/Applications/AIStudio2API.app/Contents/MacOS/aistudio2api setup --login`；deb 安装的可执行文件为 `aistudio2api`。

### 方式三：Linux 与 macOS 源码构建

#### 1. 安装依赖

- Go 1.26.0 或更高版本
- Node.js 22.18+、24.11+ 或 26+，以及配套 npm

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

### 方式四：Docker

```bash
mkdir aistudio2api && cd aistudio2api
curl -fsSLO https://raw.githubusercontent.com/Mag1cFall/AIStudio2API/main/docker-compose.yml
mkdir data
cat > data/.env <<'EOF'
PROXY_API_KEY=<API key>
ADMIN_AUTH_ENABLED=true
ADMIN_PASSWORD=<管理密码>
EOF
docker compose up -d
```

镜像支持 linux/amd64 与 linux/arm64，服务在容器内监听 `0.0.0.0:2048`。`data/` 保存 `.env`、`auth/` 与 `runtime/`，Camoufox 后端首次启动时把 Camoufox 下载到 `data/runtime/camoufox/`；迁移已有安装时把这三项复制到 `data/`。容器以 root 运行，服务写入 `data/` 的文件属于 root，Linux 宿主机上用 `sudo` 编辑。

- 端口对外开放时设置 `PROXY_API_KEY`；从容器外打开管理页面需要 `ADMIN_AUTH_ENABLED=true` 与管理密码，用户名默认为 `admin`
- 添加账户：在管理页面“添加账户”中选择“浏览器登录”，在管理页面打开的登录窗口中登录 Google；也可以粘贴 Cookie，或把能登录 Google 的电脑上的 `auth/<Google 邮箱>/` 复制到 `data/auth/`
- `PROXY` 与账户代理从容器内访问，代理运行在宿主机时填写宿主机的局域网地址，并允许代理接受局域网连接
- 修改 `data/.env` 后执行 `docker compose restart`；更新镜像执行 `docker compose pull && docker compose up -d`；在源码目录中执行 `docker compose up -d --build` 从源码构建

## 快速开始

### 首次使用（需要认证）

1. **准备首个账户**:

   Windows 可以导入本机 Chrome 账户：

   ```powershell
   cmd /c start /wait aistudio2api.exe setup
   ```

   Linux 与 macOS 使用隔离 Camoufox 登录：

   ```bash
   ./aistudio2api setup --login
   ```

   登录完成后会从 AI Studio 页面读取 Google 邮箱，并为账户授权 Google Drive。账户保存到 `.env` 中 `AISTUDIO_AUTH_STATES` 指向的目录；语言和时区默认读取当前电脑设置，也可以通过 `--locale`、`--timezone` 指定。

2. **启动图形界面**:
   - Windows 双击 `aistudio2api.exe`
   - macOS 与 Linux 桌面版从应用程序启动，命令行版运行 `./aistudio2api`
   - 桌面版在窗口中打开管理页面，命令行版在浏览器中打开 `http://127.0.0.1:2048`
   - 页面初始状态为 `STOPPED`，默认显示“日志”页面

3. **添加其他账户**:
   - 打开“账户”页面，点击“添加账户”选择方式
   - “从 Chrome 导入”可多选本机 Chrome 账户
   - “粘贴 Cookie”粘贴 Cookie 扩展（如 Cookie-Editor）在 aistudio.google.com 导出的 JSON、请求头中的 Cookie 一行或 Netscape cookies.txt，服务自动识别账户邮箱
   - “从指纹浏览器导入”自动列出本机指纹浏览器与 Chromium 中已登录 AI Studio 的窗口及其语言与时区，勾选后导入，账户沿用该浏览器的语言与时区；浏览器需开启远程调试
   - “浏览器登录”会打开独立 Camoufox 窗口，登录完成后自动识别邮箱、授权 Google Drive 并保存；Google 要求验证身份时，在该窗口或手机上确认
   - 服务器没有图形界面，或从其他电脑、经反向代理打开管理页面时，“浏览器登录”与“重新登录”的 Camoufox 在服务器上无头运行，画面显示在管理页面的全屏登录窗口中，在其中点击、输入与粘贴，点“取消”结束登录
   - 账户代理、语言区域与时区在对话框的“更多设置”中填写

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

1. 桌面版双击 `aistudio2api.exe` 或从应用程序启动；命令行版运行 `./aistudio2api`
2. 点击“启动服务”启用 API；设置 `AUTO_START=true` 后，程序启动时自动启动服务
3. 点击“停止服务”会取消正在进行的启动或活动请求并关闭 WAA Worker，管理页面与日志保持可用
4. 再次点击“启动服务”即可恢复 API

停止后再次启动会读取最新 `.env` 生成服务配置，账户选择策略与 Worker 容量保存后立即生效；管理页面地址和 `PROXY_API_KEY` 在管理进程重启后生效。

桌面版关闭窗口后在系统托盘继续运行，从托盘菜单“退出”结束管理进程；命令行版在启动窗口按 `Ctrl+C` 或关闭窗口会退出整个管理进程。关闭浏览器标签页不会停止管理进程。

### 快速启动

Windows 桌面版在命令行中运行以下命令时写作 `cmd /c start /wait aistudio2api.exe <参数>`，命令行等程序结束后返回。

`aistudio2api`：启动管理进程并打开管理页面，桌面版打开窗口，命令行版打开网页。

`aistudio2api -open-ui=false`：启动管理进程但不打开窗口或网页，桌面版只显示托盘图标。

`aistudio2api setup`：导入账户。

- 不带参数时扫描本机 Chrome 账户，`--email` 或 `--profile` 选择明确的 Chrome 账户
- 隔离登录：`aistudio2api setup --login`，需要图形界面；没有图形界面时启动服务，在管理页面的“浏览器登录”中登录
- 文件导入：`aistudio2api setup --storage-state <file>`
- Cookie 导入：`aistudio2api setup --cookie-file <file>`、`aistudio2api setup --cookie "<Cookie 请求头>"` 或 `aistudio2api setup --cdp <调试地址>`，账户邮箱按 Cookie 识别，`--cdp` 导入的账户沿用该浏览器的语言与时区

`aistudio2api setup --remote https://<远程实例>`：向远程实例添加账户。先在远程实例的账户页点击“远程添加”取得配对令牌，再在能登录 Google 的电脑上运行该命令并粘贴令牌；可与 `--login` 等参数组合。

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

模型名称从 `GET /v1/models` 或 `GET /v1beta/models` 读取。`PROXY_API_KEY` 为空时，浏览器中只有本机页面可以直接调用接口；网页版客户端和部分桌面客户端需要设置 `PROXY_API_KEY`。多个 key 用逗号分隔，任一 key 都可调用。

以 Cherry Studio 为例：

1. 打开 Cherry Studio 设置
2. 新增 OpenAI 兼容提供商
3. API 主机地址填写 `http://127.0.0.1:2048/v1`
4. API 密钥填写 `.env` 中的 `PROXY_API_KEY`
5. 从 `/v1/models` 获取模型，或手动添加 `gemini-3.6-flash`、`gemini-3.7-flash`

[Claude Code](https://github.com/anthropics/claude-code) 使用 Anthropic 接口，子 agent 按 opus、sonnet、haiku 档位选择模型，以下变量把它们映射到 AI Studio 模型；WebSearch 由 Google Search 执行：

```powershell
$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:2048"
$env:ANTHROPIC_API_KEY = "<PROXY_API_KEY>"
$env:ANTHROPIC_MODEL = "gemini-3.8-flash"
$env:ANTHROPIC_DEFAULT_OPUS_MODEL = "gemini-3.1-pro-preview"
$env:ANTHROPIC_DEFAULT_SONNET_MODEL = "gemini-3.8-flash"
$env:ANTHROPIC_DEFAULT_HAIKU_MODEL = "gemini-3.5-flash-lite"
```

[Codex](https://github.com/openai/codex) 使用 Responses 接口，在 `~/.codex/config.toml` 中添加 provider，并把 `PROXY_API_KEY` 写入 `AISTUDIO2API_KEY` 环境变量；Codex 的 `web_search` 工具由 Google Search 执行：

```toml
model = "gemini-3.8-flash"
model_provider = "aistudio"

[model_providers.aistudio]
name = "AIStudio2API"
base_url = "http://127.0.0.1:2048/v1"
env_key = "AISTUDIO2API_KEY"
wire_api = "responses"
```

[omp](https://github.com/can1357/oh-my-pi) 的 `web_search` 工具使用 `web` 角色的模型搜索。在 omp 的 `models.yml` 中添加指向本服务的 Gemini provider（`apiKey` 为保存 `PROXY_API_KEY` 的环境变量名），并在 `config.yml` 中把 `web` 角色设为该模型：

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

主要端点：

| 能力 | 端点 |
| --- | --- |
| 模型 | `GET /v1/models`、`GET /v1/models/{model}`、`GET /v1beta/models`、`GET /v1beta/models/{model}` |
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses`、`POST /v1/responses/input_tokens`、`POST /v1/responses/compact`、`GET`/`DELETE /v1/responses/{id}`、`GET /v1/responses/{id}/input_items`、`POST /v1/responses/{id}/cancel` |
| Files | `POST /v1/files`、`GET /v1/files`、`GET /v1/files/{id}`、`GET /v1/files/{id}/content`、`DELETE /v1/files/{id}`（OpenAI 与 Anthropic 格式）；`POST /upload/v1beta/files`、`GET /v1beta/files`、`GET`/`DELETE /v1beta/files/{id}` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens` |
| Embeddings | `POST /v1/embeddings`、`POST /v1beta/models/{model}:embedContent`、`:batchEmbedContents` |
| 图片 | `POST /v1/images/generations`、`POST /v1/images/edits` |
| 语音 | `POST /v1/audio/speech` |
| 转录与翻译 | `POST /v1/audio/transcriptions`、`POST /v1/audio/translations` |
| 音乐 | Gemini `generateContent` + `responseModalities: ["AUDIO"]` |
| 视频 | `POST /v1/videos`、`GET /v1/videos`、`GET`/`DELETE /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Gemini 视频 | `POST /v1beta/models/{model}:predictLongRunning`、`GET /v1beta/operations/{id}` |
| Live（含实时翻译、实时转录与实时音乐）/ Robotics | `GET /v1/live`、`GET /v1/robotics/stream` |

四套生成接口均可按各自协议字段启用 Search、Image Search、URL Context、Code Execution 和 Maps。Files、Transcribe、Live、Robotics 的请求与事件格式见 [Google AI Studio 协议规范](docs/protocol.md)。

生成请求中的内联附件会优先上传为临时 Drive 文件，随请求结束清理；账户未授予 Drive 权限时保持内联数据发送。图片、音频、视频、PDF 等输入仍需所选模型支持。重复使用的附件可通过 Files 接口上传一次并复用文件 ID。

Gemini 附件与视频图片输入支持 `inlineData` / `inline_data`、`fileData` / `file_data`、`mimeType` / `mime_type` 和 `fileUri` / `file_uri`。媒体 Base64 数据支持标准与 URL-safe 字母表、带填充与无填充形式，以及 `data:<MIME>;base64,` 前缀。OpenAI 助手历史中的 Markdown 图片同样支持 URL-safe Base64 和 CR/LF 换行。内联 GIF 和视频表单上传的 GIF 按首帧静态图片转换为 PNG，保留逻辑画布、帧位置与透明背景。

### TTS 语音生成

TalkifyTTS 与新版 Google Gen AI SDK 可连接 `http://127.0.0.1:2048/v1beta/interactions`，稳定版入口为 `/v1/interactions`。请求使用 `x-goog-api-key`，支持 `gemini-3.8-flash-tts` 与 `gemini-3.8-flash-lite-tts`：

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

流式音频默认为 Base64 编码的 24 kHz、16-bit 小端、单声道 PCM；非流式默认为完整 WAV，通过 `interaction.output_audio.data` 读取。`response_format.mime_type` 可显式选择 `audio/l16` 或 `audio/wav`。风格与多说话人输入见 [Interactions 协议](docs/protocol.md#gemini-interactions)。

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

可用语音由实时模型目录中的 `capability_options.voices` 返回。`gemini-3.8-flash-tts` 等带 `speech_metadata` 能力的模型同样接受上面的 `说话人: 台词` 写法，也可以为每个文本 part 设置 `speechMetadata.speaker` 与 `speechMetadata.style`，并用 `multiSpeakerVoiceConfig.mode` 选择 `VERBATIM` 或 `CONVERSATIONAL`；OpenAI `instructions` 在这些模型上作为语音风格。

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

公开端点实现标准 `generateContent`、`countTokens`、`embedContent`、`batchEmbedContents` 和 `predictLongRunning`；`bidiGenerateContent` 与 `bidiGenerateMusic` 模型经 `/v1/live` 调用。`/v1/models` 与 `/v1beta/models` 原样汇总各账户实时上游目录；调度按目录明确提供的模型 ID、方法、能力字段和账户当前运行状态选择账户。

启用账户的订阅都不满足的模型（如只对 Google AI Ultra 开放的模型）同样列在目录中、不含 `channels`，调用时返回 403 并写明所需订阅。能力含 `output_continuation` 的模型（如 Gemini 4 Argon）与官网一样接受到 `input_token_limit` 的输出上限，超过 32768 时分轮自动续写，各轮输出合并为一个响应。模型名可以带思考后缀，例如 `gemini-4-argon(low)`：括号内为 `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`auto` 或思考预算 token 数，后缀优先于请求体中的思考参数。客户端写死的模型名可用 `MODEL_MAPPING` 映射到目录中的模型，例如 `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash`，别名以目标模型的字段列在模型目录中。

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
├── internal/desktop/        # 桌面窗口与系统托盘
├── web/                     # Vue 3 + TypeScript 管理页面
├── docs/                    # 开发文档与协议规范
└── build/                   # 应用图标与各平台打包配置
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
| `AUTO_START` | `false` | 管理程序启动后自动启动生成服务 |
| `REQUEST_BODY_LOG` | `false` | 保存公开 API 请求与响应正文，各截断到 64 KiB，保留最近 1000 条 |
| `LOG_FILE` | `true` | 同时把服务日志写入 `runtime/logs/`，单文件 10 MB 轮转 |
| `LOG_MAX_TOTAL_MB` | `500` | 日志目录总量上限，超出时删除最旧的文件，不小于 20 |
| `ADMIN_AUTH_ENABLED` | `false` | 管理控制台账号密码登录开关 |
| `ADMIN_USERNAME` | `admin` | 管理员账号 |
| `ADMIN_PASSWORD` | 空 | 管理员密码，开启登录时必填 |
| `PROXY` | 空 | Chrome 导入、登录和账户默认使用的 HTTP、HTTPS 或 SOCKS5 代理，多个代理用逗号分隔组成代理池；留空时使用系统代理 |
| `INIT_TIMEOUT` | `2m` | 单账户 WAA 初始化超时 |
| `REQUEST_TIMEOUT` | `30m` | 单次请求最大执行时间，自动续写请求按续写轮数倍增 |
| `WARM_WORKER_LIMIT` | `5` | 常驻预热账户数 |
| `MAX_ACTIVE_WORKERS` | `10` | 高峰期最多同时运行的 Worker 数 |
| `WARM_STARTUP_CONCURRENCY` | `5` | 同时冷启动的 Camoufox Worker 数，预热与按需扩容共用；`WAA_BACKEND=go` 时不限制 |
| `PER_ACCOUNT_CONCURRENCY` | `2` | 单账号同时执行的请求数 |
| `ROUTING_STRATEGY` | `round-robin` | `round-robin` 轮询；`fill-first` 账号粘性优先 |
| `UPSTREAM_CHANNELS` | `playground,build` | 生成请求使用的上游通道，可只保留其一 |
| `MODEL_MAPPING` | 空 | 客户端模型名到目录模型的映射，每项写成 `别名:模型 ID`，逗号分隔多项 |
| `MODEL_CHANNELS` | 空 | 限定模型只走一个通道，每项写成 `模型 ID:playground` 或 `模型 ID:build`；也可在“模型目录”的模型详情中选择，保存后立即生效 |
| `BUILD_NATIVE_NONSTREAM` | `true` | 非流式请求优先使用 Build 原生单次调用；不可用时回退流式并记录原因 |
| `WAA_BACKEND` | `camoufox` | `camoufox` 在 Camoufox 页面运行 WAA；`go` 在服务进程内运行 WAA，不下载也不启动 Camoufox |
| `TEMPORARY_CHAT` | `false` | WAA 预热页是否使用临时对话 |
| `MEDIA_OUTPUT` | `inline` | Chat Completions 与 Messages 正文中生成图片与音频的引用方式：`inline` 以 data URI 内联；`url` 按内容命名保存到 `runtime/media/`，以本服务的 `/media/<文件名>` URL 引用，该路径不校验 API key |
| `MEDIA_BASE_URL` | 空 | 媒体 URL 使用的服务地址，例如 `https://ai.example.com`；留空时使用每个请求访问的地址，经反向代理时跟随 `X-Forwarded-Proto` |

服务启动时会载入 `AISTUDIO_AUTH_STATES` 中的全部账户；`WARM_WORKER_LIMIT` 控制常驻预热规模，`MAX_ACTIVE_WORKERS` 控制峰值 Worker 上限，`WARM_STARTUP_CONCURRENCY` 控制同时冷启动的 Camoufox Worker 数，`PER_ACCOUNT_CONCURRENCY` 控制单账户请求槽位；这四项在管理页面保存后立即作用于后续调度，峰值上限调低时关闭多出的空闲 Worker，常驻数调高时补足预热。

### 端口配置

管理控制台默认通过本机回环地址免密访问。远程管理时：

- 设置 `ADMIN_AUTH_ENABLED=true`、管理员账号和密码，重启程序后通过独立 `/login` 页面登录
- 登录会话有效期为 12 小时；勾选“30 天内保持登录”时为 30 天，程序重启后仍有效
- 支持退出登录，管理员共享同一个账户池、配置与日志

公开 API 使用独立的 `PROXY_API_KEY`。

反向代理使用 HTTPS，并保留原始 `Host`、设置 `X-Forwarded-Proto: https`。管理页“服务配置”可调整登录开关及凭据，保存后重启程序生效。

- **管理页面与 API**: 默认端口 `2048`
- **Camoufox**: 由程序动态分配本机端口

## 高级功能

### 代理配置

支持通过 HTTP、HTTPS 或 SOCKS5 代理访问 AI Studio，代理需要认证时在 URL 中填写账号密码，例如 `socks5://user:password@host:1080`：

1. 在“服务配置”中设置全局代理；留空时使用系统代理，先读 `HTTPS_PROXY` 环境变量，未设置时读 Windows 系统代理设置，代理输入框下方显示当前生成服务实际使用的出口
2. 全局代理可以填写多个，用逗号分隔组成代理池：没有专用代理的账户固定绑定到绑定账户最少的可用代理，绑定写入账户状态，重启后不变；代理连续 3 次网络错误或定时检测失败时，其账户改绑到其他可用代理，检测恢复后重新参与分配；“服务配置”列出各代理的状态与绑定账户数，账户卡片显示每个账户的出口
3. 在“账户”页面编辑单个账户时可以设置账户专用代理
4. 账户代理同时用于登录、WAA 与业务请求
5. 带账号密码的代理由程序在本机回环地址提供无认证 SOCKS5 中转供 Camoufox 使用，账号密码不写入浏览器 profile；中转运行期间，本机其他进程也能经它使用该代理

### 认证文件管理

认证文件默认存储在 `auth/` 目录：

| 路径 | 内容 |
| --- | --- |
| `auth/<Google 邮箱>/account.json` | 账户邮箱、代理、语言、时区和启用状态 |
| `auth/<Google 邮箱>/storage-state.json` | Google Cookie 与认证续签材料 |
| `auth/<Google 邮箱>/runtime-state.json` | 权益等级、模型资格、冷却状态与资源所属账户 |
| `auth/<Google 邮箱>/camoufox-cache/` | 该账户浏览器的网页缓存，服务停止时可删除 |
| `auth/.leases/<Google 邮箱>.lock` | 同一账户目录的跨进程占用锁 |
| `[用户缓存]/AIStudio2API/runtime-leases/<Google 邮箱>.lock` | 当前电脑上该邮箱的 WAA Worker 占用锁 |

账户邮箱同时作为目录名、管理页面标识和日志来源，统一使用小写形式。`.leases` 协调账户目录读写，用户缓存中的 runtime lease 保证同一邮箱在当前电脑上只有一个 WAA Worker。

账户页的“添加账户”支持从 Chrome 导入、粘贴 Cookie、从指纹浏览器导入、隔离 Camoufox 登录和远程添加：

- 隔离 Camoufox 登录在本机打开可见窗口；服务器没有图形界面或从其他电脑访问时，登录画面显示在管理页面的登录窗口中，经反向代理访问时需要转发 `/api/login-view/` 的 WebSocket
- `ready` 账户可以编辑、停用、立即刷新和删除，`auth_required` 账户可以重新登录
- “刷新全部”立即读取全部启用账户的权益与模型目录，后台每 10 分钟也会读取一次
- 从未读取成功的账户权益显示为未知，并显示最近一次失败原因
- 读取报告 AI Studio 不支持当前出口地区或账户时，已有目录的账户同样显示该原因

## 详细文档

- [开发与贡献](docs/development.md)
- [Google AI Studio 协议规范](docs/protocol.md)
- [WAA 实现](docs/waa.md)
- [Build 通道](docs/build.md)
- [运行日志说明](docs/logging.md)
- [可复用逆向开发指南](docs/reverse-engineering.md)

## 重要提示

### 关于 Camoufox

本项目使用 [Camoufox](https://camoufox.com/) 浏览器来降低被检测为自动化脚本的风险。Camoufox 基于 Firefox，通过修改底层实现来保持真实的设备指纹。

Go 负责编码、调度、流式解码与公开协议；受 WAA 保护的 `GenerateContent` 通过账户固定指纹 Camoufox 页面发送，保留原生 Firefox TLS/HTTP2、请求头、Cookie 与页面指纹。

`WAA_BACKEND=go` 时，WAA 在服务进程内运行，按账户指纹模拟 Firefox 页面环境并以 Firefox 请求头直接发送，运行时不下载也不启动 Camoufox。账户页的浏览器登录仍使用 Camoufox，首次登录时按需准备。

### 使用限制

- **客户端管理历史**: Chat、Anthropic 和 Gemini 请求由客户端提交完整对话上下文
- **AI Studio 历史**: API 请求不保存到官网历史；`TEMPORARY_CHAT=true` 还会关闭 WAA 预热页的自动保存
- **Responses 会话**: `previous_response_id` 仅在当前进程内保存，重启后不会保留
- **认证有效期**: Chrome 导入账户保留 OAuth 续签材料，续签在 Windows 上执行，Linux、macOS 与 Docker 中的 Chrome 导入账户失效后重新从 Windows 复制；其他账户失效时，若本机 Chrome 登录着同一邮箱，服务从 Chrome 导入该邮箱的材料续签，此后按 Chrome 导入账户续签，否则在账户页重新登录

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
| `service_starting` | 生成服务正在启动，稍后重试 |
| 没有可用账户 | 在账户页新增、启用或重新登录账户 |
| Camoufox 准备失败 | 检查 GitHub Release 访问，或设置 `CAMOUFOX_PATH` |
| Linux 预热账户 `exit status 255` | 安装 Camoufox 运行库，见“系统要求”中的 apt 命令 |
| 账户显示“AI Studio 不支持当前出口地区或账户” | 为该账户或全局代理换用 [AI Studio 支持地区](https://aistudio.google.com/docs/available-regions)的出口 |

## 贡献

欢迎提交 Issue 和 Pull Request！

## 开发计划

- ✅ **TTS 支持**: 已适配 `gemini-2.5-flash/pro-preview-tts` 语音生成模型
- ✅ **媒体生成**: 已支持 Imagen 3、Veo 2、Nano Banana 图片/视频生成
- ✅ **文档完善**: 更新并优化 `docs/` 目录下的详细使用文档与 API 规范
- ✅ **Go 语言重构**: 将核心代理服务迁移至 Go 以提升并发性能与降低资源占用
- ✅ **多Worker负载均衡**: 支持多 Google 账号轮询池，提高并发限额与稳定性
- ✅ **桌面版**: Windows、macOS 与 Linux 桌面应用，提供窗口、系统托盘与在线更新
- ✅ **API 试用页**: 按 AI Studio 官网的入口与路由提供对话、图片、视频、语音、音乐与实时对话页

### 纯 Go WAA 运行时

- ✅ **纯 Go 后端**: `WAA_BACKEND=go` 在服务进程内执行官方 interpreter 与 program，按账户指纹模拟 Firefox 页面环境，运行时不下载、不启动 Camoufox；账户登录仍使用 Camoufox
- ✅ **Firefox 引擎细节**: 补齐 `Intl` 格式化、正则字面量的全局解析时机与 `RegExp.prototype` 的 Symbol 键顺序
