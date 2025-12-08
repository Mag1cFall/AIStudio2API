# 安装指南

本文档提供基于 uv 的详细安装步骤和环境配置说明。

## 🔧 系统要求

### 基础要求

*   **Python**: 3.9+
    *   **推荐版本**: Python 3.12 以获得最佳性能和兼容性
    *   **最低要求**: Python 3.9 (支持所有当前依赖版本)
    *   **完全支持**: Python 3.9, 3.10, 3.11, 3.12, 3.13
*   **uv**: 极速的现代化 Python 包管理工具 (替代 Pip/Poetry)
*   **Git**: 用于克隆仓库 (推荐)
*   **Google AI Studio 账号**: 并能正常访问和使用
*   **Node.js**: 16+ (可选，用于 Pyright 类型检查)

### 系统依赖

*   **Linux**: `xvfb` (虚拟显示，可选)
    *   Debian/Ubuntu: `sudo apt-get update && sudo apt-get install -y xvfb`
    *   Fedora: `sudo dnf install -y xorg-x11-server-Xvfb`
*   **macOS**: 通常无需额外依赖
*   **Windows**: 通常无需额外依赖

## 🚀 快速安装 (推荐)

### 1. 安装 uv

项目采用 **uv** 进行极速依赖管理。

**Windows (PowerShell)**:
```powershell
powershell -c "irm https://astral.sh/uv/install.ps1 | iex"
```

**macOS / Linux**:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

### 2. 克隆仓库

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

### 3. 安装依赖

uv 会自动读取 `pyproject.toml` 和 `uv.lock`，并在瞬间完成虚拟环境创建与依赖同步：

```bash
# 一键同步所有依赖
uv sync
```

**uv 优势**:
- ⚡ **极速**: 比 pip/poetry 快 10-100 倍
- ✅ **省心**: 自动管理虚拟环境 (.venv)
- 🔒 **锁定**: 严格遵循 `uv.lock` 确保环境一致
- 📦 **统一**: `uv run` 自动处理命令执行环境

### 4. 下载 Camoufox 浏览器

这是项目的核心组件，必须下载：

```bash
# 使用 uv 运行命令
uv run camoufox fetch
```

**依赖版本说明** (由 uv 管理):
- **FastAPI 0.115.12**: 最新稳定版本，包含性能优化和新功能
  - 新增 Query/Header/Cookie 参数模型支持
  - 改进的类型提示和验证机制
  - 更好的 OpenAPI 文档生成和异步性能
- **Pydantic >=2.7.1,<3.0.0**: 现代数据验证库，版本范围确保兼容性
- **Uvicorn 0.29.0**: 高性能 ASGI 服务器，支持异步处理和HTTP/2
- **Playwright**: 最新版本，用于浏览器自动化、页面交互和网络拦截
- **Camoufox 0.4.11**: 反指纹检测浏览器，包含 geoip 数据和增强隐蔽性
- **WebSockets 12.0**: 用于实时日志传输、状态监控和Web UI通信
- **aiohttp ~3.9.5**: 异步HTTP客户端，支持代理和流式处理
- **python-dotenv 1.0.1**: 环境变量管理，支持 .env 文件配置

### 5. 安装 Playwright 浏览器依赖（可选）

虽然 Camoufox 使用自己的 Firefox，但首次运行可能需要安装一些基础依赖：

```bash
# 使用 uv 运行安装命令
uv run playwright install-deps firefox
```

如果 `camoufox fetch` 因网络问题失败，可以尝试运行项目中的 [`camoufox/fetch_camoufox_data.py`](../camoufox/fetch_camoufox_data.py) 脚本 (详见[故障排除指南](troubleshooting.md))。

## 🔍 验证安装

### 检查环境

```bash
# 检查 uv 环境下的 Python 版本
uv run python --version

# 查看已安装的依赖树
uv tree
```

### 检查关键组件

```bash
# 检查 Camoufox
uv run camoufox --version

# 检查 FastAPI
uv run python -c "import fastapi; print(f'FastAPI: {fastapi.__version__}')"

# 检查 Playwright
uv run python -c "import playwright; print('Playwright: OK')"
```

## 多平台指南

### macOS / Linux

