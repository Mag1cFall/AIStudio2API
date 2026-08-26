# Google AI Studio 私有协议

本文定义 AIStudio2API 使用的 Google AI Studio 私有协议、认证状态、WAA 运行时、JSON+protobuf 数组、增量事件、工具与媒体链。模型方法、限制和能力由账户的实时 `ListModels` 返回，公开 API 将原始结构投影为规范事件和兼容响应。

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
| `x-aistudio-g1-tier` | `GetAiStudioBenefitTier` 返回值映射为 `TIER0`、`TIER1` 或 `TIER2` |
| `x-goog-ext-519733851-bin` | 当前官网请求动态值 |
| `authorization` | 三段 SAPISID 签名 |
| `cookie` | 当前账户对目标 RPC 可见的 Cookie |
| `origin`、`referer` | `https://aistudio.google.com` |
| `accept-language` | 账户 locale |

请求头 `x-goog-api-key` 是 AI Studio 页面使用的动态公共值，与用户创建的 Google Cloud API key 不同；免费网页链仍依赖 Cookie、SAPISID 签名和 WAA proof。

MakerSuite 与 Drive 业务请求使用和 Camoufox 对齐的 Firefox TLS、HTTP/2 与请求头顺序；WAA VM、fresh proof 和隔离登录由同一账户的 Camoufox 环境完成。

JSON+protobuf 使用数组表示 protobuf message。数组索引从 `0` 开始，protobuf field 从 `1` 开始，因此 field `N` 对应索引 `N-1`。Google 响应允许省略空槽并形成 `[,value]`；解码器先把省略槽规范化为 `null`，再从完整 JSON 根值中提取 repeated message。HTTPS chunk 仅提供字节序列，业务事件边界由数组结构确定。

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
| `GetAiStudioBenefitTier` | 账户权益枚举与 tier 请求头 |
| `ListRecentApplets` | 最近 Applet |
| `ListPrompts` | 提示词目录 |
| `GetUserRestrictions` | 账户限制 |

管理进程启动时加载账户并准备公共头。`POST /api/control/start` 刷新实时模型目录、预热 WAA Worker并启用数据面，业务能力随后按需调用对应 RPC。

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

MakerSuite 响应的 `Set-Cookie` 在响应头到达时基于账户最新 `storage-state.json` 单写合并并原子写回。签名、Cookie 选择和过期判断均以请求时重新读取的账户状态为准。

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

Chrome 导入状态在 `storage-state.json` 的 `aistudio2api` 扩展中保存来源、Gaia ID、refresh token 与 wrapped binding key。普通或受保护 RPC 首次返回 HTTP `401` 时，服务在同一账户出口续签 Cookie、使动态头失效、关闭该账户 WAA runtime，并重放一次。HTTP `403` 保留为权限错误；首个上游语义事件前可以切换到下一个同能力账户。隔离 Camoufox 登录和外部 storage state 不携带 Chrome OAuth 扩展。

### 账户持久状态

| 文件 | 内容 |
| --- | --- |
| `auth/<Google 邮箱>/account.json` | 邮箱、enabled、proxy、locale、timezone |
| `auth/<Google 邮箱>/storage-state.json` | Cookie、localStorage 和可选 Chrome 续签材料 |
| `auth/<Google 邮箱>/camoufox-fingerprint.json` | 账户固定的 navigator、屏幕、字体、语言、地区和时区配置 |
| `auth/<Google 邮箱>/runtime-state.json` | 账户权益、实测模型资格、冷却与 Drive/Veo 资源绑定 |
| `auth/.leases/<Google 邮箱>.lock` | 同一账户目录的跨进程占用锁 |
| `[用户缓存]/AIStudio2API/runtime-leases/<Google 邮箱>.lock` | 当前电脑上该邮箱的 WAA Worker 占用锁 |

Google 邮箱的小写形式同时作为账户目录、管理页面标识和日志来源。新账户的 locale 与 timezone 读取当前电脑设置，管理页面使用浏览器语言和 IANA 时区；CLI 使用操作系统语言和时区。初始化、WAA、MakerSuite、OAuth 续签和 Drive 使用账户固定代理。locale 同时设置 navigator language、Accept-Language 与地区，timezone 设置浏览器时区；重新登录和 WAA runtime 复用同一账户指纹。同一电脑上的多个进程按邮箱共享 WAA runtime lease，调度器只会为未被占用的邮箱创建 Worker。

