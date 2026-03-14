# 逆向工程技术文档

本文档面向希望理解项目核心逆向实现原理、参与代码贡献或进行深度定制的开发者。

## 📋 概述

本项目通过以下技术手段实现对 AI Studio 网页版的 API 化：

1. **MITM (中间人) 代理**：拦截并解析 HTTPS 流量
2. **动态证书生成**：为目标域名签发可信证书
3. **私有协议解析**：解码 AI Studio 特有的嵌套数组响应格式
4. **浏览器自动化**：通过 Playwright + Camoufox 完成认证和页面交互

---

## 🔐 证书与 TLS 拦截系统

### 核心文件

- `src/proxy/connection.py` - 证书存储与生成
- `src/proxy/server.py` - MITM 代理服务器

### 实现原理

通过网络抓包分析发现，AI Studio 的流式响应通过标准 HTTPS 传输。为了拦截并解析这些响应，项目实现了自签名 CA 证书系统。

#### 1. 根 CA 证书生成 (`CertStore._create_authority`)

```python
name = x509.Name([
    x509.NameAttribute(NameOID.COUNTRY_NAME, self._profile['country']),
    x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, self._profile['state']),
    x509.NameAttribute(NameOID.LOCALITY_NAME, self._profile['city']),
    x509.NameAttribute(NameOID.ORGANIZATION_NAME, self._profile['org']),
    x509.NameAttribute(NameOID.COMMON_NAME, self._profile['cn'])
])
```

生成的 `ca.crt` 需要被系统/浏览器信任，才能使后续的域名证书被接受。

#### 2. 动态域名证书生成 (`CertStore._create_domain_cert`)

当代理拦截到对 `*.google.com` 的 CONNECT 请求时，动态生成该域名的证书：

```python
cert = (
    x509.CertificateBuilder()
    .issuer_name(self.authority_cert.subject)
    .add_extension(x509.SubjectAlternativeName([x509.DNSName(domain)]), critical=False)
    .sign(self.authority_key, hashes.SHA256())
)
```

#### 3. TLS 升级 (`MitmProxy._process_tunnel`)

在已建立的 TCP 连接上执行 TLS 握手，使代理成为"服务端"：

```python
new_transport = await loop.start_tls(
    transport=transport,
    sslcontext=ctx,
    server_side=True
)
```

---

## 📡 请求路径拦截

### 核心文件

- `src/proxy/server.py` - 请求路由判断
- `src/proxy/handler.py` - 响应内容处理

### 拦截逻辑 (`MitmProxy._relay_with_inspection`)

通过分析网络请求，确定了需要拦截的关键路径：

```python
if 'jserror' in path:
    if 'quota' in path_str or 'limit' in path_str or 'exceeded' in path_str:
        self.message_queue.put({'error': 'rate_limit', ...})
elif 'GenerateContent' in path:
    inspect_response = True
```

- **`GenerateContent`**：模型生成响应的核心 API
- **`jserror`**：前端错误上报，可用于检测 Rate Limit

---

## 🔍 响应协议解析

### 核心文件

- `src/proxy/handler.py` - 响应解码与内容提取

### 协议结构发现

通过对浏览器 Network 面板的抓包分析，发现 AI Studio 使用以下特征：

1. **HTTP Chunked Transfer Encoding**
2. **gzip/deflate 压缩**
3. **嵌套数组 JSON 格式**（非标准 Protobuf）

### 响应解码流程 (`ResponseHandler.handle_response`)

```python
async def handle_response(self, data, host, path, headers):
    decoded, completed = self._unchunk(bytes(data))
    decoded = self._inflate(decoded)
    result = self._extract_content(decoded)
    result['done'] = completed
    return result
```

#### 1. Chunked 解码 (`_unchunk`)

HTTP Chunked 编码格式：`<hex_size>\r\n<data>\r\n`

```python
chunk_size = int(hex_size, 16)
result.extend(body[start:end])
```

#### 2. gzip 解压 (`_inflate`)

```python
decompressor = zlib.decompressobj(wbits=zlib.MAX_WBITS | 32)
return decompressor.decompress(compressed)
```

#### 3. 内容提取 (`_extract_content`)

通过抓包分析，确定了 AI Studio 响应的数据包装格式：

```python
pattern = b'\\[\\[\\[null,.*?]],"model"]'
```

该正则匹配形如 `[[[null, "content"]], "model"]` 的结构。

**内容类型识别**（基于 payload 长度）：

| payload 长度 | 含义 | 提取方式 |
|-------------|-----|---------|
| `== 2` | 普通响应文本 | `payload[1]` |
| `> 2` | 推理/思考内容 | `payload[1]` |
| `== 11` 且 `payload[10]` 为 list | Tool Call | `payload[10]` |

```python
if len(payload) == 2:
    output['body'] += payload[1]
elif len(payload) == 11 and payload[1] is None and isinstance(payload[10], list):
    tool_data = payload[10]
    fn_name = tool_data[0]
    fn_params = self._parse_tool_args(tool_data[1])
elif len(payload) > 2:
    output['reason'] += payload[1]
```

