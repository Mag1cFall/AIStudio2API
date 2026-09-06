# 运行日志

管理页面与控制台使用同一组结构化事件。页面按请求汇总展示，控制台向标准错误输出逐行 JSON。日志来源使用 `service`、`request` 或实际执行账户的 Google 邮箱。

管理 API 通过 `GET /api/events` 推送历史快照和后续增量。账户创建等尚未绑定正式账户的认证操作使用 `auth` 来源。

## 日志结构

| 列 | 含义 | 示例 |
| --- | --- | --- |
| 时间 | 事件发生时间 | `22:26:46` |
| 级别 | 当前事件结果 | `INFO`、`WARN`、`ERROR` |
| 来源 | 服务、待调度请求或执行账户 | `service`、`request`、`account@example.com` |
| 消息 | 阶段、现场指标和错误 | `生成服务就绪`、`事件流停顿` |

管理事件 DTO（管理 API 返回的事件对象）使用以下字段：

| 字段 | 语义 | 示例 |
| --- | --- | --- |
| `time` | RFC 3339 时间；页面显示本地时间 | `2026-08-30T02:10:00+08:00` |
| `level` | 当前事件级别 | `WARN` |
| `source` | `service`、`request`、`auth` 或账户 label | `request` |
| `message` | 阶段、指标和原始错误 | `事件流停顿 | ...` |
| `event` | 稳定事件名 | `request.started`、`request.progress`、`request.finished`、`runtime.message` |
| `request` | 请求标识、状态、用量与诊断字段 | 请求事件携带该对象 |

`INFO` 记录状态推进和完成结果，`WARN` 记录等待、切换与客户端取消，`ERROR` 记录失败结果。管理页面支持按级别、来源和消息文本筛选，日志正文可以选择和横向滚动。

请求错误保存在 `request.error` 并直接展示在请求行下方。运行事件的错误详情保留在 `message` 中。

## 启动过程

管理进程先载入账户并启动控制面。完成后管理页面可以查看账户、配置和日志，此时生成服务保持 `STOPPED`。

```text
INFO  service  运行时装配 | 1/3 | 载入账户
INFO  service  运行时装配 | 2/3 | 校验 Camoufox | 账户=28
INFO  service  运行时装配 | 3/3 | 创建协议客户端
INFO  service  协议运行时就绪 | 账户=28 | 耗时=31ms
INFO  service  管理监听启动 | 地址=127.0.0.1:2048
INFO  service  管理服务就绪 | 地址=http://127.0.0.1:2048
INFO  service  管理页面已打开 | 地址=http://127.0.0.1:2048
```

点击“启动服务”后状态进入 `LAUNCHING`。服务先读取当前 `generation`（一次启动所创建的生成服务实例）的 `CachedModels`，并为全部 `enabled`、`ready` 或 `busy` 账户并发执行 `ListModels`。缓存非空时立即预热 Worker；没有模型缓存的 generation 等待首个非空真实目录，其余账户继续后台同步。首个 Worker 就绪后状态进入 `RUNNING` 并开始接收请求，其余目标 Worker 继续预热。`LAUNCHING` 期间点击“停止服务”会取消当前阶段并回到 `STOPPED`。

```text
INFO  service              生成服务启动 | 1/2 | 准备模型目录 | 缓存=0
INFO  service              生成服务启动 | 2/2 | 预热 WAA Worker | 模型=39 | 目标=5
INFO  service              模型目录后台同步完成 | 同步=28 | 非空=28 | 模型=39 | 待重试账户=0 | 耗时=2.792s
INFO  account@example.com  WAA Worker 就绪 | 页面模型=gemini-flash-latest | PID=18240 | 耗时=8.172s
INFO  service              生成服务就绪 | 模型=39 | Worker=1/5 | 耗时=10.686s
INFO  service              WAA Worker 预热完成 | Worker=5/5 | 耗时=34.903s
```

`准备模型目录` 的 `缓存` 是启动瞬间当前 generation 合并后的真实模型数。`模型目录后台同步完成` 的 `同步` 是本次无错误返回的账户数，包含空目录；`非空` 是返回非空真实目录的账户数；`模型` 是合并后的公共模型数；`待重试账户` 是 `pending set`（等待重试的账户 ID 集合）的大小。每个非空结果到达时已经写入公共目录、发布管理事件并在运行期预热更多 Worker，因此后台完成日志可以出现在 `生成服务就绪` 之后。

