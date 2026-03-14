# 首次运行与认证设置指南

为了避免每次启动都手动登录 AI Studio，你需要先通过 [`launch_camoufox.py --debug`](../launch_camoufox.py) 模式或 [`app_launcher.py`](../app_launcher.py) 的有头模式运行一次来生成认证文件。

## 认证文件的重要性

**认证文件是无头模式的关键**: 无头模式依赖于 `auth_profiles/active/` 目录下的有效 `.json` 文件来维持登录状态和访问权限。**文件可能会过期**，需要定期通过 [`launch_camoufox.py --debug`](../launch_camoufox.py) 模式手动运行、登录并保存新的认证文件来替换更新。

## 方法一：通过命令行运行 Debug 模式

**推荐使用 .env 配置方式**:
```env
# .env 文件配置
DEFAULT_FASTAPI_PORT=2048
STREAM_PORT=0
LAUNCH_MODE=normal
DEBUG_LOGS_ENABLED=true
```

```bash
# 简化启动命令 (推荐)
uv run python launch_camoufox.py --debug

# 传统命令行方式 (仍然支持)
uv run python launch_camoufox.py --debug --server-port 2048 --stream-port 0 --helper '' --internal-camoufox-proxy ''
```

**重要参数说明:**
*   `--debug`: 启动有头模式，用于首次认证和调试
*   `--server-port <端口号>`: 指定 FastAPI 服务器监听的端口 (默认: 2048)
*   `--stream-port <端口号>`: 启动集成的流式代理服务端口 (默认: 3120)。设置为 `0` 可禁用此服务，首次启动建议禁用
*   `--helper <端点URL>`: 指定外部 Helper 服务的地址。设置为空字符串 `''` 表示不使用外部 Helper
*   `--internal-camoufox-proxy <代理地址>`: 为 Camoufox 浏览器指定代理。设置为空字符串 `''` 表示不使用代理
*   **注意**: 如果需要启用流式代理服务，建议同时配置 `--internal-camoufox-proxy` 参数以确保正常运行

### 操作步骤

1. 脚本会启动 Camoufox（通过内部调用自身），并在终端输出启动信息。
2. 你会看到一个 **带界面的 Firefox 浏览器窗口** 弹出。
3. **关键交互:** **在弹出的浏览器窗口中完成 Google 登录**，直到看到 AI Studio 聊天界面。 (脚本会自动处理浏览器连接，无需用户手动操作)。
4. **登录确认操作**: 当系统检测到登录页面并在终端显示类似以下提示时：
   ```
   检测到可能需要登录。如果浏览器显示登录页面，请在浏览器窗口中完成 Google 登录，然后在此处按 Enter 键继续...
   ```
   **用户必须在终端中按 Enter 键确认操作才能继续**。这个确认步骤是必需的，系统会等待用户的确认输入才会进行下一步的登录状态检查。
5. 回到终端根据提示回车即可，如果设置使用非自动保存模式（即将弃用），请根据提示保存认证时输入 `y` 并回车 (文件名可默认)。文件会保存在 `auth_profiles/saved/`。
6. **将 `auth_profiles/saved/` 下新生成的 `.json` 文件移动到 `auth_profiles/active/` 目录。** 确保 `active` 目录下只有一个 `.json` 文件。
7. 可以按 `Ctrl+C` 停止 `--debug` 模式的运行。

## 方法二：通过 GUI 启动有头模式

1. 运行 `uv run python app_launcher.py`。
2. 浏览器会自动打开管理界面（默认 `http://127.0.0.1:9000`）。
3. 在 `配置` 页面选择 `调试模式 (Debug)`。
4. 点击 `启动服务` 按钮。
5. 在弹出的浏览器窗口中，按照提示进行 Google 登录。
6. 登录成功后，在你的**命令行窗口**按回车保存认证文件。
7. 你可以在 `认证文件` 页面管理和激活保存的文件。

## 激活认证文件

1. 进入 `auth_profiles/saved/` 目录，找到刚才保存的 `.json` 认证文件。
2. 将这个 `.json` 文件 **移动或复制** 到 `auth_profiles/active/` 目录下。
3. **重要:** 确保 `auth_profiles/active/` 目录下 **有且仅有一个 `.json` 文件**。无头模式启动时会自动加载此目录下的第一个 `.json` 文件。

## 认证文件过期处理

**认证文件会过期!** Google 的登录状态不是永久有效的。当无头模式启动失败并报告认证错误或重定向到登录页时，意味着 `active` 目录下的认证文件已失效。你需要：

1. 在 GUI 的 `认证文件` 页面，取消激活旧文件。
2. 重新执行上面的 **【通过命令行运行 Debug 模式】** 或 **【通过 GUI 启动有头模式】** 步骤，生成新的认证文件。
3. 在 GUI 中激活新生成的认证文件。

## 重要提示

*   **首次访问新主机的性能问题**: 当通过流式代理首次访问一个新的 HTTPS 主机时，服务需要为该主机动态生成并签署一个新的子证书。这个过程可能会比较耗时，导致对该新主机的首次连接请求响应较慢，甚至在某些情况下可能被主程序（如 [`server.py`](../server.py) 中的 Playwright 交互逻辑）误判为浏览器加载超时。一旦证书生成并缓存后，后续访问同一主机将会显著加快。

## 下一步

认证设置完成后，请参考：
- [API 使用指南](api-usage.md)
