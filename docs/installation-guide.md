# 安装指南

本文档提供基于 Poetry 的详细安装步骤和环境配置说明。

## 🔧 系统要求

### 基础要求

*   **Python**: 3.9+ (推荐 3.10+ 或 3.11+)
    *   **推荐版本**: Python 3.11+ 以获得最佳性能和兼容性
    *   **最低要求**: Python 3.9 (支持所有当前依赖版本)
    *   **完全支持**: Python 3.9, 3.10, 3.11, 3.12, 3.13
*   **Poetry**: 1.4+ (现代化 Python 依赖管理工具)
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

### 一键安装脚本

```bash
# 安装脚本已移除，请使用手动安装方式
```

## 📋 手动安装步骤

### 1. 安装 Poetry

如果您尚未安装 Poetry，请先安装：

```bash
# macOS/Linux
curl -sSL https://install.python-poetry.org | python3 -

# Windows (PowerShell)
(Invoke-WebRequest -Uri https://install.python-poetry.org -UseBasicParsing).Content | py -

# 或使用包管理器
# macOS: brew install poetry
# Ubuntu/Debian: apt install python3-poetry
# Windows: winget install Python.Poetry
```

### 2. 克隆仓库

```bash
git clone https://github.com/Mag1cFall/Gemini-AIStudio2API.git
cd Gemini-AIStudio2API
```

### 3. 安装依赖

Poetry 会自动创建虚拟环境并安装所有依赖：

```bash
# 安装生产依赖
poetry install

# 安装包括开发依赖 (推荐开发者)
poetry install --with dev
```

**Poetry 优势**:
- ✅ 自动创建和管理虚拟环境
- ✅ 依赖解析和版本锁定 (`poetry.lock`)
- ✅ 区分生产依赖和开发依赖
- ✅ 语义化版本控制

### 4. 激活虚拟环境

```bash
# 激活 Poetry 创建的虚拟环境
poetry env activate

# 或者在每个命令前加上 poetry run
poetry run python --version
```

### 5. 下载 Camoufox 浏览器

```bash
# 在 Poetry 环境中下载 Camoufox 浏览器
poetry run camoufox fetch

# 或在激活的环境中
camoufox fetch
```

**依赖版本说明** (由 Poetry 管理):
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

### 6. 安装 Playwright 浏览器依赖（可选）

虽然 Camoufox 使用自己的 Firefox，但首次运行可能需要安装一些基础依赖：

```bash
# 在 Poetry 环境中安装 Playwright 依赖
poetry run playwright install-deps firefox

# 或在激活的环境中
playwright install-deps firefox
```

如果 `camoufox fetch` 因网络问题失败，可以尝试运行项目中的 [`fetch_camoufox_data.py`](../fetch_camoufox_data.py) 脚本 (详见[故障排除指南](troubleshooting.md))。

## 🔍 验证安装

### 检查 Poetry 环境

```bash
# 查看 Poetry 环境信息
poetry env info

# 查看已安装的依赖
poetry show

# 查看依赖树
poetry show --tree

# 检查 Python 版本
poetry run python --version
```

### 检查关键组件

```bash
# 检查 Camoufox
poetry run camoufox --version

# 检查 FastAPI
poetry run python -c "import fastapi; print(f'FastAPI: {fastapi.__version__}')"

# 检查 Playwright
poetry run python -c "import playwright; print('Playwright: OK')"
```

## 多平台指南

### macOS / Linux

*   通常安装过程比较顺利。确保 Python 和 pip 已正确安装并配置在系统 PATH 中。
*   使用 `source venv/bin/activate` 激活虚拟环境。
*   `playwright install-deps firefox` 可能需要系统包管理器（如 `apt` for Debian/Ubuntu, `yum`/`dnf` for Fedora/CentOS, `brew` for macOS）安装一些依赖库。如果命令失败，请仔细阅读错误输出，根据提示安装缺失的系统包。有时可能需要 `sudo` 权限执行 `playwright install-deps`。
*   防火墙通常不会阻止本地访问，但如果从其他机器访问，需要确保端口（默认 2048）是开放的。
*   对于Linux 用户，可以考虑使用 `--virtual-display` 标志启动 (需要预先安装 `xvfb`)，它会利用 Xvfb 创建一个虚拟显示环境来运行浏览器，这可能有助于进一步降低被检测的风险和保证网页正常对话。

### Windows

#### 原生 Windows

*   确保在安装 Python 时勾选了 "Add Python to PATH" 选项。
*   使用 `venv\\Scripts\\activate` 激活虚拟环境。
*   Windows 防火墙可能会阻止 Uvicorn/FastAPI 监听端口。如果遇到连接问题（特别是从其他设备访问时），请检查 Windows 防火墙设置，允许 Python 或特定端口的入站连接。
*   `playwright install-deps` 命令在原生 Windows 上作用有限（主要用于 Linux），但运行 `camoufox fetch` (内部会调用 Playwright) 会确保下载正确的浏览器。
*   **推荐使用 [`gui_launcher.py`](../gui_launcher.py) 启动**，它们会自动处理后台进程和用户交互。如果直接运行 [`launch_camoufox.py`](../launch_camoufox.py)，终端窗口需要保持打开。

#### WSL (Windows Subsystem for Linux)

*   **推荐**: 对于习惯 Linux 环境的用户，WSL (特别是 WSL2) 提供了更好的体验。
*   在 WSL 环境内，按照 **macOS / Linux** 的步骤进行安装和依赖处理 (通常使用 `apt` 命令)。
*   需要注意的是网络访问：
    *   从 Windows 访问 WSL 中运行的服务：通常可以通过 `localhost` 或 WSL 分配的 IP 地址访问。
    *   从局域网其他设备访问 WSL 中运行的服务：可能需要配置 Windows 防火墙以及 WSL 的网络设置（WSL2 的网络通常更容易从外部访问）。
*   所有命令（`git clone`, `pip install`, `camoufox fetch`, `python launch_camoufox.py` 等）都应在 WSL 终端内执行。
*   在 WSL 中运行 `--debug` 模式：[`launch_camoufox.py --debug`](../launch_camoufox.py) 会尝试启动 Camoufox。如果你的 WSL 配置了 GUI 应用支持（如 WSLg 或第三方 X Server），可以看到浏览器界面。否则，它可能无法显示界面，但服务本身仍会尝试启动。无头模式 (通过 [`gui_launcher.py`](../gui_launcher.py) 启动) 不受影响。

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
python launch_camoufox.py --headless
```

详细配置说明请参见 [环境变量配置指南](environment-configuration.md)。

## 可选：配置API密钥

您也可以选择配置API密钥来保护您的服务：

### 创建密钥文件

在项目根目录创建 `key.txt` 文件：

```bash
# 创建密钥文件
touch key.txt

# 添加密钥（每行一个）
echo "your-first-api-key" >> key.txt
echo "your-second-api-key" >> key.txt
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
- **[环境变量配置指南](environment-configuration.md)** - ⭐ 推荐先配置，简化后续使用
- [首次运行与认证指南](authentication-setup.md)
- [日常运行指南](daily-usage.md)
- [API使用指南](api-usage.md) - 包含详细的密钥管理说明
- [故障排除指南](troubleshooting.md)