调度器按模型方法、账户权益和实测模型资格筛选，优先选择已经成功调用目标模型的预热账户，再按目标模型最近一次真实首事件耗时排序。Code 7 将当前账户模型组合记为 `denied`，该账户的其他模型继续调度；重新登录、账户验证或权益变化会重新建立资格。常驻 Worker 优先覆盖可调用模型更多的账户。预热数量低于上限时提升合格待机账户，预热账户均忙时等待并发槽位。同账号 WAA proof 串行生成，已准备的 MakerSuite HTTP 请求并发执行；首个活动请求获取 `.leases` 文件锁，最后一个释放。Drive file、Veo operation 与产物 file 始终使用创建账户。

### 账户权益

`GetAiStudioBenefitTier` 请求为 `[]`，响应 field 1 的枚举映射如下：

| 值 | 权益 | RPC Header |
| ---: | --- | --- |
| 0 | Free | 无 |
| 1 | Pro | `X-AIStudio-G1-Tier: TIER1` |
| 2 | Ultra | `X-AIStudio-G1-Tier: TIER2` |
| 3 | Plus | `X-AIStudio-G1-Tier: TIER0` |

官网为 `GenerateContent`、`CountTokens`、Interaction、Code Assistant 与 Veo RPC 注入该 header。模型 field 83 描述访问方式：`1` 为付费 API key，`3` 为 Pro/Ultra 订阅，`4` 为 Ultra 订阅。浏览器账户池使用订阅路径，公开模型目录只合并至少一个账户权益可达的模型。

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

`program` 与 challenge 属于当前 Create 生命周期，interpreter 按 hash 缓存。proof 绑定当前 prompt 摘要与 VM 内部状态，每个请求生成新的 proof。

生成服务启动时按配置的常驻数与启动并发数预热账户 WAA runtime：

1. Go 启动隔离、无头 Camoufox，并通过原生 WebDriver BiDi 建立 session
2. 写入账户 Cookie 与 localStorage，从实时目录依次尝试 `gemini-flash-latest`、`gemini-3.7-flash` 进入新对话；`TEMPORARY_CHAT=true` 时 URL 携带 `temporary=true`
3. 定位页面 bundle 中调用 `.snapshot({` 且包含 `content` 的官方高层函数
4. 为官网 `GenerateContent` 安装 `beforeRequestSent` BiDi 拦截，再填入唯一 bootstrap prompt 并执行官网 Run
5. 页面调用官方 snapshot 时保存 WAA service；请求进入拦截阶段后保存动态头并通过 `network.failRequest` 在浏览器内终止
6. 后续业务请求串行调用同一 service 获取 fresh proof
7. `GenerateContent` 写入 field 5，`GenerateVideo` 写入 field 8，正文由 Go HTTP transport 发送

Camoufox 负责官方 VM 初始化与 WAA proof；Go 负责业务请求、增量解码和公开 API。运行期依赖 Go 与 Camoufox。官方 VM 初始化形状为：

```javascript
initialize(program, ready, true, environment, signalLists, persistentState, false, loggers)
```

VM 生命周期参数为 `43,200,000ms`，检查间隔为 `300,000ms`。页面生命周期中断、snapshot 错误、计时器到期、认证续签或进程关闭会使 runtime 失效，下一次请求重新 bootstrap。`Waa/Ping` 维护官方生命周期，业务请求 proof 由 snapshot 生成。

Bootstrap 使用的 `GenerateContent` 在发往上游前终止，模型输出量为零。临时对话关闭预热页的自动保存。

同一账户的 snapshot 必须串行。GenerateContent 的 binding prompt 按 contents 和 parts 的原顺序展开，再以单个空格连接：

| Part | 写入 binding prompt 的值 |
| --- | --- |
| text | 原始文本 |
| inline data | 原始二进制的标准 Base64 |
| external media | 空字符串 |
| Drive file | file ID |
| function、function result、code、thought signature | 空字符串 |

