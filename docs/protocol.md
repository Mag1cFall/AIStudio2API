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
| Drive resumable 上传 | `https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&fields=id` | 分块 HTTPS body |
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

JSON+protobuf 使用数组表示 protobuf message。数组索引从 `0` 开始，protobuf field 从 `1` 开始，因此 field `N` 对应索引 `N-1`。Google 响应允许省略空槽并形成 `[,value]`；解码器先把省略槽规范化为 `null`，再从完整 JSON 根值中提取 repeated message。HTTPS chunk 仅提供字节序列，业务事件起止由数组结构确定。

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

本文中的数据面指处理公开 API 请求、可通过 Stop/Start 启停的生成服务。

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

MakerSuite 响应的 `Set-Cookie` 在响应头到达时与账户最新 `storage-state.json` 串行合并并原子写回。签名、Cookie 选择和过期判断均以请求时重新读取的账户状态为准。

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

`storage-state.json` 保留 Playwright 根字段和未知扩展字段，已定义形状如下。`wrapped_binding_key` 是 Go `[]byte` 的 Base64 JSON 字符串。

```json
{
  "cookies": [
    {
      "name": "<NAME>",
      "value": "<VALUE>",
      "domain": ".example.com",
      "path": "/",
      "expires": -1,
      "httpOnly": true,
      "secure": true,
      "sameSite": "Lax",
      "partitionKey": "<OPTIONAL>"
    }
  ],
  "origins": [
    {
      "origin": "https://example.com",
      "localStorage": [{"name": "<NAME>", "value": "<VALUE>"}]
    }
  ],
  "aistudio2api": {
    "source": {"browser": "chrome", "profile": "<PROFILE>", "email": "<EMAIL>"},
    "oauth": {
      "gaia_id": "<GAIA_ID>",
      "refresh_token": "<REFRESH_TOKEN>",
      "wrapped_binding_key": "<BASE64>"
    }
  }
}
```

Cookie 的 `name`、`value`、`domain`、`path`、`expires`、`httpOnly`、`secure`、`sameSite` 与可选 `partitionKey` 原样持久化；`sameSite` 接受空值、`Lax`、`Strict` 或 `None`。origin 必须包含 scheme 与 host。请求 Cookie 过滤过期项与不匹配的 Secure/domain/path 条目，同名项按 path 长度降序发送；响应 `Set-Cookie` 以 name、domain、path 三元组替换或删除现有项。

### 账户持久状态

| 文件 | 内容 |
| --- | --- |
| `auth/<Google 邮箱>/account.json` | 邮箱、enabled、proxy、locale、timezone |
| `auth/<Google 邮箱>/storage-state.json` | Cookie、localStorage 和可选 Chrome 续签材料 |
| `auth/<Google 邮箱>/camoufox-fingerprint.json` | 账户固定的 navigator、屏幕、字体、语言、地区和时区配置 |
| `auth/<Google 邮箱>/runtime-state.json` | 账户权益、实测模型资格、冷却与 Drive/Veo 资源绑定 |
| `auth/.leases/<Google 邮箱>.lock` | 同一账户目录的跨进程占用锁 |
| `auth/.leases/<Google 邮箱>.runtime.lock` | 每次 `runtime-state.json` 读取、合并与写回的短事务锁 |
| `[用户缓存]/AIStudio2API/runtime-leases/<Google 邮箱>.lock` | 当前电脑上该邮箱的 WAA Worker 占用锁 |

Google 邮箱的小写形式同时作为账户目录、管理页面标识和日志来源。新账户的 locale 与 timezone 读取当前电脑设置，管理页面使用浏览器语言和 IANA 时区；CLI 使用操作系统语言和时区。初始化、WAA、MakerSuite、OAuth 续签和 Drive 使用账户固定代理。locale 同时设置 navigator language、Accept-Language 与地区，timezone 设置浏览器时区；重新登录和 WAA runtime 复用同一账户指纹。同一电脑上的多个进程按邮箱共享 WAA runtime lease，调度器只会为未被占用的邮箱创建 Worker。

`runtime-state.json` 根字段为 `cooldowns`、`resources`、`model_access`、`benefit_tier`、`catalog_fingerprint`。cooldown value 为 `{until,reason?}`；model access value 为 `{state,checked_at,reason?}`；resource value 为 `{kind?,name?,mime?,size?,purpose?,created_at,video?:{model,seconds,size}}`。

每次运行状态事务先取得 `auth/.leases/<账户>.runtime.lock`，每 25ms 尝试一次，等待上限为 2 秒；携带调用方 context（Go 取消上下文）的事务在调用方取消或 deadline 更早到达时立即结束。取得锁后重新读取当前磁盘值，只合并目标字段，以临时文件原子替换 `runtime-state.json`，再同步内存状态与资源归属。锁等待失败、读取失败、写回失败和 unlock 失败均返回原始错误链。

调度器按实时模型方法、capability、AccessModes 和账户权益选择候选，优先使用已经成功调用目标 scope（模型资格与冷却的状态记录范围）的预热账户，再按目标模型最近一次真实首事件耗时排序。普通生成在首个语义事件前遇到 Code 7 时结束当前账户尝试并清除对应 scope 的成功记录，Bidi setup 遇到 Code 7 时改选其他候选；已建立的 Bidi 会话返回 error 并关闭。常驻 Worker 优先覆盖可调用模型更多的账户。预热数量低于上限时提升待机账户，预热账户均忙时等待并发槽位。同账号 WAA proof 串行生成，已准备的 MakerSuite HTTP 请求并发执行；首个活动请求获取 `.leases/<邮箱>.lock`，最后一个释放。Drive file、Veo operation 与产物 file 始终使用创建账户。

### 账户权益

`GetAiStudioBenefitTier` 请求为 `[]`，响应 field 1 的枚举映射如下。`[]`、`[null]` 和 `[0]` 均表示 Free；响应存在后续字段时，权益仍由 field 1 决定。

| 值 | 权益 | RPC Header |
| ---: | --- | --- |
| 0 | Free | 无 |
| 1 | Pro | `X-AIStudio-G1-Tier: TIER1` |
| 2 | Ultra | `X-AIStudio-G1-Tier: TIER2` |
| 3 | Plus | `X-AIStudio-G1-Tier: TIER0` |

官网为 `GenerateContent`、`CountTokens`、Interaction、Code Assistant 与 Veo RPC 注入该 header。模型 field 83 描述访问方式：`1` 为付费 API key，`3` 为 Pro/Ultra 订阅，`4` 为 Ultra 订阅。公开模型目录合并全部账户的实时 `ListModels` 记录；账户调度使用 AccessModes、BenefitTier 与成功调用历史选择具体账户。

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

`Waa/Create` 响应第二槽经 Base64 解码后，对每个字节加 `97` 得到 challenge。归一化后的 Challenge 对象字段如下：

```json
{
  "messageId": "<MESSAGE_ID>",
  "globalName": "<GLOBAL_NAME>",
  "interpreterHash": "<INTERPRETER_HASH>",
  "interpreterUrl": "https://www.google.com/js/bg/<INTERPRETER_HASH>.js",
  "program": "<DYNAMIC_PROGRAM>"
}
```

`Waa/Create` 请求是三槽 JSON+protobuf 数组：

```json
["lmnUSbltwc5ULv48iKLX", null, null]
```

| protobuf field | 内容 |
| ---: | --- |
| 1 | bundle 固定 request key `lmnUSbltwc5ULv48iKLX` |
| 2 | 缓存的 interpreter hash 或 null |
| 3 | 前一轮 empty snapshot 或 null |

cold Create 固定使用上面的三个槽。下载 interpreter 后计算 SHA-256，并使用 Base64URL 无 padding 编码；结果必须等于 challenge 的 interpreter hash。一份 fresh challenge 复核样例包含 33,695 个字符的 program 与 65,831 字节的 interpreter，下载内容摘要与 challenge hash 相等。

`Waa/Create` 响应外层索引 `1` 是 Base64 challenge。Base64 解码后对每个字节加 `97`，再把结果解析为稀疏数组：

| JSON 索引 | 内容 |
| ---: | --- |
| 0 | message ID |
| 1 | interpreter JavaScript 列表，取第一个非空字符串 |
| 2 | interpreter 路径列表，取第一个非空字符串并补全 `https:` |
| 3 | interpreter hash |
| 4 | dynamic program |
| 5 | global function name |
| 6 | 未识别槽，原样保留 |
| 7 | client experiments state blob |

```json
[
  "<MESSAGE_ID>",
  ["<INTERPRETER_JAVASCRIPT>"],
  ["//www.google.com/js/bg/<INTERPRETER_HASH>.js"],
  "<INTERPRETER_HASH>",
  "<DYNAMIC_PROGRAM>",
  "<GLOBAL_NAME>",
  null,
  "<CLIENT_EXPERIMENTS_STATE_BLOB>"
]
```

`program` 与 challenge 属于当前 Create 生命周期，interpreter 按 hash 缓存。proof 绑定当前 prompt 摘要与 VM 内部状态，每个请求生成新的 proof。

生成服务启动时按配置的常驻数与启动并发数预热账户 WAA runtime：

1. Go 启动隔离、无头 Camoufox，并通过原生 WebDriver BiDi 建立 session
2. 写入账户 Cookie 与 localStorage，先尝试实时目录中的 `gemini-flash-latest`，再按目录顺序尝试其余支持 generateContent、账户权益和 chat model 能力的模型；`TEMPORARY_CHAT=true` 时 URL 携带 `temporary=true`
3. 定位页面 bundle 中调用 `.snapshot({` 且包含 `content` 的官方高层函数
4. 为官网 `GenerateContent` 安装 `beforeRequestSent` BiDi 拦截，再填入唯一 bootstrap prompt 并执行官网 Run
5. 页面调用官方 snapshot 时保存 WAA service；请求进入拦截阶段后保存动态头并通过 `network.failRequest` 在浏览器内终止
6. 后续业务请求串行调用同一 service 获取 fresh proof
7. `GenerateContent` 写入 field 5，`GenerateVideo` 写入 field 8，正文由 Go HTTP transport 发送

运行时将 `gemini-flash-latest` 作为首选 bootstrap model；该别名未出现在实时目录时，使用首个支持文本生成的聊天模型。

Camoufox 负责官方 VM 初始化与 WAA proof；Go 负责业务请求、增量解码和公开 API。运行期依赖 Go 与 Camoufox。官方 VM 初始化参数顺序为：

```javascript
initialize(program, ready, true, environment, passEvent, signalLists, persistentState, false, loggers)
```

`passEvent` 是解释器事件回调，`signalLists` 为两组 signal callback，`persistentState` 是当前 challenge 生命周期的状态输入，`loggers` 是四个 logger callback。VM 生命周期参数为 `43,200,000ms`，检查间隔为 `300,000ms`。页面生命周期中断、snapshot 错误、计时器到期、认证续签或进程关闭会使 runtime 失效，下一次请求重新 bootstrap。bundle 定义 `Waa/Create` 与 `Waa/Ping`；页面初始化调用 Create，业务请求 proof 由 snapshot 生成。

snapshot 的底层输入是四槽数组，首槽承载 binding：

