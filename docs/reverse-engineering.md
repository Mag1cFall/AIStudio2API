# AI Studio 私有协议开发指南

AI Studio 网页将认证、JSON+protobuf RPC、WAA 内容证明和 WebChannel 实时协议组合成完整调用链。本文说明官方页面采集、单变量字段定位、wire（上游原始请求与响应格式）重建、统一事件解码和公开 API 适配。协议规格与字段表见 [协议规范](protocol.md)。

## 1. 协议分层与复现方法

实现按协议职责分层。上游变化由对应层吸收，其他层继续使用原有请求、响应与状态更新方式。

| 层 | 输入与状态 | 实现位置 | 输出格式 |
| --- | --- | --- | --- |
| 认证 | Cookie、SAPISID、DBSC、Authorization、动态请求头 | `internal/chromeauth`、`internal/aistudio/auth.go`、`internal/aistudio/transport_http.go` | 可发送的账户状态与请求头 |
| 权益与目录 | BenefitTier、ListModels、AccessModes、方法与能力码 | `internal/aistudio/benefit.go`、`internal/aistudio/models.go` | 每账户完整模型目录、权益与能力字段 |
| WAA | 官网高层 snapshot service、binding prompt digest、proof | `internal/camoufoxnative`、`internal/aistudio/runtime_native.go` | 与 binding prompt 的 SHA-256 绑定的 fresh proof |
| 请求编码 | 稀疏 JSON+protobuf 数组、媒体与工具字段 | `internal/aistudio/generate.go`、`internal/aistudio/tools.go` | MakerSuite 请求正文 |
| 传输 | HTTP、SSE、WebChannel、Cookie 写回 | `internal/aistudio/transport_http.go`、`internal/aistudio/webchannel.go` | 原始增量帧 |
| 统一事件 | text、reasoning、tool、media、usage、finish、error | `internal/aistudio/event.go`、`internal/aistudio/bidi.go` | 与客户端协议无关的事件流 |
| 公开适配 | OpenAI、Responses、Anthropic、Gemini | `internal/api` | 客户端响应与终止语义 |

字段复现使用以下步骤：

1. 在官方页面触发一个最小动作，保存完整请求、响应、响应头和事件顺序
2. 使用同一账户、模型和输入重复动作，区分稳定字段与单次随机字段
3. 只修改一个输入量，比较数组槽位、动态头、Cookie 和完成帧
4. 用独立程序重建请求，确认服务端接受自行编码的正文
5. 将上游帧解码为统一事件，再由公开适配器转换
6. 通过公开端点验证成功、错误、取消和终止路径

对照时保存原始字节和解析结果。压缩、转码或重新序列化会隐藏空槽、尾字段、省略值和原始网络分块位置。

最小采集记录使用结构化对象保存动作、账户权益、模型、原始传输和单变量差异：

```json
{
  "scenario": "generation-temperature",
  "action": "click_run",
  "account_tier": "Free",
  "model": "gemini-3.7-flash",
  "request": {
    "method": "POST",
    "url": "https://<upstream-rpc>",
    "ordered_headers": [["content-type", "application/json+protobuf"], ["authorization", "<raw-value>"]],
    "raw_body_base64": "<base64>"
  },
  "response": {
    "status": 200,
    "ordered_headers": [["content-type", "application/json+protobuf"]],
    "chunks": [{"t_ms": 0, "raw_base64": "<base64>"}]
  },
  "expected": {"temperature": 0.2},
  "observed": {"json_path": "$[3][4]", "value": 0.2},
  "control_variant_diff": [{"json_path": "$[3][4]", "control": 1.0, "variant": 0.2}]
}
```

`ordered_headers` 保持浏览器发送顺序和重复头，`raw_body_base64` 与 chunk 的 `raw_base64` 保存原始字节。`expected` 描述页面动作，`observed` 描述 wire 结果，`control_variant_diff` 只列控制组和变量组之间已经分类的差异。

## 2. 稀疏 JSON+Protobuf

MakerSuite 将 protobuf message 表示为 JSON 数组。protobuf field `N` 对应 JSON index `N-1`。中间字段使用 `null` 占位，末尾默认字段可以直接省略。

