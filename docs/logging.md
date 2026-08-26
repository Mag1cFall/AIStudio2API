# 运行日志

管理页面与控制台显示同一组运行事件。日志来源使用 `service`、`request` 或实际执行账户的 Google 邮箱。

## 日志结构

| 列 | 含义 | 示例 |
| --- | --- | --- |
| 时间 | 事件发生时间 | `22:26:46` |
| 级别 | 当前事件结果 | `INFO`、`WARN`、`ERROR` |
| 来源 | 服务、待调度请求或执行账户 | `service`、`request`、`account@example.com` |
| 消息 | 阶段、现场指标和错误 | `生成服务就绪`、`事件流停顿` |

`INFO` 记录状态推进和完成结果，`WARN` 记录等待、切换与客户端取消，`ERROR` 记录失败结果。管理页面支持按级别、来源和消息文本筛选，日志正文可以选择和横向滚动。

## 启动过程

管理进程先载入账户并启动控制面。完成后管理页面可以查看账户、配置和日志，此时生成服务保持 `STOPPED`。

```text
INFO  service  应用启动 | 1/4 | 载入账户
INFO  service  应用启动 | 2/4 | 校验 Camoufox | 账户=28
INFO  service  应用启动 | 3/4 | 装配协议运行时
INFO  service  协议运行时就绪 | 账户=28 | 耗时=31ms
INFO  service  应用启动 | 4/4 | 监听 HTTP | 地址=127.0.0.1:2048
INFO  service  管理服务就绪 | 地址=http://127.0.0.1:2048
INFO  service  管理页面已打开 | 地址=http://127.0.0.1:2048
```

点击“启动服务”后状态进入 `LAUNCHING`，依次同步账户模型目录并预热 WAA Worker。模型目录最多同时读取五个账户；首个 Worker 就绪后状态进入 `RUNNING` 并开始接收请求，其余目标 Worker 继续预热。`LAUNCHING` 期间点击“停止服务”会取消当前阶段并回到 `STOPPED`。

```text
INFO  service  生成服务启动 | 1/2 | 同步模型目录
INFO  service  模型目录同步完成 | 模型=36 | 耗时=8.915s
INFO  service  生成服务启动 | 2/2 | 预热 WAA Worker | 模型=36 | 目标=5
INFO  service  生成服务就绪 | 模型=36 | Worker=1/5 | 耗时=18.204s
INFO  service  WAA Worker 预热完成 | Worker=5/5 | 耗时=34.903s
```

取消启动时，日志记录停止发生的阶段：

```text
INFO  service  模型目录同步已取消 | 耗时=2.104s
INFO  service  生成服务启动已取消 | 耗时=2.132s
```

每个账户从自己的实时目录选取 WAA 启动模型，先尝试 `gemini-flash-latest`，再尝试 `gemini-3.7-flash`。`gemini-flash-latest` 是 AI Studio 维护的独立别名，日志保留实际使用的候选名称。

WAA Bootstrap 在页面生成 proof 能力和动态请求头后，通过 WebDriver BiDi 终止浏览器的 bootstrap `GenerateContent`，模型输出量为零。首个候选完成后结束候选循环。

```text
INFO  account@example.com  WAA Worker 启动 | 1/7 | 选择启动模型
INFO  account@example.com  WAA Worker 启动模型 | 1/2 | 模型=gemini-flash-latest
INFO  account@example.com  WAA Worker 启动 | 2/7 | 准备浏览器配置
INFO  account@example.com  WAA Worker 启动 | 3/7 | 启动 Camoufox
INFO  account@example.com  WAA Worker 启动 | 4/7 | 连接 WebDriver BiDi
INFO  account@example.com  WAA Worker 启动 | 5/7 | 载入 AI Studio
INFO  account@example.com  WAA Worker 启动 | 6/7 | 定位 WAA 服务
INFO  account@example.com  WAA Worker 启动 | 7/7 | 执行 WAA Bootstrap
INFO  account@example.com  WAA Worker 就绪 | 模型=gemini-flash-latest | PID=18240 | 耗时=10.842s
```

启动失败时，最后一个阶段号定位浏览器链路，`WAA Worker 启动失败` 汇总各候选模型的错误。`Worker=1/5` 表示当前已有一个 Worker 可接单、预热目标为五个。

## API 请求

生成请求完成解析后立即记录模型和生成参数。账户选定后，完成摘要的来源切换为 Google 邮箱。

