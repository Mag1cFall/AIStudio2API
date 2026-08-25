# Google AI Studio 私有协议

本文定义 AIStudio2API 使用的 Google AI Studio 私有协议、认证状态、WAA 运行时、JSON+protobuf 数组、增量事件、工具与媒体链。模型方法、限制和能力由账户的实时 `ListModels` 返回，公开 API 只投影协议核心产生的规范事件。

## 1. 协议范围、入口与公共头

| 用途 | 入口 | 格式 |
| --- | --- | --- |
| 页面 origin | `https://aistudio.google.com` | HTTPS |
| MakerSuite RPC | `https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/<METHOD>` | `application/json+protobuf` |
| WAA RPC | `https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/<METHOD>` | `application/json+protobuf` |
| BotGuard interpreter | `https://www.google.com/js/bg/<INTERPRETER_HASH>.js` | JavaScript |
| Drive 上传 | `https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id` | `multipart/related` |
| Drive 下载 | `https://www.googleapis.com/drive/v3/files/<FILE_ID>?alt=media` | HTTPS body |

MakerSuite 请求使用以下公共头：

| Header | 来源 |
| --- | --- |
| `content-type` | 固定为 `application/json+protobuf` |
| `user-agent` | 当前账户 Camoufox 官网请求 |
| `x-user-agent` | 官网 gRPC-Web 标识 |
| `x-goog-api-key` | AI Studio 首页或当前官网请求动态值 |
| `x-goog-authuser` | 当前账户官网请求 |
| `x-aistudio-visit-id` | 首页初始化或当前官网请求 |
| `x-aistudio-g1-tier`、`x-goog-ext-519733851-bin` | 官网请求存在时透传 |
| `authorization` | 三段 SAPISID 签名 |
| `cookie` | 当前账户对目标 RPC 可见的 Cookie |
| `origin`、`referer` | `https://aistudio.google.com` |
| `accept-language` | 账户 locale |

请求头 `x-goog-api-key` 是 AI Studio 页面使用的动态公共值，与用户创建的 Google Cloud API key 不同；免费网页链仍依赖 Cookie、SAPISID 签名和 WAA proof。

MakerSuite 与 Drive 业务请求使用和 Camoufox 对齐的 Firefox TLS、HTTP/2 与请求头顺序；WAA VM、fresh proof 和隔离登录由同一账户的 Camoufox 环境完成。

JSON+protobuf 使用数组表示 protobuf message。数组索引从 `0` 开始，protobuf field 从 `1` 开始，因此 field `N` 对应索引 `N-1`。Google 响应允许省略空槽并形成 `[,value]`；解码前应把省略槽规范化为 `null`，不得把 HTTPS chunk 当作数组或业务事件边界。

协议核心使用以下 MakerSuite RPC：

| RPC | 用途 |
| --- | --- |
| `ListModels` | 读取模型、方法、限制、默认参数与能力选项 |
| `CountTokens` | 权威输入 token 计数 |
| `GenerateContent` | 文本、思考、函数、Google 工具、图片、语音与音乐 |
| `GenerateAccessToken` | 获取 Drive bearer token |
| `GenerateVideo` | 创建 Veo 长任务 |
| `GetGenerateVideoOperation` | 轮询 Veo 长任务 |

AI Studio 页面初始化还包含以下控制面 RPC：

| RPC | 用途 |
| --- | --- |
| `GetLoggingContext` | 页面日志上下文 |
| `GetUserPreferences` | 用户偏好与欢迎状态 |
| `UpdateUserPreferences` | 更新欢迎状态等用户偏好 |
| `ListPromos` | 页面活动信息 |
| `GetAiStudioBenefitTier` | 账户层级枚举 |
| `ListRecentApplets` | 最近 Applet |
| `ListPrompts` | 提示词目录 |
| `GetUserRestrictions` | 账户限制 |

这些控制面响应不参与生成请求编解码。服务启动只加载账户、公开头和实时模型目录，业务能力按需调用对应数据面 RPC。

