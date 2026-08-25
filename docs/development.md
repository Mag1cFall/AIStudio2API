# 开发与贡献

AIStudio2API 使用 Go 直接调用 Google AI Studio 的 MakerSuite 私有协议，由 Vue 3 管理端展示账户、模型、冷却和请求状态。业务请求、流式解码、账户调度和公开协议适配都在 Go 进程内完成；Camoufox 只保留官方 WAA 初始化与 fresh proof 生成边界。

## 1. 环境、首次配置与启动

| 场景 | 必需组件 | 说明 |
| --- | --- | --- |
| Release 运行 | `aistudio2api` 与 Camoufox | 不需要 Python、Node.js 或 Playwright |
| 源码运行 | Go 1.26、Node.js 24、npm 与 Camoufox | Node.js 只用于构建 Vue 管理端 |
| Windows Chrome 导入 | Windows amd64、稳定版 Chrome | Go 程序直接读取本机 Profile 的 OAuth/DBSC 材料 |

Windows 用户可以直接运行根目录的 `start.bat`。脚本优先启动已有的 `aistudio2api.exe`；源码目录缺少可执行文件时才执行 `npm ci`、前端构建与 Go 构建。程序启动后自动打开管理页面，生成服务初始保持停止；账户登录、日志查看和生成服务启停均在该页面完成。

源码首次启动：

```powershell
cd web
npm ci
npm run build
cd ..
go run ./cmd/aistudio2api
```

管理页面的“账户”页是默认认证入口，可以新增账户、重新登录、验证、编辑、启停和删除账户。新增账户时会直接启动隔离 Camoufox 登录。`setup` 保留以下四种命令行导入入口：

| 入口 | 命令 | 适用场景 |
| --- | --- | --- |
| 扫描本机 Chrome | `aistudio2api setup` | 交互选择可导入 Profile |
| 指定 Chrome 账户 | `aistudio2api setup --profile <PROFILE>` 或重复使用 `--email` | 批量、确定性导入 |
| 隔离登录 | `aistudio2api setup --login` | 可见 Camoufox 中手动完成 Google 登录 |
| 文件导入 | `aistudio2api setup --storage-state <file>` | 导入 Playwright storage state 结构 |

`--proxy` 会同时固定到新账户的初始化、WAA 与业务请求，接受无认证信息的 HTTP、HTTPS 或 SOCKS5 URL。`--locale` 和 `--timezone` 设置账户环境；Chrome 导入未显式指定语言时读取 Profile 的首选语言。Camoufox 按以下顺序定位：进程环境变量 `CAMOUFOX_PATH`、`runtime/camoufox/camoufox[.exe]`、可执行文件旁的同名目录、Windows 本机 Camoufox 缓存。

日常启动只需运行二进制或 Go 入口，再从管理页面启动生成服务：

```powershell
./aistudio2api.exe
go run ./cmd/aistudio2api --listen 127.0.0.1:2048 --open-ui
```

管理页面与 `/api` 控制面在生成服务停止时继续运行。停止生成服务会取消活动生成请求并关闭 WAA worker；再次启动会重新读取账户模型目录。关闭启动窗口或按 `Ctrl+C` 才会退出整个管理进程。

## 2. 目录、组件和运行依赖

```text
cmd/aistudio2api/        配置、账户装配、认证续签、信号和服务生命周期
internal/aistudio/       账户、MakerSuite、WAA、模型、工具、上传、媒体和规范事件
internal/api/            OpenAI、Responses、Anthropic、Gemini 与管理端 HTTP 路由
internal/camoufoxnative/ 原生 WebDriver BiDi、WAA bootstrap 与隔离登录
internal/chromeauth/     Windows Chrome OAuth/DBSC 发现、导入和续签
internal/config/         六项全局配置的读取、校验和原子写回
internal/webui/          嵌入并提供 Vue 构建产物
web/                     Vue 3、TypeScript、Vite 和 Tailwind CSS 源码
docs/                    开发流程与私有协议说明
auth/                    每账户配置、认证状态和可恢复运行状态
runtime/camoufox/        Release 使用的 Camoufox 运行时
```

