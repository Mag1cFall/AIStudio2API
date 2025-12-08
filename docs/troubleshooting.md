# 故障排除指南

本文档提�?AI Studio Proxy API 项目常见问题的解决方案和调试方法，涵盖安装、配置、运行、API使用等各个方面�?

## 快速诊�?

在深入具体问题之前，可以先进行快速诊断：

### 1. 检查服务状�?
```bash
# 检查服务是否正常运�?
curl http://127.0.0.1:2048/health

# 检查API信息
curl http://127.0.0.1:2048/api/info
```

### 2. 检查配置文�?
```bash
# 检�?.env 文件是否存在
ls -la .env

# 检查关键配置项
grep -E "(PORT|SCRIPT_INJECTION|LOG_LEVEL)" .env
```

### 3. 查看日志
```bash
# 查看最新日�?
tail -f logs/app.log

# 查看错误日志
grep -i error logs/app.log
```

## 安装相关问题

### Python 版本兼容性问�?

**Python 版本过低**:
- **最低要�?*: Python 3.9+
- **推荐版本**: Python 3.12+
- **检查版�?*: `python --version`

**常见版本问题**:
```bash
# Python 3.8 或更低版本可能出现的错误
TypeError: 'type' object is not subscriptable
SyntaxError: invalid syntax (类型提示相关)

# 解决方案：升�?Python 版本
# macOS (使用 Homebrew)
brew install python@3.11

# Ubuntu/Debian
sudo apt update && sudo apt install python3.11

# Windows: �?python.org 下载安装
```

**虚拟环境版本问题**:
```bash
# 检查虚拟环境中�?Python 版本
python -c "import sys; print(sys.version)"

# 使用指定版本创建虚拟环境
python3.11 -m venv venv
source venv/bin/activate  # Linux/macOS
# venv\Scripts\activate  # Windows
```

### `pip install camoufox[geoip]` 失败

*   可能是网络问题或缺少编译环境。尝试不�?`[geoip]` 安装 (`pip install camoufox`)�?

### `camoufox fetch` 失败