## 2. SAPISID、Chrome DBSC、Cookie 与账户状态

### SAPISID 授权

`authorization` 由三个 Cookie 分别签名：

| 令牌标签 | Cookie |
| --- | --- |
| `SAPISIDHASH` | `SAPISID` |
| `SAPISID1PHASH` | `__Secure-1PAPISID` |
| `SAPISID3PHASH` | `__Secure-3PAPISID` |

三段使用相同的 Unix 秒级时间戳：

```text
source = "<TIMESTAMP> <COOKIE_VALUE> https://aistudio.google.com"
digest = lowercase_hex(SHA1(source))
token = "<LABEL> <TIMESTAMP>_<DIGEST>"
authorization = token_1 + " " + token_2 + " " + token_3
```

MakerSuite 响应的 `Set-Cookie` 在账户独占租约内合并到 `storage-state.json` 并原子写回。签名、Cookie 选择和过期判断均以请求时重新读取的账户状态为准。

### Windows Chrome DBSC 导入

Windows Chrome 导入从 Profile 恢复 OAuth 与 Device Bound Session Credentials：

```text
Chrome Local State + Profile Preferences + Web Data/token_service
  -> Gaia ID、v20 refresh token 密文、wrapped binding key
  -> 解开 App-Bound v20 主密钥
  -> AES-256-GCM 解密 refresh token
  -> OAuthMultilogin sentinel 请求取得 DBSC challenge
  -> NCrypt 设备密钥签发 ES256 assertion
  -> X25519/HPKE 解密服务端 Cookie
  -> 保存 Playwright storage state 结构与续签材料
```

`Local State.os_crypt.app_bound_encrypted_key` 使用 Base64 编码并带 `APPB` 前缀。程序把内嵌 ABE helper 加载到独立、隐藏的 Chrome 进程中，取得 32 字节主密钥；临时进程树由 Windows Job Object 管理。`token_service.encrypted_token` 使用 `v20 || nonce[12] || ciphertext+tag`，以该主密钥执行 AES-GCM 解密。

OAuthMultilogin 使用 `MultiOAuth` 头。第一次 assertion 为 `DBSC_CHALLENGE_IF_REQUIRED`，响应提供 challenge；第二次 assertion 的 JWT header 使用 `ES256` 与 `DEVICE_BOUND_SESSION_CREDENTIALS_ASSERTION`。payload 绑定 Google OAuth client、challenge、设备公钥 issuer 和临时 HPKE 公钥。Cookie 密文使用 X25519、HKDF-SHA256 与 AES-128-GCM 解密。

Chrome 导入状态在 `storage-state.json` 的 `aistudio2api` 扩展中保存来源、Gaia ID、refresh token 与 wrapped binding key。普通或受保护 RPC 首次返回 401/403 时，服务在同一账户出口续签 Cookie、使动态头失效、关闭该账户 WAA runtime，并只重放一次。隔离 Camoufox 登录和外部 storage state 不携带 Chrome OAuth 扩展。

### 账户持久状态

| 文件 | 内容 |
| --- | --- |
| `account.json` | label、enabled、proxy、locale、timezone |
| `storage-state.json` | Cookie、localStorage 和可选 Chrome 续签材料 |
| `camoufox-fingerprint.json` | 账户固定的 navigator、屏幕、字体、语言、地区和时区配置 |
| `runtime-state.json` | 模型/全局冷却与 Drive/Veo 资源到账户的绑定 |

初始化、WAA、MakerSuite、OAuth 续签和 Drive 使用账户固定代理。locale 同时设置 navigator language、Accept-Language 与地区，timezone 设置浏览器时区；重新登录和 WAA runtime 复用同一账户指纹。调度器先按模型与方法筛选，再轮询获取独占账户；进程内互斥与文件租约共同保护状态。未绑定账户和资源的请求遇到可重试的 401、403、404、429、5xx 或单账户初始化超时时，可以在首个客户端可见事件前切换到另一个同能力账户一次。Drive file、Veo operation 与产物 file 始终使用创建账户。