请求链保持单向：

```text
HTTP route
  -> client protocol decoder
  -> canonical request
  -> capability-aware account lease
  -> AI Studio array encoder
  -> per-account WAA proof
  -> authenticated MakerSuite HTTP transport
  -> incremental response decoder
  -> canonical events
  -> client protocol response
```

Camoufox 由 Go 通过 WebDriver BiDi 直接管理。每个被首次使用的账户启动一个隔离、无头、长驻 runtime，完成一次官网生成以取得官方 WAA service 与动态请求头；后续业务正文由 Go 编码并通过同账户固定出口发送。HTTP transport 使用与当前 Camoufox 对齐的 Firefox 152 TLS、HTTP/2 和请求头顺序。源码和 Release 均不包含 Python 数据面、Node.js 浏览器 worker 或 Playwright runtime。

## 3. 配置、账户和持久状态

程序从当前目录读取可选的 `.env`，进程环境变量覆盖同名配置：

| 变量 | 作用 | 默认值 |
| --- | --- | --- |
| `AISTUDIO_AUTH_STATES` | 账户文件、目录或逗号分隔的多个路径 | `auth` |
| `LISTEN_ADDR` | HTTP 服务监听地址 | `127.0.0.1:2048` |
| `PROXY_API_KEY` | 公开 API 访问密钥 | 空 |
| `PROXY` | setup 与未设置账户代理时使用的固定出口 | 空 |
| `INIT_TIMEOUT` | 单账户初始化超时 | `2m` |
| `REQUEST_TIMEOUT` | 单次请求最大执行时间 | `5m` |

每个账户目录包含：

| 文件 | 内容 | 生命周期 |
| --- | --- | --- |
| `account.json` | label、enabled、proxy、locale、timezone | 创建或编辑账户时写入 |
| `storage-state.json` | Cookie、localStorage 与可选 Chrome OAuth/DBSC 续签材料 | 合并 `Set-Cookie` 或认证续签后原子写回 |
| `camoufox-fingerprint.json` | 账户固定的浏览器指纹、语言与时区 | 首次运行生成；重新登录和 WAA runtime 继续复用 |
| `runtime-state.json` | 模型冷却与 Drive/Veo 资源到账户的绑定 | 请求失败、资源创建和服务重启期间保留 |

账户调度先按实时 `ListModels` 的模型和方法筛选，再执行轮询。租约同时覆盖进程内独占、跨进程文件锁、请求读取和 Cookie 写回。未固定账户和资源的请求遇到可重试的 401、403、404、429、5xx 或单账户初始化超时时，可以在首个客户端可见事件前切换到另一个同能力账户一次；显式账户、Drive 文件和 Veo operation 始终保持创建账户粘性。Chrome 导入状态保留续签材料，401 或 403 时在同一固定出口续签一次、重建该账户 WAA runtime 并重放请求。

认证状态包含长期凭证和设备绑定材料，只能保存在本机受控目录。提交、Issue、CI 和普通日志中不得出现 Cookie、token、proof、邮箱、账户 ID、提示词、响应正文或完整原始帧。

## 4. Go 协议层、公开端点与 Vue 管理端

公开端点统一读取同一份实时模型目录和规范事件：

| 协议 | 端点 |
| --- | --- |
| OpenAI Chat | `GET /v1/models`、`POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| OpenAI 媒体 | `POST /v1/images/generations`、`POST /v1/audio/speech`、`POST /v1/videos`、`GET /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、`:predictLongRunning`、`GET /v1beta/operations/{id}` |

OpenAI Responses 的 `previous_response_id` 在当前进程内保存最多 256 个响应节点，用于重建下一轮完整 contents；进程重启后客户端应重新提交完整上下文。Drive 文件、Veo operation 和产物文件的账户绑定写入 `runtime-state.json`，重启后仍可轮询和下载。