目录为空、单账户同步失败或单账户同步成功但仍为空的账户进入 pending ID 集合。初次 `fan-out`（同时向全部符合条件的账户发出 `ListModels`）结束后，处于 `RUNNING` 的生成服务使用单个 30 秒 ticker（定时器）对全部 pending ID 再次并发执行 `ListModels`；非空成功立即更新账户、模型和 Worker 选择，失败与空结果继续保留：

```text
INFO  service  模型目录重试完成 | 同步=2 | 非空=2 | 待重试账户=0
```

取消与失败日志：

```text
INFO   service  生成服务启动已取消 | 耗时=2.132s
ERROR  service  生成服务启动失败 | 耗时=2.132s | 错误=<ERROR>
```

启动失败、取消或 Stop 会通过 Go `context` 的取消信号停止模型目录 fan-out，并最多等待 2 秒；超时错误为 `模型目录刷新停止超时`。控制请求等待正在进行的启动或停止操作最多 12 秒，对应 `生成服务启动停止超时`、`生成服务停止超时` 或 `生成服务切换停止超时`；这些错误与 Worker 清理错误进入同一错误链。

停止生成服务会保留管理监听器，并记录 Worker 数和总耗时：

```text
INFO   service  生成服务停止 | Worker=5
INFO   service  生成服务已停止 | 耗时=3.204s
INFO   service  生成服务已处于停止状态
ERROR  service  生成服务停止失败 | 耗时=10.006s | 错误=<JOINED_ERROR>
```

`服务配置已保存` 表示 `.env` 已写入。管理进程配置通过进程重启应用；生成服务配置通过停止、启动生成服务应用。

### Worker 生命周期

每个账户从自己的实时目录选择 WAA 启动模型。`gemini-flash-latest` 在目录中可用且明确支持 `generateContent` 与 `chat` 能力字段时优先，随后按实时目录顺序尝试其余合格模型。日志保留实际页面模型。

WAA Bootstrap（页面初始化）在页面生成 proof 能力和动态请求头后，通过 WebDriver BiDi 终止用于初始化的 `GenerateContent`，模型输出量为零。首个候选完成后结束候选循环。

```text
INFO  account@example.com  WAA Worker 启动 | 1/7 | 初始化页面 | 页面模型=gemini-flash-latest
INFO  account@example.com  WAA Worker 启动 | 2/7 | 准备浏览器配置
INFO  account@example.com  WAA Worker 启动 | 3/7 | 启动 Camoufox
INFO  account@example.com  WAA Worker 启动 | 4/7 | 连接 WebDriver BiDi
INFO  account@example.com  WAA Worker 启动 | 5/7 | 载入 AI Studio
INFO  account@example.com  WAA Worker 启动 | 6/7 | 定位 WAA 服务
INFO  account@example.com  WAA Worker 启动 | 7/7 | 执行 WAA Bootstrap
INFO  account@example.com  WAA Worker 就绪 | 页面模型=gemini-flash-latest | PID=18240 | 耗时=10.842s
```

启动失败记录页面模型、耗时和原始错误。按需容量事件说明当前热池动作：

| 事件 | 语义 |
| --- | --- |
| `WAA Worker 按需扩容 | Worker=N/M` | 活动 Worker 未达 `MAX_ACTIVE_WORKERS`，新 Worker 成功发布 |
| `WAA Worker 按需替换 | Worker=N/M` | `pending Worker`（正在启动的替代 Worker）成功启动并替换一个空闲 Worker |
| `WAA Worker 旧实例停止失败` | 旧实例关闭失败，发布中止并开始关闭 pending Worker |
| `WAA Worker 重建 | 模型=... | 重放当前请求` | 当前账户 Worker 已失效，业务请求在新实例重放 |
| `WAA Worker 已更新 | 模型=... | 重放当前请求` | 并发路径已经替换 Worker，当前请求使用新实例 |

单个 Worker 停止事件：

```text
INFO   account@example.com  WAA Worker 停止 | PID=18240
INFO   account@example.com  WAA Worker 已停止 | PID=18240 | 耗时=3.014s
ERROR  account@example.com  WAA Worker 停止失败 | PID=18240 | 耗时=10.002s | 错误=<JOINED_ERROR>
```

关闭上界由三个连续阶段构成：

