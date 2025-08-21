# 开发者指南

本文档面向希望参与项目开发、贡献代码或深度定制功能的开发者。

## 🛠️ 开发环境设置

### 前置要求

- **Python**: >=3.9, <4.0 (推荐 3.10+ 以获得最佳性能)
- **Poetry**: 现代化 Python 依赖管理工具
- **Node.js**: >=16.0 (用于 Pyright 类型检查，可选)
- **Git**: 版本控制

### 快速开始

```bash
# 1. 克隆项目
git clone https://github.com/Mag1cFall/Gemini-AIStudio2API.git
cd Gemini-AIStudio2API

# 2. 安装 Poetry (如果尚未安装)
curl -sSL https://install.python-poetry.org | python3 -

# 3. 安装项目依赖 (包括开发依赖)
poetry install --with dev

# 4. 激活虚拟环境
poetry env activate

# 5. 安装 Pyright (可选，用于类型检查)
npm install -g pyright
```

## 📁 项目结构

```
Gemini-AIStudio2API/
├── api_utils/              # FastAPI 应用核心模块
│   ├── app.py             # FastAPI 应用入口
│   ├── routes.py          # API 路由定义
│   ├── request_processor.py # 请求处理逻辑
│   ├── queue_worker.py    # 队列工作器
│   └── auth_utils.py      # 认证工具
├── browser_utils/          # 浏览器自动化模块
│   ├── page_controller.py # 页面控制器
│   ├── model_management.py # 模型管理
│   ├── script_manager.py  # 脚本注入管理
│   └── operations.py      # 浏览器操作
├── config/                 # 配置管理模块
│   ├── settings.py        # 主要设置
│   ├── constants.py       # 常量定义
│   ├── timeouts.py        # 超时配置
│   └── selectors.py       # CSS 选择器
├── models/                 # 数据模型
│   ├── chat.py           # 聊天相关模型
│   ├── exceptions.py     # 异常定义
│   └── logging.py        # 日志模型
├── stream/                 # 流式代理服务
│   ├── main.py           # 代理服务入口
│   ├── proxy_server.py   # 代理服务器
│   └── interceptors.py   # 请求拦截器
├── logging_utils/          # 日志工具
├── docs/                   # 文档目录
├── docker/                 # Docker 相关文件
├── pyproject.toml         # Poetry 配置文件
├── pyrightconfig.json     # Pyright 类型检查配置
├── .env.example           # 环境变量模板
└── README.md              # 项目说明
```

## 🔧 依赖管理 (Poetry)

### Poetry 基础命令

```bash
# 查看项目信息
poetry show

# 查看依赖树
poetry show --tree

# 添加新依赖
poetry add package_name

# 添加开发依赖
poetry add --group dev package_name

# 移除依赖
poetry remove package_name

# 更新依赖
poetry update

# 更新特定依赖
poetry update package_name

# 导出 requirements.txt (兼容性)
poetry export -f requirements.txt --output requirements.txt
```

### 依赖分组

项目使用 Poetry 的依赖分组功能：

```toml
[tool.poetry.dependencies]
# 生产依赖
python = ">=3.9,<4.0"
fastapi = "==0.115.12"
# ... 其他生产依赖

[tool.poetry.group.dev.dependencies]
# 开发依赖 (可选安装)
pytest = "^7.0.0"
black = "^23.0.0"
isort = "^5.12.0"
```

### 虚拟环境管理

```bash
# 查看虚拟环境信息
poetry env info

# 查看虚拟环境路径
poetry env info --path

# 激活虚拟环境
poetry env activate

# 在虚拟环境中运行命令
poetry run python script.py

# 删除虚拟环境
poetry env remove python
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
        "./api_utils",
        "./browser_utils",
        "./config",
        "./models",
        "./logging_utils",
        "./stream"
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
pyright api_utils/app.py

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
poetry run pytest

# 运行特定测试文件
poetry run pytest tests/test_api.py

# 运行测试并生成覆盖率报告
poetry run pytest --cov=api_utils --cov-report=html
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
poetry run black .

# 使用 isort 整理导入
poetry run isort .

# 检查代码风格
poetry run flake8 .
```

### 2. 类型检查

```bash
# 运行类型检查
pyright

# 或使用 mypy (如果安装)
poetry run mypy .
```

### 3. 测试

```bash
# 运行测试
poetry run pytest

# 运行测试并检查覆盖率
poetry run pytest --cov
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
poetry build

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

- [Poetry 官方文档](https://python-poetry.org/docs/)
- [Pyright 官方文档](https://github.com/microsoft/pyright)
- [FastAPI 官方文档](https://fastapi.tiangolo.com/)
- [Playwright 官方文档](https://playwright.dev/python/)
