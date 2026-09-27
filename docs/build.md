# Build 通道

AI Studio 的 Build 应用经官网宿主页调用 MakerSuite 代理 RPC 访问 Gemini API，这部分调用使用与 Playground 分开计算的额度。服务把 Build 作为与 Playground 对等的生成通道：同一账户同时拥有两份额度，生成请求按账户与通道的组合调度。本文定义代理 RPC 的 wire、请求头与 WAA、模型目录与资格、调度与冷却、Gemini API JSON 的逐字段映射、响应解码、错误与额度处理，以及通道的配置与显示。WAA proof 的生成见 [WAA 实现](waa.md)，Playground 的 `GenerateContent` 见 [协议规范](protocol.md)。

## 1. 官网机制

Build 应用运行在独立 origin 的沙箱 iframe 中。应用的 `fetch` 被 shim 替换，发往 Gemini API 的请求经 `MessageChannel` 交给宿主页。用户在应用中没有选择付费 API key 时，宿主页调用 MakerSuite 代理 RPC，并对生成类路径（`:generateContent`、`:streamGenerateContent` 等）生成 WAA proof。宿主页在转发前检查页面的用户激活状态，这项检查只在页面内等待，代理 RPC 的字段与请求头不携带激活状态；服务不打开 Build 页，由账户 Worker 直接发送代理 RPC。

| Gemini API 请求 | 代理 RPC |
| --- | --- |
| `:streamGenerateContent` | `ProxyStreamedCall` |
| 其他方法 | `ProxyUnaryCall` |
| Files 与缓存上传 | `ProxyUnaryFileApiCall` |
| Live WebSocket | 宿主页 WebChannel 转发 |

服务接入 `ProxyStreamedCall` 与 `ProxyUnaryCall` 上的生成和模型目录。

## 2. 配置与显示

`UPSTREAM_CHANNELS` 列出启用的生成通道：

| 值 | 通道 |
| --- | --- |
| `playground` | 官网 Playground 的 `GenerateContent` |
| `build` | Build 应用代理的 Gemini API 调用 |

- 默认值为 `playground,build`；逗号分隔，去除空白并转小写；至少一个，不能重复，其他值在配置校验时报错
- 列表顺序即同一账户内的通道顺序
- 配置来源为 `.env`、管理页面设置中的“上游通道”（至少保留一个，保存为 `playground,build` 顺序）或 `PUT /api/config` 的 `upstream_channels`；保存值在下一次启动生成服务时生效

通道在以下位置显示：

| 位置 | 字段 |
| --- | --- |
| OpenAI 格式 `GET /v1/models`、`GET /v1beta/models` | 模型对象的 `channels`，列出至少一个启用账户可调用该模型的通道 |
| `GET /api/requests` 与管理页面请求列表 | 请求当前尝试的 `channel` |
| 请求日志 `request.channel` 与管理页面日志 | 请求实际使用的通道 |
| `GET /api/cooldowns` 与管理页面冷却列表 | `channel` 与去掉 `build:` 前缀的 `model_id` |

## 3. 代理 RPC

两个 RPC 位于 MakerSuite 服务：

```text
https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/ProxyStreamedCall
https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/ProxyUnaryCall
```

### 请求

| protobuf field | 内容 |
| ---: | --- |
| 1 | Gemini API 路径 |
| 2 | 请求体 JSON 字符串；GET 为查询参数的 JSON 对象 |
| 3 | WAA proof；模型目录请求为 `null` |
| 4 | HTTP 方法，只用于 `ProxyUnaryCall` |

```json
["/v1beta/models/<MODEL_ID>:streamGenerateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>"]
["/v1beta/models/<MODEL_ID>:generateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>", "POST"]
```

模型的 AccessModes 非空且 Free 权益不能使用时（需要 Pro、Ultra 等订阅），生成经 `ProxyUnaryCall` 与 `:generateContent`；其余模型经 `ProxyStreamedCall` 与 `:streamGenerateContent`。

### 请求头

