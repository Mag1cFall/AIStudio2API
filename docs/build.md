# Build 通道

AI Studio 的 Build 应用经官网宿主页调用 MakerSuite 代理 RPC 访问 Gemini API，这部分调用使用与 Playground 分开计算的额度。服务把 Build 作为与 Playground 对等的生成通道：同一账户同时拥有两份额度，生成请求按账户与通道的组合调度。本文定义代理 RPC 的 wire、请求头与 WAA、模型目录与资格、调度与冷却、Gemini API JSON 的逐字段映射、响应解码、错误与额度处理，通道的配置与显示，以及经 `-build` 模型调用的写代码助手通道。WAA proof 的生成见 [WAA 实现](waa.md)，Playground 的 `GenerateContent` 见 [协议规范](protocol.md)。

## 1. 官网机制与通道配置

Build 应用运行在 `*.scf.usercontent.goog` 的 blob 沙箱 iframe 中。宿主页先向 iframe 发送 `bootstrap` 消息与 `MessagePort`，iframe 内的 shim 接管匹配 `https://generativelanguage.googleapis.com/.*`、`https://ai.studio/.*` 与 `applet:.*` 的 `fetch`，再通过端口发送 `fetch`、`websocket_open`、`get_host_url`、`get_model_quota` 等消息。用户选择付费 API key 时，宿主页携带该 key 直连 Gemini API；AIStudio2API 实现无 key 的 MakerSuite 代理分支。

宿主页转发前要求 `navigator.userActivation.hasBeenActive` 为真，同时满足累计 30 次可信 `mousemove`、5 次可信 `keydown` 或处于移动设备中的任一条件。未满足时 iframe 的 `fetch` 保持等待；这个状态只存在于页面桥接层，代理 RPC 的字段和请求头不携带激活标记。服务由账户 Worker 直接发送代理 RPC，以 RPC wire 为实现边界，无需复制 iframe 的交互门控。

| Gemini API 请求 | 代理 RPC |
| --- | --- |
| `:streamGenerateContent` | `ProxyStreamedCall` |
| 其他方法 | `ProxyUnaryCall` |
| Files 与缓存上传 | `ProxyUnaryFileApiCall` |
| Live WebSocket | 宿主页 WebChannel 转发 |

服务接入 `ProxyStreamedCall` 与 `ProxyUnaryCall` 上的生成、embedding 和模型目录，以及宿主页 WebChannel 转发的 Live 与实时音乐。`ProxyUnaryFileApiCall` 的处理见“能力路由与边界”。

### 配置与显示

`UPSTREAM_CHANNELS` 列出启用的生成通道：

| 值 | 通道 |
| --- | --- |
| `playground` | 官网 Playground 的 `GenerateContent` |
| `build` | Build 应用代理的 Gemini API 调用 |

- 默认值为 `playground,build`；逗号分隔，去除空白并转小写；至少一个，不能重复，其他值在配置校验时报错
- 列表顺序即同一账户内的通道顺序
- 配置来源为 `.env`、管理页面设置中的“上游通道”（至少保留一个，保存为 `playground,build` 顺序）或 `PUT /api/config` 的 `upstream_channels`；保存值在下一次启动生成服务时生效

`BUILD_NATIVE_NONSTREAM=true` 为默认值：Gemini、Chat、Responses、Anthropic、Interactions、图片和语音端点的非流式请求在每个账户上优先使用 Build 通道，执行 `ProxyUnaryCall` 与 `:generateContent`。Build 通道未启用、该账户的 Build 目录不支持模型或该账户的 Build 额度冷却时，由同一账户的其余可用通道执行；文件引用与专用能力按其 Playground 路由执行。关闭该选项后按原通道顺序调度，选中 Build 的非流式请求仍执行单次调用。

Playground `GenerateContent` 的响应为 repeated 流帧，完整收集后转换为公开非流式响应。日志的“上游调用”记录实际通道、`native` / `stream` 模式和 RPC；非流式请求使用流式传输时，以 WARN 记录“回退流式”与原因。Build 明确返回单次方法不支持时，尝试该通道的 `ProxyStreamedCall`；参数错误按“错误、额度与能力边界”一节处理。

通道在以下位置显示：

| 位置 | 字段 |
| --- | --- |
| `GET /v1/models`（OpenAI 格式）、`GET /v1beta/models`（Gemini 格式）、管理端 `GET /api/models` 与管理页面模型列表 | 模型对象的 `channels` |
| `GET /api/requests` 与管理页面请求列表 | 请求当前尝试的 `channel` |
| 请求日志 `request.channel` 与管理页面日志 | 请求实际使用的通道 |
| `GET /api/cooldowns` 与管理页面冷却列表 | `channel` 与去掉 `build:` 前缀的 `model_id` |

## 2. 代理 RPC 与 WAA

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

非流式请求以及 AccessModes 非空且 Free 权益不能使用的模型，经 `ProxyUnaryCall` 与 `:generateContent`；其余流式请求经 `ProxyStreamedCall` 与 `:streamGenerateContent`。

### 请求头

