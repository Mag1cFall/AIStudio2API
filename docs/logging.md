# 运行日志

管理页面与控制台使用同一组结构化事件。页面按请求汇总展示，控制台向标准错误输出逐行 JSON。日志来源使用 `service`、`request` 或实际执行账户的 Google 邮箱。

`LOG_FILE=true` 时控制台的 JSON 行同时写入 `runtime/logs/aistudio2api.log`。单个文件 10 MB 轮转，轮转文件名带本地时间，目录总量超过 `LOG_MAX_TOTAL_MB` 时删除最旧的文件。两项在管理端设置页保存后立即生效。

管理 API 通过 `GET /api/events` 推送历史快照和后续增量。账户创建等尚未绑定正式账户的认证操作使用 `auth` 来源。

## 日志结构

| 列 | 含义 | 示例 |
| --- | --- | --- |
| 时间 | 事件发生时间 | `22:26:46` |
| 级别 | 当前事件结果 | `INFO`、`WARN`、`ERROR` |
| 来源 | 服务、待调度请求或执行账户 | `service`、`request`、`account@example.com` |
| 消息 | 短消息与字段 | `生成服务就绪 模型数 39 耗时 10.686s` |

管理事件 DTO（管理 API 返回的事件对象）使用以下字段：

| 字段 | 语义 | 示例 |
| --- | --- | --- |
| `time` | RFC 3339 本地时间，带时区偏移；页面显示本地时间 | `2026-08-30T02:10:00+08:00` |
| `level` | 当前事件级别 | `WARN` |
| `source` | `service`、`request`、`auth` 或账户 label | `request` |
| `message` | 短消息 | `等待模型继续输出` |
| `fields` | 按顺序排列的字段 | `[{"key":"waited","value":"15s"}]` |
| `stage` | 所属阶段 | `{"name":"运行时装配","step":1,"steps":3}` |
| `event` | 稳定事件名 | `request.started`、`request.progress`、`request.finished`、`runtime.message` |
| `request` | 请求标识、状态、用量与诊断字段 | 请求事件携带该对象 |

`fields` 的 `key` 为英文标识，`value` 为取值文本。`stage` 的 `name` 为阶段名，`step` 与 `steps` 为步骤与总步骤数，`state` 为结束状态 `completed`、`failed` 或 `cancelled`。

`INFO` 记录状态推进和完成结果，`WARN` 记录等待、切换与客户端取消，`ERROR` 记录失败结果。管理页面支持按级别、来源、消息、阶段名、字段名与字段取值筛选，日志正文可以选择和横向滚动。

页面每行的消息列依次为展开箭头、状态位、标题与字段、行尾耗时，各行标题左缘对齐：请求的状态位为 HTTP 状态，阶段为进度或结束状态图标，单条运行事件为中性圆点；字段 `duration` 显示在行尾。

带 `stage` 的运行事件在页面上按来源与阶段名合并为一张卡片，显示阶段名、当前进度 `step/steps` 与最新一步的消息和字段。带 `state` 的事件结束同一来源进行中的卡片并显示结束状态，没有进行中的卡片时自成一张已结束的卡片；步骤号不大于当前步骤时另起一张卡片。卡片位于首条消息的时间位置，展开详情可查看每一步的时间、消息与字段；级别、来源和文本筛选在任一步命中时显示该卡片。

请求错误保存在 `request.error` 并直接展示在请求行下方。运行事件的错误保存在字段 `error`，原因保存在字段 `reason`，页面在消息下另起一行显示。

日志文件与控制台的每行 JSON 以 `msg` 写出消息，`stage` 写出阶段对象，每个字段写成同名属性。以下示例每行依次为级别、来源、方括号中的阶段（阶段名加 `step/steps` 或结束状态）、消息与 `字段=取值`；管理页面按界面语言显示阶段名、消息与字段名。

## 启动过程

管理进程先载入账户并启动控制面。完成后管理页面可以查看账户、配置和日志，此时生成服务保持 `STOPPED`。