## 3. WAA challenge、官方 VM 与 fresh proof

受保护请求使用以下链路：

```text
Waa/Create
  -> decode challenge
  -> load interpreter by current hash
  -> initialize official VM lifecycle
  -> expose official snapshot service
  -> SHA-256(binding prompt) as lowercase hex
  -> snapshot({TYb:{content:<DIGEST>}})
  -> write fresh proof into request
  -> Go HTTP transport sends MakerSuite RPC
```

`Waa/Create` 响应第二槽经 Base64 解码后，对每个字节加 `97` 得到 challenge：

```json
{
  "messageId": "<MESSAGE_ID>",
  "globalName": "<GLOBAL_NAME>",
  "interpreterHash": "<INTERPRETER_HASH>",
  "interpreterUrl": "https://www.google.com/js/bg/<INTERPRETER_HASH>.js",
  "program": "<DYNAMIC_PROGRAM>"
}
```

`program` 与 challenge 属于当前 Create 生命周期，interpreter 只能按 hash 缓存。proof 绑定当前 prompt 摘要与 VM 内部状态，必须逐请求生成，禁止缓存或重放。

每个账户的 WAA runtime 延迟到第一次受保护请求启动：

1. Go 启动隔离、无头 Camoufox，并通过原生 WebDriver BiDi 建立 session
2. 写入账户 Cookie 与 localStorage，使用实时目录中的 `gemini-flash-latest` 进入新对话
3. 定位当前 bundle 中调用 `.snapshot({` 且包含 `content` 的官方高层函数
4. 填入唯一 bootstrap prompt 并执行一次官网 Run
5. 保存官网 `GenerateContent` 的必要动态头与官方 WAA service
6. 后续业务请求串行调用同一 service 获取 fresh proof
7. `GenerateContent` 写入 field 5，`GenerateVideo` 写入 field 8，正文由 Go HTTP transport 发送

Camoufox 不读取业务响应 DOM、不操作模型菜单、不负责公开协议输出。运行期不需要 Python、Node.js 或 Playwright。官方 VM 初始化形状为：

```javascript
initialize(program, ready, true, environment, signalLists, persistentState, false, loggers)
```

VM 生命周期参数为 `43,200,000ms`，检查间隔为 `300,000ms`。页面生命周期中断、snapshot 错误、计时器到期、认证续签或进程关闭会使 runtime 失效，下一次请求重新 bootstrap。`Waa/Ping` 属于官方生命周期控制面，不替代 snapshot 或业务请求 proof。

同一账户的 snapshot 必须串行。文本、内联二进制 Base64、Drive file ID 与工具 part 按 contents 顺序组成 binding prompt；Veo 使用视频提示词。worker 状态为 `starting`、`bootstrapping`、`ready`、`busy`、`closing`、`closed` 和 `failed`。

## 4. ListModels、CountTokens 与 GenerateContent 请求

### ListModels

请求正文：

```json
[]
```

响应根形状为 `[[<MODEL_ROW>, ...]]`。模型行字段：

| JSON 索引 | protobuf field | 内容 |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 2 | 3 | 版本 |
| 3 | 4 | 显示名称 |
| 4 | 5 | 描述 |
| 5 | 6 | 输入 token 上限 |
| 6 | 7 | 输出 token 上限 |
| 7 | 8 | 支持的方法 |
| 8 | 9 | 默认 temperature |
| 9 | 10 | 默认 topP |
| 10 | 11 | 默认 topK |
| 56 | 57 | 模型别名 |
| 64 | 65 | 主能力码 |
| 66 | 67 | TTS voice 列表 |
| 70 | 71 | Veo 配置 |
| 71 | 72 | thinking 默认配置 |
| 74 | 75 | 次能力码 |
| 75 | 76 | 图片宽高比码 |
| 76 | 77 | 图片输出分辨率码 |

