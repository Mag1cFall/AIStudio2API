# 高级配置指南

本文档介绍项目的高级配置选项和功能。

## 代理配置管理

### 代理配置优先级

项目采用统一的代理配置管理系统，按以下优先级顺序确定代理设置：

1. **`--internal-camoufox-proxy` 命令行参数** (最高优先级)
   - 明确指定代理：`--internal-camoufox-proxy 'http://127.0.0.1:7890'`
   - 明确禁用代理：`--internal-camoufox-proxy ''`
2. **`UNIFIED_PROXY_CONFIG` 环境变量** (推荐，.env 文件配置)
3. **`HTTP_PROXY` 环境变量**
4. **`HTTPS_PROXY` 环境变量**
5. **系统代理设置** (Linux 下的 gsettings，最低优先级)

**推荐配置方式**:
```env
# .env 文件中统一配置代理
UNIFIED_PROXY_CONFIG=http://127.0.0.1:7890
# 或禁用代理
UNIFIED_PROXY_CONFIG=
```

### 统一代理配置

此代理配置会同时应用于 Camoufox 浏览器和流式代理服务的上游连接，确保整个系统的代理行为一致。

## 响应获取模式配置

### 模式1: 优先使用集成的流式代理 (默认推荐)

**推荐使用 .env 配置方式**:
```env
# .env 文件配置
DEFAULT_FASTAPI_PORT=2048
STREAM_PORT=3120
UNIFIED_PROXY_CONFIG=
```

```bash
# 简化启动命令 (推荐)
python launch_camoufox.py --headless

# 传统命令行方式 (仍然支持)
python launch_camoufox.py --headless --server-port 2048 --stream-port 3120 --helper '' --internal-camoufox-proxy ''
```

# 启用统一代理配置（同时应用于浏览器和流式代理）
python launch_camoufox.py --headless --server-port 2048 --stream-port 3120 --helper '' --internal-camoufox-proxy 'http://127.0.0.1:7890'
```

在此模式下，主服务器会优先尝试通过端口 `3120` (或指定的 `--stream-port`) 上的集成流式代理获取响应。如果失败，则回退到 Playwright 页面交互。

### 模式2: 优先使用外部 Helper 服务 (禁用集成流式代理)

```bash
# 基本外部Helper模式，明确禁用代理
python launch_camoufox.py --headless --server-port 2048 --stream-port 0 --helper 'http://your-helper-service.com/api/getStreamResponse' --internal-camoufox-proxy ''

