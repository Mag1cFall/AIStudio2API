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
| `user-agent` | 账户固定指纹的 Firefox UA |
| `x-user-agent` | 官网 gRPC-Web 标识 |
| `x-goog-api-key` | AI Studio 首页或当前官网请求动态值 |
| `x-goog-authuser` | 当前账户官网请求 |
| `x-aistudio-visit-id` | 首页初始化或当前官网请求 |
| `x-aistudio-g1-tier` | `GetAiStudioBenefitTier` 返回值映射为 `TIER0`、`TIER1` 或 `TIER2` |
| `x-goog-ext-519733851-bin` | 当前官网请求动态值；纯 Go WAA 后端由 `GetLoggingContext` 编码 |
| `authorization` | 三段 SAPISID 签名 |
| `cookie` | 当前账户对目标 RPC 可见的 Cookie |
| `origin`、`referer` | `https://aistudio.google.com` |
| `accept-language` | 账户 locale |

请求头 `x-goog-api-key` 是 AI Studio 页面使用的动态公共值，与用户创建的 Google Cloud API key 不同；免费网页链仍依赖 Cookie、SAPISID 签名和 WAA proof。

受 WAA 保护的 `GenerateContent` 通过账户固定指纹 Camoufox 页面发送，保留原生 Firefox TLS、HTTP/2、请求头、Cookie 与页面指纹；其他 MakerSuite 与 Drive 请求使用同账户固定出口的 Go HTTP transport。`WAA_BACKEND=go` 时受保护请求由服务进程以 Firefox 152 网络形状经同一固定出口发送，见 [WAA 实现](waa.md)。

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

Chrome 导入状态在 `storage-state.json` 的 `aistudio2api` 扩展中保存来源、Gaia ID、refresh token 与 wrapped binding key。登录页跳转、签名 Cookie 失效、HTTP `401` 与协议 Code 16 进入同一认证恢复流程，覆盖 Worker 预热、按需启动、普通 RPC、受保护 RPC 与 Live 建连。服务优先在同一账户出口续签 Cookie；保存的 OAuth 材料被拒绝时，从当前 Chrome 匹配原账户邮箱与 Gaia ID，更新来源材料。没有 `aistudio2api` 扩展的账户在本机 Chrome 登录着同一邮箱时，从 Chrome 导入该邮箱的材料并写入扩展，此后按 Chrome 导入账户续签。有效 Cookie 提交后使动态头失效、关闭该账户 WAA runtime，并重放一次。恢复失败的账户标为 `auth_required`，管理事件同步发布账户状态，后续调度使用其他合格账户。

认证恢复等待同账户的正常请求结束；并发失效复用一次提交结果。替换登录材料推进认证代际，旧请求的认证结果按代际和请求顺序写回；生成正常结束后确认认证有效。HTTP `403`、协议 Code 7 与 Drive `unauthorized_client` 保留模型权限或 Drive 授权含义。隔离 Camoufox 登录和外部 storage state 在本机 Chrome 没有同一邮箱时只使用各自的登录材料。

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

Cookie 的 `name`、`value`、`domain`、`path`、`expires`、`httpOnly`、`secure`、`sameSite` 与可选 `partitionKey` 原样持久化；`sameSite` 接受空值、`Lax`、`Strict` 或 `None`。origin 必须包含 scheme 与 host。请求 Cookie 过滤过期项与不匹配的 Secure/domain/path 条目，同名项按 path 长度降序发送；普通 HTTP 响应的 `Set-Cookie` 按 name、domain、path 替换或删除未分区项，同名分区项独立保留。浏览器恢复时，`partitionKey` 映射到 BiDi `storageKey.sourceOrigin`，空值使用默认分区。

### 账户持久状态

| 文件 | 内容 |
| --- | --- |
| `auth/<Google 邮箱>/account.json` | 邮箱、enabled、proxy、locale、timezone |
| `auth/<Google 邮箱>/storage-state.json` | Cookie、localStorage 和可选 Chrome 续签材料 |
| `auth/<Google 邮箱>/camoufox-fingerprint.json` | 账户固定的 navigator、屏幕、字体、语言、地区和时区配置 |
| `auth/<Google 邮箱>/runtime-state.json` | 账户权益、实测模型资格、冷却与 Drive/Veo 资源绑定 |
| `auth/<Google 邮箱>/camoufox-cache/` | 该账户 Camoufox 的 HTTP 磁盘缓存，运行中的 WAA Worker 独占 |
| `auth/.leases/<Google 邮箱>.lock` | 同一账户目录的跨进程占用锁 |
| `auth/.leases/<Google 邮箱>.runtime.lock` | 每次 `runtime-state.json` 读取、合并与写回的短事务锁 |
| `[用户缓存]/AIStudio2API/runtime-leases/<Google 邮箱>.lock` | 当前电脑上该邮箱的 WAA Worker 占用锁 |

Google 邮箱的小写形式同时作为账户目录、管理页面标识和日志来源。新账户的 locale 与 timezone 读取当前电脑设置，管理页面使用浏览器语言和 IANA 时区；CLI 使用操作系统语言和时区。初始化、WAA、MakerSuite、OAuth 续签和 Drive 使用账户固定代理。locale 同时设置 navigator language、Accept-Language 与地区，timezone 设置浏览器时区；重新登录和 WAA runtime 复用同一账户指纹。同一电脑上的多个进程按邮箱共享 WAA runtime lease，调度器只会为未被占用的邮箱创建 Worker。

`runtime-state.json` 根字段为 `cooldowns`、`resources`、`model_access`、`benefit_tier`、`catalog_fingerprint`。cooldown value 为 `{until,reason?}`；model access value 为 `{state,checked_at,reason?}`；resource value 为 `{kind?,name?,mime?,size?,purpose?,created_at,video?:{model,seconds,size}}`。

每次运行状态事务先取得 `auth/.leases/<账户>.runtime.lock`，每 25ms 尝试一次，等待上限为 2 秒；携带调用方 context（Go 取消上下文）的事务在调用方取消或 deadline 更早到达时立即结束。取得锁后重新读取当前磁盘值，只合并目标字段，以临时文件原子替换 `runtime-state.json`，再同步内存状态与资源归属。锁等待失败、读取失败、写回失败和 unlock 失败均返回原始错误链。

调度器按实时模型方法、capability、AccessModes 和账户权益选择候选，优先使用已经成功调用目标 scope（模型资格与冷却的状态记录范围）的预热账户，再按目标模型最近一次真实首事件耗时排序。常驻 Worker 优先覆盖可调用模型更多的账户。预热数量低于上限时提升待机账户，预热账户均忙时等待并发槽位。同账号 WAA proof 串行生成，已准备的 MakerSuite HTTP 请求并发执行；首个活动请求获取 `.leases/<邮箱>.lock`，最后一个释放。Drive file、Veo operation 与产物 file 始终使用创建账户。

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
  -> SHA-256(binding prompt) as lowercase hex
  -> snapshot({TYb:{content:<DIGEST>}})
  -> write fresh proof into request
  -> send protected MakerSuite RPC
