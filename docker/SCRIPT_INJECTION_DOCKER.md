# Docker 环境脚本注入配置指南

## 概述

本指南专门针对 Docker 环境中的油猴脚本注入功能配置。

## 快速开始

### 1. 基础配置

```bash
# 进入 docker 目录
cd docker

# 复制配置模板
cp .env.docker .env

# 编辑配置文件
nano .env
```

在 `.env` 文件中确保以下配置：

```env
# 启用脚本注入
ENABLE_SCRIPT_INJECTION=true

# 使用默认脚本（模型数据直接从脚本解析）
USERSCRIPT_PATH=browser_utils/more_modles.js
```

### 2. 启动容器

```bash
# 构建并启动
docker compose up -d

# 查看日志确认脚本注入状态
docker compose logs -f | grep "脚本注入"
```

## 自定义配置

### 方法 1: 直接替换脚本文件

```bash
# 1. 创建自定义油猴脚本
cp ../browser_utils/more_modles.js ../browser_utils/my_custom_script.js

# 2. 编辑脚本文件中的 MODELS_TO_INJECT 数组
nano ../browser_utils/my_custom_script.js

# 3. 重启容器
docker compose restart
```

### 方法 2: 挂载自定义脚本

```bash
# 1. 创建自定义脚本文件
cp ../browser_utils/more_modles.js ../browser_utils/my_script.js

# 2. 编辑 docker-compose.yml，取消注释并修改：
# volumes:
#   - ../browser_utils/my_script.js:/app/browser_utils/more_modles.js:ro

# 3. 重启服务
docker compose down
docker compose up -d
```

### 方法 3: 环境变量配置

```bash
# 1. 在 .env 文件中修改路径
echo "USERSCRIPT_PATH=browser_utils/my_custom_script.js" >> .env

# 2. 创建对应的脚本文件
cp ../browser_utils/more_modles.js ../browser_utils/my_custom_script.js

# 3. 重启容器
docker compose restart
```

## 验证脚本注入

### 检查日志

```bash
# 查看脚本注入相关日志
docker compose logs | grep -E "(脚本注入|script.*inject|模型增强)"

# 实时监控日志
docker compose logs -f | grep -E "(脚本注入|script.*inject|模型增强)"
```

### 预期日志输出

成功的脚本注入应该显示类似以下日志：

```
设置网络拦截和脚本注入...
成功设置模型列表网络拦截
成功解析 6 个模型从油猴脚本
添加了 6 个注入的模型到API模型列表
✅ 脚本注入成功，模型显示效果与油猴脚本100%一致
   解析的模型: 👑 Kingfall, ✨ Gemini 2.5 Pro, 🦁 Goldmane...
```

### 进入容器检查

```bash
# 进入容器
docker compose exec ai-studio-proxy /bin/bash

# 检查脚本文件
cat /app/browser_utils/more_modles.js

# 检查脚本文件列表
ls -la /app/browser_utils/*.js

# 退出容器
exit
```

## 故障排除

### 脚本注入失败

1. **检查配置文件路径**：
   ```bash
   docker compose exec ai-studio-proxy ls -la /app/browser_utils/
   ```

2. **检查文件权限**：
   ```bash
   docker compose exec ai-studio-proxy cat /app/browser_utils/more_modles.js
   ```

3. **查看详细错误日志**：
   ```bash
   docker compose logs | grep -A 5 -B 5 "脚本注入"
   ```

### 脚本文件无效

1. **验证 JavaScript 格式**：
   ```bash
   # 在主机上验证 JavaScript 语法
   node -c browser_utils/more_modles.js
   ```

2. **检查必需字段**：
   确保每个模型都有 `name` 和 `displayName` 字段。

### 禁用脚本注入

如果遇到问题，可以临时禁用：

```bash
# 在 .env 文件中设置
echo "ENABLE_SCRIPT_INJECTION=false" >> .env

# 重启容器
docker compose restart
```

## 高级配置

### 使用自定义脚本

```bash
# 1. 将自定义脚本放在 browser_utils/ 目录
cp your_custom_script.js ../browser_utils/custom_injector.js

# 2. 在 .env 中修改脚本路径
echo "USERSCRIPT_PATH=browser_utils/custom_injector.js" >> .env

# 3. 重启容器
docker compose restart
```

### 多环境配置

```bash
# 开发环境
cp .env.docker .env.dev
# 编辑 .env.dev

# 生产环境
cp .env.docker .env.prod
# 编辑 .env.prod

# 使用特定环境启动
cp .env.prod .env
docker compose up -d
```

## 注意事项

1. **文件挂载**: 确保主机上的文件路径正确
2. **权限问题**: Docker 容器内的文件权限可能需要调整
3. **重启生效**: 配置更改后需要重启容器
4. **日志监控**: 通过日志确认脚本注入状态
5. **备份配置**: 建议备份工作的配置文件

## 示例配置文件

参考 `model_configs_docker_example.json` 文件了解完整的配置格式和选项。