代理 RPC 的请求头集合、顺序与取值和 Playground `GenerateContent` 相同：`content-type`、`x-goog-api-key`、`x-goog-authuser`、`x-user-agent`、`x-aistudio-visit-id`、`x-goog-ext-519733851-bin`、`authorization` 与账户 Cookie。权益头 `X-AIStudio-G1-Tier` 只随 `ProxyUnaryCall` 发送（Pro 为 `TIER1`、Ultra 为 `TIER2`、Plus 为 `TIER0`，Free 不带），`ProxyStreamedCall` 不带该头。需订阅权益的模型经 `ProxyStreamedCall` 调用时上游返回 HTTP 403 与 Code 7，因此这类模型固定使用 `ProxyUnaryCall`。

### WAA

proof 位于 field 3，由账户的同一个 WAA Worker 生成，与 Playground 共用 VM。binding 为 field 1 与 field 2 以单个空格连接：

```text
/v1beta/models/<MODEL_ID>:streamGenerateContent {"contents":[...],"generationConfig":{...}}
```

摘要为 binding 的 SHA-256 小写十六进制。受保护请求由 Worker 发送，Camoufox 后端经页面原生 `fetch`，纯 Go 后端经账户固定出口的 Go HTTP。

### 响应

`ProxyUnaryCall` 成功响应为一个 ProxyResponse：

```json
["<GenerateContentResponse JSON>"]
```

`ProxyStreamedCall` 响应的 field 1 为 repeated ProxyResponse；流中出错时响应列表之后带 google.rpc 状态 `[code, message]`：

```json
[[["<GenerateContentResponse JSON>"], ["<GenerateContentResponse JSON>"]]]
[[["<GenerateContentResponse JSON>"]], [8, "<MESSAGE>"]]
```

ProxyResponse 的索引 `0` 是 JSON 字符串正文，索引 `2` 是 Base64 字节正文，两者取其一。每个正文是一段完整的 Gemini API `GenerateContentResponse`。流式解码器在响应列表中每出现一个完整元素时立即解码。

## 4. 模型目录与资格

### Build 目录

账户同步模型目录时，Playground `ListModels` 成功后，Build 通道启用的账户再经 `ProxyUnaryCall` 读取 Gemini API 模型列表：

```json
["/v1beta/models", "{\"pageSize\":\"200\"}", null, "GET"]
```

响应含 `nextPageToken` 时把它写入查询对象的 `pageToken` 继续读取，最多 10 页。每个模型条目映射为：

| Gemini API 字段 | 目录字段 |
| --- | --- |
| `name` | 去掉 `models/` 的 ID |
| `displayName`、`description` | 名称与描述 |
| `inputTokenLimit`、`outputTokenLimit` | token 上限 |
| `supportedGenerationMethods` | methods；含 `generateContent` 时设置聊天能力 |
| `thinking` | 思考能力 |

Build 目录保存在账户内存中，不写入 `runtime-state.json`。读取失败时该账户本轮目录同步记为失败，进入待重试集合，按 30 秒周期重试。

### 通道资格

账户的 Build 通道可以承担模型 M 的请求，需要同时满足：

1. 请求是普通生成：方法为 `generateContent`，没有 capability 限定、资源绑定、专用 scope 或 Playground 固定要求
2. `UPSTREAM_CHANNELS` 启用 `build`
3. 账户的 Build 目录包含 M 且 methods 含 `generateContent`
4. M 出现在任一账户的 Playground 目录时：M 不是 Interactions 或转录模型，且账户权益满足 M 的 AccessModes
5. M 只在 Build 目录中时：M 不是只能配合 Computer Use 工具的模型

以下请求只使用 Playground：CountTokens、Live 与 Robotics、Veo、转录、Interactions 模型、带 Drive 文件引用的生成、带多说话人 `mode` 的 TTS。embedding、`aqa` 与只支持实时方法的模型没有 `generateContent`，不进入 Build 通道。

### 公开目录

公开目录是已启用通道的并集。Build 独有且至少一个启用账户可经 Build 调用的可生成模型加入公开目录。模型出现在 Playground 目录时，Build 请求使用 Playground 目录的默认参数与能力；Build 独有模型的默认输出上限为 `outputTokenLimit`，思考能力取 `thinking`。每个模型的 `channels` 列出可调用它的通道。

## 5. 调度与冷却

候选单位为账户与通道的组合，组合按账户 ID 升序、同一账户内按 `UPSTREAM_CHANNELS` 顺序排列。调度仍先尝试已有热 Worker 的账户，再按需启动待机账户的 Worker，规则见 [开发与贡献](development.md)。