```

`WAA_BACKEND=camoufox` 时官方 VM 运行在账户固定指纹的 Camoufox 页面，受保护请求由页面原生 `fetch` 发送；`WAA_BACKEND=go` 时 VM 运行在服务进程内的 goja 与 Firefox 形状宿主中，受保护请求由 Go HTTP 以 Firefox 152 网络形状发送。bootstrap、challenge 解码、解释器、初始化参数、宿主、生命周期、失败处理与上游变化的定位方法见 [WAA 实现](waa.md)。

`Waa/Create` 请求是 JSON+protobuf 数组。Worker 启动时只带 request key，VM 刷新时追加当前 interpreter hash 与上一 VM 的无绑定 snapshot：

```json
["lmnUSbltwc5ULv48iKLX"]
["lmnUSbltwc5ULv48iKLX", "<INTERPRETER_HASH>", "<PREVIOUS_SNAPSHOT>"]
```

响应外层索引 `1` 是 Base64 challenge，解码后每个字节加 `97`，得到 message ID、interpreter、program、全局函数名与 client experiments 状态。interpreter 按 hash 缓存，摘要为 SHA-256 的 Base64URL 无 padding 编码；program 属于当前 Create 生命周期，proof 绑定当前 prompt 摘要与 VM 内部状态，每个请求生成新的 proof。

snapshot 的底层输入是四槽数组，首槽承载 binding：

```javascript
[{content: sha256(bindingPrompt)}, undefined, undefined, undefined]
```

返回值是 `!` 开头的 proof。`GenerateContent` 与 `CreateInteractionStream` 写入 field 5，Build 代理写入 field 3，`GenerateVideo` 写入 field 8，Bidi 的每个客户端 wire 写入 field 6；原请求的其他槽位保持不变。各 RPC 的 binding prompt 见 [WAA 实现](waa.md)，GenerateContent 按 contents 和 parts 原顺序以单个空格连接。

`Waa/Ping` 请求和成功响应：

```json
["lmnUSbltwc5ULv48iKLX", "<BOTGUARD_RESPONSE>"]
[]
```

field 1 是 `request_key`，field 2 是 `botguard_response`。正确 proof、损坏 proof、省略 field 2、错误 request key 与无账户认证均可返回 HTTP 200 和 `[]`。Ping 成功表示 WAA RPC、API consumer identity 与字段类型可达；Worker VM、snapshot proof、binding、账户会话、模型资格和 GenerateContent 接受状态由实际受保护业务 RPC 判定。

生成服务启动时按配置的常驻数与启动并发数预热账户 WAA Worker。一个账户 Worker 为该账户的所有普通生成模型提供 proof，业务模型切换直接复用当前 Worker；同一账户的 snapshot 串行执行。

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
| 78 | 79 | Interaction 配置：索引 2 为后台任务，索引 3 类型 `1` 为 agent、`2` 为模型 |
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
| 59 | private Interaction route | 85 | TTS 分段说话人 |
| 46 | Live 实时翻译 | | |

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
| 84 | `transcription_smart` | 85 | `speech_metadata` |
| 46 | `speech_translation` | | |

每个主能力码都保留为 `capability_code_<N>`，同时为上表中的已知码增加语义键；次能力码保留为 `secondary_capability_code_<N>`。

图片与视频选项使用枚举码：

| 类型 | 码值映射 |
| --- | --- |
| 图片/视频宽高比 | `1=1:1`、`2=9:16`、`3=16:9`、`4=3:4`、`5=4:3`、`6=3:2`、`7=2:3`、`8=5:4`、`9=4:5`、`10=21:9`、`11=9:21`、`12=1:4`、`13=4:1`、`14=1:8`、`15=8:1` |
| 图片分辨率 | `1=1K`、`2=2K`、`3=4K`、`4=512` |
| 视频时长 | `1=5s`、`2=6s`、`3=7s`、`4=8s`、`5=4s` |
| 视频分辨率 | `1=720p`、`2=1080p`、`3=4k`、`4=368p`、`5=360p` |

Veo field 71 的宽高比、时长和分辨率分别位于子索引 `4`、`5`、`9`；子索引 `7` 含 `3` 而子索引 `9` 为空时，分辨率为官网默认的 `720p`、`1080p`、`4k`。TTS field 67 是 repeated voice row，每行索引 `0` 为 voice name。thinking field 72 的默认 level 位于子索引 `5`。

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

每项为 `[null, null, 类别, 阈值]`，编号与 Gemini API 枚举相同：类别 `HARM_CATEGORY_HARASSMENT`=7、`HATE_SPEECH`=8、`SEXUALLY_EXPLICIT`=9、`DANGEROUS_CONTENT`=10、`CIVIC_INTEGRITY`=11；阈值 `BLOCK_LOW_AND_ABOVE`=1、`BLOCK_MEDIUM_AND_ABOVE`=2、`BLOCK_ONLY_HIGH`=3、`BLOCK_NONE`=4、`OFF`=5。非图片模型默认发送上面 7–10 四类 `OFF`，Gemini 协议请求中的类别按名称覆盖或追加；图片模型只发送请求中的类别，没有时为 `null`。上游对 `HARM_CATEGORY_UNSPECIFIED`（0）与 `DEROGATORY` 至 `DANGEROUS`（1–6）返回 400，这些类别与 `HARM_CATEGORY_IMAGE_*`、`HARM_CATEGORY_JAILBREAK` 不发送；其他未知名称在发送前返回 400 并列出可用类别，未知阈值同样在发送前返回 400。

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
| 17 | 18 | media resolution：LOW=`1`、MEDIUM=`2`、HIGH=`3` |
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

- max output 大于模型上限时按上限发送

`reasoning_effort` / `thinkingLevel` / Anthropic `output_config.effort` 接受 `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`，`xhigh` 与 `max` 按 `high` 处理，`THINKING_LEVEL_UNSPECIFIED` 视为未设置。`none` 在只支持 thinking budget 的模型使用预算 0，支持 thinking level 的模型使用最低可用 level。模型只有 thinking level 能力时，显式 budget 按 0、1024、8192 以内与更大预算分别退化为 minimal、low、medium、high；没有 thinking level 能力的模型忽略其余 effort，两类能力都缺少时同时忽略 budget。

response modalities：

`wire` 指发送给上游的原始数组字段。

| 输出 | wire | 默认路由 |
| --- | --- | --- |
| text | `[1]` | chat |
| image | `[2]` | image route |
| image + text | `[2,1]` | 显式组合请求 |
| audio | `[3]` | speech / music route |

AUDIO 采用独立输出模态，与其他模态同时请求时按模型能力取交集。JSON Schema type code 为 string=`1`、number=`2`、integer=`3`、boolean=`4`、array=`5`、object=`6`；schema 支持 format、description、nullable、enum、items、properties、required 和 field 23 `propertyOrdering`。

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
| candidate finish message | `$[0][frame][0][0][3]` | finish message |
| Part text | `...parts[part][1]` | text |
| Part inline data | `...parts[part][2]` | `[mime, base64]` |
| Part function call | `...parts[part][10]` | `[name, Struct, callId?]` |
| Part thought | `...parts[part][12]` | boolean |
| Part signature | `...parts[part][14]` | signature |
| frame usage | `$[0][frame][2]` | usage array |
| frame response ID | `$[0][frame][7]` | response ID |

Part 文本带 `part[12]=true` 时属于 reasoning summary，普通文本属于可见正文；带 `part[12]=true` 的内联图片是图片模型思考过程中的草图，不作为输出媒体返回，最终图片以普通 Part 另行返回；`part[14]` 是 thought signature。签名可以附在文本、函数调用或独立空 Part 上，下一轮必须原样回传：

| 公开协议 | 签名输入 | 签名输出 |
| --- | --- | --- |
| OpenAI Chat | assistant tool call 的 `extra_content.google.thought_signature` | tool call 的同名扩展字段 |
| OpenAI Responses | `reasoning.encrypted_content` 紧邻后续 `function_call` | reasoning item 的 `encrypted_content` |
| Anthropic | `thinking` 或 `redacted_thinking` block 的 `signature` | thinking block 的 `signature` |
| Gemini | 数据 Part 或独立 Part 的 `thoughtSignature` | Part 的 `thoughtSignature` |

OpenAI Chat 的 assistant tool call 未带 `extra_content` 时，服务按调用 ID、函数名和参数补回本进程最近签发的签名；查不到时写入 `skip_thought_signature_validator`。

Anthropic redacted thinking block 以 `data` 承载同一份不透明状态；适配器在输入与输出两侧保留该值。流式响应不输出文本之后到达的签名。

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

携带 stop sequence 的 GenerateContent 与 CreateInteractionStream 请求并行执行同账户 `CountTokens`。正文匹配实际序列时，协议核心关闭生成流，使用 `CountTokens` 的输入总数以及已输出的正文、reasoning 和工具统计构造最终 usage；计数失败时仍返回 `stop_sequence` 终态并省略 usage。

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

generation config、安全设置、工具与转写配置在选择账户前按各账户已载入的模型目录校验，任一账户的条目接受即通过。流式请求在写出响应头前等待首个事件：参数错误与首个事件之前的上游错误按非流式返回 HTTP 状态与错误对象；10 秒内没有事件时先写出响应头，此后的错误以各协议的流内错误事件返回。

错误响应根形状为 `[null,[code,message,...]]`。输入被上游拒绝时帧为 `[null,[block_reason,null,message]]`，返回 400，错误消息包含拒绝原因与上游说明。协议核心保留 HTTP 状态、协议 code 与 message；公开适配器映射为 OpenAI、Anthropic 或 Gemini 错误对象。Chat、Responses、Anthropic Messages 与 Gemini GenerateContent 将媒体模型的普通文本作为文本结果输出；专用图片端点要求图片结果。HTTP/协议错误或缺失完成帧形成失败；上游 finish reason 作为正常终态保留并映射到各公开协议。

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
| Google Search | `web_search`、`web_search_preview` | `web_search_*` | `googleSearch`、`googleSearchRetrieval` |
| Image Search | `image_search` | `image_search` | `imageSearch` |
| URL Context | `url_context` | `web_fetch_*`、`url_context` | `urlContext` |
| Code Execution | `code_interpreter` | `code_execution_*` | `codeExecution` |
| Google Maps | `google_maps` | `google_maps` | `googleMaps` |

根 field 7 按请求声明逐项编码，函数声明和各类 Google 工具按上表对应的 Tool entry 编码。模型的工具范围取自实时能力码，模型不支持的函数声明与 Google 工具在发送前丢弃，声明的工具全部丢弃时工具选择按 `auto` 处理；模型没有 function declarations 能力时，历史函数调用与结果改写为 `{"functionCall":{"args":…,"name":…}}`、`{"functionResponse":{"name":…,"response":…}}` 文本。根 field 14 为 ToolConfig；请求同时携带函数声明与 Google 工具时，其 field 3 `include_server_side_tool_invocations` 为 `true`，由上游在同一轮内执行 Google 工具并返回函数调用。

编码器将全部函数声明合并为一个 Tool entry；Google Search 与 Image Search 合并为一个 search entry 并分别占用 `searchTypes` 索引 `0/1`；Code Execution、URL Context 与 Maps 各占一个 entry。Google Maps 与 Code Execution/URL Context 构成互斥工具组，每个请求选择其中一组。Anthropic server tool 的定义规则见“Anthropic Messages”节。

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

Playground 的函数参数载体和结构化输出 Schema 使用以下 protobuf fields：

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

结构化输出 Schema 归一化规则：

| 输入结构 | 编码结果 |
| --- | --- |
| 可选字段显式 `null` | 移除未设置字段；`example:null` 保留为数据值，`const` 按常量规则校验，属性名称保留 |
| 零参数函数的空定义、`null`、`{}` 或 `true` | object 参数结构 |
| 开放的 `{}`、`true` 与缺少元素定义的 array | 开放节点 `TYPE_UNSPECIFIED` |
| Gemini `type:"TYPE_UNSPECIFIED"` | 按无类型节点处理 |
| 非负整数约束写成字符串 int64 或 `1.0` | 按整数编码 |
| `items:false` | 空数组约束 `maxItems=0`；与正数 `minItems` 同时设置时返回参数错误 |
| `not:false` | 移除空否定约束 |
| `not:true`、`not:{}`、根 `false`、必填属性的 `false` | 返回禁止所有值的参数错误 |
| `not:{type:"null"}` | 设置 `nullable=false` |
| 字符串或字符串数组形式的 `not` | 字符串枚举排除约束 |
| `$schema`、`default`、`additionalProperties`、`exclusiveMinimum`、`propertyNames`、`prefixItems` | 从 wire schema 中省略 |
| `type: [T, "null"]` | 根类型 `T` 与 `nullable=true` |
| `anyOf` / `oneOf` 的 null 分支 | 移除 null 分支并设置 `nullable=true` |
| 多个非 null `type` | 开放根节点与完整类型集合的 `anyOf` |
| 组合 Schema 缺少根 `type` | 相同类型的分支推导根类型；混合类型保留开放根节点，相同 `items` 可写入根节点 |
| 其他节点缺少 `type` | 含 `properties` 为 object，含 `items` 或 `prefixItems` 为 array；字符串约束推导 string，其余为开放节点 |
| array 缺少 `items` | `prefixItems` 中带类型的项组成 `anyOf`；其余使用开放元素节点 |
| 其他 Schema 字段 | 按层级与类型编码，完整 Schema 写入说明 |

结构化输出以客户端的 JSON Schema 为输出契约：无法直接编码的 Schema（如数值 `const`、单独的 `null` 类型）按层级与类型编码，根说明附完整 Schema，两个通道使用同一结果。含开放节点的结构化输出在启用 Build 时使用 Build 通道并原样发送开放节点，没有账户能经 Build 服务时使用 Playground；Playground 将开放节点补为 string，根说明附完整 Schema。非对象 Schema 返回参数错误。

函数声明以客户端的 JSON Schema 为参数契约。Playground 对开放节点、混合联合、引用、数值枚举与常量生成可发送的参数载体，将完整 Schema 放入工具说明；普通可表达声明保持原编码，历史调用与工具结果保持原值。Build 使用 `parametersJsonSchema`，保留组合分支与约束。`strict:true` 和 Gemini `VALIDATED` 在完整工具事件返回前校验原始参数契约；不符合契约的上游调用返回协议错误。本地无法编译的 Schema（如外部 URL 引用）只随声明发送，不做本地校验。

`auto` 由模型决定调用，`none` 省略 tools。指定函数使用声明子集；Playground 将调用要求写入生成指令并核对实际返回，Build 使用原生 `functionCallingConfig`。客户端工具选择映射如下：

| 公开协议 | 自动与关闭 | 要求调用 |
| --- | --- | --- |
| OpenAI Chat / Responses | 默认、`auto`、`none` | `required`、named function |
| Anthropic | 默认、`auto`、`none` | `any`、named `tool` |
| Gemini | 默认、`AUTO`、`NONE`、`VALIDATED` | `ANY` 与 `allowedFunctionNames` |

函数调用响应 Part 为 `[name, Struct, callId?]`；下一轮 function result 使用同一形状并原样带回 thought signature。tool result 显式提供函数名时保留该值；缺少名称时，先按 call ID 关联当前轮尚未返回结果的调用，未匹配且仅剩一个调用时使用其名称。每个结果对应一个调用，调用与结果之间的助手文本不影响关联，新一轮普通对话开始后重新建立关联；存在歧义或缺少调用记录时返回参数错误。函数参数和结果使用 JSON object，非对象的参数与结果封装为 `{"result":<VALUE>}`，与 Build 的 Gemini API 编码相同。

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

Drive token、上传和下载使用文件所属账户的固定出口。文件 ID 与账户绑定写入 `runtime-state.json`；生成请求可以组合不同账户的文件，其他账户的文件会临时复制到本次生成账户；失败尝试的副本随即回收，成功尝试的副本在请求结束后回收。

生成请求的内联附件优先上传到本次生成账户，正文使用 Drive file Part。`GenerateAccessToken` 明确返回 `401`、Code 16 和 `OAuth error: unauthorized_client` 时，正文保留原始 inline data Part，账户继续参与普通生成调度；其他令牌错误保持失败语义。上传使用公开适配器输出的 MIME 和字节内容。图片、音频、视频、PDF 等附件的支持范围由所选模型决定。普通文本和 YouTube 外部媒体保持各自的 Part 编码。

`POST /v1/files` 的未知长度上传使用 Drive resumable upload 和 8 MiB 分块，`GET /v1/files/{id}` 从资源绑定读取文件名、大小、purpose 与创建时间；客户端取消会终止上传并释放账户租约。公开文件字段见“Files、Transcribe 与媒体”节。

### Gemini 3.5 Transcribe

`gemini-3.5-transcribe` 使用 Drive file Part 与 GenerateContent generation config field 32。转录配置子字段如下：

| protobuf field | 内容 |
| ---: | --- |
| 5 | word timestamps，启用值为 `1` |
| 6 | speaker labels，启用值为 `1` |
| 7 | repeated custom vocabulary |
| 8 | repeated language codes |
| 9 | smart transcription，启用值为 `2` |

转录元数据位于响应 Part field 23，子字段 1 为文本、2 为 speaker label、3 为 repeated timestamp span。每个 span 的 field 2 与 field 3 分别是开始和结束时间，时间消息使用 seconds 与 nanos。公开转录端点见“Files、Transcribe 与媒体”节。

### Nano、TTS 与 Lyria

三类媒体复用 `GenerateContent`：

| 路由 | generation config | 响应 |
| --- | --- | --- |
| Nano image | modalities `[2]`，image config `[aspectRatio?, imageSize?]` | Part field 3 `[mime, base64]` |
| TTS | modalities `[3]`，speech config | Part field 3 音频 chunk |
| Lyria | modalities `[3]` | Part field 3 音频 chunk |

单声音 speech config 为 `[[[voiceName]]]`。TTS 模型的请求没有设置声音时使用官网默认声音 `Zephyr`。多说话人 speech config 为 `[null,null,[null,[[speaker,[[voiceName]]],...],mode?]]`，mode `1` 为 `VERBATIM`、`2` 为 `CONVERSATIONAL`。文本 Part field 41 为 SpeechMetadata `[speaker?, style?]`；能力码 85 的 TTS 模型要求多说话人请求的每个文本 Part 带 speaker，且台词不写 `## Transcript:`。相邻且 MIME 相同的音频 Part 按到达顺序拼接。图片宽高比、图片分辨率与 TTS voice 必须来自当前模型能力选项。

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

起始帧的图像来源 oneof 为 inline image 或 Drive file。创建响应 field 1 是 operation ID。轮询请求为 `["<OPERATION_ID>"]`；轮询响应 field 1 是 done，产物 Drive file ID 位于 `$[1][0][0][0]`。operation 与结果 file 均绑定创建账户，再通过 Drive bearer 下载媒体。宽高比、秒数和分辨率取实时模型 field 71 中最接近的值。

### Omni Interaction

模型目录 field 79 类型为 `2` 且不是后台任务的模型（`gemini-omni-1.1-flash`、`gemini-omni-flash-preview`）不接受 `GenerateContent`，四套公开生成接口对这类模型改用 `CreateInteractionStream`，账户选择、Worker 与 WAA 与 GenerateContent 相同，WAA proof 位于 field 5，binding 为发送的全部文本以空格连接：

```json
[1, 1, null, <INTERACTION>, "<WAA_PROOF>", 1]
```

| interaction 索引 | 内容 |
| ---: | --- |
| 6 | system instruction |
| 17 | `["models/<MODEL_ID>", [null,null,null,null,null,<THINKING_LEVEL>,1,<MAX_OUTPUT_TOKENS>]]`，历史中没有模型输出时配置索引 24 为 `[]` |
| 26 | `[[<STEP>...]]`，用户 step 为 `[[<CONTENT>...]]`，模型 step 为 `[null,[<CONTENT>...]]`；文本 content 为 `[[<TEXT>]]`，Drive 附件 content 为 `[null×8,["<DRIVE_FILE_ID>"]]` |
| 53 | 视频输出配置 `[[[null,null,null,[null,null,null,null,null,1]]]]` |

thinking level 取值 MINIMAL=1、LOW=2、MEDIUM=3、HIGH=4，由 `reasoning_effort` 或思考预算按模型支持的等级换算，默认使用目录默认等级。Interaction 请求接受 user、assistant 与 tool 消息，tool 消息按 user 处理，相邻同角色消息合并为一个 step，历史函数调用、函数结果与代码执行改写为 Gemini API JSON 文本，assistant 消息中的附件移到下一个 user step；内联附件先上传到所选账户的 Drive，再以 file ID 引用，文件 ID 不参与 binding。音频附件由上游以 code 3 `Audio input modality is not enabled for this model` 拒绝，映射为 HTTP 400；函数声明与 Google 工具不发送，stop sequences 与 GenerateContent 相同在本地匹配，temperature、topP、topK 与 seed 不发送。

响应为 `[[<EVENT>...], <STATUS>?]`。事件索引 10 的 content delta 中 field 1 为正文、field 5 为视频 `[1,"<BASE64 MP4>"]`、field 6 为思考摘要，分别映射为 text、`video/mp4` 媒体与 reasoning 事件；索引 19 的最终 interaction 状态 `3` 产生 usage（输入、输出、思考、总 token 位于 usage 索引 0、4、8、9）与 `stop` 终态，状态 `4`、`5` 返回错误。事件列表后的 google.rpc 状态按 code 映射 HTTP 状态，额度 code 8 为 429；首个正文事件之前的错误进入换号与冷却，之后的错误以流内 error 结束。

### Build 代理

官网 Build 应用经宿主页调用 MakerSuite 代理 RPC 访问 Gemini API，额度与 Playground 分开计算。`UPSTREAM_CHANNELS` 启用 `build` 时，生成请求在 Build 通道上编码为 Gemini API JSON：

```json
["/v1beta/models/<MODEL_ID>:streamGenerateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>"]
["/v1beta/models/<MODEL_ID>:generateContent", "<GEMINI_API_JSON>", "<WAA_PROOF>", "POST"]
```

前者发往 `ProxyStreamedCall`，后者发往 `ProxyUnaryCall`。WAA proof 位于 field 3，binding 为路径与请求体以空格连接后的 SHA-256。两个 RPC 的选择与权益头、模型目录、资格、调度与冷却、请求字段映射、响应解码、错误映射与未接入范围见 [Build 通道](build.md)。Playground 目录没有的 embedding、Live 与实时音乐模型只经 Build：embedding 经 `ProxyUnaryCall` 调用 `:batchEmbedContents`，Live 与实时音乐经宿主页 WebChannel `v1/proxy:proxyBidiStreamedCall` 转发 Gemini Live JSON，每条消息为 `[路径, JSON, WAA proof]`。

### Live 与 Robotics WebSocket

`GET /v1/live` 与 `GET /v1/robotics/stream` 升级为 WebSocket，首个客户端帧为 setup，公开帧与事件见“Live 与 Robotics 公开帧”节。两个入口都使用客户端提供的模型，并由实时模型目录的 `bidiGenerateContent` 方法选择账户；Build 目录以 `bidiGenerateMusic` 提供的模型（如 `lyria-realtime-exp`）为实时音乐会话。Playground 目录没有的 Live 与实时音乐模型经 Build 通道，帧与事件相同，wire 见 [Build 通道](build.md)。上游握手、backchannel 就绪和 setup complete 共用 `INIT_TIMEOUT`，随后依次发送 `session_opened` 与 `setup_complete`。

上游 WebChannel 由握手、前向 POST、长轮询 backchannel 与 terminate 组成。握手 query 使用 `VER=8`、随机 `RID`、`CVER=22`、`X-HTTP-Session-Id=gsessionid` 与 `count=0`；响应 header 给出 gsessionid，首个控制 envelope 为：

```json
[[0,["c","<SID>","",8]]]
```

后续前向请求的 query 字段为 `VER`、`gsessionid`、`SID`、`RID`、`AID`、`zx`、`t`；form 字段依次为 `count=<N>`、`ofs=<OFFSET>`、`req0___data__` 至 `req<N-1>___data__`。客户端消息按到达顺序排队，同一时间只有一个前向 POST，在途期间到达的消息合并进下一个 POST，每个 POST 最多 25 条，与官网页面一致。audio 与 image 帧入队即返回，text、tool_response、media_end 等待自身所在 POST 的 ACK，因此 media_end 返回时此前的媒体帧均已送达；前向 POST 失败后，队列内与后续发送均返回该错误。成功响应是三个整数的 ACK 数组，三个槽只校验整数形状，第二槽不绑定本地 RID、AID 或 ofs。HTTP 200 且 ACK 合法后提交 `RID+1` 与 `ofs+N`；失败、取消或 ACK 无效时保持原值。