| 阶段 | 上界 | 错误语义 |
| --- | ---: | --- |
| BiDi `session.end` | 3 秒 | 超时后关闭 BiDi 连接并继续关闭进程 |
| 进程终止与 `command.Wait` | 5 秒 | Windows 依次执行 `taskkill /T /F` 与 `Process.Kill`，失败原因保留 |
| 浏览器 profile 目录删除重试 | 2 秒 | 每 100ms 重试，最终返回最后一次 `RemoveAll` 错误 |

`Worker.Close` 的正常总体上界约为 10 秒，另加调度开销。关闭失败时 Worker、进程句柄、`runtime lease`（Worker 占用的账户租约）、热池记录和 generation（Worker 版本号）保持可重试；再次 Stop 会重跑关闭链。热替换中旧实例与 pending Worker 都关闭失败时，两份实例都占用容量槽。

## API 请求

请求事件通过 `request.id` 关联。页面将开始、进展和结束合并为一行，展示最新账户、HTTP 状态、模型、耗时和输出统计。展开详情可查看接口、输入用量、原始终止原因、完整事件时间线与 JSON。

```text
22:11:47  200  gemini-3.8-flash  4.76 s  工具调用 1
思考 32 · 回复 38 · 输出合计 70 tokens · 平均速度 14.7 tokens/s
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
| 总用量 | `request.usage.total_tokens`，包含输入和输出 |

用量在生成服务返回 usage 后展示；用量未知时省略 `request.usage`。平均速度衡量端到端吞吐，网络缓冲或集中到达的响应也使用同一计算区间。

`request.state` 区分 `running`、`completed`、`tool_calls`、`limited`、`blocked`、`failed` 和 `cancelled`。策略终止保留 HTTP `200` 并显示原始 `finish_reason`；达到输出上限时显示 `max_tokens`。未知终止原因保留为 `provider_<code>`。

控制台每行是一条独立 JSON 事件，请求数据与中文摘要分离：

```json
{"time":"2026-09-06T14:11:47Z","level":"INFO","msg":"工具调用完成","event":"request.finished","source":"account@example.com","request":{"id":"chatcmpl_example","state":"tool_calls","model":"gemini-3.8-flash","status":200,"duration_ms":4758,"tool_calls":1,"finish_reason":"stop","usage":{"input_tokens":100,"reasoning_tokens":32,"reply_tokens":38,"output_tokens":70,"total_tokens":170,"average_tokens_per_second":14.712064}}}
```

采样参数保存在 `request.parameters`。`first_event_ms` 和 `upstream_bytes` 作为 JSON 诊断字段保留。页面支持按级别、账户以及模型、请求 ID、状态码搜索。

客户端取消记录为 `499`；认证、额度和上游失败分别使用对应 HTTP 状态与 `request.error`。

管理端取消活动请求或停止生成服务时，仍连接的客户端按公开协议收到 `503 request_canceled` 或对应的流式 error event。

## 流式等待

连续 15 秒没有阶段进展时会写入诊断日志。请求在收到上游终态、客户端取消或达到 `REQUEST_TIMEOUT` 时结束。

| 事件 | 当前停留位置 | 重点字段 |
| --- | --- | --- |
| `请求准备等待` | 账户已选定，正在准备 WAA proof 或等待 GenerateContent 响应头 | `当前`、`模型` |
| `请求准备结束` | 准备阶段超过 15 秒后开始接收响应体 | `等待`、`WAA`、`响应头`、`模型` |
| `上游首事件等待` | 上游响应体已经建立，尚未解出语义事件 | `网络字节`、`最近网络` |
| `上游首事件到达` | 首事件等待后解出第一个语义事件 | `等待`、`事件`、`模型` |
| `事件流停顿` | 已有语义事件，连续 15 秒没有下一事件 | `最近事件`、`推理`、`正文`、`网络字节`、`最近网络` |
| `事件流恢复` | 停顿后解出下一语义事件 | `停顿`、`当前事件` |
| `账号切换` | 当前账户在首个上游语义事件前失败 | 当前账户来源、模型和下一行原始原因 |

`网络字节` 是当前账户尝试累计读取的上游响应体字节数，`最近网络` 是最近一次读取距日志时刻的时间。`网络字节=0` 表示响应体尚未产生数据；数值增长且 `最近网络` 较短，表示网络仍有数据进入、解码器尚未形成新的语义事件。

```text
WARN  account@example.com  请求准备等待 | 已等待=15s | 当前=等待上游响应头 | 模型=gemini-3.7-flash
INFO  account@example.com  请求准备结束 | 等待=47.755s | WAA=1.204s | 响应头=46.551s | 模型=gemini-3.7-flash
```

```text
WARN  account@example.com  上游首事件等待 | 已等待=15s | 模型=gemini-3.7-flash | 网络字节=0
INFO  account@example.com  上游首事件到达 | 等待=28.447s | 事件=reasoning | 模型=gemini-3.7-flash
```

```text
WARN  account@example.com  事件流停顿 | 模型=gemini-3.7-flash | 已等待=15s | 最近事件=reasoning | 推理=4 | 正文=0 | 网络字节=18240 | 最近网络=15.001s
INFO  account@example.com  事件流恢复 | 模型=gemini-3.7-flash | 停顿=1m31.208s | 当前事件=reasoning
```

所有流式公开协议在连续 10 秒没有语义事件时发送 SSE 注释帧：

```text
: ping