# 外部Helper模式 + 统一代理配置
python launch_camoufox.py --headless --server-port 2048 --stream-port 0 --helper 'http://your-helper-service.com/api/getStreamResponse' --internal-camoufox-proxy 'http://127.0.0.1:7890'
```

在此模式下，主服务器会优先尝试通过 `--helper` 指定的端点获取响应 (需要有效的 `auth_profiles/active/*.json` 以提取 `SAPISID`)。如果失败，则回退到 Playwright 页面交互。

### 模式3: 仅使用 Playwright 页面交互 (禁用所有流式代理和 Helper)

```bash
# 纯Playwright模式，明确禁用代理
python launch_camoufox.py --headless --server-port 2048 --stream-port 0 --helper '' --internal-camoufox-proxy ''

# Playwright模式 + 统一代理配置
python launch_camoufox.py --headless --server-port 2048 --stream-port 0 --helper '' --internal-camoufox-proxy 'http://127.0.0.1:7890'
```

在此模式下，主服务器将仅通过 Playwright 与 AI Studio 页面交互 (模拟点击"编辑"或"复制"按钮) 来获取响应。这是传统的后备方法。

## 虚拟显示模式 (Linux)

### 关于 `--virtual-display`

- **为什么使用**: 与标准的无头模式相比，虚拟显示模式通过创建一个完整的虚拟 X 服务器环境 (Xvfb) 来运行浏览器。这可以模拟一个更真实的桌面环境，从而可能进一步降低被网站检测为自动化脚本或机器人的风险
- **什么时候使用**: 当您在 Linux 环境下运行，并且希望以无头模式操作
- **如何使用**:
  1. 确保您的 Linux 系统已安装 `xvfb`
  2. 在运行时添加 `--virtual-display` 标志：
     ```bash
     python launch_camoufox.py --virtual-display --server-port 2048 --stream-port 3120 --internal-camoufox-proxy ''
     ```

## 流式代理服务配置

### 自签名证书管理

集成的流式代理服务会在 `certs` 文件夹内生成自签名的根证书。

#### 证书删除与重新生成

- 可以删除 `certs` 目录下的根证书 (`ca.crt`, `ca.key`)，代码会在下次启动时重新生成
- **重要**: 删除根证书时，**强烈建议同时删除 `certs` 目录下的所有其他文件**，避免信任链错误

#### 手动生成证书

如果需要重新生成证书，可以使用以下命令：

```bash
openssl genrsa -out certs/ca.key 2048
openssl req -new -x509 -days 3650 -key certs/ca.key -out certs/ca.crt -subj "/C=CN/ST=Shanghai/L=Shanghai/O=AiStudioProxyHelper/OU=CA/CN=AiStudioProxyHelper CA/emailAddress=ca@example.com"
openssl rsa -in certs/ca.key -out certs/ca.key
```

### 工作原理

流式代理服务的特性：

- 创建一个 HTTP 代理服务器（默认端口：3120）
- 拦截针对 Google 域名的 HTTPS 请求
- 使用自签名 CA 证书动态自动生成服务器证书
- 将 AIStudio 响应解析为 OpenAI 兼容格式

## 模型排除配置

### excluded_models.txt

项目根目录下的 `excluded_models.txt` 文件可用于从 `/v1/models` 端点返回的列表中排除特定的模型 ID。

每行一个模型ID，例如：
```
gemini-1.0-pro
gemini-1.0-pro-vision
deprecated-model-id
```

## 脚本注入高级配置 🆕

### 概述

脚本注入功能允许您动态挂载油猴脚本来增强 AI Studio 的模型列表。该功能使用 Playwright 原生网络拦截技术，确保 100% 可靠性。

### 工作原理

1. **双重拦截机制**：
   - **Playwright 路由拦截**：在网络层面直接拦截和修改模型列表响应
   - **JavaScript 脚本注入**：作为备用方案，确保万无一失

2. **自动模型解析**：
   - 从油猴脚本中自动解析 `MODELS_TO_INJECT` 数组
   - 前端和后端使用相同的模型数据源
   - 无需手动维护模型配置文件

### 高级配置选项

#### 自定义脚本路径

```env
# 使用自定义脚本文件
USERSCRIPT_PATH=custom_scripts/my_enhanced_script.js
```

#### 自定义脚本配置

```env
# 使用自定义脚本文件（模型数据直接从脚本解析）
USERSCRIPT_PATH=configs/production_script.js
```

#### 调试模式

```env
# 启用详细的脚本注入日志
DEBUG_LOGS_ENABLED=true
ENABLE_SCRIPT_INJECTION=true
```

### 自定义脚本开发

#### 脚本格式要求

您的自定义脚本必须包含 `MODELS_TO_INJECT` 数组：

```javascript
const MODELS_TO_INJECT = [
    {
        name: 'models/your-custom-model',
        displayName: '🚀 Your Custom Model',
        description: 'Custom model description'
    },
    // 更多模型...
];
```

#### 脚本模型数组格式

```javascript
const MODELS_TO_INJECT = [
    {
        name: 'models/custom-model-1',
        displayName: `🎯 Custom Model 1 (Script ${SCRIPT_VERSION})`,
        description: `First custom model injected by script ${SCRIPT_VERSION}`
    },
    {
        name: 'models/custom-model-2',
        displayName: `⚡ Custom Model 2 (Script ${SCRIPT_VERSION})`,
        description: `Second custom model injected by script ${SCRIPT_VERSION}`
    }
];
```
```

### 网络拦截技术细节

#### Playwright 路由拦截

```javascript
// 系统会自动设置类似以下的路由拦截
await context.route("**/*", async (route) => {
    const request = route.request();
    if (request.url().includes('alkalimakersuite') &&
        request.url().includes('ListModels')) {
        // 拦截并修改模型列表响应
        const response = await route.fetch();
        const modifiedBody = await modifyModelListResponse(response);
        await route.fulfill({ response, body: modifiedBody });
    } else {
        await route.continue_();
    }
});
```

#### 响应修改流程

1. **请求识别**：检测包含 `alkalimakersuite` 和 `ListModels` 的请求
2. **响应获取**：获取原始模型列表响应
3. **数据解析**：解析 JSON 响应并处理反劫持前缀
4. **模型注入**：将自定义模型注入到响应中
5. **响应返回**：返回修改后的响应给浏览器

### 故障排除

#### 脚本注入失败

1. **检查脚本文件**：
   ```bash
   # 验证脚本文件存在且可读
   ls -la browser_utils/more_modles.js
   cat browser_utils/more_modles.js | head -20
   ```

2. **检查日志输出**：
   ```bash
   # 查看脚本注入相关日志
   python launch_camoufox.py --debug | grep -i "script\|inject"
   ```

3. **验证配置**：
   ```bash
   # 检查环境变量配置
   grep SCRIPT .env
   ```

#### 模型未显示

1. **前端检查**：在浏览器开发者工具中查看是否有 JavaScript 错误
2. **后端检查**：查看 API 响应是否包含注入的模型
3. **网络检查**：确认网络拦截是否正常工作

### 性能优化

#### 脚本缓存

系统会自动缓存解析的模型列表，避免重复解析：

```python
# 系统内部缓存机制
if not hasattr(self, '_cached_models'):
    self._cached_models = parse_userscript_models(script_content)