```text
INFO  service  [运行时装配 1/3] 载入账户
INFO  service  [运行时装配 2/3] 使用 Camoufox WAA 后端 accounts=28
INFO  service  [运行时装配 3/3] 创建协议客户端 egress=直连
INFO  service  [运行时装配 completed] 协议运行时就绪 accounts=28 duration=31ms
INFO  service  [管理服务启动 1/2] 管理监听启动 addr=127.0.0.1:2048
INFO  service  [管理服务启动 2/2] 预览监听启动 addr=127.0.0.1:2050
INFO  service  [管理服务启动 completed] 管理服务就绪 addr=http://127.0.0.1:2048 duration=3ms
INFO  service  管理页面已打开 addr=http://127.0.0.1:2048
```

点击“启动服务”后状态进入 `LAUNCHING`，服务先读取当前 `generation`（一次启动所创建的生成服务实例）的 `CachedModels`：

- 缓存非空时状态进入 `RUNNING` 并开始接收请求，Worker 在后台预热；首个 Worker 有结果后，读取 `enabled` 且处于 `ready`、`busy` 或 `cooldown` 的账户中目录缓存缺失或最近一次同步已满 24 小时的账户会话页面载入时的权益与 `ListModels` 目录
- 没有模型缓存的 generation 先同时读取这些账户的页面目录，取得首个非空真实目录后进入 `RUNNING` 并在后台预热 Worker，其余账户继续后台同步
- 预热的首个 Worker 有结果之前到达的请求等待这个 Worker（指定账户的请求直接启动该账户的 Worker），之后热池未满时请求按需启动所选账户的 Worker；首轮目录同步完成后预热仍失败时记录 `WAA Worker 预热失败`（`error` 为原因），生成服务保持 `RUNNING`
- 可预热的账户全部处于冷却时记录 `WAA Worker 预热等待账户冷却`（`until` 为恢复时间）；冷却期内的请求按冷却规则排队或返回 429，冷却结束后开始预热
- `LAUNCHING` 期间点击“停止服务”会取消当前阶段并回到 `STOPPED`
- 有模型缓存时第 1 步记录 `准备模型目录`（`cached_models` 为缓存的模型数）；配置了多个代理时先记录 `检测代理池`；管理页侧栏在启动与停止期间显示同样的步骤与已用时间

```text
INFO  service              [生成服务启动] 开始启动生成服务 accounts=28
INFO  service              [生成服务启动 1/2] 等待首个账户读取模型目录
INFO  service              [生成服务启动 2/2] 预热 WAA Worker models=39 target_workers=5
INFO  service              模型目录后台同步完成 cached_accounts=0 synced_accounts=28 nonempty_accounts=28 models=39 retry_accounts=0 duration=2.792s
INFO  account@example.com  [WAA Worker 启动 completed] WAA Worker 就绪 page_model=gemini-flash-latest pid=18240 duration=8.172s
INFO  service              [生成服务启动 completed] 生成服务就绪 models=39 workers=1/5 duration=10.686s
INFO  service              WAA Worker 预热完成 workers=5/5 duration=34.903s
```

`准备模型目录` 的 `cached_models` 是启动瞬间当前 generation 合并后的真实模型数。`模型目录后台同步完成` 的字段：

| 字段 | 含义 |
| --- | --- |
| `cached_accounts` | 目录缓存未满 24 小时、本次启动沿用缓存的账户数 |
| `synced_accounts` | 本次无错误返回的账户数，包含空目录 |
| `nonempty_accounts` | 返回非空真实目录的账户数 |
| `models` | 合并后的公共模型数 |
| `retry_accounts` | `pending set`（等待重试的账户 ID 集合）的大小 |

每个非空结果到达时已经写入公共目录、发布管理事件并在运行期预热更多 Worker，因此后台完成日志可以出现在 `生成服务就绪` 之后。

目录为空、单账户同步失败或单账户同步成功但仍为空的账户进入 pending ID 集合。启动期同步结束后，处于 `RUNNING` 的生成服务使用单个 30 秒 ticker（定时器）重新载入全部 pending 账户的页面再次读取目录；非空成功立即更新账户、模型和 Worker 选择，失败与空结果继续保留：

```text
INFO  account@example.com  模型目录已更新 models=39
```

同一循环从启动期同步开始时起每 24 小时巡检一轮：