*   通常安装过程比较顺利。
*   `uv sync` 会自动在项目根目录下创建 `.venv` 虚拟环境。
*   `playwright install-deps firefox` 可能需要系统包管理器（如 `apt` for Debian/Ubuntu, `yum`/`dnf` for Fedora/CentOS, `brew` for macOS）安装一些依赖库。如果命令失败，请仔细阅读错误输出，根据提示安装缺失的系统包。有时可能需要 `sudo` 权限执行 `playwright install-deps`。
*   防火墙通常不会阻止本地访问，但如果从其他机器访问，需要确保端口（默认 2048）是开放的。
*   对于Linux 用户，可以考虑使用 `--virtual-display` 标志启动 (需要预先安装 `xvfb`)，它会利用 Xvfb 创建一个虚拟显示环境来运行浏览器，这可能有助于进一步降低被检测的风险和保证网页正常对话。

### Windows

#### 原生 Windows

*   `uv` 在 Windows 上的表现同样优秀。
*   Windows 防火墙可能会阻止 Uvicorn/FastAPI 监听端口。如果遇到连接问题（特别是从其他设备访问时），请检查 Windows 防火墙设置，允许 Python 或特定端口的入站连接。
*   `playwright install-deps` 命令在原生 Windows 上作用有限（主要用于 Linux），但运行 `camoufox fetch` (内部会调用 Playwright) 会确保下载正确的浏览器。
*   **推荐使用 [`src/app_launcher.py`](../src/app_launcher.py) 启动**，或者直接运行 `start_webui.bat`，它们会自动处理后台进程和用户交互。

#### WSL (Windows Subsystem for Linux)

*   **推荐**: 对于习惯 Linux 环境的用户，WSL (特别是 WSL2) 提供了更好的体验。
*   在 WSL 环境内，按照 **macOS / Linux** 的步骤进行安装 (使用 `uv`)。
*   所有命令（`uv sync`, `uv run camoufox fetch` 等）都应在 WSL 终端内执行。
*   无头模式 (通过 `uv run src/app_launcher.py` 启动) 不受影响。

## 配置环境变量（推荐）

安装完成后，强烈建议配置 `.env` 文件来简化后续使用：

### 创建配置文件

```bash
# 复制配置模板
cp .env.example .env

# 编辑配置文件
nano .env  # 或使用其他编辑器
```

### 基本配置示例

```env
# 服务端口配置
DEFAULT_FASTAPI_PORT=2048
STREAM_PORT=3120

# 代理配置（如需要）
# HTTP_PROXY=http://127.0.0.1:7890

# 日志配置
SERVER_LOG_LEVEL=INFO
DEBUG_LOGS_ENABLED=false
```

配置完成后，启动命令将变得非常简单：

```bash
# 简单启动，无需复杂参数
uv run python src/launch_camoufox.py --headless
```

详细配置说明请参见 [环境变量配置指南](environment-configuration.md)。

## 可选：配置API密钥

您也可以选择配置API密钥来保护您的服务：

### 创建密钥文件

在 `data/` 目录下创建 `key.txt` 文件：

```bash
# 创建密钥文件
touch data/key.txt

# 添加密钥（每行一个）
echo "your-first-api-key" >> data/key.txt
echo "your-second-api-key" >> data/key.txt
```

### 密钥格式要求

- 每行一个密钥
- 至少8个字符
- 支持空行和注释行（以 `#` 开头）
- 使用 UTF-8 编码

### 示例密钥文件

```
# API密钥配置文件
# 每行一个密钥

sk-1234567890abcdef
my-secure-api-key-2024
admin-key-for-testing

# 这是注释行，会被忽略
```

### 安全说明

- **无密钥文件**: 服务不需要认证，任何人都可以访问API
- **有密钥文件**: 所有API请求都需要提供有效的密钥
- **密钥保护**: 请妥善保管密钥文件，不要提交到版本控制系统

## 下一步

安装完成后，请参考：
- [环境变量配置指南](environment-configuration.md) - ⭐ 推荐先配置，简化后续使用
- [首次运行与认证指南](authentication-setup.md)
- [API使用指南](api-usage.md) - 包含详细的密钥管理说明
- [故障排除指南](troubleshooting.md)
