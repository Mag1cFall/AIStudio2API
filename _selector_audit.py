import os
import sys
import re
import glob
from html.parser import HTMLParser

SNAPSHOT_DIR = os.path.join(os.path.dirname(__file__), "test", "dom_snapshots")

class TagCollector(HTMLParser):
    def __init__(self):
        super().__init__()
        self.tags = []
    def handle_starttag(self, tag, attrs):
        self.tags.append((tag, dict(attrs)))

def load_snapshot(path):
    with open(path, encoding="utf-8") as f:
        return f.read()

def css_match_count(html_text, selector):
    try:
        from lxml import html as lxml_html
        from lxml.cssselect import CSSSelector, SelectorSyntaxError
        sel_clean = selector.strip()
        if sel_clean.startswith("//"):
            return -1
        if ":has-text(" in sel_clean or ":text-is(" in sel_clean:
            return -1
        if ":has(" in sel_clean:
            return -1
        try:
            doc = lxml_html.fromstring(html_text)
        except Exception:
            return -2
        try:
            css = CSSSelector(sel_clean)
            return len(css(doc))
        except SelectorSyntaxError:
            return -1
    except ImportError:
        return -3

SELECTORS = {
    "chat": {
        "PROMPT_TEXTAREA[0]": 'ms-prompt-box textarea[aria-label="Enter a prompt"]',
        "PROMPT_TEXTAREA[1]": 'ms-prompt-box textarea[placeholder="Start typing a prompt"]',
        "PROMPT_TEXTAREA[2]": "ms-prompt-box .prompt-box-container .text-wrapper textarea",
        "PROMPT_TEXTAREA[3]": "ms-prompt-box textarea",
        "SUBMIT_BUTTON[0]": 'ms-run-button button[type="submit"]',
        "INSERT_BUTTON[0]": 'button[data-test-id="add-media-button"]',
        "INSERT_BUTTON[2]": "ms-add-media-button button",
        "RESPONSE_CONTAINER": "ms-chat-turn .chat-turn-container.model",
        "RESPONSE_TEXT": "ms-cmark-node.cmark-node",
        "LOADING_SPINNER[0]": 'ms-run-button button[type="submit"] svg .stoppable-spinner',
        "ZERO_STATE": "ms-zero-state",
        "ADVANCED_SETTINGS": 'button[aria-label="Expand or collapse advanced settings"]',
        "MAX_OUTPUT_TOKENS": 'input[aria-label="Maximum output tokens"]',
        "STOP_SEQUENCE_INPUT": 'input[aria-label="Add stop token"]',
        "SYSTEM_INSTRUCTIONS_BTN": 'button[aria-label="System instructions"]',
        "SYSTEM_INSTRUCTIONS_TA": 'textarea[aria-label="System instructions"]',
        "MODEL_SELECTOR_CARD_TITLE": ".model-selector-card .title",
        "MODEL_SELECTOR_CARD_NAME": '[data-test-id="model-name"]',
        "GROUNDING_TOGGLE": 'div[data-test-id="searchAsAToolTooltip"] mat-slide-toggle button',
        "THINKING_MODE_TOGGLE": 'mat-slide-toggle[data-test-toggle="enable-thinking"]',
        "EDIT_MSG_BUTTON": 'button[aria-label="Edit"].toggle-edit-button',
        "URL_CONTEXT_TOGGLE": 'ms-browse-as-a-tool mat-slide-toggle button[role="switch"]',
    },
    "imagen": {
        "ROOT": "ms-image-prompt",
        "PROMPT_INPUT[0]": 'ms-prompt-input-wrapper textarea[aria-label="Enter a prompt to generate an image"]',
        "PROMPT_INPUT[1]": 'textarea[aria-label="Enter a prompt to generate an image"]',
        "RUN_BUTTON[0]": 'ms-run-button button[type="submit"]',
        "GALLERY_CONTAINER": "ms-image-generation-gallery",
        "GALLERY_ITEM": "ms-image-generation-gallery-image",
        "GEN_IMAGE": "ms-image-generation-gallery-image img.loaded-image",
        "GEN_IMAGE_ALT1": "ms-image-generation-gallery-image .image-container img",
        "GEN_IMAGE_ALT2": "ms-image-generation-gallery img",
        "PAID_DIALOG": "ms-paid-usage-dialog",
        "PAID_CLOSE": 'ms-paid-usage-dialog button[aria-label="close"]',
        "DOWNLOAD_BTN": 'ms-image-generation-gallery-image button[aria-label="Download this image"]',
        "SETTINGS_PANEL": "ms-run-settings",
        "ASPECT_RATIO_BTN": "ms-run-settings ms-aspect-ratio-radio-button button",
    },
    "nano": {
        "IMAGE_CHUNK": "ms-chat-turn ms-prompt-chunk ms-image-chunk",
        "NANO_GEN_IMAGE": "ms-chat-turn ms-image-chunk img.loaded-image",
        "DOWNLOAD_BTN": 'ms-chat-turn ms-image-chunk button[aria-label="Download"]',
        "SETTINGS_PANEL": "ms-run-settings",
    },
    "tts": {
        "ROOT": "ms-speech-prompt",
        "AUDIO_PLAYER": ".speech-prompt-footer audio[controls]",
        "RUN_BUTTON[0]": 'ms-run-button button[type="submit"]',
        "SINGLE_TEXT_INPUT": 'textarea[placeholder="Start writing or paste text here to generate speech"]',
        "MULTI_RAW_INPUT": "textarea.multi-speaker-raw-prompt",
        "MODE_SELECTOR": "ms-tts-mode-selector",
        "VOICE_DROPDOWN": "ms-voice-selector mat-select",
        "SETTINGS_PANEL": "ms-speech-run-settings",
    },
}

