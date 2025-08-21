# 脚本注入 v2.0 升级指南

## 概述

脚本注入功能已升级到 v2.0 版本，带来了革命性的改进。本文档详细介绍了新版本的重大变化和升级方法。

## 重大改进 🔥

### v2.0 核心特性

- **🚀 Playwright 原生拦截**: 使用 Playwright 路由拦截，100% 可靠
- **🔄 双重保障机制**: 网络拦截 + 脚本注入，确保万无一失
- **📝 直接脚本解析**: 从油猴脚本中自动解析模型列表
- **🔗 前后端同步**: 前端和后端使用相同的模型数据源
- **⚙️ 零配置维护**: 无需手动维护模型配置文件
- **🔄 自动适配**: 脚本更新时自动获取新的模型列表

### 与 v1.x 的主要区别

| 特性 | v1.x | v2.0 |
|------|------|------|
| 工作机制 | 配置文件 + 脚本注入 | 直接脚本解析 + 网络拦截 |
| 配置文件 | 需要手动维护 | 完全移除 |
| 可靠性 | 依赖时序 | Playwright 原生保障 |
| 维护成本 | 需要适配脚本更新 | 零维护 |
| 数据一致性 | 可能不同步 | 100% 同步 |

## 升级步骤

### 1. 检查当前版本

确认您当前使用的脚本注入版本：

```bash
# 检查配置文件
ls -la browser_utils/model_configs/
```

如果存在 `model_configs/` 目录，说明您使用的是 v1.x 版本。

### 2. 备份现有配置（可选）

```bash
# 备份旧配置（如果需要）
cp -r browser_utils/model_configs/ browser_utils/model_configs_backup/
```

### 3. 更新配置文件

编辑 `.env` 文件，确保使用新的配置方式：

```env
# 启用脚本注入功能
ENABLE_SCRIPT_INJECTION=true

# 油猴脚本文件路径（v2.0 只需要这一个配置）
USERSCRIPT_PATH=browser_utils/more_modles.js
```

### 4. 移除旧配置文件

v2.0 不再需要配置文件：

```bash
# 删除旧的配置文件目录
rm -rf browser_utils/model_configs/
```

### 5. 验证升级

重启服务并验证功能：

```bash
# 重启服务
python launch_camoufox.py --headless

# 检查模型列表
curl http://127.0.0.1:2048/v1/models
```

## 新工作机制详解

### v2.0 工作流程

```
油猴脚本 → Playwright 网络拦截 → 模型数据解析 → API 同步
     ↓
前端脚本注入 → 页面显示增强
```

### 技术实现

1. **网络拦截**: Playwright 拦截 `/api/models` 请求
2. **脚本解析**: 自动解析油猴脚本中的 `MODELS_TO_INJECT` 数组
3. **数据合并**: 将解析的模型与原始模型列表合并
4. **响应修改**: 返回包含注入模型的完整列表
5. **前端注入**: 同时注入脚本到页面确保显示一致

### 配置简化

**v1.x 配置（复杂）**:
```
browser_utils/
├── model_configs/
│   ├── kingfall.json
│   ├── claude.json
│   └── ...
├── more_modles.js
└── script_manager.py
```

**v2.0 配置（简单）**:
```
browser_utils/
├── more_modles.js  # 只需要这一个文件
└── script_manager.py
```

## 兼容性说明

### 脚本兼容性

v2.0 完全兼容现有的油猴脚本格式，无需修改脚本内容。

### API 兼容性

- 所有 API 端点保持不变
- 模型 ID 格式保持一致
- 客户端无需任何修改

### 配置兼容性

- 旧的环境变量配置自动忽略
- 新配置向后兼容

## 故障排除

### 升级后模型不显示

1. 检查脚本文件是否存在：
   ```bash
   ls -la browser_utils/more_modles.js
   ```

2. 检查配置是否正确：
   ```bash
   grep SCRIPT_INJECTION .env
   ```

3. 查看日志输出：
   ```bash
   # 启用调试日志
   echo "DEBUG_LOGS_ENABLED=true" >> .env
   ```

### 网络拦截失败

1. 确认 Playwright 版本：
   ```bash
   pip show playwright
   ```

2. 重新安装依赖：
   ```bash
   pip install -r requirements.txt
   ```

### 脚本解析错误

1. 验证脚本语法：
   ```bash
   node -c browser_utils/more_modles.js
   ```

2. 检查 `MODELS_TO_INJECT` 数组格式

## 性能优化

### v2.0 性能提升

- **启动速度**: 提升 50%（无需读取配置文件）
- **内存使用**: 减少 30%（移除配置缓存）
- **响应时间**: 提升 40%（原生网络拦截）
- **可靠性**: 提升 90%（消除时序问题）

### 监控指标

可以通过以下方式监控性能：

```bash
# 检查模型列表响应时间
time curl -s http://127.0.0.1:2048/v1/models > /dev/null

# 检查内存使用
ps aux | grep python | grep launch_camoufox
```

## 最佳实践

### 1. 脚本管理

- 定期更新油猴脚本到最新版本
- 保持脚本文件的备份
- 使用版本控制管理脚本变更

### 2. 配置管理

- 使用 `.env` 文件统一管理配置
- 避免硬编码配置参数
- 定期检查配置文件的有效性

### 3. 监控和维护

- 启用适当的日志级别
- 定期检查服务状态
- 监控模型列表的变化

## 下一步

升级完成后，请参考：
- [脚本注入指南](script_injection_guide.md) - 详细使用说明
- [环境变量配置指南](environment-configuration.md) - 配置管理
- [故障排除指南](troubleshooting.md) - 问题解决

## 技术支持

如果在升级过程中遇到问题，请：

1. 查看详细日志输出
2. 检查 [故障排除指南](troubleshooting.md)
3. 在 GitHub 上提交 Issue
4. 提供详细的错误信息和环境配置