- 按权益从高到低、同权益按账户 ID 的顺序，逐个重新载入全部 `enabled` 且处于 `ready`、`busy` 或 `cooldown` 的账户的页面，读取权益与 `ListModels` 目录
- 没有空闲并发槽位或正在冷却的账户在本轮最后等到槽位空闲再读
- 有常驻 Worker 的账户重新载入它的页面，其余账户临时载入一个页面，取得响应后关闭
- 权益或模型数变化时更新账户调度与公开目录；读取失败时记录原因并把账户加入 pending ID 集合，登录失效的账户转为需要登录
- 订阅模型遇到额度错误时同样刷新该账户

请求的模型不在任何账户的目录中时，目录读取正在进行（启动期同步或巡检的一轮）则请求等到该模型出现在某个账户的目录中或这一轮读完；没有进行中的一轮且距上一轮开始已超过 10 分钟时巡检立即开始一轮，请求同样等到该模型出现或这一轮读完再查找模型；等待最长为 `REQUEST_TIMEOUT`。

巡检日志：

```text
INFO  account@example.com  WAA Worker 页面已重新载入 duration=4.521s
INFO  account@example.com  [WAA Worker 启动 completed] WAA Worker 临时页面已关闭 page_model=gemini-flash-latest duration=6.112s
WARN  account@example.com  WAA Worker 重新载入页面失败 duration=2.082s error=...
INFO  account@example.com  账户状态已更新 tier=Free → Ultra models=46 → 47
WARN  account@example.com  账户状态刷新失败 error=...
```

在线更新经两步写入同一张卡片，完成后管理进程重新启动：

```text
INFO  service  [在线更新 1/2] 下载发行包 version=v0.2.14
INFO  service  [在线更新 2/2] 替换可执行文件 version=v0.2.14
INFO  service  [在线更新 completed] 在线更新完成 version=v0.2.14
ERROR service  [在线更新 failed] 在线更新失败 error=...
```

取消与失败日志：

```text
INFO   service  [生成服务启动 cancelled] 生成服务启动已取消 duration=2.132s
ERROR  service  [生成服务启动 failed] 生成服务启动失败 duration=2.132s error=<ERROR>
```

启动失败、取消或 Stop 会通过 Go `context` 的取消信号停止模型目录 fan-out，并最多等待 2 秒；超时错误为 `模型目录刷新停止超时`。控制请求等待正在进行的启动或停止操作最多 12 秒，对应 `生成服务启动停止超时`、`生成服务停止超时` 或 `生成服务切换停止超时`；这些错误与 Worker 清理错误进入同一错误链。

停止生成服务会保留管理监听器，各行属于阶段“生成服务停止”，日志页合成一行，记录 Worker 数和总耗时；服务本来就停着时不写日志：

```text
INFO   service  生成服务停止 workers=5
INFO   service  停止在途请求 requests=2
INFO   service  关闭 WAA Worker workers=5
INFO   service  生成服务已停止 duration=3.204s
ERROR  service  生成服务停止失败 duration=10.006s error=<JOINED_ERROR>
```

`服务配置已保存` 表示 `.env` 已写入。管理进程配置通过进程重启应用；Worker 容量、账户选择策略、换号次数上限、模型映射、非流式保活、假流式、搜索型号、全模型搜索与媒体输出设置保存后立即应用，其余生成服务配置通过停止、启动生成服务应用。

### Worker 生命周期

每个账户从自己的实时目录选择 WAA 启动模型。`gemini-flash-latest` 在目录中可用且明确支持 `generateContent` 与 `chat` 能力字段时优先，随后按实时目录顺序尝试其余合格模型。日志保留实际页面模型。

WAA Bootstrap（页面初始化）在页面生成 proof 能力和动态请求头后，通过 WebDriver BiDi 终止用于初始化的 `GenerateContent`，模型输出量为零。首个候选完成后结束候选循环。

```text
INFO  account@example.com  [WAA Worker 启动 1/7] 初始化页面 page_model=gemini-flash-latest
INFO  account@example.com  [WAA Worker 启动 2/7] 准备浏览器配置
INFO  account@example.com  [WAA Worker 启动 3/7] 启动 Camoufox
INFO  account@example.com  [WAA Worker 启动 4/7] 连接 WebDriver BiDi
INFO  account@example.com  [WAA Worker 启动 5/7] 载入 AI Studio
INFO  account@example.com  [WAA Worker 启动 6/7] 定位 WAA 服务
INFO  account@example.com  [WAA Worker 启动 7/7] 执行 WAA Bootstrap
INFO  account@example.com  [WAA Worker 启动 completed] WAA Worker 就绪 page_model=gemini-flash-latest pid=18240 duration=10.842s
```

