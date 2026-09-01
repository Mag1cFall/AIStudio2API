# 开发与贡献

AIStudio2API 使用 Go 直接调用 Google AI Studio 的 MakerSuite 私有协议，由 Vue 3 管理端展示账户、模型、冷却和请求状态。业务请求、流式解码、账户调度和公开协议适配都在 Go 进程内完成；Camoufox 只保留官方 WAA 初始化与 fresh proof 生成流程。

管理监听器、公开 API、账户池、协议运行时和内嵌 Vue 管理端位于同一进程。生成服务是可停止、可重新创建的公开 API 服务实例；原始 JSON+protobuf、WebChannel 与 WAA 格式见 [protocol.md](protocol.md)。

## 1. 环境、首次配置与启动

| 场景 | 必需组件 | 说明 |
| --- | --- | --- |
| Release 运行 | `aistudio2api` | 首次启动自动准备 Camoufox，不需要 Python、Node.js 或 Playwright |
| 源码运行 | Go 1.26、Node.js 24 与 npm | Node.js 只用于构建 Vue 管理端 |
| Windows Chrome 导入 | Windows amd64、稳定版 Chrome | Go 程序直接读取本机 Profile 的 OAuth/DBSC 材料 |

Windows 用户可以直接运行根目录的 `start.bat`。脚本优先启动已有的 `aistudio2api.exe`；源码目录缺少可执行文件时才执行 `npm ci`、前端构建与 Go 构建。程序启动后自动打开管理页面，生成服务初始保持停止；账户登录、日志查看和生成服务启停均在该页面完成。

`start.bat` 的执行顺序如下：

```text
aistudio2api.exe 已存在
  -> 直接运行现有二进制

aistudio2api.exe 不存在
  -> 检查 node、npm、go 与 web/package-lock.json
  -> web/npm ci
  -> web/npm run build
  -> go build -o aistudio2api.exe ./cmd/aistudio2api
  -> 运行新二进制
```

前端或 Go 源码变化需要重建时，先结束当前管理进程并移除旧二进制，再运行 `start.bat`。Windows 在替换运行中的输出文件时可能留下 `aistudio2api.exe~`，它是旧进程占用目标文件期间产生的构建副本。

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

`setup` 还接受以下参数：

| 参数 | 作用 |
| --- | --- |
| `--chrome-root <DIR>` | 指定 Chrome User Data 根目录 |
| `--label <EMAIL>` | 设置单个导入账户的标签；隔离登录时必须提供 Google 邮箱 |
| `--proxy <URL>` | 固定到账户初始化、WAA 与业务请求 |
| `--locale <LOCALE>` | 设置账户语言 |
| `--timezone <IANA_ZONE>` | 设置账户时区 |

`--storage-state`、`--login` 与 Chrome 导入参数分别构成文件导入、隔离登录和浏览器导入模式。`--label` 适用于单个 Chrome 导入结果；隔离登录使用 `--login --label <Google 邮箱>`。`setup` 要求 `AISTUDIO_AUTH_STATES` 指向一个账户目录。

`--proxy` 会同时固定到新账户的初始化、WAA 与业务请求，接受无认证信息的 HTTP、HTTPS 或 SOCKS5 URL。`--locale` 和 `--timezone` 设置账户环境；Chrome 导入未显式指定语言时读取 Profile 的首选语言。Camoufox 按以下顺序定位：进程环境变量 `CAMOUFOX_PATH`、`runtime/camoufox/`、可执行文件旁的同名目录、Windows 本机 Camoufox 缓存。全部不存在时自动下载当前平台的固定版本。

日常启动只需运行二进制或 Go 入口，再从管理页面启动生成服务：

```powershell
./aistudio2api.exe
go run ./cmd/aistudio2api --listen 127.0.0.1:2048 --open-ui
```

日常服务入口接受以下参数：

| 参数 | 作用 | 默认来源 |
| --- | --- | --- |
| `--auth <PATHS>` | 覆盖本次进程使用的账户文件、目录或逗号分隔路径 | `AISTUDIO_AUTH_STATES` |
| `--listen <HOST:PORT>` | 覆盖管理页面与 API 的监听地址 | `LISTEN_ADDR` |
| `--proxy <URL>` | 覆盖每次新建生成服务实例使用的全局代理 | `PROXY` |
| `--open-ui` | 启动后打开管理页面 | 无参数启动时为 `true` |

管理页面与 `/api` 控制面在生成服务停止时继续运行。停止生成服务会取消活动生成请求并关闭 WAA worker；再次启动会重新读取账户模型目录。关闭启动窗口或按 `Ctrl+C` 才会退出整个管理进程。

