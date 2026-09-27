# WAA 实现

WAA（Web Application Attestation）为 AI Studio 的受保护 RPC 提供 BotGuard proof。服务在每个账户的 WAA Worker 中持有一份 BotGuard VM，对每个请求的 binding 摘要调用 `snapshot` 取得 `!` 开头的 proof，再写入请求正文。本文定义两种 WAA 后端的职责边界、纯 Go 后端的完整链路、Firefox 形状宿主、数据文件的生成方法、上游变化的定位方法与 goja 分叉。受保护请求的 wire 字段见 [协议规范](protocol.md)，Build 代理见 [Build 通道](build.md)。

## 1. 后端与职责边界

`WAA_BACKEND` 选择承担 BotGuard 生命周期的后端：

| 值 | VM 位置 | 受保护请求发送 | 浏览器依赖 |
| --- | --- | --- | --- |
| `camoufox`（默认） | 账户固定指纹的 Camoufox 页面 | 页面原生 `fetch` | 启动时定位或下载 Camoufox |
| `go` | 服务进程内的 goja VM | Go HTTP，Firefox 152 网络形状 | 仅账户登录与验证时按需准备 |

- 配置来源为 `.env`、管理页面设置中的“WAA 后端”或 `PUT /api/config` 的 `waa_backend`；取值去除空白并转小写，其他值在配置校验时报错；保存值在下一次启动生成服务时生效
- `go` 后端装配运行时时输出 `运行时装配 | 2/3 | WAA 后端=go | 账户=<数量>`，不定位、不下载、不启动 Camoufox，也不清理遗留 Camoufox profile
- 账户页的浏览器登录与账户验证使用按需创建的登录驱动：第一次调用时定位 Camoufox，缺失时下载当前平台的固定版本，之后复用该驱动
- 账户指纹由 Go 生成并保存到 `camoufox-fingerprint.json`，两种后端读取同一份文件
- Worker 配置没有 Camoufox 路径时启动纯 Go Worker；Worker 状态中 `RuntimeID` 为 `go-waa`，`PID` 为服务进程 PID
- 调度、热池容量、runtime 租约、重试与冷却在两种后端上相同，见 [开发与贡献](development.md)
- 纯 Go 后端不含平台相关代码，随同一 Go 程序在各发布平台运行；浏览器登录依赖 Camoufox 发行包覆盖的 Windows、Linux 与 macOS

两种后端通过同一组 Worker 能力接入请求编码器：

| 能力 | 语义 |
| --- | --- |
| `Proof(digest, prompt)` | 同步页面提示词后为 SHA-256 摘要生成 fresh proof |
| `ProtocolHeaders` | 返回官网公共协议头 |
| `SendProtected(url, headers, body)` | 以官网页面的请求形状流式发送受保护请求 |
| `StorageCookies` | 导出 runtime 当前 Cookie |
| `State`、`Close` | 状态与关闭 |

`NativeWorker` 把两种 runtime 适配为 `ProtectedPreparer`：计算 binding 的 SHA-256 小写十六进制摘要，取 proof，把 proof 写入正文的目标 field，并附上公共协议头。受保护 RPC 使用以下 field 与 binding：

| RPC | proof field | binding | 发送方 |
| --- | ---: | --- | --- |
| `GenerateContent` | 5 | contents 各 part 以空格连接 | Worker `SendProtected` |
| `CreateInteractionStream` | 5 | 发送的全部文本以空格连接 | Worker `SendProtected` |
| `ProxyStreamedCall`、`ProxyUnaryCall` | 3 | `<路径> <请求体>` | Worker `SendProtected` |
| `GenerateVideo` | 8 | 视频提示词 | MakerSuite Go HTTP transport |
| Bidi setup | 6 | `models/<模型>` 与每个函数的 `名称 描述`，以空格连接 | WebChannel |
| Bidi text、audio、image、media end | 6 | 空字符串 | WebChannel |
| Bidi tool response | 6 | 第一条 function response 的 call ID | WebChannel |

`GenerateContent` 的 part 取值：

| Part | binding 取值 |
| --- | --- |
| text | 原始文本 |
| inline data | 原始字节的标准 Base64 |
| Drive file | file ID |
| external media、function、function result、code、thought signature | 空字符串 |

user 文本中的 YouTube 链接先转为 external media part，链接从文本中移除后再参与 binding。

同一账户的 proof 串行生成：`NativeWorker`、纯 Go runtime 与 VM 各持有一把操作锁。服务不调用 `Waa/Ping`。

### Camoufox 后端

1. Go 启动隔离、无头的 Camoufox，通过 WebDriver BiDi 建立 session，写入账户 Cookie 与 localStorage
2. 打开 `/prompts/new_chat?model=<bootstrap 模型>`，`TEMPORARY_CHAT=true` 时追加 `&temporary=true`；等待提示词输入框，识别 Google 登录跳转并关闭已知弹层
3. 在页面 bundle 的 `default_MakerSuite` 命名空间中定位调用 `.snapshot({` 且包含 `content` 的高层函数
4. 填入唯一 bootstrap 提示词，为官网 `GenerateContent` 安装 `beforeRequestSent` 拦截并点击 Run
5. 页面调用 snapshot 时保存 WAA service；请求进入拦截阶段后保存公共请求头，通过 `network.failRequest` 在浏览器内终止，并校验请求正文中的实际模型
6. 每个请求先把提示词写入页面输入框（最多重试 5 秒直到页面值一致），再调用保存的 service 取 proof；受保护请求由页面原生 `fetch` 发送，响应经 BiDi 分块交回 Go

- bootstrap 的 `GenerateContent` 在发往上游前终止，不产生生成用量；`TEMPORARY_CHAT=true` 同时关闭预热页的自动保存
- 取消 Worker 启动会关闭 BiDi、终止 Camoufox 进程树并删除临时 profile；启动页面停在其他状态时返回包含当前 URL 的启动错误

bootstrap 模型优先使用实时目录中的 `gemini-flash-latest`，否则按目录顺序选择支持 `generateContent`、账户权益与聊天能力的模型。两种后端使用同一选择规则，一个账户 Worker 为该账户的全部普通生成模型提供 proof，切换业务模型直接复用当前 Worker。

## 2. 纯 Go bootstrap 与网络输入

### 运行链

```text
Worker 启动
  storage-state.json + camoufox-fingerprint.json
  -> GET /prompts/new_chat?model=<bootstrap 模型>      页面 API key
  -> GetLoggingContext                                  x-goog-ext-519733851-bin
  -> Waa/Create ["lmnUSbltwc5ULv48iKLX"]                challenge
  -> interpreter：challenge 内联 / 内存 / 磁盘缓存 / 下载，SHA-256 校验
  -> goja 顶层 Realm 安装 Firefox 宿主
  -> 执行 interpreter，调用 <globalName>.a(program, ...)
       program 创建 iframe Realm、/generate_204 图片请求与事件监听
  -> 写入 bootstrap 提示词，派发 input/change，点击 Run
  -> 等待图片请求完成并静置 3 秒 -> ready

每个受保护请求
  -> 写入提示词，派发 input/change
  -> snapshot(callback, [{content: SHA256(binding)}, undefined, undefined, undefined])
  -> "!" proof 写入 field 5 / 3
  -> SAPISID Authorization + 公共协议头 + Firefox 请求头 + Cookie
  -> 账户固定出口 POST，流式返回
  -> Set-Cookie 合并到 runtime，再写回 storage-state.json

每 12 小时
  -> snapshot(callback, [undefined, undefined, undefined, undefined])
  -> Waa/Create [key, interpreterHash, 上一 VM snapshot]
  -> 新 VM 就绪后关闭旧 VM
```