代理 RPC 的请求头集合、顺序与取值和 Playground 文本请求的 `GenerateContent` 相同：`content-type`、`x-goog-api-key`、`x-goog-authuser`、`x-user-agent`、`x-aistudio-visit-id`、`x-goog-ext-519733851-bin`、`authorization` 与账户 Cookie。Playground 图片请求不带 `x-goog-ext-519733851-bin`，Build 代理请求生成图片时仍携带该头。权益头 `X-AIStudio-G1-Tier` 只随 `ProxyUnaryCall` 发送（Pro 为 `TIER1`、Ultra 为 `TIER2`、Plus 为 `TIER0`，Free 不带），`ProxyStreamedCall` 不带该头。需订阅权益的模型经 `ProxyStreamedCall` 调用时上游返回 HTTP 403 与 Code 7。

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

### embedding

`POST /v1/embeddings`、`:embedContent` 与 `:batchEmbedContents` 都经 `ProxyUnaryCall` 调用上游 `:batchEmbedContents`；上游的 `:embedContent` 与 `/v1beta/openai/embeddings` 返回 404。

```json
["/v1beta/models/<MODEL_ID>:batchEmbedContents", "{\"requests\":[{\"content\":{\"role\":\"user\",\"parts\":[{\"text\":\"hello\"}]},\"taskType\":\"RETRIEVAL_QUERY\",\"outputDimensionality\":256,\"model\":\"models/<MODEL_ID>\"}]}", "<WAA_PROOF>", "POST"]
```

- 每条请求都带 `model`，`taskType`、`title`、`outputDimensionality` 有值时写入
- 上游单批最多 100 条；更多输入按顺序分批发送，各批取 fresh proof，向量与 `tokenCount` 按输入顺序合并
- 响应为 `{"embeddings":[{"values":[...]}],"tokenCount":"<N>"}`；向量数与输入数不一致时返回协议错误；`tokenCount` 计为请求的输入 token

### Live 与实时音乐

Build 应用的 Gemini Live WebSocket 由宿主页经 WebChannel 转发：

```text
https://webchannel-alkalimakersuite-pa.clients6.google.com/v1/proxy:proxyBidiStreamedCall
```

握手、前向 POST、backchannel、terminate 的 query、form 与帧格式和 Playground `BidiGenerateContent` 相同，见 [协议规范](protocol.md)。每条客户端消息为 `[路径, Gemini Live JSON, WAA proof]`：proof 位于 field 3，binding 为路径与 JSON 文本以空格连接，setup 和之后的每条消息各取一次 fresh proof。

| 方法 | 路径 |
| --- | --- |
| `bidiGenerateContent` | `/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent` |
| `bidiGenerateMusic` | `/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateMusic` |

Live setup 与 Playground setup 的语义一致：

```json
{"setup":{"model":"models/<MODEL_ID>","generationConfig":{"responseModalities":["AUDIO"],"speechConfig":{"voiceConfig":{"prebuiltVoiceConfig":{"voiceName":"Zephyr"}}},"mediaResolution":"MEDIA_RESOLUTION_MEDIUM"},"tools":[{"functionDeclarations":[...]}],"sessionResumption":{"handle":"<SESSION_TOKEN>"},"contextWindowCompression":{"triggerTokens":"104857","slidingWindow":{"targetTokens":"52428"}},"inputAudioTranscription":{},"outputAudioTranscription":{}}}
```

- `tools` 只在声明函数时出现，函数参数写入 `parametersJsonSchema`；无 session token 时 `sessionResumption` 为 `{}`
- 输出模态为 audio，不接受 translation 与 transcription
- 实时音乐 setup 只有 `{"setup":{"model":"models/<MODEL_ID>"}}`，不接受 tools、translation、transcription 与 text 输出

| 公开客户端帧 | Gemini Live JSON |
| --- | --- |
| `text` | `{"realtimeInput":{"text":"..."}}` |
| `audio` | `realtimeInput.audio`，`mimeType` 为 `audio/pcm;rate=16000` |
| `image` | `realtimeInput.video`，`mimeType` 为 `image/jpeg` |
| `media_end` | `{"realtimeInput":{"audioStreamEnd":true}}` |
| `tool_response` | `toolResponse.functionResponses[{id,name,response}]`，非对象结果包装为 `{"result":...}` |
| `music_prompts` | `clientContent.weightedPrompts` |
| `music_config` | `musicGenerationConfig` |
| `playback` | `playbackControl`：`PLAY`、`PAUSE`、`STOP`、`RESET_CONTEXT` |

Live 会话只接受前五种帧，实时音乐会话只接受后三种。

backchannel envelope 的 payload 是 ProxyResponse 列表，每个元素的索引 `0` 是一条 Gemini Live 服务端 JSON；`["noop"]`、`["close"]` 与 `{"__sm__":{"status":...}}` 和 Playground 相同：

```json
[[3,[["{\"serverContent\":{\"outputTranscription\":{\"text\":\"Hello\"}}}"]]]]
```