## 2. 目录、组件和运行依赖

```text
cmd/aistudio2api/        薄入口，只调用 internal/app 的 app.Run
internal/app/            配置、账户装配、认证续签、信号、生成服务实例、调度和服务生命周期
internal/setup/          Chrome 导入、storage-state 导入与隔离登录命令
internal/aistudio/       账户、MakerSuite、WAA、模型、工具、上传、媒体和规范事件
internal/api/            OpenAI、Responses、Anthropic、Gemini 与管理端 HTTP 路由
internal/camoufoxnative/ 原生 WebDriver BiDi、WAA bootstrap 与隔离登录
internal/chromeauth/     Windows Chrome OAuth/DBSC 发现、导入和续签
internal/config/         全局配置的读取、校验和原子写回
internal/webui/          嵌入并提供 Vue 构建产物
web/                     Vue 3、TypeScript、Vite 和 Tailwind CSS 源码
docs/                    开发流程与私有协议说明
auth/                    每账户配置、认证状态和可恢复运行状态
runtime/camoufox/        Release 使用的 Camoufox 运行时
```

主依赖方向：

```text
cmd/aistudio2api
  -> internal/app
       -> internal/api
       -> internal/aistudio
       -> internal/setup
       -> internal/camoufoxnative
       -> internal/config
       -> internal/webui

internal/setup
  -> internal/aistudio
  -> internal/camoufoxnative
  -> internal/chromeauth
  -> internal/config
```

请求链保持单向：

```text
HTTP route
  -> client protocol decoder
  -> canonical request
  -> capability-aware account lease
  -> AI Studio array encoder
  -> per-account WAA proof
  -> fingerprinted Camoufox GenerateContent transport
  -> incremental response decoder
  -> canonical events
  -> client protocol response
```

WebSocket 入口沿用相同分层：`internal/api` 解码公开协议，`internal/app` 绑定账户和运行状态，`internal/aistudio` 执行 WebChannel 与规范事件转换。公开适配器只消费规范请求与事件；账户文件、WAA 对象、原始数组和资源粘性由 `internal/aistudio` 与 `internal/app` 管理。

Camoufox 由 Go 通过 WebDriver BiDi 直接管理。启动数据面时，服务按 `WARM_WORKER_LIMIT` 与 `WARM_STARTUP_CONCURRENCY` 准备隔离、无头、长驻的账户 runtime，并在需要其他账户能力时替换最久未用的空闲 runtime。每个 runtime 在官网触发 GenerateContent 并于网络发送前拦截请求，以取得官方 WAA service 与动态请求头；后续业务正文由 Go 编码，在同步官网 prompt 状态并生成 fresh proof 后，通过同一固定指纹页面的原生 `fetch` 发送，响应流由 WebDriver BiDi 分块交回 Go。其他 MakerSuite、Drive 与媒体控制面请求继续使用账户固定出口的 Go HTTP transport。源码和 Release 均不包含 Python 数据面、Node.js 浏览器 worker 或 Playwright runtime。

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
| `WARM_WORKER_LIMIT` | 常驻预热账户数 | `5` |
| `MAX_ACTIVE_WORKERS` | 活动 Worker 容量上限，必须不小于热池目标 | `10` |
| `WARM_STARTUP_CONCURRENCY` | 同时初始化的预热账户数 | `2` |
| `PER_ACCOUNT_CONCURRENCY` | 单账号同时执行的请求数 | `2` |
| `TEMPORARY_CHAT` | WAA 预热页是否使用临时对话 | `false` |

`LISTEN_ADDR` 使用 `host:port`，端口范围为 `1..65535`。时长和容量字段必须为正值，`WARM_STARTUP_CONCURRENCY` 的有效范围为 `1..WARM_WORKER_LIMIT`。全局代理 URL 使用 `http`、`https` 或 `socks5` 纯 origin 形状。命令行 `--auth` 与 `--proxy` 会覆盖每次启动生成服务时读取的保存值。

`GET /api/config` 与 `PUT /api/config` 同时暴露保存值和当前生效值：

| 字段 | 语义 |
| --- | --- |
| `auth_states`、`proxy`、`init_timeout`、`request_timeout` | 下一次启动生成服务时使用的保存值 |
| `warm_worker_limit`、`max_active_workers`、`warm_startup_concurrency`、`per_account_concurrency` | 下一次启动生成服务时使用的容量参数 |
| `temporary_chat` | 下一次启动生成服务时使用的 WAA 配置 |
| `listen_addr`、`proxy_api_key` | 保存的管理监听配置 |
| `active_listen_addr`、`active_proxy_api_key` | 当前管理进程固定使用的值 |
| `management_restart_required` | 保存的监听地址或 API key 与当前管理进程不同 |
| `service_restart_required` | 保存的生成服务配置与当前生成服务实例不同 |

