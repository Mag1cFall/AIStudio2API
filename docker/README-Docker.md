# Docker 部署指南 (AI Studio Proxy API)

> 📁 **注意**: 所有 Docker 相关文件现在都位于 `docker/` 目录中，保持项目根目录的整洁。

本文档提供了使用 Docker 构建和运行 AI Studio Proxy API 项目的完整指南，包括 Poetry 依赖管理、`.env` 配置管理和脚本注入功能。

## 🐳 概述

Docker 部署提供了以下优势：
- ✅ **环境隔离**: 容器化部署，避免环境冲突
- ✅ **Poetry 依赖管理**: 使用现代化的 Python 依赖管理工具
- ✅ **统一配置**: 基于 `.env` 文件的配置管理
- ✅ **版本更新无忧**: `bash update.sh` 即可完成更新
- ✅ **跨平台支持**: 支持 x86_64 和 ARM64 架构
- ✅ **配置持久化**: 认证文件和日志持久化存储
- ✅ **多阶段构建**: 优化镜像大小和构建速度

## 先决条件

*   **Docker**: 确保您的系统已正确安装并正在运行 Docker。您可以从 [Docker 官方网站](https://www.docker.com/get-started) 下载并安装 Docker Desktop (适用于 Windows 和 macOS) 或 Docker Engine (适用于 Linux)。
*   **项目代码**: 项目代码已下载到本地。
*   **认证文件**: 首次运行需要在主机上完成认证文件获取，Docker环境目前仅支持日常运行。

## 🔧 Docker 环境规格

*   **基础镜像**: Python 3.10-slim-bookworm (稳定且轻量)
*   **Python版本**: 3.10 (在容器内运行，与主机Python版本无关)
*   **依赖管理**: Poetry (现代化 Python 依赖管理)
*   **构建方式**: 多阶段构建 (builder + runtime)
*   **架构支持**: x86_64 和 ARM64 (Apple Silicon)
*   **模块化设计**: 完全支持项目的模块化架构
*   **虚拟环境**: Poetry 自动管理虚拟环境

## 1. 理解项目中的 Docker 相关文件

在项目根目录下，您会找到以下与 Docker 配置相关的文件：

*   **[`Dockerfile`](./Dockerfile:1):** 这是构建 Docker 镜像的蓝图。它定义了基础镜像、依赖项安装、代码复制、端口暴露以及容器启动时执行的命令。
*   **[`.dockerignore`](./.dockerignore:1):** 这个文件列出了在构建 Docker 镜像时应忽略的文件和目录。这有助于减小镜像大小并加快构建速度，例如排除 `.git` 目录、本地开发环境文件等。
*   **[`supervisord.conf`](./supervisord.conf:1):** (如果项目使用 Supervisor) Supervisor 是一个进程控制系统，它允许用户在类 UNIX 操作系统上监控和控制多个进程。此配置文件定义了 Supervisor 应如何管理应用程序的进程 (例如，主服务和流服务)。

## 2. 构建 Docker 镜像

要构建 Docker 镜像，请在项目根目录下打开终端或命令行界面，然后执行以下命令：

```bash
# 方法 1: 使用 docker compose (推荐)
cd docker
docker compose build

# 方法 2: 直接使用 docker build (在项目根目录执行)
docker build -f docker/Dockerfile -t ai-studio-proxy:latest .
```

**命令解释:**

*   `docker build`: 这是 Docker CLI 中用于构建镜像的命令。
*   `-t ai-studio-proxy:latest`: `-t` 参数用于为镜像指定一个名称和可选的标签 (tag)，格式为 `name:tag`。
    *   `ai-studio-proxy`: 是您为镜像选择的名称。
    *   `latest`: 是标签，通常表示这是该镜像的最新版本。您可以根据版本控制策略选择其他标签，例如 `ai-studio-proxy:1.0`。
*   `.`: (末尾的点号) 指定了 Docker 构建上下文的路径。构建上下文是指包含 [`Dockerfile`](./Dockerfile:1) 以及构建镜像所需的所有其他文件和目录的本地文件系统路径。点号表示当前目录。Docker 守护进程会访问此路径下的文件来执行构建。

构建过程可能需要一些时间，具体取决于您的网络速度和项目依赖项的多少。成功构建后，您可以使用 `docker images` 命令查看本地已有的镜像列表，其中应包含 `ai-studio-proxy:latest`。

## 3. 运行 Docker 容器

镜像构建完成后，您可以选择以下两种方式来运行容器：

### 方式 A: 使用 Docker Compose (推荐)

Docker Compose 提供了更简洁的配置管理方式，特别适合使用 `.env` 文件：

```bash
# 1. 准备配置文件 (进入 docker 目录)
cd docker
cp .env.docker .env
# 编辑 .env 文件以适应您的需求

# 2. 使用 Docker Compose 启动 (在 docker 目录下)
docker compose up -d

# 3. 查看日志
docker compose logs -f

# 4. 停止服务
docker compose down
```

### 方式 B: 使用 Docker 命令

您也可以使用传统的 Docker 命令来创建并运行容器：

### 方法 1: 使用 .env 文件 (推荐)

```bash
docker run -d \
    -p <宿主机_服务端口>:2048 \
    -p <宿主机_流端口>:3120 \
    -v "$(pwd)/../auth_profiles":/app/auth_profiles \
    -v "$(pwd)/.env":/app/.env \
    # 可选: 如果您想使用自己的 SSL/TLS 证书，请取消下面一行的注释。
    # 请确保宿主机上的 'certs/' 目录存在，并且其中包含应用程序所需的证书文件。
    # -v "$(pwd)/../certs":/app/certs \
    --name ai-studio-proxy-container \
    ai-studio-proxy:latest
```

### 方法 2: 使用环境变量 (传统方式)

```bash
docker run -d \
    -p <宿主机_服务端口>:2048 \
    -p <宿主机_流端口>:3120 \
    -v "$(pwd)/auth_profiles":/app/auth_profiles \
    # 可选: 如果您想使用自己的 SSL/TLS 证书，请取消下面一行的注释。
    # 请确保宿主机上的 'certs/' 目录存在，并且其中包含应用程序所需的证书文件。
    # -v "$(pwd)/certs":/app/certs \
    -e PORT=8000 \
    -e DEFAULT_FASTAPI_PORT=2048 \
    -e DEFAULT_CAMOUFOX_PORT=9222 \
    -e STREAM_PORT=3120 \
    -e SERVER_LOG_LEVEL=INFO \
    -e DEBUG_LOGS_ENABLED=false \
    -e AUTO_CONFIRM_LOGIN=true \
    # 可选: 如果您需要设置代理，请取消下面的注释
    # -e HTTP_PROXY="http://your_proxy_address:port" \
    # -e HTTPS_PROXY="http://your_proxy_address:port" \
    # -e UNIFIED_PROXY_CONFIG="http://your_proxy_address:port" \
    --name ai-studio-proxy-container \
    ai-studio-proxy:latest
```

**命令解释:**

*   `docker run`: 这是 Docker CLI 中用于从镜像创建并启动容器的命令。
*   `-d`: 以“分离模式”(detached mode) 运行容器。这意味着容器将在后台运行，您的终端提示符将立即可用，而不会被容器的日志输出占用。
*   `-p <宿主机_服务端口>:2048`: 端口映射 (Port mapping)。
    *   此参数将宿主机的某个端口映射到容器内部的 `2048` 端口。`2048` 是应用程序主服务在容器内监听的端口。
    *   您需要将 `<宿主机_服务端口>` 替换为您希望在宿主机上用于访问此服务的实际端口号 (例如，如果您想通过宿主机的 `8080` 端口访问服务，则使用 `-p 8080:2048`)。
*   `-p <宿主机_流端口>:3120`: 类似地，此参数将宿主机的某个端口映射到容器内部的 `3120` 端口，这是应用程序流服务在容器内监听的端口。
    *   您需要将 `<宿主机_流端口>` 替换为您希望在宿主机上用于访问流服务的实际端口号 (例如 `-p 8081:3120`)。
*   `-v "$(pwd)/auth_profiles":/app/auth_profiles`: 卷挂载 (Volume mounting)。
    *   此参数将宿主机当前工作目录 (`$(pwd)`) 下的 `auth_profiles/` 目录挂载到容器内的 `/app/auth_profiles/` 目录。
    *   这样做的好处是：
        *   **持久化数据:** 即使容器被删除，`auth_profiles/` 中的数据仍保留在宿主机上。
        *   **方便配置:** 您可以直接在宿主机上修改 `auth_profiles/` 中的文件，更改会实时反映到容器中 (取决于应用程序如何读取这些文件)。
    *   **重要:** 在运行命令前，请确保宿主机上的 `auth_profiles/` 目录已存在。如果应用程序期望在此目录中找到特定的配置文件，请提前准备好。
*   `# -v "$(pwd)/certs":/app/certs` (可选，已注释): 挂载自定义证书。
    *   如果您希望应用程序使用您自己的 SSL/TLS 证书而不是自动生成的证书，可以取消此行的注释。
    *   它会将宿主机当前工作目录下的 `certs/` 目录挂载到容器内的 `/app/certs/` 目录。
    *   **重要:** 如果启用此选项，请确保宿主机上的 `certs/` 目录存在，并且其中包含应用程序所需的证书文件 (通常是 `server.crt` 和 `server.key` 或类似名称的文件)。应用程序也需要被配置为从 `/app/certs/` 读取这些证书。
*   `-e SERVER_PORT=2048`: 设置环境变量。
    *   `-e` 参数用于在容器内设置环境变量。
    *   这里，我们将 `SERVER_PORT` 环境变量设置为 `2048`。应用程序在容器内会读取此变量来确定其主服务应监听哪个端口。这应与 [`Dockerfile`](./Dockerfile:1) 中 `EXPOSE` 指令以及 [`supervisord.conf`](./supervisord.conf:1) (如果使用) 中的配置相匹配。
*   `-e STREAM_PORT=3120`: 类似地，设置 `STREAM_PORT` 环境变量为 `3120`，供应用程序的流服务使用。
*   `# -e INTERNAL_CAMOUFOX_PROXY="http://your_proxy_address:port"` (可选，已注释): 设置内部 Camoufox 代理。
    *   如果您的应用程序需要通过一个特定的内部代理服务器来访问 Camoufox 或其他外部服务，可以取消此行的注释，并将 `"http://your_proxy_address:port"` 替换为实际的代理服务器地址和端口 (例如 `http://10.0.0.5:7890` 或 `socks5://proxy-user:proxy-pass@10.0.0.10:1080`)。
*   `--name ai-studio-proxy-container`: 为正在运行的容器指定一个名称。
    *   这使得管理容器更加方便。例如，您可以使用 `docker stop ai-studio-proxy-container` 来停止这个容器，或使用 `docker logs ai-studio-proxy-container` 来查看其日志。
    *   如果您不指定名称，Docker 会自动为容器生成一个随机名称。
*   `ai-studio-proxy:latest`: 指定要运行的镜像的名称和标签。这必须与您在 `docker build` 命令中使用的名称和标签相匹配。

**首次运行前的重要准备:**

### 配置文件准备

1. **创建 `.env` 配置文件 (推荐):**
   ```bash
   # 复制配置模板 (在项目 docker 目录下执行)
   cp .env.docker .env

   # 编辑配置文件
   nano .env  # 或使用其他编辑器
   ```

   **`.env` 文件的优势:**
   - ✅ **版本更新无忧**: 一个 `git pull` 就完成更新，无需重新配置
   - ✅ **配置集中管理**: 所有配置项统一在 `.env` 文件中
   - ✅ **Docker 兼容**: 容器会自动读取挂载的 `.env` 文件
   - ✅ **安全性**: `.env` 文件已被 `.gitignore` 忽略，不会泄露配置

2. **创建 `auth_profiles/` 目录:** 在项目根目录下 (与 [`Dockerfile`](./Dockerfile:1) 同级)，手动创建一个名为 `auth_profiles` 的目录。如果您的应用程序需要初始的认证配置文件，请将它们放入此目录中。

3. **(可选) 创建 `certs/` 目录:** 如果您计划使用自己的证书并取消了相关卷挂载行的注释，请在项目根目录下创建一个名为 `certs` 的目录，并将您的证书文件 (例如 `server.crt`, `server.key`) 放入其中。

## 4. 环境变量配置详解

### 使用 .env 文件配置 (推荐)

项目现在支持通过 `.env` 文件进行配置管理。在 Docker 环境中，您只需要将 `.env` 文件挂载到容器中即可：

```bash
# 挂载 .env 文件到容器
-v "$(pwd)/.env":/app/.env
```

### 常用配置项

以下是 Docker 环境中常用的配置项：

```env
# 服务端口配置
PORT=8000
DEFAULT_FASTAPI_PORT=2048
DEFAULT_CAMOUFOX_PORT=9222
STREAM_PORT=3120

# 代理配置
HTTP_PROXY=http://127.0.0.1:7890
HTTPS_PROXY=http://127.0.0.1:7890
UNIFIED_PROXY_CONFIG=http://127.0.0.1:7890

# 日志配置
SERVER_LOG_LEVEL=INFO
DEBUG_LOGS_ENABLED=false
TRACE_LOGS_ENABLED=false

# 认证配置
AUTO_CONFIRM_LOGIN=true
AUTO_SAVE_AUTH=false
AUTH_SAVE_TIMEOUT=30

# 脚本注入配置 v3.0 (重大升级)
ENABLE_SCRIPT_INJECTION=true
USERSCRIPT_PATH=browser_utils/more_modles.js
# 注意：MODEL_CONFIG_PATH 已废弃，现在直接从油猴脚本解析模型数据
# v3.0 使用 Playwright 原生网络拦截，100% 可靠

# API 默认参数
DEFAULT_TEMPERATURE=1.0
DEFAULT_MAX_OUTPUT_TOKENS=65536
DEFAULT_TOP_P=0.95
```

### 配置优先级

在 Docker 环境中，配置的优先级顺序为：

1. **Docker 运行时环境变量** (`-e` 参数) - 最高优先级
2. **挂载的 .env 文件** - 中等优先级
3. **Dockerfile 中的 ENV** - 最低优先级

### 示例：完整的 Docker 运行命令

```bash
# 使用 .env 文件的完整示例
docker run -d \
    -p 8080:2048 \
    -p 8081:3120 \
    -v "$(pwd)/../auth_profiles":/app/auth_profiles \
    -v "$(pwd)/.env":/app/.env \
    --name ai-studio-proxy-container \
    ai-studio-proxy:latest
```

## 5. 管理正在运行的容器

一旦容器启动，您可以使用以下 Docker 命令来管理它：

*   **查看正在运行的容器:**
    ```bash
    docker ps
    ```
    (如果您想查看所有容器，包括已停止的，请使用 `docker ps -a`)

*   **查看容器日志:**
    ```bash
    docker logs ai-studio-proxy-container
    ```
    (如果您想持续跟踪日志输出，可以使用 `-f` 参数: `docker logs -f ai-studio-proxy-container`)

*   **停止容器:**
    ```bash
    docker stop ai-studio-proxy-container
    ```

*   **启动已停止的容器:**
    ```bash
    docker start ai-studio-proxy-container
    ```

*   **重启容器:**
    ```bash
    docker restart ai-studio-proxy-container
    ```

*   **进入容器内部 (获取一个交互式 shell):**
    ```bash
    docker exec -it ai-studio-proxy-container /bin/bash
    ```
    (或者 `/bin/sh`，取决于容器基础镜像中可用的 shell。这对于调试非常有用。)

*   **删除容器:**
    首先需要停止容器，然后才能删除它。
    ```bash
    docker stop ai-studio-proxy-container
    docker rm ai-studio-proxy-container
    ```
    (如果您想强制删除正在运行的容器，可以使用 `docker rm -f ai-studio-proxy-container`，但不建议这样做，除非您知道自己在做什么。)

## 5. 更新应用程序

当您更新了应用程序代码并希望部署新版本时，通常需要执行以下步骤：

1.  **停止并删除旧的容器** (如果它正在使用相同的端口或名称)：
    ```bash
    docker stop ai-studio-proxy-container
    docker rm ai-studio-proxy-container
    ```
2.  **重新构建 Docker 镜像** (确保您在包含最新代码和 [`Dockerfile`](./Dockerfile:1) 的目录中)：
    ```bash
    docker build -t ai-studio-proxy:latest .
    ```
3.  **使用新的镜像运行新的容器** (使用与之前相同的 `docker run` 命令，或根据需要进行调整)：
    ```bash
    docker run -d \
        -p <宿主机_服务端口>:2048 \
        # ... (其他参数与之前相同) ...
        --name ai-studio-proxy-container \
        ai-studio-proxy:latest
    ```

## 6. 清理

*   **删除指定的 Docker 镜像:**
    ```bash
    docker rmi ai-studio-proxy:latest
    ```
    (注意：如果存在基于此镜像的容器，您需要先删除这些容器。)

*   **删除所有未使用的 (悬空) 镜像、容器、网络和卷:**
    ```bash
    docker system prune
    ```
    (如果想删除所有未使用的镜像，不仅仅是悬空的，可以使用 `docker system prune -a`)
    **警告:** `prune` 命令会删除数据，请谨慎使用。

希望本教程能帮助您成功地通过 Docker 部署和运行 AI Studio Proxy API 项目！

## 脚本注入配置 (v3.0 新功能) 🆕

### 概述

Docker 环境完全支持最新的脚本注入功能 v3.0，提供革命性的改进：

- **🚀 Playwright 原生拦截**: 使用 Playwright 路由拦截，100% 可靠性
- **🔄 双重保障机制**: 网络拦截 + 脚本注入，确保万无一失
- **📝 直接脚本解析**: 从油猴脚本中自动解析模型列表，无需配置文件
- **🔗 前后端同步**: 前端和后端使用相同的模型数据源，100%一致
- **⚙️ 零配置维护**: 无需手动维护模型配置文件，脚本更新自动生效
- **🔄 自动适配**: 油猴脚本更新时无需手动更新配置

### 配置选项

在 `.env` 文件中配置以下选项：

```env
# 是否启用脚本注入功能
ENABLE_SCRIPT_INJECTION=true

# 油猴脚本文件路径（容器内路径）
# 模型数据直接从此脚本文件中解析，无需额外配置文件
USERSCRIPT_PATH=browser_utils/more_modles.js
```

### 自定义脚本和模型配置

如果您想使用自定义的脚本或模型配置：

1. **自定义脚本配置**：
   ```bash
   # 在主机上创建自定义脚本文件
   cp browser_utils/more_modles.js browser_utils/my_script.js
   # 编辑 my_script.js 中的 MODELS_TO_INJECT 数组

   # 在 docker-compose.yml 中取消注释并修改挂载行：
   # - ../browser_utils/my_script.js:/app/browser_utils/more_modles.js:ro

   # 或者在 .env 中修改路径：
   # USERSCRIPT_PATH=browser_utils/my_script.js
   ```

2. **自定义脚本**：
   ```bash
   # 将自定义脚本放在 browser_utils/ 目录
   cp your_custom_script.js browser_utils/custom_script.js

   # 在 .env 中修改路径：
   # USERSCRIPT_PATH=browser_utils/custom_script.js
   ```

### Docker Compose 挂载配置

在 `docker-compose.yml` 中，您可以取消注释以下行来挂载自定义文件：

```yaml
volumes:
  # 挂载自定义模型配置
  - ../browser_utils/model_configs.json:/app/browser_utils/model_configs.json:ro
  # 挂载自定义脚本目录
  - ../browser_utils/custom_scripts:/app/browser_utils/custom_scripts:ro
```

### 注意事项

- 脚本或配置文件更新后需要重启容器
- 如果脚本注入失败，不会影响主要功能
- 可以通过容器日志查看脚本注入状态

## 注意事项

1. **认证文件**: Docker 部署需要预先在主机上获取有效的认证文件，并将其放置在 `auth_profiles/active/` 目录中。
2. **模块化架构**: 项目采用模块化设计，所有配置和代码都已经过优化，无需手动修改。
3. **端口配置**: 确保宿主机上的端口未被占用，默认使用 2048 (主服务) 和 3120 (流式代理)。
4. **日志查看**: 可以通过 `docker logs` 命令查看容器运行日志，便于调试和监控。
5. **脚本注入**: 新增的脚本注入功能默认启用，可通过 `ENABLE_SCRIPT_INJECTION=false` 禁用。

## 配置管理总结 ⭐

### 新功能：统一的 .env 配置

现在 Docker 部署完全支持 `.env` 文件配置管理：

✅ **统一配置**: 使用 `.env` 文件管理所有配置
✅ **版本更新无忧**: `git pull` + `docker compose up -d` 即可完成更新
✅ **配置隔离**: 开发、测试、生产环境可使用不同的 `.env` 文件
✅ **安全性**: `.env` 文件不会被提交到版本控制

### 推荐的 Docker 工作流程

```bash
# 1. 初始设置
git clone <repository>
cd <project>/docker
cp .env.docker .env
# 编辑 .env 文件

# 2. 启动服务
docker compose up -d

# 3. 版本更新
bash update.sh

# 4. 查看状态
docker compose ps
docker compose logs -f
```

### 配置文件说明

- **`.env`**: 您的实际配置文件 (从 `.env.docker` 复制并修改)
- **`.env.docker`**: Docker 环境的配置模板
- **`.env.example`**: 通用配置模板 (适用于所有环境)
- **`docker-compose.yml`**: Docker Compose 配置文件

这样的配置管理方式确保了 Docker 部署与本地开发的一致性，同时简化了配置和更新流程。