能力码映射：

| 码 | 能力 | 码 | 能力 |
| ---: | --- | ---: | --- |
| 1 | chat model | 9 | code execution |
| 10 | function declarations | 12 | Google Search |
| 13 | URL Context | 20 | Veo route |
| 21 | image route | 25 | thinking |
| 26 | live route | 35 | thinking budget |
| 37 | speech route | 43 | media resolution |
| 47 | aspect ratio | 49 | output resolution |
| 52 | thinking level | 53 | music route |
| 54 | image search | 58 | Google Maps |
| 59 | private Interaction route | | |

未知能力码以 `capability_code_<N>` 或 `secondary_capability_code_<N>` 保留，不从模型名称推断能力。

图片与视频选项使用枚举码：

| 类型 | 码值映射 |
| --- | --- |
| 图片/视频宽高比 | `1=1:1`、`2=9:16`、`3=16:9`、`4=3:4`、`5=4:3`、`6=3:2`、`7=2:3`、`8=5:4`、`9=4:5`、`10=21:9`、`11=9:21`、`12=1:4`、`13=4:1`、`14=1:8`、`15=8:1` |
| 图片分辨率 | `1=1K`、`2=2K`、`3=4K`、`4=512` |
| 视频时长 | `1=5s`、`2=6s`、`3=7s`、`4=8s`、`5=4s` |
| 视频分辨率 | `1=720p`、`2=1080p`、`3=4k`、`4=368p`、`5=360p` |

Veo field 71 的宽高比、时长和分辨率分别位于子索引 `4`、`5`、`9`。TTS field 67 是 repeated voice row，每行索引 `0` 为 voice name。thinking field 72 的默认 level 位于子索引 `5`。

### CountTokens

纯文本且无 system：

```json
["models/<MODEL_ID>", [<CONTENT>, ...]]
```

含 system、inline data 或 Drive file：

```json
["models/<MODEL_ID>", null, ["models/<MODEL_ID>", [<CONTENT>, ...], null, null, null, <SYSTEM>]]
```

响应索引 `0` 是权威输入 token 数。其他槽按不透明协议字段保留。

### Content、Part 与 system

Content 形状：

```json
[[<PART>, ...], "user|model"]
```

客户端 tool result 使用 `user` role。Part 字段：

| JSON 索引 | protobuf field | 内容 |
| ---: | ---: | --- |
| 1 | 2 | 文本 |
| 2 | 3 | inline data `[mime, base64]` |
| 5 | 6 | Drive file `[fileId]` |
| 7 | 8 | executable code `[languageCode, code]` |
| 8 | 9 | code execution result `[outcomeCode, output]` |
| 10 | 11 | function call `[name, Struct, callId?]` |
| 11 | 12 | function result `[name, Struct, callId?]` |
| 12 | 13 | thought boolean |
| 14 | 15 | thought signature |

system instruction：

```json
[[[null, "<SYSTEM_TEXT>"]], "user"]
```

### GenerateContent

根消息字段：

| JSON 索引 | protobuf field | 内容 |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 1 | 2 | contents |
| 2 | 3 | safety settings |
| 3 | 4 | generation config |
| 4 | 5 | fresh WAA proof |
| 5 | 6 | system instruction |
| 6 | 7 | tools |
| 10 | 11 | 固定值 `1` |
| 13 | 14 | `[[null,null,<TIMEZONE>]]` |
| 14 | 15 | 用户 Cloud API key，免费网页链保持 `null` |

safety settings：

```json
[
  [null, null, 7, 5],
  [null, null, 8, 5],
  [null, null, 9, 5],
  [null, null, 10, 5]
]
```

generation config 字段：

