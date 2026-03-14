# 多 Worker 并发模式

多 Worker 模式允许您使用多个 Google 账号同时处理 API 请求，显著提升系统吞吐量和稳定性。

## 概述

在多 Worker 模式下：
- 每个 Worker 对应一个独立的 Google 账号（认证文件）
- Gateway 网关自动将请求分发到可用的 Worker
- 所有 Worker 共享同一个外部 API 端口（默认 2048）
- 支持流式响应和完整的思考过程透传

## 快速开始

### 1. 添加多个认证文件

首先需要为每个 Google 账号创建认证文件：

1. 启动有头模式，完成第一个账号的登录认证
2. 认证文件自动保存到 `data/auth_profiles/saved/` 目录
3. 重复上述步骤为其他账号创建认证文件

### 2. 配置 Workers

1. 打开 Web 管理界面 `http://127.0.0.1:9000`
2. 进入「认证文件」标签页（现在称为「Worker 管理」）
3. 从已保存的认证文件列表中，点击「添加为 Worker」
4. 确认 Worker 列表中显示所有要使用的账号
5. **点击「保存配置」按钮**保存 Worker 池配置

### 3. 启用 Worker 模式

1. 在 Worker 管理页面，开启「Worker 模式」开关
2. 点击「保存配置」
3. 返回主页，点击「启动服务」

### 4. 使用 API

与单 Worker 模式完全相同，所有请求发送到同一端口：

```bash
curl -X POST http://localhost:2048/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-pro",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

## 架构说明

```
客户端请求
    ↓
Gateway (端口 2048)
    ↓ 轮询分发
+----+----+----+----+
| W1 | W2 | W3 | W4 |  (各自独立端口 3001, 3002, ...)
+----+----+----+----+
    ↓
Google AI Studio
```

## 配置文件

Worker 配置保存在 `data/workers.json`：

```json
{
  "workers": [
    {"id": "w1", "profile": "account1.json", "port": 3001, "camoufox_port": 9223},
    {"id": "w2", "profile": "account2.json", "port": 3002, "camoufox_port": 9224}
  ],
  "settings": {"recovery_hours": 6}
}
```

## 日志过滤

多 Worker 模式下日志量较大，可使用主页的日志过滤功能：

- **来源筛选**: 选择 Gateway / Worker-w1 / Worker-w2 等单独查看

## 注意事项

1. **端口分配**: 每个 Worker 自动分配独立端口，避免冲突
2. **流式代理端口**: Worker-w1 使用 3120，Worker-w2 使用 3121，以此类推
3. **账号安全**: 确保每个账号的认证文件独立，不要共用
4. **资源占用**: 每个 Worker 运行独立的浏览器实例，注意内存占用

## 常见问题

### Q: 为什么某些请求没有响应？
检查目标 Worker 的日志，确认浏览器实例正常运行。

### Q: 如何扩展更多 Worker？
添加更多认证文件，然后在 Worker 管理中添加即可。重启服务生效。

### Q: 认证文件过期怎么办？
需要重新进入有头模式登录对应账号，更新认证文件。