binding prompt 的输入域为 contents parts；Veo 使用视频提示词。prompt 的 SHA-256 小写十六进制摘要交给官方 snapshot，返回值是 `!` 开头的字符串；编码器随后把 proof 写入目标 protobuf field，原请求的其他槽位保持不变。worker 状态为 `starting`、`bootstrapping`、`ready`、`busy`、`closing`、`closed` 和 `failed`。

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
| 77 | 78 | Paid 标记，值 `2` 时显示 Paid |
| 82 | 83 | 模型访问方式 |

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

未知能力码按原值保留为 `capability_code_<N>` 或 `secondary_capability_code_<N>`。

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

含 system、inline data、外部媒体或 Drive file：

```json
["models/<MODEL_ID>", null, ["models/<MODEL_ID>", [<CONTENT>, ...], null, null, null, <SYSTEM>]]
```

请求形状选择：

| 条件 | 根结构 | GenerateContent 子消息位置 |
| --- | --- | --- |
| 纯文本 contents | `[model, contents]` | — |
| system instruction | `[model, null, generate]` | `$[2][5]` |
| function / Google tools | `[model, null, generate]` | `$[2][6]` |
| inline data、external media、Drive、function call/result、code result | `[model, null, generate]` | `$[2][1]` |

包含 system 与函数声明的完整计数请求：

```json
[
  "models/gemini-3.6-flash",
  null,
  [
    "models/gemini-3.6-flash",
    [
      [
        [[null, "调用 ping 检查服务"]],
        "user"
      ]
    ],
    null,
    null,
    null,
    [
      [[null, "你是诊断助手"]],
      "user"
    ],
    [
      [null, [["ping", "检查服务"]]]
    ]
  ]
]
```

响应为单元素数组：

```text
[<INPUT_TOKEN_COUNT>]
```

索引 `0` 是权威输入 token 数。其他槽按不透明协议字段保留。

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
| 6 | 7 | 外部媒体 `[mime, url]` |
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

生成参数校验：

| 参数 | 默认来源 | 有效值 |
| --- | --- | --- |
| max output | ListModels field 7 | `1..model.outputTokenLimit` |
| temperature | ListModels field 9 | `0..2` |
| topP | ListModels field 10 | `0..1` |
| topK | ListModels field 11 | 非负整数 |
| thinking level | ListModels field 72 | Low=`1`、Medium=`2`、High=`3`、Minimal=`4` |
| thinking budget | 请求值 | 模型能力码包含 thinking budget |

response modalities：

| 输出 | wire | 默认路由 |
| --- | --- | --- |
| text | `[1]` | chat |
| image | `[2]` | image route |
| image + text | `[2,1]` | 显式组合请求 |
| audio | `[3]` | speech / music route |

AUDIO 采用独立输出模态。JSON Schema type code 为 string=`1`、number=`2`、integer=`3`、boolean=`4`、array=`5`、object=`6`；schema 支持 format、description、nullable、enum、items、properties、required 和 field 23 `propertyOrdering`。

以下最小组合请求包含 system、文本、函数声明、generation config、WAA proof 与账户时区。连续空槽保持在同行，字段含义查上表：

```json
[
  "models/gemini-3.6-flash",
  [
    [
      [[null, "调用 ping 检查服务"]],
      "user"
    ]
  ],
  [
    [null, null, 7, 5],
    [null, null, 8, 5],
    [null, null, 9, 5],
    [null, null, 10, 5]
  ],
  [null, null, null, 512, 0.2, 0.95, 40, null, null, null, null, null, null, 1],
  "!WAA_PROOF",
  [
    [[null, "你是诊断助手"]],
    "user"
  ],
  [
    [null, [["ping", "检查服务"]]]
  ],
  null,
  null,
  null,
  1,
  null,
  null,
  [[null, null, "Asia/Taipei"]]
]
```

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

传输正文是一个 JSON 根值，网络 chunk 提供字节；解码器在 `$[0]` 中每出现一个完整 repeated frame 时立即消费该 frame。每个内容帧包含一个 candidate，candidate content 为 `[[parts...], "model"]`。完成帧可以同时携带最后一组 Part、usage、response ID 和 finish reason，根数组解析完成后结束读取。

从 `$[0]` 提取出的文本帧：