backchannel 使用 `RID=rpc`、当前 SID、gsessionid 与 AID。每个网络帧为十进制长度、LF 和对应 JSON 文本，长度按 UTF-16 码元计数：

```text
<DECIMAL_UTF16_LENGTH><LF>
<JSON_TEXT>
```

JSON 根值包含 repeated `[serverAID,payload]` envelope。payload 解码成功并按顺序发布后，将 AID 提交为 `max(currentAID,serverAID)`。首次 backchannel 建立后的临时读取失败保留 SID、gsessionid 与已提交 AID 并重新连接；首次建立失败、协议终态、显式 close 或不可恢复错误进入关闭链。会话状态为 `new -> handshaking -> ready -> reconnecting -> ready`，关闭路径依次进入 `closing -> closed`。

`tool_call` 事件携带单个函数调用；一条上游消息包含多个调用时按顺序发送多条事件。`tool_call_cancellation.tool_call_ids` 携带被取消的调用 ID。新的 `session_resumption.session_token` 原子替换上一枚 token；后续 setup 携带该 token 时绑定原账户恢复。

Live 纯文本与实时音乐使用模型 scope，Live 音频/图像和 Robotics 使用 `bidi-media:<modelID>` scope，经 Build 通道时 scope 前加 `build:`。上游 Code 7 通过 `error` 事件返回并结束当前连接，Code 13 以 HTTP 500 的 `error` 事件结束连接。

上游 Bidi 客户端 wire 使用六槽或七槽稀疏数组。setup 位于外层 field 7：

| setup JSON 索引 | protobuf field | 内容 |
| ---: | ---: | --- |
| 0 | 1 | `models/<MODEL_ID>` |
| 1 | 2 | generation configuration |
| 2 | 3 | tools |
| 6 | 7 | `[sessionToken]`；无 token 时 Live 对话与 Robotics 为 `[]`，实时翻译与转录省略 |
| 7 | 8 | `[104857,[52428]]` buffering 参数；实时翻译与转录省略 |
| 9 | 10 | 输入音频转录参数；空数组，实时转录的语言写在子索引 `7` |
| 10 | 11 | 固定空数组 |
| 15 | 16 | timezone `[null,null,null,null,[zone]]` |

Live 对话与 Robotics 的 configuration 使用 18 槽数组：

| JSON 索引 | 内容 |
| ---: | --- |
| 14 | 输出 modalities；TEXT=`[1]`、AUDIO=`[3]` |
| 15 | Live voice `[[["Zephyr"]]]` |
| 16 | thinking `[1,null,null,level]`；Live Minimal=`4`、Robotics High=`3` |
| 17 | MediaResolution 固定值 `2` |

实时翻译模型不接受 MediaResolution，configuration 使用 31 槽：索引 13 为 `0`、14 为 `[3]`、30 为 TranslationConfig `[echoTargetLanguage 0/1, targetLanguageCode]`。实时转录模型只接受 TEXT 输出，configuration 为 `[null×14,[1]]`。两者均不带 voice 与 thinking。上游在 serverContent 索引 5、6 返回输入与翻译文本，实时转录在索引 10 返回当前累计的临时输入转写，映射为 `interim_input_transcription`，音频结束后在索引 5 返回最终输入转写。

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
| OpenAI Chat | `GET /v1/models`、`GET /v1/models/{id}`、`POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses`、`POST /v1/responses/input_tokens`、`GET /v1/responses/{id}`、`DELETE /v1/responses/{id}`、`GET /v1/responses/{id}/input_items`、`POST /v1/responses/{id}/cancel` |
| OpenAI Embeddings | `POST /v1/embeddings` |
| OpenAI 媒体 | `POST /v1/images/generations`、`POST /v1/images/edits`、`POST /v1/audio/speech`、`POST /v1/videos`、`GET /v1/videos`、`GET /v1/videos/{id}`、`DELETE /v1/videos/{id}`、`GET /v1/videos/{id}/content` |
| Anthropic | `POST /v1/messages`、`POST /v1/messages/count_tokens` |
| Gemini | `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、`:embedContent`、`:batchEmbedContents`、`:predictLongRunning`、`GET /v1beta/operations/{id}` |

扩展端点：

| 协议 | 端点 |
| --- | --- |
| OpenAI 与 Anthropic 文件 | `POST /v1/files`、`GET /v1/files`、`GET /v1/files/{id}`、`GET /v1/files/{id}/content`、`DELETE /v1/files/{id}` |
| OpenAI 转录与翻译 | `POST /v1/audio/transcriptions`、`POST /v1/audio/translations` |
| 实时 WebSocket | `GET /v1/live`、`GET /v1/robotics/stream` |
| Gemini Interactions | `POST /v1beta/interactions`、`GET /v1beta/interactions/{id}`、`DELETE /v1beta/interactions/{id}`、`POST /v1beta/interactions/{id}/cancel`，`/v1` 前缀相同 |
| Gemini 文件 | `POST /upload/v1beta/files`、`GET /v1beta/files`、`GET /v1beta/files/{id}`、`DELETE /v1beta/files/{id}` |

动态路由的注册形状为 `GET /v1/models/{model...}`、`GET /v1beta/models/{model...}`、`GET /v1/responses/{response}`、`DELETE /v1/responses/{response}`、`GET /v1/responses/{response}/input_items`、`POST /v1/responses/{response}/cancel`、`GET /v1/files/{file}`、`GET /v1/files/{file}/content`、`DELETE /v1/files/{file}`、`GET /v1/videos/{video}`、`DELETE /v1/videos/{video}`、`GET /v1/videos/{video}/content`、`POST /v1beta/models/{action}`、`GET /v1beta/operations/{operation}`、`GET/DELETE /v1/interactions/{id}`、`GET/DELETE /v1beta/interactions/{id}`、`POST /v1/interactions/{id}/cancel`、`POST /v1beta/interactions/{id}/cancel` 与 `GET/DELETE /v1beta/files/{file}`；端点表中的 `{id}` 表示对应资源标识。

公开 `/v1` 与 `/v1beta` 接受 `Authorization: Bearer`、`X-API-Key`、`X-Goog-API-Key` 或 `?key=`，读取优先级为 `?key=`、`X-Goog-API-Key`、`X-API-Key`、`Authorization: Bearer`；配置为空时关闭本地 API key 校验，此时 `Origin` 为 `null` 或非 localhost、非回环地址的 http/https 页面请求返回 401，不带 `Origin` 的客户端与其他 scheme 不受限制。`/v1*`、`/upload/` 与 `/health` 响应允许任意 origin，并以 `Access-Control-Expose-Headers: *` 暴露响应头；OPTIONS 预检不经过 API key 校验，允许 `GET/POST/PUT/DELETE/OPTIONS`，`Access-Control-Allow-Headers` 回显请求的 `Access-Control-Request-Headers`。`/v1*` 请求体上限约为 684 MiB，可容纳 Base64 编码的 512 MiB 文件。`/upload/v1beta/files` 使用相同的认证与请求体上限；可续传上传的分块请求发往 start 响应 `X-Goog-Upload-URL` 给出的地址，凭其中的 `upload_id` 鉴权，不需要 API key。

`/api` 控制面在 `ADMIN_AUTH_ENABLED=false` 时要求来源地址为 loopback 且 `Host` 为 localhost 或回环地址；开启登录后，通过管理员账号和密码签发的 Cookie 会话访问。管理请求执行 same-origin 校验并携带 `Cache-Control: no-store`。全部响应携带 `X-Frame-Options: DENY`、`Content-Security-Policy: frame-ancestors 'none'`、`X-Content-Type-Options: nosniff` 与 `Referrer-Policy: no-referrer`。`GET /health` 返回 `{"status":"ok"}`。

`POST /api/auth/login` 接受 `{"username":"<ADMIN_USERNAME>","password":"<ADMIN_PASSWORD>"}`，成功返回 `{"enabled":true,"authenticated":true,"username":"<ADMIN_USERNAME>"}`。会话 Cookie 为 `aistudio_admin`，Path 为 `/api`，使用 HttpOnly、SameSite=Strict；HTTPS 或代理设置 `X-Forwarded-Proto: https` 时附带 Secure，有效期 12 小时。`GET /api/auth/session` 返回相同的状态结构，未登录时账号为空。`POST /api/auth/logout` 返回 204，撤销当前会话并取消关联管理请求。登录失败返回 401，同一来源一分钟内连续失败 5 次后返回 429 与 `Retry-After: 60`。

| 控制能力 | 端点 |
| --- | --- |
| 管理登录 | `GET /api/auth/session`、`POST /api/auth/login`、`POST /api/auth/logout` |
| 状态与模型 | `GET /api/status`、`GET /api/models` |
| 生成服务 | `POST /api/control/start`、`POST /api/control/stop` |
| 账户 | `GET /api/accounts`、`POST /api/accounts`、`GET/POST /api/accounts/import/chrome`、`PUT /api/accounts/{id}`、`DELETE /api/accounts/{id}` |
| 账户认证 | `POST /api/accounts/{id}/login`、`POST /api/accounts/{id}/verify` |
| 远程配对 | `POST /api/pairing`、`POST /api/pairing/accounts` |
| 配置 | `GET /api/config`、`PUT /api/config` |
| 冷却与请求 | `GET /api/cooldowns`、`GET /api/requests`、`POST /api/requests/{id}/cancel` |
| 用量与正文 | `GET /api/usage`、`GET /api/usage/records`、`GET /api/usage/records.csv`、`GET /api/requests/{id}/body` |
| 日志与事件 | `DELETE /api/logs`、`GET /api/events` |

管理 API 使用 DTO（data transfer object）表示请求、响应和事件对象。端点结果：

| 端点 | 成功状态 | body |
| --- | ---: | --- |
| `GET /api/status` | 200 | `AdminStatus` |
| `GET /api/models` | 200 | `{"models":[Model,...]}` |
| `GET /api/accounts` | 200 | `{"accounts":[AdminAccount,...]}` |
| `POST /api/accounts` | 201 | `{"account":AdminAccount}` |
| `GET /api/accounts/import/chrome` | 200 | `{"profiles":[ChromeImportProfile,...]}` |
| `POST /api/accounts/import/chrome` | 201 | `{"accounts":[AdminAccount,...]}` |
| `POST /api/pairing` | 201 | `PairingToken` |
| `POST /api/pairing/accounts` | 201 | `{"account":AdminAccount}` |
| `PUT /api/accounts/{id}` | 200 | `{"account":AdminAccount}` |
| `POST /api/accounts/{id}/login`、`verify` | 200 | `{"account":AdminAccount}` |
| `DELETE /api/accounts/{id}` | 204 | 空 body |
| `POST /api/control/start`、`stop` | 200 | `AdminStatus` |
| `GET /api/config`、`PUT /api/config` | 200 | `RuntimeConfig` |
| `GET /api/cooldowns` | 200 | `{"cooldowns":[AdminCooldown,...]}` |
| `GET /api/requests` | 200 | `{"requests":[AdminRequest,...]}` |
| `POST /api/requests/{id}/cancel` | 204 | 空 body |
| `GET /api/usage` | 200 | `UsageReport` |
| `GET /api/usage/records` | 200 | `UsageRecordPage` |
| `GET /api/usage/records.csv` | 200 | 带 UTF-8 BOM 的 CSV 附件 |
| `GET /api/requests/{id}/body` | 200 | `RequestBody` |
| `DELETE /api/logs` | 204 | 空 body |
| `GET /api/events` | 200 SSE | `{"type":"<TYPE>","data":<DTO>}` |

- `POST /api/pairing/accounts` 的令牌无效或过期时返回 401 `pairing_token_invalid`，账户已存在时返回 409 `account_exists`
- `GET /api/requests/{id}/body` 在没有保存正文时返回 404 `request_body_not_found`

管理 DTO 字段：

| DTO | 字段 |
| --- | --- |
| `AdminStatus` | `state`、`running`、`ready`、`version`、`active_requests`、`accounts` |
| `AdminAccountCounts` | `total`、`ready`、`busy`、`cooldown`、`auth_required` |
| `AdminAccount` | `id`、`label`、`enabled`、`state`、`proxy`、`locale`、`timezone`、`models`、`benefit_tier`、`message` |
| `AccountCreateInput` | `proxy`、`locale`、`timezone` |
| `AccountInput` | `label`、`enabled`、`proxy`、`locale`、`timezone` |
| `ChromeImportProfile` | `id`、`profile`、`display_name`、`email`、`locale` |
| `ChromeImportInput` | `account_ids`、`proxy`、`locale`、`timezone` |
| `PairingToken` | `token`、`expires_at` |
| `AccountStateInput` | `label`、`locale`、`timezone`、`storage_state`、可选 `fingerprint` |
| `AdminCooldown` | `account_id`、`account_label`、`model_id`、`until`、可选 `reason` |
| `AdminRequest` | `id`、`model`、`account_id`、`account_label`、`state`、`started_at` |
| `AdminLog` | `time`、`level`、`source`、`message`、`event`；请求事件携带 `request`，包含 `id`、`state`、HTTP `status`、`model`、`duration_ms`、`tool_calls`、`usage` 与诊断字段，字段口径见 [logging.md](logging.md) |
| `AdminEvent` | `type`、`data` |
| `UsageLatency` | `avg_ms`、`p50_ms`、`p95_ms`、`p99_ms` |
| `UsageStats` | `requests`、`succeeded`、`failed`、`canceled`、`rate_limited`、`input_tokens`、`reasoning_tokens`、`reply_tokens`、`total_tokens`、`duration`、`first_event`、`queue_avg_ms`、可选 `last_at` |
| `UsageReport` | `from`、`to`、`bucket_seconds`、`generated_at`、可选 `latest_at`、`totals`、`previous`、`recent`、`buckets`、`stack_by`、`series`、`groups`、`pairs`、`options` |
| `UsageRecord` | `id`、`time`、`protocol`、`path`、`model`、`account`、`channel`、`status`、`state`、`duration_ms`、`first_event_ms`、`queue_ms`、`input_tokens`、`reasoning_tokens`、`reply_tokens`、`total_tokens`、`tool_calls`、`error`、`attempts`、`has_body` |
| `UsageRecordPage` | `items`、可选 `next_cursor` |
| `RequestBody` | `id`、`time`、`request`、`request_size`、`response`、`response_size` |

`AdminStatus.state` 为 `STOPPED`、`LAUNCHING` 或 `RUNNING`；`running` 只在 `RUNNING` 为 true；`ready` 要求 `RUNNING` 且至少一个账户处于 ready 或 busy；`version` 来自构建信息；`active_requests` 是当前进程请求注册表数量。`AdminAccount.message` 保存当前状态原因，`models` 是该账户实时目录 ID。`until` 与 `started_at` 使用 RFC 3339 JSON time。

`GET /api/usage` 的参数：

| 参数 | 含义 |
| --- | --- |
| `from`、`to` | 必填，RFC 3339 时间 |
| `tz` | IANA 时区，默认 UTC |
| `bucket` | 分桶秒数 `60`、`300`、`900`、`3600`、`86400`，或 `auto` |
| `model`、`account`、`channel`、`protocol`、`state` | 维度筛选，逗号分隔多个取值 |
| `stack` | `series` 的堆叠维度，默认 `model` |

- 统计区间为 `[from, to)`，长度不超过 93 天，分桶数不超过 1500
- `tz` 决定按天分桶的自然日与分钟、小时分桶的对齐
- 同一维度的多个取值为或，不同维度之间为且
- `stack` 取上述五个筛选维度之一

`auto` 按范围长度选择粒度：2 小时以内 1 分钟，12 小时以内 5 分钟，8 天以内 1 小时，更长按天。`buckets` 从包含 `from` 的对齐分桶开始，空分桶补零；`previous` 是紧邻 `from` 之前的等长周期；`recent` 统计最近 5 分钟。`succeeded` 包含 `completed`、`tool_calls`、`limited` 与 `blocked`，`rate_limited` 是 HTTP 429 的失败数；`duration` 与 `first_event` 只统计成功请求，分位数相对误差约 0.5%。`groups` 按 `model`、`account`、`channel`、`protocol`、`state` 与 HTTP 状态码 `status` 列出全部取值，`pairs` 是账户与模型的组合，`series` 取请求数前 8 的取值，其余合并为 `other: true` 的一组。`options` 列出范围内出现过的维度取值，不受筛选影响。`latest_at` 是账本中最新一条记录的完成时间。

`GET /api/usage/records` 与 `records.csv` 接受同样的 `from`、`to` 与维度筛选，另有 `status`（HTTP 状态码）与 `q`（匹配请求 ID 或错误文本的子串）。记录按完成时间与 ID 倒序；`limit` 为 1 到 200，默认 50；`next_cursor` 原样传回 `cursor` 读取更早的一页。`attempts` 列出本次请求在最终结果之前未成功的上游尝试（`account`、`channel`、`error`、`duration_ms`）。CSV 导出范围内全部符合条件的记录，以 `=`、`+`、`-`、`@`、制表符或回车开头的文本加单引号前缀。

Chrome 导入列表按 `Preferences.account_info` 中的 Gaia ID 与邮箱逐个列出账号，同一 Profile 可以包含多个账号，同一邮箱只列出一次。`ChromeImportProfile.id` 为 `<Profile>/<Gaia ID>`；导入读取 `token_service` 中 service 为 `AccountId-<Gaia ID>` 的凭据。管理页列表默认不勾选，并提供全选。CLI 的 `--profile` 导入该 Profile 下的全部账号，交互编号对应单个账号。

`POST /api/pairing` 需要管理会话，签发 10 分钟内有效的配对令牌；再次签发使上一令牌失效。服务只在内存中保存令牌的 SHA-256 摘要，管理进程重启后令牌失效。`POST /api/pairing/accounts` 不经过管理会话、loopback 与 same-origin 检查，凭 `Authorization: Bearer <token>` 导入一个账户，令牌有效期内可多次调用；body 上限 1 MiB。`AccountStateInput.storage_state` 必须能生成 SAPISID 授权，邮箱取认证扩展中的来源邮箱，没有时取 `label`；`fingerprint` 是隔离登录生成的 `camoufox-fingerprint.json` 内容。配对导入的账户使用服务端默认代理。

`AccountCreateInput` 启动隔离 Camoufox 登录，邮箱由 AI Studio 页面读取。`ChromeImportInput.account_ids` 可一次选择多个账号。`AccountInput.label` 必须与不可变的 Google 邮箱 ID 一致，`locale` 与 `timezone` 必须非空，`proxy` 使用 HTTP、HTTPS 或 SOCKS5 origin，可带 `user:password@`，不含 path、query 或 fragment。新增、导入、登录和验证成功后立即刷新该账户模型目录，并发布最新账户与模型事件。

`PUT /api/accounts/{id}` 的提交顺序固定为：校验不可变邮箱 ID，取得账户独占租约，创建未发布的新固定出口，关闭当前 Worker 并把新 Worker 配置标记为 `pending`（尚未发布），在模型目录写锁内原子写入 `account.json` 并更新账户池，随后发布 Worker 配置、替换固定出口、释放租约并重建模型缓存。`account.json` 写入是唯一持久提交点。提交前的出口创建、Worker 关闭或写入错误会丢弃这份待发布配置并保持旧配置；已经关闭的 Worker 由后续请求按旧配置重建。持久写入后，新配置、Worker 配置与固定出口共同成为已提交状态。租约释放错误保留该提交状态并返回原始 unlock 错误；释放成功后记录完成日志并同步模型缓存。

`RuntimeConfig` 字段。`response-only` 表示字段由服务器填充，客户端提交的值不参与保存：

| 字段 | 读写与生效时机 |
| --- | --- |
| `auth_states`、`proxy`、`init_timeout`、`request_timeout` | 保存值；下一次启动生成服务时使用 |
| `warm_worker_limit`、`max_active_workers`、`warm_startup_concurrency`、`per_account_concurrency` | 保存值；下一次启动生成服务时使用 |
| `temporary_chat` | 保存值；下一次启动生成服务时使用 |
| `build_native_nonstream` | 保存值；下一次启动生成服务时决定非流式请求是否优先选择 Build |
| `auto_start` | 保存值；下一管理进程就绪后是否自动启动生成服务 |
| `request_body_log` | 保存值；保存后立即决定是否保存请求与响应正文 |
| `admin_auth_enabled`、`admin_username` | 保存值；下一管理进程使用 |
| `admin_password` | 只写；省略时保留现值，下一管理进程使用 |
| `admin_password_set` | response-only；是否已保存管理密码 |
| `listen_addr`、`proxy_api_key` | 保存值；下一管理进程使用 |
| `active_listen_addr`、`active_proxy_api_key` | response-only；当前管理进程固定值 |
| `management_restart_required` | response-only；保存的监听地址、API key 或管理凭据与当前管理进程不同 |
| `service_restart_required` | response-only；保存的生成服务配置与当前生成服务实例不同 |

`PUT /api/config` 原子保存配置。监听地址、API key 和管理凭据在管理进程重启后生效；账户路径、代理、timeout、容量、临时对话与上游通道选项在 Stop/Start 创建的新生成服务实例中生效。启动时读取最新配置；配置加载、校验、实例创建失败或启用前取消时保留原实例，切换到新实例后由它完成启动或进入 `STOPPED`。

`GET /api/events` 的初始顺序为 `status`、`models`、`accounts`、最近 200 条 `log`、`cooldowns`、按开始时间排序的活动 `request`。后续事件的 `data` 形状：

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

请求状态使用 `queued`、`running`、`completed`、`cancelled`、`failed`。视频状态使用 `queued`、`completed`、`failed`，对应 progress `0` 或 `100`。

模型目录驻留在当前 generation（`runtimeGeneration` 表示的一次生成服务实例）的内存中；正常 Stop/Start 创建的新 generation 以空缓存启动。启动先用当前 generation 的 `catalog.CachedModels()` 建立公开快照，并立即为每个 `enabled` 且处于 `ready` 或 `busy` 的账户启动一个后台 `ListModels` 任务。当前 generation 的真实缓存非空时直接进入 Worker 预热。每个非空结果到达时立即从 `CachedModels` 合并公共目录并发布 `accounts`、`models`；生成服务已经 `RUNNING` 时同时触发 Worker 预热，新 generation 的首个非空结果还会解除启动等待。全账户 fan-out 结束时，`auth_required` 集合发生变化会补发一次账户与模型快照，并记录同步成功数、非空数、公共模型数、待重试账户数和耗时。第一个 Worker 就绪后重新读取当前缓存，并在持有账户与配置变化锁时把服务状态设为 `RUNNING`。

账户状态：

| 状态 | 调度语义 |
| --- | --- |
| `ready` | 认证有效且存在可用槽位 |
| `busy` | 存在独占操作、认证刷新或活动请求 |
| `cooldown` | 全局 `*` 冷却仍有效 |
| `auth_required` | 账户级认证失败 |
| `unavailable` | 当前运行时不可用 |
| `disabled` | 配置已停用 |

- `busy` 账户仍按 `PER_ACCOUNT_CONCURRENCY` 判断剩余槽位
- 模型 scope 冷却不改变账户状态，只影响对应请求的候选分类

认证状态写回携带 `authGeneration` 与 `checkedAt`，仅应用账户对象、`authGeneration` 值与时间均匹配当前状态的结果；相同时间以 `ready` 为最终状态。模型访问和冷却写回携带 `modelAccessGeneration` 与 `checked_at`，仅应用 `modelAccessGeneration` 值匹配且时间不早于当前状态的结果；相同时间以 `verified` 为最终状态。`verified` 表示该账户已经成功调用对应模型或能力。

`ModelAccessKey(scope, model)` 先移除 `models/` 前缀；空 scope 返回 canonical model ID（规范化模型 ID），非空 scope 返回 `<scope>:<canonicalModelID>`。各能力使用以下值：

| 能力 | scope | 成功记录 |
| --- | --- | --- |
| 普通生成 | `<modelID>` | 规范事件 `EventFinish` 到达时写 `verified` |
| CountTokens | `count-tokens:<modelID>` | 清 scope 冷却，保留普通生成资格 |
| Transcribe | `<modelID>` | 非空 text 或 segments 写 `verified` |
| Live 文本 | `<modelID>` | setup complete 写 `verified`；每次 text 开始一次模型资格检查 |
| Live 音频/图像 | `bidi-media:<modelID>` | media 开始媒体资格检查 |
| Robotics | `bidi-media:<modelID>` | text 开始模型资格检查 |

Code 7 保留当前 operation scope 的 verified 状态；认证失败更新账户级 `auth_required`。上传、临时文件清理等非生成失败使用全局 `*` 冷却。

正文、reasoning、工具、usage 与首事件用于输出和性能统计；终态前断流、客户端取消或错误保持原有验证状态。

Bidi setup 成功使用 lease（本次会话持有的账户租约）的 `checkedAt`。此后每个会更新模型资格的轮次分配会话内严格递增的 attempt 时间，`turn_complete` 消费对应 attempt 并写入资格。Code 7 不更新模型资格。Bidi 认证成功与 401 使用相同 attempt 顺序，setup 后较晚返回的 401 可以更新账户为 `auth_required`。

确认浏览器进程退出后，将 process 和 Worker 状态设置为 `closed`。关闭失败时保留 Worker、runtime lease、warm 标记与 generation（Worker 实例版本号）；后续 Stop 即使服务状态已经是 `STOPPED`，也会再次执行 Worker reset。Camoufox 关闭阶段上界为 BiDi 3 秒、进程终止与等待 5 秒、profile 删除 2 秒；各阶段错误使用 `errors.Join` 保留。

模型目录 fan-out 绑定当前生成服务的 context。启动失败、启动取消或 Stop 会取消全部目录任务，并最多等待 2 秒确认后台目录协程退出。Stop 遇到尚未完成的 `LAUNCHING` 或其他启动/停止切换时，等待 transition channel（切换完成通知 channel）的上界为 12 秒；该上界覆盖 2 秒目录退出与有界 Worker 清理，超时错误与清理错误使用 `errors.Join` 返回。

按需热替换先启动 pending Worker（正在启动、尚未发布的替代 Worker），再关闭旧 Worker；旧实例成功退出后，替代 Worker 才成为当前 Worker。旧实例关闭和替代 Worker 回收同时失败时，两者都保留等待再次清理，并各占一个活动容量槽；达到容量上限后停止新建 Worker。完整生成服务 Stop/Start 的顺序为：Start 创建新生成服务实例前先重试停止旧实例，旧 PID 未退出时返回停止错误并保留原实例。

Worker 进程故障、Worker 被替换与协议 Code 5 会重建当前账户 Worker 并在原账户重放一次。

模型目录重试的 pending 集合保存等待再次同步的账户 ID。启动期全账户 fan-out、以及新增、登录或验证后的单账户同步，遇到任意错误或成功返回空目录时加入；返回非空目录时移除；删除账户同时移除。全账户后台同步结束后启动单个 30 秒 ticker（Go 定时器），每次对排序后的待重试账户列表再次并发 fan-out，并在任务开始时复核该 ID 仍在 pending 集合中。错误或空目录继续保留；每个非空成功立即更新账户缓存与公共目录快照、发布 `accounts` 和 `models`，并在 `RUNNING` 状态触发 Worker 预热。批次结束时，`auth_required` 集合发生变化会补发当前账户与模型快照；即时单账户同步无论成功或失败都立即发布当前快照。

模型目录投影：

| 规则 | 结果 |
| --- | --- |
| OpenAI | model list 与同形单个 model 对象 |
| Anthropic | 带 `Anthropic-Version` 时为 Anthropic Models API 格式 |
| Gemini | 模型名称 `models/<ID>` |
| 单模型解析 | 正式 ID 优先于别名 |
| 多账户同模型 | methods、capabilities、能力选项与 access modes 取并集，`paid` 取逻辑 OR |
| 多账户 token limit | 输入和输出上限分别取正数最小值 |
| 模型别名 | 来自 ListModels field 57 |
| 请求匹配 | model ID/alias、method、capability、AccessModes、账户权益与运行状态 |
| 候选排序 | `verified` 与目标模型首事件耗时 |
| capability 约束 | 端点要求与账户实时能力同时命中 |

- 未验证账户仍可进入候选

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
    "paid": true,
    "channels": ["playground", "build"]
  }]
}
```