| JSON 索引 | protobuf field | 内容 |
| ---: | ---: | --- |
| 1 | 2 | stop sequences |
| 3 | 4 | max output tokens |
| 4 | 5 | temperature |
| 5 | 6 | topP |
| 6 | 7 | topK |
| 7 | 8 | response MIME type |
| 8 | 9 | response schema |
| 13 | 14 | 固定值 `1` |
| 14 | 15 | response modalities：TEXT=`1`、IMAGE=`2`、AUDIO=`3` |
| 15 | 16 | speech config |
| 16 | 17 | thinking config `[1, budget?, null, level]` |
| 18 | 19 | seed |
| 26 | 27 | image config `[aspectRatio?, imageSize?]` |

thinking level 为 Low=`1`、Medium=`2`、High=`3`。JSON Schema type code 为 string=`1`、number=`2`、integer=`3`、boolean=`4`、array=`5`、object=`6`；schema 支持 format、description、nullable、enum、items、properties、required 和 field 23 `propertyOrdering`。

## 5. 增量流、思考、usage、来源与错误

`GenerateContent` 返回持续增长的 JSON+protobuf 根数组，根索引 `0` 是 repeated frames。帧结构：

| 路径 | 内容 |
| --- | --- |
| `$[0][frame][0]` | candidates |
| `$[0][frame][0][0][0]` | candidate content |
| `$[0][frame][0][0][1]` | finish reason code |
| `$[0][frame][0][0][6]` | citations |
| `$[0][frame][0][0][7]` | grounding metadata |
| `$[0][frame][2]` | usage |
| `$[0][frame][7]` | response ID |
| `$[0][frame][3]` 且 frame 0 为空 | interaction metadata |

candidate content 为 `[[parts...], "model"]`。Part 文本带 `part[12]=true` 时属于 reasoning summary，普通文本属于可见正文；`part[14]` 是 thought signature。签名可以附在文本、函数调用或独立空 Part 上，下一轮必须原样回传。

协议核心按网络顺序输出 `text`、`reasoning`、`tool_call`、`executable_code`、`code_execution_result`、`grounding`、`citation`、`media`、`thought_signature`、`usage`、`finish` 和 `error`。

### Grounding 与引用

grounding metadata 字段：

| JSON 索引 | 内容 |
| ---: | --- |
| 0 | search entry point `[renderedContent?, sdkBlob?]` |
| 1 | grounding chunks |
| 2 | grounding supports |
| 3 | retrieval metadata，动态分数位于子索引 1 |
| 4 | web search queries |
| 6 | Maps widget context token |

grounding chunk 的 oneof 索引 `0/1/2` 分别为 web、retrieved context、maps；内部字段依次为 URI、title、text、place ID。support 为 `[segment, chunkIndices, confidenceScores]`，segment 为 `[partIndex,startIndex,endIndex,text]`。candidate citations 的 entries 位于 metadata 索引 0，每项 URL 在索引 2、title 在索引 3。

Code Execution 的 language code 为 `0=LANGUAGE_UNSPECIFIED`、`1=PYTHON`。执行结果 outcome code 为 `0=OUTCOME_UNSPECIFIED`、`1=OUTCOME_OK`、`2=OUTCOME_FAILED`、`3=OUTCOME_DEADLINE_EXCEEDED`。

### Usage

完成帧 usage：

| 数组索引 | 语义 | 规范字段 |
| ---: | --- | --- |
| 0 | input tokens | `input_tokens` |
| 1 | visible output tokens | `output_tokens` |
| 2 | total tokens | `total_tokens` |
| 7 | tool tokens | `tool_tokens` |
| 9 | thought tokens | `reasoning_tokens` |

当完成帧省略 visible output tokens 时，服务使用同一账户调用 `CountTokens` 测量实际返回的 model content，并据此补全 output 与 total。完整 usage 直接按上游原值返回。

OpenAI 与 Anthropic 的输入统计为 input + tool，输出统计为 visible output + reasoning。Gemini 分别投影 `promptTokenCount`、`candidatesTokenCount`、`thoughtsTokenCount` 与 `totalTokenCount`。隐藏完整思考只使用服务端 thought tokens，不根据思考摘要估算。