```json
[
  [
    [
      [
        [[null, "42"]],
        "model"
      ]
    ]
  ]
]
```

随后到达的完成帧包含 `finish=1`、usage 和 response ID：

```json
[
  [[null, 1]],
  null,
  [27, 1, 28, null, null, null, null, 0, null, 0],
  null,
  null,
  null,
  null,
  "response_01"
]
```

高频路径速查：

| 结构 | JSONPath | 内容 |
| --- | --- | --- |
| GenerateContent | `$[0]` | model |
| GenerateContent | `$[1]` | contents |
| GenerateContent | `$[3]` | generation config |
| GenerateContent | `$[4]` | WAA proof |
| GenerateContent | `$[5]` | system instruction |
| GenerateContent | `$[6]` | tools |
| GenerateContent | `$[13][0][2]` | timezone |
| response root | `$[0][frame]` | repeated frame |
| candidate content | `$[0][frame][0][0][0]` | `[[parts], "model"]` |
| candidate finish | `$[0][frame][0][0][1]` | finish reason code |
| Part text | `...parts[part][1]` | text |
| Part inline data | `...parts[part][2]` | `[mime, base64]` |
| Part function call | `...parts[part][10]` | `[name, Struct, callId?]` |
| Part thought | `...parts[part][12]` | boolean |
| Part signature | `...parts[part][14]` | signature |
| frame usage | `$[0][frame][2]` | usage array |
| frame response ID | `$[0][frame][7]` | response ID |

Part 文本带 `part[12]=true` 时属于 reasoning summary，普通文本属于可见正文；`part[14]` 是 thought signature。签名可以附在文本、函数调用或独立空 Part 上，下一轮必须原样回传：

| 公开协议 | 签名输入 | 签名输出 |
| --- | --- | --- |
| OpenAI Chat | assistant tool call 的 `extra_content.google.thought_signature` | tool call 的同名扩展字段 |
| OpenAI Responses | `reasoning.encrypted_content` 紧邻后续 `function_call` | reasoning item 的 `encrypted_content` |
| Anthropic | `thinking` 或 `redacted_thinking` block 的 `signature` | thinking block 的 `signature` |
| Gemini | 数据 Part 或独立 Part 的 `thoughtSignature` | Part 的 `thoughtSignature` |

reasoning summary 是服务端返回的摘要文本。thought signature 作为下一轮请求的协议状态字段原样回传。

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

包含 web chunk、maps chunk、正文 support、检索分数和查询词的 raw metadata：

```json
[
  ["<div>Search results</div>", "SDK_BLOB"],
  [
    [["https://example.com/gemini", "Gemini Guide", "Protocol overview"]],
    [null, null, ["https://maps.google.com/?cid=1", "Google Taipei", "", "ChIJ_demo"]]
  ],
  [
    [[0, 0, 12, "Gemini Guide"], [0], [0.98]]
  ],
  [null, 0.91],
  ["Gemini AI Studio protocol"],
  null,
  "MAPS_WIDGET_CONTEXT_TOKEN"
]
```

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

完整 usage 直接按上游原值返回。完成帧省略 visible output tokens 时，服务按上游 total 与其余分类字段恢复该值。完整 usage 缺失时，内置 Gemini SentencePiece tokenizer 在本地统计可观测输入、工具声明、reasoning summary 和实际输出。

OpenAI 与 Anthropic 的输入统计为 input + tool，输出统计为 visible output + reasoning。Gemini 分别投影 `promptTokenCount`、`candidatesTokenCount`、`thoughtsTokenCount` 与 `totalTokenCount`。隐藏思考用量来自上游 usage field 9；本地 fallback 统计服务端返回的 reasoning summary。

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
| 其他整数 | `provider_<code>` | | |

错误响应根形状为 `[null,[code,message,...]]`。协议核心保留 HTTP 状态、协议 code 与 message；公开适配器映射为 OpenAI、Anthropic 或 Gemini 错误对象。Chat、Responses、Anthropic Messages 与 Gemini GenerateContent 将媒体模型的普通文本作为文本结果输出；专用图片端点要求图片结果。HTTP/协议错误或缺失完成帧形成失败；上游 finish reason 作为正常终态保留并映射到各公开协议。

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