配置保存使用临时文件、`Sync` 和原子替换。监听地址与本地 API key 由管理进程持有，进程重启后应用；其余配置在停止并再次启动生成服务后应用。

生成服务启动顺序如下。源码中的 `generation` 表示一次 Stop/Start 创建的生成服务实例：

```text
PUT /api/config
  -> 校验并原子写入 .env
  -> 返回 saved/active 差异

POST /api/control/stop
  -> 取消 LAUNCHING 或活动请求
  -> 等待模型目录刷新退出并关闭当前 Worker
  -> 管理监听器继续提供 /api 与页面

POST /api/control/start
  -> 完成当前已停止实例的清理
  -> 重新读取 .env 并应用命令行 --auth/--proxy 覆盖
  -> 创建并启用新的生成服务实例
  -> 从当前 generation 的 CachedModels 建立内存目录
  -> 并发刷新全部 enabled ready/busy 账户
  -> 冷 generation 等待首个非空真实目录
  -> 启动首个 WAA Worker并进入 RUNNING
  -> 剩余目录同步与热池预热继续在后台运行
```

配置读取、校验、生成服务实例创建失败或启用前取消时，当前已停止实例保持不变。切换到新实例后启动失败时，该实例进入 `STOPPED`，管理端返回结构化错误。模型目录保存在当前生成服务实例的内存中；正常 Stop/Start 创建的新实例从空 `CachedModels` 冷启动。当前实例已有真实缓存时，`trackedService.Start` 可直接使用该缓存预热 Worker，同时继续刷新全部账户。

模型目录刷新与当前生成服务共用 context（Go 取消信号）。启动失败、取消或 Stop 后等待刷新退出的上界为 2 秒；未完成的 lifecycle transition（启动或停止操作）最多等待 12 秒。超时与 Worker 清理错误通过 `errors.Join` 保留在同一错误链中。

每个账户目录包含：

| 文件 | 内容 | 生命周期 |
| --- | --- | --- |
| `account.json` | label、enabled、proxy、locale、timezone | 创建或编辑账户时写入 |
| `storage-state.json` | Cookie、localStorage 与可选 Chrome OAuth/DBSC 续签材料 | 合并 `Set-Cookie` 或认证续签后原子写回 |
| `camoufox-fingerprint.json` | 账户固定的浏览器指纹、语言与时区 | 首次运行生成；重新登录和 WAA runtime 继续复用 |
| `runtime-state.json` | 权益等级、模型资格、冷却与资源账户绑定 | 权益同步、首次模型结果或资源变化后原子写回 |

`runtime-state.json` 的 `model_access` value 为 `{state,checked_at,reason?}`，成功状态为 `verified`；`cooldowns` value 为 `{until,reason?}`；`resources` value 保存 kind、name、mime、size、purpose、created_at 与可选 video 元数据。Drive file、Veo operation、视频产物和 Bidi 恢复 token 均保持创建账户粘性。Veo operation 额外保存公开 video object 的 model、seconds、size 与 UTC 创建时间，生成服务或进程重启后的轮询继续投影相同字段。

活动请求锁保护跨进程账户租约，短事务锁保护 `runtime-state.json` 合并写回。短事务锁位于 `auth/.leases/<账户>.runtime.lock`，以 25ms 间隔等待，最多 2 秒；取得锁后重读磁盘状态，只修改目标字段，原子替换文件，再同步内存与资源索引。接收 context 的资源事务会在取消或 deadline 到达时提前返回。

账户更新以 `account.json` 原子写入为持久提交点。`internal/app` 先准备 `pending`（尚未提交）的固定出口，关闭旧 Worker并锁定该账户的 Worker 配置，再调用 `AccountLease.SaveConfig`；保存成功后依次提交 Worker 配置与固定出口。准备、关闭或保存失败时丢弃 pending 更新；保存后的租约释放错误原样返回，已发布配置继续生效。

