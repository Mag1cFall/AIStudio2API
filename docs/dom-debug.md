# AIStudio2API DOM 调试指南

## 概述

AIStudio2API 通过 Playwright 自动操控 Google AI Studio 页面。由于 AI Studio 的 DOM 结构会频繁变更，选择器失效是最常见的故障类型。本项目内建了 DOM 快照调试基础设施，可在关键操作点自动抓取页面状态用于诊断。

## 快速开始

### 启用 DOM 调试

```bash
# 环境变量
set DOM_DEBUG=true

# 单 Worker 启动
set PYTHONPATH=src && uv run python src/launch_camoufox.py --headless

# 10 Worker 集群启动（通过 Manager）
uv run python src/app_launcher.py
# 然后 POST http://127.0.0.1:9000/api/control/start
```

### 快照存储

- 路径: `test/dom_snapshots/`
- 命名: `{序号}_{时间}_{阶段}_{请求ID}.html`
- 已被 `.gitignore` 排除

### 快照内容

快照经过清洗处理（`src/debug/dom_snapshot.py`）：
- 移除 `<script>` 标签和 inline 事件
- 截断 base64 `data:` URI（保留前 100 字符）
- 截断超长 SVG `d=` 属性
- 保留完整 DOM 结构和所有属性（class/aria-label/data-test-id 等）

## 快照抓取点

### Chat 流程 (`page_controller.py`)
| 快照名 | 触发时机 |
|---|---|
| `chat_nav_ready` | 导航到新聊天完成 |
| `chat_cleared` | 聊天记录清空后 |
| `chat_model_switch_{model}` | 模型切换完成 |
| `chat_google_search_{on/off}` | Google Search toggle |
| `chat_params_ready` | 参数设置完成 |
| `chat_prompt_filled` | 提示词填入后 |
| `chat_submit` | 提交后 |
| `chat_max_tokens_{n}` | Max tokens 设置 |
| `chat_temperature_{t}` | Temperature 设置 |
| `chat_top_p_{p}` | Top-P 设置 |

### Imagen 流程 (`imagen_controller.py`)
| 快照名 | 触发时机 |
|---|---|
| `imagen_nav` | 导航到 Imagen 页面 |
| `imagen_set_ratio` | 画面比例设置 |
| `imagen_prompt` | 提示词填入 |
| `imagen_run` | 点击生成 |
| `imagen_paid_dialog` | 检测到 paid API key 弹窗 |
| `imagen_paid_dialog_closed` | 弹窗关闭后 |
| `imagen_result` | 生成结果就绪 |
| `imagen_timeout` | 超时 |

### Nano 流程 (`nano_controller.py`)
| 快照名 | 触发时机 |
|---|---|
| `nano_nav` | 导航到新聊天 |
| `nano_prompt` | 提示词填入 |
| `nano_run` | 点击生成 |
| `nano_content` | 内容生成完成 |
| `nano_timeout` | 超时 |

### TTS 流程 (`tts_controller.py`)
| 快照名 | 触发时机 |
|---|---|
| `tts_nav` | 导航到 TTS 页面 |
| `tts_mode_switched_{mode}` | 模式切换 |
| `tts_voice_set_{voice}` | 语音设置 |
| `tts_text_filled` | 文本填入 |
| `tts_multi_filled` | 多说话人脚本填入 |
| `tts_run` | 点击运行 |
| `tts_audio_found` | 音频就绪 |
| `tts_timeout` | 超时 |

## 选择器审计（2026-04-01）

通过 `_selector_audit.py` 脚本对 46 个快照进行自动化验证的结果：

### Chat 选择器 (`src/config/selectors.py`)

