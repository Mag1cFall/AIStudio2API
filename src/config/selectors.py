PROMPT_TEXTAREA_SELECTORS = [
    'ms-prompt-input-wrapper ms-autosize-textarea textarea',
    'ms-prompt-box textarea',
]
PROMPT_TEXTAREA_SELECTOR = PROMPT_TEXTAREA_SELECTORS[0]
INPUT_SELECTOR = PROMPT_TEXTAREA_SELECTOR
INPUT_SELECTOR2 = PROMPT_TEXTAREA_SELECTOR

SUBMIT_BUTTON_SELECTORS = [
    'button[aria-label="Run"].run-button',
    'ms-run-button button[aria-label="Run"]',
    'ms-prompt-box ms-run-button button',
]
SUBMIT_BUTTON_SELECTOR = SUBMIT_BUTTON_SELECTORS[0]

INSERT_BUTTON_SELECTORS = [
    'button[aria-label="Insert assets such as images, videos, files, or audio"]',
    'button[data-test-add-chunk-menu-button]',
    'button[data-test-id="add-media-button"]',
]
INSERT_BUTTON_SELECTOR = INSERT_BUTTON_SELECTORS[0]

UPLOAD_BUTTON_SELECTORS = [
    'button[aria-label="Upload File"]',
    'button[role="menuitem"]:has-text("Upload File")',
    'button[role="menuitem"]:has-text("Upload a file")',
]
UPLOAD_BUTTON_SELECTOR = UPLOAD_BUTTON_SELECTORS[0]

HIDDEN_FILE_INPUT_SELECTORS = [
    'input[type="file"][data-test-upload-file-input]',
    'input.file-input[type="file"]',
]
HIDDEN_FILE_INPUT_SELECTOR = HIDDEN_FILE_INPUT_SELECTORS[0]

UPLOADED_MEDIA_ITEM_SELECTOR = 'ms-prompt-box .multi-media-row ms-media-chip'
SKIP_PREFERENCE_VOTE_BUTTON_SELECTOR = 'button[data-test-id="skip-button"][aria-label="Skip preference vote"]'
RESPONSE_CONTAINER_SELECTOR = 'ms-chat-turn .chat-turn-container.model'
RESPONSE_TEXT_SELECTOR = 'ms-cmark-node.cmark-node'

LOADING_SPINNER_SELECTORS = [
    'button[aria-label="Run"].run-button svg .stoppable-spinner',
    'ms-run-button button svg .stoppable-spinner',
    'ms-prompt-box ms-run-button button svg .stoppable-spinner',
]
LOADING_SPINNER_SELECTOR = LOADING_SPINNER_SELECTORS[0]

OVERLAY_SELECTOR = '.mat-mdc-dialog-inner-container'
ZERO_STATE_SELECTOR = 'ms-zero-state'
ERROR_TOAST_SELECTOR = 'div.toast.warning, div.toast.error'
EDIT_MESSAGE_BUTTON_SELECTOR = 'button[aria-label="Edit"].toggle-edit-button:has(span:text-is("edit"))'
MESSAGE_TEXTAREA_SELECTOR = 'ms-chat-turn:last-child ms-text-chunk ms-autosize-textarea'
FINISH_EDIT_BUTTON_SELECTOR = 'button[aria-label="Stop editing"].toggle-edit-button'
MORE_OPTIONS_BUTTON_SELECTOR = 'button[aria-label="Open options"]'
COPY_MARKDOWN_BUTTON_SELECTOR = 'button[role="menuitem"]:has-text("Copy markdown")'
COPY_MARKDOWN_BUTTON_SELECTOR_ALT = 'div[role="menu"] button:has-text("Copy Markdown")'
ADVANCED_SETTINGS_EXPANDER_SELECTOR = 'button[aria-label="Expand or collapse advanced settings"]'
MAX_OUTPUT_TOKENS_SELECTOR = 'input[aria-label="Maximum output tokens"]'
STOP_SEQUENCE_INPUT_SELECTOR = 'input[aria-label="Add stop token"]'
MAT_CHIP_REMOVE_BUTTON_SELECTOR = 'mat-chip-set mat-chip-row button[aria-label*="Remove"]'
TOP_P_INPUT_SELECTOR = '//div[contains(@class, "settings-item-column") and .//h3[normalize-space()="Top P"]]//input[@type="number"]'
TEMPERATURE_INPUT_SELECTOR = '//div[contains(@class, "settings-item-column") and .//h3[normalize-space()="Temperature"]]//input[@type="number"]'
USE_URL_CONTEXT_SELECTOR = 'button[aria-label="Browse the url context"]'
THINKING_MODE_TOGGLE_SELECTOR = 'mat-slide-toggle[data-test-toggle="enable-thinking"] button'
SET_THINKING_BUDGET_TOGGLE_SELECTOR = 'mat-slide-toggle[data-test-toggle="manual-budget"] button'
THINKING_BUDGET_INPUT_SELECTOR = '//div[contains(@class, "settings-item") and .//p[normalize-space()="Set thinking budget"]]/following-sibling::div//input[@type="number"]'
GROUNDING_WITH_GOOGLE_SEARCH_TOGGLE_SELECTOR = 'div[data-test-id="searchAsAToolTooltip"] mat-slide-toggle button'
SYSTEM_INSTRUCTIONS_BUTTON_SELECTOR = 'button[aria-label="System instructions"]'
SYSTEM_INSTRUCTIONS_TEXTAREA_SELECTOR = 'textarea[aria-label="System instructions"]'
MODEL_SELECTOR_CARD_TITLE = '.model-selector-card .title'
MODEL_SELECTOR_CARD_NAME = '[data-test-id="model-name"]'
MODEL_SELECTOR_CARD_SUBTITLE = '.model-selector-card .subtitle'
MODEL_SELECTOR_LEGACY_PRIMARY = 'mat-select[data-test-ms-model-selector] .model-option-content span'
MODEL_SELECTOR_LEGACY_FALLBACK = 'mat-select[data-test-ms-model-selector] span'
MODEL_SELECTOR_LEGACY_GENERIC = '[data-test-ms-model-selector] span'
MODEL_SELECTOR_BUTTON_SPAN = 'button[data-test-ms-model-selector] span'
MODEL_OPTION_CONTENT_SPAN = '.model-option-content span'
MODEL_SELECTORS_LIST = [MODEL_SELECTOR_CARD_TITLE, MODEL_SELECTOR_CARD_NAME, MODEL_SELECTOR_CARD_SUBTITLE, MODEL_SELECTOR_LEGACY_PRIMARY, MODEL_SELECTOR_LEGACY_FALLBACK, MODEL_SELECTOR_LEGACY_GENERIC, '.model-selector span', MODEL_SELECTOR_BUTTON_SPAN, MODEL_OPTION_CONTENT_SPAN]

THINKING_LEVEL_SELECT_SELECTOR = 'mat-select[aria-label="Thinking Level"], mat-select[aria-label="Thinking level"]'
THINKING_LEVEL_OPTION_HIGH_SELECTOR = 'mat-option:has-text("High")'
THINKING_LEVEL_OPTION_LOW_SELECTOR = 'mat-option:has-text("Low")'
DEFAULT_THINKING_LEVEL = "high"

RATE_LIMIT_CALLOUT_SELECTOR = 'ms-callout.error-callout .message, ms-callout.warning-callout .message'
RATE_LIMIT_KEYWORDS = ["exceeded quota", "out of free generations"]