账户调度先按每个账户实时 `ListModels` 返回的模型和方法筛选，再选择已经就绪且有并发槽位的 Worker。相同条件下优先使用目标模型最近首事件更快的账户。每个账号最多同时租用 `PER_ACCOUNT_CONCURRENCY` 个请求槽位；首个请求获取跨进程文件锁，最后一个请求释放。WAA proof 由账号 worker 串行生成，`GenerateContent` 由同一 Camoufox 页面并发发送并流式读取；请求前使用浏览器当前 Cookie 生成 Authorization，响应头到达后把浏览器 Cookie 原子同步到账户持久状态。其他 MakerSuite HTTP 响应的 Cookie 在响应头到达时与最新账户状态合并。未固定账户和资源的请求遇到可重试的 401、403、404、429、5xx 或单账户初始化超时时，可以在首个上游语义事件前继续切换尚未尝试的同能力账户；显式账户、Drive 文件和 Veo operation 始终保持创建账户粘性。Chrome 导入状态保留续签材料，HTTP `401` 时在同一固定出口续签一次、重建该账户 WAA runtime 并重放请求。

Worker 容量由热池目标、活动上限和单账户并发共同约束。活动数低于 `MAX_ACTIVE_WORKERS` 时直接启动并发布新 Worker。容量已满且存在空闲旧实例时，先启动 pending Worker（正在启动、尚未发布的替代 Worker），再关闭最久未用的空闲 Worker；旧实例成功关闭后发布替代 Worker。启动失败或取消时现有 Worker 继续服务。旧 Worker 与 pending 回收同时失败时，两份进程与租约均保留为 cleanup pending（仍待关闭）并占用容量槽，后续 Stop 会重试关闭。

账户状态：

| 状态 | 含义 |
| --- | --- |
| `ready` | 认证有效且存在可调度容量 |
| `busy` | 账户存在独占操作、认证刷新或活动请求；调度仍按 `PER_ACCOUNT_CONCURRENCY` 判断剩余槽位 |
| `cooldown` | 账户的全局 `*` 冷却仍有效；模型 `scope`（状态记录范围）冷却只影响对应请求的候选分类 |
| `auth_required` | 账户级认证失败，需要重新登录或续签 |
| `unavailable` | 当前运行时无法使用账户 |
| `disabled` | 账户配置已停用 |

认证结果携带 `authGeneration`（认证状态版本）与 `checkedAt`（检查时间），仅在账户对象、authGeneration 和时间顺序均匹配时应用；同一时间点的成功结果优先于失败。模型成功与冷却写回使用独立 `modelAccessGeneration`（模型状态版本）和 `checked_at`，目录变化时 modelAccessGeneration 递增，同一时间点已有 `verified` 时保留成功状态。

模型访问 scope：

| 操作 | scope | 成功与失败语义 |
| --- | --- | --- |
| 普通 GenerateContent | `<modelID>` | 规范 `EventFinish` 到达后写 `verified`；Code 7 清该模型成功记录 |
| CountTokens | `count-tokens:<modelID>` | 成功清该 scope 冷却，模型 `verified` 保持原值 |
| Transcribe | `<modelID>` | 非空文本或 segments 写 `verified`；生成 Code 7 清该模型记录 |
| Live 纯文本 | `<modelID>` | setup 成功写 `verified`；每次 `SendText` 开始一次模型资格检查，`turn_complete` 更新成功 |
| Live 音频或图像 | `bidi-media:<modelID>` | `SendMedia` 开始一次模型资格检查并更新媒体 scope |
| Robotics | `bidi-media:<modelID>` | `SendText` 开始一次模型资格检查，`turn_complete` 更新成功 |

`ModelAccessKey(scope, model)` 会移除 `models/` 前缀；空 scope 返回规范模型 ID，非空 scope 返回 `<scope>:<canonicalModelID>`。Bidi setup 使用 lease（账户租约）时间，会话内每个 qualifying turn（一次模型资格检查）分配严格递增的 attempt 时间，对应的 `turn_complete` 或 Code 7 消费同一次 attempt。普通流式生成在规范 `EventFinish` 到达时写 `verified`；此前的 text、reasoning、tool、usage 和首事件用于输出与性能统计，终态前断流、取消或错误保持原验证状态。

模型目录刷新为全部 enabled ready/busy 账户并发执行。同步报错或返回空目录的账户进入 generation 内的 pending ID 集合；每个非空结果立即更新公共目录、账户状态并在 RUNNING 期间预热更多 Worker。初次 fan-out（同时向全部符合条件的账户发出 `ListModels`）结束后，单个 30 秒 ticker（定时器）对排序后的 pending ID 再次并发刷新，账户删除会同步移除 pending ID。`modelRevision` 跟踪账户和配置变化，生成服务开始接收请求前会确认已应用当前 revision。

认证状态包含长期凭证和设备绑定材料，保存在本机受控目录。提交、Issue、CI 和普通日志使用脱敏材料，保留字段形状并替换 Cookie、token、proof、邮箱、账户 ID、提示词、响应正文和完整原始帧。