请求携带 `Anthropic-Version` 时，同一路由返回：

```json
{
  "data": [{"type":"model","id":"gemini-example","display_name":"Gemini Example","created_at":"1970-01-01T00:00:00Z","max_input_tokens":1048576,"max_tokens":65536,"capabilities":null,"lifecycle":"active","line":null,"deprecated_at":null,"retires_at":null}],
  "has_more": false,
  "first_id": "gemini-example",
  "last_id": "gemini-example"
}
```

`max_input_tokens` 与 `max_tokens` 取实时目录的输入与输出 token 上限，目录缺失时为 `null`。`limit` 为 1–1000 的整数，超出范围返回 `400 invalid_request_error`；省略时返回全部模型。`after_id` 返回该模型之后的一页，`before_id` 返回之前的一页，`has_more` 表示该方向是否还有模型，`first_id`、`last_id` 为本页首尾 ID。`lifecycle`（含 `lifecycle[]` 与逗号分隔写法）不包含 `active` 时返回空列表。单模型路由返回与列表项相同的对象，失败响应使用 Anthropic 错误对象。

Gemini `GET /v1beta/models` 返回 `{"models":[...]}`，单模型路由直接返回一个对象。字段为 `name`、`displayName`、`description`、`supportedGenerationMethods`、`inputTokenLimit`、`outputTokenLimit`、可选 `capabilities`、`capabilityOptions`、`accessModes`、`paid`、`channels`。

`GET /v1/models/{model}` 与 `GET /v1beta/models/{model}` 接受 `models/` 前缀；这两个模型查询入口以及生成、计数、视频、转录与 Bidi 同时接受 canonical ID 和 `capability_options.aliases` 中的 alias；alias 在调度和发送上游前换成对应的 canonical ID。

管理模型中的 `description`、token limits、capabilities、capability options、access modes 与 false `paid` 使用 `omitempty`；OpenAI 和 Gemini 响应始终包含身份、methods 与 token limits，并在 map/slice 非空或 `paid=true` 时增加对应扩展字段。

管理目录合并全部账户的上游实时模型集合，并按 ID 排序。公开目录保留至少一个启用账户具有访问资格、且当前公开协议承载其调用方法的模型；启用 Build 通道时同时包含 Build 独有的可生成模型，`channels` 列出可调用该模型的通道，见 [Build 通道](build.md)。短期冷却和忙碌状态由请求调度处理。

主要请求格式：

| 端点 | 必需字段 | 主要结果 |
| --- | --- | --- |
| `/v1/chat/completions` | `model` | Chat completion 或增量 chunk |
| `/v1/responses` | `model` | Response object 或 `response.*` 事件 |
| `/v1/responses/input_tokens` | `model` 或 `previous_response_id` | `{"object":"response.input_tokens","input_tokens":<INT>}` |
| `/v1/files` | multipart `file`、可选 `purpose` | OpenAI file object |
| `/v1/messages` | `model` | Anthropic message 或 message 事件 |
| `:generateContent` / `:streamGenerateContent` | 路径中的模型 | Gemini candidates、usage 与 grounding metadata |
| `/v1/images/generations` | `prompt` | `b64_json` 或 data URL |
| `/v1/audio/speech` | `model`、`input` | MP3、Opus、AAC、FLAC、WAV 或 PCM body |
| `/v1/audio/transcriptions` | multipart `file` | 文本或转录 JSON |
| `/v1/videos` | `prompt` | 长任务对象，随后轮询并下载内容 |