Worker 启动日志与 Camoufox 后端共用 7 个阶段编号，纯 Go 后端输出第 1、2、5、6、7 阶段：

| 阶段 | 日志 | 纯 Go 动作 |
| ---: | --- | --- |
| 1 | `初始化页面`，附页面模型 | 取得 runtime 租约 |
| 2 | `准备浏览器配置` | 读取 Cookie 与指纹，创建出口客户端 |
| 5 | `载入 AI Studio` | 请求首页与 `GetLoggingContext` |
| 6 | `定位 WAA 服务` | 调用 `Waa/Create` |
| 7 | `执行 WAA Bootstrap` | 加载解释器、初始化 VM、bootstrap 交互与静置 |

### 账户、指纹与网络形状

#### Cookie

Worker 启动时把 `storage-state.json` 的 Cookie 读入 runtime 内存。每个出站请求按目标 URL 过滤过期、domain、path 与 Secure 条件后生成 `Cookie` 头；响应 `Set-Cookie` 在响应头到达时合并回 runtime。页面 `document.cookie` 为 domain 匹配 `aistudio.google.com` 且非 HttpOnly 的 Cookie，以 `; ` 连接。页面 `localStorage` 的读写使用 VM 内存存储，初始为空。

#### 指纹与宿主现场值

`camoufox-fingerprint.json` 不存在时按 Firefox 152、账户 locale 与 timezone 生成；两种后端使用同一份配置。纯 Go 后端的映射：

| 指纹键 | 宿主取值 |
| --- | --- |
| `navigator.*` | `navigator` 同名属性 |
| `screen.*` | `screen` 同名属性 |
| `window.*` | 顶层 Window 同名属性 |
| `navigator.language` | 时区显示名的区域设置 |
| `timezone` | Date 本地时区与时区显示名 |
| `navigator.userAgent` | `User-Agent` 请求头 |
| `headers.Accept-Language` | `Accept-Language` 请求头 |

- `innerWidth` 等于 `outerWidth`，`innerHeight` 等于 `outerHeight` 减 57
- `navigator.doNotTrack` 为 `"unspecified"`，`navigator.globalPrivacyControl` 为 `false`，`document.hasFocus()` 为 `true`
- iframe Realm 的 Window 只继承 `outerWidth`、`outerHeight`、`screenX`、`screenY`、`screenLeft`、`screenTop`、`devicePixelRatio`、`mozInnerScreenX`，其余取形状表的 iframe 取值
- `location` 与 `document.URL` 为 bootstrap 页面地址
- 指纹缺少 UA 时使用 Windows Firefox 152 UA，缺少 `Accept-Language` 时使用 `en-US,en;q=0.5`，缺少时区时使用 `UTC`

#### 网络形状

所有出站请求经账户代理（未设置时使用全局 `PROXY`），使用 Firefox 152 的 TLS ClientHello、HTTP/2 设置与伪头顺序，不自动跟随跳转。请求头按以下顺序发送，未列出的头排在其后：

```text
user-agent, accept, accept-language, accept-encoding, referer, content-type,
x-goog-api-key, x-goog-authuser, x-user-agent, x-aistudio-g1-tier,
x-aistudio-visit-id, x-goog-ext-519733851-bin, authorization, cookie,
origin, sec-fetch-dest, sec-fetch-mode, sec-fetch-site, priority, te
```

每个请求都带 `User-Agent`、`Accept-Language`、`Accept-Encoding: gzip, deflate, br, zstd` 与匹配的 `Cookie`。各类请求的其他头：

| 请求 | 方法 | 请求头 |
| --- | --- | --- |
| 首页 | GET | `Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8`、`Upgrade-Insecure-Requests: 1`、`Sec-Fetch-Dest: document`、`Sec-Fetch-Mode: navigate`、`Sec-Fetch-Site: none`、`Sec-Fetch-User: ?1`、`Priority: u=0, i` |
| `GetLoggingContext`、`Waa/Create` | POST | RPC 头 |
| 解释器 | GET | `Accept: */*`、`Referer`、`Sec-Fetch-Dest: script`、`Sec-Fetch-Mode: no-cors`、`Sec-Fetch-Site: cross-site` |
| 页面图片 | GET | `Accept: image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5`、`Referer`、`Sec-Fetch-Dest: image`、`Sec-Fetch-Mode: no-cors`、`Sec-Fetch-Site: same-origin` |
| 受保护业务 RPC | POST | 公共协议头、`Authorization`、`Accept: */*`、`Referer`、`Origin`、`Sec-Fetch-Dest: empty`、`Sec-Fetch-Mode: cors`、`Sec-Fetch-Site: same-site` |

RPC 头为 `Accept: */*`、`Referer: https://aistudio.google.com/`、`Content-Type: application/json+protobuf`、`X-Goog-Api-Key`、`X-Goog-AuthUser: 0`、`X-User-Agent: grpc-web-javascript/0.1`、`Authorization`、`Origin: https://aistudio.google.com`、`Sec-Fetch-Dest: empty`、`Sec-Fetch-Mode: cors`、`Sec-Fetch-Site: same-site`。`Authorization` 为三段 SAPISID 签名，算法见 [协议规范](protocol.md)。

### 首页、公共头与 Waa/Create

#### 首页

runtime 以浏览器导航形状请求 `https://aistudio.google.com/prompts/new_chat?model=<bootstrap 模型>`，`TEMPORARY_CHAT=true` 时追加 `&temporary=true`。3xx 按 `Location` 继续，最多 5 次；跳转到 `accounts.google.com` 时启动失败并报告登录态失效。HTTP 200 页面中 `"WIu0Nc":"<值>"` 的值为页面 API key，缺失时启动失败。

#### GetLoggingContext 与扩展头

`GetLoggingContext` 使用页面 API key 与 RPC 头，正文为 `[]`。响应是 JSON+protobuf 数组，runtime 把它逐字段编码为 protobuf 二进制，再用标准 Base64 编码为 `x-goog-ext-519733851-bin`：

| JSON 值 | protobuf 编码 |
| --- | --- |
| `null` 或缺失 | 跳过 |
| 字符串 | wire type 2，varint 长度加 UTF-8 字节 |
| `true`、`false` | wire type 0，值 1 或 0 |
| 整数 | wire type 0 varint |

field number 为 JSON 索引加 1。其他 JSON 类型使编码失败。

#### 公共协议头

`ProtocolHeaders` 返回以下 6 个头，受保护请求在其上叠加 RPC 自身的 `Content-Type` 与权益头：

| Header | 取值 |
| --- | --- |
| `user-agent` | 指纹 UA |
| `x-goog-api-key` | 页面 API key |
| `x-goog-authuser` | `0` |
| `x-user-agent` | `grpc-web-javascript/0.1` |
| `x-aistudio-visit-id` | `v1_` 加 UUIDv4 文本的标准 Base64 |
| `x-goog-ext-519733851-bin` | `GetLoggingContext` 编码结果 |

图片路由的 `GenerateContent` 不带 `x-goog-ext-519733851-bin`。

#### Waa/Create 请求

`POST https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/Create` 使用 RPC 头，`X-Goog-Api-Key` 为 WAA 专用的公开 key（代码常量 `waaAPIKey`，与页面 API key 不同）：

| protobuf field | 内容 |
| ---: | --- |
| 1 | request key `lmnUSbltwc5ULv48iKLX` |
| 2 | 当前 VM 的 interpreter hash |
| 3 | 当前 VM 的无绑定 snapshot |

Worker 启动时只设置 field 1，末尾未设置的 field 省略：

```json
["lmnUSbltwc5ULv48iKLX"]
```

VM 刷新时三个 field 都设置。官网在 snapshot 超时时写 `E:CTO`，其他异常写 `E:UCE`；当前纯 Go runtime 的 snapshot 错误统一写 `E:UCE`：