公开工具名称归一化后再生成上述 Tool 数组：

| AI Studio 工具 | OpenAI Chat / Responses | Anthropic | Gemini |
| --- | --- | --- | --- |
| function declarations | `function` | 空 type 或 `custom` | `functionDeclarations` |
| Google Search | `web_search`、`web_search_preview` | `web_search*` | `googleSearch`、`googleSearchRetrieval` |
| Image Search | `image_search` | — | `imageSearch` |
| URL Context | `url_context` | `web_fetch*`、`url_context*` | `urlContext` |
| Code Execution | `code_interpreter` | `code_execution*` | `codeExecution` |
| Google Maps | `google_maps` | `google_maps*` | `googleMaps` |

根 field 7 按请求声明逐项编码，函数声明和各类 Google 工具分别占用独立 Tool entry。模型的工具范围取自实时能力码。

函数 JSON Struct 使用 protobuf `Struct/Value` 数组：map 为 `[[[key,value],...]]`；Value oneof 索引 `0..5` 分别表示 null、number、string、bool、Struct、ListValue。对象键排序后编码。

例如以下函数参数：

```json
{
  "city": "Taipei",
  "days": 2,
  "metric": true,
  "note": null,
  "units": ["C", "F"]
}
```

编码后的 Struct 为：

```json
[
  [
    ["city", [null, null, "Taipei"]],
    ["days", [null, 2]],
    ["metric", [null, null, null, true]],
    ["note", [0]],
    [
      "units",
      [
        null,
        null,
        null,
        null,
        null,
        [[[null, null, "C"], [null, null, "F"]]]
      ]
    ]
  ]
]
```

完整 function call Part 的关键槽位为：

```json
[
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  null,
  ["multiply", [[["a", [null, 21]], ["b", [null, 2]]]], "call_01"],
  null,
  null,
  null,
  "!THOUGHT_SIGNATURE"
]
```

其中 Part 索引 `10` 保存 function call，索引 `14` 保存 thought signature。

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

Schema 归一化规则：

| 输入结构 | 编码结果 |
| --- | --- |
| `$schema`、`default`、`additionalProperties`、`exclusiveMinimum` | 从 wire schema 中省略 |
| `type: [T, "null"]` | 根类型 `T` 与 `nullable=true` |
| `anyOf` / `oneOf` 的 null 分支 | 移除 null 分支并设置 `nullable=true` |
| 多个非 null `type` | 首项作为根类型，完整类型集合写入 `anyOf` |
| 组合 Schema 缺少根 `type` | 首个带类型的分支作为根类型 |
| 其他 Schema 字段 | 返回 `400 invalid_request` / `INVALID_ARGUMENT` |

AI Studio 网页协议使用自动函数调用：auto 请求只携带根 field 7 的函数声明，由模型决定是否调用；none 省略 tools。客户端工具选择映射如下：

| 公开协议 | 接受 | 返回 400 |
| --- | --- | --- |
| OpenAI Chat / Responses | 默认、`auto`、`none` | `required`、named function |
| Anthropic | 默认、`auto`、`none` | `any`、named `tool` |
| Gemini | 默认、`AUTO`、`NONE` | `ANY`、`allowedFunctionNames` |

函数调用响应 Part 为 `[name, Struct, callId?]`；下一轮 function result 使用同一形状并原样带回 thought signature。公开协议的 tool result 只有 call ID 时，实现从同一 contents 链的先前 function call 恢复函数名，查找失败返回参数错误。函数参数和结果使用 JSON object，标量或数组结果封装为 `{"result":<VALUE>}`。

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
| 冷却与请求 | `GET /api/cooldowns`、`GET /api/requests`、`POST /api/requests/{id}/cancel` |
| 日志与事件 | `DELETE /api/logs`、`GET /api/events` |

运行状态机：

```text
process start
  -> control plane ready
  -> STOPPED

POST /api/control/start
  -> LAUNCHING
  -> refresh account model catalogs with up to 5 concurrent accounts
  -> prewarm up to WARM_WORKER_LIMIT workers
     with WARM_STARTUP_CONCURRENCY bootstraps
  -> first worker ready
  -> RUNNING
  -> continue remaining worker prewarm in background

request
  -> match model + method
  -> acquire one PER_ACCOUNT_CONCURRENCY slot
  -> prepare WAA proof
  -> send MakerSuite RPC
  -> stream frames
  -> release slot

POST /api/control/stop
  -> cancel launch or active requests
  -> close WAA workers
  -> STOPPED
```