生成与计数请求需要系统提示或对话内容中的至少一项，只有系统提示时以它作为 user 轮发送。请求以 assistant 轮结尾时，末尾连续 assistant 轮的正文与代码执行内容作为前缀写入系统指令，模型从前缀之后续写，响应只返回续写部分；末尾未回传结果的函数调用由模型重新生成。各生成入口与计数接口使用同一转换。

四套生成入口共享同一规范请求，输入映射如下：

| 能力 | OpenAI Chat | OpenAI Responses | Anthropic | Gemini |
| --- | --- | --- | --- | --- |
| system | `system` / `developer` messages | `instructions` 和 system/developer message items | `system` 字符串或 text blocks | `systemInstruction` text parts |
| text | 字符串或 text content part | 字符串、message item | 字符串或 text block | Part `text` |
| image/document | Base64 data URL、`file_id` | `input_image`、`input_file` | base64、URL 或 file source | `inlineData`、`fileData` |
| audio input | `input_audio` Base64 | message content 中的 `input_audio` | base64 document source | `inlineData` |
| YouTube | `video_url` / `input_video` | `input_video` | URL source | `fileData.fileUri` |
| function call | assistant `tool_calls` | `function_call` item | `tool_use` block | `functionCall` Part |
| function result | tool message | `function_call_output` item | `tool_result` block | `functionResponse` Part |
| structured output | `response_format` | `text.format` | — | `responseMimeType` 与 response schema |
| thinking | `reasoning_effort` 或 `reasoning.effort` | `reasoning.effort` | `thinking.budget_tokens`、`output_config.effort` | `thinkingConfig` |

Gemini 附件与 `predictLongRunning` 的图片输入接受 `inlineData` / `inline_data`、`fileData` / `file_data`、`mimeType` / `mime_type` 和 `fileUri` / `file_uri`。同一别名对同时出现时，外层优先选择驼峰对象，内层优先选择非空驼峰值。

媒体 Base64 输入接受标准和 URL-safe 字母表、可选的 `=` 填充，以及 `data:<MIME>;base64,` 前缀。GIF 内联图片和 OpenAI 视频 `input_reference` 表单附件提取首帧，按逻辑画布尺寸与帧偏移编码为 PNG 后发送。透明首帧保留透明背景；不透明首帧的未覆盖区域使用全局色表中的背景色。

OpenAI Chat 与 Anthropic 省略转换后没有 parts 的空历史消息；纯空白文本、工具调用、工具结果及媒体保留原有内容。

生成参数映射：

| 参数 | 规则 |
| --- | --- |
| OpenAI max tokens | `max_completion_tokens` 优先于 `max_tokens` |
| Anthropic max tokens | `max_tokens` 映射 generation config field 4 |
| Gemini max tokens | `maxOutputTokens` 映射 generation config field 4 |
| temperature / topP / topK / seed | 映射 generation config fields 5 / 6 / 7 / 19 |
| stop sequence | 映射 generation config field 2 |
| stop sequence 命中 | 本地匹配并返回实际命中的序列 |
| structured output | MIME type 映射 field 8，Schema 映射 field 9 |
| OpenAI `parallel_tool_calls` | `false` 时本轮最多一个函数调用 |
| Responses `truncation` | `auto` 移除最早的完整对话轮次 |
| Anthropic `thinking` | `enabled` 写入 `budget_tokens`；`adaptive` 使用模型默认思考 |
| Anthropic thinking type | `disabled` 使用模型最低思考配置 |

- OpenAI Chat 与 Responses 的 `parallel_tool_calls` 省略或为 `true` 时允许并行；Responses 同时把该值写入响应元数据
- Responses `truncation:auto` 按上游权威计数移除最早的完整对话轮次，保留最新轮次与工具调用/结果配对；省略或 `disabled` 时保留完整输入
- Anthropic `thinking.type:"disabled"` 同时隐藏思考正文，续接签名保持；未知 type 返回 `400 invalid_request_error`
- Chat、Responses 与 Gemini 的协议专属参数见各协议小节

### OpenAI Chat Completions

`POST /v1/chat/completions` 请求字段：

| 字段 | 类型与语义 |
| --- | --- |
| `model` | 必需模型 ID |
| `messages` | message 数组 |
| `stream` | boolean |
| `stream_options.include_usage` | 在 finish chunk 后发送 usage-only chunk |
| `tools` | function、custom 或 Google server tool 数组 |
| `tool_choice` | 省略/`auto`/`none`/`required`、named function 或 custom 对象、`allowed_tools` |
| `web_search_options` | 对象，开启 Google Search |
| `temperature`、`top_p` | 可选采样值 |
| `max_tokens`、`max_completion_tokens` | 后者优先 |
| `frequency_penalty`、`presence_penalty` | 不影响生成 |
| `n` | 1–128 |
| `parallel_tool_calls` | 省略/`true`/`false` |
| `logprobs`、`top_logprobs`、`logit_bias` | 不影响生成 |
| `stop` | string 或 string array |
| `response_format` | `{type:"text"}`、`{type:"json_object"}` 或 `{type:"json_schema",json_schema:{schema}}` |
| `reasoning_effort` | thinking effort |
| `reasoning.effort` | nested thinking effort |
| `seed` | 64 位整数 |

- `allowed_tools` 的 mode 与工具列表映射为调用模式与允许的函数名，请求只发送列表中的函数与托管工具对应的 Google 工具
- `web_search_options` 的搜索深度与近似位置写入搜索指令
- `n` 大于 1 时每个 choice 独立并发生成，带 `seed` 时第 i 个 choice 使用 `seed+i`；`choices` 按 `index` 排列，各自带 `finish_reason`，`usage` 为各 choice 之和；任一生成失败时整个请求返回该错误；流式请求在首个 choice 就绪后开始输出，其余 choice 的启动错误以流内错误返回
- `frequency_penalty`、`presence_penalty`、`logprobs`、`top_logprobs` 与 `logit_bias` 接受任意值；非流式响应的 `choices[].logprobs` 为 `null`
- `stop` 中的空字符串从停止条件中移除
- `reasoning.effort` 与 `reasoning_effort` 映射到同一配置，两者同时提供时以 `reasoning.effort` 为准

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
| `image_url`、`input_image` | `image_url` string 或 `{"url":"..."}`；也可只给 `file_id` |
| `video_url`、`input_video` | `video_url` string 或 `{"url":"..."}` |
| `input_file`、`file` | `file_id`，或 `filename` + `file_data`；Chat `file` 的字段位于嵌套 `file` 对象 |
| `input_audio` | `input_audio.data`、`input_audio.format` |
| `refusal` | `refusal`，作为所在消息的文本 |

OpenAI `image_url` / `input_image` 值为 data URL 时形成 inline data，值为 YouTube URL 时形成 external media，Gemini Files API 文件 URI 保持文件引用，其他 http(s) URL 由服务端下载后作为内联媒体发送，其他非 data 字符串按已上传 file ID 解析。data URL 接受 Base64 与百分号编码数据，省略 MIME 时按内容判断类型，MIME 参数不发送。同一请求内相同 URL 只下载一次；下载随请求取消，大小上限与内联媒体相同（512 MiB），MIME 取响应 `Content-Type`，缺失、无法解析或为 octet-stream 时按内容判断；URL 主机或任一重定向目标解析到本机、私网、链路本地、组播或未指定地址时不连接，按下载失败处理；下载失败返回 400，错误信息包含 URL 与原因。`video_url` / `input_video` 只接受 YouTube URL。`file_data` 接受 data URL 或 Base64 文件内容，没有声明 MIME 时按 `/v1/files` 的规则依次按文件内容与 `filename` 扩展名判断。

function tool 使用 `{"type":"function","function":{"name","description","parameters","strict"}}`。`strict:true` 启用参数契约校验。Google tool type 为 `web_search`、`web_search_preview`、`image_search`、`url_context`、`code_interpreter`、`google_maps`。custom tool `{"type":"custom","custom":{name,description,format}}` 声明为只有字符串参数 `input` 的函数，grammar 格式的语法与定义写入函数描述；调用以 `{"type":"custom","custom":{name,input}}` 返回，历史中的 custom 调用以 `{"input":<INPUT>}` 发送。历史函数调用的 `arguments` 不是 JSON object 时以 `{"_raw":"<原文>"}` 发送。

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
    "logprobs": null,
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

1. 每个 choice 一个 role chunk：`delta={"role":"assistant","content":""}`
2. 正文 `delta.content`、思考 `delta.reasoning_content`、工具 `delta.tool_calls`、媒体或代码渲染 `delta.content`
3. 各 choice 结束时发送引用 `delta.annotations`
4. 随后发送该 choice 的 finish chunk：`finish_reason`，可选 `provider_finish_reason`
5. `include_usage=true` 且上游有 usage 时，在全部 choice 结束后发送 `choices:[]` 的 usage-only chunk
6. `data: [DONE]`

每个普通 chunk 为 `{id,object:"chat.completion.chunk",created,model,choices:[{index,delta,finish_reason}],usage?}`，`index` 标识所属 choice。响应头已发送后的失败为 `data: {"error":{"message","type","code"}}`。

### OpenAI Responses

`POST /v1/responses` 请求字段：

| 字段 | 类型与语义 |
| --- | --- |
| `model` | 必需模型 ID |
| `input` | string 或 input item 数组 |
| `instructions` | 顶层 system instruction |
| `stream` | boolean |
| `tools`、`tool_choice` | function、custom、namespace（可含 custom）、local_shell/shell/apply_patch 与 Google tools；choice 支持 auto/none/required、named function/custom、allowed_tools、apply_patch/shell 与托管工具类型 |
| `temperature`、`top_p`、`max_output_tokens` | 生成参数 |
| `reasoning` | `{"effort":"..."}` |
| `text` | `{"format":{"type":"text|json_object|json_schema","schema":...}}` |
| `previous_response_id` | 当前进程内已保存的前一响应 ID |
| `parallel_tool_calls` | 省略/`true`/`false` |
| `truncation` | 省略/`disabled`/`auto` |
| `metadata` | string-to-string object |
| `store` | 省略/`true`/`false` |
| `background` | boolean |

- namespace 内的函数以全名编码，输出恢复 `namespace` 与原函数名，不同 namespace 可声明同名函数
- `store` 省略或为 `true` 时保存当前进程会话节点与输出项；`false` 只返回本次结果，不建立节点，既有续接链保持不变
- `background:true` 的请求在当前连接内执行并返回完成的响应对象；该响应不论 `store` 取值都会保存，供查询与取消

input item 字段为 `type`、`id`、`role`、`content`、`call_id`、`name`、`namespace`、`arguments`、`input`、`action`、`operation`、`status`、`output`、`encrypted_content`。支持 message、`function_call`、`function_call_output`、reasoning item 与下文列出的工具历史项。message content Part：

| type | 字段 |
| --- | --- |
| `input_text`、`output_text` | `text` |
| `input_image` | `image_url` string 或 `{url}`；也可只给 `file_id` |
| `input_file` | `file_id`、`file_url`，或 `filename` + `file_data` |
| `input_audio` | `input_audio:{data,format}` |
| `input_video` | `video_url` string 或 `{url}` |
| `refusal` | `refusal`，作为所在消息的文本 |

Responses 的图片、视频与文件 Part 复用上述 data URL、file ID 与 YouTube 规则。

其他 input item 映射：

| item | 映射 |
| --- | --- |
| system/developer message | 文本进入系统指令，图片与文件移到首个 user 轮 |
| `custom_tool_call` | 函数调用，参数为 `{"input":<INPUT>}` |
| `local_shell_call` | `local_shell` 函数调用，参数为去掉 `type` 的 `action` |
| `shell_call` | `shell` 函数调用，参数为 `action` |
| `apply_patch_call` | `apply_patch` 函数调用，参数为 `operation` |
| `custom_tool_call_output`、`local_shell_call_output`、`shell_call_output`、`apply_patch_call_output` | 按 `call_id` 关联的函数结果 |
| `web_search_call`、`code_interpreter_call`、`file_search_call`、`computer_call`、`mcp_call`、`mcp_list_tools`、`mcp_approval_request`、`tool_search_call`、`tool_search_output` | assistant 文本摘要 `<type> <JSON>` |
| `image_generation_call` | assistant 文本摘要与 `result` 图片 |
| `computer_call_output`、`mcp_approval_response` | user 文本摘要，截图作为图片 |
| `item_reference` | 当前进程保存的同 ID 输入项或输出项 |
| `compaction`、`compaction_trigger` | 不发送 |

文本摘要的 JSON 省略 `id`、`type` 与 `status`。函数结果项缺少 `call_id` 时使用 `id`；`apply_patch_call_output` 的结果为 `{"status","output"}`。没有 `type` 与 `role`、只带 `id` 的项按 `item_reference` 展开，找不到同 ID 的项时返回 400。system/developer message 的图片与文件在没有 user 轮时单独组成首个 user 轮。

Responses tool 字段：

| tool type | 字段 |
| --- | --- |
| `function` | `name`、`description`、`parameters`、`strict` |
| `web_search`、`web_search_2025_08_26`、`web_search_preview`、`web_search_preview_2025_03_11` | `search_context_size`、`user_location`、`filters.allowed_domains` |
| `image_search`、`url_context`、`google_maps` | `type` |
| `code_interpreter` | `container` |
| `custom` | 只有字符串参数 `input` 的函数，grammar 写入描述 |
| `local_shell` | 函数，参数为 `command`、`working_directory`、`timeout_ms` |
| `shell` | 函数，参数为 `commands`、`timeout_ms`、`max_output_length` |
| `apply_patch` | 函数，参数为 `type`、`path`、`diff` |
| `image_generation`、`file_search`、`mcp`、`computer`、`computer_use_preview`、`tool_search`、`programmatic_tool_calling` | 接受但不声明 |

- web search 工具的 `search_context_size`、`user_location` 与 `filters.allowed_domains` 写入搜索指令，域名限制使用 `site:` 查询；上游 grounding query 与 sources 原样返回
- `code_interpreter.container` 为容器 ID 字符串时忽略；为对象时其 `file_ids` 作为文件引用附到最后一个 user 轮，没有 user 轮时作为新的 user 轮

`tool_choice` 的 `allowed_tools` 映射为 `mode`（`auto` 或 `required`）与允许的函数名，请求只发送列表中的函数与托管工具对应的 Google 工具；`web_search_preview`、`web_search_preview_2025_03_11` 与 `code_interpreter` 映射为 `required`；`file_search`、`computer`、`computer_use`、`computer_use_preview`、`image_generation`、`mcp`、`programmatic_tool_calling` 与 `tool_search` 映射为 `auto`。

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
| `custom_tool_call` | `id`、`status`、`call_id`、`name`、`input`，可选 `namespace` |
| `local_shell_call` | `id`、`status`、`call_id`、`action:{type:"exec",command,env,...}` |
| `shell_call` | `id`、`status`、`call_id`、`action:{commands,timeout_ms,max_output_length}`、`environment:null` |
| `apply_patch_call` | `id`、`status`、`call_id`、`operation:{type,path,diff}` |
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
| `response.custom_tool_call_input.delta` | `item_id`、`output_index`、`delta` |
| `response.custom_tool_call_input.done` | `item_id`、`output_index`、`input` |
| `response.image_generation_call.in_progress`、`response.image_generation_call.completed` | `item_id`、`output_index` |
| `response.code_interpreter_call.in_progress`、`response.code_interpreter_call.interpreting`、`response.code_interpreter_call.completed` | `item_id`、`output_index` |
| `response.code_interpreter_call_code.delta` | `item_id`、`output_index`、`delta` |
| `response.code_interpreter_call_code.done` | `item_id`、`output_index`、`code` |
| `response.web_search_call.in_progress`、`response.web_search_call.searching`、`response.web_search_call.completed` | `item_id`、`output_index` |
| `response.completed`、`response.incomplete`、`response.failed` | 完整 `response` |