新增上游能力从 `internal/aistudio` 开始：编码真实数组槽位、解码服务器事件，再由 `internal/api` 投影到公开协议。公开适配器不得直接解析上游数组，也不得从模型名称猜测能力。模型方法、上下文、输出上限、工具、声音、图片规格和视频规格均来自实时 `ListModels`。

前端开发命令：

```powershell
cd web
npm run dev
npm run typecheck
npm run lint
npm run format:check
npm run build
```

Vite 将生产产物写入 `internal/webui/dist`。管理端通过本机 `/api` 路由管理生成服务、日志、账户、配置、模型冷却、活动请求和 SSE 状态事件；认证状态与 WAA 对象不进入浏览器存储。视觉、布局、图标和多语言以旧版 `dashboard.html`、`i18n.js` 与 `icons.js` 为基线，新增界面只绑定现有结构化 API。

## 5. 协议实现约束

新增上游能力按以下顺序实现：

1. 在 `internal/aistudio` 定义规范请求、响应类型与模型能力
2. 编码 [protocol.md](protocol.md) 中对应的 JSON+protobuf 数组
3. 将网络增量解码为规范事件
4. 由 `internal/api` 分别投影 OpenAI、Responses、Anthropic 和 Gemini 协议
5. 将结构化状态与操作入口绑定到管理端

公开适配器不得访问账户文件、WAA runtime 或原始上游数组。协议核心不得从模型 ID 猜测方法、token 上限、工具或媒体参数。资源型操作在创建时记录账户 ID，后续轮询、下载与提示引用继续使用该账户。

已识别字段必须校验类型和 oneof 约束。新增的未知非空槽保留为脱敏 DEBUG 信息，不中断已识别文本和媒体事件；出现无法解释的已消费字段、缺失完成帧或无效媒体内容时返回结构化协议错误。

## 6. 自动检查与集成测试

提交前执行：

```powershell
cd web
npm ci
npm run typecheck
npm run lint
npm run format:check
npm run build
cd ..
gofmt -w <修改的 Go 文件>
go test ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
go build ./cmd/aistudio2api
```

集成测试使用临时认证目录、临时监听端口和一次性本地 API key。测试矩阵按改动范围选择：

| 能力组 | 必须检查的结果 |
| --- | --- |
| 认证 | Chrome 导入或隔离登录、模型目录、Cookie 轮换、401/403 续签、停止后重启 |
| 文本 | 四个入口的流式与非流式正文、权威 usage、finish reason、客户端取消 |
| 多轮与工具 | 完整消息历史、`previous_response_id`、函数调用、tool result、thought signature |
| Google 工具 | Search、URL Context、Code Execution、Maps、Image Search 的事件与来源结构 |
| 媒体 | 上传文件可被模型读取、图片可解码、音频可播放、Veo 可轮询并下载 MP4 |
| 多账户 | 能力筛选、轮询、冷却、首个可见事件前故障转移、资源粘性和重启恢复 |

协议改动应使用脱敏 fixture 检查稀疏数组、UTF-8 跨块、思考、工具、媒体、usage、finish 和错误。账户改动应检查租约、轮询、冷却、Cookie 合并、资源绑定与原子持久化。集成改动应检查多次 WAA proof、认证轮换、请求取消、进程 `Ctrl+C`、再次启动和继续调用。

## 7. Release 与贡献验收

Release 构建顺序固定为前端安装、生产构建和 Go 二进制构建：

```powershell
cd web
npm ci
npm run build
cd ..
go build -trimpath -o aistudio2api.exe ./cmd/aistudio2api
```

Windows 发布包包含 `aistudio2api.exe`、`start.bat` 和 `runtime/camoufox/`。其他平台提供同一 Go 程序与对应 Camoufox 运行时。发布验收在全新解压目录完成管理页新增账户和登录、生成服务启停、实时模型发现、一次流式文本、一个媒体能力、退出管理进程和重启后再次调用。

协议相关 Issue 或 PR 应提供入口协议、模型 ID、HTTP 状态、最短脱敏请求形状、响应事件顺序、静态检查结果和集成测试结果。原始认证状态、完整上游请求与响应、本机绝对路径留在本机私有目录。