```javascript
[{content: sha256(bindingPrompt)}, undefined, undefined, undefined]
```

`Waa/Ping` 请求和成功响应：

```json
["lmnUSbltwc5ULv48iKLX", "<BOTGUARD_RESPONSE>"]
[]
```

field 1 是 `request_key`，field 2 是 `botguard_response`。正确 proof、损坏 proof、省略 field 2、错误 request key 与无账户认证均可返回 HTTP 200 和 `[]`。Ping 成功表示 WAA RPC、API consumer identity 与字段类型可达；Worker VM、snapshot proof、binding、账户会话、模型资格和 GenerateContent 接受状态由实际受保护业务 RPC 判定。

Bootstrap 使用的 `GenerateContent` 在发往上游前终止，模型输出量为零。一个账户 Worker 为该账户的所有普通生成模型提供 proof，业务模型切换直接复用当前 Worker。临时对话关闭预热页的自动保存。

取消 Worker 启动会关闭 BiDi、终止 Camoufox 进程树并删除临时 profile。页面状态识别 Google 登录跳转与网页 Cookie 拒绝条；其他页面状态返回包含当前 URL 的启动错误。

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

响应根形状为 `[[<MODEL_ROW>, ...]]`。模型列表位于 field 1，后续根字段不改变模型目录。模型行字段：

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

公开模型对象为已知主能力码增加以下语义键：

| 码 | capability 键 | 码 | capability 键 |
| ---: | --- | ---: | --- |
| 1 | `chat_model` | 9 | `code_execution` |
| 10 | `function_declarations` | 12 | `google_search` |
| 13 | `browse` | 20 | `video_route` |
| 21 | `image_route` | 25 | `thinking` |
| 26 | `live_route` | 35 | `thinking_budget` |
| 37 | `speech_route` | 43 | `media_resolution` |
| 47 | `aspect_ratio` | 49 | `output_resolution` |
| 52 | `thinking_level` | 53 | `music_route` |
| 54 | `image_search` | 58 | `google_maps` |
| 59 | `interaction_route` | 74 | `transcription_word_timestamps` |
| 76 | `transcription_language_codes` | 77 | `transcription_output` |
| 80 | `transcription_speaker_labels` | 81 | `transcription_custom_vocabulary` |
| 84 | `transcription_smart` | | |

每个主能力码都保留为 `capability_code_<N>`，同时为上表中的已知码增加语义键；次能力码保留为 `secondary_capability_code_<N>`。

图片与视频选项使用枚举码：

| 类型 | 码值映射 |
| --- | --- |
| 图片/视频宽高比 | `1=1:1`、`2=9:16`、`3=16:9`、`4=3:4`、`5=4:3`、`6=3:2`、`7=2:3`、`8=5:4`、`9=4:5`、`10=21:9`、`11=9:21`、`12=1:4`、`13=4:1`、`14=1:8`、`15=8:1` |
| 图片分辨率 | `1=1K`、`2=2K`、`3=4K`、`4=512` |
| 视频时长 | `1=5s`、`2=6s`、`3=7s`、`4=8s`、`5=4s` |
| 视频分辨率 | `1=720p`、`2=1080p`、`3=4k`、`4=368p`、`5=360p` |

Veo field 71 的宽高比、时长和分辨率分别位于子索引 `4`、`5`、`9`。TTS field 67 是 repeated voice row，每行索引 `0` 为 voice name。thinking field 72 的默认 level 位于子索引 `5`。

field 57 alias 可以是 `["models/<ALIAS>"]`，也可以是 repeated row；row 形状时取每行索引 `0` 并移除 `models/` 前缀。公开 `capability_options` 的键全集为 `aliases`、`voices`、`image_aspect_ratios`、`image_output_resolutions`、`video_aspect_ratios`、`video_durations_seconds` 和 `video_output_resolutions`；没有值的键省略。

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

带 finish reason 的模型完成帧可以使用 `[null,"model"]`，该帧只提供终止状态与 usage。

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
| 22 | 23 | transcription metadata `[text, speaker?, timestamp spans?]` |

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
| 31 | 32 | transcription config |

生成参数校验：

| 参数 | 默认来源 | 有效值 |
| --- | --- | --- |
| max output | ListModels field 7 | `1..model.outputTokenLimit` |
| temperature | ListModels field 9 | `0..2` |
| topP | ListModels field 10 | `0..1` |
| topK | ListModels field 11 | 非负整数 |
| thinking level | ListModels field 72 | Low=`1`、Medium=`2`、High=`3`、Minimal=`4` |
| thinking budget | 请求值 | 模型能力码包含 thinking budget |

`reasoning_effort` / `thinkingLevel` / Anthropic `output_config.effort` 接受 `minimal`、`low`、`medium`、`high`。模型只有 thinking budget 能力时，显式 effort 需要同时提供 budget；模型只有 thinking level 能力时，显式 budget 退化为 level。两类能力都缺少时返回参数错误。

response modalities：

`wire` 指发送给上游的原始数组字段。

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

Anthropic redacted thinking block 以 `data` 承载同一份不透明状态；适配器在输入与输出两侧保留该值。

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
| 5 | 第二个 repeated web search query 槽 |
| 6 | Maps widget context token |

索引 `4`、`5` 按槽位与元素顺序合并并去重。

`oneof` 表示同组字段中最多选择一种值。

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

Anthropic 流式 `message_start` 写入即时输入估算，最终 `message_delta` 使用完成 usage 覆盖为权威输入与输出统计。

携带 stop sequence 的请求并行执行同账户 `CountTokens`。正文匹配实际序列时，协议核心关闭生成流，使用 `CountTokens` 的输入总数以及已输出的正文、reasoning 和工具统计构造最终 usage；计数失败时仍返回 `stop_sequence` 终态并省略 usage。

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
| 18 | missing_thought_signature | 19 | `provider_19` |
| 其他整数 | `provider_<code>` | | |

错误响应根形状为 `[null,[code,message,...]]`。协议核心保留 HTTP 状态、协议 code 与 message；公开适配器映射为 OpenAI、Anthropic 或 Gemini 错误对象。Chat、Responses、Anthropic Messages 与 Gemini GenerateContent 将媒体模型的普通文本作为文本结果输出；专用图片端点要求图片结果。HTTP/协议错误或缺失完成帧形成失败；上游 finish reason 作为正常终态保留并映射到各公开协议。

各协议的具体终态转换如下：

| 上游终态 | OpenAI Chat | OpenAI Responses | Anthropic Messages | Gemini GenerateContent |
| --- | --- | --- | --- | --- |
| `stop` 且包含函数调用 | `tool_calls` | `completed`，保留 function call item | `tool_use` | `STOP`，保留 functionCall Part |
| `stop_sequence` | `stop` | `completed` | `stop_sequence` 与实际序列 | `STOP` |
| `max_tokens` | `length` | `incomplete/max_output_tokens` | `max_tokens` | `MAX_TOKENS` |
| policy、refusal、签名缺失及其他异常终态 | `content_filter` | `incomplete/content_filter` | `refusal` | 对应 Gemini 枚举或 `OTHER` |

异常终态优先于同一结果中的工具调用终态，已产生的正文、reasoning、工具事件和 usage 保持在响应中。`provider_*` 在 Chat choice、Responses response、Anthropic message 或 `message_delta` 的 `provider_finish_reason` 中保留原值；Gemini 使用 `finishMessage` 保留编号。`provider_19` 对应 AI Studio 页面的 `Content blocked`。

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

Search tool 的 index `3` 是 `[timeRange?,searchTypes]`。timeRange 为 `[start?,end?]`，每个时间值编码为 `["<UNIX_SECONDS>"]`；searchTypes 的索引 `0/1` 分别启用 web 与 image search。

公开工具名称归一化后再生成上述 Tool 数组：

| AI Studio 工具 | OpenAI Chat / Responses | Anthropic | Gemini |
| --- | --- | --- | --- |
| function declarations | `function` | 空 type 或 `custom` | `functionDeclarations` |
| Google Search | `web_search`、`web_search_preview` | `web_search*` | `googleSearch`、`googleSearchRetrieval` |
| Image Search | `image_search` | `image_search` | `imageSearch` |
| URL Context | `url_context` | `web_fetch*`、`url_context*` | `urlContext` |
| Code Execution | `code_interpreter` | `code_execution*` | `codeExecution` |
| Google Maps | `google_maps` | `google_maps*` | `googleMaps` |

Anthropic 接受的具体 server tool type 为：

| AI Studio 工具 | Anthropic type |
| --- | --- |
| Google Search | `web_search_20250305` |
| Image Search | `image_search` |
| URL Context | `web_fetch_20250910`、`url_context` |
| Code Execution | `code_execution_20250522`、`code_execution_20250825` |
| Google Maps | `google_maps` |

根 field 7 按请求声明逐项编码，函数声明和各类 Google 工具按上表对应的 Tool entry 编码。模型的工具范围取自实时能力码。

编码器将全部函数声明合并为一个 Tool entry；Google Search 与 Image Search 合并为一个 search entry 并分别占用 `searchTypes` 索引 `0/1`；Code Execution、URL Context 与 Maps 各占一个 entry。Google Maps 与 Code Execution/URL Context 构成互斥工具组，每个请求选择其中一组。

Anthropic server tool 的 `name` 必须分别为 `web_search`、`image_search`、`web_fetch`、`code_execution`、`url_context` 或 `google_maps`。这些定义接受 `type` 与对应 `name`；额外选项、`description` 或 `input_schema` 返回 `400 invalid_request_error`。

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

OpenAI 文件入口接收 `multipart/form-data` 的 `file` 与 `purpose`，单文件上限为 512 MiB。未知长度的请求使用 Drive resumable upload 和 8 MiB 分块；上传完成后 `POST /v1/files` 返回持久文件对象，`GET /v1/files/{id}` 从资源绑定读取文件名、大小、purpose 与创建时间。客户端取消会终止上传并释放账户租约。

### Gemini 3.5 Transcribe

`gemini-3.5-transcribe` 使用 Drive file Part 与 GenerateContent generation config field 32。转录配置子字段如下：

| protobuf field | 内容 |
| ---: | --- |
| 5 | word timestamps，启用值为 `1` |
| 6 | speaker labels，启用值为 `1` |
| 7 | repeated custom vocabulary |
| 8 | repeated language codes |
| 9 | smart transcription，启用值为 `2` |

转录元数据位于响应 Part field 23，子字段 1 为文本、2 为 speaker label、3 为 repeated timestamp span。每个 span 的 field 2 与 field 3 分别是开始和结束时间，时间消息使用 seconds 与 nanos。

`POST /v1/audio/transcriptions` 接受最大 512 MiB 的音频、MP4 或 WebM 文件，公开格式为 `json`、`text`、`verbose_json` 与 `diarized_json`。每次账户尝试创建一个临时 Drive file；生成结束后在同一账户删除该文件。`smart_transcription` 与显式 word timestamps 或 speaker labels 的组合返回 `400 invalid_request`。

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