| 服务端字段 | 公开事件 |
| --- | --- |
| `setupComplete` | `setup_complete` |
| `serverContent.modelTurn.parts[].text` | `text`；`thought` 为真时为 `provider`，`raw` 为 `{"kind":"reasoning","text":...}` |
| `serverContent.modelTurn.parts[].inlineData` | `media`，Live 音频为 `audio/pcm;rate=24000` |
| `serverContent.audioChunks[]` | `media`，实时音乐为 `audio/l16;rate=48000;channels=2` |
| `serverContent.inputTranscription`、`outputTranscription` | `input_transcription`、`output_transcription` |
| `serverContent.interrupted`、`generationComplete`、`turnComplete` | `interrupted`、`generation_complete`、`turn_complete` |
| `toolCall.functionCalls[]` | 每个调用一条 `tool_call` |
| `toolCallCancellation.ids` | `tool_call_cancellation` |
| `usageMetadata` | `usage`，`raw` 为 Gemini API JSON |
| `goAway` | `go_away` |
| `sessionResumptionUpdate` | `session_resumption` |
| 其他 | `provider` |

- native audio 模型在轮次结束后经 `sessionResumptionUpdate` 下发 `newHandle`

一条消息内的事件顺序与 Playground 相同：setup、模型内容、转写、打断与完成标志、函数调用、usage、go away、恢复令牌。

## 3. 模型、资格与调度

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

以下请求只使用 Playground：CountTokens、Playground 目录中的 Live 与 Robotics 模型、Veo、转录、Interactions 模型、带 Drive 文件引用的生成、带多说话人 `mode` 的 TTS。

`MODEL_CHANNELS` 限定的模型只经所列通道调用。限定为 `build` 的模型收到带文件引用或只有 Playground 支持的参数的 `generateContent` 请求时，在选择账户前返回 400，错误写明该模型限定的通道。

Playground 目录没有的 embedding（`embedContent`）、Live（`bidiGenerateContent`）与实时音乐（`bidiGenerateMusic`）模型只经 Build 通道调用：`UPSTREAM_CHANNELS` 启用 `build`，且账户的 Build 目录包含该模型与方法时，账户可以承担请求。`aqa` 等其他方法不进入 Build 通道。

### 公开目录

公开目录是已启用通道的并集。Build 独有且至少一个启用账户可经 Build 调用的可生成、embedding、Live 与实时音乐模型加入公开目录；embedding 模型的 methods 为 `batchEmbedContents, embedContent`，实时模型为 `bidiGenerateContent` 或 `bidiGenerateMusic`。Build 未启用时这些模型不在目录中，请求返回模型不存在。模型出现在 Playground 目录时，Build 请求使用 Playground 目录的默认参数与能力；Build 独有模型的默认输出上限为 `outputTokenLimit`，思考能力取 `thinking`。每个模型的 `channels` 列出至少一个启用账户可调用它的通道，启用账户的权益都不满足 AccessModes 时不含 `channels`。

### 调度与冷却

候选单位为账户与通道的组合，组合按账户 ID 升序、同一账户内按 `UPSTREAM_CHANNELS` 顺序排列。调度仍先尝试已有热 Worker 的账户，再按需启动待机账户的 Worker，规则见 [开发与贡献](development.md)。

| 策略 | 组合推进 |
| --- | --- |
| `round-robin` | 每个模型记录上次选中的账户与通道，从其后的组合开始 |
| `fill-first` | 始终从首个可用组合开始 |

- 并发槽位、Worker 与 WAA 按账户共享，两个通道占用同一组请求槽位
- 冷却按通道记录：Playground 为 `<模型>`，Build 为 `build:<模型>`；Live 媒体输入的 scope 为 `bidi-media:<模型>`，经 Build 时为 `build:bidi-media:<模型>`；全局 `*` 冷却对两个通道都生效
- 账户只有在其全部支持的通道都冷却时才视为该模型冷却
- 一个通道返回额度错误后写入该通道冷却；同一账户另一通道可用时，请求在同一账户的另一通道重试，不计为已尝试账户
- 全部候选组合冷却时，最早恢复时间在 1 分钟内的请求排队等待，更晚的请求返回 HTTP 429 `rate_limit_exceeded`，消息给出最早恢复时间，`Retry-After` 为距最早恢复的秒数
- 生成成功在该通道的 scope（`<模型>` 或 `build:<模型>`）上写入 `verified`

### 附件与文件引用

| 输入 | Build | Playground |
| --- | --- | --- |
| 内联附件 | 直接写入 `inlineData` | 上传到本次生成账户的 Drive，以 file ID 引用 |
| YouTube 链接 | `fileData` | external media Part |
| Drive 文件引用 | 不接受 | 使用文件所属账户 |

带 Drive 文件引用的生成固定走 Playground。文件所属账户没有可调度的 Playground 候选，或其冷却在 1 分钟后才恢复时，文件临时复制到其他账户，由该账户的 Playground 生成，请求结束后回收副本。

## 4. 请求映射

Build 请求与 Playground 共用同一预处理：工具可用性校验、模型媒体默认值、TTS 台词处理、参数校验与目录默认值。停止序列不发送，由本地匹配截断（见本节“停止序列、usage 与终态”）。请求体顶层字段：