### Finish 与错误

| code | reason | code | reason |
| ---: | --- | ---: | --- |
| 0 | unspecified | 1 | stop |
| 2 | max_tokens | 3 | safety |
| 4 | recitation | 5 | other |
| 6 | language | 7 | blocklist |
| 8 | prohibited_content | 9 | spii |
| 10 | malformed_function_call | 11 | image_safety |
| 12 | unexpected_tool_call | 13 | too_many_tool_calls |
| 14 | image_prohibited_content | 15 | image_other |
| 16 | no_image | 17 | image_recitation |

错误响应根形状为 `[null,[code,message,...]]`。协议核心保留 HTTP 状态、协议 code、message 与脱敏原始数组；公开适配器映射为 OpenAI、Anthropic 或 Gemini 错误对象。Chat、Responses、Anthropic Messages 与 Gemini GenerateContent 将媒体模型的普通文本作为文本结果输出；专用图片端点要求图片结果。HTTP/协议错误、缺失完成帧或明确 finish reason 形成失败。

## 6. 函数、Google 工具、Drive 与媒体

### 函数与 Google 工具

根 field 7 是 repeated Tool：

| 工具 | Tool 数组形状 |
| --- | --- |
| Function declarations | `[null, [[name, description?, schema?], ...]]` |
| Code Execution | `[[]]` |
| Google Search | `[null,null,null,[null,[searchTypes]]]`，searchTypes 索引 0 为 `[]` |
| Image Search | 同一 Search tool，searchTypes 索引 1 为 `[]` |
| URL Context | 8 槽数组，索引 7 为 `[]` |
| Google Maps | 11 槽数组，索引 10 为 `[]` |

函数 JSON Struct 使用 protobuf `Struct/Value` 数组：map 为 `[[[key,value],...]]`；Value oneof 索引 `0..5` 分别表示 null、number、string、bool、Struct、ListValue。对象键排序后编码。

函数参数和结构化输出 Schema 使用以下 protobuf fields：

| JSON Schema | Field | JSON Schema | Field |
| --- | ---: | --- | ---: |
| `type` | 1 | `format` | 2 |
| `description` | 3 | `nullable` | 4 |
| `enum` | 5 | `items` | 6 |
| `properties` | 7 | `required` | 8 |
| `minProperties` | 9 | `maxProperties` | 10 |
| `minimum` | 11 | `maximum` | 12 |
| `minLength` | 13 | `maxLength` | 14 |
| `pattern` | 15 | `example` | 16 |
| `oneOf` | 17 | `anyOf` | 18 |
| `allOf` | 19 | `not` | 20 |
| `maxItems` | 21 | `minItems` | 22 |
| `propertyOrdering` | 23 | | |

公开协议输入中的 `$schema`、`default`、`additionalProperties` 和 `exclusiveMinimum` 在编码前移除；其余未知 Schema 字段返回参数错误。`type: [T, "null"]` 以及 `anyOf`、`oneOf` 中的 null 分支映射为 `nullable`，其余联合分支保持原顺序。缺少 `type` 的组合 Schema 使用首个声明类型的分支作为根类型，并保留完整组合分支。

AI Studio 网页协议使用自动函数调用：auto 请求只携带根 field 7 的函数声明，由模型决定是否调用；none 省略 tools。客户端工具选择映射如下：

| 公开协议 | 接受 | 返回 400 |
| --- | --- | --- |
| OpenAI Chat / Responses | 默认、`auto`、`none` | `required`、named function |
| Anthropic | 默认、`auto`、`none` | `any`、named `tool` |
| Gemini | 默认、`AUTO`、`NONE` | `ANY`、`allowedFunctionNames` |

函数调用响应 Part 为 `[name, Struct, callId?]`；下一轮 function result 使用同一形状并原样带回 thought signature。

### Drive 上传与文件 Part