| 策略 | 组合推进 |
| --- | --- |
| `round-robin` | 每个模型记录上次选中的账户与通道，从其后的组合开始 |
| `fill-first` | 始终从首个可用组合开始 |

- 并发槽位、Worker 与 WAA 按账户共享，两个通道占用同一组请求槽位
- 冷却按通道记录：Playground 为 `<模型>`，Build 为 `build:<模型>`；全局 `*` 冷却对两个通道都生效
- 账户只有在其全部支持的通道都冷却时才视为该模型冷却
- 一个通道返回额度错误后写入该通道冷却；同一账户另一通道可用时，请求在同一账户的另一通道重试，不计为已尝试账户
- 全部候选组合冷却时，最早恢复时间在 1 分钟内的请求排队等待，更晚的请求返回 HTTP 429 `rate_limit_exceeded`，消息给出最早恢复时间
- 生成成功在该通道的 scope（`<模型>` 或 `build:<模型>`）上写入 `verified`

### 附件与文件引用

| 输入 | Build | Playground |
| --- | --- | --- |
| 内联附件 | 直接写入 `inlineData` | 上传到本次生成账户的 Drive，以 file ID 引用 |
| YouTube 链接 | `fileData` | external media Part |
| Drive 文件引用 | 不接受 | 使用文件所属账户 |

带 Drive 文件引用的生成固定走 Playground。文件所属账户没有可调度的 Playground 候选，或其冷却在 1 分钟后才恢复时，文件临时复制到其他账户，由该账户的 Playground 生成，请求结束后回收副本。

## 6. 请求映射

Build 请求与 Playground 共用同一预处理：工具可用性校验、模型媒体默认值、TTS 台词处理、参数校验与目录默认值。停止序列不发送，由本地匹配截断（见第 8 节）。请求体顶层字段：

| 字段 | 取值 |
| --- | --- |
| `contents` | 规范 contents |
| `systemInstruction` | system 非空时为 `{"parts":[{"text":"<SYSTEM>"}]}` |
| `tools` | 工具声明 |
| `toolConfig` | 同时声明函数与 Google 工具时为 `{"includeServerSideToolInvocations":true}` |
| `generationConfig` | 生成参数 |
| `safetySettings` | 骚扰、仇恨、色情、危险四类 `OFF`；图片路由不发送 |

```json
{
  "contents": [{"role": "user", "parts": [{"text": "Reply OK"}]}],
  "systemInstruction": {"parts": [{"text": "你是诊断助手"}]},
  "generationConfig": {
    "maxOutputTokens": 512,
    "temperature": 0.2,
    "thinkingConfig": {"includeThoughts": true, "thinkingLevel": "LOW"}
  },
  "safetySettings": [
    {"category": "HARM_CATEGORY_HARASSMENT", "threshold": "OFF"},
    {"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "OFF"},
    {"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "OFF"},
    {"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "OFF"}
  ]
}
```

### contents

user 与 tool 角色写为 `user`，assistant 写为 `model`，没有 part 的 content 省略。user 文本中的 YouTube 链接先转为 external media part。

| 规范 Part | Gemini API Part |
| --- | --- |
| text | `text`；带说话人或风格时附 `speechMetadata {speaker, style}`；思考文本带 `thought: true` |
| inline data | `inlineData {mimeType, data}`，data 为标准 Base64 |
| external media | `fileData {mimeType, fileUri}` |
| Drive file | 返回参数错误 |
| function call | `functionCall {name, args, id?}` |
| function result | `functionResponse {name, response, id?}` |
| executable code | `executableCode {language, code}` |
| code execution result | `codeExecutionResult {outcome, output?}` |
| 只有签名 | `text: ""` |

- thought signature 写入 Part 的 `thoughtSignature`；函数调用没有签名时写 `skip_thought_signature_validator`
- function result 缺少名称时，按 call ID 关联当前轮尚未返回结果的调用，未匹配且只剩一个调用时使用其名称
- 函数参数与结果是 JSON 对象时原样写入，其他 JSON 值封装为 `{"result":<VALUE>}`
- 代码执行结果的 outcome 不是 `OUTCOME_OK` 时 `output` 写错误文本

### tools