```json
["lmnUSbltwc5ULv48iKLX", "<INTERPRETER_HASH>", "<PREVIOUS_SNAPSHOT>"]
```

#### challenge 解码

响应外层索引 `1` 是 Base64 字符串。标准 Base64 解码后每个字节加 97（按字节回绕），得到 UTF-8 JSON 数组。外层索引 `1` 为空或缺失时，外层索引 `0` 是明文 challenge 数组，字段布局相同；两者都为空时 Create 失败：

| JSON 索引 | 字段 | 读取规则 |
| ---: | --- | --- |
| 0 | `MessageID` | 字符串 |
| 1 | `InterpreterJavaScript` | 列表中第一个非空字符串 |
| 2 | `InterpreterURL` | 列表中第一个非空路径，补 `https:` 前缀 |
| 3 | `InterpreterHash` | 字符串，必需 |
| 4 | `Program` | 字符串，必需 |
| 5 | `GlobalName` | 字符串，必需 |
| 6 | 未使用 | |
| 7 | `ClientExperimentsStateBlob` | 字符串 |

数组少于 8 项或必需字段为空时 Create 失败。当前捕获样本的 `MessageID` 为 `bfkj`，`GlobalName` 为 `botguard`，interpreter 路径为 `//www.google.com/js/bg/<INTERPRETER_HASH>.js`。runtime 始终从每次 challenge 动态读取这些字段；program 每次 Create 都不同，属于当前 VM 生命周期。

#### client experiments

`ClientExperimentsStateBlob` 是 JSON 数组，派生初始化参数中的 signal lists 与 persistent state：

- 索引 5 为 `[[value, key], ...]`；`key` 不大于 53 的 value 按出现顺序进入第一组，其余进入第二组
- signal lists 为 `[[第一组 value..., 第二组 value...], [第一组各项 1..., 第二组各项 2...]]`
- 索引 4 为非空字符串时作为 persistent state，否则为 `undefined`
- blob 为空时 signal lists 为 `[[], []]`

当前捕获样本的 blob 为 `[null,null,null,null,null,null,null,[],[]]`，对应 `[[], []]` 与 `undefined`。runtime 从 challenge 动态解析该字段，不固定样本值。

## 3. 解释器、VM 与 Realm

### 解释器

解释器摘要为源码字节的 SHA-256，编码为 Base64URL 无 padding，必须等于 challenge 的 `InterpreterHash`。runtime 按以下顺序取得源码：

1. challenge 的 `InterpreterJavaScript` 非空时直接使用
2. hash 与当前 VM 相同时复用内存中的源码
3. 读取 `<账户根目录>/.waa-interpreters/<hash>.js`，摘要一致时使用
4. 按 `InterpreterURL` 下载，HTTP 200 且摘要一致后写入缓存

账户根目录是账户目录的上级目录，默认 `auth/`。解释器以 `InterpreterURL`（缺失时为 `https://www.google.com/js/bg/<hash>.js`）作为脚本来源名执行，该名称出现在 `Error.stack` 与 `fileName` 中。

### 事件循环

每个 VM 使用一个 goroutine 串行执行任务队列。VM 初始化、提示词写入、snapshot、计时器回调与图片加载完成回调都以任务形式入队；计时器到期后把回调追加到队尾。微任务由 goja agent 在最外层脚本返回时依次执行，顶层 Realm 与 iframe Realm 共享同一个微任务队列。

### Realm

| Realm | 创建时机 | 形状 | 全局名称顺序 |
| --- | --- | --- | --- |
| 顶层 | 执行解释器前 | `top` | 官网页面采集时的全局键顺序 |
| iframe | 脚本把 iframe 挂到已连接文档，或读取已连接 iframe 的 `contentWindow` | `frame` | 新建同源 iframe 的首次枚举顺序 |

- iframe Realm 通过 goja 分叉的 `NewRealm` 创建，与顶层共享堆、调用栈与微任务队列，拥有独立的全局对象、内建对象、`Math.random` 序列、计时器编号与 `performance` 起点
- iframe 挂载后通过 `setTimeout(0)` 在 iframe 元素上派发 `isTrusted=true` 的 `load` 事件
- 两个 Realm 共享品牌表、原生函数源码表与 Trusted Types 记录，因此跨 Realm 传递的 DOM 对象保留接口身份

### 初始化调用

runtime 执行解释器后读取全局 `<GlobalName>`，调用其方法 `a`，`this` 为该全局对象：

```javascript
botguard.a(program, ready, true, undefined, passEvent, signalLists, persistentState, false, loggers)
```

| 位置 | 参数 | 取值 |
| ---: | --- | --- |
| 1 | program | challenge `Program` |
| 2 | ready | 回调，参数 1 为 snapshot 函数，参数 2 为 shutdown 函数 |
| 3 | 启用标志 | `true` |
| 4 | environment | `undefined` |
| 5 | passEvent | 空回调 `(v, x, C, G) => {}` |
| 6 | signal lists | client experiments 派生值 |
| 7 | persistent state | client experiments 派生值 |
| 8 | secondary flag | `false` |
| 9 | loggers | 4 个空回调 |

ready 回调收到函数后 VM 进入可用状态。解释器没有定义全局对象、缺少方法 `a` 或 ready 的首个参数不是函数时初始化失败。

### bootstrap 交互与静置

初始化后 runtime 向页面输入框写入 bootstrap 提示词（默认 `AIStudio2API bootstrap <UnixNano>`），派发 `input` 与 `change` 并点击 Run 按钮，然后等待 VM 发出的全部图片请求完成，再静置 3 秒。纯 Go 页面没有 Angular 应用，点击只产生事件，不发送 `GenerateContent`。

## 4. Firefox 宿主与键顺序

`internal/waa/dom.js` 在每个 Realm 中按形状表 `firefox152.json` 生成 Window、WebIDL 接口对象与实例。宿主脚本的来源名为 `\x00waa-host`，这些帧不出现在 `Error.stack` 中，宿主脚本执行期间也不触发惰性全局名称解析。

### 形状表结构

当前数据采集自 Windows Firefox 152 的已登录 AI Studio 页面：

| 键 | 内容 |
| --- | --- |
| `userAgent`、`capturedAt` | 采集浏览器 UA 与时间 |
| `interfaces` | 714 个接口对象，父接口在前 |
| `frame` | iframe Realm：`global` 992 个全局自有属性、`chain` Window 原型链、`windowValues` Window 取值、`freshKeys` 992 个首次枚举名 |
| `top` | 顶层 Realm：`global` 1058 个全局自有属性、`chain`、`windowValues` |
| `namespaces` | `CSS`、`console`、`WebAssembly`、`Intl` 的成员 |
| `defaults` | 143 个接口的实例取值 |
| `own` | 实例自有属性，如 `Location` 成员与事件的 `isTrusted` |
| `singletons` | iframe Realm 的 46 个单例路径 |
| `topSingletons` | 顶层 Realm 的 13 个单例路径 |
| `tagMap` | 140 个标签名到元素接口 |
| `mediaQueries` | 77 条媒体查询 |
| `builtins` | 178 个内建对象路径的自有成员 |
| `promises` | 207 个 Promise 返回成员 |

`interfaces` 条目：

| 字段 | 含义 |
| --- | --- |
| `n` | 全局名 |
| `cn` | 构造器 `name`，别名接口与 `n` 不同 |
| `p` | 构造器的原型父接口 |
| `pp` | `prototype` 的原型父接口 |
| `l` | 构造器 `length` |
| `pw` | `prototype` 属性是否可写 |
| `st`、`pr` | 静态成员与 `prototype` 成员 |
| `call` | 不带 `new` 调用的结果文本 |
| `construct` | 无参 `new` 的结果，`ok:<标签>` 或错误文本 |