以下三个值具有不同语义：

```json
[]
[null]
[0]
```

`GetAiStudioBenefitTier` 对这三种形状都解释为 Free。其他枚举或 boolean 分别采集 absent、`null`、零值与非零值，再决定默认语义。

### 字段读取

解析器先保留根原文，再按 field number 读取。缺失尾字段返回 absent，显式 `null` 返回 null，类型不匹配返回带方法和 JSON 路径的协议错误。

以下伪代码展示 field number 到数组 index 的读取方式。真实实现使用 `internal/aistudio` 的 `rawAt` 与字段类型 helper 完成同一语义。

```go
// WireField 按 protobuf field number 读取稀疏数组
func WireField(values []json.RawMessage, field int) (json.RawMessage, bool) {
	index := field - 1
	if index < 0 || index >= len(values) {
		return nil, false
	}
	return values[index], true
}
```

解析步骤保持固定：

1. 校验当前节点是数组、对象、字符串、整数或 boolean
2. 在已识别 field 上区分 absent、`null` 和默认值
3. 接受根消息与子消息的后续未知字段
4. 保留未知非空事件为 provider 事件或原始扩展字段
5. 对已经消费的字段执行严格类型与 oneof（同组字段中最多选择一种值）校验

### 单变量字段定位

完整 GenerateContent、Content、Part 与 GenerationConfig 字段表见 [协议规范](protocol.md)。定位新增字段时使用一个 baseline 和一个单变量 variant：

```json
{
  "baseline": [
    "models/gemini-3.7-flash",
    [[[[null, "Reply OK"]], "user"]],
    null,
    [null, null, null, 512, 1.0, 0.95, 64],
    "!FRESH_PROOF"
  ],
  "variant": [
    "models/gemini-3.7-flash",
    [[[[null, "Reply OK"]], "user"]],
    null,
    [null, null, null, 512, 0.2, 0.95, 64],
    "!FRESH_PROOF"
  ]
}
```

变量请求只修改页面 temperature。预期稳定差异位于根 field 4 的 GenerationConfig field 5，即 JSON path `$[3][4]`。fresh proof、visit ID、时间、Cookie 与随机请求标识属于已知动态字段，先从差异集中分类，再判断是否出现第二个业务字段变化。

单变量字段的判定顺序：

1. 请求方法、URL、模型、账户权益和页面状态一致
2. 输入正文与所有未操作的 UI 参数一致
3. 数组长度变化先还原为 field number，再比较 absent、`null` 和默认值
4. 动态认证与 WAA 字段单独归类
5. 变量字段能够在自行构造的请求中独立改变服务端行为

### 增量响应

`GenerateContent` 返回一个持续增长的 JSON 根值，`$[0]` 是 repeated frames。网络 chunk 是字节分块，协议 frame 才是解码单位。

| 路径 | 内容 |
| --- | --- |
| `$[0][frame][0]` | candidates |
| `$[0][frame][0][0][0]` | candidate content |
| `$[0][frame][0][0][1]` | finish reason |
| `$[0][frame][0][0][6]` | citations |
| `$[0][frame][0][0][7]` | grounding metadata |
| `$[0][frame][2]` | usage |
| `$[0][frame][7]` | response ID |

完成帧可能只有 `[null,"model"]`、usage 和 finish reason。解码器在一个 repeated frame 完整时立即输出事件，并在根数组结束时确认已经收到完成语义。

已知 finish reason 转换为统一终止原因。未知整数保留为 `provider_<code>`，公开适配器同时返回合法的标准终止字段和原始 provider 原因。这样可以让严格客户端结束读取，也能保留上游新枚举。

## 3. WAA 运行时

受保护请求的完整链路如下。Waa/Create、interpreter、dynamic program、Host/Realm 与 persistent state 由官网页面运行，本项目从页面的高层 snapshot service 接入：