| 选择器 | 状态 | 说明 |
|---|---|---|
| `ms-prompt-box textarea[aria-label="Enter a prompt"]` | ✅ 稳定 | 主选择器，3/3 命中 |
| `ms-prompt-box textarea[placeholder="Start typing a prompt"]` | ⚠️ 废弃 | 3/3 未命中，placeholder 已变更 |
| `ms-prompt-box textarea` | ✅ 稳定 | 兜底选择器 |
| `ms-run-button button[type="submit"]` | ✅ 稳定 | Run 按钮 |
| `button[data-test-id="add-media-button"]` | ✅ 稳定 | 文件上传 |
| `ms-zero-state` | ✅ 稳定 | 空聊天状态 |
| `button[aria-label="Expand or collapse advanced settings"]` | ✅ 稳定 | 高级设置 |
| `input[aria-label="Maximum output tokens"]` | ✅ 稳定 | |
| `.model-selector-card .title` | ✅ 稳定 | 模型选择器 |
| `[data-test-id="model-name"]` | ✅ 稳定 | |
| `div[data-test-id="searchAsAToolTooltip"] mat-slide-toggle button` | ✅ 稳定 | Google Search |
| `mat-slide-toggle[data-test-toggle="enable-thinking"]` | ✅ 条件 | 仅在支持 thinking 的模型上出现 |
| `ms-chat-turn .chat-turn-container.model` | ✅ 条件 | 仅有回复时出现 |
| `textarea[aria-label="System instructions"]` | ✅ 条件 | 仅展开系统指令面板时出现 |

### Imagen 选择器 (`src/config/imagen_selectors.py`)

| 选择器 | 状态 | 说明 |
|---|---|---|
| `ms-image-prompt` | ✅ 稳定 | 页面根元素 |
| `ms-prompt-input-wrapper textarea[...]` | ⚠️ 废弃 | 3/3 未命中，`ms-prompt-input-wrapper` 已不存在 |
| `textarea[aria-label="Enter a prompt to generate an image"]` | ✅ 稳定 | 应作为主选择器 |
| `ms-run-button button[type="submit"]` | ✅ 稳定 | |
| `ms-run-settings` | ✅ 稳定 | 设置面板 |
| `ms-run-settings ms-aspect-ratio-radio-button button` | ✅ 稳定 | 5 个按钮 |
| `ms-paid-usage-dialog` | ✅ 条件 | paid key 弹窗存在时命中 |
| `ms-image-generation-gallery-image img.loaded-image` | ✅ 条件 | 生成完成后命中 |

### Nano 选择器 (`src/config/nano_selectors.py`)

| 选择器 | 状态 | 说明 |
|---|---|---|
| `ms-run-settings` | ✅ 稳定 | |
| `ms-chat-turn ms-image-chunk img.loaded-image` | ✅ 条件 | 图片生成后才出现 |

### TTS 选择器 (`src/config/tts_selectors.py`)

| 选择器 | 状态 | 说明 |
|---|---|---|
| `ms-speech-prompt` | ✅ 稳定 | 页面根 |
| `ms-run-button button[type="submit"]` | ✅ 稳定 | |
| `ms-tts-mode-selector` | ✅ 条件 | 设置面板加载后出现 |
| `ms-voice-selector mat-select` | ✅ 条件 | 同上 |
| `.speech-prompt-footer audio[controls]` | ✅ 条件 | 音频生成后出现 |

## 诊断工作流

### 步骤 1：复现故障
```bash
set DOM_DEBUG=true
# 触发失败的请求
```

### 步骤 2：找到相关快照
```bash
# 列出最近的快照
dir /od test\dom_snapshots\*.html
```

### 步骤 3：检查 DOM 结构

在快照 HTML 中搜索：
- 自定义元素标签名（`ms-*`）
- `aria-label` 属性值
- `data-test-id` 属性值
- class 名变化

### 步骤 4：修复选择器

在 `src/config/` 目录下更新对应的选择器文件，优先使用：
1. `data-test-id`（最稳定）
2. `aria-label`（语义稳定）
3. 自定义元素标签 `ms-*`（结构稳定）
4. class 名（最不稳定）

### 步骤 5：验证
```bash
python _selector_audit.py
```

## 环境变量参考

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DOM_DEBUG` | `""` | 设为 `true` 启用 DOM 快照 |
| `HOST_OS_FOR_SHORTCUT` | 自动检测 | 键盘快捷键修饰键（macOS 用 Meta） |

## 注意事项

- 快照文件可能很大（200-400KB），定期清理 `test/dom_snapshots/`
- `DOM_DEBUG` 关闭时，`dump_page` 是零开销空操作
- 快照中的 base64 数据已截断，无法用于恢复原始媒体
