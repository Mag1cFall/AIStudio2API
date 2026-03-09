# 开发者指南

本文档面向希望参与项目开发、贡献代码或深度定制功能的开发者。

## 🛠️ 开发环境设置

### 前置要求

- **Python**: >=3.9, <4.0 (推荐 3.12 以获得最佳性能)
- **uv**: 极速的现代化 Python 依赖管理工具
- **Node.js**: >=16.0 (用于 Pyright 类型检查，可选)
- **Git**: 版本控制

### 快速开始

```bash
# 1. 克隆项目
git clone https://github.com/Mag1cFall/AIStudio2API.git
cd AIStudio2API

# 2. 安装 uv (如果尚未安装)
# macOS/Linux: curl -LsSf https://astral.sh/uv/install.sh | sh
# Windows: powershell -c "irm https://astral.sh/uv/install.ps1 | iex"

# 3. 同步项目依赖 (uv 会自动创建 .venv 并安装依赖)
uv sync

# 4. 验证环境
uv run python --version

# 5. 安装 Pyright (可选，用于类型检查)
npm install -g pyright
```

## 📁 项目结构

```
AIStudio2API/
├── src/                        # 源码目录
│   ├── api/                    # FastAPI 应用核心模块
│   │   ├── app.py              # FastAPI 应用入口
│   │   ├── routes.py           # API 路由定义
│   │   ├── request_processor.py# 请求处理逻辑
│   │   ├── queue_worker.py     # 队列工作器
│   │   └── auth_utils.py       # 认证工具
│   ├── browser/                # 浏览器自动化模块
│   │   ├── page_controller.py  # 页面控制器
│   │   ├── model_management.py # 模型管理
│   │   ├── script_manager.py   # 脚本注入管理
│   │   └── operations.py       # 浏览器操作
│   ├── config/                 # 配置管理模块
│   │   ├── settings.py         # 主要设置
│   │   ├── constants.py        # 常量定义
│   │   ├── timeouts.py         # 超时配置
│   │   └── selectors.py        # CSS 选择器
│   ├── models/                 # 数据模型
│   │   ├── types.py            # 聊天/异常模型
│   │   └── websocket.py        # WebSocket日志模型
│   ├── tts/                    # TTS 语音生成模块
│   │   ├── __init__.py         # 模块初始化
│   │   ├── models.py           # TTS 数据模型
│   │   ├── tts_controller.py   # TTS 页面控制器
│   │   └── tts_processor.py    # TTS 请求处理器
│   ├── media/                  # 媒体生成模块
│   │   ├── __init__.py         # 模块初始化
│   │   ├── models.py           # 媒体数据模型
│   │   ├── nano_controller.py  # Nano Banana 控制器
│   │   ├── imagen_controller.py# Imagen 控制器
│   │   ├── veo_controller.py   # Veo 控制器
│   │   └── media_processor.py  # 媒体请求处理器
│   ├── proxy/                  # 流式代理服务
│   │   ├── runner.py           # 代理服务入口
│   │   ├── server.py           # 代理服务器
│   │   ├── handler.py          # 请求处理器
│   │   └── connection.py       # 连接/证书管理
│   ├── worker/                 # 多Worker管理模块
│   │   ├── models.py           # Worker数据模型
│   │   └── pool.py             # Worker池管理
│   ├── logger/                 # 日志工具
│   │   └── config.py           # 日志配置
│   ├── static/                 # 静态资源
│   ├── app_launcher.py         # GUI 启动器
│   ├── launch_camoufox.py      # 命令行启动器
│   ├── manager/                # WebUI 管理器包
│   ├── gateway.py              # 多Worker负载均衡网关
│   └── server.py               # 主服务器
├── data/                       # 运行时数据目录
│   ├── auth_profiles/          # 认证文件存储
│   ├── certs/                  # 代理证书
│   └── key.txt                 # API 密钥
├── camoufox/                   # Camoufox 脚本
├── docker/                     # Docker 相关文件
├── docs/                       # 文档目录
├── logs/                       # 日志目录
├── pyproject.toml              # uv/hatch 配置文件
├── pyrightconfig.json          # Pyright 类型检查配置
├── .env.example                # 环境变量模板
└── README.md                   # 项目说明
```

## 🔧 依赖管理 (uv)

### uv 基础命令

```bash
# 同步依赖（安装所有 pyproject.toml 中的包）
uv sync

# 查看依赖树
uv tree

# 添加新依赖
uv add package_name

# 添加开发依赖
uv add --dev package_name

# 移除依赖
uv remove package_name

# 运行命令（自动使用虚拟环境）
uv run python script.py
```

### 依赖分组

项目使用标准 `pyproject.toml` 的依赖分组功能（uv 支持）：