```text
Waa/Create
  -> decode challenge
  -> load interpreter by hash
  -> execute dynamic program
  -> install browser host and iframe realm
  -> initialize persistent snapshot state
  -> SHA-256(binding prompt)
  -> snapshot({TYb:{content:digest}})
  -> fresh proof
  -> write proof field
  -> send protected RPC
```

### Challenge

`Waa/Create` 请求为 `["lmnUSbltwc5ULv48iKLX",interpreterHash|null,previousEmptySnapshot|null]`。cold 请求固定保留三个槽并在后两槽使用 null。interpreter 下载后以 SHA-256 + Base64URL 无 padding 计算摘要，摘要必须等于 challenge hash。previous empty snapshot 属于下一次 Create 的持久输入。一份 fresh challenge 复核样例包含 33,695 个字符的 program 与 65,831 字节的 interpreter。

`Waa/Create` 响应的第二槽是 Base64 字符串。解码后每个字节加 `97`，结果是 challenge 数组。解码结果包含：

| JSON 索引 | 字段 |
| ---: | --- |
| 0 | `MessageID` |
| 1 | `InterpreterJavaScript` 列表 |
| 2 | interpreter URL path 列表 |
| 3 | `InterpreterHash` |
| 4 | `Program` |
| 5 | `GlobalName` |
| 6 | 未识别槽 |
| 7 | `ClientExperimentsStateBlob` |

```go
// Challenge 保存一次 Waa/Create 生命周期
type Challenge struct {
	MessageID                  string
	InterpreterJavaScript      string
	InterpreterURL             string
	InterpreterHash            string
	Program                    string
	GlobalName                 string
	ClientExperimentsStateBlob string
}
```

interpreter 可以按 hash 缓存。program、message ID、实验状态和 snapshot 状态属于当前 challenge 生命周期。每次从 challenge 读取全局函数名称。

初始化调用的参数顺序：

```javascript
initialize(
  program,
  readyCallback,
  true,
  environment,
  passEvent,
  signalLists,
  persistentState,
  false,
  loggers
)
```

program、environment、passEvent、signal lists、persistent state、callback Realm 和两个 boolean 都参与状态推进。loggers 是四个回调。生命周期时长为 43,200,000ms，检查间隔为 300,000ms。

`Waa/Ping` wire 为 `[request_key,botguard_response]`，成功响应为 `[]`。以下输入都能得到成功空响应：正确 proof、损坏 proof、省略 proof、任意 request key、无账户 Cookie。Ping 只验证 WAA RPC consumer identity 与字段类型，业务 proof 资格使用实际 GenerateContent、GenerateVideo 或 Bidi 请求判定。

### Host 与 Realm

dynamic program 会读取以下浏览器宿主语义，Camoufox runtime 负责提供：

- navigator、screen、window、document、location、performance 与 timezone
- TextEncoder、TextDecoder、Base64、TypedArray、ArrayBuffer、Blob 与 URL
- Promise、microtask、timer、事件循环顺序与高精度时间
- iframe 的独立 global、prototype、constructor、eval 与 Trusted Types 语义
- DOM 属性描述符、原生对象字符串化、getter 副作用与访问顺序
- persistent state 的输入、写回和下一次 snapshot 读取

发送入口分为三条链：

| 链路 | 必需内容 | 可验证内容 |
| --- | --- | --- |
| 完整 Angular | 真实键盘输入、框架事件、Run action、官方 snapshot 与官网请求 | 页面原生链的成功或失败 |
| 页面上下文 | 同一 Worker、同一请求正文，只替换发送入口 | 浏览器上下文 transport 对照 |
| Go HTTP | 同一 Worker、同一 headers/body，只替换发送 transport | Go transport 与页面发送等价性 |

包含真实输入、框架事件、Run action、官方 snapshot 与官网请求的完整 Angular 路径代表页面原生调用。账户资格、模型资格、proof 有效性和 runtime（账户的 WAA 浏览器运行实例）是否就绪由该路径发起的受保护业务 RPC 判定；页面上下文与 Go HTTP 链分别验证对应 transport。完整 Angular 返回 Code 7 表示该次调用被拒绝。

发送链行为：