| 字段 | 取值 |
| --- | --- |
| `contents` | 规范 contents |
| `systemInstruction` | system 非空时为 `{"parts":[{"text":"<SYSTEM>"}]}` |
| `tools` | 工具声明 |
| `toolConfig` | 同时声明函数与 Google 工具时为 `{"includeServerSideToolInvocations":true}` |
| `generationConfig` | 生成参数 |
| `safetySettings` | 安全设置 |

- `safetySettings` 默认为骚扰、仇恨、色情、危险四类 `OFF`，请求中的类别按名称覆盖或追加；图片路由只发送请求中的类别

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
| `maxOutputTokens` | 请求值或目录默认值 |
| `temperature`、`topP`、`topK`、`seed` | 请求值或目录默认值 |
| `responseMimeType` | 请求值 |
| `responseSchema` | protobuf JSON 形式的 Schema |
| `responseModalities` | 大写模态名 |
| `imageConfig` | `{aspectRatio?, imageSize?}` |
| `speechConfig` | 单声音为 `voiceConfig.prebuiltVoiceConfig.voiceName`；多说话人为 `multiSpeakerVoiceConfig.speakerVoiceConfigs[{speaker, voiceConfig}]` |
| `thinkingConfig` | `includeThoughts`（请求要求输出思考时为 true）、`thinkingBudget`、`thinkingLevel` |

- `maxOutputTokens` 按模型上限校验；带语音配置且未显式设置时不发送
- `responseSchema` 复用 Playground 的 Schema 校验与规范化：嵌套 `type` 使用大写枚举，字符串 `const` 转为 `enum`，null 联合转为 `nullable`
- `responseModalities` 为图片模型补 `IMAGE`、`TEXT`，为 TTS 与音乐模型补 `AUDIO`
- 可设置分辨率的图片模型的 `imageSize` 默认 `1K`
- `thinkingConfig` 只在模型支持思考时发送

thinking level 由 Playground 枚举换算：Low=`LOW`、Medium=`MEDIUM`、High=`HIGH`、Minimal=`MINIMAL`。`reasoning_effort`、预算与等级之间的换算规则与 Playground 相同。

TTS 模型带 `speech_metadata` 能力时，`说话人: 台词` 文本按多说话人配置拆成带 `speechMetadata` 的分段；其他 TTS 模型把分段的说话人与风格折叠回台词文本。Build 代理请求的 `multiSpeakerVoiceConfig` 只含 `speakerVoiceConfigs`，带 `mode` 的请求走 Playground。

Playground 私有字段在 Build 请求中不发送：账户时区、GenerateContent 与 generation config 的固定槽、可设置分辨率图片模型的默认工具槽。

## 5. 响应、终止与 usage

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
| `urlContextMetadata.urlMetadata` | grounding 的 URL 读取结果（`retrievedUrl`、`urlRetrievalStatus`） |
| `usageMetadata` | usage |
| `finishReason` | usage 与 finish |

- Part 的 `thoughtSignature` 附在该 Part 产生的事件上
- 带 `thought: true` 的 `inlineData` 是图片模型思考过程中的草图，不作为输出媒体
- `groundingMetadata` 读取 `webSearchQueries`、`searchEntryPoint {renderedContent, sdkBlob}`、`groundingChunks` 的 `web`、`retrievedContext`、`maps`（`uri`、`title`、`text`、`placeId`）、`groundingSupports`（segment 的 `partIndex`、`startIndex`、`endIndex`、`text`，`groundingChunkIndices`，`confidenceScores`）与 `googleMapsWidgetContextToken`
- `usageMetadata` 的 `totalTokenCount` 大于 0 时记录：`promptTokenCount` 为输入，`candidatesTokenCount` 为输出，`thoughtsTokenCount` 为思考，`toolUsePromptTokenCount` 为工具；缺少 `candidatesTokenCount` 时输出为总数减去其余三项
- 第一次出现 `finishReason` 时先发出最近的 usage，再发出 finish；终止原因为去掉 `FINISH_REASON_` 前缀后的小写枚举名，与 Playground 的规范终止原因一致，`UNSPECIFIED` 为 `unspecified`
- 没有 candidate 且带 `promptFeedback.blockReason` 时返回 prompt feedback 错误，原因为去掉 `BLOCK_REASON_` 前缀的小写枚举名
- candidate 数量不是 1、正文不是 JSON 或 Base64 无效时返回带路径的协议错误；流结束时没有 `finishReason` 与读取中断相同，报告为上游流中断与已接收帧数

### 停止序列、usage 与终态

Build 与 Playground 的事件流经过同一处理：

1. 停止序列不发送给上游；本地在 text 事件中匹配，命中时截断正文并关闭上游流，终态为 `stop_sequence`
2. 带停止序列的请求并行调用同账户的 Playground `CountTokens`，命中时用计数的输入总数加已输出内容构造 usage；计数失败时省略 usage
3. 上游没有返回 usage 时在本地统计输入、工具声明、思考与输出
4. usage 在 finish 之前发出

## 6. 错误、额度与能力边界

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