| 工具 | Gemini API |
| --- | --- |
| 函数声明 | `{"functionDeclarations":[{"name","description?","parametersJsonSchema?"}]}`，JSON Schema 原样发送 |
| Google Search、Image Search | `{"googleSearch":{...}}`；请求 Image Search 时带 `searchTypes {imageSearch, webSearch?}`；时间范围写 `timeRangeFilter {startTime, endTime}`（RFC 3339 UTC） |
| Code Execution | `{"codeExecution":{}}` |
| URL Context | `{"urlContext":{}}` |
| Google Maps | `{"googleMaps":{}}` |

工具选择为默认或 `auto` 时发送工具声明，`none` 时省略 `tools`，其他取值返回参数错误。

### generationConfig

| 字段 | 取值 |
| --- | --- |
| `maxOutputTokens` | 请求值或目录默认值，按模型上限校验；带语音配置且未显式设置时不发送 |
| `temperature`、`topP`、`topK`、`seed` | 请求值或目录默认值 |
| `responseMimeType` | 请求值 |
| `responseJsonSchema` | 请求的 JSON Schema 原样发送 |
| `responseModalities` | 大写模态名；图片模型补 `IMAGE`、`TEXT`，TTS 与音乐模型补 `AUDIO` |
| `imageConfig` | `{aspectRatio?, imageSize?}`；可设置分辨率的图片模型默认 `1K` |
| `speechConfig` | 单声音为 `voiceConfig.prebuiltVoiceConfig.voiceName`；多说话人为 `multiSpeakerVoiceConfig.speakerVoiceConfigs[{speaker, voiceConfig}]` |
| `thinkingConfig` | 模型支持思考时发送：`includeThoughts: true`、`thinkingBudget`、`thinkingLevel` |

thinking level 由 Playground 枚举换算：Low=`LOW`、Medium=`MEDIUM`、High=`HIGH`、Minimal=`MINIMAL`。`reasoning_effort`、预算与等级之间的换算规则与 Playground 相同。

TTS 模型带 `speech_metadata` 能力时，`说话人: 台词` 文本按多说话人配置拆成带 `speechMetadata` 的分段；其他 TTS 模型把分段的说话人与风格折叠回台词文本。Build 代理请求的 `multiSpeakerVoiceConfig` 只含 `speakerVoiceConfigs`，带 `mode` 的请求走 Playground。

Playground 私有字段在 Build 请求中不发送：账户时区、GenerateContent 与 generation config 的固定槽、可设置分辨率图片模型的默认工具槽。不等于 1 的 `candidateCount` 与 logprobs 由公开适配器拒绝。

## 7. 响应解码

每段 `GenerateContentResponse` 解码为规范事件：

| 字段 | 事件 |
| --- | --- |
| `text`（`thought` 为假） | text |
| `text`（`thought: true`） | reasoning |
| `inlineData`（`thought` 为假） | media，Base64 解码为字节 |
| `executableCode` | executable code |
| `codeExecutionResult` | code execution result；`OUTCOME_OK` 时 `output` 为输出，否则为错误 |
| `functionCall` | tool call，`args` 缺失时为 `{}` |
| 只有 `thoughtSignature` 的 Part | thought signature |
| `citationMetadata.citationSources` | citation（`uri`、`title`、`startIndex`、`endIndex`） |
| `groundingMetadata` | grounding |
| `usageMetadata` | usage |
| `finishReason` | usage 与 finish |

- Part 的 `thoughtSignature` 附在该 Part 产生的事件上
- 带 `thought: true` 的 `inlineData` 是图片模型思考过程中的草图，不作为输出媒体
- `groundingMetadata` 读取 `webSearchQueries`、`searchEntryPoint {renderedContent, sdkBlob}`、`groundingChunks` 的 `web`、`retrievedContext`、`maps`（`uri`、`title`、`text`、`placeId`）、`groundingSupports`（segment 的 `partIndex`、`startIndex`、`endIndex`、`text`，`groundingChunkIndices`，`confidenceScores`）与 `googleMapsWidgetContextToken`
- `usageMetadata` 的 `totalTokenCount` 大于 0 时记录：`promptTokenCount` 为输入，`candidatesTokenCount` 为输出，`thoughtsTokenCount` 为思考，`toolUsePromptTokenCount` 为工具；缺少 `candidatesTokenCount` 时输出为总数减去其余三项
- 第一次出现 `finishReason` 时先发出最近的 usage，再发出 finish；终止原因为去掉 `FINISH_REASON_` 前缀后的小写枚举名，与 Playground 的规范终止原因一致，`UNSPECIFIED` 为 `unspecified`
- 没有 candidate 且带 `promptFeedback.blockReason` 时返回 prompt feedback 错误，原因为去掉 `BLOCK_REASON_` 前缀的小写枚举名
- candidate 数量不是 1、正文不是 JSON 或 Base64 无效时返回带路径的协议错误；流结束时没有 `finishReason` 同样为协议错误

