import asyncio
import base64
import tempfile
import os
from typing import Callable, Optional, List
from playwright.async_api import Page as AsyncPage, Locator, expect as expect_async
from config.veo_selectors import (
    VEO_PAGE_URL_TEMPLATE, VEO_SUPPORTED_MODELS, VEO_ROOT_SELECTOR,
    VEO_PROMPT_INPUT_SELECTOR, VEO_RUN_BUTTON_SELECTOR, VEO_ADD_MEDIA_BUTTON_SELECTOR,
    VEO_GALLERY_CONTAINER_SELECTOR, VEO_GALLERY_ITEM_SELECTOR,
    VEO_GENERATED_VIDEO_SELECTOR, VEO_VIDEO_DOWNLOAD_BUTTON_SELECTOR,
    VEO_SETTINGS_NUM_RESULTS_INPUT_SELECTOR, VEO_SETTINGS_ASPECT_RATIO_BUTTON_SELECTOR,
    VEO_SETTINGS_DURATION_DROPDOWN_SELECTOR, VEO_SETTINGS_NEGATIVE_PROMPT_SELECTOR
)
from config.timeouts import (
    MAX_RETRIES, SLEEP_RETRY, SLEEP_SHORT, SLEEP_MEDIUM, SLEEP_LONG, SLEEP_TICK,
    SLEEP_VIDEO_POLL, TIMEOUT_PAGE_NAVIGATION, TIMEOUT_ELEMENT_ATTACHED,
    TIMEOUT_ELEMENT_ENABLED, TIMEOUT_DOWNLOAD_VIDEO, DELAY_AFTER_TOGGLE
)
from browser.operations import safe_click
from .models import VideoGenerationConfig, GeneratedVideo
from models import ClientDisconnectedError
from debug.dom_snapshot import dump_page


