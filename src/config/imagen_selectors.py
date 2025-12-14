IMAGEN_PAGE_URL_TEMPLATE = 'https://aistudio.google.com/prompts/new_image?model={model}'
IMAGEN_SUPPORTED_MODELS = ['imagen-4.0-generate-001', 'imagen-4.0-ultra-generate-001', 'imagen-4.0-fast-generate-001']

IMAGEN_ROOT_SELECTOR = 'ms-image-prompt'
IMAGEN_MAIN_CONTENT_SELECTOR = '.image-prompt-main'

IMAGEN_TOOLBAR_SELECTOR = 'ms-toolbar'
IMAGEN_TOOLBAR_TITLE_SELECTOR = 'ms-toolbar h1.mode-title'

IMAGEN_PROMPT_INPUT_SELECTORS = [
    'ms-prompt-input-wrapper textarea[aria-label="Enter a prompt to generate an image"]',
    'textarea[aria-label="Enter a prompt to generate an image"]',
    'ms-prompt-box textarea[aria-label="Enter a prompt to generate an image"]',
]
IMAGEN_PROMPT_INPUT_SELECTOR = IMAGEN_PROMPT_INPUT_SELECTORS[0]

IMAGEN_RUN_BUTTON_SELECTORS = [
    'ms-run-button button[aria-label="Run"]',
    'button[aria-label="Run"].run-button',
    'ms-prompt-input-wrapper ms-run-button button[aria-label="Run"]',
    'ms-prompt-box ms-run-button button[aria-label="Run"]',
]
IMAGEN_RUN_BUTTON_SELECTOR = IMAGEN_RUN_BUTTON_SELECTORS[0]

IMAGEN_GALLERY_CONTAINER_SELECTOR = 'ms-image-generation-gallery'
IMAGEN_GALLERY_ITEM_SELECTOR = 'ms-image-generation-gallery-image'
IMAGEN_GENERATED_IMAGE_SELECTOR = 'ms-image-generation-gallery-image .image-container img'
IMAGEN_IMAGE_ACTIONS_CONTAINER_SELECTOR = 'ms-image-generation-gallery-image .actions-container'
IMAGEN_IMAGE_DOWNLOAD_BUTTON_SELECTOR = 'ms-image-generation-gallery-image button[aria-label="Download this image"]'
IMAGEN_IMAGE_COPY_BUTTON_SELECTOR = 'ms-image-generation-gallery-image button[aria-label="Copy this image"]'

IMAGEN_SETTINGS_PANEL_SELECTOR = 'ms-run-settings'
IMAGEN_SETTINGS_HEADER_SELECTOR = 'ms-run-settings .overlay-header h2'
IMAGEN_SETTINGS_MODEL_SELECTOR_CARD_SELECTOR = 'ms-run-settings ms-model-selector-v3 button.model-selector-card'
IMAGEN_SETTINGS_NUM_RESULTS_CONTAINER_SELECTOR = 'div:has(> div > p:text-is("Number of results"))'
IMAGEN_SETTINGS_NUM_RESULTS_SLIDER_SELECTOR = 'div:has(> div > p:text-is("Number of results")) mat-slider'
IMAGEN_SETTINGS_NUM_RESULTS_INPUT_SELECTOR = 'div:has(> div > p:text-is("Number of results")) input[type="number"]'
IMAGEN_SETTINGS_ASPECT_RATIO_GALLERY_SELECTOR = 'ms-run-settings .aspect-ratio-gallery'
IMAGEN_SETTINGS_ASPECT_RATIO_BUTTON_SELECTOR = 'ms-run-settings ms-aspect-ratio-radio-button button'
IMAGEN_SETTINGS_NEGATIVE_PROMPT_SELECTOR = 'ms-run-settings textarea[aria-label="Add a negative prompt to define what should not be generated"]'
IMAGEN_SETTINGS_RESET_BUTTON_SELECTOR = 'ms-run-settings button#resetSettingsBtn'
IMAGEN_SETTINGS_CLOSE_BUTTON_SELECTOR = 'ms-run-settings button[aria-label="Close run settings panel"]'

IMAGEN_IMAGE_THUMB_UP_BUTTON_SELECTOR = 'ms-image-generation-gallery-image button[aria-label="Good response"]'
IMAGEN_IMAGE_THUMB_DOWN_BUTTON_SELECTOR = 'ms-image-generation-gallery-image button[aria-label="Bad response"]'
IMAGEN_IMAGE_FULLSCREEN_BUTTON_SELECTOR = 'ms-image-generation-gallery-image button[aria-label="Large view of this image"]'
IMAGEN_IMAGE_EXPORT_DRIVE_BUTTON_SELECTOR = 'ms-image-generation-gallery-image button[aria-label="Export this image to Google Drive"]'