## 8. 停止序列、usage 与终态

Build 与 Playground 的事件流经过同一处理：

1. 停止序列不发送给上游；本地在 text 事件中匹配，命中时截断正文并关闭上游流，终态为 `stop_sequence`
2. 带停止序列的请求并行调用同账户的 Playground `CountTokens`，命中时用计数的输入总数加已输出内容构造 usage；计数失败时省略 usage
3. 上游没有返回 usage 时在本地统计输入、工具声明、思考与输出
4. usage 在 finish 之前发出

## 9. 错误与额度

HTTP 非 200 响应解码为协议错误，保留 HTTP 状态、google.rpc code、消息与 `ErrorInfo` 元数据。错误正文有两种形状：

```json
[7, "The caller does not have permission"]
[null, [7, "The caller does not have permission"]]
```

`ProxyStreamedCall` 流尾状态的 code 为 0 表示成功，其他 code 按下表换算 HTTP 状态：

| code | HTTP |
| ---: | ---: |
| 3、9 | 400 |
| 4 | 504 |
| 5 | 404 |
| 7 | 403 |
| 8 | 429 |
| 13 | 500 |
| 14 | 503 |
| 16 | 401 |
| 其他 | 502 |

- HTTP 429 按元数据或消息识别分钟限额与每日限额：分钟限额冷却到窗口重置，每日限额冷却到下一个额度日；非全局限额写入 `build:<模型>`，全局限额写入账户 `*`
- 流尾错误按换算后的 HTTP 状态处理；首个上游语义事件之前的 401、403、404、429 与 5xx 进入换号、换通道与冷却流程，之后的错误以流内 error 结束
- HTTP 403 与 Code 7 保留账户与模型资格，请求切换到其他账户
- 模型不存在返回 404 Code 5；只能配合 Computer Use 的模型在纯文本请求时返回 400 Code 3，这类模型不进入 Build 通道

## 10. 未接入范围

| 能力 | 当前处理 |
| --- | --- |
| Live WebSocket | 使用 Playground `BidiGenerateContent` |
| Files 与缓存上传（`ProxyUnaryFileApiCall`） | 未接入；附件以 `inlineData` 发送 |
| embedding（`batchEmbedContents`，`embedContent` 返回 404） | 未接入 |
| Interactions（`/v1beta/interactions` 返回 404） | 使用 Playground `CreateInteractionStream` |
| Free 账户的订阅图片模型 | 上游返回 403，调度按 AccessModes 排除 |
| 付费 API key 直连 | 不使用 |

## 11. 实现位置

| 路径 | 职责 |
| --- | --- |
| `internal/config/config.go` | `UPSTREAM_CHANNELS` 解析与校验 |
| `internal/app/channels.go` | 配置到调度池通道的转换 |
| `internal/aistudio/channel.go` | 通道定义、冷却 scope、通道资格、组合排序与公开目录 `channels` |
| `internal/aistudio/accounts.go` | 候选分类、租约通道、全部冷却错误 |
| `internal/aistudio/build.go` | 请求编码、代理 wire、binding、响应与流尾解码、Build 目录 |
| `internal/aistudio/generate.go` | 通道分派、共用预处理、停止序列与 usage |
| `internal/aistudio/service.go` | 受保护发送与 Build proof field |
| `internal/aistudio/upload.go` | Build 通道保留内联附件 |
| `internal/app/runtime.go` | 重试、冷却写入、同账户换通道、文件引用复制 |
| `internal/app/admin.go`、`internal/api` | 冷却、请求、日志与模型目录中的通道字段 |
| `web/src` | 设置页通道选择与各列表的通道显示 |