起始帧的图像来源 oneof 为 inline image 或 Drive file。创建响应 field 1 是 operation ID。轮询请求为 `["<OPERATION_ID>"]`；轮询响应 field 1 是 done，产物 Drive file ID 位于 `$[1][0][0][0]`。operation 与结果 file 均绑定创建账户，再通过 Drive bearer 下载媒体。count、宽高比、秒数和分辨率按实时模型 field 71 校验。

### Live 与 Robotics WebSocket

`GET /v1/live` 与 `GET /v1/robotics/stream` 升级为 WebSocket。连接建立后的首个客户端 JSON 必须是 setup：

```json
{"type":"setup","model":"gemini-3.1-flash-live-preview","input_modalities":["text"],"output_modalities":["audio"],"tools":[{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"session_token":""}
```

Live 的 input modalities 可以由 text、audio、image 组成，output modalities 固定为 audio。Robotics 输入输出均为 text。两个入口都使用客户端提供的模型，并由实时模型目录的 `bidiGenerateContent` 方法选择账户。客户端在 WebSocket 建立后 10 秒内发送 setup 首帧；上游握手、backchannel 就绪和 setup complete 共用 `INIT_TIMEOUT`，随后依次发送 `session_opened` 与 `setup_complete`。

上游 WebChannel 由握手、前向 POST、长轮询 backchannel 与 terminate 组成。握手 query 使用 `VER=8`、随机 `RID`、`CVER=22`、`X-HTTP-Session-Id=gsessionid` 与 `count=0`；响应 header 给出 gsessionid，首个控制 envelope 为：

```json
[[0,["c","<SID>","",8]]]
```

后续前向请求的 query 字段为 `VER`、`gsessionid`、`SID`、`RID`、`AID`、`zx`、`t`；form 字段为 `count=1`、`ofs=<OFFSET>`、`req0___data__=<JSON_PROTOBUF>`。成功响应是三个整数的 ACK 数组，三个槽只校验整数形状，第二槽不绑定本地 RID、AID 或 ofs。HTTP 200 且 ACK 合法后提交 `RID+1` 与 `ofs+1`；失败、取消或 ACK 无效时保持原值。

backchannel 使用 `RID=rpc`、当前 SID、gsessionid 与 AID。每个网络帧为十进制字节数、LF 和对应 JSON 字节：

```text
<DECIMAL_LENGTH><LF>
<JSON_BYTES>
```

JSON 根值包含 repeated `[serverAID,payload]` envelope。payload 解码成功并按顺序发布后，将 AID 提交为 `max(currentAID,serverAID)`。首次 backchannel 建立后的临时读取失败保留 SID、gsessionid 与已提交 AID 并重新连接；首次建立失败、协议终态、显式 close 或不可恢复错误进入关闭链。会话状态为 `new -> handshaking -> ready -> reconnecting -> ready`，关闭路径依次进入 `closing -> closed`。

客户端帧：

| type | 字段 | 作用 |
| --- | --- | --- |
| `text` | `text` | 发送文本输入 |
| `audio` | `mime_type: audio/pcm`、`data` | 发送 PCM Base64 |
| `image` | `mime_type: image/jpeg`、`data` | 发送 JPEG Base64 |
| `media_end` | 无 | 结束当前媒体输入 |
| `tool_response` | `tool_responses` | 批量发送函数结果，保留调用的 `id` 与 `name` |
| `close` | 无 | 关闭逻辑会话 |

`tool_response` 示例：

```json
{"type":"tool_response","tool_responses":[{"id":"call-1","name":"get_weather","content":{"temperature":26}}]}
```

服务端事件使用 `session_opened`、`setup_complete`、`text`、`media`、`input_transcription`、`output_transcription`、`tool_call`、`tool_call_cancellation`、`interrupted`、`generation_complete`、`turn_complete`、`session_resumption`、`usage`、`go_away`、`provider`、`closed` 和 `error`。`tool_call` 携带单个函数调用；一条上游消息包含多个调用时按顺序发送多条事件。`tool_call_cancellation.tool_call_ids` 携带被取消的调用 ID。新的 `session_resumption.session_token` 原子替换上一枚 token；后续 setup 携带该 token 时绑定原账户恢复。

Live 纯文本使用模型 scope，Live 音频/图像和 Robotics 使用 `bidi-media:<modelID>` scope。上游 Code 7 通过 `error` 事件返回并结束当前连接。客户端单帧上限为 8 MiB，超限使用 WebSocket close code `1009`。

上游 Bidi 客户端 wire 使用六槽或七槽稀疏数组。setup 位于外层 field 7：

| setup JSON 索引 | protobuf field | 内容 |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 1 | 2 | generation configuration |
| 2 | 3 | tools |
| 6 | 7 | `[sessionToken]`；无 token 时为 `[]` |
| 7 | 8 | `[104857,[52428]]` buffering 参数 |
| 9 | 10 | 固定空数组 |
| 10 | 11 | 固定空数组 |
| 15 | 16 | timezone `[null,null,null,null,[zone]]` |

configuration 使用 18 槽数组：

| JSON 索引 | 内容 |
| ---: | --- |
| 14 | 输出 modalities；TEXT=`[1]`、AUDIO=`[3]` |
| 15 | Live voice `[[["Zephyr"]]]` |
| 16 | thinking `[1,null,null,level]`；Live Minimal=`4`、Robotics High=`3` |
| 17 | 固定值 `2` |

最小 setup 外层形状：

```json
[
  null, null, null, null, null, null,
  [
    "models/<MODEL_ID>",
    [null, null, null, null, null, null, null, null, null, null, null, null, null, null, [3], [[["Zephyr"]]], [1, null, null, 4], 2],
    null,
    null, null, null,
    [],
    [104857, [52428]],
    null,
    [],
    [],
    null, null, null, null,
    [null, null, null, null, ["Asia/Taipei"]]
  ]
]
```

后续客户端 wire：

| 公开帧 | 外层位置 | 子消息 |
| --- | --- | --- |
| `text` | index 2 / field 3 | realtime input index 4 保存文本 |
| `audio` | index 2 / field 3 | realtime input index 1 保存 `[mime,base64]` |
| `image` | index 2 / field 3 | realtime input index 3 保存 `[mime,base64]` |
| `media_end` | index 2 / field 3 | realtime input index 2 为 `1` |
| `tool_response` | index 3 / field 4 | 子消息 index 1 保存 repeated `[name,Struct,id]` |

setup 与每个后续客户端 wire 都在发送前把 fresh WAA proof 写入外层 index `5` / field `6`。snapshot binding 输入：

| wire | binding prompt |
| --- | --- |
| setup | `models/<MODEL_ID>`，随后按声明顺序追加每个 `name + " " + description`，各段以单个空格连接 |
| text、audio、image、media end | 空字符串 |
| tool response | 第一条 function response 的 call ID |

服务端业务 message 索引：

| JSON 索引 | 内容 |
| ---: | --- |
| 1 | setup complete |
| 2 | server content |
| 3 | tool calls |
| 4 | tool cancellation |
| 5 | usage raw |
| 6 | go away raw |
| 7 | session resumption |

server content 的 index `0/1/2/4/5/6` 分别为 model content、turn complete、interrupted、generation complete、input transcription、output transcription；model content 的 index `0` 是 repeated Part。transcription 子索引 `0/1/2/3` 分别为 text、finished、duration milliseconds、language code。tool calls 位于 message index `3` 的子索引 `1`，每项为 `[name,Struct,id]`；tool cancellation 位于 message index `4` 的子索引 `0`，值为 repeated call ID。session resumption 子索引 `0/1` 分别为 token 与 resumable。对象状态错误形状为 `{"__sm__":{"status":[[[code,message]]]}}`；字符串 payload `"noop"`、`"close"`、`"stop"` 分别表示空操作、正常关闭与错误停止。

## 7. 公开端点、状态映射与实现

| 协议 | 端点 |
| --- | --- |
| OpenAI Chat | `GET /v1/models`、`POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| OpenAI 媒体 | `POST /v1/images/generations`、`POST /v1/audio/speech`、`POST /v1/videos`、`GET /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、`:predictLongRunning`、`GET /v1beta/operations/{id}` |

扩展端点：

| 协议 | 端点 |
| --- | --- |
| OpenAI 文件 | `POST /v1/files`、`GET /v1/files/{id}`、`GET /v1/files/{id}/content`、`DELETE /v1/files/{id}` |
| OpenAI 转录 | `POST /v1/audio/transcriptions` |
| 实时 WebSocket | `GET /v1/live`、`GET /v1/robotics/stream` |

动态路由的注册形状为 `GET /v1/files/{file}`、`GET /v1/files/{file}/content`、`DELETE /v1/files/{file}`、`GET /v1/videos/{video}`、`GET /v1/videos/{video}/content`、`POST /v1beta/models/{action}` 与 `GET /v1beta/operations/{operation}`；端点表中的 `{id}` 表示对应资源标识。

公开 `/v1` 与 `/v1beta` 接受 `Authorization: Bearer`、`X-API-Key`、`X-Goog-API-Key` 或 `?key=`，读取优先级为 `?key=`、`X-Goog-API-Key`、`X-API-Key`、`Authorization: Bearer`；配置为空时关闭本地 API key 校验。`/v1*` 响应允许任意 origin，允许 `GET/POST/PUT/DELETE/OPTIONS` 与 `Authorization`、`Content-Type`、`X-API-Key`、`X-Goog-API-Key`、`Anthropic-Version`、`Anthropic-Beta` headers。`/api` 控制面仅允许 loopback，携带 Origin 时执行 same-origin 校验。`GET /health` 返回 `{"status":"ok"}`。

| 控制能力 | 端点 |
| --- | --- |
| 状态与模型 | `GET /api/status`、`GET /api/models` |
| 生成服务 | `POST /api/control/start`、`POST /api/control/stop` |
| 账户 | `GET /api/accounts`、`POST /api/accounts`、`PUT /api/accounts/{id}`、`DELETE /api/accounts/{id}` |
| 账户认证 | `POST /api/accounts/{id}/login`、`POST /api/accounts/{id}/verify` |
| 配置 | `GET /api/config`、`PUT /api/config` |
| 冷却与请求 | `GET /api/cooldowns`、`GET /api/requests`、`POST /api/requests/{id}/cancel` |
| 日志与事件 | `DELETE /api/logs`、`GET /api/events` |

管理 API 使用 DTO（data transfer object）表示请求、响应和事件对象。端点结果：

| 端点 | 成功状态 | body |
| --- | ---: | --- |
| `GET /api/status` | 200 | `AdminStatus` |
| `GET /api/models` | 200 | `{"models":[Model,...]}` |
| `GET /api/accounts` | 200 | `{"accounts":[AdminAccount,...]}` |
| `POST /api/accounts` | 201 | `{"account":AdminAccount}` |
| `PUT /api/accounts/{id}` | 200 | `{"account":AdminAccount}` |
| `POST /api/accounts/{id}/login`、`verify` | 200 | `{"account":AdminAccount}` |
| `DELETE /api/accounts/{id}` | 204 | 空 body |
| `POST /api/control/start`、`stop` | 200 | `AdminStatus` |
| `GET /api/config`、`PUT /api/config` | 200 | `RuntimeConfig` |
| `GET /api/cooldowns` | 200 | `{"cooldowns":[AdminCooldown,...]}` |
| `GET /api/requests` | 200 | `{"requests":[AdminRequest,...]}` |
| `POST /api/requests/{id}/cancel` | 204 | 空 body |
| `DELETE /api/logs` | 204 | 空 body |
| `GET /api/events` | 200 SSE | `{"type":"<TYPE>","data":<DTO>}` |