return self._cached_models
```

#### 网络拦截优化

- 只拦截必要的请求，其他请求直接通过
- 使用高效的 JSON 解析和序列化
- 最小化响应修改的开销

### 安全考虑

#### 脚本安全

- 脚本在受控的浏览器环境中执行
- 不会影响主机系统安全
- 建议只使用可信的脚本源

#### 网络安全

- 网络拦截仅限于特定的模型列表请求
- 不会拦截或修改其他敏感请求
- 所有修改都在本地进行，不会发送到外部服务器

## GUI 启动器高级功能

### 本地LLM模拟服务

GUI 集成了启动和管理一个本地LLM模拟服务的功能：

- **功能**: 监听 `11434` 端口，模拟部分 Ollama API 端点和 OpenAI 兼容的 `/v1/chat/completions` 端点
- **启动**: 在 GUI 的"启动选项"区域，点击"启动本地LLM模拟服务"按钮
- **依赖检测**: 启动前会自动检测 `localhost:2048` 端口是否可用
- **用途**: 主要用于测试客户端与 Ollama 或 OpenAI 兼容 API 的对接

### 端口进程管理

GUI 提供端口进程管理功能：

- 查询指定端口上当前正在运行的进程
- 选择并尝试停止在指定端口上找到的进程
- 手动输入 PID 终止进程

## 环境变量配置

### 代理配置

```bash
# 使用环境变量配置代理（不推荐，建议明确指定）
export HTTP_PROXY=http://127.0.0.1:7890
export HTTPS_PROXY=http://127.0.0.1:7890
python launch_camoufox.py --headless --server-port 2048 --stream-port 3120 --helper ''
```

### 日志控制

详见 [日志控制指南](logging-control.md)。

## 重要提示

### 代理配置建议

**强烈建议在所有 `launch_camoufox.py` 命令中明确指定 `--internal-camoufox-proxy` 参数，即使其值为空字符串 (`''`)，以避免意外使用系统环境变量中的代理设置。**

### 参数控制限制

API 请求中的模型参数（如 `temperature`, `max_output_tokens`, `top_p`, `stop`）**仅在通过 Playwright 页面交互获取响应时生效**。当使用集成的流式代理或外部 Helper 服务时，这些参数的传递和应用方式取决于这些服务自身的实现。

### 首次访问性能

当通过流式代理首次访问一个新的 HTTPS 主机时，服务需要为该主机动态生成并签署一个新的子证书。这个过程可能会比较耗时，导致对该新主机的首次连接请求响应较慢。一旦证书生成并缓存后，后续访问同一主机将会显著加快。

## 下一步

高级配置完成后，请参考：
- [脚本注入指南](script_injection_guide.md) - 详细的脚本注入功能使用说明
- [日志控制指南](logging-control.md)
- [故障排除指南](troubleshooting.md)