```json
{"n":"Blob","cn":"Blob","p":null,"pp":"Object","l":0,"pw":false,"st":[],
 "pr":[{"n":"slice","f":"cew","k":"m","l":0,"fn":"slice","nat":true}],
 "call":"TypeError: Blob constructor: 'new' is required","construct":"ok:Blob"}
```

成员条目：

| 字段 | 含义 |
| --- | --- |
| `n` | 属性名；Symbol 键写为 `@@<描述>`，去掉 `Symbol.` 前缀 |
| `f` | 描述符标志：`c` configurable、`e` enumerable、`w` writable |
| `k` | `v` 数据、`m` 方法、`a` 访问器、`i` 接口构造器；`builtins` 中另有 `o` 对象值 |
| `v` | 数据值编码 |
| `l`、`fn`、`nat` | 函数 `length`、`name`、是否原生 |
| `g`、`gl`、`s`、`sl` | getter 与 setter 的 `name` 和 `length` |
| `ctor` | `builtins` 方法是否为构造器 |
| `tag` | `builtins` 对象值的 `Object.prototype.toString` 结果 |
| `err` | 采集时读取描述符失败 |

值编码：

| `t` | 值 |
| --- | --- |
| `u`、`null` | `undefined`、`null` |
| `s`、`b` | 字符串、布尔，值在 `v` |
| `n` | 数值；`NaN`、`-0`、`Infinity` 以字符串保存 |
| `bigint`、`sym` | BigInt 十进制字符串、Symbol 描述 |
| `a` | 基本值数组 |
| `ref` | 单例路径，如 `frame.navigator`、`top.document` |
| `f` | 函数，`v` 为名称 |
| `o` | 对象，`c` 为 toString 标签，`k` 为构造器名 |
| `throw` | 读取时抛出的错误文本 |

`singletons` 与 `topSingletons` 的每项为 `{iface, values, own}`，`values` 是沿原型链读取全部访问器得到的取值。`mediaQueries` 的值为 `[顶层 matches, media 文本, iframe matches]`。`promises` 的条目为 `接口.成员`，访问器写为 `接口.成员:get`。

### 接口对象与品牌检查

接口按表序生成。goja 已提供的 ECMAScript 内建类直接复用；其他接口生成 `prototype`（继承 `pp` 的 prototype）与构造器（继承 `p` 的构造器），安装 `st` 与 `pr` 成员，别名接口指向同一构造器。

- 构造器不带 `new` 调用时按 `call` 抛出对应错误，`call` 为 `ok` 时等同构造；带 `new` 时按 `construct` 创建实例或抛错，`At least N arguments required` 在参数足够时创建实例
- `Event`、`CustomEvent`、`UIEvent`、`MouseEvent`、`KeyboardEvent`、`FocusEvent`、`IntersectionObserver`、`Image`、`Audio`、`Option` 与 `HTMLImageElement` 使用宿主构造逻辑；其他事件接口在提供 type 时按 init 字典写入已声明的属性
- 访问器与方法先做品牌检查：`this` 不是该接口或其子接口的实例时抛出 `TypeError: '<成员>' called on an object that does not implement interface <接口>.`
- `promises` 中的成员品牌检查失败时返回 rejected Promise；没有宿主实现时返回永不完成的 Promise
- 宿主函数的 `name`、`length` 与 `Function.prototype.toString` 结果与 Firefox 原生函数一致

### Window 与全局属性

每个 Realm 的全局对象原型链为 `Window.prototype -> WindowProperties -> EventTarget.prototype`。宿主按 Realm 形状的 `global` 顺序重建全局自有属性：接口写入构造器，方法优先保留已有实现（goja 内建函数与宿主计时器），访问器读取 `windowValues` 与指纹覆盖值，命名空间生成对应对象。整数键、`undefined`、`NaN` 与 `Infinity` 保留引擎定义；形状表中不存在的可配置全局属性被删除。

### 实例取值

每个宿主对象在品牌表中记录接口、取值表、写入覆盖值、缓存、DOM 父子关系、属性与事件监听。访问器按以下顺序取值：

1. 写入覆盖值与指纹覆盖值
2. 已缓存的对象、数组与函数值
3. 宿主实现的特殊 getter
4. 实例取值表，缺失时沿父接口查 `defaults`

赋值按采集值的类型转换后写入覆盖值。实例创建时安装本接口或最近祖先接口的 `own` 条目。单例在首次访问时按 `topSingletons`（顶层）或 `singletons` 生成，`document` 同时建立 `html`、`head`、`body` 三个元素。

### 宿主对象行为

| 对象 | 行为 |
| --- | --- |
| Node、Element | 插入、移除、替换、克隆、包含判断、父子与兄弟节点、`isConnected` |
| 属性与选择器 | `id`、`class` 与任意属性；选择器支持标签、`#id`、`.class`、`*` 与逗号列表 |
| Document | `createElement` 按 `tagMap` 选择接口，含 `-` 的未知标签为 `HTMLElement`，其余为 `HTMLUnknownElement`；`createTextNode`、`createComment`、`createDocumentFragment`、`createEvent`、`createRange`、`getElementById` |
| 事件 | 捕获、目标、冒泡三阶段；`once`、`capture`、同一监听器去重、`on<type>` 属性、`stopPropagation`、`stopImmediatePropagation`、`preventDefault` |
| 布局 | 顶层 `html` 与 `body` 的矩形为视口大小，其余为 0；`offsetHeight` 按内联样式、子元素与文本行高计算 |
| `IntersectionObserver` | `observe` 后 16 ms 回调一次，时间戳对齐 60 Hz 帧 |
| `getComputedStyle`、`matchMedia` | 使用采集默认值与媒体查询表；未收录的 `min/max-width/height` 按视口计算 |
| Storage | `getItem`、`setItem`、`removeItem`、`key`、`clear` 使用内存存储 |
| Location | `toString` 返回 `href`，`assign`、`replace`、`reload` 不导航 |
| Performance | `now`、`timeOrigin`、`toJSON`，`getEntries*` 只返回导航条目 |
| Navigator | `javaEnabled` 为 `false`，`sendBeacon` 为 `true`，`getGamepads` 为空，`permissions.query` 返回 `prompt` |
| Canvas、GPU | `getContext('2d')` 返回 `CanvasRenderingContext2D` 实例，`requestAdapter` 返回 `null` |
| Trusted Types | `createPolicy` 与 `createHTML`、`createScript`、`createScriptURL`；`TrustedScript` 传给 `eval` 时解包为源码 |
| 图片 | 设置 `src` 后经宿主网络请求，完成后派发 `load` 或 `error` |

### Realm 基础能力

| 能力 | 行为 |
| --- | --- |
| `setTimeout`、`setInterval` | 每个 Realm 独立编号；`setInterval` 最小 4 ms；回调的 `this` 为 Realm 全局对象，附加参数原样传递 |
| `requestIdleCallback` | 延迟在 0 ms 与 4 ms 间交替，`timeRemaining()` 为 4 或 0 |
| `requestAnimationFrame` | 时间戳对齐 60 Hz 帧边界（偏移 0.66 ms） |
| `performance.now` | Realm 起点以来的整数毫秒；顶层 Realm 起点比宿主安装时刻早 2.5 秒 |
| `Math.random` | SpiderMonkey XorShift128+，每个 Realm 独立随机种子 |
| `atob`、`btoa` | 去除 ASCII 空白后解码；非法输入抛出 `DOMException`（`InvalidCharacterError`） |
| `queueMicrotask` | 进入 agent 共享微任务队列 |
| `eval` | 接受字符串与 `TrustedScript` |
| 调用栈 | 深度上限 20000，超出抛出 `InternalError: too much recursion` |
| Date | 使用账户时区，时区注释见下节 |

