# AI Studio Proxy API

一个基于 Python 的代理服务器，用于将 Google AI Studio 的网页界面转换为 OpenAI 兼容的 API。通过 Camoufox (反指纹检测的 Firefox) 和 Playwright 自动化，提供稳定的 API 访问。

## 🚀 特性

- **OpenAI 兼容 API**: 完全兼容 OpenAI 格式的 `/v1/chat/completions` 端点
- **智能模型切换**: 通过 `model` 字段动态切换 AI Studio 中的模型
- **反指纹检测**: 使用 Camoufox 浏览器降低被检测风险
- **图形界面启动器**: 功能丰富的 **网页** 启动器，简化配置和管理
- **Ollama 兼容层**: 内置 `llm.py` 提供 Ollama 格式 API 兼容
- **模块化架构**: 清晰的模块分离设计，易于维护
- **现代化工具链**: uv 依赖管理 + 完整类型支持

## 📋 系统要求

- **Python**: 3.12 (推荐)
- **依赖管理**: [uv](https://docs.astral.sh/uv/)
- **操作系统**: Windows, macOS, Linux
- **内存**: 建议 2GB+ 可用内存
- **网络**: 稳定的互联网连接访问 Google AI Studio

## 🛠️ 安装步骤

### 1. 安装 uv

Windows (PowerShell):
```powershell
powershell -c "irm https://astral.sh/uv/install.ps1 | iex"
```

macOS / Linux:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

预期输出：
```
PS C:\Users\2\Desktop\AIStudio2API> powershell -c "irm https://astral.sh/uv/install.ps1 | iex"
Downloading uv 0.9.11 (x86_64-pc-windows-msvc)
Installing to C:\Users\2\.local\bin
  uv.exe
  uvx.exe
  uvw.exe
everything's installed!

To add C:\Users\2\.local\bin to your PATH, either restart your shell or run:

    set Path=C:\Users\2\.local\bin;%Path%   (cmd)
    $env:Path = "C:\Users\2\.local\bin;$env:Path"   (powershell)
```
请按照您的路径将其添加到环境变量。

### 2. 克隆项目

```bash
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API
```

### 3. 安装依赖

```bash
uv sync
uv run camoufox fetch
uv run playwright install firefox
```

**注意**: 安装过程中会自动下载和安装 Camoufox 浏览器（约 600MB），这是项目的核心组件，用于反指纹检测。首次安装可能需要较长时间，请耐心等待。

***

## 🚀 快速开始

### 首次使用（需要认证）

1. **启动图形界面**:
   ```bash
   uv run python app_launcher.py
   ```

2. **配置代理**（建议）:
   - 在 GUI 中勾选"启用浏览器代理"
   - 输入您的代理地址（如`http://127.0.0.1:7890`）

3. **启动有头模式进行认证**:
   - 点击"启动有头模式 (新终端)"
   - **命令行终端**内输入`N`，获取新的认证文件
   - 命令行终端指`start_webui.bat`启动的终端，或者您运行`uv run python app_launcher.py`的终端
   - 浏览器会自动打开并导航到 AI Studio
   - 手动登录您的 Google 账号
   - 确保进入 AI Studio 主页
   - 在命令行终端按回车键保存认证信息
   - 认证文件保存情况会在日志里输出，命令行内不会输出内容

4. **认证完成后**:
   - 认证信息会自动保存
   - 可以关闭有头模式的浏览器和终端

### 日常使用（已有认证）

认证保存后，可以使用无头模式：

1. 启动图形界面:
   ```bash
   uv run python app_launcher.py
   ```

2. 点击「启动无头模式」或 「虚拟显示模式」

3. API 服务将在后台运行，默认端口 `2048`

### 快速启动

`start_cmd.bat`：命令行直接启动。
```
 - --- 请选择启动模式 (未通过命令行参数指定) ---
  请输入启动模式 ([1] 无头模式, [2] 调试模式; 默认: 1 headless模式，15秒超时):
```

`start_webui.bat`：
启动前端界面，自动跳转或访问`http://127.0.0.1:9000`进行后续使用，推荐。

等待出现`ℹ️  INFO    | --- 队列 Worker 已启动 ---`后，即可开始使用API。


## 📡 API 使用

### OpenAI 兼容接口

服务启动后，可以使用 OpenAI 兼容的 API：

```bash
curl -X POST http://localhost:2048/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-pro",
    "messages": [
      {"role": "user", "content": "Hello, world!"}
    ]
  }'
```

### 客户端配置示例

以 Cherry Studio 为例：

1. 打开 Cherry Studio 设置
2. 在"连接"部分添加新模型:
   - **API 主机地址**: `http://127.0.0.1:2048/v1/`
   - **模型名称**: `gemini-2.5-pro` (或其他 AI Studio 支持的模型)
   - **API 密钥**: 留空或输入任意字符，如`123`

### Ollama 兼容层

项目还提供 Ollama 格式的 API 兼容：

```bash
# 启动 Ollama 兼容服务
uv run python gui_launcher.py
# 在 GUI 中点击"启动本地LLM模拟服务"

# 使用 Ollama 格式 API
curl http://localhost:11434/api/tags
curl -X POST http://localhost:11434/api/chat \
  -d '{"model": "gemini", "messages": [{"role": "user", "content": "Hello"}]}'
```

## 🏗️ 项目架构

```
AIStudio2API/
├── app_launcher.py          # 图形界面启动器
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

## 📅 开发计划

- **文档完善**: 更新并优化 `docs/` 目录下的详细使用文档与 API 规范
- **一键部署**: 提供 Windows/Linux/macOS 的全自动化安装与启动脚本
- **Docker 支持**: 提供标准 Dockerfile 及 Docker Compose 编排文件，简化部署流程
- **Go 语言重构**: 将核心代理服务迁移至 Go 以提升并发性能与降低资源占用
- **CI/CD 流水线**: 建立 GitHub Actions 自动化测试与构建发布流程
- **单元测试**: 增加核心模块（特别是浏览器自动化部分）的测试覆盖率
- **负载均衡**: 支持多 Google 账号轮询池，以提高并发限额与稳定性 (这项或许不可能实现)


<!-- 
## 📈 Star History

[![Star History Chart](https://api.star-history.com/svg?repos=Mag1cFall/AIStudio2API&type=Date)](https://star-history.com/#Mag1cFall/AIStudio2API&Date) -->