```text
GenerateAccessToken ["users/me"]
  -> response ["<BEARER_TOKEN>"]
  -> POST Drive multipart/related
       part 1: {"mimeType":"<MIME>","name":"<NAME>"}
       part 2: raw bytes
  -> {"id":"<FILE_ID>"}
  -> GenerateContent Part field 6 ["<FILE_ID>"]
```

Drive token、上传、提示引用和下载使用创建账户固定出口。文件 ID 与账户绑定写入 `runtime-state.json`；同一请求内的多个 Drive file 必须属于同一账户。

### Nano、TTS 与 Lyria

三类媒体复用 `GenerateContent`：

| 路由 | generation config | 响应 |
| --- | --- | --- |
| Nano image | modalities `[2]`，image config `[aspectRatio?, imageSize?]` | Part field 3 `[mime, base64]` |
| TTS | modalities `[3]`，speech config | Part field 3 音频 chunk |
| Lyria | modalities `[3]` | Part field 3 音频 chunk |

单声音 speech config 为 `[[[voiceName]]]`。多说话人 speech config 为 `[null,null,[null,[[speaker,[[voiceName]]],...]]]`。相邻且 MIME 相同的音频 Part 按到达顺序拼接。图片宽高比、图片分辨率与 TTS voice 必须来自当前模型能力选项。

### Veo

`GenerateVideo` 使用 8 槽数组，WAA proof 位于 field 8：

```json
[
  "models/<MODEL_ID>",
  "<PROMPT>",
  [1, "<ASPECT_RATIO>", ["<SECONDS>"], "<RESOLUTION>"],
  ["<IMAGE_MIME>", "<BASE64>"] | null,
  ["<DRIVE_FILE_ID>"] | null,
  null,
  null,
  "<WAA_PROOF>"
]
```

起始帧只能选择 inline image 或 Drive file。创建响应 field 1 是 operation ID。轮询请求为 `["<OPERATION_ID>"]`；轮询响应 field 1 是 done，产物 Drive file ID 位于 `$[1][0][0][0]`。operation 与结果 file 均绑定创建账户，再通过 Drive bearer 下载媒体。count、宽高比、秒数和分辨率按实时模型 field 71 校验。

## 7. 公开端点、状态映射与实现约束

| 协议 | 端点 |
| --- | --- |
| OpenAI Chat | `GET /v1/models`、`POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| OpenAI 媒体 | `POST /v1/images/generations`、`POST /v1/audio/speech`、`POST /v1/videos`、`GET /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、`:predictLongRunning`、`GET /v1beta/operations/{id}` |

公开 `/v1` 与 `/v1beta` 接受 `Authorization: Bearer`、`X-API-Key`、`X-Goog-API-Key` 或 `?key=`。配置为空时关闭本地 API key 校验。`/api` 控制面仅允许 loopback；`GET /health` 返回管理进程健康状态。

| 控制能力 | 端点 |
| --- | --- |
| 状态与模型 | `GET /api/status`、`GET /api/models` |
| 生成服务 | `POST /api/control/start`、`POST /api/control/stop` |
| 账户 | `GET /api/accounts`、`POST /api/accounts`、`PUT /api/accounts/{id}`、`DELETE /api/accounts/{id}` |
| 账户认证 | `POST /api/accounts/{id}/login`、`POST /api/accounts/{id}/verify` |
| 配置 | `GET /api/config`、`PUT /api/config` |
| 冷却与请求 | `GET /api/quota`、`GET /api/requests`、`POST /api/requests/{id}/cancel` |
| 日志与事件 | `DELETE /api/logs`、`GET /api/events` |

管理进程启动后生成服务处于停止状态。`POST /api/control/start` 刷新可用账户的模型目录并开启生成数据面；没有可用账户时返回 `400 account_required`。`POST /api/control/stop` 取消活动生成请求、关闭 WAA worker，并保持管理页面、控制端点、健康检查和模型查询可用。停止期间生成与计数请求返回 `503 service_stopped`。进程退出后管理页面和全部端点一并关闭。