| 条件 | 行为 |
| --- | --- |
| 同一账户、同 Worker、fresh proof 连续调用 | 可以先 Code 7，后续 HTTP 200 |
| 相同模型的多个 fresh Worker | 首次结果可以分别为 200 或 Code 7 |
| 同一 Worker、不同业务模型 | 一个模型 200，另一个模型 Code 7 |
| 完整 Angular、页面上下文、Go HTTP 使用同一成功 Worker | 三条链均可返回 200 |
| 完整 Angular 起点已经 Code 7 | 页面上下文与 Go HTTP 可得到相同 Code 7 |
| 正确、损坏和空 Ping proof | 均返回 HTTP 200 `[]` |

同一正文的受保护业务 RPC 接受结果直接判定发送链的业务可用性。proof 长度、program 长度、等待时长、Ping、单次 `sD()`、模型名称和本地 snapshot 结果作为诊断字段记录。

### Snapshot 接口

WAA 通过 `ProtectedPreparer` 向请求编码器提供 proof。以下签名与 `internal/aistudio/service.go`、`internal/aistudio/waa.go` 一致：

```go
// ProtectedPreparer 为一次请求写入 fresh WAA proof
type ProtectedPreparer interface {
	Prepare(context.Context, ProtectedRequest) (PreparedProtectedRequest, error)
}

// ProtectedRequest 表示需要 WAA 保护的请求
type ProtectedRequest struct {
	URL        string
	Headers    http.Header
	Body       []byte
	Prompt     string
	ProofField int
}
```

GenerateContent 的 proof field 为 5，Veo GenerateVideo 为 8，Bidi setup 与实时输入为 6。请求编码器先形成无 proof 正文和 binding prompt，WAA Worker 串行调用 snapshot，再原位写入 proof。

GenerateContent 的 binding prompt 按 contents 和 parts 原顺序展开，以单个空格连接：文本使用原文、inline data 使用标准 Base64、Drive file 使用 file ID；external media、function、result、code 和 thought signature 贡献空字符串。Veo 使用视频提示词。摘要是 binding prompt 的 SHA-256 小写十六进制。

官方 snapshot 的底层输入保持四个槽：

```javascript
[{content: digest}, undefined, undefined, undefined]
```

外层对象键名、数组长度、`undefined` 与缺失槽的差异都会进入 VM 行为对照。

### 生命周期

```text
starting -> bootstrapping -> ready -> busy -> ready
     |             |          |        |
     +-----------> failed <----+--------+
ready -> closing -> closed
```

同一账户的 snapshot 串行执行。页面生命周期中断、snapshot 错误、VM 到期、认证续签或进程关闭使当前 runtime 进入 failed/closed。调度层为每个 Worker 实例维护 generation（递增版本号），需要重建时创建新 generation；较晚返回的旧实例错误按旧 generation 处理，新实例状态保持不变。

Camoufox bootstrap 通过官方页面取得 snapshot service 和动态请求头。页面 GenerateContent 在 `network.beforeRequestSent` 阶段由 `network.failRequest` 结束，因此 bootstrap 没有上游模型输出。同一账户的普通业务模型共享该 snapshot service。交互式登录、Cookie 导出和官方运行时位于 `internal/camoufoxnative`。

## 4. WebChannel、Live 与 Robotics

Live 和 Robotics 使用 `BidiGenerateContent` WebChannel。它由握手、前向 POST、长轮询 backchannel 和 terminate 四类请求组成。

会话状态机为：

```text
new -> handshaking -> ready -> reconnecting -> ready
                       |                        |
                       +------> closing <-------+
                                  |
                                closed
```

握手同时取得 SID 与 gsessionid。setup complete 推进到 ready。首次 backchannel 成功后的网络读取错误进入 reconnecting；重建 backchannel 后回到 ready。显式关闭先发送 terminate；协议终态和不可恢复错误结束网络读取；两条路径均在账户租约释放后进入 closed。

### 握手与会话标识

握手使用 `VER=8`、随机 `RID`、`CVER=22`、`X-HTTP-Session-Id=gsessionid`、动态认证头和 `count=0`。响应头提供 `gsessionid`，第一条控制帧提供 SID：