### 时区与 Date 字符串

Date 的本地时间使用指纹 `timezone`。`Date.prototype.toString` 与 `toTimeString` 末尾的时区注释使用 Firefox Intl 长时区名，例如 `GMT+0800 (台北標準時間)`。显示名取自 `timezones.json.gz`：

1. 按指纹 `navigator.language` 选择区域设置表：精确匹配，其次忽略大小写匹配；`zh-HK`、`zh-MO` 使用 `zh-HK`，`zh-TW` 与 `Hant` 使用 `zh-TW`，其他中文使用 `zh-CN`；英文使用 `en-US`；其他语言使用排序后首个同语言表；都没有时使用 `en-US`
2. 表中该时区只有一个名称时始终使用它
3. 有两个名称时，当前时刻的夏令时状态与当年 1 月 15 日 12:00 相同时使用第一个（1 月名称），否则使用第二个（7 月名称）
4. 表中没有该时区时使用 `GMT±HH:MM`，零偏移为 `GMT`

### 惰性全局名称与内建键顺序

program 在自建 iframe 中调用 `Object.getOwnPropertyNames(window)`，按随机下标取接口名，再枚举这些接口 prototype 的成员名写入 proof。全局对象与内建对象的自有键顺序因此必须与 Firefox 一致。

#### 惰性全局名称

SpiderMonkey 与 Gecko 惰性定义标准类和 WebIDL 名称：名称在首次使用时才成为已定义属性，并追加到已定义属性的末尾。每个 Realm 的全局对象由 `firefoxGlobalOrder` 维护两张表：

| Realm | 惰性名称表 | 已定义属性初始顺序 |
| --- | --- | --- |
| iframe | `frame.freshKeys` 中 `Function` 之前的名称 | `frame.freshKeys` 从 `Function` 起的名称 |
| 顶层 | 只有 `undefined` | `top.global` 顺序 |

iframe 惰性名称表依次为 `undefined`、`globalThis`、SpiderMonkey 标准类（`Boolean`、`JSON`、`Date`、`Math`、`Number`、`String`、`RegExp`、错误类、TypedArray 等）、`NaN`、`Infinity`、全局函数与 WebIDL 名称，均按引擎表序。

以下情况在非宿主脚本中解析名称：

- 对全局对象按名称读取、`in`、`hasOwnProperty`、`getOwnPropertyDescriptor`、赋值、`defineProperty` 与 `delete`
- 引擎内部使用内建原型：字符串属性访问（`length` 除外）解析 `String`；数值、布尔、正则、Symbol、Date、WeakMap、WeakSet、BigInt 原型分别解析同名类；数组、Map、Set、字符串、正则迭代器与生成器解析 `Iterator`；创建内建错误解析对应错误类
- 宿主把 DOM 对象交给脚本（访问器返回值，或宿主安装完成后新建的实例）时解析其接口
- 第一次枚举全局对象时解析 `globalThis`

解析一个名称时，按组把组内仍为惰性的名称依次追加到已定义属性末尾：

| 名称 | 组 |
| --- | --- |
| `Number` 及其全局函数 | `isNaN`、`isFinite`、`parseInt`、`parseFloat`、`NaN`、`Infinity`、`Number` |
| `String` 及其全局函数 | `escape`、`unescape`、`decodeURI`、`encodeURI`、`decodeURIComponent`、`encodeURIComponent`、`String` |
| 错误子类与 `WebAssembly` | `Error`，然后是该名称 |
| WebIDL 接口 | 从最远祖先接口到自身；`HTMLImageElement`、`HTMLAudioElement`、`HTMLOptionElement` 之后追加 `Image`、`Audio`、`Option` |
| `Image`、`Audio`、`Option` | 与对应元素接口相同 |
| 其他 | 该名称 |

脚本定义的新全局属性追加到已定义属性末尾，被删除的属性从中移除。

#### 全局枚举顺序

`Object.getOwnPropertyNames`、`Object.keys`、`Reflect.ownKeys` 与 `for-in` 枚举全局对象时，字符串键按以下顺序排列，Symbol 键排在其后：

1. 数组索引键
2. `undefined`
3. `globalThis`（第一次枚举时解析）
4. 仍未解析的惰性名称，按表序
5. 已定义属性，按定义顺序
6. 其余键

#### 内建对象成员与键顺序

宿主按形状表 `builtins` 逐个对齐内建对象（构造器、prototype、命名空间与 `%TypedArray%`、`%IteratorPrototype%` 等内在对象）：

1. 补齐 goja 缺少的成员：有宿主实现的使用实现，其余为返回 `undefined` 的原生形状函数或访问器
2. 删除形状表未列出的可配置成员
3. 按形状表顺序重排字符串键（goja 分叉的 `Object.OrderOwnKeys`），未列出的键保持原相对顺序排在其后

宿主实现覆盖 `String.prototype` 的 HTML 方法与 `isWellFormed`、`toWellFormed`，`Date.prototype.getYear`、`setYear`、`toGMTString`，`Object.prototype.__defineGetter__` 系列，`Object.groupBy`、`Map.groupBy`、`Array.fromAsync`、`Promise.withResolvers`、`Promise.try`、`Error.captureStackTrace`、`Math.f16round`、`Math.sumPrecise`、Set 集合运算、`getOrInsert` 系列、`RegExp.escape`、`Uint8Array` Base64 方法、`Atomics` 基本操作、Iterator helper 与 dispose 方法，以及 `RegExp.prototype.hasIndices`、`unicodeSets` 和 `ArrayBuffer.prototype` 的 `maxByteLength`、`resizable`、`detached` 访问器。goja 的 `Iterator` 与 `%IteratorPrototype%` 不对应时替换为 Firefox 形状的 `Iterator` 构造器。

## 5. 提示词、事件与受保护请求

### 提示词写入

每次取 proof 前与 bootstrap 时，runtime 在事件循环中执行与 Camoufox Worker 相同的页面脚本：

```javascript
((prompt, submit) => {
  let box = document.querySelector('ms-prompt-box');
  if (!box) {
    box = document.createElement('ms-prompt-box');
    document.body.appendChild(box);
    box.appendChild(document.createElement('textarea'));
    const run = document.createElement('ms-run-button');
    document.body.appendChild(run);
    run.appendChild(document.createElement('button'));
  }
  const textarea = box.querySelector('textarea');
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, prompt);
  textarea.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: prompt }));
  textarea.dispatchEvent(new Event('change', { bubbles: true }));
  if (submit) document.querySelector('ms-run-button').querySelector('button').click();
  return textarea.value;
})
```

`textarea` 写入时把 CRLF 与 CR 规范为 LF，脚本返回值必须等于按此规则规范后的提示词，否则本次 proof 失败。第一次调用在 `body` 下创建输入框与 Run 按钮，之后复用。写入的是 binding 使用的真实提示词。

### 事件语义

官网 VM 在 `body` 上以 capture 方式监听鼠标、键盘、指针、焦点、输入与剪贴板事件，监听器随 VM 存活，收到的事件进入 proof。没有收到任何交互事件的 VM 生成的 proof 会被上游拒绝，因此 bootstrap 与每次取 proof 都派发 `input` 与 `change`。

`input` 从 `textarea` 冒泡经 `ms-prompt-box`、`body`、`html`、`document` 到 `window`；捕获监听器按 `window -> document -> html -> body` 先于目标执行。事件的 `isTrusted`：

| 来源 | `isTrusted` |
| --- | --- |
| 脚本构造或 `createEvent` 创建的事件，含提示词的 `input`、`change` | `false` |
| `HTMLElement.click()` 产生的 `PointerEvent` | `false` |
| iframe 挂载后的 `load` | `true` |
| 图片请求完成后的 `load`、`error` | `true` |