```

SSE 客户端把该帧作为连接存活信号，正文、推理和 usage 继续使用各协议的 `data` 或命名事件。Anthropic 在调用生成服务前发送起始事件；OpenAI Chat 与 Responses 在取得生成事件流后发送起始事件。

## 账户事件

HTTP `401` 会使用该 Chrome 导入账户保存的 OAuth/DBSC 材料续签 Cookie、重置 WAA Worker并重放一次请求。

```text
INFO  account@example.com  账户认证续签 | 1/2 | 刷新 Cookie
INFO  account@example.com  账户认证续签 | 2/2 | 重置协议运行时
INFO  account@example.com  账户认证续签完成 | 耗时=1.116s
ERROR account@example.com  账户认证续签失败 | 耗时=1.116s | 错误=<ERROR>
```

HTTP `403` 与协议 Code 7 保留当前账户和模型的 `verified` 记录，不标记永久权限失败。协议 Code 5、Worker 进程失败或 Worker 被替换时重建当前账户 Worker 并重放一次；其他可重试错误写入临时冷却。

```text
WARN  account@example.com  账号切换 | 模型=gemini-3.7-flash
                           原因: AI Studio GenerateContent 返回 HTTP 403、协议错误码 7: The caller does not have permission
```

账户页的 `Free`、`Pro`、`Ultra` 与 `Plus` 来自 `GetAiStudioBenefitTier`。Paid 模型先按权益和模型访问方式筛选，成功调用过目标模型的账户排在同模型未知账户之前。

管理端账户操作事件：

| 事件 | 结果 |
| --- | --- |
| `账户添加 | 1/2`、`2/2`、`账户添加完成` | 隔离登录、保存认证状态、同步模型目录 |
| `账户登录 | 1/2`、`2/2`、`账户登录完成` | 更新当前账户认证材料与 Worker 实例 |
| `账户验证`、`账户验证完成` | 验证 AI Studio、权益与实时目录 |
| `账户配置已更新`、`账户已删除` | 账户配置与运行对象已更新 |
| `账户模型目录同步完成` | 新目录已写入当前模型目录和账户调度状态 |
| `账户模型目录等待重试` | 当前同步失败，运行期重试继续处理 |

资源与 `scope`（状态记录范围）事件：

| 事件 | 语义 |
| --- | --- |
| `文件引用复制` | Drive file 已临时复制到本次目标账户 |
| `临时文件清理失败` | 跨账户临时副本回收失败，原始错误保留 |
| `转录账号切换` | 转录生成阶段在首个结果前切换候选 |
| `Bidi 账号切换` | Live/Robotics setup 阶段切换候选 |

较晚返回的认证结果只有在 `authGeneration` 和 `checkedAt` 均匹配当前账户时才应用；较晚返回的模型成功或冷却结果只有在 `modelAccessGeneration` 和 `checked_at` 均匹配当前模型目录时才应用。日志记录实际应用到当前状态的变更和持久化错误。

## 管理事件流

管理页面连接 `GET /api/events` 时先收到当前状态、模型、账户、最近约 2000 条日志事件、冷却和活动请求，随后接收增量事件。请求事件在页面按 ID 汇总，控制台通过 Go `slog.JSONHandler` 输出逐行 JSON。

初始事件顺序：

1. `status`
2. `models`
3. `accounts`
4. 最多 2000 条 `log`
5. `cooldowns`
6. 按开始时间排序的活动 `request`

后续增量事件类型为 `status`、`models`、`accounts`、`log`、`cooldowns` 和 `request`。每个 SSE `data` 行是 `{"type":"<TYPE>","data":<DTO>}`，其中 `<DTO>` 是对应类型的事件对象；字段定义见 [protocol.md](protocol.md)。
