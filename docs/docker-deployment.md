# Docker 部署指南

本文档详细介绍如何使用 Docker 和 Docker Compose 部署 AI Studio Proxy API。

## 📋 前置条件

1.  **安装 Docker**: 确保已安装 Docker 和 Docker Compose。
2.  **获取认证文件 (关键)**: Docker 容器默认运行在无头模式，无法进行交互式登录。您**必须**先在有图形界面的本地电脑上生成认证文件。

## 🛠️ 部署步骤

### 第一步：准备认证文件

1.  **在本地电脑上**（Windows/macOS/Linux 桌面版），按照 [安装指南](installation-guide.md) 配置环境。
2.  运行 `start_webui.bat` (Windows) 或 `uv run python src/app_launcher.py` (Mac/Linux)。
3.  在 Web 界面点击 "调试模式 (Debug)" -> "启动服务"。
4.  在弹出的浏览器中完成 Google 登录。
5.  登录成功后，在终端按回车保存认证文件。
6.  在 Web 界面的 "认证文件" 页面，激活刚刚保存的文件。
7.  **检查文件**: 确保 `data/auth_profiles/active/` 文件夹中有一个 `.json` 文件（例如 `auth_state_xxx.json`）。

### 第二步：配置环境

1.  确保项目根目录有 `.env` 文件（复制 `.env.example`）。
2.  建议配置：
    ```env
    # 容器内监听端口 (保持默认)
    DEFAULT_FASTAPI_PORT=2048
    STREAM_PORT=3120
    
    # 启动模式 (Docker内推荐使用 virtual_headless)
    LAUNCH_MODE=virtual_headless
    
    # 日志级别
    SERVER_LOG_LEVEL=INFO
    ```

### 第三步：启动 Docker 容器

在 `docker/` 目录下执行：

```bash
# 进入 docker 目录
cd docker

# 构建并后台启动
docker-compose up -d

# 查看日志
docker-compose logs -f
```

### 第四步：验证服务

访问以下地址检查服务状态：
*   API 服务: `http://localhost:2048/health`
*   模型列表: `http://localhost:2048/v1/models`

如果返回正常的 JSON 数据，说明部署成功。

## 📂 数据持久化

`docker-compose.yml` 已经配置了以下卷挂载，确保数据不会因容器重启而丢失：

*   `./data/auth_profiles`: 存放认证文件 (**重要**)
*   `./logs`: 存放运行日志
*   `./data/certs`: 存放流式代理生成的证书
*   `./.env`: 挂载配置文件

## ❓ 常见问题

### 1. 容器启动后报错 "Authentication failed" 或 "Login required"
*   **原因**: `data/auth_profiles/active/` 目录下没有有效的 `.json` 认证文件，或者文件已过期。
*   **解决**: 重新在本地执行"第一步"，生成新的认证文件，并确保它位于 `data/auth_profiles/active/` 目录下，然后重启容器。

### 2. 端口冲突
*   默认占用 `2048` 和 `3120` 端口。
*   如果冲突，请修改 `docker-compose.yml` 中的 `ports` 映射，例如 `"8080:2048"`。

### 3. 无法连接到 Google
*   如果您的网络环境需要代理才能访问 Google，请在 `.env` 文件中配置 `UNIFIED_PROXY_CONFIG`，Docker 会自动读取该配置。
    ```env
    UNIFIED_PROXY_CONFIG=http://host.docker.internal:7890
    ```
    *(注: `host.docker.internal` 用于访问宿主机的代理服务，Linux下可能需要额外配置)*