管理 DTO 字段：

| DTO | 字段 |
| --- | --- |
| `AdminStatus` | `state`、`running`、`ready`、`version`、`active_requests`、`accounts` |
| `AdminAccountCounts` | `total`、`ready`、`busy`、`cooldown`、`auth_required` |
| `AdminAccount` | `id`、`label`、`enabled`、`state`、`proxy`、`locale`、`timezone`、`models`、`benefit_tier`、`message` |
| `AccountInput` | `label`、`enabled`、`proxy`、`locale`、`timezone` |
| `AdminCooldown` | `account_id`、`account_label`、`model_id`、`until`、可选 `reason` |
| `AdminRequest` | `id`、`model`、`account_id`、`account_label`、`state`、`started_at` |
| `AdminLog` | `time`、`level`、`source`、`message` |
| `AdminEvent` | `type`、`data` |

`AdminStatus.state` 为 `STOPPED`、`LAUNCHING` 或 `RUNNING`；`running` 只在 `RUNNING` 为 true；`ready` 要求 `RUNNING` 且至少一个账户处于 ready 或 busy；`version` 来自构建信息；`active_requests` 是当前进程请求注册表数量。`AdminAccount.message` 保存当前状态原因，`models` 是该账户实时目录 ID。`until` 与 `started_at` 使用 RFC 3339 JSON time。

`AccountInput.label` 必须是 Google 邮箱，`locale` 与 `timezone` 必须非空，`proxy` 使用无 credentials、path、query 或 fragment 的 HTTP、HTTPS、SOCKS5 origin。新增账户完成隔离登录后保存认证状态；更新账户保存固定配置；login 重新取得认证状态；verify 使用当前认证状态访问 AI Studio。新增、登录和验证成功后立即刷新该账户模型目录，并发布最新账户与模型事件。

`PUT /api/accounts/{id}` 的提交顺序固定为：校验不可变邮箱 ID，取得账户独占租约，创建未发布的新固定出口，关闭当前 Worker 并把新 Worker 配置标记为 `pending`（尚未发布），在模型目录写锁内原子写入 `account.json` 并更新账户池，随后发布 Worker 配置、替换固定出口、释放租约并重建模型缓存。`account.json` 写入是唯一持久提交点。提交前的出口创建、Worker 关闭或写入错误会丢弃这份待发布配置并保持旧配置；已经关闭的 Worker 由后续请求按旧配置重建。持久写入后，新配置、Worker 配置与固定出口共同成为已提交状态。租约释放错误保留该提交状态并返回原始 unlock 错误；释放成功后记录完成日志并同步模型缓存。

`RuntimeConfig` 字段。`response-only` 表示字段由服务器填充，客户端提交的值不参与保存：

| 字段 | 读写与生效时机 |
| --- | --- |
| `auth_states`、`proxy`、`init_timeout`、`request_timeout` | 保存值；下一次启动生成服务时使用 |
| `warm_worker_limit`、`max_active_workers`、`warm_startup_concurrency`、`per_account_concurrency` | 保存值；下一次启动生成服务时使用 |
| `temporary_chat` | 保存值；下一次启动生成服务时使用 |
| `listen_addr`、`proxy_api_key` | 保存值；下一管理进程使用 |
| `active_listen_addr`、`active_proxy_api_key` | response-only；当前管理进程固定值 |
| `management_restart_required` | response-only；保存的 listen/key 与当前管理进程不同 |
| `service_restart_required` | response-only；保存的生成服务配置与当前生成服务实例不同 |

`PUT /api/config` 原子保存配置。监听地址和 API key 在管理进程重启后生效；账户路径、代理、timeout、容量与临时对话在 Stop/Start 创建的新生成服务实例中生效。启动时读取最新配置；配置加载、校验、实例创建失败或启用前取消时保留原实例，切换到新实例后由它完成启动或进入 `STOPPED`。

`GET /api/events` 的初始顺序为 `status`、`models`、`accounts`、最多 2000 条 `log`、`cooldowns`、按开始时间排序的活动 `request`。后续事件的 `data` 形状：

| `type` | `data` |
| --- | --- |
| `status` | `AdminStatus` |
| `models` | `{"models":[Model,...]}` |
| `accounts` | `{"accounts":[AdminAccount,...]}` |
| `log` | `AdminLog` |
| `cooldowns` | `[AdminCooldown,...]` |
| `request` | `AdminRequest`，状态变化时重复发送同一 ID |

管理错误统一为：

```json
{"error":{"code":"invalid_request","message":"..."}}
```

控制面错误码包括 `control_plane_forbidden`、`control_plane_origin_forbidden`、`invalid_request`、`invalid_account`、`account_not_found`、`account_busy`、`account_required`、`request_not_found` 和 `upstream_error`。

| HTTP | 管理错误码 |
| ---: | --- |
| 400 | `invalid_request`、`invalid_account`、`account_required` |
| 403 | `control_plane_forbidden`、`control_plane_origin_forbidden` |
| 404 | `account_not_found`、`request_not_found` |
| 409 | `account_busy` |
| 上游状态或 502 | `upstream_error` |

运行状态机如下。目录 fan-out 表示并发向全部符合条件的账户调用 `ListModels`，pending account 表示等待下一轮目录重试的账户：

```text
process start
  -> control plane ready
  -> STOPPED

POST /api/control/start
  -> LAUNCHING
  -> load CachedModels
  -> fan out ListModels to every enabled ready/busy account
  -> if cache is empty, wait for the first non-empty live catalog
  -> prewarm up to WARM_WORKER_LIMIT workers
     with WARM_STARTUP_CONCURRENCY bootstraps
  -> first worker ready
  -> RUNNING
  -> continue full catalog fan-out and remaining worker prewarm in background
  -> every 30s, fan out ListModels to every pending account

request
  -> resolve model and endpoint capability
  -> acquire one PER_ACCOUNT_CONCURRENCY slot
  -> prepare WAA proof
  -> send MakerSuite RPC
  -> stream frames
  -> release slot

POST /api/control/stop
  -> cancel launch or active requests
  -> bound catalog fan-out shutdown to 2s
  -> close WAA workers
  -> bound unfinished lifecycle-transition wait to 12s
  -> STOPPED
```

管理状态字段使用大写服务状态 `STOPPED`、`LAUNCHING`、`RUNNING`。请求状态使用 `queued`、`running`、`completed`、`cancelled`、`failed`。视频状态使用 `queued`、`completed`、`failed`，对应 progress `0` 或 `100`。

模型目录驻留在当前 generation（`runtimeGeneration` 表示的一次生成服务实例）的内存中；正常 Stop/Start 创建的新 generation 以空缓存启动。启动先用当前 generation 的 `catalog.CachedModels()` 建立公开快照，并立即为每个 `enabled` 且处于 `ready` 或 `busy` 的账户启动一个后台 `ListModels` 任务。当前 generation 的真实缓存非空时直接进入 Worker 预热。每个非空结果到达时立即从 `CachedModels` 合并公共目录并发布 `accounts`、`models`；生成服务已经 `RUNNING` 时同时触发 Worker 预热，新 generation 的首个非空结果还会解除启动等待。全账户 fan-out 结束时，`auth_required` 集合发生变化会补发一次账户与模型快照，并记录同步成功数、非空数、公共模型数、待重试账户数和耗时。第一个 Worker 就绪后重新读取当前缓存，并在持有账户与配置变化锁时把服务状态设为 `RUNNING`。

账户状态：

| 状态 | 调度语义 |
| --- | --- |
| `ready` | 认证有效且存在可用槽位 |
| `busy` | 账户存在独占操作、认证刷新或活动请求；调度仍按 `PER_ACCOUNT_CONCURRENCY` 判断剩余槽位 |
| `cooldown` | 账户的全局 `*` 冷却仍有效；模型 scope 冷却只影响对应请求的候选分类 |
| `auth_required` | 账户级认证失败 |
| `unavailable` | 当前运行时不可用 |
| `disabled` | 配置已停用 |

认证状态写回携带 `authGeneration` 与 `checkedAt`，仅应用账户对象、`authGeneration` 值与时间均匹配当前状态的结果；相同时间以 `ready` 为最终状态。模型访问和冷却写回携带 `modelAccessGeneration` 与 `checked_at`，仅应用 `modelAccessGeneration` 值匹配且时间不早于当前状态的结果；相同时间以 `verified` 为最终状态。`verified` 表示该账户已经成功调用对应模型或能力。

`ModelAccessKey(scope, model)` 先移除 `models/` 前缀；空 scope 返回 canonical model ID（规范化模型 ID），非空 scope 返回 `<scope>:<canonicalModelID>`。各能力使用以下值：

| 能力 | scope | 成功记录 |
| --- | --- | --- |
| 普通生成 | `<modelID>` | 规范事件 `EventFinish` 到达时写 `verified` |
| CountTokens | `count-tokens:<modelID>` | 清 scope 冷却，保留普通生成资格 |
| Transcribe | `<modelID>` | 非空 text 或 segments 写 `verified` |
| Live 文本 | `<modelID>` | setup complete 写 `verified`；每次 text 开始一次模型资格检查 |
| Live 音频/图像 | `bidi-media:<modelID>` | media 开始媒体资格检查；Live text 使用普通模型 scope |
| Robotics | `bidi-media:<modelID>` | text 开始模型资格检查 |

Code 7 清当前 operation scope 的 verified 状态；认证失败更新账户级 `auth_required`。上传、临时文件清理等非生成失败使用全局 `*` 冷却。

普通流式生成在消费到规范事件 `EventFinish` 时写入 verified。正文、reasoning、工具、usage 与首事件用于输出和性能统计；终态前断流、客户端取消或错误保持原有验证状态。

Bidi setup 成功使用 lease（本次会话持有的账户租约）的 `checkedAt`。此后每个会更新模型资格的轮次分配会话内严格递增的 attempt 时间；`turn_complete` 与 Code 7 消费对应 attempt。结果按 `(generation, attempt)` 的版本顺序应用，其中 generation 是模型目录版本，attempt 是会话内资格检查顺序；较新的 `turn_complete` 写入资格，较新的 Code 7 清除资格。Bidi 认证成功与 401 使用相同 attempt 顺序，setup 后较晚返回的 401 可以更新账户为 `auth_required`。

确认浏览器进程退出后，将 process 和 Worker 状态设置为 `closed`。关闭失败时保留 Worker、runtime lease、warm 标记与 generation（Worker 实例版本号）；后续 Stop 即使服务状态已经是 `STOPPED`，也会再次执行 Worker reset。Camoufox 关闭阶段上界为 BiDi 3 秒、进程终止与等待 5 秒、profile 删除 2 秒；各阶段错误使用 `errors.Join` 保留。