class VeoController:

    def __init__(self, page: AsyncPage, logger, req_id: str):
        self.page = page
        self.logger = logger
        self.req_id = req_id

    async def _check_disconnect(self, check_client_disconnected: Callable, stage: str):
        if check_client_disconnected(stage):
            raise ClientDisconnectedError(f'[{self.req_id}] Client disconnected at stage: {stage}')


    async def navigate_to_veo_page(self, model: str, check_client_disconnected: Callable):
        if model not in VEO_SUPPORTED_MODELS:
            model = VEO_SUPPORTED_MODELS[0]
        url = VEO_PAGE_URL_TEMPLATE.format(model=model)
        self.logger.info(f'[{self.req_id}] 🎬 导航到 Veo 页面: {url}')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                await self.page.goto(url, timeout=TIMEOUT_PAGE_NAVIGATION, wait_until='domcontentloaded')
                await self._check_disconnect(check_client_disconnected, 'Veo 页面导航后')
                root = self.page.locator(VEO_ROOT_SELECTOR)
                await expect_async(root).to_be_visible(timeout=TIMEOUT_ELEMENT_ATTACHED)
                self.logger.info(f'[{self.req_id}] ✅ Veo 页面已加载')
                await dump_page(self.page, f'veo_nav_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] Veo 页面加载失败 (尝试 {attempt}): {e}')
                if attempt < max_retries:
                    await asyncio.sleep(SLEEP_RETRY)
        raise Exception(f'Veo 页面加载失败，已重试 {max_retries} 次')

    async def set_number_of_videos(self, count: int, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 设置视频数量: {count}')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                input_locator = self.page.locator(VEO_SETTINGS_NUM_RESULTS_INPUT_SELECTOR)
                if await input_locator.count() == 0:
                    self.logger.warning(f'[{self.req_id}] 未找到数量输入框')
                    await dump_page(self.page, f'veo_no_count_input_{self.req_id}', self.logger)
                    return
                await input_locator.fill(str(count))
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f'[{self.req_id}] ✅ 视频数量已设置: {count}')
                await dump_page(self.page, f'veo_count_set_{count}_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 设置数量失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_SHORT)

    async def set_aspect_ratio(self, aspect_ratio: str, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 设置宽高比: {aspect_ratio}')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                btn = self.page.locator(f'{VEO_SETTINGS_ASPECT_RATIO_BUTTON_SELECTOR}:has-text("{aspect_ratio}")')
                if await btn.count() > 0:
                    if await safe_click(btn.first, f'宽高比按钮 {aspect_ratio}', self.req_id):
                        self.logger.info(f'[{self.req_id}] ✅ 宽高比已设置: {aspect_ratio}')
                        await dump_page(self.page, f'veo_ratio_set_{self.req_id}', self.logger)
                        return
                else:
                    self.logger.warning(f'[{self.req_id}] 未找到宽高比按钮: {aspect_ratio}')
                    return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 设置宽高比失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_SHORT)

    async def set_duration(self, duration_seconds: int, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 设置视频时长: {duration_seconds}s')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                dropdown = self.page.locator(VEO_SETTINGS_DURATION_DROPDOWN_SELECTOR)
                if await dropdown.count() == 0:
                    self.logger.warning(f'[{self.req_id}] 未找到时长下拉框')
                    await dump_page(self.page, f'veo_no_duration_dropdown_{self.req_id}', self.logger)
                    return
                if not await safe_click(dropdown, '时长下拉框', self.req_id):
                    continue
                await asyncio.sleep(SLEEP_SHORT)
                option = self.page.locator(f'mat-option:has-text("{duration_seconds}")')
                if await option.count() > 0:
                    if await safe_click(option.first, f'时长选项 {duration_seconds}s', self.req_id):
                        self.logger.info(f'[{self.req_id}] ✅ 视频时长已设置: {duration_seconds}s')
                        await dump_page(self.page, f'veo_duration_set_{duration_seconds}_{self.req_id}', self.logger)
                        return
                else:
                    self.logger.warning(f'[{self.req_id}] 未找到时长选项: {duration_seconds}s')
                    await self.page.keyboard.press('Escape')
                    return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 设置时长失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_SHORT)

    async def set_negative_prompt(self, negative_prompt: str, check_client_disconnected: Callable):
        if not negative_prompt:
            return
        self.logger.info(f'[{self.req_id}] 设置负面提示词')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                textarea = self.page.locator(VEO_SETTINGS_NEGATIVE_PROMPT_SELECTOR)
                if await textarea.count() == 0:
                    self.logger.warning(f'[{self.req_id}] 未找到负面提示词输入框')
                    return
                await textarea.fill(negative_prompt)
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f'[{self.req_id}] ✅ 负面提示词已设置')
                await dump_page(self.page, f'veo_neg_prompt_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 设置负面提示词失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_SHORT)

    async def upload_image(self, image_bytes: bytes, mime_type: str, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 上传参考图片 ({len(image_bytes)} bytes)')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                add_media_btn = self.page.locator(VEO_ADD_MEDIA_BUTTON_SELECTOR)
                if await add_media_btn.count() == 0:
                    self.logger.warning(f'[{self.req_id}] 未找到添加媒体按钮')
                    return
                if not await safe_click(add_media_btn, '添加媒体按钮', self.req_id):
                    if attempt < max_retries:
                        continue
                    return
                await asyncio.sleep(SLEEP_MEDIUM)
                await self._check_disconnect(check_client_disconnected, '添加媒体按钮点击后')
                
                ext = 'png' if 'png' in mime_type else 'jpg'
                file_input = self.page.locator('input[type="file"]')
                if await file_input.count() > 0:
                    await file_input.set_input_files({
                        'name': f'input_image.{ext}',
                        'mimeType': mime_type,
                        'buffer': image_bytes
                    })
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
                    await self.page.keyboard.press('Escape')
                    await asyncio.sleep(SLEEP_SHORT)
                    self.logger.info(f'[{self.req_id}] ✅ 图片已上传')
                    await dump_page(self.page, f'veo_img_uploaded_{self.req_id}', self.logger)
                    return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 上传图片失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_SHORT)

    async def fill_prompt(self, prompt: str, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 填充提示词 ({len(prompt)} chars)')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                await self.page.keyboard.press('Escape')
                await asyncio.sleep(SLEEP_SHORT)
                text_input = self.page.locator(VEO_PROMPT_INPUT_SELECTOR)
                await safe_click(text_input, '输入框', self.req_id)
                await text_input.fill(prompt)
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f'[{self.req_id}] ✅ 提示词已填充')
                await dump_page(self.page, f'veo_prompt_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 填充提示词失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_SHORT)
        raise Exception('填充提示词失败')

    async def run_generation(self, check_client_disconnected: Callable):
        self.logger.info(f'[{self.req_id}] 🚀 开始生成视频...')
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                await self.page.keyboard.press('Escape')
                await asyncio.sleep(SLEEP_SHORT)
                run_btn = self.page.locator(VEO_RUN_BUTTON_SELECTOR)
                await expect_async(run_btn).to_be_visible(timeout=TIMEOUT_ELEMENT_ENABLED)
                await expect_async(run_btn).to_be_enabled(timeout=TIMEOUT_ELEMENT_ENABLED)
                if not await safe_click(run_btn, 'Run 按钮', self.req_id):
                    if attempt < max_retries:
                        continue
                    raise Exception('Run 按钮点击失败')
                await self._check_disconnect(check_client_disconnected, 'Run 按钮点击后')
                self.logger.info(f'[{self.req_id}] ✅ 生成已启动')
                await dump_page(self.page, f'veo_run_{self.req_id}', self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(f'[{self.req_id}] 点击 Run 失败 (尝试 {attempt}): {e}')
            if attempt < max_retries:
                await asyncio.sleep(SLEEP_LONG)
        raise Exception('点击 Run 按钮失败')

    async def wait_for_videos(self, expected_count: int, check_client_disconnected: Callable, timeout_seconds: int = 300) -> List[GeneratedVideo]:
        self.logger.info(f'[{self.req_id}] ⏳ 等待视频生成 (期望 {expected_count} 个)...')
        video_item_locator = self.page.locator(VEO_GALLERY_ITEM_SELECTOR)
        start_time = asyncio.get_event_loop().time()
        
        while True:
            elapsed = asyncio.get_event_loop().time() - start_time
            if elapsed > timeout_seconds:
                await dump_page(self.page, f'veo_timeout_{self.req_id}', self.logger)
                raise TimeoutError(f'视频生成超时 ({timeout_seconds}s)')
            await self._check_disconnect(check_client_disconnected, f'等待视频 ({int(elapsed)}s)')
            
            try:
                current_count = await video_item_locator.count()
                self.logger.info(f'[{self.req_id}] 当前视频项数量: {current_count}')
                
                if current_count >= expected_count:
                    ready_count = 0
                    for i in range(current_count):
                        if await self._is_video_ready(i):
                            ready_count += 1
                    
                    self.logger.info(f'[{self.req_id}] 准备好的视频: {ready_count}/{current_count}')
                    
                    if ready_count >= expected_count:
                        videos = []
                        for i in range(expected_count):
                            video_bytes = await self._download_video(i, check_client_disconnected)
                            if video_bytes:
                                videos.append(GeneratedVideo(
                                    video_bytes=video_bytes,
                                    mime_type='video/mp4',
                                    index=i
                                ))
                        if len(videos) >= expected_count:
                            await dump_page(self.page, f'veo_done_{self.req_id}', self.logger)
                            self.logger.info(f'[{self.req_id}] ✅ 视频生成完成 ({len(videos)} 个)')
                            return videos
            except Exception as e:
                self.logger.warning(f'[{self.req_id}] 检查视频时出错: {e}')
            
            await asyncio.sleep(SLEEP_VIDEO_POLL)

    async def _download_video(self, index: int, check_client_disconnected: Callable) -> Optional[bytes]:
        try:
            download_btn = self.page.locator(VEO_VIDEO_DOWNLOAD_BUTTON_SELECTOR).nth(index)
            btn_count = await download_btn.count()
            if btn_count == 0:
                self.logger.warning(f'[{self.req_id}] 未找到下载按钮 (索引 {index})')
                return None
            
            if not await download_btn.is_visible():
                self.logger.warning(f'[{self.req_id}] 下载按钮不可见 (索引 {index})')
                return None
            
            temp_dir = tempfile.gettempdir()
            temp_file = os.path.join(temp_dir, f'veo_video_{self.req_id}_{index}.mp4')
            
            try:
                async with self.page.expect_download(timeout=TIMEOUT_DOWNLOAD_VIDEO) as download_info:
                    await download_btn.click()
                
                download = await download_info.value
                await download.save_as(temp_file)
                
                if os.path.exists(temp_file):
                    with open(temp_file, 'rb') as f:
                        video_bytes = f.read()
                    os.remove(temp_file)
                    self.logger.info(f'[{self.req_id}] ✅ 视频下载成功 ({len(video_bytes)} bytes)')
                    return video_bytes
            except Exception as download_error:
                self.logger.warning(f'[{self.req_id}] expect_download 失败: {download_error}')
                video_element = self.page.locator(VEO_GENERATED_VIDEO_SELECTOR).nth(index)
                if await video_element.count() > 0:
                    src = await video_element.get_attribute('src') or ''
                    if src.startswith('data:video/'):
                        if ',' in src:
                            base64_data = src.split(',', 1)[1]
                            video_bytes = base64.b64decode(base64_data)
                            self.logger.info(f'[{self.req_id}] ✅ 从 data URL 提取视频成功 ({len(video_bytes)} bytes)')
                            return video_bytes
            
            return None
        except Exception as e:
            self.logger.warning(f'[{self.req_id}] 下载视频失败: {e}')
            return None


    async def _is_video_ready(self, index: int) -> bool:
        try:
            video_item = self.page.locator(VEO_GALLERY_ITEM_SELECTOR).nth(index)
            if await video_item.count() == 0:
                return False
            
            download_btn = video_item.locator('button[aria-label="Download video"]')
            if await download_btn.count() > 0 and await download_btn.is_visible():
                return True
            
            video_el = video_item.locator('video')
            if await video_el.count() > 0:
                src = await video_el.get_attribute('src') or ''
                if src and (src.startswith('blob:') or src.startswith('data:')):
                    return True
            
            return False
        except:
            return False