`web_search_call` 由实际 grounding query 触发。搜索发生时，先输出 index 0 的 search call item，再输出 index 1 的 message；无 grounding query 时只输出 message，并按正文事件逐块发送。上游在正文后失败时，已产生的 delta 按原顺序位于 `response.failed` 之前。

### Anthropic Messages

`POST /v1/messages` 请求字段：

| 字段 | 类型与语义 |
| --- | --- |
| `model` | 必需模型 ID |
| `messages` | `{role,content}` 数组，role 为 `user`、`assistant`、`system` |
| `system` | string 或 text block 数组 |
| `max_tokens` | 正整数，省略时使用模型默认输出上限 |
| `stop_sequences` | string array |
| `stream` | boolean |
| `temperature`、`top_p`、`top_k` | 生成参数 |
| `tools`、`tool_choice` | custom/server tools、Anthropic 定义的客户端工具与 auto/none/any/named tool |
| `thinking` | `{type:"enabled",budget_tokens:<INT>}`、`{type:"adaptive"}` 或 `{type:"disabled"}` |
| `output_config` | `{effort:"..."}` |

- `system` 消息在原位置以 `<system-reminder>` 包裹的用户内容发送

message content 可以是 string 或 block 数组：

| block type | 字段 |
| --- | --- |
| `text` | `text` |
| `thinking` | `thinking`、`signature` |
| `redacted_thinking` | `data` |
| `image`、`document` | `source:{type,media_type,data,url,file_id,content}` |
| `tool_use` | `id`、`name`、object `input` |
| `tool_result` | `tool_use_id`、`content`、`is_error` |
| `server_tool_use` | `id`、`name`、`input` |
| `web_search_tool_result` | `tool_use_id`、`content:[{type:"web_search_result",url,title,encrypted_content,page_age}]` 或 `{type:"web_search_tool_result_error",error_code}` |
| `search_result` | `source`、`title`、`content` |
| `web_fetch_tool_result` | `tool_use_id`、`content:{type:"web_fetch_result",url,content:<document>}` 或错误对象 |
| `code_execution_tool_result`、`bash_code_execution_tool_result`、`text_editor_code_execution_tool_result`、`tool_search_tool_result` | `tool_use_id`、`content` |
| `container_upload` | `file_id` |

`image` / `document` 的 Base64 source 使用 `type:"base64"`、`media_type`、`data`；URL source 使用 `type:"url"` 与非空 `url`，省略 media type 时 image 默认 `image/*`、document 默认 `application/pdf`。document 的 `type:"text"` source 以 `data` 作为文本，`type:"content"` source 按原顺序展开字符串或 content block；`type:"file"` source 与 `container_upload` 以 `file_id` 引用 `/v1/files` 上传的文件。服务端工具历史块在原位置以文本续接：`server_tool_use` 写为工具名与 input，`web_fetch_tool_result` 写为 URL 并展开其中的 document，抓取失败时写为错误码，`search_result` 写为标题、来源与正文，其余工具结果写为 block type 与 content JSON。`tool_result.is_error=true` 把合法 JSON content 包装为 `{"error":<CONTENT>}`；普通标量或数组结果包装为 `{"result":<CONTENT>}`。

custom tool 为 `{name,description,input_schema}`，可选 `type:"custom"`。server tool 字段：

| type | 必需 name |
| --- | --- |
| `web_search_*` | `web_search` |
| `image_search` | `image_search` |
| `web_fetch_*` | `web_fetch` |
| `code_execution_*` | `code_execution` |
| `url_context` | `url_context` |
| `google_maps` | `google_maps` |

`web_search_*` 的 `allowed_domains` 与 `user_location` 写入搜索偏好，调用次数由上游决定；其余选项不改变上游请求。`bash_*`、`text_editor_*`、`memory_*` 以请求中的 `name` 声明为函数，描述与参数由服务固定，参数分别为 `{command,restart}`、`{command,path,file_text,old_str,new_str,insert_line,insert_text,view_range}`（`text_editor_20241022` 与 `text_editor_20250124` 的 command 另含 `undo_edit`）与再加 `old_path`、`new_path` 的 memory 命令，模型调用以 `tool_use` 返回由客户端执行；`tool_search_tool_*` 与类型名含 `toolset` 的工具（如 `mcp_toolset`）不声明到上游。

server tool 接受对应 `type`、`name` 与 Anthropic 工具定义中的选项 `allowed_callers`、`allowed_domains`、`blocked_domains`、`cache_control`、`citations`、`defer_loading`、`max_content_tokens`、`max_uses`、`response_inclusion`、`strict`、`url_sources`、`use_cache`、`user_location`；其他选项、`description` 或 `input_schema` 返回 `400 invalid_request_error`。tool choice 接受省略、`{"type":"auto"}`、`{"type":"none"}`、`{"type":"any"}` 与 `{"type":"tool","name":"FUNCTION_NAME"}`。custom tool 的 `cache_control` 等客户端提示保留生成能力，`strict:true` 校验完整返回参数。

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

content 输出 block 为 `text`、`thinking`、`redacted_thinking`、`tool_use`、`server_tool_use` 或 `web_search_tool_result`。stop reason 为 `end_turn`、`tool_use`、`stop_sequence`、`max_tokens`、`pause_turn` 或 `refusal`。`POST /v1/messages/count_tokens` 接受同一 message/system/tools 输入并返回 `{"input_tokens":<INT>}`；它提供独立计数估算，PDF 等媒体请求的最终用量以生成响应 `usage.input_tokens` 为准。

Anthropic 响应中的媒体编码为 text block 中的 Markdown data URL，代码编码为 fenced text。Google Search 的每个去重查询生成一组 `server_tool_use` 和 `web_search_tool_result`，结果按 URL 去重，来源集合覆盖本次搜索的全部查询。`usage.server_tool_use.web_search_requests` 记录上游报告的去重查询数，`web_fetch_requests` 为 `0`。URL Context 等其余引用在末尾追加 `Sources:` Markdown 列表。

搜索来源的 `encrypted_content` 由本服务生成，保存上游返回的 URL、标题与可用摘要。客户端回传的搜索块以文本续接：查询写为 `Web search: <query>`，配对结果追加到同一文本，每个来源写标题与 URL，能用当前 `PROXY_API_KEY` 解开的 `encrypted_content` 追加摘要；来自 Anthropic 服务或以其他 `PROXY_API_KEY` 生成的来源只保留标题与 URL。错误结果写为 `Web search failed: <error_code>`；没有配对调用的结果与没有结果的调用（`pause_turn` 续接）分别单独成文。该字段用于本服务的多轮会话，和 Anthropic 服务的来源令牌分别使用。

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

delta 联合类型为 `text_delta{text}`、`thinking_delta{thinking}`、`signature_delta{signature}`、`input_json_delta{partial_json}`。thinking signature 在对应 thinking block 关闭前发送；redacted thinking 使用一个 start/stop block；tool_use 先发送空 input，再通过 `input_json_delta` 发送完整参数 JSON。搜索块在来源汇总后以完整的 start/stop block 输出，查询计数随最终 `message_delta.usage` 返回。

### Gemini Interactions

`POST /v1beta/interactions` 与 `POST /v1/interactions` 接受同一创建请求，通过 `x-goog-api-key`、Bearer 或 `key` 查询参数认证。

```json
{
  "model": "gemini-3.8-flash-tts",
  "input": [{"type":"user_input","content":[{
    "type":"text","text":"Have a wonderful day!",
    "annotations":[{"type":"speech_metadata","style":"cheerful and friendly"}]
  }]}],
  "response_format": {"type":"audio","mime_type":"audio/l16","sample_rate":24000},
  "generation_config": {"speech_config":[{"voice":"Kore"}]},
  "stream": true
}
```

| 字段 | 映射 |
| --- | --- |
| `input` | 字符串、单个内容块、内容块数组或步骤数组；内容类型为 `text`、`image`、`audio`、`video`、`document` |
| 媒体内容 | `data`（Base64）或 `uri`，可选 `mime_type` |
| 输入步骤 | `user_input`、`model_output`、`thought`、`function_call`、`function_result`、`code_execution_call`、`code_execution_result` 与工具 call/result 步骤 |
| `system_instruction` | 当前请求的系统指令 |
| `generation_config` | `temperature`、`top_p`、`top_k`、`max_output_tokens`、`seed`、`stop_sequences`、`thinking_level`、`thinking_summaries`、`speech_config`、`tool_choice` |
| `response_format` | 单对象或数组：`{type:"text",mime_type?,schema?}`、JSON Schema 对象、`{type:"image",mime_type?,aspect_ratio?,image_size?}`、`{type:"audio",mime_type?,sample_rate?,bit_rate?,delivery?}` |
| 语音配置 | `speech_config:[{voice,language?}]` 或 `{speakers:[{speaker,voice}],mode?}` |
| 语音文本 | 文本块 `annotations` 中的 `{type:"speech_metadata",speaker?,style?}` |
| 函数与工具 | `tools:[{type:"function",name,description?,parameters?}]` 与 Google 工具；`tool_choice` 为 `auto`、`any`、`none`、`validated` 或 `{allowed_tools:{mode?,tools}}` |
| 续接 | `previous_interaction_id`、`store` |

- 媒体内容的 `data` 与 `uri` 二选一，复用 Gemini 文件与内联媒体解析：省略 `mime_type` 时内联数据按内容识别类型，无法识别时返回 `400 INVALID_ARGUMENT`；`uri` 为外部 http(s) URL 时由服务下载为内联媒体，规则与 GenerateContent 的 `fileData` 相同；本服务 Gemini 文件接口返回的 `uri` 作为文件引用
- 函数结果通过 `call_id` 匹配历史调用，没有对应调用时使用自带的 `name`；`code_execution_call` 与 `code_execution_result` 以代码块文本并入模型轮次；`google_search`、`url_context`、`google_maps`、`file_search`、`mcp_server_tool`、`retrieval`、`processing` 的 call 与 result 步骤不发送上游
- 文本 `response_format` 带 `schema` 时请求结构化输出，`mime_type` 默认 `application/json`；不带 `type` 或 `type` 为 JSON Schema 类型的对象直接作为结构化输出 Schema；图片指定 `mime_type:"image/jpeg"` 时其他格式的图片输出转为 JPEG
- `speech_config` 的 `language` 不发送上游，多说话人 `mode` 为 `verbatim` 或 `conversational`；`speech_metadata` 保留说话人与风格
- 工具另接受 `google_search`、`url_context`、`code_execution`、`google_maps`，`computer_use`、`file_search`、`mcp_server`、`retrieval` 不发送上游；`tool_choice:"any"` 要求调用函数，`allowed_tools.tools` 限定可调用的函数
- interaction 默认保存；`previous_interaction_id` 重建前序内容，`store:false` 仅返回本次响应，不能检索或续接；interaction 与 Responses 响应共用当前服务实例内最多 256 个保存节点，超出时淘汰最早的节点

音频 `mime_type` 为 `audio/wav`、`audio/l16`、`audio/mp3`、`audio/ogg_opus`、`audio/alaw` 或 `audio/mulaw`，非流式默认 `audio/wav`，流式默认 `audio/l16`。TTS 上游为 24 kHz、16-bit 小端、单声道 PCM，Lyria 等返回的 MP3 先解码为 PCM；请求 `audio/mp3` 且未改采样率、未指定 `bit_rate` 时原样返回上游 MP3。`sample_rate` 省略时使用上游采样率，指定时重采样；MP3 取 32、44.1、48 kHz，Ogg Opus 取 8、12、16、24、48 kHz，均为不低于目标采样率的最小值，超出时取最高值。`bit_rate` 以 bit/s 计，用于 MP3（CBR 32000–320000 取最接近值，默认 192000）与 Opus。输出内容的 `mime_type` 依次为 `audio/wav`、`audio/l16`、`audio/mp3`、`audio/ogg`、`audio/alaw`、`audio/mulaw`，`sample_rate` 与 `channels` 为实际值。上游为 PCM 且不重采样时，`audio/l16`、`audio/alaw`、`audio/mulaw` 逐块输出，其余情况在音频汇总完成后发送一个完整块。请求同时含文本与音频而模型只能生成文本时按文本生成。`delivery` 取任意值时都内联返回。创建请求在当前连接内执行，`background:true` 时同样在当前连接内完成并返回终态 interaction。

非流式响应包含 `id`、`object:"interaction"`、`model`、`created`、`updated`、`status`、`steps` 与 `usage`。`steps` 的 `model_output.content` 保存文本或媒体，音频位于 `{type:"audio",data,mime_type,sample_rate,channels}`；SDK 的 `output_audio` 与 `output_text` 从这些步骤读取。函数调用作为 `function_call` 步骤返回，状态为 `requires_action`；正常生成状态为 `completed`，输出限额等提前终止状态为 `incomplete`。

SSE 使用相同的事件名与 JSON `event_type`：`interaction.created` → `step.start` → `step.delta` → `step.stop` → `interaction.completed`。步骤以 `index` 对应，音频增量为 `{type:"audio",data,mime_type,sample_rate,channels}`。10 秒无语义事件时发送 `: ping`，上游错误或缺失终态发送 `error` 事件并结束；客户端断开会取消上游生成。流开始前使用 Gemini HTTP 错误对象。

`GET /v1beta/interactions/{id}` 返回创建时的完整 interaction；`include_input=true` 时 `steps` 先列出请求输入，字符串与连续的内容块合并为 `user_input` 步骤；`stream=true` 时按创建流的事件格式重放保存的 interaction，每个事件带递增的 `event_id`，查询参数 `last_event_id` 从其后的事件继续，无法识别时返回 `400 INVALID_ARGUMENT`。`POST /v1beta/interactions/{id}/cancel` 返回该 interaction 的终态对象。`DELETE /v1beta/interactions/{id}` 返回 `{}`，之后检索返回 404 `NOT_FOUND`，作为 `previous_interaction_id` 或 Responses 的 `previous_response_id` 返回 400。`/v1` 前缀行为相同。

### Embeddings

`POST /v1/embeddings` 请求字段为 `model`、`input`，可选 `dimensions`、`encoding_format`。`input` 是字符串或字符串数组，token 数组与空数组返回 400；`dimensions` 为正整数，写入每条请求的 `outputDimensionality`；`encoding_format` 为 `float`（默认）或 `base64`，`base64` 为小端 float32。响应为 `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[...]}],"model":"<MODEL>","usage":{"prompt_tokens":N,"total_tokens":N}}`。

`:embedContent` 接受 `content`、`taskType`、`title`、`outputDimensionality`，返回 `{"embedding":{"values":[...]}}`。`:batchEmbedContents` 接受 `requests[]`，每项字段相同并可带与路径一致的 `model`，返回 `{"embeddings":[...]}`。字段同时接受 snake_case；缺 `content` 或 `model` 与路径不一致时返回 400 `INVALID_ARGUMENT`。

三个端点都经 Build 通道调用上游 `batchEmbedContents`，超过 100 条时按顺序分批并合并结果，上游 `tokenCount` 计为输入 token。Build 未启用或没有可调用账户时返回 404 `model_not_found` / `NOT_FOUND`。

### Gemini GenerateContent

`POST /v1beta/models/{model}:generateContent`、`:streamGenerateContent` 与 `:countTokens` 接受：

```json
{
  "contents": [{"role":"user","parts":[{"text":"Hello"}]}],
  "systemInstruction": {"role":"user","parts":[{"text":"Be concise"}]},
  "generationConfig": {},
  "tools": [],
  "toolConfig": {},
  "safetySettings": [{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_ONLY_HIGH"}]
}
```