SNAPSHOT_TYPE_MAP = {
    "chat": ["chat_"],
    "imagen": ["imagen_"],
    "nano": ["nano_"],
    "tts": ["tts_"],
}

def pick_snapshots(category):
    patterns = SNAPSHOT_TYPE_MAP[category]
    files = sorted(glob.glob(os.path.join(SNAPSHOT_DIR, "*.html")))
    matched = []
    for f in files:
        base = os.path.basename(f)
        for p in patterns:
            parts = base.split("_", 3)
            if len(parts) >= 4 and p in parts[3]:
                matched.append(f)
                break
    if len(matched) > 3:
        return [matched[0], matched[len(matched)//2], matched[-1]]
    return matched

def main():
    if not os.path.isdir(SNAPSHOT_DIR):
        print(f"❌ 快照目录不存在: {SNAPSHOT_DIR}")
        sys.exit(1)

    all_files = glob.glob(os.path.join(SNAPSHOT_DIR, "*.html"))
    print(f"📁 快照目录: {SNAPSHOT_DIR}")
    print(f"📄 快照数量: {len(all_files)}\n")

    for category, selectors in SELECTORS.items():
        snapshots = pick_snapshots(category)
        if not snapshots:
            print(f"\n{'='*60}")
            print(f"⚠️  {category.upper()}: 无快照可用")
            continue

        print(f"\n{'='*60}")
        print(f"🔍 {category.upper()} (采样 {len(snapshots)} 个快照)")
        print(f"{'='*60}")

        for snap_path in snapshots:
            snap_name = os.path.basename(snap_path)
            html = load_snapshot(snap_path)
            print(f"\n  📄 {snap_name} ({len(html):,} chars)")

            for name, sel in selectors.items():
                count = css_match_count(html, sel)
                if count == -1:
                    icon = "⏭️"
                    detail = "Playwright-only 语法"
                elif count == -2:
                    icon = "⚠️"
                    detail = "HTML 解析失败"
                elif count == -3:
                    icon = "❌"
                    detail = "lxml 未安装"
                    print(f"    {icon} lxml 未安装，无法继续")
                    sys.exit(1)
                elif count == 0:
                    icon = "❌"
                    detail = "未命中"
                else:
                    icon = "✅"
                    detail = f"命中 {count} 个"

                short_sel = sel if len(sel) <= 60 else sel[:57] + "..."
                print(f"    {icon} {name:30s} | {detail:20s} | {short_sel}")

    print(f"\n{'='*60}")
    print("图例: ✅=命中  ❌=未命中  ⏭️=Playwright专用语法(无法静态验证)")
    print(f"{'='*60}")

if __name__ == "__main__":
    main()