```toml
[project]
dependencies = [
    "fastapi==0.115.12",
    # ... 其他生产依赖
]

[dependency-groups]
dev = [
    "pytest>=7.0.0",
    "black>=23.0.0",
    "isort>=5.12.0"
]
```

### 虚拟环境管理

uv 默认在项目根目录下管理 `.venv`。

```bash
# 显式创建虚拟环境（通常 uv sync 会自动做）
uv venv

# 在虚拟环境中运行命令
uv run python script.py
```

## 🔍 类型检查 (Pyright)

### Pyright 配置

项目使用 `pyrightconfig.json` 进行类型检查配置：

```json
{
    "pythonVersion": "3.13",
    "pythonPlatform": "Darwin",
    "typeCheckingMode": "off",
    "extraPaths": [
        "./api",
        "./browser",
        "./config",
        "./models",
        "./logger",
        "./proxy"
    ]
}
```

### 使用 Pyright

```bash
# 安装 Pyright
npm install -g pyright

# 检查整个项目
pyright

# 检查特定文件
pyright api/app.py

# 监视模式 (文件变化时自动检查)
pyright --watch
```

### 类型注解最佳实践

```python
from typing import Optional, List, Dict, Any
from pydantic import BaseModel

# 函数类型注解
def process_request(data: Dict[str, Any]) -> Optional[str]:
    """处理请求数据"""
    return data.get("message")

# 类型别名
ModelConfig = Dict[str, Any]
ResponseData = Dict[str, str]

# Pydantic 模型
class ChatRequest(BaseModel):
    message: str
    model: Optional[str] = None
    temperature: float = 0.7
```

## 🧪 测试

### 运行测试

```bash
# 运行所有测试
uv run pytest

# 运行特定测试文件
uv run pytest tests/test_api.py

# 运行测试并生成覆盖率报告
uv run pytest --cov=api --cov-report=html
```

### 测试结构

```
tests/
├── conftest.py           # 测试配置
├── test_api.py          # API 测试
├── test_browser.py      # 浏览器功能测试
└── test_config.py       # 配置测试
```

## 🔄 开发工作流程

### 1. 代码格式化

```bash
# 使用 Black 格式化代码
uv run black .

# 使用 isort 整理导入
uv run isort .

# 检查代码风格
uv run flake8 .
```

### 2. 类型检查

```bash
# 运行类型检查
pyright

# 或使用 mypy (如果安装)
uv run mypy .
```

### 3. 测试

```bash
# 运行测试
uv run pytest

# 运行测试并检查覆盖率
uv run pytest --cov
```

### 4. 提交代码

```bash
# 添加文件
git add .

# 提交 (建议使用规范的提交信息)
git commit -m "feat: 添加新功能"

# 推送
git push origin feature-branch
```

## 📝 代码规范

### 命名规范

- **文件名**: 使用下划线分隔 (`snake_case`)
- **类名**: 使用驼峰命名 (`PascalCase`)
- **函数名**: 使用下划线分隔 (`snake_case`)
- **常量**: 使用大写字母和下划线 (`UPPER_CASE`)

### 文档字符串

```python
def process_chat_request(request: ChatRequest) -> ChatResponse:
    """
    处理聊天请求
    
    Args:
        request: 聊天请求对象
        
    Returns:
        ChatResponse: 聊天响应对象
        
    Raises:
        ValidationError: 当请求数据无效时
        ProcessingError: 当处理失败时
    """
    pass
```

## 🚀 部署和发布

### 构建项目

```bash
# 构建分发包
uv build

# 检查构建结果
ls dist/
```

### Docker 开发

```bash
# 构建开发镜像
docker build -f docker/Dockerfile.dev -t aistudio-dev .

# 运行开发容器
docker run -it --rm -v $(pwd):/app aistudio-dev bash
```

## 🤝 贡献指南

### 提交 Pull Request

1. Fork 项目
2. 创建功能分支: `git checkout -b feature/amazing-feature`
3. 提交更改: `git commit -m 'feat: 添加惊人的功能'`
4. 推送分支: `git push origin feature/amazing-feature`
5. 创建 Pull Request

### 代码审查清单

- [ ] 代码遵循项目规范
- [ ] 添加了必要的测试
- [ ] 测试通过
- [ ] 类型检查通过
- [ ] 文档已更新
- [ ] 变更日志已更新

## 📞 获取帮助

- **GitHub Issues**: 报告 Bug 或请求功能
- **GitHub Discussions**: 技术讨论和问答
- **开发者文档**: 查看详细的 API 文档

## 🔗 相关资源

- [uv 官方文档](https://github.com/astral-sh/uv)
- [Pyright 官方文档](https://github.com/microsoft/pyright)
- [FastAPI 官方文档](https://fastapi.tiangolo.com/)
- [Playwright 官方文档](https://playwright.dev/python/)
