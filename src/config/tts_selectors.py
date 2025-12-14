TTS_ROOT_SELECTOR = 'ms-speech-prompt'
TTS_MAIN_CONTENT_SELECTOR = '.speech-prompt-main-content'
TTS_TOOLBAR_SELECTOR = 'ms-toolbar'
TTS_TOOLBAR_TITLE_SELECTOR = 'ms-toolbar h1.mode-title'
TTS_TOOLBAR_COPY_PROMPT_BUTTON_SELECTOR = 'ms-toolbar button[aria-label="Copy prompt to clipboard"]'
TTS_TOOLBAR_MORE_ACTIONS_BUTTON_SELECTOR = 'ms-toolbar button[aria-label="View more actions"]'

TTS_FOOTER_SELECTOR = '.speech-prompt-footer'
TTS_FOOTER_ACTIONS_SELECTOR = '.speech-prompt-footer-actions'
TTS_FOOTER_AUDIO_PLAYER_WRAPPER_SELECTOR = '.speech-prompt-footer-actions-left'
TTS_AUDIO_PLAYER_SELECTOR = '.speech-prompt-footer audio[controls]'
TTS_RUN_BUTTON_WRAPPER_SELECTOR = '.speech-prompt-footer .button-wrapper'
TTS_RUN_BUTTON_SELECTORS = [
    'ms-run-button button[aria-label="Run"]',
    '.speech-prompt-footer button[aria-label="Run"].run-button',
]
TTS_RUN_BUTTON_SELECTOR = TTS_RUN_BUTTON_SELECTORS[0]

TTS_SINGLE_SPEAKER_BUILDER_SELECTOR = '.single-speaker-prompt-builder-wrapper'
TTS_SINGLE_SPEAKER_STYLE_INPUT_SELECTOR = 'ms-autosize-textarea.style-instructions-textarea textarea'
TTS_SINGLE_SPEAKER_TEXT_INPUT_SELECTOR = 'textarea[placeholder="Start writing or paste text here to generate speech"]'

TTS_MULTI_SPEAKER_CONTAINER_SELECTOR = '.multi-speaker-container'
TTS_MULTI_SPEAKER_RAW_STRUCTURE_SELECTOR = '.multi-speaker-raw-structure'
TTS_MULTI_SPEAKER_RAW_INPUT_SELECTOR = 'textarea.multi-speaker-raw-prompt'
TTS_MULTI_SPEAKER_BUILDER_SELECTOR = 'ms-multi-speaker-prompt-builder'
TTS_MULTI_SPEAKER_STYLE_INPUT_SELECTOR = 'ms-multi-speaker-prompt-builder ms-autosize-textarea.style-instructions-textarea textarea'
TTS_MULTI_SPEAKER_BLOCKS_CONTAINER_SELECTOR = '.speaker-blocks-container'
TTS_MULTI_SPEAKER_BLOCK_SELECTOR = '.speaker-block'
TTS_MULTI_SPEAKER_BLOCK_HEADER_SELECTOR = '.speaker-block .block-header'
TTS_MULTI_SPEAKER_BLOCK_CHIP_SELECTOR = '.speaker-block .speaker-chip'
TTS_MULTI_SPEAKER_BLOCK_DELETE_BUTTON_SELECTOR = '.speaker-block .block-actions button[aria-label="Delete dialog"]'
TTS_MULTI_SPEAKER_BLOCK_TEXT_INPUT_SELECTOR = '.speaker-block .text-block textarea'
TTS_MULTI_SPEAKER_ADD_DIALOG_BUTTON_SELECTOR = 'button.add-dialog'

TTS_SETTINGS_PANEL_SELECTOR = 'ms-speech-run-settings'
TTS_SETTINGS_HEADER_SELECTOR = 'ms-speech-run-settings .overlay-header h2'
TTS_SETTINGS_GET_CODE_BUTTON_SELECTOR = 'ms-speech-run-settings button#getCodeBtn'
TTS_SETTINGS_RESET_BUTTON_SELECTOR = 'ms-speech-run-settings button#resetSettingsBtn'
TTS_SETTINGS_CLOSE_BUTTON_SELECTOR = 'ms-speech-run-settings button[aria-label="Close run settings panel"]'
TTS_SETTINGS_MODEL_SELECTOR_CARD_SELECTOR = 'ms-speech-run-settings ms-model-selector-v3 button.model-selector-card'
TTS_SETTINGS_MODEL_TITLE_SELECTOR = 'ms-speech-run-settings .model-selector-card .title'
TTS_SETTINGS_MODEL_NAME_SELECTOR = 'ms-speech-run-settings .model-selector-card [data-test-id="model-name"]'
TTS_SETTINGS_MODEL_DESCRIPTION_SELECTOR = 'ms-speech-run-settings .model-selector-card [data-test-id="model-description"]'
TTS_SETTINGS_MODE_SELECTOR_CONTAINER = 'ms-tts-mode-selector'
TTS_SETTINGS_SINGLE_SPEAKER_MODE_BUTTON = 'ms-tts-mode-selector button:has-text("Single-speaker")'
TTS_SETTINGS_MULTI_SPEAKER_MODE_BUTTON = 'ms-tts-mode-selector button:has-text("Multi-speaker")'
TTS_SETTINGS_TEMPERATURE_SLIDER_SELECTOR = 'div[data-test-id="temperatureSliderContainer"] mat-slider'
TTS_SETTINGS_TEMPERATURE_INPUT_SELECTOR = 'div[data-test-id="temperatureSliderContainer"] input[type="number"]'

TTS_SETTINGS_VOICE_SETTINGS_CONTAINER_SELECTOR = 'ms-voice-settings'
TTS_SETTINGS_SPEAKER_SETTINGS_EXPANDER = 'mat-expansion-panel.speaker-settings-container'
TTS_SETTINGS_SPEAKER_NAME_INPUT_SELECTOR = 'input[aria-label="Speaker name"]'
TTS_SETTINGS_VOICE_SELECT_DROPDOWN_SELECTOR = 'ms-voice-selector mat-select'
TTS_SETTINGS_VOICE_SELECT_VALUE_TEXT_SELECTOR = 'ms-voice-selector .mat-mdc-select-value-text'
TTS_SETTINGS_VOICE_OPTION_SELECTOR = 'mat-option'

TTS_PAGE_URL_TEMPLATE = 'https://aistudio.google.com/generate-speech?model={model}'
TTS_MODEL_FLASH = 'gemini-2.5-flash-preview-tts'
TTS_MODEL_PRO = 'gemini-2.5-pro-preview-tts'
TTS_SUPPORTED_MODELS = [TTS_MODEL_FLASH, TTS_MODEL_PRO]

TTS_PREBUILT_VOICES = [
    'Zephyr', 'Puck', 'Charon', 'Kore', 'Fenrir', 'Leda',
    'Orus', 'Aoede', 'Callirrhoe', 'Autonoe', 'Enceladus', 'Iapetus',
    'Umbriel', 'Algieba', 'Despina', 'Erinome', 'Algenib', 'Rasalgethi',
    'Laomedeia', 'Achernar', 'Alnilam', 'Schedar', 'Gacrux', 'Pulcherrima',
    'Achird', 'Zubenelgenubi', 'Vindemiatrix', 'Sadachbia', 'Sadaltager', 'Sulafat'
]
