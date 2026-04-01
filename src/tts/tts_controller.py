import asyncio
from typing import Callable, Optional
from playwright.async_api import Page as AsyncPage, Locator, expect as expect_async, TimeoutError as PlaywrightTimeoutError
from config.tts_selectors import (
    TTS_ROOT_SELECTOR, TTS_RUN_BUTTON_SELECTOR, TTS_RUN_BUTTON_SELECTORS,
    TTS_AUDIO_PLAYER_SELECTOR,
    TTS_SINGLE_SPEAKER_TEXT_INPUT_SELECTOR, TTS_SINGLE_SPEAKER_STYLE_INPUT_SELECTOR,
    TTS_MULTI_SPEAKER_RAW_INPUT_SELECTOR, TTS_SETTINGS_MODE_SELECTOR_CONTAINER,
    TTS_SETTINGS_SINGLE_SPEAKER_MODE_BUTTON, TTS_SETTINGS_MULTI_SPEAKER_MODE_BUTTON,
    TTS_SETTINGS_VOICE_SELECT_DROPDOWN_SELECTOR, TTS_SETTINGS_VOICE_OPTION_SELECTOR,
    TTS_PAGE_URL_TEMPLATE, TTS_SUPPORTED_MODELS
)
from config.timeouts import (
    MAX_RETRIES, SLEEP_RETRY, SLEEP_SHORT, SLEEP_MEDIUM, SLEEP_LONG, SLEEP_TICK,
    TIMEOUT_PAGE_NAVIGATION, TIMEOUT_ELEMENT_ATTACHED, TIMEOUT_ELEMENT_VISIBLE,
    TIMEOUT_SELECTOR_MATCH, DELAY_AFTER_FILL
)
from browser.operations import safe_click
from browser.selector_utils import wait_for_any_selector
from .models import SpeechConfig
from models import ClientDisconnectedError
from debug.dom_snapshot import dump_page