---

## 🛠️ Tool Call 参数解析

### 核心文件

- `src/proxy/handler.py` - `_parse_tool_args` 函数

### 结构发现

通过分析包含 Function Call 的响应，发现参数使用递归嵌套数组表示：

```python
def _parse_tool_args(self, args):
    extractors = {
        1: lambda v: None,
        2: lambda v: v[1],
        3: lambda v: v[2],
        4: lambda v: v[3] == 1,
        5: lambda v: self._parse_tool_args(v[4]),
    }
    
    for param in params:
        name, value = param[0], param[1]
        if isinstance(value, list):
            extractor = extractors.get(len(value))
            if extractor:
                result[name] = extractor(value)
```

value 数组长度与数据类型的映射关系：

| 长度 | 类型 | 取值位置 |
|-----|-----|---------|
| 1 | null | - |
| 2 | 字符串/数字 | `value[1]` |
| 3 | 字符串/数字 | `value[2]` |
| 4 | 布尔值 | `value[3] == 1` |
| 5 | 嵌套对象 | 递归解析 `value[4]` |

---

## 🌐 API 端点与请求头

### 通过抓包分析得到的关键信息

#### 1. RPC 服务端点

```
Base URL: https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/
```

主要方法：
- `/GenerateContent` - 生成模型回复
- `/CountTokens` - 计算 Token 数量
- `/CreatePrompt` - 保存对话
- `/ListPrompts` - 获取对话列表

#### 2. 必需请求头

| Header | 值 | 说明 |
|--------|---|-----|
| `content-type` | `application/json+protobuf` | Body 为 JSON 但结构遵循 Protobuf 定义 |
| `authorization` | `SAPISIDHASH ...` | 动态认证 Hash |
| `x-goog-api-key` | `AIzaSy...` | 静态 API Key |
| `x-goog-authuser` | `0` | 账号索引 |

#### 3. 请求 Payload 结构

对话历史的标准化格式：

```json
[
  "models/gemini-2.5-pro",
  [
    [
      [[null, "用户输入"], "user"],
      [[null, "模型回复"], "model"]
    ]
  ]
]
```

---

## � 完整 Payload 结构示例

### 核心发现来源

通过浏览器 Network 面板抓包 `CreatePrompt` 请求，获得了完整的数据存储结构。

### 1. 流式响应块结构

以下是实际拦截到的 `GenerateContent` 流式响应数据：

```json
[
    [
        [
            [
                [
                    [
                        [
                            null,
                            "**Defining Numerical Sequences**\n\nI've determined the user input is \"2\"...",
                            null, null, null, null, null, null, null, null, null, null,
                            1
                        ]
                    ],
                    "model"
                ]
            ]
        ],
        null,
        [
            491,
            null,
            558,
            null,
            [[1, 491]],
            null, null, null, null,
            67
        ],
        null, null, null, null,
        "v1_ChdvSWc1YWZUa0xZaktfdU1QNE1ERTZBRRIX..."
    ]
]
```

**字段解析**：

| 层级路径 | 值 | 含义 |
|---------|---|-----|
| `[0][0][0][0][0][0][1]` | 文本内容 | Thinking/Response 文本 |
| `[0][0][0][0][0][0][12]` | `1` | 标记为思考过程 |
| `[0][0][0][0][1]` | `"model"` | 角色标识 |
| `[0][1]` | `null` | 保留字段 |
| `[0][2][0]` | `491` | 输入 Token 数 |
| `[0][2][2]` | `558` | 累计输出 Token 数 |
| `[0][7]` | `"v1_..."` | 请求追踪 ID |

### 2. 多轮对话存储结构

以下是 `CreatePrompt` 请求体中的对话历史格式：

```json
[
    "prompts/13qWuAz0RxugAkEuoKS2Pd9Bwas7U997E",
    null,
    null,
    [
        1, null,
        "models/gemini-2.5-pro",
        null,
        0.95,
        64,
        65536,
        [[null,null,7,5], [null,null,8,5], [null,null,9,5], [null,null,10,5]],
        null, 0, null, null, null, null, 0, null, null, 0, 0,
        null, null, null, null, null,
        8192,
        null, null,
        "1K"
    ],
    ["Title", null, ["Author", 1, "avatar_url"], ...],
    null, null, null, null, null, null, null,
    [],
    [
        [User_Turn_1],
        [Model_Thinking_1],
        [Model_Response_1],
        [User_Turn_2],
        [Model_Thinking_2],
        [Model_Response_2]
    ]
]
```

**模型参数字段（索引 3）**：

| 索引 | 值 | 含义 |
|-----|---|-----|
| `[2]` | `"models/gemini-2.5-pro"` | 模型名称 |
| `[4]` | `0.95` | Temperature |
| `[5]` | `64` | TopK |
| `[6]` | `65536` | MaxOutputTokens |
| `[7]` | `[[null,null,7,5],...]` | SafetySettings |
| `[25]` | `8192` | Thinking Budget |