```text
INFO  request              请求开始 | POST "/v1/chat/completions" | gemini-3.7-flash | 温度=1 | TopP=1 | 思考=high | 最大=64000
INFO  account@example.com  200 | 34.898s | POST "/v1/chat/completions" | gemini-3.7-flash | 首事件=18.236s | 首正文=18.236s | 4414字/正文2092t | 终止=prohibited_content
```

| 字段 | 含义 |
| --- | --- |
| `温度`、`TopP` | 请求提交的采样参数；`默认` 表示使用实时模型目录的默认值 |
| `思考` | thinking level；预算模式显示为 `预算8192`，未指定时显示 `默认` |
| `最大` | 请求提交的最大输出 Token；未指定时显示 `默认` |
| `34.898s` | 请求进入服务到流结束的总耗时 |
| `首事件` | 第一个上游语义事件到达时间，事件可以是推理或正文 |
| `首正文` | 第一段正文到达时间；只有推理事件时显示 `-` |
| `4414字/正文2092t` | 正文 Unicode 字符数与上游正文 Token |
| `思考61448t` | 上游 usage 返回的思考 Token，值大于零时追加 |
| `终止` | AI Studio 返回的 finish reason |

`prohibited_content` 等策略终止属于已完成的上游终态，摘要保留 HTTP `200` 与原始 finish reason。未知整数终止原因显示为 `provider_<code>`。

模型列表等普通接口沿用相同的开始与完成骨架。

```text
INFO  request  请求开始 | GET "/v1/models"
INFO  service  200 | 1ms | GET "/v1/models"
```

客户端取消统一记录为 `499`。上游错误使用对应失败状态并在下一行保留错误详情。

```text
WARN  account@example.com  499 | 52.104s | POST "/v1/chat/completions" | gemini-3.7-flash | 首事件=18.236s | 首正文=- | 0字/正文0t/思考1260t | client_canceled
```

```text
ERROR account@example.com  502 | 4m56.668s | POST "/v1/chat/completions" | gemini-3.6-flash | 首事件=18.104s | 首正文=- | 0字/正文0t/思考1260t
                           错误: AI Studio stream closed before finish
```

## 流式等待

连续 15 秒没有阶段进展时会写入诊断日志。请求在收到上游终态、客户端取消或达到 `REQUEST_TIMEOUT` 时结束。

| 事件 | 当前停留位置 | 重点字段 |
| --- | --- | --- |
| `请求准备等待` | 账户已选定，正在准备 WAA proof 或等待 GenerateContent 响应头 | `当前`、`模型` |
| `请求准备结束` | 准备阶段超过 15 秒后开始接收响应体 | `等待`、`WAA`、`响应头`、`模型` |
| `上游首事件等待` | 上游响应体已经建立，尚未解出语义事件 | `网络字节`、`最近网络` |
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

SSE 客户端把该帧作为连接存活信号，正文、推理和 usage 继续使用各协议的 `data` 或命名事件。OpenAI Chat、Responses 与 Anthropic 还会在调度期间立即发送各自的起始事件。

## 账户事件

HTTP `401` 会使用该 Chrome 导入账户保存的 OAuth/DBSC 材料续签 Cookie、重置 WAA Worker并重放一次请求。

```text
INFO  account@example.com  账户认证续签 | 1/2 | 刷新 Cookie
INFO  account@example.com  账户认证续签 | 2/2 | 重置协议运行时
INFO  account@example.com  账户认证续签完成 | 耗时=1.116s
```

HTTP `403` 与协议 Code 7 表示上游明确拒绝当前账户模型组合。调度器保存该组合的拒绝结果并切换到下一个合格账户，后续请求直接跳过该组合。账户的其他模型继续使用；重新登录、验证账户或权益等级变化后重新建立模型资格。WAA runtime 失效时重建当前账户 Worker 并重放一次请求；连续失败后切换账户。其他可重试的上游错误使用模型级临时冷却。

```text
WARN  account@example.com  账号切换 | 模型=gemini-3.7-flash
                           原因: AI Studio GenerateContent 返回 HTTP 403、协议错误码 7: The caller does not have permission
```

账户页的 `Free`、`Pro`、`Ultra` 与 `Plus` 来自 `GetAiStudioBenefitTier`。Paid 模型先按权益和模型访问方式筛选，成功调用过目标模型的账户排在同模型未知账户之前。

管理页面连接 `GET /api/events` 时先收到当前状态、模型、账户、最近约 2000 条日志、冷却和活动请求，随后接收增量事件。控制台按 Go `slog` 格式输出运行事件。