*   常见原因是网络问题或 SSL 证书验证失败�?
*   可以尝试运行 [`python fetch_camoufox_data.py`](../fetch_camoufox_data.py) 脚本，它会尝试禁�?SSL 验证来下�?(有安全风险，仅在确认网络环境可信时使�?�?

### `playwright install-deps` 失败

*   通常�?Linux 系统缺少必要的库。仔细阅读错误信息，根据提示安装缺失的系统包 (�?`libgbm-dev`, `libnss3` �?�?

## 启动相关问题

### `launch_camoufox.py` 启动报错

*   检�?Camoufox 是否已通过 `uv run camoufox fetch` 正确下载�?
*   查看终端输出，是否有来自 Camoufox 库的具体错误信息�?
*   确保没有其他 Camoufox �?Playwright 进程冲突�?

### 端口被占�?

如果 [`server.py`](../server.py) 启动时提示端�?(`2048`) 被占用：

*   如果使用 [`app_launcher.py`](../app_launcher.py) 启动，Web UI 提供了端口占用检测和进程终止功能�?
*   手动查找并结束占用进程：
    ```bash
    # Windows
    netstat -ano | findstr 2048
    
    # Linux/macOS
    lsof -i :2048
    ```
*   或修�?[`launch_camoufox.py`](../launch_camoufox.py) �?`--server-port` 参数�?

## 认证相关问题

### 认证失败 (特别是无头模�?

**最常见**: `auth_profiles/active/` 下的 `.json` 文件已过期或无效�?

**解决方案**:
1. 启动 Web UI (`uv run python app_launcher.py`)�?
2. �?`认证文件` 页面取消激活旧文件�?
3. 重新运行 `uv run python launch_camoufox.py --debug` 生成新的认证文件�?
4. �?`认证文件` 页面激活新文件�?

### 检查认证状�?

*   查看 [`server.py`](../server.py) 日志（可通过 Web UI 的日志侧边栏查看，或 `logs/app.log`�?
*   看是否明确提到登录重定向

## 流式代理服务问题

### 端口冲突

确保流式代理服务使用的端�?(`3120` 或自定义�?`--stream-port`) 未被其他应用占用�?

### 代理配置问题

**推荐使用 .env 配置方式**:
```env
# 统一代理配置
UNIFIED_PROXY_CONFIG=http://127.0.0.1:7890
# 或禁用代�?
UNIFIED_PROXY_CONFIG=
```

**常见问题**:
*   **代理不生�?*: 确保�?`.env` 文件中设�?`UNIFIED_PROXY_CONFIG` 或使�?`--internal-camoufox-proxy` 参数
*   **代理冲突**: 使用 `UNIFIED_PROXY_CONFIG=` �?`--internal-camoufox-proxy ''` 明确禁用代理
*   **代理连接失败**: 检查代理服务器是否可用，代理地址格式是否正确

### 三层响应获取机制问题

**流式响应中断**:
- 检查集成流式代理状�?(端口 3120)
- 尝试禁用流式代理测试：在 `.env` 中设�?`STREAM_PORT=0`
- 查看 `/health` 端点了解各层状�?

**响应获取失败**:
1. **第一层失�?*: 检查流式代理服务是否正常运�?
2. **第二层失�?*: 验证 Helper 服务配置和认证文�?
3. **第三层失�?*: 检�?Playwright 浏览器连接状�?

详细说明请参�?[流式处理模式详解](streaming-modes.md)�?

### 自签名证书管�?

集成的流式代理服务会�?`certs` 文件夹内生成自签名的根证书�?

**证书删除与重新生�?*:
*   可以删除 `certs` 目录下的根证�?(`ca.crt`, `ca.key`)，代码会在下次启动时重新生成
*   **重要**: 删除根证书时�?*强烈建议同时删除 `certs` 目录下的所有其他文�?*，避免信任链错误

## API 请求问题

### 5xx / 499 错误

*   **503 Service Unavailable**: [`server.py`](../server.py) 未完全就�?
*   **504 Gateway Timeout**: AI Studio 响应慢或处理超时
*   **502 Bad Gateway**: AI Studio 页面返回错误。检�?`errors_py/` 快照
*   **500 Internal Server Error**: [`server.py`](../server.py) 内部错误。检查日志和 `errors_py/` 快照
*   **499 Client Closed Request**: 客户端提前断开连接

### 客户端无法连�?

*   确认 API 基础 URL 配置正确 (`http://<服务器IP或localhost>:端口/v1`，默认端�?2048)
*   检�?[`server.py`](../server.py) 日志是否有错�?

### AI 回复不完�?格式错误

*   AI Studio Web UI 输出不稳定。检�?`errors_py/` 快照

## 页面交互问题

### 自动清空上下文失�?

*   检查主服务器日志中的警�?
*   很可能是 AI Studio 页面更新导致 [`config/selectors.py`](../config/selectors.py) 中的 CSS 选择器失�?
*   检�?`errors_py/` 快照，对比实际页面元素更新选择器常�?

### AI Studio 页面更新导致功能失效

如果 AI Studio 更新了网页结构或 CSS 类名�?

1. 检查主服务器日志中的警告或错误
2. 检�?`errors_py/` 目录下的错误快照
3. 对比实际页面元素，更�?[`config/selectors.py`](../config/selectors.py) 中对应的 CSS 选择器常�?

### 模型参数设置未生�?

这可能是由于 AI Studio 页面�?`localStorage` 中的 `isAdvancedOpen` 未正确设置为 `true`�?

*   代理服务在启动时会尝试自动修正这些设置并重新加载页面
*   如果问题依旧，可以尝试清除浏览器缓存�?`localStorage` 后重启代理服�?

## Web UI 问题

### 无法显示日志或服务器信息

*   检查浏览器开发者工�?(F12) 的控制台和网络选项卡是否有错误
*   确认 WebSocket 连接 (`/ws/logs`) 是否成功建立
*   确认 `/health` �?`/api/info` 端点是否能正常访�?

## API密钥相关问题

### key.txt 文件问题

**文件不存在或为空**:
- 系统会自动创建空�?`key.txt` 文件
- 空文件意味着不需要API密钥验证
- 如需启用验证，手动添加密钥到文件�?

**文件权限问题**:
```bash
# 检查文件权�?
ls -la key.txt

# 修复权限问题
chmod 644 key.txt
```

**文件格式问题**:
- 确保每行一个密钥，无额外空�?
- 支持空行和以 `#` 开头的注释�?
- 使用 UTF-8 编码保存文件

### API认证失败

**401 Unauthorized 错误**:
- 检查请求头是否包含正确的认证信�?
- 验证密钥是否�?`key.txt` 文件�?
- 确认使用正确的认证头格式�?
  ```bash
  Authorization: Bearer your-api-key
  # �?
  X-API-Key: your-api-key
  ```

**密钥验证逻辑**:
- 如果 `key.txt` 为空，所有请求都不需要认�?
- 如果 `key.txt` 有内容，所�?`/v1/*` 请求都需要认�?
- 除外路径：`/v1/models`, `/health`, `/docs` �?

### Web UI 密钥管理问题

**无法验证密钥**:
- 检查输入的密钥格式，确保至�?个字�?
- 确认服务器上�?`key.txt` 文件包含该密�?
- 检查网络连接，确认 `/api/keys/test` 端点可访�?

**验证成功但无法查看密钥列�?*:
- 检查浏览器控制台是否有JavaScript错误
- 确认 `/api/keys` 端点返回正确的JSON格式数据
- 尝试刷新页面重新验证

**验证状态丢�?*:
- 验证状态仅在当前浏览器会话中有�?
- 关闭浏览器或标签页会丢失验证状�?
- 需要重新验证才能查看密钥列�?

**密钥显示异常**:
- 确认服务器返回的密钥数据格式正确
- 检查密钥打码显示功能是否正常工�?
- 验证 `maskApiKey` 函数是否正确执行

### 客户端配置问�?

**其他客户端配�?*:
- 检查客户端是否支持 `Authorization: Bearer` 认证�?
- 确认客户端正确处�?401 认证错误
- 验证客户端的超时设置是否合理

### 密钥管理最佳实�?

**安全建议**:
- 定期更换API密钥
- 不要在日志或公开场所暴露完整密钥
- 使用足够复杂的密钥（建议16个字符以上）
- 限制密钥的使用范围和权限

**备份建议**:
- 定期备份 `key.txt` 文件
- 记录密钥的创建时间和用�?
- 建立密钥轮换机制

### 对话功能问题

*   **发送消息后收到401错误**: API密钥认证失败，需要重新验证密�?
*   **无法发送空消息**: 这是正常的安全机�?
*   **对话请求失败**: 检查网络连接，确认服务器正常运�?

## 脚本注入问题 🆕

### 脚本注入功能未启�?

**检查配�?*:
```bash
# 检�?.env 文件中的配置
grep SCRIPT_INJECTION .env
grep USERSCRIPT_PATH .env
```

**常见问题**:
- `ENABLE_SCRIPT_INJECTION=false` - 功能被禁�?
- 脚本文件路径不正�?
- 脚本文件不存在或无法读取

**解决方案**:
```bash
# 启用脚本注入
echo "ENABLE_SCRIPT_INJECTION=true" >> .env

# 检查脚本文件是否存�?
ls -la browser/more_models.js

# 检查文件权�?
chmod 644 browser/more_models.js
```

### 模型未显示在列表�?

**前端检�?*:
1. 打开浏览器开发者工�?(F12)
2. 查看控制台是否有 JavaScript 错误
3. 检查网络选项卡中的模型列表请�?

**后端检�?*:
```bash
# 查看脚本注入相关日志
uv run python launch_camoufox.py --debug | grep -i "script\|inject\|model"

# 检�?API 响应
curl http://localhost:2048/v1/models | jq '.data[] | select(.injected == true)'
```

**常见原因**:
- 脚本格式错误，无法解�?`MODELS_TO_INJECT` 数组
- 网络拦截失败，脚本注入未生效
- 模型名称格式不正�?

### 脚本解析失败

**检查脚本格�?*:
```javascript
// 确保脚本包含正确的模型数组格�?
const MODELS_TO_INJECT = [
    {
        name: 'models/your-model-name',
        displayName: 'Your Model Display Name',
        description: 'Model description'
    }
];
```

**调试步骤**:
1. 验证脚本文件�?JavaScript 语法
2. 检查模型数组的格式是否正确
3. 确认模型名称�?`models/` 开�?

### 网络拦截失败

**检�?Playwright 状�?*:
- 确认浏览器上下文正常创建
- 检查网络路由是否正确设�?
- 验证请求 URL 匹配规则

**调试方法**:
```bash
# 启用详细日志查看网络拦截状�?
export DEBUG_LOGS_ENABLED=true
uv run python launch_camoufox.py --debug
```

**常见错误**:
- 浏览器上下文创建失败
- 网络路由设置异常
- 请求 URL 不匹配拦截规�?

### 模型解析问题

**脚本格式错误**:
```bash
# 检查脚本文件语�?
node -c browser/more_models.js
```

**文件权限问题**:
```bash
# 检查文件权�?
ls -la browser/more_models.js

# 修复权限
chmod 644 browser/more_models.js
```

**脚本文件不存�?*:
- 系统会静默跳过不存在的脚本文�?
- 检�?`USERSCRIPT_PATH` 环境变量设置
- 确保脚本文件包含有效�?`MODELS_TO_INJECT` 数组

### 性能问题

**脚本注入延迟**:
- 网络拦截可能增加轻微延迟
- 大量模型注入可能影响页面加载
- 建议限制注入模型数量�? 20个）

**内存使用**:
- 脚本内容会被缓存在内存中
- 大型脚本文件可能增加内存使用
- 定期重启服务释放内存

### 调试技�?

**启用详细日志**:
```bash
# �?.env 文件中添�?
DEBUG_LOGS_ENABLED=true
TRACE_LOGS_ENABLED=true
SERVER_LOG_LEVEL=DEBUG
```

**检查注入状�?*:
```bash
# 查看脚本注入相关的日志输�?
tail -f logs/app.log | grep -i "script\|inject"
```

**验证模型注入**:
```bash
# 检�?API 返回的模型列�?
curl -s http://localhost:2048/v1/models | jq '.data[] | select(.injected == true) | {id, display_name}'
```

### 禁用脚本注入

如果遇到严重问题，可以临时禁用脚本注入：

```bash
# 方法1：修�?.env 文件
echo "ENABLE_SCRIPT_INJECTION=false" >> .env

# 方法2：使用环境变�?
export ENABLE_SCRIPT_INJECTION=false
uv run python launch_camoufox.py --headless

# 方法3：删除脚本文件（临时�?
mv browser/more_models.js browser/more_models.js.bak
```

## 日志和调�?

### 查看详细日志

*   `logs/app.log`: FastAPI 服务器详细日�?
*   `logs/launch_app.log`: 启动器日�?
*   Web UI 右侧边栏: 实时显示 `INFO` 及以上级别的日志

### 环境变量控制

可以通过环境变量控制日志详细程度�?

```bash
# 设置日志级别
export SERVER_LOG_LEVEL=DEBUG

# 启用详细调试日志
export DEBUG_LOGS_ENABLED=true

# 启用跟踪日志（通常不需要）
export TRACE_LOGS_ENABLED=true
```

### 错误快照

出错时会自动�?`errors_py/` 目录保存截图�?HTML，这些文件对调试很有帮助�?

## 性能问题

### Asyncio 相关错误

您可能会在日志中看到一些与 `asyncio` 相关的错误信息，特别是在网络连接不稳定时。如果核心代理功能仍然可用，这些错误可能不直接影响主要功能�?

### 首次访问新主机的性能问题

当通过流式代理首次访问一个新�?HTTPS 主机时，服务需要动态生成证书，这个过程可能比较耗时。一旦证书生成并缓存后，后续访问会显著加快�?

## 获取帮助

如果问题仍未解决�?

1. 查看项目�?[GitHub Issues](https://github.com/Mag1cFall/AIStudio2API/issues)
2. 提交新的 Issue 并包含：
   - 详细的错误描�?
   - 相关的日志文件内�?
   - 系统环境信息
   - 复现步骤

## 下一�?

故障排除完成后，请参考：
- [脚本注入指南](script_injection_guide.md) - 脚本注入功能详细说明
- [日志控制指南](logging-control.md)
- [高级配置指南](advanced-configuration.md)