### 3. 用户消息结构

```json
[
    "1",
    null, null, null, null, null, null, null,
    "user",
    null, null, null, null, null, null, null, null, null,
    2
]
```

| 索引 | 值 | 含义 |
|-----|---|-----|
| `[0]` | 文本 | 用户输入内容 |
| `[8]` | `"user"` | 角色标识 |
| `[18]` | `2` | 状态码/版本 |

### 4. 模型思考消息结构

```json
[
    "**Evaluating the Input**\n\nI've just finished analyzing...",
    null, null, null, null, null, null, null,
    "model",
    null, null, null, null, null, null, null, null, null,
    962,
    1,
    null, null, null, null, null,
    -1,
    null, null, null,
    [
        [null, "**Evaluating the Input**\n\n...", null,...,null, 1],
        [null, "**Assessing Possible Meanings**\n\n...", null,...,null, 1],
        [null, "**Interpreting the Number**\n\n...", null,...,null, 1]
    ]
]
```

| 索引 | 值 | 含义 |
|-----|---|-----|
| `[0]` | 完整文本 | 思考过程合并后的文本 |
| `[8]` | `"model"` | 角色标识 |
| `[18]` | `962` | 思考 Token 消耗 |
| `[19]` | `1` | 标记为思考类型 |
| `[25]` | `-1` 或 `8192` | Thinking Budget（-1 表示无限制） |
| `[29]` | 数组 | 思考过程的流式分块 |

**思考分块内部结构** (`[29][n]`)：

```json
[null, "**Chunk Title**\n\nContent...", null,...,null, 1]
```

- 索引 `1`：思考文本片段
- 索引 `12`：`1` 表示这是思考内容

### 5. 模型回复消息结构

```json
[
    "The number one. In mathematics, it is:\n\n*   The first positive integer...",
    null, null, null, null, null, null, null,
    "model",
    null, null, null, null, null, null, null,
    1,
    null,
    80,
    null, null, null, null, null, null, null, null, null, null,
    [
        [null, "The number one. In mathematics, it is:\n\n*   The first positive integer.\n*   The multiplicative identity ("],
        [null, "any number multiplied by 1 is itself).\n*   Neither a prime nor a composite number.\n\nIt can also represent"],
        [null, " unity, a beginning, or the first in a series.\n\nDid you have a question about it, or would you like to continue"],
        [null, " counting?"]
    ]
]
```

| 索引 | 值 | 含义 |
|-----|---|-----|
| `[0]` | 完整文本 | 最终回复内容 |
| `[8]` | `"model"` | 角色标识 |
| `[16]` | `1` | 回复完成标记 |
| `[18]` | `80` | 回复 Token 数 |
| `[29]` | 数组 | 流式响应分块 |

**流式分块结构** (`[29][n]`)：

```json
[null, "文本片段"]
```

与思考分块不同，回复分块没有索引 `12` 的标记。

---

## 🔄 代码实现映射

### 思考过程识别

```python
def is_thinking_chunk(payload):
    if len(payload) > 12 and payload[12] == 1:
        return True
    if len(payload) > 2:
        return True
    return False
```

### 多轮对话构建

```python
def build_history_payload(messages):
    history = []
    for msg in messages:
        if msg['role'] == 'user':
            history.append([msg['content'], None,None,None,None,None,None,None, 'user', None,None,None,None,None,None,None,None,None, 2])
        elif msg['role'] == 'assistant':
            if msg.get('thinking'):
                history.append([msg['thinking'], None,None,None,None,None,None,None, 'model', ...])
            history.append([msg['content'], None,None,None,None,None,None,None, 'model', ...])
    return history
```

### 流式响应解析

```python
def parse_stream_chunk(chunk):
    text = chunk[0][0][0][0][0][0][1]
    is_thinking = len(chunk[0][0][0][0][0][0]) > 12 and chunk[0][0][0][0][0][0][12] == 1
    return {'text': text, 'type': 'thinking' if is_thinking else 'content'}
```

---

## �🔗 上游代理支持

### 核心文件

- `src/proxy/connection.py` - `UpstreamConnector` 类

### 实现方式

支持通过 SOCKS4/SOCKS5/HTTP 代理连接上游服务器：

```python
upstream = Proxy.from_url(self.upstream_url)
sock = await upstream.connect(dest_host=target_host, dest_port=target_port)

ctx = ssl_module.SSLContext(ssl_module.PROTOCOL_TLS_CLIENT)
ctx.check_hostname = False
ctx.verify_mode = ssl_module.CERT_NONE
```

---

## 📚 参考资料

- [cryptography 文档](https://cryptography.io/) - 证书生成
- [python-socks 文档](https://github.com/romis2012/python-socks) - 代理连接
- [Playwright 文档](https://playwright.dev/python/) - 浏览器自动化