`stopped` 状态下生成与计数端点返回 `503 service_stopped`。模型路由从支持目标模型与方法的 ready 账户中选择可用预热账户，并使用真实请求记录的目标模型首事件耗时排序；需要时启动合格待机账户。无可用账户时返回 `400 account_required`。

模型目录投影：

| 规则 | 结果 |
| --- | --- |
| OpenAI | `GET /v1/models` 返回 OpenAI model list |
| Anthropic | `GET /v1/models` 携带 `Anthropic-Version` 时返回 Anthropic model list |
| Gemini | 模型名称使用 `models/<ID>` |
| 多账户同模型 | generation methods 与能力选项取并集 |
| 多账户 token limit | 输入和输出上限分别取正数最小值 |
| 模型别名 | 来自 ListModels field 57 |
| 请求匹配 | model ID/alias、method、账户权益与实测资格同时命中后进入账户调度 |

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

四套生成入口共享同一规范请求，输入映射如下：

| 能力 | OpenAI Chat | OpenAI Responses | Anthropic | Gemini |
| --- | --- | --- | --- | --- |
| system | `system` / `developer` messages | `instructions` 和 system/developer message items | `system` 字符串或 text blocks | `systemInstruction` text parts |
| text | 字符串或 text content part | 字符串、message item | 字符串或 text block | Part `text` |
| image/document | Base64 data URL、`file_id` | `input_image`、`input_file` | base64 source 或 URL source | `inlineData`、`fileData` |
| audio input | `input_audio` Base64 | message content 中的 `input_audio` | base64 document source | `inlineData` |
| YouTube | `video_url` / `input_video` | `input_video` | URL source | `fileData.fileUri` |
| function call | assistant `tool_calls` | `function_call` item | `tool_use` block | `functionCall` Part |
| function result | tool message | `function_call_output` item | `tool_result` block | `functionResponse` Part |
| structured output | `response_format` | `text.format` | — | `responseMimeType` 与 response schema |
| thinking | `reasoning_effort` 或 `reasoning.effort` | `reasoning.effort` | `thinking.budget_tokens`、`output_config.effort` | `thinkingConfig` |

生成参数映射：

| 参数 | 规则 |
| --- | --- |
| OpenAI max tokens | `max_completion_tokens` 优先于 `max_tokens` |
| Anthropic max tokens | `max_tokens` 映射 generation config field 4 |
| Gemini max tokens | `maxOutputTokens` 映射 generation config field 4 |
| temperature / topP / topK / seed | 映射 generation config fields 5 / 6 / 7 / 19 |
| stop sequence | 映射 generation config field 2 |
| structured output | MIME type 映射 field 8，Schema 映射 field 9 |
| frequency / presence penalty | `0` 采用 AI Studio 默认值，非零值返回 `400 invalid_request` |
| Responses `parallel_tool_calls` | 写入响应合同元数据，函数调用仍采用 AI Studio auto 模式 |

流式端点统一使用 `text/event-stream`，每个 SSE frame 以空行结束：

| 协议 | 首事件 | 内容序列 | usage | 终止事件 |
| --- | --- | --- | --- | --- |
| OpenAI Chat | assistant role chunk | chat completion delta | `include_usage=true` 时位于 finish chunk 之后 | `data: [DONE]` |
| OpenAI Responses | `response.created`、`response.in_progress` | output item / content part / delta / done | 完成 response 的 `usage` | `response.completed` 或 `response.incomplete` |
| Anthropic | `message_start` | `content_block_start`、delta、`content_block_stop` | `message_delta.usage` | `message_stop` |
| Gemini | candidate Part | `GenerateContentResponse` 增量 | 最后一帧 `usageMetadata` | 最后一帧 finish reason |

