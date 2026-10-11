# ==========================================
# Stage 1: 构建前端静态资源 (Vue + Vite)
# ==========================================
FROM node:22-alpine AS frontend-builder

WORKDIR /app

# 优先拷贝 package 描述文件以利用缓存
COPY web/package*.json ./web/
RUN cd web && npm ci

# 拷贝前端源码并执行编译，产物输出至 internal/webui/dist
COPY web/ ./web/
RUN cd web && npm run build

# ==========================================
# Stage 2: 构建 Go 二进制程序
# ==========================================
FROM golang:bookworm AS builder

WORKDIR /app

# 优先下载 Go 依赖模块
COPY go.mod go.sum ./
RUN go mod download

# 拷贝后端源码
COPY . .

# 从 Stage 1 复制前端静态资源产物
COPY --from=frontend-builder /app/internal/webui/dist ./internal/webui/dist

# 静态编译可执行文件
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app/aistudio2api ./cmd/aistudio2api

# ==========================================
# Stage 3: 最小运行时环境 (Debian Slim)
# ==========================================
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

# 安装基础证书、时区以及 Camoufox/Firefox 运行依赖库
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    tzdata \
    libgtk-3-0 \
    libasound2 \
    libnss3 \
    libdbus-glib-1-2 \
    libxtst6 \
    libxrandr2 \
    libgbm1 \
    libxkbcommon0 \
    libpango-1.0-0 \
    libcairo2 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libx11-xcb1 \
    fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# 拷贝二进制程序到系统路径并软链接至当前目录
COPY --from=builder /app/aistudio2api /usr/local/bin/aistudio2api
RUN ln -s /usr/local/bin/aistudio2api /app/aistudio2api

# 预创建持久化目录
RUN mkdir -p /app/auth /app/runtime

EXPOSE 2048

ENTRYPOINT ["aistudio2api"]
