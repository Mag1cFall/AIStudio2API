VEO_PAGE_URL_TEMPLATE = 'https://aistudio.google.com/prompts/new_video?model={model}'
VEO_SUPPORTED_MODELS = ['veo-2.0-generate-001']

VEO_ROOT_SELECTOR = 'ms-video-prompt'
VEO_MAIN_CONTENT_SELECTOR = '.video-prompt-main'

VEO_TOOLBAR_SELECTOR = 'ms-toolbar'
VEO_TOOLBAR_TITLE_SELECTOR = 'ms-toolbar h1.mode-title'

VEO_PROMPT_INPUT_SELECTOR = 'textarea[aria-label="Enter a prompt to generate a video"]'
VEO_ADD_MEDIA_BUTTON_SELECTOR = 'ms-add-media-button button[aria-label="Add an image to the prompt"]'
VEO_RUN_BUTTON_SELECTOR = 'ms-run-button button[aria-label="Run"]'

VEO_GALLERY_CONTAINER_SELECTOR = 'ms-video-generation-gallery'
VEO_GALLERY_ITEM_SELECTOR = 'ms-video-generation-gallery-video'
VEO_GENERATED_VIDEO_SELECTOR = 'ms-video-generation-gallery-video video'
VEO_VIDEO_CONTROLS_SELECTOR = 'ms-video-generation-gallery-video .bottom-controls-container'
VEO_VIDEO_PLAY_BUTTON_SELECTOR = 'ms-video-generation-gallery-video button[aria-label="Play video"]'
VEO_VIDEO_DOWNLOAD_BUTTON_SELECTOR = 'ms-video-generation-gallery-video button[aria-label="Download video"]'
VEO_VIDEO_FULLSCREEN_BUTTON_SELECTOR = 'ms-video-generation-gallery-video button[aria-label="Large view"]'

VEO_SETTINGS_PANEL_SELECTOR = 'ms-run-settings'
VEO_SETTINGS_HEADER_SELECTOR = 'ms-run-settings .overlay-header h2'
VEO_SETTINGS_MODEL_SELECTOR_CARD_SELECTOR = 'ms-run-settings ms-model-selector-v3 button.model-selector-card'
VEO_SETTINGS_NUM_RESULTS_CONTAINER_SELECTOR = 'div:has(> div > p:text-is("Number of results"))'
VEO_SETTINGS_NUM_RESULTS_SLIDER_SELECTOR = 'div:has(> div > p:text-is("Number of results")) mat-slider'
VEO_SETTINGS_NUM_RESULTS_INPUT_SELECTOR = 'div:has(> div > p:text-is("Number of results")) input[type="number"]'
VEO_SETTINGS_ASPECT_RATIO_GALLERY_SELECTOR = 'ms-run-settings .aspect-ratio-gallery'
VEO_SETTINGS_ASPECT_RATIO_BUTTON_SELECTOR = 'ms-run-settings ms-aspect-ratio-radio-button button'
VEO_SETTINGS_DURATION_DROPDOWN_SELECTOR = 'mat-select#duration-selector'
VEO_SETTINGS_NEGATIVE_PROMPT_SELECTOR = 'ms-run-settings textarea[aria-label="Add a negative prompt to define what should not be generated"]'
VEO_SETTINGS_RESET_BUTTON_SELECTOR = 'ms-run-settings button#resetSettingsBtn'
VEO_SETTINGS_CLOSE_BUTTON_SELECTOR = 'ms-run-settings button[aria-label="Close run settings panel"]'

VEO_VIDEO_THUMB_UP_BUTTON_SELECTOR = 'ms-video-generation-gallery-video button[aria-label="Good response"]'
VEO_VIDEO_THUMB_DOWN_BUTTON_SELECTOR = 'ms-video-generation-gallery-video button[aria-label="Bad response"]'
VEO_VIDEO_EXPORT_DRIVE_BUTTON_SELECTOR = 'ms-video-generation-gallery-video button[aria-label="Export this video to Google Drive"]'

VEO_SETTINGS_FRAMERATE_DROPDOWN_SELECTOR = 'mat-select#frame-rate-selector'
VEO_SETTINGS_RESOLUTION_DROPDOWN_SELECTOR = 'mat-select#mat-select-11'