class TTSController:

    def __init__(self, page: AsyncPage, logger, req_id: str):
        self.page = page
        self.logger = logger
        self.req_id = req_id

    async def _check_disconnect(self, check_client_disconnected: Callable, stage: str):
        if check_client_disconnected(stage):
            raise ClientDisconnectedError(f'[{self.req_id}] Client disconnected at stage: {stage}')


    async def navigate_to_tts_page(self, model: str, check_client_disconnected: Callable):
        if model not in TTS_SUPPORTED_MODELS:
            model = TTS_SUPPORTED_MODELS[0]
        url = TTS_PAGE_URL_TEMPLATE.format(model=model)
        self.logger.info(f'[{self.req_id}] 🎤 导航到 TTS 页面: {url}')
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.goto(url, timeout=TIMEOUT_PAGE_NAVIGATION, wait_until='domcontentloaded')
                await self._check_disconnect(check_client_disconnected, 'TTS 页面导航后')
                tts_root = self.page.locator(TTS_ROOT_SELECTOR)
                await expect_async(tts_root).to_be_visible(timeout=TIMEOUT_ELEMENT_ATTACHED)
                self.logger.info(f'[{self.req_id}] ✅ TTS 页面已加载')
                await dump_page(self.page, f'tts_nav_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] TTS 页面加载失败 (尝试 {attempt}): {e}')
                if attempt < MAX_RETRIES:
                    await asyncio.sleep(SLEEP_RETRY)
        raise Exception(f'TTS 页面加载失败，已重试 {MAX_RETRIES} 次')

    async def set_tts_mode(self, is_multi_speaker: bool, check_client_disconnected: Callable):
        mode_name = '多说话人' if is_multi_speaker else '单说话人'
        self.logger.info(f'[{self.req_id}] 设置 TTS 模式: {mode_name}')
        selector = TTS_SETTINGS_MULTI_SPEAKER_MODE_BUTTON if is_multi_speaker else TTS_SETTINGS_SINGLE_SPEAKER_MODE_BUTTON
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                mode_btn = self.page.locator(selector).first
                await expect_async(mode_btn).to_be_visible(timeout=TIMEOUT_ELEMENT_VISIBLE)
                
                btn_class = await mode_btn.get_attribute('class') or ''
                is_active = 'ms-button-active' in btn_class
                
                if is_active:
                    self.logger.info(f'[{self.req_id}] ✅ TTS 模式已就绪: {mode_name}')
                    await dump_page(self.page, f'tts_mode_ready_{mode_name}_{self.req_id}', self.logger)
                    return
                
                if not await safe_click(mode_btn, f'TTS 模式按钮 {mode_name}', self.req_id):
                    continue
                await self._check_disconnect(check_client_disconnected, f'TTS 模式切换后')
                await asyncio.sleep(SLEEP_LONG)
                
                new_class = await mode_btn.get_attribute('class') or ''
                is_now_active = 'ms-button-active' in new_class
                
                if not is_now_active:
                    self.logger.warning(f'[{self.req_id}] 按钮 class 验证失败，检查输入框可见性...')
                
                if is_multi_speaker:
                    raw_input = self.page.locator(TTS_MULTI_SPEAKER_RAW_INPUT_SELECTOR)
                    if await raw_input.count() > 0 and await raw_input.is_visible():
                        self.logger.info(f'[{self.req_id}] ✅ TTS 模式已切换: {mode_name} (通过输入框验证)')
                        await dump_page(self.page, f'tts_mode_switched_{mode_name}_{self.req_id}', self.logger)
                        return
                else:
                    text_input = self.page.locator(TTS_SINGLE_SPEAKER_TEXT_INPUT_SELECTOR)
                    if await text_input.count() > 0 and await text_input.is_visible():
                        self.logger.info(f'[{self.req_id}] ✅ TTS 模式已切换: {mode_name} (通过输入框验证)')
                        await dump_page(self.page, f'tts_mode_switched_{mode_name}_{self.req_id}', self.logger)
                        return
                
                self.logger.warning(f'[{self.req_id}] TTS 模式切换验证失败 (尝试 {attempt})')
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] TTS 模式切换失败 (尝试 {attempt}): {e}')
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_MEDIUM)
        raise Exception(f'TTS 模式切换失败: {mode_name}')

    async def set_voice(self, voice_name: str, speaker_index: int = 0, check_client_disconnected: Callable = None):
        self.logger.info(f'[{self.req_id}] 设置语音: {voice_name} (说话人 {speaker_index})')
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                voice_dropdowns = self.page.locator(TTS_SETTINGS_VOICE_SELECT_DROPDOWN_SELECTOR)
                dropdown_count = await voice_dropdowns.count()
                if dropdown_count == 0:
                    self.logger.warning(f'[{self.req_id}] 未找到语音选择下拉框')
                    return
                target_dropdown = voice_dropdowns.nth(speaker_index) if dropdown_count > speaker_index else voice_dropdowns.first
                if not await safe_click(target_dropdown, f'语音下拉框 {speaker_index}', self.req_id):
                    continue
                await asyncio.sleep(SLEEP_SHORT)
                option = self.page.locator(f'{TTS_SETTINGS_VOICE_OPTION_SELECTOR}:has-text("{voice_name}")')
                try:
                    await expect_async(option.first).to_be_visible(timeout=TIMEOUT_SELECTOR_MATCH)
                except PlaywrightTimeoutError:
                    self.logger.warning(f'[{self.req_id}] 语音选项 {voice_name} 未出现 (尝试 {attempt})')
                    await self.page.keyboard.press('Escape')
                    await asyncio.sleep(DELAY_AFTER_FILL)
                    continue
                if await safe_click(option.first, f'语音选项 {voice_name}', self.req_id):
                    await asyncio.sleep(SLEEP_SHORT)
                    self.logger.info(f'[{self.req_id}] ✅ 语音已设置: {voice_name}')
                    await dump_page(self.page, f'tts_voice_set_{voice_name}_{self.req_id}', self.logger)
                    return
                else:
                    self.logger.warning(f'[{self.req_id}] 语音选项点击失败 (尝试 {attempt})')
                    await self.page.keyboard.press('Escape')
                    await asyncio.sleep(DELAY_AFTER_FILL)
                    continue
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 设置语音失败 (尝试 {attempt}): {e}')
                try:
                    await self.page.keyboard.press('Escape')
                except:
                    pass
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)

    async def fill_single_speaker_text(self, text: str, style_instructions: str = '', check_client_disconnected: Callable = None):
        self.logger.info(f'[{self.req_id}] 填充单说话人文本 ({len(text)} chars)')
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                text_input = self.page.locator(TTS_SINGLE_SPEAKER_TEXT_INPUT_SELECTOR)
                await expect_async(text_input).to_be_visible(timeout=TIMEOUT_ELEMENT_VISIBLE)
                await text_input.fill(text)
                await asyncio.sleep(SLEEP_TICK)
                actual = await text_input.input_value()
                if actual == text:
                    self.logger.info(f'[{self.req_id}] ✅ 文本已填充')
                    if style_instructions:
                        style_input = self.page.locator(TTS_SINGLE_SPEAKER_STYLE_INPUT_SELECTOR)
                        if await style_input.count() > 0:
                            await style_input.fill(style_instructions)
                    await dump_page(self.page, f'tts_text_filled_{self.req_id}', self.logger)
                    return
                self.logger.warning(f'[{self.req_id}] 文本填充验证失败 (尝试 {attempt})')
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 填充文本失败 (尝试 {attempt}): {e}')
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)

    async def fill_multi_speaker_text(self, raw_script: str, check_client_disconnected: Callable = None):
        self.logger.info(f'[{self.req_id}] 填充多说话人脚本 ({len(raw_script)} chars)')
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                raw_input = self.page.locator(TTS_MULTI_SPEAKER_RAW_INPUT_SELECTOR)
                await expect_async(raw_input).to_be_visible(timeout=TIMEOUT_ELEMENT_VISIBLE)
                await raw_input.fill(raw_script)
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f'[{self.req_id}] ✅ 多说话人脚本已填充')
                await dump_page(self.page, f'tts_multi_filled_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 填充脚本失败 (尝试 {attempt}): {e}')
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)

    async def run_generation(self, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 🚀 开始生成语音...')
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.keyboard.press('Escape')
                await asyncio.sleep(SLEEP_SHORT)
                run_btn, matched = await wait_for_any_selector(self.page, TTS_RUN_BUTTON_SELECTORS, timeout=TIMEOUT_ELEMENT_VISIBLE)
                if not run_btn:
                    raise Exception('未找到Run按钮')
                self.logger.info(f'[{self.req_id}] 找到Run按钮 (匹配: {matched})')
                await expect_async(run_btn).to_be_enabled(timeout=TIMEOUT_ELEMENT_VISIBLE)
                if not await safe_click(run_btn, 'Run 按钮', self.req_id):
                    if attempt < MAX_RETRIES:
                        continue
                    raise Exception('Run 按钮点击失败')
                await self._check_disconnect(check_client_disconnected, 'TTS Run 按钮点击后')
                self.logger.info(f'[{self.req_id}] ✅ 生成已启动')
                await dump_page(self.page, f'tts_run_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 点击 Run 失败 (尝试 {attempt}): {e}')
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)
        raise Exception('点击 Run 按钮失败')

    async def wait_for_audio(self, check_client_disconnected: Callable, timeout_seconds: int = 120) -> str:
        self.logger.info(f'[{self.req_id}] ⏳ 等待音频生成...')
        audio_player = self.page.locator(TTS_AUDIO_PLAYER_SELECTOR)
        start_time = asyncio.get_event_loop().time()
        last_src = ''
        while True:
            elapsed = asyncio.get_event_loop().time() - start_time
            if elapsed > timeout_seconds:
                await dump_page(self.page, f'tts_timeout_{self.req_id}', self.logger)
                raise TimeoutError(f'音频生成超时 ({timeout_seconds}s)')
            await self._check_disconnect(check_client_disconnected, f'等待音频 ({int(elapsed)}s)')
            try:
                if await audio_player.count() > 0:
                    src = await audio_player.get_attribute('src') or ''
                    if src and src.startswith('data:audio/') and src != last_src:
                        self.logger.info(f'[{self.req_id}] ✅ 音频已生成 ({len(src)} bytes)')
                        await dump_page(self.page, f'tts_audio_found_{self.req_id}', self.logger)
                        if ',' in src:
                            base64_data = src.split(',', 1)[1]
                            return base64_data
                        return src
                    last_src = src
            except Exception as e:
                self.logger.warning(f'[{self.req_id}] 检查音频元素时出错: {e}')
            await asyncio.sleep(SLEEP_RETRY)