模型目录 fan-out 绑定当前生成服务的 context。启动失败、启动取消或 Stop 会取消全部目录任务，并最多等待 2 秒确认后台目录协程退出。Stop 遇到尚未完成的 `LAUNCHING` 或其他启动/停止切换时，等待 transition channel（切换完成通知 channel）的上界为 12 秒；该上界覆盖 2 秒目录退出与有界 Worker 清理，超时错误与清理错误使用 `errors.Join` 返回。

按需热替换先启动 pending Worker（正在启动、尚未发布的替代 Worker），再关闭旧 Worker；旧实例成功退出后，替代 Worker 才成为当前 Worker。旧实例关闭和替代 Worker 回收同时失败时，两者都保留等待再次清理，并各占一个活动容量槽；达到容量上限后停止新建 Worker。完整生成服务 Stop/Start 的顺序为：Start 创建新生成服务实例前先重试停止旧实例，旧 PID 未退出时返回停止错误并保留原实例。

管理状态使用 `STOPPED`。该状态下生成与计数端点返回 `503 service_stopped`。普通生成首事件前和 Bidi setup 遇到 Code 7 时结束当前账户与 operation scope 的尝试并选择其他候选；已建立的 Bidi 会话返回 error 并关闭。Worker 进程故障、Worker 被替换与协议 Code 5 会重建当前账户 Worker 并在原账户重放一次。候选耗尽且没有符合方法、能力、权益与运行状态的账户时返回 HTTP 400：OpenAI code 为 `account_required`，Anthropic type 为 `invalid_request_error`，Gemini status 为 `INVALID_ARGUMENT`。

模型目录重试的 pending 集合保存等待再次同步的账户 ID。启动期全账户 fan-out、以及新增、登录或验证后的单账户同步，遇到任意错误或成功返回空目录时加入；返回非空目录时移除；删除账户同时移除。全账户后台同步结束后启动单个 30 秒 ticker（Go 定时器），每次对排序后的待重试账户列表再次并发 fan-out，并在任务开始时复核该 ID 仍在 pending 集合中。错误或空目录继续保留；每个非空成功立即更新账户缓存与公共目录快照、发布 `accounts` 和 `models`，并在 `RUNNING` 状态触发 Worker 预热。批次结束时，`auth_required` 集合发生变化会补发当前账户与模型快照；即时单账户同步无论成功或失败都立即发布当前快照。

模型目录投影：

| 规则 | 结果 |
| --- | --- |
| OpenAI | `GET /v1/models` 返回 OpenAI model list |
| Anthropic | `GET /v1/models` 携带 `Anthropic-Version` 时返回 Anthropic model list |
| Gemini | 模型名称使用 `models/<ID>` |
| 多账户同模型 | generation methods 与能力选项取并集 |
| 多账户 token limit | 输入和输出上限分别取正数最小值 |
| 模型别名 | 来自 ListModels field 57 |
| 请求匹配 | model ID/alias、method、capability、AccessModes、账户权益与运行状态决定候选集合 |
| 候选排序 | `verified` 与目标模型首事件耗时用于排序，未验证账户仍可进入候选 |
| capability 约束 | 端点要求的 capability 与账户实时能力同时命中 |

管理模型对象完整字段：

```json
{
  "id": "gemini-example",
  "name": "Gemini Example",
  "description": "...",
  "methods": ["countTokens", "generateContent"],
  "input_token_limit": 1048576,
  "output_token_limit": 65536,
  "capabilities": {"thinking": true, "capability_code_25": true},
  "capability_options": {"aliases": ["gemini-example-latest"]},
  "access_modes": [3, 4],
  "paid": true
}
```

OpenAI `GET /v1/models`：

```json
{
  "object": "list",
  "data": [{
    "id": "gemini-example",
    "object": "model",
    "created": 0,
    "owned_by": "google",
    "name": "Gemini Example",
    "description": "...",
    "supported_generation_methods": ["countTokens", "generateContent"],
    "input_token_limit": 1048576,
    "output_token_limit": 65536,
    "capabilities": {},
    "capability_options": {},
    "access_modes": [],
    "paid": true
  }]
}
```

请求携带 `Anthropic-Version` 时，同一路由返回：

```json
{
  "data": [{"id":"gemini-example","type":"model","display_name":"Gemini Example","created_at":"1970-01-01T00:00:00Z"}],
  "has_more": false,
  "first_id": "gemini-example",
  "last_id": "gemini-example"
}
```

Gemini `GET /v1beta/models` 返回 `{"models":[...]}`，单模型路由直接返回一个对象。字段为 `name`、`displayName`、`description`、`supportedGenerationMethods`、`inputTokenLimit`、`outputTokenLimit`、可选 `capabilities`、`capabilityOptions`、`accessModes`、`paid`。

`GET /v1beta/models/{model}` 只按 canonical model ID 查找；生成、计数、视频与 Bidi 调度同时接受 canonical ID 和 `capability_options.aliases` 中的 alias。

管理模型中的 `description`、token limits、capabilities、capability options、access modes 与 false `paid` 使用 `omitempty`；OpenAI 和 Gemini 响应始终包含身份、methods 与 token limits，并在 map/slice 非空或 `paid=true` 时增加对应扩展字段。

公开目录是上游实时模型集合的完整合并结果，并按 ID 排序。多账户同 ID 的 methods、capabilities、capability options 和 access modes 取并集，`paid` 取逻辑 OR，正数 token limit 取最小值。调度使用上游 methods、capabilities、access modes、账户权益和当前运行状态。

主要请求格式：

| 端点 | 必需字段 | 主要结果 |
| --- | --- | --- |
| `/v1/chat/completions` | `model`、非空 `messages` | Chat completion 或增量 chunk |
| `/v1/responses` | `model`、`input` | Response object 或 `response.*` 事件 |
| `/v1/files` | multipart `file`、`purpose` | OpenAI file object |
| `/v1/messages` | `model`、非空 `messages`、`max_tokens` | Anthropic message 或 message 事件 |
| `:generateContent` / `:streamGenerateContent` | 非空 `contents` | Gemini candidates、usage 与 grounding metadata |
| `/v1/images/generations` | `model`、`prompt`、固定 `n=1` | `b64_json` 或 data URL |
| `/v1/audio/speech` | `model`、`input` | WAV、PCM 或 MP3 body |
| `/v1/audio/transcriptions` | multipart `file`；`model` 默认 `gemini-3.5-transcribe` | 文本或转录 JSON |
| `/v1/videos` | `model`、`prompt` | 长任务对象，随后轮询并下载内容 |

Anthropic assistant prefill 以最后一条 `assistant` message 表示。AI Studio 当前没有对应生成前缀字段，`/v1/messages` 对该输入返回 `400 invalid_request_error`。

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
| stop sequence 命中 | 协议核心在正文事件流中匹配并返回实际命中的序列 |
| structured output | MIME type 映射 field 8，Schema 映射 field 9 |
| OpenAI Chat `n` | 仅接受省略或 `1` |
| OpenAI Chat `parallel_tool_calls` | 仅接受省略或 `true` |
| OpenAI Chat `logprobs` / `logit_bias` | 分别接受省略或 `false`、省略或空对象 |
| OpenAI Chat frequency / presence penalty | 仅接受 `0` |
| OpenAI Chat function `strict` | 接受省略或 `false`；`true` 返回 `400 invalid_request` |
| Responses `parallel_tool_calls` | 写入响应元数据，函数调用采用 AI Studio auto 模式 |
| Responses `parallel_tool_calls` 接受值 | 仅接受省略或 `true` |
| Responses `truncation` | 仅接受省略或 `disabled` |
| Responses function `strict` | 接受省略或 `false`；`true` 返回 `400 invalid_request` |
| Responses `store` | 省略或 `true` 时保存当前进程会话节点；`false` 只返回本次结果 |
| Gemini frequency / presence penalty | 仅接受 `0` |
| Gemini `candidateCount` | 仅接受省略或 `1` |
| Gemini `responseLogprobs` / `logprobs` | 分别接受省略或 `false`、省略或 `0` |
| Gemini `googleSearchRetrieval` | 仅接受空对象；`dynamicRetrievalConfig` 返回 `400 INVALID_ARGUMENT` |
| Anthropic `thinking` | `type` 为 `enabled` 且携带 `budget_tokens`；预算值直接写入 thinking budget |
| Anthropic thinking capability | 模型缺少 thinking budget 能力时形成 `invalid_request_error`；非流式返回 HTTP 400，流式返回 Anthropic error event |
| Anthropic thinking type | `disabled` 与未知 type 返回 `400 invalid_request_error` |

### OpenAI Chat Completions

`POST /v1/chat/completions` 请求字段：

| 字段 | 类型与语义 |
| --- | --- |
| `model` | 必需模型 ID |
| `messages` | 必需非空 message 数组 |
| `stream` | boolean |
| `stream_options.include_usage` | 在 finish chunk 后发送 usage-only chunk |
| `tools` | function 或 Google server tool 数组 |
| `tool_choice` | 省略/`auto`/`none` |
| `temperature`、`top_p` | 可选采样值 |
| `max_tokens`、`max_completion_tokens` | 后者优先 |
| `frequency_penalty`、`presence_penalty` | 省略或 `0` |
| `n` | 省略或 `1` |
| `parallel_tool_calls` | 省略或 `true` |
| `logprobs` | 省略或 `false` |
| `logit_bias` | 省略、`null` 或空对象 |
| `stop` | string 或 string array；空字符串从条件中移除 |
| `response_format` | `{type:"text"}`、`{type:"json_object"}` 或 `{type:"json_schema",json_schema:{schema}}` |
| `reasoning_effort` | thinking effort |
| `reasoning.effort` | nested thinking effort；与顶层字段映射到同一配置 |
| `seed` | 64 位整数 |

message 字段为 `role`、`content`、可选 `name`、`tool_call_id`、`tool_calls`。assistant tool call：

```json
{
  "id": "call_01",
  "type": "function",
  "function": {"name":"get_weather","arguments":"{\"city\":\"Taipei\"}"},
  "extra_content": {"google":{"thought_signature":"<SIGNATURE>"}}
}
```

`content` 可以是 string 或 Part 数组。Part 字段：

| `type` | 其他字段 |
| --- | --- |
| `text`、`input_text`、`output_text` | `text` |
| `image_url`、`input_image` | `image_url` string 或 `{"url":"..."}` |
| `video_url`、`input_video` | `video_url` string 或 `{"url":"..."}` |
| `input_file`、`file` | `file_id`，或 `filename` + `file_data` |
| `input_audio` | `input_audio.data`、`input_audio.format` |

OpenAI `image_url` / `input_image` 值为 Base64 data URL 时形成 inline data，值为 YouTube URL 时形成 external media，其他非 data 字符串按已上传 file ID 解析；适配器不下载普通 HTTP 图片 URL。`video_url` / `input_video` 只接受 YouTube URL。`file_data` 接受 Base64 data URL 或已上传 file ID。

function tool 使用 `{"type":"function","function":{"name","description","parameters","strict"}}`。`strict` 接受省略或 `false`。Google tool type 为 `web_search`、`web_search_preview`、`image_search`、`url_context`、`code_interpreter`、`google_maps`。

非流式响应：