- HTTP 429 按元数据或消息识别分钟限额与每日限额：分钟限额冷却到窗口重置，每日限额冷却到下一个额度日；非全局限额写入 `build:<模型>`，全局限额写入账户 `*`；列出的额度项上限全部为 `limit: 0` 时冷却类型为“无配额”
- HTTP 400、Code 3 发生在可经 Playground 完成的请求上且 Playground 通道启用时，同一账户改经 Playground 重试，需要 Build 的结构化输出改用 Playground 载体；只启用 Build 时返回 Build 的错误。Playground 返回首个事件后按 `build:<模型>` 递增冷却 1、5、15、30、60 分钟，该范围成功一次后从 1 分钟开始；Playground 同样拒绝时返回 Playground 的错误
- 流尾错误按换算后的 HTTP 状态处理；首个上游语义事件之前的 401、403、404、429 与 5xx 进入换号、换通道与冷却流程，之后的错误以流内 error 结束
- HTTP 403 与 Code 7 保留账户与模型资格，请求切换到其他账户
- 模型不存在返回 404 Code 5；只能配合 Computer Use 的模型在纯文本请求时返回 400 Code 3，这类模型不进入 Build 通道

### 能力路由与边界

| 能力 | 当前处理 |
| --- | --- |
| Live WebSocket | Playground `BidiGenerateContent` 或 Build WebChannel |
| Files 与缓存上传（`ProxyUnaryFileApiCall`） | 不使用 |
| embedding | 上游 `batchEmbedContents` |
| Interactions（`/v1beta/interactions` 返回 404） | Playground `CreateInteractionStream` |
| Free 账户的订阅图片模型 | 按 AccessModes 排除 |
| 付费 API key 直连 | 不使用 |

- Live 与实时音乐按模型所在目录选择通道，见“通道资格”；附件处理见“附件与文件引用”；embedding 见“embedding”
- Free 账户调用订阅图片模型时上游返回 403

## 7. 实现位置

| 路径 | 职责 |
| --- | --- |
| `internal/config/config.go` | `UPSTREAM_CHANNELS` 解析与校验 |
| `internal/app/channels.go` | 配置到调度池通道的转换 |
| `internal/aistudio/channel.go` | 通道定义、冷却 scope、通道资格、组合排序与公开目录 `channels`；`-build` 模型只用 `assistant` 通道 |
| `internal/aistudio/accounts.go` | 候选分类、租约通道、全部冷却错误 |
| `internal/aistudio/build.go` | 请求编码、代理 wire、binding、响应与流尾解码、Build 目录 |
| `internal/aistudio/generate.go` | 通道分派、共用预处理、停止序列与 usage |
| `internal/aistudio/service.go` | 受保护发送、Build 与写代码助手的 binding 和 proof field |
| `internal/aistudio/upload.go` | Build 通道与写代码助手通道保留内联附件 |
| `internal/aistudio/assistant.go` | 写代码助手的目录条目派生、附件改写、`CodeAssistantOffline` 编码、RPC 顺序、临时应用清理与任务流解码 |
| `internal/aistudio/transport_http.go` | 写代码助手附件上传 |
| `internal/aistudio/models.go` | 能力码 48 与 `-build` 条目追加 |
| `internal/aistudio/build_embed.go` | embedding 编码、分批、发送、解码与换号 |
| `internal/aistudio/build_bidi.go` | Build Live 与实时音乐的代理外层、客户端帧编码与 backchannel 解码 |
| `internal/aistudio/webchannel.go` | WebChannel 会话按租约通道选择地址、proof field、帧编码与解码 |
| `internal/app/embeddings.go` | embedding 候选、通道日志与输入 token |
| `internal/api/embeddings.go`、`internal/api/live.go` | OpenAI 与 Gemini embedding 端点、实时音乐客户端帧 |
| `internal/app/runtime.go` | 重试、冷却写入、同账户换通道、文件引用复制 |
| `internal/app/admin.go`、`internal/api` | 冷却、请求、日志与模型目录中的通道字段 |
| `web/src` | 设置页通道选择与各列表的通道显示 |

### 协议实现

新增一个 Build 代理方法或把现有公开能力接到 Build 时，按以下顺序落点：

1. 在 `internal/aistudio/build.go` 定义 Gemini API 路径、HTTP 方法、JSON 请求体与 `ProxyStreamedCall` 或 `ProxyUnaryCall` 外层；binding 始终取最终路径和最终 JSON 文本，避免编码后再改正文
2. 在 `internal/aistudio/channel.go` 声明候选资格与 Playground 固定条件；文件归属、专用 scope、模型方法和账户权益必须在取得租约前确定
3. 在 `internal/aistudio/generate.go` 复用规范请求预处理，并为 Build 编码器只映射 Gemini API 公开字段；Playground 私有槽位留在 Playground 编码器
4. 在 `DecodeBuildStream` 或 `DecodeBuildUnary` 把完整 Gemini API 响应转成规范事件；流式外层按完整 ProxyResponse 元素切分，不能按网络 chunk 猜测 JSON 边界
5. 把错误映射到统一的 HTTP 状态、重试阶段与通道冷却 scope；首个语义事件之后的错误只能作为流内终态
6. 将模型 `channels`、请求 `channel` 与冷却 `channel` 暴露到管理 API 和页面，使实际路由可以直接观察

### 最小验收矩阵