## 4. Go 协议层、公开端点与 Vue 管理端

公开端点统一读取同一份实时模型目录和规范事件：

| 协议 | 端点 |
| --- | --- |
| OpenAI Chat | `GET /v1/models`、`POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| OpenAI Files | `POST /v1/files`、`GET/DELETE /v1/files/{file}`、`GET /v1/files/{file}/content` |
| OpenAI 媒体 | `POST /v1/images/generations`、`POST /v1/audio/speech`、`POST /v1/videos`、`GET /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| OpenAI Transcribe | `POST /v1/audio/transcriptions` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、`:predictLongRunning`、`GET /v1beta/operations/{id}` |
| Realtime | `GET /v1/live`、`GET /v1/robotics/stream` |

管理端路由：

| 能力 | 路由 |
| --- | --- |
| 健康与状态 | `GET /health`、`GET /api/status` |
| 模型与账户 | `GET /api/models`、`GET/POST /api/accounts`、`PUT/DELETE /api/accounts/{id}` |
| 登录与验证 | `POST /api/accounts/{id}/login`、`POST /api/accounts/{id}/verify` |
| 生成服务 | `POST /api/control/start`、`POST /api/control/stop` |
| 配置 | `GET /api/config`、`PUT /api/config` |
| 冷却与请求 | `GET /api/cooldowns`、`GET /api/requests`、`POST /api/requests/{id}/cancel` |
| 日志与事件 | `DELETE /api/logs`、`GET /api/events` |

`/api` 接受 loopback 请求，并在请求带 `Origin` 时执行 same-origin 校验。`/v1` 与 `/v1beta` 使用公开 API key 与 CORS。

OpenAI Responses 的 `previous_response_id` 在当前进程内保存最多 256 个响应节点，用于重建下一轮完整 contents；进程重启后客户端应重新提交完整上下文。Drive 文件、Veo operation 和产物文件的账户绑定写入 `runtime-state.json`，重启后仍可轮询和下载。

新增上游能力从 `internal/aistudio` 开始：编码真实数组槽位、解码服务器事件，再由 `internal/api` 投影到公开协议。模型方法、上下文、输出上限、工具、声音、图片规格和视频规格均来自实时 `ListModels`。

`runtimeManager` 同时实现基础 `Service` 以及 Video、File、Bidi 和 Transcription 扩展接口。`internal/app` 负责资源账户绑定、跨账户文件复制、运行状态和重试流程；`internal/api` 负责 HTTP、SSE 与 WebSocket DTO，即公开协议使用的请求、响应和事件对象。完整请求字段、响应 DTO、SSE 和 WebSocket 事件见 [protocol.md](protocol.md)。

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

`internal/webui/embed.go` 使用 `//go:embed dist`，因此 Go 构建前必须生成当前前端产物。管理端从 `/api/events` 接收 `status`、`models`、`accounts`、`log`、`cooldowns` 和 `request` 事件。

## 5. 协议实现

新增上游能力按以下顺序实现：

1. 在 `internal/aistudio` 定义规范请求、响应类型与模型能力
2. 编码 [protocol.md](protocol.md) 中对应的 JSON+protobuf 数组
3. 将网络增量解码为规范事件
4. 由 `internal/api` 分别投影 OpenAI、Responses、Anthropic 和 Gemini 协议
5. 将结构化状态与操作入口绑定到管理端

`internal/api` 消费规范请求与事件，`internal/aistudio` 管理账户文件、WAA runtime、原始上游数组和实时模型能力。资源型操作在创建时记录账户 ID，后续轮询、下载与提示引用使用该账户。

已识别字段校验类型和 oneof 约束。未知非空槽保留为 provider 事件或原始扩展字段；无法解释的已消费字段、缺失完成帧和无效媒体内容返回结构化协议错误。

## 6. 构建与贡献

发布二进制前先构建前端：

```powershell
cd web
npm ci
npm run build
cd ..
go build -trimpath -o aistudio2api.exe ./cmd/aistudio2api
```

提交前运行定向 Go 测试和前端检查；发布前可执行完整 Go 测试：

```powershell
go test ./...
```

Windows 发布包包含 `aistudio2api.exe` 与 `start.bat`；其他平台使用同一 Go 程序。Camoufox 在首次启动时自动准备。贡献内容聚焦单一功能或协议变更，并使用脱敏后的请求与响应样例。

源码提交包含协议实现、前端源码和公开文档。本机账户状态、Cookie、token、proof、提示正文、响应正文和运行产物留在本机。