请求体各层消息字段同时接受 proto 字段名与 lowerCamelCase 名，例如 `system_instruction`、`generation_config.response_mime_type`、`tools[].function_declarations`、`tool_config.function_calling_config` 和 `function_call`；同一层两种写法并存时使用驼峰字段。`functionCall.args`、`functionResponse.response`、`parameters`、`parametersJsonSchema`、`responseSchema`、`responseJsonSchema` 与 `partMetadata` 的取值按原样使用，其中的键名不转换。

Content 字段为 `role` 与 `parts`。Part oneof：

| Part | 字段 |
| --- | --- |
| text/thought | `text`、可选 `thought`、`thoughtSignature` |
| inline data | `inlineData:{mimeType,data}` |
| file data | `fileData:{mimeType?,fileUri,displayName}` |
| function call | `functionCall:{id,name,args}` |
| function response | `functionResponse:{id,name,response}` |
| executable code | `executableCode:{language,code}` |
| code result | `codeExecutionResult:{outcome,output,error}` |

不含数据字段且没有 `thoughtSignature` 的 Part 不发送；同一 Part 含多个数据字段时返回 `400 INVALID_ARGUMENT`。`fileData.fileUri` 为 YouTube 链接时作为外部视频，为本服务 `/upload/v1beta/files` 返回的文件 URI 或 `/v1/files` 文件 ID 时作为文件引用，为其他 http(s) URL 时由服务下载为内联媒体；其他 Gemini Files API URI 不下载。外部 URL 在同一请求内只下载一次，上限 512 MiB，MIME 依次取响应 `Content-Type`、`mimeType` 与内容识别；主机或任一重定向目标解析到本机、私网、链路本地、组播或未指定地址时不连接，按下载失败处理；下载失败返回带 URL 与原因的 `400 INVALID_ARGUMENT`。`:countTokens` 同样先下载外部 URL 再计数。

`generationConfig` 全字段：

| 类别 | 字段 |
| --- | --- |
| sampling | `temperature`、`topP`、`topK`、`frequencyPenalty`、`presencePenalty`、`seed` |
| media | `mediaResolution`：`MEDIA_RESOLUTION_LOW`、`MEDIA_RESOLUTION_MEDIUM`、`MEDIA_RESOLUTION_HIGH` |
| output limits | `candidateCount`、`maxOutputTokens`、`stopSequences` |
| log probabilities | `responseLogprobs`、`logprobs` |
| structured output | `responseMimeType`、`responseSchema`、`responseJsonSchema` |
| modalities | `responseModalities` |
| image | `imageConfig:{aspectRatio,imageSize}` |
| thinking | `thinkingConfig:{thinkingBudget,thinkingLevel}` |
| transcription | `transcriptionConfig:{languageCodes,customVocabulary,wordTimestamps,speakerLabels,smartTranscription}` |
| speech | `speechConfig` |

`candidateCount` 取省略、`0` 或 `1` 至 `8`；大于 1 时每个候选单独生成，候选 0 沿用请求 ID，候选 i 的请求 ID 为 `<id>-<i>`，带 `seed` 时使用 `seed+i`；任一候选失败时整个请求返回该错误，`:streamGenerateContent` 在首个候选就绪后开始输出，其余候选的启动错误以流内错误返回；用量为全部候选之和。`frequencyPenalty`、`presencePenalty`、`responseLogprobs` 与 `logprobs` 接受任意值，不发送上游，响应不含 logprobs。

`responseModalities` 接受 `TEXT`、`IMAGE`、`AUDIO` 与 `MODALITY_UNSPECIFIED`，`MODALITY_UNSPECIFIED` 不发送，只含该值时按省略处理；请求的模态先与模型可生成的模态取交集：TTS 与音乐模型为 `AUDIO`，图像模型为 `IMAGE`、`TEXT`，其他模型为 `TEXT`；没有交集时保持原样，此时 `AUDIO` 与其他模态同时出现返回参数错误。图像模型省略模态时发送 `[IMAGE,TEXT]`，仅请求 `IMAGE` 时发送 `[IMAGE]`；`imageConfig` 保留显式宽高比与尺寸，支持输出分辨率的模型省略图片配置时使用 `1K`。

`speechConfig.voiceConfig` 与 `multiSpeakerVoiceConfig` 互斥；单声音必须提供 `prebuiltVoiceConfig.voiceName`，每个多说话人条目必须提供非空 `speaker` 与 `voiceConfig.prebuiltVoiceConfig.voiceName`；`multiSpeakerVoiceConfig.mode` 可选 `VERBATIM` 或 `CONVERSATIONAL`。文本 part 的 `speechMetadata`（或 `speech_metadata`）`{speaker,style}` 写入 Part field 41。能力码 85 的模型把未带 speaker 的文本按行拆分，以配置中说话人名加冒号开头的行开始新分段，续行并入上一分段，首个说话人行之前的文本不发送；没有匹配行的文本原样发送。旧 TTS 模型把 `speechMetadata` 写回 `speaker: 台词` 前缀与 `style\n\n` 说明段落。`transcriptionConfig.smartTranscription=true` 与显式 true 的 `wordTimestamps` 或 `speakerLabels` 互斥；language code `detect` 归一为空自动检测。

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

`googleSearch.searchTypes` 可以包含 `webSearch` 与 `imageSearch` 空对象；未提供或两项均未启用时默认 web search。`timeRangeFilter.startTime/endTime` 使用 RFC 3339 Nano。`googleSearchRetrieval` 映射为 web search，`dynamicRetrievalConfig` 不发送上游。tool choice 位于 `toolConfig.functionCallingConfig:{mode,allowedFunctionNames}`，接受 `AUTO`、`NONE`、`ANY`、`VALIDATED` 与按 `AUTO` 处理的 `MODE_UNSPECIFIED`；`allowedFunctionNames` 使用已声明函数的子集。

`:countTokens` 也接受把生成请求放在 `generateContentRequest`（或 `generate_content_request`）中的包装形式，按其中的 `contents`、`systemInstruction` 与 `tools` 计数。

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

`finishMessage` 为上游给出的结束说明，例如上游过滤生成结果时的原因；没有上游说明时，`provider_*` 与缺少思维签名的结束原因写入固定说明。候选没有输出 Part 时省略 `content`。`candidateCount` 大于 1 时，`candidates` 按 `index` 0 到 n-1 排列，各自携带 `finishReason`。`:streamGenerateContent` 按到达顺序输出各候选的增量块，每块只含一个带 `index` 的候选；最后一块列出全部候选的 `finishReason` 与合计的 `usageMetadata`。

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

非流式结果合并相邻、同类且无签名边界的正文或思考片段。工具、媒体与带转录元数据的 Part 保持独立；独立签名附着于前一未签名 Part，缺少可附着内容时用 `{"text":"","thought":true,"thoughtSignature":"..."}` 承载。

`:streamGenerateContent` 使用 SSE。每个语义事件发送一个部分 `GenerateContentResponse`，包含 `responseId`、`modelVersion` 与一个 candidate Part、grounding 或 citation；最后一帧包含 candidate `finishReason`、可选 `finishMessage` 和 `usageMetadata`。响应头后的错误帧为 `data: {"error":{"code","message","status"}}`。

### Files、Transcribe 与媒体

`POST /v1/files` 接受 multipart `file` 与可选 `purpose`，两者可以按任意顺序到达。文件上限 512 MiB，请求额外允许 1 MiB multipart overhead；普通 scalar part 上限 64 KiB。filename 与非空 file 必需；`purpose` 省略或为空时为 `user_data`。Content-Type 为空、无效、不含子类型或为 `application/octet-stream` 时从文件前缀检测不带参数的 MIME，Ogg 前缀记为 `audio/ogg`，WAV 前缀记为 `audio/wav`；前缀无法识别时依次按 FLAC 头、MPEG 音频帧头与扩展名（flac、mp3、mp4、mpeg、mpga、m4a、ogg、wav、webm，其余扩展名查系统 MIME 表）确定。

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

未知文件返回 404 `file_not_found`，超过大小限制返回 413 `file_too_large`。Drive 账户绑定与跨账户复制见“Drive 上传与文件 Part”节。

`GET /v1/files` 按创建时间从新到旧返回 `{"object":"list","data":[File object],"first_id","last_id","has_more"}`。`order` 为 `asc` 或 `desc`（默认），`after` 为上一页最后一个文件 ID，`limit` 为 1–10000、默认 10000，`purpose` 只返回该用途的文件。未知的 `after` 返回空页；`limit` 越界或 `order` 无效返回 400。

请求带 `anthropic-version`，或 `anthropic-beta` 含 `files-api-` 时，同一组路径按 Anthropic Files API 返回。文件对象为 `{"id","type":"file","filename","mime_type","size_bytes","created_at":"<RFC 3339>","downloadable":true}`。上传只需 `file` 段，用途记为 `user_data`。删除返回 `{"id","type":"file_deleted"}`。错误为 Anthropic 错误对象，未知文件为 404 `not_found_error`。列表按创建时间从新到旧返回 `{"data","next_page","first_id","last_id","has_more"}`：`limit` 为 1–1000、默认 20；`page` 取上一页的 `next_page`（形如 `page_<文件 ID>`），也接受 `after_id` 与 `before_id`；`ids`（也接受 `ids[]` 或逗号分隔）只返回列出的文件且不分页；带 `scope_id` 时返回空列表。文件 ID 与 OpenAI 文件相同，可用于 Messages 的 `{"type":"file","file_id"}` source。

`POST /upload/v1beta/files` 按 `X-Goog-Upload-Protocol` 或 `uploadType` 接受 `resumable` 与 `multipart`。可续传上传先发送 `X-Goog-Upload-Command: start`，请求体为可选的 `{file:{displayName?,mimeType?}}`（接受 `display_name` 与单引号字符串），`X-Goog-Upload-Header-Content-Type` 声明类型，响应头 `X-Goog-Upload-URL` 给出上传地址；分块以 `X-Goog-Upload-Command: upload` 或 `upload, finalize` 与 `X-Goog-Upload-Offset` 发往该地址，偏移与已接收字节数不符时返回 400，`query` 返回 `X-Goog-Upload-Size-Received`，`cancel` 结束上传；地址闲置 1 小时后失效。multipart 请求体由可省略的 JSON 元数据段与文件段组成，`multipart/related` 与 `multipart/form-data` 均可。显示名依次取 `displayName`、文件段的 filename 与 `X-Goog-Upload-File-Name`，均缺失时为 `file`；类型依次取文件段 `Content-Type`、`X-Goog-Upload-Header-Content-Type`、`mimeType`，均缺失或为 `application/octet-stream` 时按内容识别。完成时返回 `{file:File}` 与 `X-Goog-Upload-Status: final`。

File 为 `{name:"files/<id>",displayName,mimeType,sizeBytes,createTime,updateTime,uri,state:"ACTIVE",source:"UPLOADED"}`，`uri` 为 `https://generativelanguage.googleapis.com/v1beta/files/<id>`，可直接用作 `fileData.fileUri` 与 Interactions 媒体内容的 `uri`。`GET /v1beta/files/{id}` 返回 File，`DELETE /v1beta/files/{id}` 返回 `{}`，未知文件返回 404 `NOT_FOUND`。`GET /v1beta/files` 按创建时间从新到旧返回 `{files,nextPageToken?}`，`pageSize` 默认 10、最大 100，`pageToken` 取上一页的 `nextPageToken`。Gemini 文件与 `/v1/files` 共用存储、512 MiB 上限与 Drive 账户绑定；资源名由服务分配。

`POST /v1/audio/transcriptions` multipart 字段：

| 字段 | 取值与处理 |
| --- | --- |
| `file` | 必需非空；`audio/*`、`video/mp4`、`video/webm`；最大 512 MiB；分段 Content-Type 按 `POST /v1/files` 的规则识别 |
| `model` | 默认 `gemini-3.5-transcribe`；接受可选 `models/` 前缀 |
| `response_format` | `json`、`text`、`srt`、`verbose_json`、`vtt`、`diarized_json`；默认 `json` |
| `language` | 语言码；`detect` 与 `auto` 映射为空自动检测 |
| `temperature` | `0..2` |
| `custom_vocabulary` | 可重复文本字段或 JSON string array |
| `word_timestamps`、`speaker_labels`、`smart_transcription` | `true` 或 `false` |
| `prompt` | 当前 wire 无对应字段，忽略 |

`smart_transcription=true` 与显式 true 的 word timestamps 或 speaker labels 互斥，custom vocabulary 与 word timestamps 互斥，冲突组合返回 `400 invalid_request`。每次账户尝试创建临时 Drive file，生成结束、失败或取消后在同账户清理。

`text` 格式返回 `text/plain; charset=utf-8`。`srt` 与 `vtt` 按 `segments` 每段生成一条字幕，分别返回 `text/plain; charset=utf-8` 与 `text/vtt; charset=utf-8`；这两种格式在未设置 `word_timestamps`、`smart_transcription` 不为 `true` 且没有 `custom_vocabulary` 时请求词级时间戳。`json` 返回 `{"text":"...","usage":...}`。详细格式：

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

`POST /v1/audio/translations` 接受 `file`、`model`、`prompt`、`response_format`（`json`、`text`、`srt`、`verbose_json`、`vtt`，默认 `json`）与 `temperature`（0–2）。服务先按转录规则转录音频，再用文本模型把转录译为英文：`model` 是转录模型或省略时（默认 `gemini-3.5-transcribe`），由 `gemini-flash-latest` 翻译；`model` 是其他模型时，由 `gemini-3.5-transcribe` 转录、该模型翻译。`srt`、`vtt` 与 `verbose_json` 请求词级时间戳并逐段翻译，保留每段起止时间；`json` 与 `text` 整段翻译。翻译请求用 JSON Schema 要求输出与分段数相同的字符串数组；`prompt` 作为译文的上下文与风格说明，`temperature` 作用于翻译。`json` 返回 `{"text"}`；`verbose_json` 返回 `{"task":"translate","language":"english","duration","text","segments"}`，不含 `words` 与 `usage`。转录为空时不调用翻译；译文段数与转录不一致时返回 502 `upstream_error`。

`POST /v1/images/generations`：

| 字段 | 取值与处理 |
| --- | --- |
| `model` | 省略时取实时目录按 ID 排序、支持 `generateContent` 的首个 `image_route` 模型 |
| `prompt` | 必需 |
| `n` | 默认 `1`，范围 1–10 |
| `size` | `auto` 或 `WIDTHxHEIGHT` |
| `quality` | `auto`、`low/standard=1K`、`medium/hd=2K`、`high/xhigh/max=4K` |
| `response_format` | `b64_json` 返回 Base64；其他值返回 data URL |
| `stream` | boolean |

- 每张图片单独生成，按序合并到 `data`
- `size` 取模型 `image_aspect_ratios` 中比值最接近的宽高比，目录未列出时在 1:1、2:3、3:2、3:4、4:3、4:5、5:4、9:16、16:9、21:9 中选择
- 模型列出 `image_output_resolutions` 时，`quality` 取其中最接近的分辨率
- `stream:true` 时以 SSE 返回，每张图片一个 `image_generation.completed` 事件

响应为 `{"created":<UNIX>,"data":[{"b64_json":"...","revised_prompt":"..."}]}` 或 `{"created":<UNIX>,"data":[{"url":"data:<MIME>;base64,...","revised_prompt":"..."}]}`。`revised_prompt` 只在上游同时返回文本时出现。任一张未返回最终图片时返回 HTTP 502 `upstream_error`，结束原因不是正常结束时写入错误消息，例如 `image_recitation`；其他生成错误按该错误返回，不返回部分结果。流式事件为 `{"type":"image_generation.completed","b64_json","created_at","output_format","size","quality","background"}`，在全部图片生成后依次发送，不发送 partial image 事件；生成失败时按非流式返回错误。

