# AI Studio Proxy API

一个基于 Python 的代理服务器，用于将 Google AI Studio 的网页界面转换为 OpenAI 兼容的 API。通过 Camoufox (反指纹检测的 Firefox) 和 Playwright 自动化，提供稳定的 API 访问。

## 🚀 特性

- **OpenAI 兼容 API**: 完全兼容 OpenAI 格式的 `/v1/chat/completions` 端点
- **智能模型切换**: 通过 `model` 字段动态切换 AI Studio 中的模型
- **反指纹检测**: 使用 Camoufox 浏览器降低被检测风险
- **图形界面启动器**: 功能丰富的 GUI 启动器，简化配置和管理
- **Ollama 兼容层**: 内置 `llm.py` 提供 Ollama 格式 API 兼容
- **模块化架构**: 清晰的模块分离设计，易于维护
- **现代化工具链**: Poetry 依赖管理 + 完整类型支持

## 📋 系统要求

- **Python**: 3.12 (推荐)
- **依赖管理**: [Poetry](https://python-poetry.org/)
- **操作系统**: Windows, macOS, Linux
- **内存**: 建议 2GB+ 可用内存
- **网络**: 稳定的互联网连接访问 Google AI Studio

## 🛠️ 安装步骤

### 1. 安装 Poetry

```bash
curl.exe -sSL https://install.python-poetry.org | python -
# 或
(Invoke-WebRequest -Uri https://install.python-poetry.org -UseBasicParsing).Content | python -
```
---
安装结束后会显示类似如下内容:
````
A. Append the bin directory to your user environment variable `PATH`:

```
[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";C:\Users\1\AppData\Roaming\Python\Scripts", "User")
```

B. Try to append the bin directory to PATH every when you run PowerShell (>=6 recommended):

```
echo 'if (-not (Get-Command poetry -ErrorAction Ignore)) { $env:Path += ";C:\Users\1\AppData\Roaming\Python\Scripts" }' | Out-File -Append $PROFILE
```

Alternatively, you can call Poetry explicitly with `C:\Users\1\AppData\Roaming\Python\Scripts\poetry`.

You can test that everything is set up by executing:

`poetry --version`
````
按要求执行指令，建议直接执行:
```
[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";C:\Users\1\AppData\Roaming\Python\Scripts", "User")
```
执行后过几秒，什么都不会输出。此时关闭Powershell窗口(或VSCode软件、命令行...等)，重新打开后执行:
```
poetry --version
```
预计输出:
```
PS C:\Users\1\Desktop\AIStudio2API> poetry --version
Poetry (version 2.2.1)
```
如果执行命令报错、总之你没有成功通过执行命令来将`poetry`添加到系统变量，可以手动去「编辑账户的环境变量」- Path 里添加路径，或不添加系统变量，直接使用绝对路径继续后面的命令。

### 2. 克隆项目

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

### 3. 配置 Python 环境

**重要**: 确保使用 Python 3.12，不要使用 Python 3.13

```bash
# 设置 Poetry 使用指定的 Python 3.12 版本
# 更换成你自己的用户名和路径
poetry env use C:\Users\2\AppData\Local\Programs\Python\Python312\python.exe

# Poetry 会输出类似以下信息：
# Creating virtualenv aistudio2api-QAhNHTrK-py3.12 in C:\Users\2\AppData\Local\pypoetry\Cache\virtualenvs
# Using virtualenv: C:\Users\2\AppData\Local\pypoetry\Cache\virtualenvs\aistudio2api-QAhNHTrK-py3.12
```

### 4. 安装依赖

```bash
poetry install
poetry run camoufox fetch
```

**注意**: 安装过程中会自动下载和安装 Camoufox 浏览器（约 600MB），这是项目的核心组件，用于反指纹检测。首次安装可能需要较长时间，请耐心等待。

***

## 🚀 快速开始

### 首次使用（需要认证）

1. **启动图形界面**:
   ```bash
   poetry run python gui_launcher.py
   ```

2. **配置代理**（建议）:
   - 在 GUI 中勾选"启用浏览器代理"
   - 输入您的代理地址（如有）

3. **启动有头模式进行认证**:
   - 点击"启动有头模式 (新终端)"
   - 浏览器会自动打开并导航到 AI Studio
   - 手动登录您的 Google 账号
   - 确保进入 AI Studio 主页
   - 在命令行终端按回车键保存认证信息

4. **认证完成后**:
   - 认证信息会自动保存
   - 可以关闭有头模式的浏览器和终端

### 日常使用（已有认证）

认证保存后，可以使用无头模式：

1. 启动图形界面:
   ```bash
   poetry run python gui_launcher.py
   ```

2. 点击"启动无头模式 (新终端)"

3. API 服务将在后台运行，默认端口 `2048`
增加了前端界面（开发中），访问`http://localhost:2048/`，可以获取更清晰的日志。

## 📡 API 使用

### OpenAI 兼容接口

服务启动后，可以使用 OpenAI 兼容的 API：

```bash
curl -X POST http://localhost:2048/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-1.5-pro",
    "messages": [
      {"role": "user", "content": "Hello, world!"}
    ]
  }'
```

### 客户端配置示例

以 Open WebUI 为例：

1. 打开 Open WebUI 设置
2. 在"连接"部分添加新模型:
   - **API 基础 URL**: `http://127.0.0.1:2048/v1`
   - **模型名称**: `gemini-1.5-pro` (或其他 AI Studio 支持的模型)
   - **API 密钥**: 留空或输入任意字符

### Ollama 兼容层

项目还提供 Ollama 格式的 API 兼容：

```bash
# 启动 Ollama 兼容服务
poetry run python gui_launcher.py
# 在 GUI 中点击"启动本地LLM模拟服务"

# 使用 Ollama 格式 API
curl http://localhost:11434/api/tags
curl -X POST http://localhost:11434/api/chat \
  -d '{"model": "gemini", "messages": [{"role": "user", "content": "Hello"}]}'
```

## 🏗️ 项目架构

```
AIStudio2API/
├── gui_launcher.py          # 图形界面启动器
├── launch_camoufox.py       # 命令行启动器
├── server.py                # 主服务器
├── llm.py                   # Ollama 兼容层
├── api_utils/               # API 处理模块
├── browser_utils/           # 浏览器自动化模块
├── config/                  # 配置管理
├── models/                  # 数据模型
├── stream/                  # 流式代理
└── docs/                    # 详细文档
```

## ⚙️ 配置说明

### 环境变量配置

复制并编辑环境配置文件：

```bash
cp .env.example .env
# 编辑 .env 文件进行自定义配置
```

### 端口配置

- **FastAPI 服务**: 默认端口 `2048`
- **Camoufox 调试**: 默认端口 `9222`
- **流式代理**: 默认端口 `3120`
- **Ollama 兼容**: 默认端口 `11434`

## 🔧 高级功能

### 代理配置

支持通过代理访问 AI Studio：

1. 在 GUI 中启用"浏览器代理"
2. 输入代理地址（如 `http://127.0.0.1:7890`）
3. 点击"测试"按钮验证代理连接

### 认证文件管理

- 认证文件存储在 `auth_profiles/` 目录
- 支持多个认证文件的保存和切换
- 通过 GUI 的"管理认证文件"功能进行管理

## 🐳 Docker 部署

```bash
cd docker
cp .env.docker .env
# 编辑 .env 文件
docker compose up -d
```

详细说明请参见 [Docker 部署指南](docker/README-Docker.md)。

## 📚 详细文档

- [安装指南](docs/installation-guide.md)
- [环境变量配置](docs/environment-configuration.md)
- [认证设置](docs/authentication-setup.md)
- [API 使用指南](docs/api-usage.md)
- [故障排除](docs/troubleshooting.md)

## ⚠️ 重要提示

### 关于 Camoufox

本项目使用 [Camoufox](https://camoufox.com/) 浏览器来避免被检测为自动化脚本。Camoufox 基于 Firefox，通过修改底层实现来伪装设备指纹，提供更好的隐蔽性。

### 使用限制

- **客户端管理历史**: 代理不支持 UI 内编辑，客户端需要维护完整的聊天记录
- **参数支持**: 支持 `temperature`、`max_output_tokens`、`top_p`、`stop` 等参数
- **认证有效期**: 认证文件可能会过期，需要重新进行认证流程

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！