连续 10 秒没有上游语义事件时，四套流式协议发送 SSE 注释帧 `: ping` 并立即 flush。OpenAI Chat 先发送 assistant role chunk，Responses 先发送 `response.created` 与 `response.in_progress`，Anthropic 先发送 `message_start`；这些起始事件在账户调度期间即可到达客户端。Gemini 的首个帧来自上游语义事件或 `: ping`。

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

用户文本中的 `youtu.be/<ID>`、`youtube.com/watch?v=<ID>`、`/shorts/<ID>`、`/live/<ID>` 和 `/embed/<ID>` 会转换为 `video/*` 外部媒体 part，并从用户 text part 中移除；重复 URL 合并为一个附件。OpenAI `video_url`/`input_video`、Anthropic URL source 与 Gemini `fileData.fileUri` 使用相同的外部媒体编码。

OpenAI Responses 的 `previous_response_id` 在进程内保存最多 256 个响应节点并重建完整 contents；重启后客户端重新提交完整上下文。Drive 与 Veo 资源绑定持久化到磁盘。

模型、参数、账户与上游错误按下方状态表投影。客户端取消会关闭上游 reader并释放账户租约。

错误对象与状态语义：

| 情况 | HTTP | OpenAI | Anthropic | Gemini |
| --- | ---: | --- | --- | --- |
| 参数、Schema、tool choice 无效 | 400 | `invalid_request` | `invalid_request_error` | `INVALID_ARGUMENT` |
| 本地 API key 无效 | 401 | `invalid_api_key` | `authentication_error` | `UNAUTHENTICATED` |
| 模型或方法不存在 | 404 | `model_not_found` | `not_found_error` | `NOT_FOUND` |
| 上游拒绝权限 | 403 | `upstream_error` | `permission_error` | `PERMISSION_DENIED` |
| 上游配额或限流 | 429 | `upstream_error` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| 生成服务已停止 | 503 | `service_stopped` | `api_error` | `UNAVAILABLE` |
| 请求期限到期 | 504 | `upstream_error` | `api_error` | `DEADLINE_EXCEEDED` |
| 传输、Content-Type、解码或缺失终态 | 502 | `upstream_error` | `api_error` | `INTERNAL` |

错误对象 raw body：

**OpenAI Chat / Responses**

```json
{
  "error": {
    "message": "upstream response ended before finish frame",
    "type": "api_error",
    "code": "upstream_error"
  }
}
```

**Anthropic**

```json
{
  "type": "error",
  "error": {
    "type": "api_error",
    "message": "upstream response ended before finish frame"
  }
}
```

**Gemini**

```json
{
  "error": {
    "code": 502,
    "message": "upstream response ended before finish frame",
    "status": "INTERNAL"
  }
}
```

已开始流式响应后的终止原文：

```text
# OpenAI Chat
data: {"error":{"message":"...","type":"api_error","code":"upstream_error"}}

# OpenAI Responses
event: response.failed
data: {"response":{"id":"resp_...","object":"response","status":"failed","error":{"code":"upstream_error","message":"..."}}}

# Anthropic
event: error
data: {"type":"error","error":{"type":"api_error","message":"..."}}

# Gemini
data: {"error":{"code":502,"message":"...","status":"INTERNAL"}}
```

MakerSuite 错误解析：

| 来源 | 路径 | 公开结果 |
| --- | --- | --- |
| HTTP status | response status | 保留原状态码 |
| protocol code | `$[1][0]` | 映射到协议 error code/type/status |
| protocol message | `$[1][1]` | 写入公开错误的 `message` |
| 原始形状 | `[null,[code,message,...]]` | 解析后进入统一错误事件 |

请求生命周期：

| 阶段 | HTTP / SSE 行为 | 资源状态 |
| --- | --- | --- |
| response headers 前失败 | 返回对应 HTTP status 与协议 JSON error | 释放账户槽位 |
| SSE 已开始后失败 | 发送 OpenAI error、`response.failed`、Anthropic `error` 或 Gemini error frame | 关闭上游 reader并释放账户槽位 |
| 完成帧 | 输出 finish reason、usage 与协议终止事件 | 合并 Set-Cookie并释放账户槽位 |
| 客户端取消 | 结束上游读取 | 取消请求上下文并释放账户槽位 |

欢迎二次开发，如果对你有帮助，考虑给仓库点一个Star~