### /generate_204 与页面图片

VM 初始化时 program 创建图片元素请求 `/generate_204?<令牌>` 并等待 `error` 事件。宿主把 `src` 解析为绝对地址（`//` 补 `https:`，`/` 补页面 origin，相对路径基于页面地址），经账户出口以 Firefox 图片请求头发送。响应为 HTTP 200、`Content-Type` 以 `image/` 开头且正文非空时派发 `load`，其他结果（包括 `generate_204` 的 204）派发 `error`。runtime 在 bootstrap 与 VM 刷新后等待全部图片请求完成再静置。

### snapshot、proof 与发送

#### proof

```text
digest = lowercase_hex(SHA256(binding))
snapshot(callback, [{content: digest}, undefined, undefined, undefined])
proof  = callback 的第一个参数
```

snapshot 输入保持四个槽，首槽对象只有 `content` 键，其余三槽为 `undefined`。proof 必须以 `!` 开头，否则 Worker 进入 failed。`NativeWorker` 解析请求正文数组，把 proof 写入 `payload[field-1]`，其他槽位保持不变。

Build 代理的 binding 为代理请求正文前两个字符串以单个空格连接：

```text
/v1beta/models/<MODEL_ID>:streamGenerateContent {"contents":[...],...}
```

#### 发送与 Cookie 写回

受保护请求由 `WorkerProtectedTransport` 组装：

1. 从 Worker 导出当前 Cookie，按 SAPISID 规则生成 `Authorization`
2. RPC 自身的头（`Content-Type`、权益头 `X-AIStudio-G1-Tier`）覆盖公共协议头；图片路由删除 `x-goog-ext-519733851-bin`
3. 发送前再次检查账户对该模型与通道的冷却
4. 纯 Go runtime 追加 Firefox fetch 头与 runtime Cookie，经账户固定出口 POST，响应体交给流式解码器
5. 响应头到达后，Worker 的 Cookie 原子替换账户 `storage-state.json` 中的 Cookie

## 6. 生命周期、运行数据与失败处理

### VM 生命周期

| 参数 | 值 |
| --- | --- |
| VM 时长 | 12 小时，对应官网 BotGuard 生命周期参数 43,200,000 ms |
| 刷新方式 | 到期计时器在后台重建；取 proof 时发现已到期也会先重建 |
| 刷新超时 | 后台刷新 2 分钟 |
| 静置 | 新 VM 就绪后等待图片请求并静置 3 秒 |

刷新时旧 VM 先生成无绑定 snapshot，runtime 以当前 hash 与该 snapshot 调用 `Waa/Create`，新 VM 就绪后才关闭旧 VM。刷新期间同一账户的 proof 请求等待刷新完成。后台刷新失败只记录 `WAA 纯 Go VM 刷新失败`，下一次取 proof 会再次尝试。关闭 VM 时调用 ready 回调给出的 shutdown 函数，最多等待 1 秒，然后停止事件循环。

### Worker 状态

Worker 状态为 `starting`、`bootstrapping`、`ready`、`busy`、`closing`、`closed` 与 `failed`。取 proof 期间为 `busy`，完成后回到 `ready`；非取消错误进入 `failed`。Worker 以 generation 区分实例，替换或重建时 generation 递增，较晚返回的旧实例错误按旧 generation 处理。

### Ping 与 Worker 就绪

官网 bundle 定义了 `Waa/Ping`，自然页面与服务运行链都不调用它。该 RPC 适合检查 WAA endpoint 与公开 API key 的可达性：

```text
POST https://waa-pa.clients6.google.com/$rpc/google.internal.waa.v1.Waa/Ping
Content-Type: application/json+protobuf
```

```json
["lmnUSbltwc5ULv48iKLX", "<BOTGUARD_PROOF>"]
```

| field | 类型 | 内容 |
| ---: | --- | --- |
| 1 | string | request key |
| 2 | string | 调用者提供的 BotGuard proof |

成功响应为 `[]`。正确 proof、末字符损坏的 proof、省略 field 2、任意 field 1 以及没有账户 Cookie/Authorization 的请求都可返回 HTTP 200；移除 WAA API key 返回 HTTP 403，字段类型错误返回 HTTP 400。因此 Ping 只验证 RPC 与 API consumer identity，不参与 Worker readiness。

| 层级 | 通过条件 | 失败含义 |
| --- | --- | --- |
| VM 初始化 | ready 回调提供 snapshot 函数，bootstrap 图片已完成并静置 | challenge、解释器、宿主或事件循环失败 |
| 本地 proof | snapshot 返回 `!` 开头的字符串 | 当前 Worker 需要重建 |
| 业务接受 | 使用该 proof 的受保护业务 RPC 返回预期语义事件与终态 | 按 HTTP/code 区分账户、模型、额度与 Worker |

bootstrap 模型只负责建立账户 Worker，不是业务模型白名单，一个 Worker 可跨普通生成模型复用。proof 长度或 hash、bootstrap 与业务模型一致、`capability_code_83`、固定等待、Ping、sentinel 请求和 `sD()` 都不能预测下一次业务请求；真正的本地就绪条件是 VM 与 snapshot 成功，业务接受由实际受保护 RPC 的语义事件和终态判定。当前上游没有零生成用量的业务就绪 RPC。

### 失败分类

| 信号 | 处理 |
| --- | --- |
| proof 生成失败：snapshot 异常、提示词未同步、前缀不是 `!`、VM 刷新失败 | Worker 进入 `failed`；同账户重建 Worker 并重放一次 |
| 受保护请求网络错误 | 同上 |
| `GenerateContent`、`GenerateVideo`、Bidi 返回 HTTP 404、Code 5 且消息含 `Ambiguous request for service ''` | 同账户重建 Worker 并重放一次 |
| HTTP 403 或 Code 7 | 保留账户与模型资格，首个上游事件前切换到未尝试的同能力账户；不重建 Worker |
| HTTP 429 | 按分钟或每日限额写入冷却（Build 通道为 `build:<模型>`），同账户另一通道可用时在同账户重试 |
| HTTP 401 | Chrome 导入账户在同一出口续签，重建 WAA runtime 后重放一次；没有续签材料或续签后仍为 401 的账户进入 `auth_required` |
| Worker 启动失败 | 记录 `WAA Worker 启动失败`，请求可切换账户 |
| runtime 租约由其他进程持有 | 账户暂停调度，首次 5 秒后重试，间隔翻倍到 1 分钟 |

Code 7 表示本次上游调用被拒绝。它可以是账户或模型级结果：同一 Worker 可以先返回 Code 7 再返回 200，同一页面上下文可以让一个模型返回 200、另一个模型返回 Code 7。单次 Code 7 不能判定 WAA 宿主失效。

### 解释器版本与日志

每个解释器 hash 在进程内第一次执行时，控制台标准错误输出一条 JSON 日志：

```json
{"level":"INFO","msg":"WAA 解释器版本","hash":"<INTERPRETER_HASH>","url":"https://www.google.com/js/bg/<INTERPRETER_HASH>.js"}
```

同一 runtime 的 VM 刷新得到不同 hash 时输出 `WAA 解释器版本变化`（`previous`、`current`）。解释器 hash 随上游发布变化，新 hash 在 Worker 启动、VM 刷新或 Worker 重建时自动下载执行。

### 形状数据与时区数据

`internal/waa` 的数据文件由真实 Firefox 页面采集生成：

| 文件 | 内容 | 格式 |
| --- | --- | --- |
| `firefox152.json` | Firefox 形状表 | 单行 JSON，UTF-8，LF |
| `timezones.json.gz` | Firefox Intl 长时区名 | gzip 压缩的 JSON，键排序，gzip 时间戳为 0 |
| `dom.js` | 宿主生成脚本 | JavaScript 源码 |