| 范围 | 必须确认的结果 |
| --- | --- |
| 通道选择 | `playground`、`build` 单独启用和同时启用时，目录与实际路由一致 |
| 额度隔离 | 一个通道进入模型冷却后，同一账户的另一个通道仍可承担请求；全局冷却同时作用于两者 |
| 协议 | Chat、Responses、Anthropic 与 Gemini 的非流式、流式正文和终态一致 |
| 内容 | system、多轮、函数、Google 工具、图片输入、TTS 与图片输出按模型能力编码 |
| 生命周期 | 取消、客户端断开、停止序列与流尾错误都释放租约和账户并发槽位 |
| 可观测性 | 模型目录、请求列表、请求日志和冷却列表显示同一个实际通道 |
| embedding 与实时 | embedding、Live 与实时音乐经 Build 返回预期向量、音频与终态，请求日志通道为 `build` |

调试代理正文时先保存最终 Gemini API JSON、外层数组和解码后的 google.rpc 状态。HTTP 200 只表示代理 RPC 到达；业务通过条件是收到预期内容、合法 usage 和唯一终态。

## 8. 写代码助手通道

Build 页面的对话面板由写代码助手回复。助手带有服务端的系统提示词，用量计入 Build 对话的额度：免费账户为免费请求，Google AI 订阅账户为订阅额度，与 Playground、Build 应用代理分别计算。服务把它作为第三个通道 `assistant`，经模型名后缀 `-build` 调用。

本节的 RPC 都位于 MakerSuite 服务，地址为 `https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/<RPC>`，方法为 `POST`，正文为 `application/json+protobuf` 稀疏数组：protobuf field N 对应 JSON 索引 N-1，中间未设置的 field 写 `null`。

### 目录与调度

ListModels 行的主能力码（field 65）含 48 的模型，即 Build 设置面板 “Select model to use in Chat” 下拉框列出的模型。目录解析完成后，每个这样的模型派生一个条目，按原顺序追加在该账户目录末尾；目录已有同名 ID 时不派生。

| 字段 | 派生条目 |
| --- | --- |
| ID | `<基础模型 ID>-build` |
| 名称 | `<基础模型名称> (Build)` |
| methods | 基础模型 methods 中的 `generateContent` 与 `countTokens` |
| capabilities | `assistant_route`，加基础模型具备的 `chat_model`、`thinking`、`thinking_level` |
| 能力选项 | 空 |
| 默认参数 | 输出上限与可选 thinking level 取基础模型，思考开启，默认 level 为 high（3） |
| 其余字段 | 取基础模型，含描述、token 上限与 AccessModes |

```text
ListModels: gemini-3.8-flash（主能力码含 48）
目录追加:   gemini-3.8-flash-build  "Gemini 3.8 Flash (Build)"  methods [countTokens, generateContent]
```

- `-build` 模型的候选通道只有 `assistant`；该通道始终启用，`UPSTREAM_CHANNELS` 与 `BUILD_NATIVE_NONSTREAM` 只作用于其他模型
- 账户的目录含该 `-build` 条目、且账户权益满足基础模型的 AccessModes 时，账户可以承担请求；候选顺序、热 Worker 优先与 `round-robin`、`fill-first` 策略与其他模型相同
- 冷却 scope 为 `-build` 模型 ID 本身，例如 `gemini-3.8-flash-build`，与基础模型的 Playground `<模型>`、Build `build:<模型>` 分开记录；额度错误、换号与排队按“调度与冷却”与“错误、额度与能力边界”处理
- 模型 `channels`、请求 `channel` 与冷却 `channel` 为 `assistant`，管理页面显示为 “Build 对话”
- 模型名可以带思考后缀，后缀写在 `-build` 之后，例如 `gemini-3.8-flash-build(low)`
- 计数请求把模型换成基础模型 ID，调用 Playground `CountTokens`

### 请求映射

| 规范请求 | `CodeAssistantOffline` |
| --- | --- |
| contents | field 1 |
| system | field 9 自定义指令 |
| 思考强度或思考预算 | field 19 的 thinking level |

- 上游请求只携带对话轮、系统提示与思考强度；生成参数、结构化输出与安全设置使用助手的服务端设置
- 内联附件按官网对话面板上传，对话轮中以 Part field 18 `[mime, 上传路径, 文件名, 字节数]` 引用，文件名为 `attachment-<序号>`，序号按附件在请求中的顺序从 1 开始；上传见“附件上传”
- `-build` 条目没有函数与 Google 工具能力：工具声明在发送前丢弃，历史中的函数调用与结果按 [协议规范](protocol.md) 改写为文本
- 停止序列在本地匹配截断，见“停止序列、usage 与终态”
- thinking level 为 high `3`、medium `2`、low `1`、minimal `4`；未指定时为 high。`reasoning_effort`、`thinkingLevel` 与 Anthropic `output_config.effort` 的 `xhigh`、`max` 按 high，`none` 按 minimal；思考预算 `<=0`、`<=1024`、`<=8192` 与更大值分别取 minimal、low、medium、high。模型不支持所得 level 时取强度顺序 minimal、low、medium、high 中最接近的可用 level

### RPC 顺序