```json
{
  "id": "chatcmpl_...",
  "object": "chat.completion",
  "created": 0,
  "model": "gemini-example",
  "provider_model": "gemini-provider-id",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "...",
      "reasoning_content": "...",
      "tool_calls": [],
      "annotations": []
    },
    "finish_reason": "stop",
    "provider_finish_reason": "provider_19"
  }],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 20,
    "total_tokens": 30,
    "completion_tokens_details": {"reasoning_tokens": 5}
  }
}
```

`provider_model`、`provider_finish_reason`、`reasoning_content`、`tool_calls`、`annotations` 和 `usage` 仅在有对应数据时出现。annotation 形状为 `{"type":"url_citation","url_citation":{"url","title","start_index","end_index"}}`。

Chat SSE 顺序：

1. role chunk：`delta={"role":"assistant","content":""}`
2. 正文 `delta.content`、思考 `delta.reasoning_content`、工具 `delta.tool_calls`、媒体或代码渲染 `delta.content`
3. 引用 `delta.annotations`
4. finish chunk：`finish_reason`，可选 `provider_finish_reason`
5. `include_usage=true` 且上游有 usage 时发送 `choices:[]` 的 usage-only chunk
6. `data: [DONE]`

每个普通 chunk 为 `{id,object:"chat.completion.chunk",created,model,choices:[{index,delta,finish_reason}],usage?}`。响应头已发送后的失败为 `data: {"error":{"message","type","code"}}`。

### OpenAI Responses

`POST /v1/responses` 请求字段：

| 字段 | 类型与语义 |
| --- | --- |
| `model` | 必需模型 ID |
| `input` | string 或 input item 数组 |
| `instructions` | 顶层 system instruction |
| `stream` | boolean |
| `tools`、`tool_choice` | function 与 Google tools；choice 为 auto/none |
| `temperature`、`top_p`、`max_output_tokens` | 生成参数 |
| `reasoning` | `{"effort":"..."}` |
| `text` | `{"format":{"type":"text|json_object|json_schema","schema":...}}` |
| `previous_response_id` | 当前进程内已保存的前一响应 ID |
| `parallel_tool_calls` | 省略或 `true` |
| `truncation` | 省略或 `disabled` |
| `metadata` | string-to-string object |
| `store` | 省略/`true` 保存节点，`false` 只返回本次结果 |

input item 字段为 `type`、`role`、`content`、`call_id`、`name`、`arguments`、`output`、`encrypted_content`。支持 message、`function_call`、`function_call_output` 与 reasoning item。message content Part：

| type | 字段 |
| --- | --- |
| `input_text`、`output_text` | `text` |
| `input_image` | `image_url` string 或 `{url}` |
| `input_file` | `file_id`，或 `filename` + `file_data` |
| `input_audio` | `input_audio:{data,format}` |
| `input_video` | `video_url` string 或 `{url}` |

Responses 的图片、视频与文件 Part 复用上述 data URL、file ID 与 YouTube 规则。

Responses tool 字段：

| tool type | 字段 |
| --- | --- |
| `function` | `name`、`description`、`parameters`、`strict` |
| `web_search`、`web_search_2025_08_26`、`web_search_preview`、`web_search_preview_2025_03_11` | 只接受 `type`；`search_context_size`、`user_location`、`filters` 必须省略 |
| `image_search`、`url_context`、`google_maps` | `type` |
| `code_interpreter` | `container` 可省略、为 `"auto"`，或为 `{"type":"auto","file_ids":[]}`；非空 `file_ids` 返回 400 |

响应 shell 的字段始终存在：

```json
{
  "id": "resp_...",
  "object": "response",
  "created_at": 0,
  "completed_at": null,
  "status": "in_progress",
  "error": null,
  "incomplete_details": null,
  "instructions": null,
  "metadata": {},
  "model": "gemini-example",
  "output": [],
  "output_text": "",
  "parallel_tool_calls": true,
  "previous_response_id": null,
  "reasoning": null,
  "temperature": null,
  "text": {"format":{"type":"text"}},
  "tool_choice": "auto",
  "tools": [],
  "top_p": null,
  "truncation": "disabled",
  "max_output_tokens": null,
  "usage": null
}
```

完成对象可以增加 `provider_model` 与 `provider_finish_reason`。status 为 `completed`、`incomplete` 或 `failed`；长度终态使用 `incomplete_details.reason=max_output_tokens`，策略终态使用 `content_filter`。

output item 联合类型：

| type | 字段 |
| --- | --- |
| `reasoning` | `id`、`status`、`summary:[{type:"summary_text",text}]`、可选 `encrypted_content` |
| `message` | `id`、`status`、`role:"assistant"`、`content:[{type:"output_text",text,annotations}]` |
| `function_call` | `id`、`status`、`call_id`、`name`、`arguments` |
| `code_interpreter_call` | `id`、`status`、`code`、`container_id:"aistudio"`、`outputs:[{type:"logs",logs}]` |
| `image_generation_call` | `id`、`status`、Base64 `result` |
| `web_search_call` | `id`、`status`、`action` |

`web_search_call.action` 为 `{"type":"search","query":"...","sources":[{"type":"url","url":"..."}]}`，query 按首次出现去重，sources 按 URI 去重。`code_interpreter_call.status` 在没有结果时为 `incomplete`，成功结果为 `completed`，非 `OUTCOME_OK` 结果为 `failed`；stdout 写入 `outputs[].logs`，失败文本加 `stderr:` 前缀。流式 `output_item.added` 使用 `in_progress`，对应 `output_item.done` 使用最终状态。

Responses usage：

```json
{
  "input_tokens": 10,
  "output_tokens": 20,
  "total_tokens": 30,
  "input_tokens_details": {"cached_tokens":0},
  "output_tokens_details": {"reasoning_tokens":5}
}
```

每个 Responses SSE payload 都包含 `type` 和从 0 单调递增的 `sequence_number`：

| 事件 | 事件字段 |
| --- | --- |
| `response.created`、`response.in_progress` | `response` shell |
| `response.output_item.added`、`response.output_item.done` | `output_index`、`item` |
| `response.reasoning_summary_part.added`、`response.reasoning_summary_part.done` | `item_id`、`output_index`、`summary_index`、`part` |
| `response.reasoning_summary_text.delta` | `item_id`、`output_index`、`summary_index`、`delta` |
| `response.reasoning_summary_text.done` | 上述索引与 `text` |
| `response.content_part.added`、`response.content_part.done` | `item_id`、`output_index`、`content_index`、`part` |
| `response.output_text.delta` | `item_id`、`output_index`、`content_index`、`delta`、`logprobs:[]` |
| `response.output_text.done` | 上述索引与 `text`、`logprobs:[]` |
| `response.output_text.annotation.added` | `item_id`、`output_index`、`content_index`、`annotation_index`、`annotation` |
| `response.function_call_arguments.delta` | `item_id`、`output_index`、`delta` |
| `response.function_call_arguments.done` | `item_id`、`output_index`、`arguments`、`name` |
| `response.image_generation_call.in_progress`、`response.image_generation_call.completed` | `item_id`、`output_index` |
| `response.code_interpreter_call.in_progress`、`response.code_interpreter_call.interpreting`、`response.code_interpreter_call.completed` | `item_id`、`output_index` |
| `response.code_interpreter_call_code.delta` | `item_id`、`output_index`、`delta` |
| `response.code_interpreter_call_code.done` | `item_id`、`output_index`、`code` |
| `response.web_search_call.in_progress`、`response.web_search_call.searching`、`response.web_search_call.completed` | `item_id`、`output_index` |
| `response.completed`、`response.incomplete`、`response.failed` | 完整 `response` |

web search 发生时，search call item 排在 message 前；无 grounding query 时只输出 message。已产生的 delta 在 `response.failed` 前保持原顺序。

### Anthropic Messages

`POST /v1/messages` 请求字段：

| 字段 | 类型与语义 |
| --- | --- |
| `model` | 必需模型 ID |
| `messages` | 必需非空 `{role,content}` 数组 |
| `system` | string 或 text block 数组 |
| `max_tokens` | 必需正整数 |
| `stop_sequences` | string array |
| `stream` | boolean |
| `temperature`、`top_p`、`top_k` | 生成参数 |
| `tools`、`tool_choice` | custom/server tools 与 auto/none |
| `thinking` | `{type:"enabled",budget_tokens:<INT>}` |
| `output_config` | `{effort:"..."}` |

message content 可以是 string 或 block 数组：

| block type | 字段 |
| --- | --- |
| `text` | `text` |
| `thinking` | `thinking`、`signature` |
| `redacted_thinking` | `data` |
| `image`、`document` | `source:{type,media_type,data,url}` |
| `tool_use` | `id`、`name`、object `input` |
| `tool_result` | `tool_use_id`、`content`、`is_error` |

`image` / `document` 的 Base64 source 使用 `type:"base64"`、`media_type`、`data`；URL source 使用 `type:"url"` 与非空 `url`，省略 media type 时 image 默认 `image/*`、document 默认 `application/pdf`。`tool_result.is_error=true` 把合法 JSON content 包装为 `{"error":<CONTENT>}`；普通标量或数组结果包装为 `{"result":<CONTENT>}`。

custom tool 为 `{name,description,input_schema}`，可选 `type:"custom"`。server tool 字段：

| type | 必需 name |
| --- | --- |
| `web_search_20250305` | `web_search` |
| `image_search` | `image_search` |
| `web_fetch_20250910` | `web_fetch` |
| `code_execution_20250522`、`code_execution_20250825` | `code_execution` |
| `url_context` | `url_context` |
| `google_maps` | `google_maps` |

server tool 只接受对应 `type` 与 `name`。`description`、`input_schema` 或额外 option 返回 `invalid_request_error`。tool choice 接受省略、`{"type":"auto"}`、`{"type":"none"}`；`any` 和 named `tool` 返回 400。

非流式响应：

```json
{
  "id": "msg_...",
  "type": "message",
  "role": "assistant",
  "model": "gemini-example",
  "content": [
    {"type":"thinking","thinking":"...","signature":"..."},
    {"type":"text","text":"..."},
    {"type":"tool_use","id":"call_01","name":"get_weather","input":{}}
  ],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "provider_model": "gemini-provider-id",
  "provider_finish_reason": "provider_19",
  "usage": {"input_tokens":10,"output_tokens":20}
}
```

content 输出 block 为 `text`、`thinking`、`redacted_thinking` 或 `tool_use`。stop reason 为 `end_turn`、`tool_use`、`stop_sequence`、`max_tokens`、`pause_turn` 或 `refusal`。`POST /v1/messages/count_tokens` 接受同一 message/system/tools 输入并返回 `{"input_tokens":<INT>}`。

生成媒体、代码执行与来源不产生 Anthropic 专用媒体或 citation block：媒体编码为 text block 中的 Markdown data URL，代码编码为 fenced text，来源在末尾追加 `Sources:` Markdown 列表。

Anthropic SSE：