#### 采集环境

在 Camoufox 中用已登录账户打开 AI Studio 聊天页面（Firefox 152，Windows 指纹，未检测到触控设备），通过 WebDriver BiDi 或远程调试在页面上下文中执行采集脚本。`Touch`、`TouchEvent`、`TouchList` 是否暴露取决于宿主机是否检测到触控设备，形状表按未检测到触控设备的环境采集。

#### 形状表采集内容

| 部分 | 采集方法 |
| --- | --- |
| `frame.freshKeys` | 新建隐藏 iframe 并挂到 `body`，立即读取 `Reflect.ownKeys(iframe.contentWindow)` |
| `frame`、`top` | 对页面的 BotGuard iframe（不存在时新建 `about:blank` iframe）与顶层 window 读取全部自有属性描述符与 Window 原型链各层（到 `Object.prototype` 前） |
| `interfaces` | 遍历 iframe 全局名，取原生且带 prototype 对象的函数：名称、`length`、父构造器、prototype 父级、prototype 可写性、静态与 prototype 自有成员；按父接口优先排序 |
| `call`、`construct` | 在新 iframe 中对每个接口构造器执行一次直接调用与一次无参 `new`，记录结果或错误名与消息；会发起网络、媒体或 Worker 活动的构造器只记录直接调用 |
| `promises` | 在新 iframe 中以空原型对象为 `this` 调用每个 prototype 方法与 getter，记录返回 thenable 的成员 |
| `singletons`、`topSingletons`、`defaults`、`own` | 对 `document`、`navigator`、`screen`、`location`、`history`、`performance`、存储、`visualViewport` 等单例及常见实例（元素、事件、集合、样式、Range、XHR、URL、Blob 等）沿原型链读取全部访问器值，并记录自有属性 |
| `tagMap` | 对现行与已废弃的 HTML 标签调用 `createElement`，记录原型构造器名 |
| `mediaQueries` | 在顶层与 iframe 分别对常见媒体特性的两种空格写法执行 `matchMedia` |
| `namespaces` | `CSS`、`console`、`WebAssembly`、`Intl` 的自有成员 |
| `builtins` | 在新 iframe 中对 ECMAScript 内建构造器及其 prototype、`Intl`、`WebAssembly`、`Temporal` 的成员与内在对象，按 `Reflect.ownKeys` 记录成员描述 |

#### 整理与导出

整理步骤把原始采集转换为上述结构：接口按父接口优先排序并并入 `call`、`construct`；实例取值按接口写入 `defaults` 与 `own`，单例写入 `singletons` 与 `topSingletons`，顶层单例中超过 4096 字符的字符串截断。导出到正式仓库前去除账户与页面状态：

- 所有 `defaults` 中的 `cookie` 取值，以及 `singletons` 与 `topSingletons` 中 document 的 `cookie` 置空
- `defaults.Storage` 只保留 `length` 为 0，`own.Storage` 为空
- `localStorage`、`sessionStorage` 单例的键值与自有条目清除，`length` 为 0
- Window 的 `name`、`status` 置空
- `html`、`head`、`body` 的 `innerText`、`outerText`、`textContent`、`innerHTML`、`outerHTML` 删除
- `frame.freshKeys` 只保留出现在 `frame.global` 中的名称
- `top.global` 以及 `top`、`frame` 的 `windowValues` 删除采集工具与 bootstrap 注入的全局名以及 `botguard`
- 导出脚本断言结果中不含邮箱、账户 Cookie 名值对（`SID`、`HSID`、`SSID`、`APISID`、`SAPISID`、`SIDCC`、`NID`、`OSID`、`AEC`、`__Secure-*`、`__Host-*`）、`PSID`、注入名与官网 localStorage 键名

#### 时区显示名表

在同一页面对 21 个区域设置（`en-US`、`en-GB`、`zh-CN`、`zh-TW`、`zh-HK`、`ja-JP`、`ko-KR`、`de-DE`、`fr-FR`、`es-ES`、`it-IT`、`pt-BR`、`ru-RU`、`vi-VN`、`th-TH`、`id-ID`、`tr-TR`、`pl-PL`、`nl-NL`、`ar-SA`、`hi-IN`）与 `Intl.supportedValuesOf('timeZone')` 加 `UTC` 的全部时区执行：

```javascript
new Intl.DateTimeFormat(locale, {timeZone, timeZoneName: 'long'})
  .formatToParts(date)
  .find(part => part.type === 'timeZoneName').value
```

`date` 分别取 2026-01-15 12:00 UTC 与 2026-07-15 12:00 UTC；两个名称相同时保存 `[名称]`，不同时保存 `[1 月名称, 7 月名称]`。文件结构为 `{区域设置: {IANA 时区: [名称...]}}`，当前为 21 个区域设置 × 445 个时区。

#### 数据更新验收

- `go build ./...` 与 `go vet ./...`
- 纯 Go 与 Camoufox 在同一账户、同一模型下分别完成冷启动首请求、连续请求、模型切换、并发请求与服务重启
- 按“上游变化定位与引擎对照”的方法对若干 fresh challenge 做页面与纯 Go 双侧对照

## 7. 上游变化定位与引擎对照

### 信号

| 现象 | 优先检查 |
| --- | --- |
| Worker 启动失败，报 `WIu0Nc`、登录跳转或 HTTP 状态 | 首页结构与登录态 |
| `Waa/Create` 解析失败 | 响应形状：外层索引 1 的混淆 challenge 或索引 0 的明文 challenge，混淆常量是否仍为 97，字段数与索引 |
| 解释器摘要不一致 | hash 编码规则与下载地址 |
| 初始化失败或 ready 未回调 | 全局名与初始化方法名、参数顺序、client experiments 结构 |
| 新解释器 hash 后纯 Go proof 稳定 403，Camoufox 仍为 200 | 宿主差异，按下述方法对照 |
| 两种后端同时 403 | 账户或模型状态，先用官网页面确认 |

### 同 challenge 双侧对照

1. 在 Camoufox 页面中用同一账户调用 `Waa/Create`，保存原始响应与解释器
2. 页面侧用已加载的解释器以该 challenge 新建 VM，按“解释器、VM 与 Realm”的参数初始化，派发 `input`、`change` 后取 proof
3. Go 侧用 `waa.ParseChallenge` 解析同一响应，以相同解释器与账户 Profile 调用 `waa.NewRuntime`、`FillPrompt`、`Settle` 与 `Proof`
4. 两份 proof 用同一账户、同一模型与同一提示词分别发送 `GenerateContent`

页面 200、Go 403 说明差异在纯 Go 宿主或 goja；两侧同为 403 说明是账户、模型或 challenge 状态。一个 challenge 稳定复现差异后，用下面三种方法逐层缩小范围。

### 固定 Math.random 后比较 proof

program 的随机选择使两次 proof 无法直接比较。两侧都把每个 Realm 的 `Math.random` 替换为同一确定性序列（页面侧在顶层与每个新建 iframe 的 window 上替换，Go 侧用 goja 的 `SetRandSource` 替换 `newFirefoxRandSource`），并固定提示词。此时两侧 program 走相同分支，proof 从开头逐字节比较，第一处分歧说明 program 写入的哪一段取值不同。proof 中的执行耗时计数随时钟变化，不影响验收。

### charCodeAt 字符流

program 通过 `String.prototype.charCodeAt` 逐字符读取要编码的字符串。两侧在每个 Realm 替换该方法，每当 `this` 字符串与上一次不同时记录一次，得到两条字符串序列。在固定随机序列的前提下对齐两条序列，第一处不同的字符串就是宿主取值差异，例如 iframe 全局名枚举取到的接口名、内建对象成员名或 Date 字符串的时区注释。