```json
[[0, ["c", "<SID>", "", 8]]]
```

后续前向请求携带：

```text
query: VER, gsessionid, SID, RID, AID, zx, t
form:  count=1, ofs=<OFFSET>, req0___data__=<JSON_PROTOBUF>
```

`RID` 标识前向请求，成功发送后递增。`ofs` 标识客户端消息 offset，成功 ACK 后递增。`AID` 是已消费的服务端 envelope ID，由 backchannel 中的 envelope 首槽推进。

WebChannel ACK 是三个整数的数组。三个字段只按已观察形状校验；第二个整数不等于本地请求序号。将 ACK 字段强行绑定 RID、AID 或 ofs 会在合法响应上制造协议错误。

RID、ofs 与 AID 在各自成功点提交：

```text
send(payload):
  lock sendMu
  snapshot rid, aid, ofs
  POST query(RID=rid, AID=aid) form(ofs=ofs, payload)
  require HTTP 200 and ACK=[int,int,int]
  commit rid=rid+1, ofs=ofs+1

consume_backchannel(envelope):
  require envelope=[serverAid,payload]
  decode payload events
  commit aid=max(aid,serverAid)
  emit payload events in order

reconnect_backchannel():
  keep SID, gsessionid and committed aid
  open RID=rpc with AID=aid
```

HTTP 失败、ACK 解析失败或取消发生在 commit 前时，RID 与 ofs 保持原值。AID 只由成功解码的服务端 envelope 推进。

### Backchannel

backchannel 使用 `RID=rpc` 和当前 `AID` 长轮询。每个网络 frame 使用十进制长度、换行和对应字节数的 JSON：

```text
<decimal-length><LF>
<json-bytes>
```

JSON frame 包含若干 `[AID, payload]` envelope。payload 可能是业务数组，也可能是对象状态帧：

```json
{"__sm__":{"status":[[[7,"The caller does not have permission"]]]}}
```

业务 payload 解码为 setup、text、media、input/output transcription、generation complete、turn complete、interrupted、session resumption、usage、go away、provider、closed 或 error 事件。未知非空业务槽保存为 provider 事件，保持事件顺序。

首次 backchannel 建立成功后，后续网络读取错误重新建立 backchannel，并继续使用当前 SID、gsessionid 与 AID。首次建立失败、协议完成、显式 close 或不可恢复错误结束逻辑会话。

### Setup 与输入

setup 位于外层 field 7，包含模型、生成配置、恢复 token、缓冲参数和时区。Live 使用 AUDIO 输出与 Minimal thinking，Robotics 使用 TEXT 输出与 High thinking。公开会话在收到 `setup_complete` 后进入 ready。

文本输入位于 realtime input 的 text 槽。音频使用 `audio/pcm`，图像使用 `image/jpeg`，二进制采用标准 Base64。媒体结束使用独立结束帧。

每次上游发布新的 resumption token 时，用新 token 原子替换同一逻辑会话的上一枚 token。恢复 token 通过资源绑定选择原账户，新 setup 携带本次请求的模型和模式。

Live 纯文本使用模型 scope（模型资格与冷却的状态记录范围）；Live 音频/图像与 Robotics 使用 `bidi-media:<modelID>`。会更新模型资格的轮次收到 Code 7 时结束当前会话并保留上游错误。

## 5. 权益、目录与排序

账户候选集合由实时目录与账户权益决定，成功调用历史用于排序：

```text
BenefitTier
  + ListModels.methods
  + ListModels.accessModes
  + model capability fields
  = account candidate set

successful model/scope history
  = candidate priority
```

`GetAiStudioBenefitTier` field 1 与 tier 请求头的完整映射见 [协议规范](protocol.md)。运行时将省略、`null` 和 `0` 解析为 Free，并分别映射 Pro、Ultra 与 Plus。ListModels field 83 描述访问方式：`1` 是付费 API key，`3` 是 Pro/Ultra 订阅，`4` 是 Ultra 订阅。Paid 标记描述模型属性，BenefitTier 描述当前账户，两者分别解析。