| 事件 | payload |
| --- | --- |
| `message_start` | `{type,message:{id,type,role,model,content:[],stop_reason:null,stop_sequence:null,usage}}` |
| `content_block_start` | `{type,index,content_block}` |
| `content_block_delta` | `{type,index,delta}` |
| `content_block_stop` | `{type,index}` |
| `message_delta` | `{type,delta:{stop_reason,stop_sequence,provider_finish_reason?},usage}` |
| `message_stop` | `{type:"message_stop"}` |
| `error` | `{type:"error",error:{type,message}}` |

delta 联合类型为 `text_delta{text}`、`thinking_delta{thinking}`、`signature_delta{signature}`、`input_json_delta{partial_json}`。thinking signature 在对应 thinking block 关闭前发送；redacted thinking 使用一个 start/stop block；tool_use 先发送空 input，再通过 `input_json_delta` 发送完整参数 JSON。

### Gemini GenerateContent

`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent` 与 `:countTokens` 接受：

```json
{
  "contents": [{"role":"user","parts":[{"text":"Hello"}]}],
  "systemInstruction": {"role":"user","parts":[{"text":"Be concise"}]},
  "generationConfig": {},
  "tools": [],
  "toolConfig": {}
}
```

Content 字段为 `role` 与 `parts`。Part oneof：

| Part | 字段 |
| --- | --- |
| text/thought | `text`、可选 `thought`、`thoughtSignature` |
| inline data | `inlineData:{mimeType,data}` |
| file data | `fileData:{mimeType,fileUri,displayName}` |
| function call | `functionCall:{id,name,args}` |
| function response | `functionResponse:{id,name,response}` |
| executable code | `executableCode:{language,code}` |
| code result | `codeExecutionResult:{outcome,output,error}` |

`generationConfig` 全字段：

| 类别 | 字段 |
| --- | --- |
| sampling | `temperature`、`topP`、`topK`、`frequencyPenalty`、`presencePenalty`、`seed` |
| output limits | `candidateCount`、`maxOutputTokens`、`stopSequences` |
| log probabilities | `responseLogprobs`、`logprobs` |
| structured output | `responseMimeType`、`responseSchema`、`responseJsonSchema` |
| modalities | `responseModalities` |
| image | `imageConfig:{aspectRatio,imageSize}` |
| thinking | `thinkingConfig:{thinkingBudget,thinkingLevel}` |
| transcription | `transcriptionConfig:{languageCodes,customVocabulary,wordTimestamps,speakerLabels,smartTranscription}` |
| speech | `speechConfig` |

`responseModalities` 只接受 `TEXT`、`IMAGE` 与 `AUDIO`。`speechConfig.voiceConfig` 与 `multiSpeakerVoiceConfig` 互斥；单声音必须提供 `prebuiltVoiceConfig.voiceName`，每个多说话人条目必须提供非空 `speaker` 与 `voiceConfig.prebuiltVoiceConfig.voiceName`。`transcriptionConfig.smartTranscription=true` 与显式 true 的 `wordTimestamps` 或 `speakerLabels` 互斥；language code `detect` 归一为空自动检测。

单声音 speech config：

```json
{"voiceConfig":{"prebuiltVoiceConfig":{"voiceName":"Kore"}}}
```

多说话人：

```json
{
  "multiSpeakerVoiceConfig": {
    "speakerVoiceConfigs": [{
      "speaker": "Speaker A",
      "voiceConfig": {"prebuiltVoiceConfig":{"voiceName":"Kore"}}
    }]
  }
}
```

tool group 字段：

| 工具 | 字段 |
| --- | --- |
| functions | `functionDeclarations:[{name,description,parameters,parametersJsonSchema}]` |
| search | `googleSearch` 或 `googleSearchRetrieval` |
| URL | `urlContext` |
| code | `codeExecution` |
| maps | `googleMaps` |
| image search | `imageSearch` |

`googleSearch.searchTypes` 可以包含 `webSearch` 与 `imageSearch` 空对象；未提供或两项均未启用时默认 web search。`timeRangeFilter.startTime/endTime` 使用 RFC 3339 Nano。`googleSearchRetrieval` 只接受空对象。tool choice 位于 `toolConfig.functionCallingConfig:{mode,allowedFunctionNames}`，接受 `AUTO` 与 `NONE`；`ANY` 或非空 `allowedFunctionNames` 返回 400。

`:countTokens` 返回：

```json
{"totalTokens": 123}
```

非流式生成响应：

```json
{
  "candidates": [{
    "content": {"role":"model","parts":[]},
    "index": 0,
    "finishReason": "STOP",
    "finishMessage": "...",
    "groundingMetadata": {},
    "citationMetadata": {}
  }],
  "modelVersion": "gemini-provider-id",
  "responseId": "request-id",
  "usageMetadata": {
    "promptTokenCount": 10,
    "candidatesTokenCount": 20,
    "thoughtsTokenCount": 5,
    "toolUsePromptTokenCount": 0,
    "totalTokenCount": 35
  }
}
```

输出 Part 使用与输入相同的 `text`、`thought`、`thoughtSignature`、`inlineData`、`fileData`、`functionCall`、`executableCode`、`codeExecutionResult`。转录文本可以携带：

```json
{
  "text": "...",
  "transcriptionMetadata": {
    "speaker": "Speaker 1",
    "timestamps": [{
      "start":{"seconds":0,"nanos":0},
      "end":{"seconds":1,"nanos":250000000}
    }]
  }
}
```

`groundingMetadata` 字段为 `searchEntryPoint`、`groundingChunks`、`groundingSupports`、`retrievalMetadata`、`webSearchQueries`、`googleMapsWidgetContextToken`。`searchEntryPoint` 包含 `renderedContent`、`sdkBlob`；`groundingChunks` 元素的 oneof 为 `web:{uri,title}`、`retrievedContext:{uri,title,text}` 或 `maps:{uri,title,text,placeId}`；`groundingSupports` 元素包含 `segment:{partIndex,startIndex,endIndex,text}`、`groundingChunkIndices` 和可选 `confidenceScores`；`retrievalMetadata` 包含 `googleSearchDynamicRetrievalScore`。`citationMetadata.citationSources` 的元素包含 `uri`、`title`、`startIndex`、`endIndex`。

`:streamGenerateContent` 使用 SSE。每个语义事件发送一个部分 `GenerateContentResponse`，包含 `responseId`、`modelVersion` 与一个 candidate Part、grounding 或 citation；最后一帧包含 candidate `finishReason`、可选 `finishMessage` 和 `usageMetadata`。响应头后的错误帧为 `data: {"error":{"code","message","status"}}`。

### Files、Transcribe 与媒体

`POST /v1/files` 接受 multipart `file` 与 `purpose`，两者可以按任意顺序到达。文件上限 512 MiB，请求额外允许 1 MiB multipart overhead；普通 scalar part 上限 64 KiB。filename、非空 file 和非空 purpose 必需。Content-Type 为空或 `application/octet-stream` 时从文件前缀检测 MIME。

File object：

```json
{
  "id": "file_...",
  "object": "file",
  "bytes": 1234,
  "created_at": 0,
  "filename": "document.pdf",
  "purpose": "assistants",
  "status": "processed"
}
```

`POST /v1/files` 与 `GET /v1/files/{id}` 返回该对象。`GET /v1/files/{id}/content` 返回原始 body，并设置 `Content-Type`、attachment `Content-Disposition` 和已知时的 `Content-Length`。`DELETE /v1/files/{id}` 返回：

```json
{"id":"file_...","object":"file","deleted":true}
```

未知文件返回 404 `file_not_found`，超过大小限制返回 413 `file_too_large`。Drive file 绑定创建账户；跨账户生成会临时复制文件并在本次尝试结束后清理副本。

`POST /v1/audio/transcriptions` multipart 字段：

| 字段 | 取值与处理 |
| --- | --- |
| `file` | 必需非空；`audio/*`、`video/mp4`、`video/webm`；最大 512 MiB |
| `model` | 默认 `gemini-3.5-transcribe`；接受可选 `models/` 前缀 |
| `response_format` | `json`、`text`、`verbose_json`、`diarized_json`；默认 `json` |
| `language` | 语言码；`detect` 与 `auto` 映射为空自动检测 |
| `temperature` | `0..2` |
| `custom_vocabulary` | 可重复文本字段或 JSON string array |
| `word_timestamps`、`speaker_labels`、`smart_transcription` | `true` 或 `false` |
| `prompt` | 当前 wire 无对应字段，非空值返回 400 |

`smart_transcription=true` 与显式 true 的 word timestamps 或 speaker labels 互斥；custom vocabulary 与 word timestamps 互斥。每次账户尝试创建临时 Drive file，生成结束、失败或取消后在同账户清理。

`text` 格式返回 `text/plain; charset=utf-8`。`json` 返回 `{"text":"...","usage":...}`。详细格式：

```json
{
  "task": "transcribe",
  "language": "en",
  "duration": 1.25,
  "text": "Hello",
  "segments": [{"id":0,"start":0,"end":1.25,"text":"Hello","speaker":"Speaker 1"}],
  "words": [{"word":"Hello","start":0,"end":1.25,"speaker":"Speaker 1"}],
  "usage": {"input_tokens":10,"output_tokens":2,"total_tokens":12}
}
```

`verbose_json` 与 `diarized_json` 使用同一详细对象形状。`language` 是规范化后的请求语言；`detect` / `auto` 时为空并省略。`segments` 来自 response Part field 23；单词数与 timestamp span 数一致时生成 `words`。

`POST /v1/images/generations`：

| 字段 | 取值与处理 |
| --- | --- |
| `model`、`prompt` | 必需 |
| `n` | 默认与唯一值 `1` |
| `size` | `auto`、`1024x1024`、`1536x1024`、`1024x1536` |
| `quality` | `auto`；`low/standard=1K`、`medium/hd=2K`、`high=4K` |
| `response_format` | `b64_json` 返回 Base64；其他值返回 data URL |

响应为 `{"created":<UNIX>,"data":[{"b64_json":"...","revised_prompt":"..."}]}` 或 `{"created":<UNIX>,"data":[{"url":"data:<MIME>;base64,...","revised_prompt":"..."}]}`。`revised_prompt` 只在上游同时返回文本时出现。

`POST /v1/audio/speech`：

| 字段 | 取值与处理 |
| --- | --- |
| `model`、`input` | 必需 |
| `voice` | 默认 `Zephyr` |
| `response_format` | 默认 `wav`；支持 `wav`、`pcm`，上游已经返回 `audio/mpeg` 时支持 `mp3` |
| `speed` | 省略/`0` 或 `1` |
| `instructions` | 以 `instructions + "\n\n" + input` 形成提示 |

`pcm` 返回上游 PCM body 与 MIME；`wav` 要求上游 `audio/l16` 和有效 rate，再封装 16-bit WAV；响应设置 `Content-Type` 与 `Content-Length`。

### Video

OpenAI `POST /v1/videos` 接受 JSON 或 multipart：

| 字段 | 取值与处理 |
| --- | --- |
| `model`、`prompt` | 必需 |
| `seconds` | 整数字符串；默认 4 |
| `size` | `1280x720`、`720x1280`、`1792x1024`、`1920x1080`、`1024x1792`、`1080x1920` |
| `input_reference` | JSON 中为 file ID/data URL；multipart 中为文件 |

OpenAI video object：

