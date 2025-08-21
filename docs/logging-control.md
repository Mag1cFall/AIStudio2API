# 日志控制指南

本文档介绍如何控制项目的日志输出详细程度和行为。

## 日志系统概述

项目包含两个主要的日志系统：

1. **启动器日志** (`launch_camoufox.py`)
2. **主服务器日志** (`server.py`)

## 启动器日志控制

### 日志文件位置

- 文件路径: `logs/launch_app.log`
- 日志级别: 通常为 `INFO`
- 内容: 启动和协调过程，以及内部启动的 Camoufox 进程的输出

### 配置方式

启动器的日志级别在脚本内部通过 `setup_launcher_logging(log_level=logging.INFO)` 设置。

## 主服务器日志控制

### 日志文件位置

- 文件路径: `logs/app.log`
- 配置模块: `logging_utils/setup.py`
- 内容: FastAPI 服务器详细运行日志

### 环境变量控制

主服务器日志主要通过**环境变量**控制，这些环境变量由 `launch_camoufox.py` 在启动主服务器之前设置：

#### SERVER_LOG_LEVEL

控制主服务器日志记录器 (`AIStudioProxyServer`) 的级别。

- **默认值**: `INFO`
- **可选值**: `DEBUG`, `INFO`, `WARNING`, `ERROR`, `CRITICAL`

**使用示例**:

```bash
# Linux/macOS
export SERVER_LOG_LEVEL=DEBUG
python launch_camoufox.py --headless

# Windows (cmd)
set SERVER_LOG_LEVEL=DEBUG
python launch_camoufox.py --headless

# Windows (PowerShell)
$env:SERVER_LOG_LEVEL="DEBUG"
python launch_camoufox.py --headless
```

#### SERVER_REDIRECT_PRINT

控制主服务器内部的 `print()` 和 `input()` 行为。

- **`'true'`**: `print()` 输出重定向到日志系统，`input()` 可能无响应（无头模式默认）
- **`'false'`**: `print()` 输出到原始终端，`input()` 在终端等待用户输入（调试模式默认）

#### DEBUG_LOGS_ENABLED

控制主服务器内部特定功能的详细调试日志点是否激活。

- **默认值**: `false`
- **可选值**: `true`, `false`

**使用示例**:

```bash
# Linux/macOS
export DEBUG_LOGS_ENABLED=true
python launch_camoufox.py --headless

# Windows (cmd)
set DEBUG_LOGS_ENABLED=true
python launch_camoufox.py --headless

# Windows (PowerShell)
$env:DEBUG_LOGS_ENABLED="true"
python launch_camoufox.py --headless
```

#### TRACE_LOGS_ENABLED

控制更深层次的跟踪日志。

- **默认值**: `false`
- **可选值**: `true`, `false`
- **注意**: 通常不需要启用，除非进行深度调试

**使用示例**:

```bash
# Linux/macOS
export TRACE_LOGS_ENABLED=true
python launch_camoufox.py --headless

# Windows (cmd)
set TRACE_LOGS_ENABLED=true
python launch_camoufox.py --headless

# Windows (PowerShell)
$env:TRACE_LOGS_ENABLED="true"
python launch_camoufox.py --headless
```

## 组合使用示例

### 启用详细调试日志

```bash
# Linux/macOS
export SERVER_LOG_LEVEL=DEBUG
export DEBUG_LOGS_ENABLED=true
python launch_camoufox.py --headless --server-port 2048

# Windows (PowerShell)
$env:SERVER_LOG_LEVEL="DEBUG"
$env:DEBUG_LOGS_ENABLED="true"
python launch_camoufox.py --headless --server-port 2048
```

### 启用最详细的跟踪日志

```bash
# Linux/macOS
export SERVER_LOG_LEVEL=DEBUG
export DEBUG_LOGS_ENABLED=true
export TRACE_LOGS_ENABLED=true
python launch_camoufox.py --headless

# Windows (PowerShell)
$env:SERVER_LOG_LEVEL="DEBUG"
$env:DEBUG_LOGS_ENABLED="true"
$env:TRACE_LOGS_ENABLED="true"
python launch_camoufox.py --headless
```

## 日志查看方式

### 文件日志

- `logs/app.log`: FastAPI 服务器详细日志
- `logs/launch_app.log`: 启动器日志
- 文件日志通常包含比终端或 Web UI 更详细的信息

### Web UI 日志

- Web UI 右侧边栏实时显示来自主服务器的 `INFO` 及以上级别的日志
- 通过 WebSocket (`/ws/logs`) 连接获取实时日志
- 包含日志级别、时间戳和消息内容
- 提供清理日志的按钮

### 终端日志

- 调试模式 (`--debug`) 下，日志会直接输出到启动的终端
- 无头模式下，终端日志较少，主要信息在日志文件中

## 日志级别说明

### DEBUG

- 最详细的日志信息
- 包含函数调用、变量值、执行流程等
- 用于深度调试和问题排查

### INFO

- 一般信息日志
- 包含重要的操作和状态变化
- 日常运行的默认级别

### WARNING

- 警告信息
- 表示可能的问题或异常情况
- 不影响正常功能但需要注意

### ERROR

- 错误信息
- 表示功能异常或失败
- 需要立即关注和处理

### CRITICAL

- 严重错误
- 表示系统级别的严重问题
- 可能导致服务不可用

## 性能考虑

### 日志级别对性能的影响

- **DEBUG 级别**: 会产生大量日志，可能影响性能，仅在调试时使用
- **INFO 级别**: 平衡了信息量和性能，适合日常运行
- **WARNING 及以上**: 日志量最少，性能影响最小

### 日志文件大小管理

- 日志文件会随时间增长，建议定期清理或轮转
- 可以手动删除旧的日志文件
- 考虑使用系统的日志轮转工具（如 logrotate）

## 故障排除

### 日志不显示

1. 检查环境变量是否正确设置
2. 确认日志文件路径是否可写
3. 检查 Web UI 的 WebSocket 连接是否正常

### 日志过多

1. 降低日志级别（如从 DEBUG 改为 INFO）
2. 禁用 DEBUG_LOGS_ENABLED 和 TRACE_LOGS_ENABLED
3. 定期清理日志文件

### 日志缺失重要信息

1. 提高日志级别（如从 WARNING 改为 INFO 或 DEBUG）
2. 启用 DEBUG_LOGS_ENABLED 获取更多调试信息
3. 检查日志文件而不仅仅是终端输出

## 最佳实践

### 日常运行

```bash
# 推荐的日常运行配置
export SERVER_LOG_LEVEL=INFO
python launch_camoufox.py --headless
```

### 调试问题

```bash
# 推荐的调试配置
export SERVER_LOG_LEVEL=DEBUG
export DEBUG_LOGS_ENABLED=true
python launch_camoufox.py --debug
```

### 生产环境

```bash
# 推荐的生产环境配置
export SERVER_LOG_LEVEL=WARNING
python launch_camoufox.py --headless
```

## 下一步

日志控制配置完成后，请参考：
- [故障排除指南](troubleshooting.md)
- [高级配置指南](advanced-configuration.md)