`POST /v1/images/edits` 接受 multipart 的 `image` 或 `image[]`（一个或多个文件）、`prompt`、`mask`，以及与图片生成相同的 `model`、`n`、`size`、`quality`、`response_format`、`stream`，响应格式也相同，流式事件类型为 `image_edit.completed`。输入图片按顺序作为参考图写在提示词之前；分段 Content-Type 缺失或为 `application/octet-stream` 时按内容识别，GIF 取首帧转为 PNG。`mask` 中完全透明的像素转为白色、其余为黑色，生成 PNG 遮罩，放在输入图片之后，并附说明“白色为编辑区域、黑色保持不变”。`background`、`output_format`、`output_compression`、`input_fidelity`、`partial_images`、`user` 没有上游对应项，不影响请求。缺少图片或提示词、遮罩无法解码时返回 400。

`POST /v1/audio/speech`：

| 字段 | 取值与处理 |
| --- | --- |
| `model`、`input` | 必需 |
| `voice` | 默认 `Zephyr` |
| `response_format` | 默认 `wav`；支持 `mp3`、`opus`、`aac`、`flac`、`wav`、`pcm` |
| `speed` | `0.25..4.0` |
| `instructions` | 文本 part 的 `speechMetadata.style` |

- `speed` 省略、`0` 或 `1` 为正常语速，其他值以 `Speak at <speed> times the normal speaking rate.` 追加到语音风格
- 旧 TTS 模型以 `instructions + "\n\n" + input` 形成提示

上游返回 MP3（Lyria 等）时先解码为 PCM，`mp3` 原样返回上游数据。`pcm` 返回 PCM body 与采样参数；上游返回 PCM16 WAV 时先提取音频数据。`wav` 将 PCM 按有效 rate 与 channels 封装为 16-bit WAV；原生 WAV 保留对应音频格式，多个片段先合并 PCM 数据再封装。`mp3` 为 MPEG-1 Layer III 192 kbit/s CBR（`audio/mpeg`，32/44.1/48 kHz），`opus` 为 Ogg Opus（`audio/opus`），`aac` 为 ADTS AAC-LC（`audio/aac`，44.1/48 kHz），`flac` 为 16-bit FLAC（`audio/flac`）。响应设置 `Content-Type` 与 `Content-Length`。

旧 TTS 模型的语音请求按官网 wire 在首个文本前写入 `## Transcript:\n`，AUDIO-only generation config 不写默认 `maxOutputTokens`；`responseModalities` 与 `speechConfig` 分别写入官网确认的槽位。

### Video

OpenAI `POST /v1/videos` 接受 JSON 或 multipart：

| 字段 | 取值与处理 |
| --- | --- |
| `model` | 省略时取实时目录按 ID 排序的首个具有 `predictLongRunning` 方法的模型 |
| `prompt` | 必需 |
| `seconds` | 整数字符串；默认 4；按模型选项取最接近值 |
| `size` | `1280x720`、`720x1280`、`1792x1024`、`1920x1080`、`1024x1792`、`1080x1920` |
| `input_reference` | JSON 中为 file ID/data URL；multipart 中为文件 |

- `input_reference` 文件段的 Content-Type 缺失、无效或为 `application/octet-stream` 时按内容识别图片类型

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

`GET /v1/videos/{id}` 返回当前对象。完成时 status 为 `completed`、progress 为 `100`；上游 done 且无 file 时 status 为 `failed`。上游已找不到该任务时，按本地记录返回 status 为 `failed`、progress 为 `100` 的对象，并带 `error:{"code":"video_not_found","message":<上游说明>}`。`GET /v1/videos/{id}/content` 接受省略或 `variant=video`，未完成时返回 409 `video_not_ready`；成功下载设置媒体 `Content-Type`、`attachment; filename="video.mp4"` 和已知的 `Content-Length`。

`GET /v1/videos` 从资源绑定列出视频，按创建时间从新到旧返回 `{"object":"list","data":[video object],"first_id","last_id","has_more"}`。`order` 为 `asc` 或 `desc`（默认），`after` 为上一页最后一个视频 ID，`limit` 为 0–100、默认 20。页内每个视频经上游轮询给出与 `GET /v1/videos/{id}` 相同的对象，上游已找不到的任务同样为带 `error` 的 `failed` 对象；其他轮询失败时返回该错误。`DELETE /v1/videos/{id}` 先轮询任务：已完成的删除 Drive 结果文件，并解除结果文件与任务的绑定；失败的只解除任务绑定；都返回 `{"id","object":"video.deleted","deleted":true}`。未结束的任务返回 409 `video_not_ready`。未知或已删除的视频在查询、下载与删除时都返回 404 `video_not_found`。

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

`model`、实际发送的 `seconds`、`size` 与 UTC `created_at` 随创建账户持久保存，后续轮询从资源绑定恢复；生成服务或进程重新启动后，OpenAI POST 与 GET 仍返回相同字段。`size` 为宽高比与分辨率换成模型支持的值后实际生成的尺寸：720p 为 `1280x720` 或 `720x1280`，1080p 为 `1920x1080` 或 `1080x1920`，4k 为 `3840x2160` 或 `2160x3840`。公开对象的 `created_at` 是绑定创建时间的 Unix 秒。

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

`instances` 必须只有一个非空 prompt；image 必须在 `inlineData` 与 `fileData:{mimeType,fileUri}` 中选择一个。`numberOfVideos` 为 0 时读取 `sampleCount`，两者均为 0 时默认 1，范围 1–4；每个视频创建一个上游任务。`durationSeconds` 接受 JSON integer 或十进制字符串。duration、aspect ratio 与 resolution 省略时分别为 `4`、`16:9`、`720p`，并换成实时模型 `video_durations_seconds`、`video_aspect_ratios`、`video_output_resolutions` 中最接近的取值（时长按秒数、宽高比按比值对数、分辨率按像素高度，距离相同时取较大值）；负数时长返回 400，模型列出对应选项时无法解析的宽高比与分辨率返回 400，未列出时原样发送。创建返回 `{"name":"operations/<ID>"}`，多个视频时 `<ID>` 为各任务 ID 以 `~` 连接；中途创建失败时停止，已创建的任务照常返回，第一个即失败时返回该错误。Gemini operation 使用同一份持久元数据恢复创建账户与轮询上下文；Gemini 响应只包含 `name`、`done` 与完成后的 `response.generateVideoResponse.generatedSamples`。

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

`response` 在全部任务 done 时出现，`generatedSamples` 按创建顺序列出有产物的任务，uri 指向各任务的 `/v1/videos/<任务ID>/content`；无产物时为空。上游已找不到的任务按已结束且无产物处理；其他任务查询失败时返回该错误。

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

Live input modalities 是 text/audio/image 的非空子集，output 按模型能力确定：普通 Live 模型为 `["audio"]`；实时翻译模型（能力码 46）要求 output `["audio"]` 与 `"translation":{"target_language_code":"es","echo_target_language":false}`，输入音频的原文经 `input_transcription`、译文经 `output_transcription` 与 `media` 返回；实时转录模型（能力码 77）要求 output `["text"]`，可带 `"transcription":{"language_codes":["en"]}`，转写经 `interim_input_transcription`（当前累计文本）与 `input_transcription`（最终文本）返回。两类模型不接受 tools，其他模型不接受 translation 与 transcription。Robotics input/output 必须分别为 `["text"]` 与 `["text"]`，不接受 translation 与 transcription。实时音乐模型 input/output 为 `["text"]` 与 `["audio"]`，不接受 tools、translation 与 transcription。数组不接受空字符串或重复项。

setup 之后的客户端对象统一字段为 `type`、可选 `text`、`mime_type`、Base64 `data`、`tool_responses`、`weighted_prompts`、`music_config`、`playback_control`。各帧字段：

| type | 字段 |
| --- | --- |
| `text` | `text` |
| `audio` | `mime_type:"audio/pcm"`、`data` |
| `image` | `mime_type:"image/jpeg"`、`data` |
| `media_end` | 无附加字段 |
| `tool_response` | `tool_responses:[{id,name,content}]` |
| `music_prompts` | `weighted_prompts:[{text,weight}]` |
| `music_config` | `music_config` 对象，字段同 Gemini API `LiveMusicGenerationConfig` |
| `playback` | `playback_control:"play"\|"pause"\|"stop"\|"reset_context"` |
| `close` | 无附加字段 |

- `text` 帧要求 setup 声明 text 输入，`tool_response` 帧要求 setup 声明 tools，`music_prompts` 的 text 非空
- `audio` 与 `image` 省略 `mime_type` 时分别使用 `audio/pcm` 与 `image/jpeg`
- `music_config` 的键可用 snake_case 或 camelCase
- 实时音乐会话只接受 `music_prompts`、`music_config`、`playback` 与 `close`，Live 与 Robotics 会话不接受这三种帧
- `tool_response` 批量发送函数结果，保留调用的 `id` 与 `name`

`tool_response` 示例：

```json
{"type":"tool_response","tool_responses":[{"id":"call-1","name":"get_weather","content":{"temperature":26}}]}
```

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

按 type 使用对应字段：`session_opened{model}`、`setup_complete`、`text{text}`、`media{mime_type,data}`、`input_transcription/output_transcription/interim_input_transcription{transcription}`、`tool_call{tool_call}`、`tool_call_cancellation{tool_call_ids}`、`interrupted`、`generation_complete`、`turn_complete`、`session_resumption{session_token,resumable}`、`usage{raw}`、`go_away{raw}`、`provider{raw}`、`closed`、`error{error,code,retryable,raw?}`。

首帧或后续客户端字段无效时发送 `{type:"error",code:"invalid_request",error:"..."}`。上游错误保留原始 `error` 内容。客户端单帧上限 8 MiB，超限发送 WebSocket close code 1009。每次写操作上限 10 秒；会话结束等待读取和发送 goroutine（Go 协程）的上限各 5 秒。

流式端点统一使用 `text/event-stream`，每个 SSE frame 以空行结束：

| 协议 | 首事件 | 内容序列 | usage | 终止事件 |
| --- | --- | --- | --- | --- |
| OpenAI Chat | assistant role chunk | chat completion delta | `include_usage=true` 时位于 finish chunk 之后 | `data: [DONE]` |
| OpenAI Responses | `response.created`、`response.in_progress` | output item / content part / delta / done | 完成 response 的 `usage` | `response.completed` 或 `response.incomplete` |
| Anthropic | `message_start` | `content_block_start`、delta、`content_block_stop` | `message_delta.usage` | `message_stop` |
| Gemini | candidate Part | `GenerateContentResponse` 增量 | 最后一帧 `usageMetadata` | 最后一帧 finish reason |

上游语义事件间隔达到 10 秒时，四套流式协议发送 SSE 注释帧 `: ping` 并立即 flush。OpenAI Chat 先发送 assistant role chunk，Responses 先发送 `response.created` 与 `response.in_progress`，Anthropic 先发送 `message_start`；这些起始事件在账户调度期间即可到达客户端。Gemini 的首个帧来自上游语义事件或 `: ping`。

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

Responses 媒体使用 image generation item。

OpenAI Chat 使用 Markdown data URL 承载生成图片；客户端把 assistant `message.content` 回传下一轮时，适配器将其中的图片恢复为 inline data Part，保留图片多轮上下文。图片 Base64 支持标准与 URL-safe 字母表、可选填充及 CR/LF 换行；图片前后的文本保持原顺序。

用户文本中的 `youtu.be/<ID>`、`youtube.com/watch?v=<ID>`、`/shorts/<ID>`、`/live/<ID>` 和 `/embed/<ID>` 会转换为 `video/*` 外部媒体 part，并从用户 text part 中移除；重复 URL 合并为一个附件。OpenAI `video_url`/`input_video`、Anthropic URL source 与 Gemini `fileData.fileUri` 使用相同的外部媒体编码。

OpenAI Responses 的 `previous_response_id` 与 Interactions 的 `previous_interaction_id` 在进程内共用最多 256 个响应节点并重建完整 contents；重启后客户端重新提交完整上下文。Drive 与 Veo 资源绑定持久化到磁盘。

`GET /v1/responses/{id}` 返回完整响应对象；`DELETE /v1/responses/{id}` 返回 `{"id":"resp_...","object":"response","deleted":true}`，以它为 `previous_response_id` 的已保存响应保留完整上下文；`POST /v1/responses/{id}/cancel` 对 `background:true` 创建的响应返回其最终响应对象，其他响应返回 400。`GET` 带 `stream=true` 返回 400。未保存、已淘汰或已删除的 ID 返回 404 `not_found`。

`GET /v1/responses/{id}/input_items` 返回 `{"object":"list","data":[...],"first_id":...,"last_id":...,"has_more":...}`，按续接顺序列出前序响应的输入项与输出项及本次输入项。`order` 为 `asc` 或 `desc`（默认），`limit` 为 1–100（默认 20），`after` 与 `before` 取列表中的项 ID，不在列表中时返回 400。字符串输入与字符串 content 展开为 `input_text`（assistant 为 `output_text`）内容数组；缺少 ID 的 message 项编号为 `msg_<响应 ID>_<序号>`，其他项为 `item_<响应 ID>_<序号>`。

`POST /v1/responses/input_tokens` 接受 `POST /v1/responses` 的 input、instructions、tools 与 `previous_response_id`，经同一转换与前序上下文拼接后调用上游 CountTokens，不发起生成；省略 `model` 时使用前序响应的模型。

模型、参数、账户与上游错误按下方状态表投影。客户端取消会关闭上游 reader并释放账户租约。

错误对象与状态语义：

| 情况 | HTTP | OpenAI | Anthropic | Gemini |
| --- | ---: | --- | --- | --- |
| 参数、Schema、tool choice 无效 | 400 | `invalid_request` | `invalid_request_error` | `INVALID_ARGUMENT` |
| 没有符合条件的账户 | 400 | `account_required` | `invalid_request_error` | `INVALID_ARGUMENT` |
| 支持请求的账户均不可调度 | 503 | `account_unavailable` | `api_error` | `UNAVAILABLE` |
| 本地 API key 无效 | 401 | `invalid_api_key` | `authentication_error` | `UNAUTHENTICATED` |
| 模型或方法不存在 | 404 | `model_not_found` | `not_found_error` | `NOT_FOUND` |
| 本地文件不存在 | 404 | `file_not_found` | `not_found_error` | `NOT_FOUND` |
| 上游拒绝权限 | 403 | `upstream_error` | `permission_error` | `PERMISSION_DENIED` |
| 视频仍在生成 | 409 | `video_not_ready` | `api_error` | `INTERNAL` |
| 文件超过 512 MiB | 413 | `file_too_large` | `request_too_large` | `INTERNAL` |
| 上游配额或限流 | 429 | `upstream_error` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| 候选账户均在冷却且 1 分钟内不恢复 | 429 | `rate_limit_exceeded` | `rate_limit_error` | `RESOURCE_EXHAUSTED` |
| 上游过载 | 529 | `upstream_error` | `overloaded_error` | `INTERNAL` |
| 当前客户端请求被管理端取消 | 503 | `request_canceled` | `api_error` | `UNAVAILABLE` |
| 生成服务已停止 | 503 | `service_stopped` | `api_error` | `UNAVAILABLE` |
| 请求期限到期 | 504 | `upstream_error` | `api_error` | `DEADLINE_EXCEEDED` |
| 传输、Content-Type、解码或缺失终态 | 502 | `upstream_error` | `api_error` | `INTERNAL` |

- “没有符合条件的账户”指候选耗尽且没有符合方法、能力与权益的账户
- “支持请求的账户均不可调度”指这些账户都处于需要重新登录、不可用或已停用状态，错误消息逐个列出账户、状态与原因
- 生成服务处于 `STOPPED` 时，生成与计数端点返回 `service_stopped`

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