目录、权益与成功历史的组合如下：

| 目录显示 | 权益允许 | 成功历史 | 调度结果 |
| --- | --- | --- | --- |
| 否 | 任意 | 任意 | 排除 |
| 是 | 否 | 任意 | 排除 |
| 是 | 是 | 无 | 正常候选 |
| 是 | 是 | 有 | 优先候选 |
| 目录或权益变化 | 重新计算 | 对应旧记录清除 | 使用新目录 |

成功记录 key 使用 canonical model（去除 `models/` 前缀后的模型 ID）与 scope 组合。`verified` 表示该账户已经成功调用对应模型或能力：

| 操作 | scope | verified 写入点 |
| --- | --- | --- |
| 普通生成 | `<modelID>` | 统一事件 `EventFinish` |
| CountTokens | `count-tokens:<modelID>` | 不写 verified，只清该 scope 冷却 |
| Transcribe | `<modelID>` | 非空 text 或 segments |
| Veo GenerateVideo | `<modelID>` | operation 创建成功 |
| Live 文本 | `<modelID>` | setup complete；每次 text 开始一次模型资格检查 |
| Live 音频/图像 | `bidi-media:<modelID>` | media 开始媒体资格检查；Live text 使用普通模型 scope |
| Robotics | `bidi-media:<modelID>` | text 开始模型资格检查 |

`model_access` 保存 `state` 与 `checked_at`，`verified` 是当前唯一的非空 state。Code 7 保留已有状态，CountTokens 使用独立 scope 更新 `checked_at`。认证失败属于账户级状态。

普通流式请求的 text、reasoning、tool、usage 与首事件用于输出和性能统计；模型 verified 在 `EventFinish` 写入，终态前断流、取消和错误保留已有模型资格。

调度顺序为：

1. 按方法、模型、capability、AccessModes 和 BenefitTier 形成候选集合
2. 先尝试已有 Worker 且仍有请求槽位的账户，组内依次按 verified、同模型首事件样本、首事件 EWMA、空闲槽位和活动数排序
3. 已有 Worker 无可用槽位时，按活动 Worker 上限从待机账户启动 Worker；容量已满时先启动目标 Worker，再替换最久未用的空闲 Worker
4. 目标 Worker 启动失败或请求取消时保留原 Worker；旧实例关闭失败时保留旧实例并回收目标 Worker，两者回收均失败时同时计入容量；候选均忙时等待槽位

HTTP 403 且协议 Code 7 保留对应模型或 scope 的成功记录；未固定且首个上游语义事件尚未到达的请求继续尝试下一账户，固定请求、资源绑定请求和已开始的流返回上游错误。HTTP 404 且协议 Code 5 的消息包含 `Ambiguous request for service ''`、本地 Worker 失败或实例被替换时，运行时按 Worker generation 重建同一账户实例并重放一次。

认证写回使用 `authGeneration` 与 `checkedAt`；模型资格和冷却使用 `modelAccessGeneration` 与 `checked_at`。结果仅在对应 generation 值匹配且时间不早于当前状态时应用，同一时间以成功状态为最终值。目录、权益或认证材料更新时递增对应 generation。

Bidi setup 成功使用 lease（本次会话持有的账户租约）的 `checked_at`；会话内资格请求使用严格递增的 `checked_at`。`turn_complete` 消费 pending 轮次并写入资格，Code 7 保留已有资格。认证成功与 401 复用该时序。

`runtime-state.json` 保存 `benefit_tier`、`catalog_fingerprint`、`model_access:{state,checked_at,reason?}`、`cooldowns:{until,reason?}` 与 `resources:{kind?,name?,mime?,size?,purpose?,created_at,video?:{model,seconds,size}}`。`video-operation` 绑定把公开 video object 的 model、seconds、size、UTC 创建时间和创建账户一并持久化；重新建立账户池后，operation 轮询仍从该绑定恢复元数据。活动请求使用账户租约；读取最新磁盘状态、合并目标字段和原子写回使用独立短事务锁。该锁每 25ms 尝试一次并在 2 秒后超时；调用方 context（Go 取消上下文）可以更早取消等待。取得锁后必须重读磁盘值，写回后再更新内存与资源索引。跨进程读取资源未命中时刷新磁盘状态，防止旧内存覆盖另一个进程已经写入的绑定。