启动失败记录页面模型、耗时和原始错误。

Worker 运行中或浏览器登录期间 Camoufox 退出时，错误写明退出码与 Camoufox 最近 20 行输出（见 [Camoufox 后端](development.md#camoufox-后端)），据此判断原因：

- Windows 上退出码 `0xC0000409` 为浏览器崩溃；浏览器被其他程序强制结束时退出码由该程序决定，`taskkill /F` 为 `0x00000001`，PowerShell `Stop-Process` 为 `0xFFFFFFFF`；最近的输出给出崩溃前 Camoufox 打印的内容
- 错误为 `BiDi 连接已断开` 时，浏览器进程在连接断开 5 秒后仍在运行
- 浏览器登录期间退出码为 0 时，错误为 `登录窗口已关闭，登录没有完成`，表示登录窗口被关闭

热池动作记录在它引起的 Worker 启动或停止阶段中，字段 `trigger` 为起因，`workers` 为动作完成后的热池 Worker 数与 `MAX_ACTIVE_WORKERS`：

| 起因 | 记录位置 | 语义 |
| --- | --- | --- |
| `trigger=scale_up workers=N/M` | `WAA Worker 就绪` | 活动 Worker 超出常驻名额、未达 `MAX_ACTIVE_WORKERS`，新 Worker 成功发布 |
| `trigger=replace workers=N/M` | `WAA Worker 就绪` | `pending Worker`（正在启动的替代 Worker）成功启动并替换一个空闲 Worker |
| `trigger=replace` | 被替换账户的 `WAA Worker 停止` 阶段 | 空闲 Worker 为替代 Worker 让出名额 |
| `trigger=idle_reclaim workers=N/M` | `WAA Worker 已停止` | 超出热池目标的 Worker 空闲 5 分钟后关闭 |

```text
INFO   account@example.com  [WAA Worker 启动 completed] WAA Worker 就绪 trigger=scale_up workers=11/20 page_model=gemini-flash-latest pid=18240 duration=4.78s
INFO   account@example.com  [WAA Worker 停止 completed] WAA Worker 已停止 trigger=idle_reclaim workers=10/20 pid=18240 duration=136ms
```

| 事件 | 语义 |
| --- | --- |
| `WAA Worker 旧实例停止失败` | 旧实例关闭失败，发布中止并开始关闭 pending Worker |
| `WAA Worker 重建，重放当前请求` | 当前账户 Worker 已失效，业务请求在新实例重放；`reason` 为触发重建的错误 |
| `WAA Worker 已更新，重放当前请求` | 并发路径已经替换 Worker，当前请求使用新实例 |

单个 Worker 停止经 `WAA Worker 停止` 阶段写入同一张卡片：

```text
INFO   account@example.com  [WAA Worker 停止] WAA Worker 停止 pid=18240
INFO   account@example.com  [WAA Worker 停止 completed] WAA Worker 已停止 pid=18240 duration=3.014s
ERROR  account@example.com  [WAA Worker 停止 failed] WAA Worker 停止失败 pid=18240 duration=10.002s error=<JOINED_ERROR>
```

关闭上界由三个连续阶段构成：

| 阶段 | 上界 | 错误语义 |
| --- | ---: | --- |
| BiDi `session.end` | 3 秒 | 超时后关闭 BiDi 连接并继续关闭进程 |
| 进程终止与 `command.Wait` | 5 秒 | Windows 依次执行 `taskkill /T /F` 与 `Process.Kill`，失败原因保留 |
| 浏览器 profile 目录删除重试 | 2 秒 | 每 100ms 重试，最终返回最后一次 `RemoveAll` 错误 |

`Worker.Close` 的正常总体上界约为 10 秒，另加调度开销。关闭失败时 Worker、进程句柄、`runtime lease`（Worker 占用的账户租约）、热池记录和 generation（Worker 版本号）保持可重试；再次 Stop 会重跑关闭链。热替换中旧实例与 pending Worker 都关闭失败时，两份实例都占用容量槽。

## API 请求

请求事件通过 `request.id` 关联。页面将开始、进展和结束合并为一行：

- 标题行展示最新账户、HTTP 状态、模型、耗时和输出统计；请求换过账户时显示换号次数与按上游 HTTP 状态的分组计数
- 进行中的请求在标题行显示所处阶段：等待执行、已发出等待上游或正在输出，以及距最近一次数据的时长；换号后重新排队时回到等待执行
- 展开详情可查看接口、输入用量、原始终止原因、事件时间线与 JSON
- 时间线中每次换号为一行，显示账户、上游状态、按状态归类的原因与上游原文，点击展开该账户的全部事件；请求的来源按换号顺序写成 `A → B`，上游要求验证账户的 403 归为“需要验证账户”，跳转到拒绝页的归为“服务受限”
- 连续的同类进度事件合为一行并标出次数，悬停次数查看每一次的时刻与字段

```text
22:11:47  200  gemini-3.8-flash  换号 5  403 ×3  429 ×2  4.76 s  工具调用 1
思考 32  回复 38  输出合计 70 tokens  平均速度 14.7 tokens/s
```

| 字段 | 含义 |
| --- | --- |
| HTTP 状态 | 请求结果；流式响应开始后的错误仍按实际失败状态记录 |
| 耗时 | 从请求进入服务到处理结束，单位为秒 |
| 思考 | `request.usage.reasoning_tokens` |
| 回复 | `request.usage.reply_tokens`，包含文本、工具调用等非思考输出 |
| 输出合计 | `request.usage.output_tokens`，等于思考加回复 |
| 平均速度 | 输出合计除以请求总耗时，单位为 tokens/s，包含准备与等待时间 |
| 工具调用 | 本次模型输出的函数调用事件数 |
| 输入用量 | `request.usage.input_tokens`，包含输入消息与工具声明 |
| 缓存命中 | `request.usage.cached_tokens`，输入用量中命中上游隐式缓存的部分，为 0 时不显示 |
| 总用量 | `request.usage.total_tokens`，包含输入和输出 |

用量在生成服务返回 usage 后展示；用量未知时省略 `request.usage`。平均速度衡量端到端吞吐，网络缓冲或集中到达的响应也使用同一计算区间。

`request.state` 区分 `running`、`completed`、`tool_calls`、`limited`、`blocked`、`failed` 和 `cancelled`。策略终止保留 HTTP `200` 并显示原始 `finish_reason`；达到输出上限时显示 `max_tokens`。未知终止原因保留为 `provider_<code>`。

控制台每行是一条独立 JSON 事件，请求数据与摘要分离；控制台中的摘要与错误按操作系统语言输出，日志文件中为简体中文：

```json
{"time":"2026-09-06T22:11:47.248+08:00","level":"INFO","msg":"工具调用完成","event":"request.finished","source":"account@example.com","request":{"id":"chatcmpl_example","state":"tool_calls","model":"gemini-3.8-flash","status":200,"duration_ms":4758,"tool_calls":1,"finish_reason":"stop","usage":{"input_tokens":100,"cached_tokens":0,"reasoning_tokens":32,"reply_tokens":38,"output_tokens":70,"total_tokens":170,"average_tokens_per_second":14.712064}}}
```

采样参数保存在 `request.parameters`。`first_event_ms`、`queue_ms`、`proof_ms` 和 `upstream_bytes` 作为 JSON 诊断字段保留：

- `queue_ms` 是请求进入服务到取得最终执行账户的时间
- `proof_ms` 是等待并生成 WAA proof 的累计时间
- `proof_phases` 把 `proof_ms` 分为等待页面 `lock_ms`、页面操作 `interaction_ms`（到页面应用发出请求为止）、等待 WAA 刷新 `refresh_ms` 与 WAA 计算 `compute_ms`；请求卡片展开后的“WAA 耗时”与用量页请求详情按这四项显示

页面支持按级别、账户以及模型、请求 ID、状态码搜索。

客户端取消记录为 `499`；认证、额度和上游失败分别使用对应 HTTP 状态与 `request.error`。

管理端取消活动请求时，仍连接的客户端按公开协议收到 `503 request_canceled` 或对应的流式 error event；停止生成服务时收到 `503 service_stopped` 或对应的流式 error event，错误消息写明生成服务已停止。

## 流式等待

连续 15 秒没有阶段进展时会写入诊断日志。请求在收到上游终态、客户端取消或达到 `REQUEST_TIMEOUT` 时结束。

| 事件 | 当前停留位置 | 重点字段 |
| --- | --- | --- |
| `请求准备等待` | 账户已选定，正在准备 WAA proof 或等待 GenerateContent 响应头 | `current_step`、`model` |
| `请求准备结束` | 准备阶段超过 15 秒后开始接收响应体 | `waited`、`waa`、`response_headers`、`model` |
| `上游首事件等待` | 上游响应体已经建立，尚未解出语义事件 | `network_bytes`、`network_idle` |
| `上游首事件到达` | 首事件等待后解出第一个语义事件 | `waited`、`first_event`、`model` |
| `等待模型继续输出` | 已有语义事件，连续 15 秒没有下一事件；模型在正文之后生成函数调用参数时，上游生成完整个调用才发出；上游在输出中途审核后续内容时，审完放出剩余内容或以 `OTHER` 拦截结束；两种情况期间连接上都没有数据 | `last_event`、`reasoning_events`、`text_events`、`network_bytes`、`network_idle` |
| `模型继续输出` | 等待后解出下一语义事件 | `waited`、`current_event` |
| `账号切换` | 当前账户在首个上游语义事件前失败，或非流式请求的上游流中途中断，额度以外的失败最多换 `MAX_ACCOUNT_SWITCHES` 次；`续写轮账号切换` 为自动续写轮在当前账户上失败后换账户重发 | 当前账户来源、`model`、上游 HTTP 状态 `status` 与协议错误码 `code`、原始原因 `reason` |
| `同模型同通道最近两个请求都因连接断开失败，本请求也连续两次连接断开，停止重试` | 同一模型与通道最近两个请求都在本请求开始前 30 秒内以连接断开失败，本请求在前两个账户上的尝试也都以连接断开失败，以原错误结束 | 当前账户来源、`model`、原始原因 `reason` |
| `备用请求发起` | 原请求等待首个语义事件达到 `FIRST_EVENT_HEDGE` 的阈值，空闲账户开始同一请求 | 备用账户来源、`primary_waited`、`threshold`、`model` |
| `备用请求胜出` | 备用请求先解出首个语义事件，原请求取消 | 备用账户来源、`primary_waited`、`model` |
| `原请求胜出，取消备用请求` | 原请求先解出首个语义事件，备用请求取消 | `hedge_account`、`model` |

`current_step` 写明请求停在哪一步：SpiderMonkey 后端为 `WAA proof` 或 `等待上游响应头`；Camoufox 后端在页面界面运行时依次为 `取得标签页`、`等待其他请求的界面操作`、`选中标签页`、`新开对话`、`粘贴提示词`、`等待 Run 可点`、`点击 Run`（再次运行同一段对话为 `点击 Rerun`，函数结果为 `粘贴函数结果`、`点击提交`）、`等待页面发出请求`，页面发出请求后为 `等待上游响应头`。界面步骤各有上限：取得标签页 2 分钟，等待其他请求的界面操作 2 分钟，选中标签页到点击结束 1 分钟，页面开始运行后 30 秒内发出请求；超过上限时请求以 `界面运行停在<步骤>，<上限> 内没有完成` 失败。

`network_bytes` 是当前账户尝试累计读取的上游响应体字节数，`network_idle` 是最近一次读取距日志时刻的时间。`network_bytes=0` 表示响应体尚未产生数据；数值增长且 `network_idle` 较短，表示网络仍有数据进入、解码器尚未形成新的语义事件。

```text
WARN  account@example.com  请求准备等待 waited=15s current_step=等待上游响应头 model=gemini-3.7-flash
INFO  account@example.com  请求准备结束 waited=47.755s waa=1.204s response_headers=46.551s model=gemini-3.7-flash
```

```text
WARN  account@example.com  上游首事件等待 waited=15s model=gemini-3.7-flash network_bytes=0
INFO  account@example.com  上游首事件到达 waited=28.447s first_event=reasoning model=gemini-3.7-flash
```

```text
INFO  account@example.com  等待模型继续输出 model=gemini-3.7-flash waited=15s last_event=text reasoning_events=4 text_events=12 network_bytes=18240 network_idle=15.001s
INFO  account@example.com  模型继续输出 model=gemini-3.7-flash waited=18.4s current_event=tool_call
```

所有流式公开协议在连续 10 秒没有语义事件时发送 SSE 注释帧：

```text
: ping

```

SSE 客户端把该帧作为连接存活信号，正文、推理和 usage 继续使用各协议的 `data` 或命名事件。Anthropic 在调用生成服务前发送起始事件；OpenAI Chat 与 Responses 在取得生成事件流后发送起始事件。

## 账户事件

HTTP `401` 会使用账户保存的 OAuth/DBSC 材料续签 Cookie；材料被拒绝时，从本机 Chrome 重新导入同一 Google 账户的材料。随后重置 WAA Worker 并重放一次请求。没有 OAuth 材料的账户按认证恢复失败处理。同一账户在续签完成前发出的请求同时收到 `401` 时只续签一次，其余请求等这次续签完成后直接重放。

```text
INFO  account@example.com  [账户认证续签 1/2] 刷新 Cookie
INFO  account@example.com  [账户认证续签] 更新 Chrome 来源材料
INFO  account@example.com  [账户认证续签 2/2] 重置协议运行时
INFO  account@example.com  [账户认证续签 completed] 账户认证续签完成 duration=1.116s
ERROR account@example.com  [账户认证续签 failed] 账户认证续签失败 duration=1.116s error=<ERROR>
```

HTTP `403` 与协议 Code 7 保留当前账户和模型的 `verified` 记录。官网不跳转拒绝页的这类拒绝：

- 自上次成功后第一次出现时只换号；没有其他账户可换时在该账户上重试一次，并记 WARN “没有其他账户可换，同账户重试被上游拒绝的请求”
- 连续第二次起，该账户在该模型上冷却 1、5、15、30、60 分钟，类型为 `权限拒绝`，成功一次后重新计数
- 全部候选账户都在冷却时，权限拒绝冷却中的账户仍依次接收请求，每个账户一次

其他错误：

- 协议 Code 5、Worker 进程失败或 Worker 被替换时，重建当前账户 Worker 并重放一次
- 额度、权限拒绝与服务受限的失败直接换号
- 其他可重试错误写入临时冷却

```text
WARN  account@example.com  账号冷却 kind=权限拒绝 scope=gemini-3.7-flash until=2026-11-12T20:41:07+08:00
WARN  account@example.com  账号切换 model=gemini-3.7-flash status=403 code=7 reason=AI Studio GenerateContent 返回 HTTP 403、协议错误码 7: The caller does not have permission
```

账户页的 `Free`、`Pro`、`Ultra` 与 `Plus` 来自 `GetAiStudioBenefitTier`。Paid 模型先按权益和模型访问方式筛选，成功调用过目标模型的账户排在同模型未知账户之前。

管理端账户操作事件：

| 事件 | 结果 |
| --- | --- |
| `[账户添加 1/2]`、`[账户添加 2/2]`、`账户添加完成` | 隔离登录、保存认证状态、同步模型目录 |
| `[账户登录 1/2]`、`[账户登录 2/2]`、`账户登录完成` | 更新当前账户认证材料与 Worker 实例 |
| `[账户导入 n/m]`、`Chrome 账户已导入`、`认证状态账户已导入`、`账户导入失败` | 读取 Chrome 登录状态、检查 AI Studio 访问、保存账户；结束带耗时 |
| `[账户刷新 n/m]`、`账户刷新完成`、`账户刷新失败` | 立即刷新的等待在途请求、读取权益与模型目录、核对验证状态；结束带耗时，写明是否解除服务受限 |
| `等待账户的在途请求结束` | 保存、目录同步或立即刷新在等该账户的请求结束 |
| `账户配置已更新`、`账户已删除`、`账户删除失败` | 账户配置与运行对象已更新，或删除失败的原因 |
| `账户停用完成` | 停用后在途请求已结束、Worker 已关闭，带耗时 |
| `账户模型目录同步完成` | 新目录已写入当前模型目录和账户调度状态 |
| `账户模型目录等待重试` | 当前同步失败，运行期重试继续处理 |

资源与 `scope`（状态记录范围）事件：

| 事件 | 语义 |
| --- | --- |
| `文件引用复制` | Drive file 已临时复制到本次目标账户 |
| `内联附件处理完成` | 本次请求的附件数量、原始字节数与处理耗时 |
| `临时文件清理失败` | 本次请求的临时 Drive 文件回收失败，原始错误保留 |
| `到期资源删除失败` | 到期资源回收的 Drive 删除失败，资源恢复绑定、下一轮重试，原始错误保留 |
| `Build 写代码助手伴随请求失败` | 写代码助手的伴随请求失败，本轮回复不受影响，原始错误保留 |
| `Build 写代码助手取消任务失败` | 回复没有完整结束时取消上游任务失败，原始错误保留 |
| `保存 Build 写代码助手续接位置失败` | trajectory 续接位置写入账户运行状态失败，下一轮开启新 trajectory，原始错误保留 |
| `Build 写代码助手新建云项目失败`、`Build 写代码助手生成 API key 失败` | 新建应用前取得 API key 的一步失败，继续下一步，原始错误保留 |
| `账号切换` | 转录生成阶段在首个结果前、Live/Robotics setup 阶段切换候选 |

较晚返回的认证结果只有在 `authGeneration` 和 `checkedAt` 均匹配当前账户时才应用；较晚返回的模型成功或冷却结果只有在 `modelAccessGeneration` 和 `checked_at` 均匹配当前模型目录时才应用。日志记录实际应用到当前状态的变更和持久化错误。

## 用量账本

`runtime/requests.db` 是本地 SQLite 账本。

记录：

- `/v1` 与 `/v1beta` 通过 API key 校验的每个 POST 请求完成后写入一行：请求 ID、完成时间、协议、路径、模型、账户、通道、HTTP 状态、`request.state`、耗时、首个上游事件、排队时间、token 用量（含缓存命中数）、工具调用数、错误摘要，以及最终结果之前未成功的上游尝试
- 写入由后台协程批量提交，响应照常返回；等待写入的记录超过 4096 条时丢弃新记录，写入恢复后输出丢弃数
- 记录与汇总按服务器本地日整日保留 90 天

汇总：

- 同一事务按 UTC 小时与服务器本地日累加汇总与耗时分布
- 查询时完整落在范围与单个分桶内的本地日读取日汇总，其余整小时读取小时汇总，范围边缘与跨分桶的部分读取原始记录
- 服务器时区变化后，下次启动时从小时汇总与原始记录重建本地日汇总

查询与费用：

- 管理页的用量看板通过 `GET /api/usage`、`GET /api/usage/pairs` 与 `GET /api/usage/records` 查询
- 看板的估算费用按 Gemini API 与 Vertex AI 公布的当前文本单价从原始记录逐条计算，缓存命中的输入按缓存单价计算

```text
INFO  service  请求账本已按服务器时区重建本地日汇总 days=<COUNT>
WARN  service  请求账本写入队列已满 dropped=<COUNT>
ERROR service  请求账本写入失败 records=<COUNT> error=<ERROR>
ERROR service  请求账本清理失败 error=<ERROR>
```

`REQUEST_BODY_LOG=true` 时，账本同时保存通过 API key 校验的 POST 请求与响应的原始正文，各截断到 64 KiB 并记录原始字节数，只保留最近 1000 条；流式响应保存 SSE 原文。正文包含提示词与模型输出，以 gzip 压缩的明文保存在本机。请求日志展开后或用量页的请求详情中按请求 ID 查看正文。

## 管理事件流

管理页面连接 `GET /api/events` 时先收到当前状态、模型、账户、最近 200 条日志事件、冷却和活动请求，随后接收增量事件。请求事件在页面按 ID 汇总，控制台通过 Go `slog.JSONHandler` 输出逐行 JSON。

初始事件顺序：

1. `status`
2. `models`
3. `accounts`
4. 最近 200 条 `log`
5. `cooldowns`
6. 按开始时间排序的活动 `request`

后续增量事件类型为 `status`、`models`、`accounts`、`log`、`cooldowns` 和 `request`。每个 SSE `data` 行是 `{"type":"<TYPE>","data":<DTO>}`，其中 `<DTO>` 是对应类型的事件对象；字段定义见 [protocol.md](protocol.md)。`status`、`models`、`accounts` 与 `cooldowns` 只在内容与该连接上次收到的不同时发送。