```json
{
  "id": "operation-id",
  "object": "video",
  "model": "veo-example",
  "status": "queued",
  "progress": 0,
  "created_at": 0,
  "size": "1280x720",
  "seconds": "4"
}
```

`GET /v1/videos/{id}` 返回当前对象。完成时 status 为 `completed`、progress 为 `100`；上游 done 且无 file 时 status 为 `failed`。`GET /v1/videos/{id}/content` 接受省略或 `variant=video`，未完成时返回 409 `video_not_ready`；成功下载设置媒体 `Content-Type`、`attachment; filename="video.mp4"` 和已知的 `Content-Length`。

视频创建成功后按 operation ID 写入以下资源绑定：

```json
{
  "kind": "video-operation",
  "created_at": "2026-01-01T00:00:00Z",
  "video": {
    "model": "veo-example",
    "seconds": "4",
    "size": "1280x720"
  }
}
```

`model`、`seconds`、`size` 与 UTC `created_at` 随创建账户持久保存，后续轮询从资源绑定恢复；生成服务或进程重新启动后，OpenAI POST 与 GET 仍返回相同字段。OpenAI 请求显式提供 `size` 时原样保存该公开值，省略时按默认 16:9、720p 保存 `1280x720`。Gemini 请求保存归一化输出尺寸：720p 为 `1280x720` 或 `720x1280`，1080p 为 `1920x1080` 或 `1080x1920`，4k 为 `3840x2160` 或 `2160x3840`。公开对象的 `created_at` 是绑定创建时间的 Unix 秒。

Gemini `:predictLongRunning` 请求：

```json
{
  "instances": [{
    "prompt": "...",
    "image": {
      "inlineData": {"mimeType":"image/jpeg","data":"..."}
    }
  }],
  "parameters": {
    "numberOfVideos": 1,
    "sampleCount": 1,
    "aspectRatio": "16:9",
    "durationSeconds": 4,
    "resolution": "720p"
  }
}
```

`instances` 必须只有一个非空 prompt；image 必须在 `inlineData` 与 `fileData:{mimeType,fileUri}` 中选择一个。`numberOfVideos` 为 0 时读取 `sampleCount`，两者均为 0 时默认 1，最终只接受一个结果。`durationSeconds` 接受 JSON integer 或十进制字符串。duration、aspect ratio 与 resolution 省略时分别为 `4`、`16:9`、`720p`，并与实时模型的 `video_durations_seconds`、`video_aspect_ratios`、`video_output_resolutions` 校验。创建返回 `{"name":"operations/<ID>"}`。Gemini operation 使用同一份持久元数据恢复创建账户与轮询上下文；Gemini 响应只包含 `name`、`done` 与完成后的 `response.generateVideoResponse.generatedSamples`。

`GET /v1beta/operations/{id}`：

```json
{
  "name": "operations/<ID>",
  "done": true,
  "response": {
    "generateVideoResponse": {
      "generatedSamples": [{
        "video": {"uri":"http://<HOST>/v1/videos/<ID>/content","mimeType":"video/mp4"}
      }]
    }
  }
}
```

`response` 只在 done 时出现；done 且无产物时 `generatedSamples` 为空。operation 与结果 file 绑定创建账户。

### Live 与 Robotics 公开帧

连接升级后 10 秒内发送 setup：

```json
{
  "type": "setup",
  "model": "gemini-live-model",
  "input_modalities": ["text", "audio", "image"],
  "output_modalities": ["audio"],
  "tools": [{"name":"get_weather","description":"...","parameters":{"type":"object"}}],
  "session_token": ""
}
```

Live input modalities 是 text/audio/image 的非空子集，output 必须为 `["audio"]`。Robotics input/output 必须分别为 `["text"]` 与 `["text"]`。数组不接受空字符串或重复项。

setup 之后的客户端对象统一字段为 `type`、可选 `text`、`mime_type`、Base64 `data`、`tool_responses`。各帧字段：

| type | 字段 |
| --- | --- |
| `text` | `text`，且 setup 声明 text |
| `audio` | `mime_type:"audio/pcm"`、`data`；MIME 省略时使用该默认值 |
| `image` | `mime_type:"image/jpeg"`、`data`；MIME 省略时使用该默认值 |
| `media_end` | 无附加字段 |
| `tool_response` | `tool_responses:[{id,name,content}]`，setup 必须声明 tools |
| `close` | 无附加字段 |

服务端对象字段全集：

```json
{
  "type": "text",
  "model": "gemini-live-model",
  "text": "...",
  "mime_type": "audio/l16;rate=24000",
  "data": "<BASE64>",
  "transcription": {"text":"...","finished":true,"duration_ms":1000,"language_code":"en"},
  "tool_call": {"id":"call-1","name":"get_weather","arguments":{}},
  "tool_call_ids": ["call-1"],
  "session_token": "...",
  "resumable": true,
  "raw": {},
  "error": "...",
  "code": "...",
  "retryable": true
}
```

按 type 使用对应字段：`session_opened{model}`、`setup_complete`、`text{text}`、`media{mime_type,data}`、`input_transcription/output_transcription{transcription}`、`tool_call{tool_call}`、`tool_call_cancellation{tool_call_ids}`、`interrupted`、`generation_complete`、`turn_complete`、`session_resumption{session_token,resumable}`、`usage{raw}`、`go_away{raw}`、`provider{raw}`、`closed`、`error{error,code,retryable,raw?}`。

首帧或后续客户端字段无效时发送 `{type:"error",code:"invalid_request",error:"..."}`。上游 Code 7 使用 `bidi_account_denied`；媒体 scope 使用 `bidi_media_account_denied`，两者 `retryable=true`。客户端单帧上限 8 MiB，超限发送 WebSocket close code 1009。每次写操作上限 10 秒；会话结束等待读取和发送 goroutine（Go 协程）的上限各 5 秒。

流式端点统一使用 `text/event-stream`，每个 SSE frame 以空行结束：

| 协议 | 首事件 | 内容序列 | usage | 终止事件 |
| --- | --- | --- | --- | --- |
| OpenAI Chat | assistant role chunk | chat completion delta | `include_usage=true` 时位于 finish chunk 之后 | `data: [DONE]` |
| OpenAI Responses | `response.created`、`response.in_progress` | output item / content part / delta / done | 完成 response 的 `usage` | `response.completed` 或 `response.incomplete` |
| Anthropic | `message_start` | `content_block_start`、delta、`content_block_stop` | `message_delta.usage` | `message_stop` |
| Gemini | candidate Part | `GenerateContentResponse` 增量 | 最后一帧 `usageMetadata` | 最后一帧 finish reason |

上游语义事件间隔达到 10 秒时，四套流式协议发送 SSE 注释帧 `: ping` 并立即 flush。OpenAI Chat 先发送 assistant role chunk，Responses 先发送 `response.created` 与 `response.in_progress`，Anthropic 先发送 `message_start`；这些起始事件在账户调度期间即可到达客户端。Gemini 的首个帧来自上游语义事件或 `: ping`。

Responses 的 `web_search_call` 由实际 grounding query 触发。搜索发生时，SSE 先输出 index 0 的 search call，再输出 index 1 的 message；模型未调用搜索时仅输出 message，并按正文事件逐块发送。上游在正文后失败时，已收到的正文 delta 位于 `response.failed` 之前。

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

Responses 媒体使用 image generation item；Anthropic 媒体使用 text block 中的 data URL Markdown。Anthropic 来源在 text block 末尾使用 `Sources:` Markdown 列表。

OpenAI Chat 使用 Markdown data URL 承载生成图片；客户端把 assistant `message.content` 回传下一轮时，适配器将其中的图片恢复为 inline data Part，保留图片多轮上下文。

用户文本中的 `youtu.be/<ID>`、`youtube.com/watch?v=<ID>`、`/shorts/<ID>`、`/live/<ID>` 和 `/embed/<ID>` 会转换为 `video/*` 外部媒体 part，并从用户 text part 中移除；重复 URL 合并为一个附件。OpenAI `video_url`/`input_video`、Anthropic URL source 与 Gemini `fileData.fileUri` 使用相同的外部媒体编码。

OpenAI Responses 的 `previous_response_id` 在进程内保存最多 256 个响应节点并重建完整 contents；重启后客户端重新提交完整上下文。Drive 与 Veo 资源绑定持久化到磁盘。

`store` 省略或为 `true` 时建立响应节点；`store=false` 返回当前响应并保持既有续接链。

模型、参数、账户与上游错误按下方状态表投影。客户端取消会关闭上游 reader并释放账户租约。

错误对象与状态语义：

| 情况 | HTTP | OpenAI | Anthropic | Gemini |
| --- | ---: | --- | --- | --- |
| 参数、Schema、tool choice 无效 | 400 | `invalid_request` | `invalid_request_error` | `INVALID_ARGUMENT` |
| 没有符合条件的账户 | 400 | `account_required` | `invalid_request_error` | `INVALID_ARGUMENT` |
| 本地 API key 无效 | 401 | `invalid_api_key` | `authentication_error` | `UNAUTHENTICATED` |
| 模型或方法不存在 | 404 | `model_not_found` | `not_found_error` | `NOT_FOUND` |
| 本地文件不存在 | 404 | `file_not_found` | `not_found_error` | `NOT_FOUND` |
| 上游拒绝权限 | 403 | `upstream_error` | `permission_error` | `PERMISSION_DENIED` |
| 视频仍在生成 | 409 | `video_not_ready` | `api_error` | `INTERNAL` |
| 文件超过 512 MiB | 413 | `file_too_large` | `request_too_large` | `INTERNAL` |
| 上游配额或限流 | 429 | `upstream_error` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| 上游过载 | 529 | `upstream_error` | `overloaded_error` | `INTERNAL` |
| 当前客户端请求被管理端取消 | 503 | `request_canceled` | `api_error` | `UNAVAILABLE` |
| 生成服务已停止 | 503 | `service_stopped` | `api_error` | `UNAVAILABLE` |
| 请求期限到期 | 504 | `upstream_error` | `api_error` | `DEADLINE_EXCEEDED` |
| 传输、Content-Type、解码或缺失终态 | 502 | `upstream_error` | `api_error` | `INTERNAL` |

上游 RPC 的 HTTP 404 表示传输或上游失败，默认映射为 502；公开 404 对应本地模型目录或本地资源查找失败。客户端自身断开时，访问日志记录 499 并结束响应写入。

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
| 原始形状 | `[null,[code,message,...]]` | 解析后进入规范错误事件 |

请求生命周期：

| 阶段 | HTTP / SSE 行为 | 资源状态 |
| --- | --- | --- |
| response headers 前失败 | 返回对应 HTTP status 与协议 JSON error | 释放账户槽位 |
| SSE 已开始后失败 | 发送 OpenAI error、`response.failed`、Anthropic `error` 或 Gemini error frame | 关闭上游 reader并释放账户槽位 |
| 完成帧 | 输出 finish reason、usage 与协议终止事件 | 合并 Set-Cookie并释放账户槽位 |
| 客户端取消 | 结束上游读取 | 取消请求上下文并释放账户槽位 |

欢迎二次开发，如果对你有帮助，考虑给仓库点一个Star~