```text
/_/upload（每个内联附件一次）               → 上传路径
CreateApplet                                → 应用 ID
CodeAssistantOffline（WAA proof）           → 任务 ID
StreamCodeAssistantOfflineGeneration        → 步骤快照流，读到上游关闭
流关闭后在后台：
  CancelCodeAssistantOfflineGeneration      （已取得任务 ID 且流没有正常解码完成时）
  DeleteApplet
```

- 请求头的集合、顺序与取值和 Playground 文本请求的 `GenerateContent` 相同；权益头 `X-AIStudio-G1-Tier` 只随 `CodeAssistantOffline` 发送（Pro 为 `TIER1`、Ultra 为 `TIER2`、Plus 为 `TIER0`，Free 不带）
- `CodeAssistantOffline` 由账户 WAA Worker 发送；其余四个 RPC 与附件上传经账户的 MakerSuite Go HTTP transport，使用同一账户的 Cookie、UA 与出口
- 每个请求新建一个应用，请求结束后删除。上游把同一应用上以前的对话带入后续生成，应用只承载一次请求
- 非流式请求同样读取任务流，收齐后输出；日志的“上游调用”记录 `stream` 模式、RPC `CodeAssistantOffline` 与原因 “Build 写代码助手使用服务端流，收集完成后返回”

#### 附件上传

附件经 AI Studio 的可续传上传接口写入，两次请求都是 `POST`，带账户 Cookie、UA、`Accept-Language`、`Origin` 与 `Referer` 为 `https://aistudio.google.com`、`Sec-Fetch-Site: same-origin`，不带 `Authorization` 与 `X-Goog-Api-Key`。`X-Goog-Hash` 为附件字节 SHA-256 的标准 Base64，写作 `sha256=<BASE64>`。

| 步骤 | 地址 | 请求头 | 正文 | 响应 |
| --- | --- | --- | --- | --- |
| start | `https://aistudio.google.com/_/upload` | `X-Goog-Upload-Protocol: resumable`、`X-Goog-Upload-Command: start`、`X-Goog-Upload-Header-Content-Length`、`X-Goog-Upload-Header-Content-Type`、`X-Goog-Hash` | 空 | 200，`X-Goog-Upload-URL` 为上传地址 |
| upload | `X-Goog-Upload-URL` | `Content-Type`、`X-Goog-Hash`、`X-Goog-Upload-Command: upload, finalize`、`X-Goog-Upload-Offset: 0` | 附件字节 | 200，`X-Goog-Upload-Status: final`，正文为上传路径 |

```text
/blobstore/prod/makersuite/uploads/<SHA256_HEX>
```

非 200 响应按“错误、额度与能力边界”换算。上游在任务流的用户轮回显中把上传路径改写为存储内部路径。

#### CreateApplet

```json
[null,["Untitled","Generated by Gemini.",null,null,"default"],null,null,1]
```

| field | 内容 |
| ---: | --- |
| 2 | 应用元数据 `[标题, 描述, 首条提示, null, 模板]` |
| 5 | `1` |

官网页面在元数据 field 3 写入用户的第一条提示，在 field 6 写入页面 API key，上游据首条提示生成应用的标题与描述；服务发送的元数据 field 3 为 `null`，正文只到 field 5。响应：

```json
["<APPLET_ID>",["<APPLET_ID>",["1792242943",522276000],["1792242943",522276000],["Untitled","Generated by Gemini.",null,null,"default",null,null,1,[1],null,[1]],1,[1,1,1]]]
```

服务读取索引 0 的应用 ID；缺失或为空时返回协议错误 “缺少应用 ID”。

#### CodeAssistantOffline

```json
[[[[[null,"What is 6*7?"]],"user"]],"<WAA_PROOF>",null,null,null,1,null,"models/gemini-3.8-flash","<SYSTEM>",null,null,"<APPLET_ID>",null,[2,null,2],null,1,null,null,[1,null,null,3],null,null,null,null,null,null,null,null,null,"<UUID>"]
```

| field | 内容 |
| ---: | --- |
| 1 | 对话轮，编码与 Playground `GenerateContent` 的 contents 相同：`[[Part...], role]`，role 为 `user` 或 `model` |
| 2 | WAA proof |
| 6 | `1` |
| 8 | `models/<基础模型 ID>` |
| 9 | 系统提示；system 为空时为 `null` |
| 12 | 应用 ID |
| 14 | `[2,null,2]` |
| 16 | `1` |
| 19 | 思考配置 `[1,null,null,<thinking level>]` |
| 29 | 每个请求新生成的大写 UUID v4 |

WAA binding 与 Playground `GenerateContent` 相同：contents 各 Part 依次取文本、内联数据的标准 Base64 或 Drive file ID，其余 Part 取空字符串，以单个空格连接；proof 写入 field 2。Camoufox 后端的页面输入框写入 contents 各 Part 的文本，以空格连接。proof 生成与发送见 [WAA 实现](waa.md)。

响应：

```json
["<GENERATION_ID>","<APPLET_ID>"]
```

服务读取索引 0 的任务 ID；缺失或为空时返回协议错误 “缺少任务 ID”。