`GET /v1/models` 默认返回 OpenAI model list；请求携带 `Anthropic-Version` 时返回 Anthropic model list。Gemini 模型端点使用 `models/<ID>` 名称。private Interaction 模型只保留在管理端实时目录，不进入公开模型列表。多个账户出现同一模型时，generation methods 与能力选项取并集，输入和输出 token limit 取正数最小值，保证轮询到任一合格账户时都不超过公开上限。

主要请求合同：

| 端点 | 必需字段 | 主要结果 |
| --- | --- | --- |
| `/v1/chat/completions` | `model`、非空 `messages` | Chat completion 或增量 chunk |
| `/v1/responses` | `model`、`input` | Response object 或 `response.*` 事件 |
| `/v1/messages` | `model`、非空 `messages`、`max_tokens` | Anthropic message 或 message 事件 |
| `:generateContent` / `:streamGenerateContent` | 非空 `contents` | Gemini candidates、usage 与 grounding metadata |
| `/v1/images/generations` | `model`、`prompt`，`n` 只能为 `1` | `b64_json` 或 data URL |
| `/v1/audio/speech` | `model`、`input` | WAV、PCM 或 MP3 body |
| `/v1/videos` | `model`、`prompt` | 长任务对象，随后轮询并下载内容 |

流式端点统一使用 `text/event-stream`，每个 SSE frame 以空行结束：

| 协议 | 流事件 |
| --- | --- |
| OpenAI Chat | `data: <chat.completion.chunk>`，终态后发送 `data: [DONE]` |
| OpenAI Responses | `event: <response.*>` 与对应 JSON data，终态为 `response.completed` |
| Anthropic | `event: <message/content_block_*>` 与对应 JSON data，终态为 `message_stop` |
| Gemini | `data: <GenerateContentResponse>` 增量，最后一帧携带 finish reason 与 usage |

公开适配规则：

| 规范事件 | OpenAI Chat | Responses | Anthropic | Gemini |
| --- | --- | --- | --- | --- |
| text | message/content delta | output_text | text block | candidate text Part |
| reasoning | `reasoning_content` | reasoning summary | thinking block | thought Part |
| function | `tool_calls` | function_call item | tool_use block | functionCall Part |
| function result | tool message | function_call_output | tool_result | functionResponse Part |
| code execution | 可读 Markdown | code_interpreter item | text block | executableCode/result Part |
| grounding/citation | annotations | output annotations | text sources | groundingMetadata |
| media | data URL/媒体端点 | output content | content block | inlineData Part |
| usage | prompt/completion/total | input/output/total | input/output | prompt/candidates/thoughts/total |

OpenAI Chat 使用 Markdown data URL 承载生成图片；客户端把 assistant `message.content` 回传下一轮时，适配器将其中的图片恢复为 inline data Part，保留图片多轮上下文。

OpenAI Responses 的 `previous_response_id` 在进程内保存最多 256 个响应节点并重建完整 contents；重启后客户端重新提交完整上下文。Drive 与 Veo 资源绑定持久化到磁盘。

模型不存在返回 `model_not_found`/`NOT_FOUND`，本地参数错误返回 `invalid_request`/`INVALID_ARGUMENT`，账户暂时不可用返回服务错误，上游 HTTP 与协议错误保留供应商 message。客户端取消必须关闭上游 reader并释放账户租约；只有收到完成帧才发送正常终态。

实现不公开 cached content、batch、Bidi/Live 或 private Interactions 占位端点。模型目录列出实时并集及其 generation methods，别名只接受 field 57 的显式声明；请求的模型或方法不在目录时返回对应错误，不静默降级。WAA 仍依赖每账户 Camoufox 官方 VM；业务协议、流式解码和公开 API 均由 Go 实现。

认证文件、Cookie、SAPISID、authorization、proof、OAuth 材料、账户标识和完整原始帧不得写入普通日志或公开提交。