### 异常序列对照

program 大量依赖异常路径探测宿主。页面侧通过 Firefox 远程调试协议对线程开启 pause on exceptions（包括已捕获异常），记录每个异常的类型、消息与抛出位置后继续执行；Go 侧在 goja 分叉的异常抛出路径临时记录同样的信息，定位完成后移除。两侧序列应逐条一致，第一处不同通常是宿主缺少成员、品牌检查、错误消息文本或栈格式差异。

### 常见差异来源

- 全局对象自有键顺序与惰性解析时机
- 内建对象的成员集合与键顺序
- 错误类型、消息文本、`Error.stack` 格式、函数显示名与列号
- Date 字符串的时区注释
- 多 Realm 的微任务顺序与计时器语义
- 没有派发交互事件或图片请求未完成就取 proof

修正落在形状表、`dom.js`、`internal/waa` 的 Go 宿主或 goja 分叉中产生差异的一层。

### goja 分叉

`internal/waa/goja` 是 `github.com/dop251/goja` 在 `v0.0.0-20260826204918-8f1c0696a37b` 上的 MIT 分叉，模块路径为 `github.com/Mag1cFall/AIStudio2API/internal/waa/goja`。分叉只包含上游的非测试 Go 源码；上游源码与注释保持原样，分叉自有代码使用单行中文 Go Doc 注释。`LICENSE`、`ftoa/LICENSE_LUCENE` 与 `ftoa/internal/fast/LICENSE_V8` 随分叉源码保留。

与上游的行为差异：

| 范围 | 分叉行为 | 文件 |
| --- | --- | --- |
| 多 Realm | `NewAgent`、`NewWithAgent`、`NewRealm` 让多个 Runtime 共享堆、调用栈与微任务队列；跨 Realm 调用切换当前 Runtime；Promise 可在同一 agent 的 Realm 间解析 | `runtime.go`、`vm.go`、`func.go`、`proxy.go`、`object_template.go`、`builtin_promise.go` |
| 微任务 | `QueueMicrotask` 按 agent 排队，最外层脚本返回时统一执行 | `runtime.go`、`builtin_promise.go` |
| eval | `SetEvalTransformer` 在原生 eval 前转换参数；eval 源码名为 `<调用文件> line <行> > eval` | `runtime.go` |
| 错误栈 | `Error.prototype.stack` 为访问器，格式为 `函数名@文件:行:列`；来源名以 `\x00` 开头的脚本帧不出现；Error 实例带 `fileName`、`lineNumber`、`columnNumber` | `builtin_error.go` |
| 函数名推断 | 按 SpiderMonkey NameFunctions 规则推断匿名函数的栈显示名，如 `a.b/<` | `names.go`、`compiler.go`、`compiler_expr.go` |
| 调用位置 | 调用帧列号取被调属性名、字符串键或 eval 标识符的位置 | `compiler_expr.go`、`compiler_stmt.go` |
| 错误消息 | `x is not a function`、`can't access property "p", x is undefined`、`can't convert x to object`、`invalid array length`、`radix must be an integer at least 2 and no greater than 36`、`Function.prototype.toString called on incompatible object`；被调表达式按 SpiderMonkey 规则反编译 | `vm.go`、`decompile.go`、`value.go`、`object.go`、`runtime.go`、`builtin_array.go`、`builtin_number.go`、`builtin_function.go` |
| 递归上限 | 栈溢出抛出 `InternalError: too much recursion`，全局提供 `InternalError` | `vm.go`、`builtin_global.go`、`builtin_error.go`、`runtime.go` |
| SyntaxError | 消息为 `unexpected token: identifier`、`unexpected token: numeric literal`、`unexpected token: string literal` 与未终止字符串的 SpiderMonkey 文案；位置写入 `fileName`、`lineNumber`、`columnNumber` | `parser/error.go`、`parser/lexer.go`、`compiler.go`、`runtime.go` |
| 全局键顺序 | `SetGlobalObserver` 接收全局属性的解析、定义、删除与枚举；引擎内部使用内建原型时通知解析；宿主脚本执行期间不通知 | `global_order.go`、`string.go` 与各 `builtin_*.go` |
| 自有键顺序 | `Object.OrderOwnKeys` 按给定顺序重排自有字符串键 | `order.go` |
| Symbol | 提供 `Symbol.asyncIterator`、`Symbol.dispose`、`Symbol.asyncDispose`，Symbol 构造器键顺序与 SpiderMonkey 一致 | `builtin_symbol.go` |
| Date | `SetTimeLocation` 设置本地时区，`SetTimeZoneName` 设置 `toString` 与 `toTimeString` 的时区注释；解析与格式化使用 Runtime 时区 | `date.go`、`builtin_date.go`、`runtime.go` |
| 数值转换 | 负数的非十进制 `toString(radix)` 输出 `-` 前缀 | `ftoa/ftobasestr.go` |

分叉独有的文件为 `doc.go`、`decompile.go`、`global_order.go`、`names.go` 与 `order.go`。已知差异：

- `Intl` 只有命名空间与成员形状，没有格式化实现
- SpiderMonkey 在编译期解析正则字面量对应的全局 `RegExp`，分叉在首次使用正则原型时解析
- `RegExp.prototype` 的 Symbol 键顺序与 Firefox 不同

升级上游版本时，用 `go mod download github.com/dop251/goja@<版本>` 取得上游源码，逐文件比较当前分叉与原上游版本的差异，把上表的行为改动合并到新版本，替换模块路径并执行 gofmt，再按“数据更新验收”检查。

## 8. 实现位置

| 路径 | 职责 |
| --- | --- |
| `internal/config/config.go` | `WAA_BACKEND` 解析与校验 |
| `internal/app/waa_backend.go` | 按后端准备 Camoufox 或按需登录驱动，选择 Worker 实现 |
| `internal/app/runtime.go` | Worker 热池、启动日志、runtime 租约、失败重建与换号 |
| `internal/app/auth_retry.go` | 401 续签、重建 runtime 与重放 |
| `internal/aistudio/runtime_go.go` | 纯 Go runtime：首页、`GetLoggingContext`、`Waa/Create`、解释器缓存、VM 刷新、proof、受保护请求与 Cookie |
| `internal/aistudio/runtime_native.go` | `NativeWorker`：proof 写入与状态 |
| `internal/aistudio/service.go` | `WorkerProtectedTransport`、binding、失败判定 |
| `internal/aistudio/build.go` | Build binding 与 proof field |
| `internal/aistudio/transport_browser.go` | Firefox 152 TLS 与请求头顺序 |
| `internal/aistudio/transport_http.go` | visit ID、页面 API key 提取与默认 UA |
| `internal/camoufoxnative/` | Camoufox 后端、账户指纹与隔离登录 |
| `internal/waa/challenge.go` | challenge 解码与解释器摘要 |
| `internal/waa/runtime.go` | VM 初始化、client experiments、提示词写入、snapshot、静置与关闭 |
| `internal/waa/host.go` | Realm 宿主安装、Profile 与 `Function.prototype.toString`、计时器 |
| `internal/waa/realm.go` | iframe Realm、`Math.random`、`atob`/`btoa`、`queueMicrotask`、eval 转换与调用栈上限 |
| `internal/waa/loop.go` | VM 事件循环 |
| `internal/waa/global_order.go` | 惰性全局名称与枚举顺序 |
| `internal/waa/timezone.go` | 时区与显示名选择 |
| `internal/waa/dom.js` | 按形状表生成 Window、接口、实例与宿主对象 |
| `internal/waa/firefox152.json` | Firefox 形状表 |
| `internal/waa/timezones.json.gz` | 时区显示名表 |
| `internal/waa/goja/` | goja 分叉 |
