# UI状态强制设置功能

## 概述

在 `browser_utils/model_management.py` 中实现了强制设置UI状态的功能，确保 `isAdvancedOpen` 始终为 `true`，`areToolsOpen` 始终为 `false`。

## 实现的功能

### 1. 状态验证函数

#### `_verify_ui_state_settings(page, req_id)`
- **功能**: 验证当前localStorage中的UI状态设置
- **返回**: 包含验证结果的字典
  - `exists`: localStorage是否存在
  - `isAdvancedOpen`: 当前isAdvancedOpen的值
  - `areToolsOpen`: 当前areToolsOpen的值
  - `needsUpdate`: 是否需要更新
  - `prefs`: 当前的preferences对象（如果存在）
  - `error`: 错误信息（如果有）

### 2. 强制设置函数

#### `_force_ui_state_settings(page, req_id)`
- **功能**: 强制设置UI状态为正确值
- **设置**: `isAdvancedOpen = true`, `areToolsOpen = false`
- **返回**: 设置是否成功的布尔值
- **特点**: 会自动验证设置是否生效

#### `_force_ui_state_with_retry(page, req_id, max_retries=3, retry_delay=1.0)`
- **功能**: 带重试机制的UI状态强制设置
- **参数**: 
  - `max_retries`: 最大重试次数（默认3次）
  - `retry_delay`: 重试延迟秒数（默认1秒）
- **返回**: 最终是否设置成功

### 3. 完整流程函数

#### `_verify_and_apply_ui_state(page, req_id)`
- **功能**: 验证并应用UI状态设置的完整流程
- **流程**: 
  1. 首先验证当前状态
  2. 如果需要更新，则调用强制设置功能
  3. 返回操作是否成功
- **特点**: 这是推荐使用的主要接口

## 集成点

### 1. 模型切换流程
在 `switch_ai_studio_model()` 函数中的关键节点：
- 设置localStorage后
- 页面导航完成后
- 恢复流程中

### 2. 页面初始化流程
在 `_handle_initial_model_state_and_storage()` 函数中：
- 检查localStorage状态时
- 页面重新加载后

### 3. 模型显示设置流程
在 `_set_model_from_page_display()` 函数中：
- 更新localStorage时

## 验证和重试机制

### 验证机制
- 每次设置后都会验证是否生效
- 支持检测JSON解析错误
- 提供详细的状态信息

### 重试机制
- 默认最多重试3次
- 每次重试间隔1秒
- 记录详细的重试日志
- 失败后会记录错误信息

### 关键操作后的验证
系统会在以下操作后自动验证UI状态：
1. **网页切换模型后**
2. **页面初始化完成后**
3. **页面重新加载后**
4. **任何需要重载页面的操作后**

如果验证发现设置不正确，系统会：
1. 继续执行刷新操作
2. 重新应用设置
3. 直到验证成功为止

## 日志记录

所有操作都有详细的日志记录：
- `[req_id] 开始验证UI状态设置...`
- `[req_id] UI状态验证结果: isAdvancedOpen=true, areToolsOpen=false, needsUpdate=false`
- `[req_id] ✅ UI状态设置在第 1 次尝试中成功`
- `[req_id] ⚠️ UI状态设置验证失败，可能需要重试`

## 错误处理

- 捕获并处理JSON解析错误
- 捕获并处理页面操作异常
- 提供详细的错误信息
- 在失败时不会中断主要流程

## 使用示例

```python
# 基本验证
result = await _verify_ui_state_settings(page, req_id)
if result['needsUpdate']:
    print("需要更新UI状态")

# 强制设置
success = await _force_ui_state_settings(page, req_id)
if success:
    print("UI状态设置成功")

# 完整流程（推荐）
success = await _verify_and_apply_ui_state(page, req_id)
if success:
    print("UI状态验证和应用成功")
```

## 配置要求

确保以下设置始终生效：
- `isAdvancedOpen: true` - 高级选项面板始终打开
- `areToolsOpen: false` - 工具面板始终关闭

这些设置对于系统的正常运行至关重要，特别是 `areToolsOpen` 必须为 `false`。
