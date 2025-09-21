"""
CSS选择器配置模块
包含所有用于页面元素定位的CSS选择器
"""

# --- 输入相关选择器 ---
PROMPT_TEXTAREA_SELECTOR = 'ms-prompt-input-wrapper ms-autosize-textarea textarea'
INPUT_SELECTOR = PROMPT_TEXTAREA_SELECTOR
INPUT_SELECTOR2 = PROMPT_TEXTAREA_SELECTOR

# --- 按钮选择器 ---
SUBMIT_BUTTON_SELECTOR = 'button[aria-label="Run"].run-button'
INSERT_BUTTON_SELECTOR = 'button[aria-label="Insert assets such as images, videos, files, or audio"]'
UPLOAD_BUTTON_SELECTOR = 'button[aria-label="Upload File"]'
SKIP_PREFERENCE_VOTE_BUTTON_SELECTOR = 'button[data-test-id="skip-button"][aria-label="Skip preference vote"]'

# --- 响应相关选择器 ---
RESPONSE_CONTAINER_SELECTOR = 'ms-chat-turn .chat-turn-container.model'
RESPONSE_TEXT_SELECTOR = 'ms-cmark-node.cmark-node'

# --- 加载和状态选择器 ---
LOADING_SPINNER_SELECTOR = 'button[aria-label="Run"].run-button svg .stoppable-spinner'
OVERLAY_SELECTOR = '.mat-mdc-dialog-inner-container'
ZERO_STATE_SELECTOR = 'ms-zero-state'

# --- 错误提示选择器 ---
ERROR_TOAST_SELECTOR = 'div.toast.warning, div.toast.error'

# --- 编辑相关选择器 ---
EDIT_MESSAGE_BUTTON_SELECTOR = 'button[aria-label="Edit"].toggle-edit-button'
MESSAGE_TEXTAREA_SELECTOR = 'ms-chat-turn:last-child ms-text-chunk ms-autosize-textarea'
FINISH_EDIT_BUTTON_SELECTOR = 'button[aria-label="Stop editing"].toggle-edit-button'

# --- 菜单和复制相关选择器 ---
MORE_OPTIONS_BUTTON_SELECTOR = 'button[aria-label="Open options"]'
COPY_MARKDOWN_BUTTON_SELECTOR = 'button[role="menuitem"]:has-text("Copy markdown")'
COPY_MARKDOWN_BUTTON_SELECTOR_ALT = 'div[role="menu"] button:has-text("Copy Markdown")'

# --- 设置相关选择器 ---
MAX_OUTPUT_TOKENS_SELECTOR = 'input[aria-label="Maximum output tokens"]'
STOP_SEQUENCE_INPUT_SELECTOR = 'input[aria-label="Add stop token"]'
MAT_CHIP_REMOVE_BUTTON_SELECTOR = 'mat-chip-set mat-chip-row button[aria-label*="Remove"]'
TOP_P_INPUT_SELECTOR = '//div[contains(@class, "settings-item-column") and .//h3[normalize-space()="Top P"]]//input[@type="number"]'
TEMPERATURE_INPUT_SELECTOR = '//div[contains(@class, "settings-item-column") and .//h3[normalize-space()="Temperature"]]//input[@type="number"]'
USE_URL_CONTEXT_SELECTOR = 'button[aria-label="Browse the url context"]'
THINKING_MODE_TOGGLE_SELECTOR = 'mat-slide-toggle[data-test-toggle="enable-thinking"] button'
SET_THINKING_BUDGET_TOGGLE_SELECTOR = 'mat-slide-toggle[data-test-toggle="manual-budget"] button'
# Thinking budget slider input
THINKING_BUDGET_INPUT_SELECTOR = '//div[contains(@class, "settings-item") and .//p[normalize-space()="Set thinking budget"]]/following-sibling::div//input[@type="number"]'
# --- Google Search Grounding ---
GROUNDING_WITH_GOOGLE_SEARCH_TOGGLE_SELECTOR = 'div[data-test-id="searchAsAToolTooltip"] mat-slide-toggle button'

# --- 系统指令 ---
SYSTEM_INSTRUCTIONS_BUTTON_SELECTOR = 'button[aria-label="System instructions"]'
SYSTEM_INSTRUCTIONS_TEXTAREA_SELECTOR = 'textarea[aria-label="System instructions"]'

# --- 模型选择器 ---
# 新版界面模型选择器（优先尝试）
MODEL_SELECTOR_CARD_TITLE = '.model-selector-card .title'
MODEL_SELECTOR_CARD_NAME = '[data-test-id="model-name"]'
MODEL_SELECTOR_CARD_SUBTITLE = '.model-selector-card .subtitle'

# 旧版界面模型选择器（备用）
MODEL_SELECTOR_LEGACY_PRIMARY = 'mat-select[data-test-ms-model-selector] .model-option-content span'
MODEL_SELECTOR_LEGACY_FALLBACK = 'mat-select[data-test-ms-model-selector] span'
MODEL_SELECTOR_LEGACY_GENERIC = '[data-test-ms-model-selector] span'
MODEL_SELECTOR_BUTTON_SPAN = 'button[data-test-ms-model-selector] span'
MODEL_OPTION_CONTENT_SPAN = '.model-option-content span'

# 模型选择器列表（按优先级排序）
MODEL_SELECTORS_LIST = [
    MODEL_SELECTOR_CARD_TITLE,
    MODEL_SELECTOR_CARD_NAME,
    MODEL_SELECTOR_CARD_SUBTITLE,
    MODEL_SELECTOR_LEGACY_PRIMARY,
    MODEL_SELECTOR_LEGACY_FALLBACK,
    MODEL_SELECTOR_LEGACY_GENERIC,
    '.model-selector span',
    MODEL_SELECTOR_BUTTON_SPAN,
    MODEL_OPTION_CONTENT_SPAN
]