#### StreamCodeAssistantOfflineGeneration

```json
["<GENERATION_ID>",null,null,"<APPLET_ID>"]
```

响应为流式正文，格式见下一小节。

#### 清理

```json
["<GENERATION_ID>","<APPLET_ID>"]
["<APPLET_ID>"]
```

第一行是 `CancelCodeAssistantOfflineGeneration` 的正文，第二行是 `DeleteApplet` 的正文；`DeleteApplet` 成功响应为 `[]`，之后该应用返回 404。

- 清理在任务流正文关闭时触发，包括正常读完、客户端断开与停止序列截断；`CreateApplet` 之后任一步失败时同样触发
- 每个请求的清理只执行一次，在后台以独立的 1 分钟期限依次执行 Cancel 与 Delete
- 任一步失败时以 WARN 记录 “Build 写代码助手清理失败”，字段为 `account`、`method`、`applet` 与 `error`

### 任务流

响应根数组的 field 1 是消息列表，之后可带 google.rpc 状态 `[code, message, details?]`。下例为一次完整的纯问答，消息 1 是用户轮回显，消息 2 是完成的模型轮：

```json
[[
  [null,null,[[[[[null,"What is the capital of France?"]],"user"]],null,null,null,null,null,["1792220560",486323906],["1792220562",547770000],null,null,["<APPLET_ID>",["1792220558",913217000]],0,1,"models/gemini-4-argon",null,null,"<GENERATION_ID>"]],"<APPLET_ID>"],
  [null,null,[[[[[null,"Paris"]],"model"]],null,null,null,null,null,["1792220560",486643491],["1792220571",188407749],null,null,["<APPLET_ID>",["1792220571",308506000]],1,1,"models/gemini-4-argon",null,["19942","45"],"<GENERATION_ID>"]],"<APPLET_ID>"]
]]
```

消息为 `[null, null, [步骤...], 应用 ID]`。每条消息带若干步骤的当前快照，同一条消息可以同时含用户轮回显与模型轮。步骤字段：

| field | 内容 |
| ---: | --- |
| 1 | 对话轮列表 `[[[Part...], role]]` |
| 3 | 应用文件修改 |
| 7、8 | 时间戳 `["<秒>", <纳秒>]` |
| 10 | 工具执行，例如编译应用 |
| 11 | `[应用 ID, 时间戳]` |
| 12 | 步骤序号，用户轮为 0 |
| 6 | 失败原因 `[消息, 详情]` |
| 13 | 状态：`1` 完成，`2` 进行中，`4` 失败 |
| 14 | `models/<模型 ID>` |
| 16 | 用量 `[未命中缓存的输入, 输出, 缓存命中]`，数值为十进制字符串，缺项为 0 |
| 17 | 任务 ID |

Part 的 field 2 为文本，field 13 为 `1` 或 `true` 时是思考。只修改应用文件或执行工具的步骤没有模型轮。

解码：

1. 消息列表中每出现一个完整元素立即解码；消息 field 3 为 `null` 时跳过
2. 每个步骤读取序号与状态，二者缺失或类型不符时返回带路径的协议错误；field 6 非空时返回上游错误 “生成步骤失败: <消息>”；状态为 `1` 且带用量的步骤按序号记录用量，同一序号以最后一次为准
3. 跳过 role 不是 `model` 的对话轮。模型轮的 Part 以（步骤序号，Part 序号）定位，同一步骤内多个模型轮的 Part 序号连续编号
4. Part 文本是该位置的完整快照：以上一次快照开头时输出新增部分，与上一次相同时不输出；不以上一次快照开头时返回协议错误 “步骤正文不是上一次快照的延续”
5. 思考 Part 的增量输出为 reasoning，其余为 text；text 来自与上一段 text 不同的步骤、且是该 Part 的第一段增量时，前面加 `\n\n`
6. 状态为 `1` 的步骤含模型轮时，回复记为已完成
7. 消息列表结束后读取流尾状态，code 0 为成功，其他 code 按“错误、额度与能力边界”的表换算 HTTP 状态
8. 流关闭时回复已完成：有用量时先输出用量之和，再以 `stop` 结束；回复没有完成时返回协议错误 “流结束前没有完成的模型回复”

用量之和：输入为各步骤未命中缓存的输入与缓存命中之和，缓存为缓存命中之和，输出为输出之和，总数为输入加输出。

最后一个步骤完成后，上游约 1 到 14 秒后关闭流，finish 在流关闭后输出。

### 错误

| 情况 | 处理 |
| --- | --- |
| 附件上传、`CreateApplet`、`CodeAssistantOffline` 或任务流返回非 200 | 按“错误、额度与能力边界”解码与换算，进入换号与冷却流程，冷却 scope 为 `-build` 模型 ID |
| 流尾状态非 0 | 同上；首个语义事件之后以流内 error 结束 |
| 步骤 field 6 非空 | HTTP 502，消息为 “生成步骤失败: <上游消息>”，按 5xx 处理 |
| 应用 ID、任务 ID 缺失或流结构不符 | 协议错误，HTTP 502 |
| 流关闭时没有完成的模型回复 | 协议错误，HTTP 502 |