本段中的 generation 表示一次 Stop/Start 创建的生成服务实例。模型目录驻留在当前 generation 的内存中，新 generation 以空缓存启动。`Start` 先用当前 generation 已有的真实 `CachedModels` 生成公开目录，并为全部 `enabled` 且处于 `ready` 或 `busy` 的账户各启动一个后台 `ListModels` 任务。缓存非空时立即开始 Worker 预热；缓存为空时等待新 generation 的首个非空结果，再开始 Worker 预热。每个非空结果都会立即重新合并 `CachedModels`、发布账户与模型事件，并在生成服务已经运行时继续预热其他 Worker。全部账户同步结束时，认证集合变化会补发一次账户与模型快照；日志记录同步成功数、非空数、公共模型数、待重试账户数与耗时。

目录错误或返回空目录的账户 ID 保留在 pending set（待重试账户集合），返回非空目录或删除账户时移除。初次 fan-out（并发向全部目标账户调用 `ListModels`）结束后，单个 30 秒 ticker（Go 定时器）对排序后的待重试账户列表再次执行 fan-out；每个非空结果立即同步公共目录、发布管理事件并继续预热其他 Worker，批末认证集合变化触发一次快照补发。停止流程对目录任务最多等待 2 秒；管理调用对整次启动或停止切换最多等待 12 秒，超时后返回，后台清理继续并保留最终错误。

## 6. 上游变化与恢复

上游变化分为运行时值变化与协议语义变化：

| 变化 | 吸收方式 | 是否改码 |
| --- | --- | --- |
| 模型增删、别名、token 上限、默认采样值 | 刷新 ListModels 并重建目录 | 否 |
| BenefitTier 当前值、AccessModes 当前组合 | 重新计算账户候选 | 否 |
| challenge message ID、program、interpreter hash | 官网 WAA runtime 在页面 bootstrap 时重新取得 | 否 |
| Cookie、visit ID、Set-Cookie 与动态请求头值 | 账户状态和响应头写回 | 否 |
| 已知数组 field 的值或省略尾字段 | 现有字段解析与默认值语义 | 否 |
| 数组嵌套层级、field 类型、oneof 或已消费槽位变化 | 更新对应编码器或解码器 | 是 |
| 新 finish enum 的业务语义 | 更新统一终止原因和公开响应 | 是 |
| 高层 snapshot 定位或页面交互 | 更新 Camoufox 运行时定位与交互 | 是 |
| WebChannel control、ACK、envelope 或 payload shape | 更新 WebChannel 与 Bidi parser | 是 |

| 失效信号 | 变化层 | 最短恢复动作 |
| --- | --- | --- |
| 上游 401 | Cookie、SAPISID 或 DBSC | 对照登录态、续签响应、Authorization 与 Set-Cookie |
| 页面成功，协议请求 403 | 动态头、TLS、请求正文或 WAA binding | 对照官方请求头顺序、proof field 与无 proof 正文 |
| 输入框、Run 或 snapshot 定位失败 | AI Studio 页面与 bundle | 重新定位稳定组件和高层 snapshot 调用 |
| 带 JSON 路径的字段类型错误 | protobuf 数组或新 oneof | 保存原始帧，更新单一字段解析器 |
| `provider_<code>` | finish enum | 确认官方页面表现并更新四套公开响应 |
| ACK、setup 或 backchannel 失败 | WebChannel | 对照 RID、AID、ofs、frame 长度和 payload 形状 |
| 模型消失或大量 Code 7 | BenefitTier、ListModels、AccessModes 或 WAA 状态 | 刷新目录并比较可用账户的 WAA 链路 |

恢复后依次验证官方页面请求、自行构造的上游请求和公开 API 结果。公开端点保持上游错误、客户端取消与终态顺序；协议变化只修改产生差异的实现层，统一事件和其他公开适配继续使用原有字段与状态转换。
