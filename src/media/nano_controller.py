import asyncio
from typing import Callable, Optional, List
from playwright.async_api import Page as AsyncPage, Locator, expect as expect_async
from config.nano_selectors import (
    NANO_PAGE_URL_TEMPLATE,
    NANO_SUPPORTED_MODELS,
    NANO_IMAGE_CHUNK_SELECTOR,
    NANO_IMAGE_DOWNLOAD_BUTTON_SELECTOR,
    NANO_SETTINGS_ASPECT_RATIO_DROPDOWN_SELECTOR,
)
from config.selectors import (
    PROMPT_TEXTAREA_SELECTOR,
    PROMPT_TEXTAREA_SELECTORS,
    SUBMIT_BUTTON_SELECTOR,
    SUBMIT_BUTTON_SELECTORS,
    INSERT_BUTTON_SELECTOR,
    INSERT_BUTTON_SELECTORS,
    UPLOAD_BUTTON_SELECTOR,
    LOADING_SPINNER_SELECTORS,
)
from config.timeouts import (
    MAX_RETRIES,
    SLEEP_RETRY,
    SLEEP_SHORT,
    SLEEP_MEDIUM,
    SLEEP_LONG,
    SLEEP_TICK,
    SLEEP_IMAGE_UPLOAD,
    TIMEOUT_PAGE_NAVIGATION,
    TIMEOUT_ELEMENT_ATTACHED,
    TIMEOUT_ELEMENT_ENABLED,
    TIMEOUT_DOWNLOAD,
    TIMEOUT_INNER_TEXT,
)
from browser.operations import safe_click
from browser.selector_utils import wait_for_any_selector, get_first_visible_locator
from .models import NanoBananaConfig, GeneratedImage, GeneratedContent
from .image_utils import extract_image_from_locator
from models import ClientDisconnectedError
from debug.dom_snapshot import dump_page


class NanoController:
    def __init__(self, page: AsyncPage, logger, req_id: str):
        self.page = page
        self.logger = logger
        self.req_id = req_id

    async def _check_disconnect(self, check_client_disconnected: Callable, stage: str):
        if check_client_disconnected(stage):
            raise ClientDisconnectedError(
                f"[{self.req_id}] Client disconnected at stage: {stage}"
            )

    async def navigate_to_nano_page(
        self, model: str, check_client_disconnected: Callable
    ):
        if model not in NANO_SUPPORTED_MODELS:
            model = NANO_SUPPORTED_MODELS[0]
        url = NANO_PAGE_URL_TEMPLATE.format(model=model)
        self.logger.info(f"[{self.req_id}] 🖼️ 导航到 Nano Banana 页面: {url}")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.goto(
                    url, timeout=TIMEOUT_PAGE_NAVIGATION, wait_until="domcontentloaded"
                )
                await self._check_disconnect(
                    check_client_disconnected, "Nano 页面导航后"
                )
                locator, matched = await wait_for_any_selector(
                    self.page,
                    PROMPT_TEXTAREA_SELECTORS,
                    timeout=TIMEOUT_ELEMENT_ATTACHED,
                )
                if locator:
                    self.logger.info(
                        f"[{self.req_id}] ✅ Nano Banana 页面已加载 (匹配: {matched})"
                    )
                    await dump_page(self.page, f"nano_nav_{self.req_id}", self.logger)
                    return
                raise Exception("未找到输入框")
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] Nano 页面加载失败 (尝试 {attempt}): {e}"
                )
                if attempt < MAX_RETRIES:
                    await asyncio.sleep(SLEEP_RETRY)
        raise Exception(f"Nano Banana 页面加载失败，已重试 {MAX_RETRIES} 次")

    async def set_aspect_ratio(
        self, aspect_ratio: str, check_client_disconnected: Callable
    ):
        self.logger.info(f"[{self.req_id}] 设置宽高比: {aspect_ratio}")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                dropdown = self.page.locator(
                    NANO_SETTINGS_ASPECT_RATIO_DROPDOWN_SELECTOR
                )
                if await dropdown.count() == 0:
                    self.logger.warning(f"[{self.req_id}] 未找到宽高比下拉框")
                    await dump_page(
                        self.page, f"nano_no_ratio_dropdown_{self.req_id}", self.logger
                    )
                    return
                if not await safe_click(dropdown, "宽高比下拉框", self.req_id):
                    continue
                await asyncio.sleep(SLEEP_SHORT)
                option = self.page.locator(f'mat-option:has-text("{aspect_ratio}")')
                if await option.count() > 0:
                    if await safe_click(
                        option.first, f"宽高比选项 {aspect_ratio}", self.req_id
                    ):
                        self.logger.info(
                            f"[{self.req_id}] ✅ 宽高比已设置: {aspect_ratio}"
                        )
                        await dump_page(
                            self.page, f"nano_ratio_set_{self.req_id}", self.logger
                        )
                        return
                else:
                    self.logger.warning(
                        f"[{self.req_id}] 未找到宽高比选项: {aspect_ratio}"
                    )
                    await self.page.keyboard.press("Escape")
                    return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 设置宽高比失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)
        self.logger.warning(f"[{self.req_id}] 宽高比设置失败，使用默认值")

    async def upload_image(
        self, image_bytes: bytes, mime_type: str, check_client_disconnected: Callable
    ):
        self.logger.info(f"[{self.req_id}] 上传参考图片 ({len(image_bytes)} bytes)")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                insert_btn_locator, _ = await get_first_visible_locator(
                    self.page, INSERT_BUTTON_SELECTORS
                )
                if not insert_btn_locator:
                    self.logger.warning(f"[{self.req_id}] 未找到插入按钮")
                    return
                if not await safe_click(insert_btn_locator, "插入按钮", self.req_id):
                    if attempt < MAX_RETRIES:
                        continue
                    return
                await asyncio.sleep(SLEEP_MEDIUM)
                await self._check_disconnect(
                    check_client_disconnected, "插入菜单展开后"
                )

                ext = "png" if "png" in mime_type else "jpg"
                file_input = self.page.locator('input[type="file"]')
                await file_input.set_input_files(
                    {
                        "name": f"input_image.{ext}",
                        "mimeType": mime_type,
                        "buffer": image_bytes,
                    }
                )
                await asyncio.sleep(SLEEP_IMAGE_UPLOAD)
                await self.page.keyboard.press("Escape")
                await asyncio.sleep(SLEEP_MEDIUM)
                self.logger.info(f"[{self.req_id}] ✅ 图片已上传")
                await dump_page(
                    self.page, f"nano_img_uploaded_{self.req_id}", self.logger
                )
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 上传图片失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_MEDIUM)

    async def fill_prompt(self, prompt: str, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 填充提示词 ({len(prompt)} chars)")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.keyboard.press("Escape")
                await asyncio.sleep(SLEEP_SHORT)
                text_input_locator, _ = await get_first_visible_locator(
                    self.page, PROMPT_TEXTAREA_SELECTORS
                )
                if not text_input_locator:
                    raise Exception("未找到输入框")
                await safe_click(text_input_locator, "输入框", self.req_id)
                await text_input_locator.fill(prompt)
                await asyncio.sleep(SLEEP_TICK)
                actual = await text_input_locator.input_value()
                if prompt in actual or actual in prompt:
                    self.logger.info(f"[{self.req_id}] ✅ 提示词已填充")
                    await dump_page(
                        self.page, f"nano_prompt_{self.req_id}", self.logger
                    )
                    return
                self.logger.warning(
                    f"[{self.req_id}] 提示词填充验证失败 (尝试 {attempt})"
                )
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 填充提示词失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)
        raise Exception("填充提示词失败")

    async def run_generation(self, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 🚀 开始生成...")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.keyboard.press("Escape")
                await asyncio.sleep(SLEEP_SHORT)
                run_btn, matched = await wait_for_any_selector(
                    self.page, SUBMIT_BUTTON_SELECTORS, timeout=TIMEOUT_ELEMENT_ENABLED
                )
                if not run_btn:
                    raise Exception("未找到Run按钮")
                self.logger.info(f"[{self.req_id}] 找到Run按钮 (匹配: {matched})")
                await expect_async(run_btn).to_be_enabled(
                    timeout=TIMEOUT_ELEMENT_ENABLED
                )
                if not await safe_click(run_btn, "Run 按钮", self.req_id):
                    if attempt < MAX_RETRIES:
                        continue
                    raise Exception("Run 按钮点击失败")
                await self._check_disconnect(
                    check_client_disconnected, "Run 按钮点击后"
                )
                self.logger.info(f"[{self.req_id}] ✅ 生成已启动")
                await dump_page(self.page, f"nano_run_{self.req_id}", self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 点击 Run 失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_LONG)
        raise Exception("点击 Run 按钮失败")

    async def wait_for_content(
        self, check_client_disconnected: Callable, timeout_seconds: int = 120
    ) -> GeneratedContent:
        self.logger.info(f"[{self.req_id}] ⏳ 等待内容生成...")
        response_locator = self.page.locator("ms-chat-turn ms-cmark-node")
        image_chunk_locator = self.page.locator(NANO_IMAGE_CHUNK_SELECTOR)
        start_time = asyncio.get_event_loop().time()

        result = GeneratedContent()
        last_chunk_count = 0
        stable_count = 0
        no_progress_count = 0

        while True:
            elapsed = asyncio.get_event_loop().time() - start_time
            if elapsed > timeout_seconds:
                await dump_page(self.page, f"nano_timeout_{self.req_id}", self.logger)
                if result.images or result.text:
                    self.logger.info(
                        f"[{self.req_id}] ⚠️ 超时但有内容，返回已获取的内容"
                    )
                    return result
                raise TimeoutError(f"内容生成超时 ({timeout_seconds}s)")
            await self._check_disconnect(
                check_client_disconnected, f"等待内容 ({int(elapsed)}s)"
            )

            try:
                error_detected = await self._check_for_error()
                if error_detected:
                    self.logger.error(
                        f"[{self.req_id}] ❌ 检测到生成错误: {error_detected}"
                    )
                    raise Exception(f"生成失败: {error_detected}")
                chunk_count = await image_chunk_locator.count()
                self.logger.info(f"[{self.req_id}] 检测到图片块数量: {chunk_count}")

                text_count = await response_locator.count()
                if text_count > 0:
                    texts = []
                    for i in range(text_count):
                        txt = await response_locator.nth(i).inner_text()
                        if txt.strip():
                            texts.append(txt.strip())
                    if texts:
                        result.text = "\n".join(texts)

                is_generating = False
                spinner_selectors = LOADING_SPINNER_SELECTORS + [
                    "mat-spinner",
                    ".loading-spinner",
                    "ms-chat-turn.model .thinking-indicator",
                ]
                for sel in spinner_selectors:
                    if await self.page.locator(sel).count() > 0:
                        is_generating = True
                        break

                stop_btn = self.page.locator('button[aria-label="Stop"]')
                if await stop_btn.count() > 0 and await stop_btn.is_visible():
                    is_generating = True

                if chunk_count > 0 and not is_generating:
                    if chunk_count == last_chunk_count:
                        stable_count += 1
                        if stable_count >= 2:
                            await dump_page(
                                self.page, f"nano_content_{self.req_id}", self.logger
                            )
                            self.logger.info(f"[{self.req_id}] 🔽 开始提取图片...")
                            images = await self._extract_images(chunk_count)
                            if images:
                                result.images = images
                            self.logger.info(f"[{self.req_id}] ✅ 内容生成完成")
                            return result
                    else:
                        stable_count = 0
                    last_chunk_count = chunk_count
                elif not is_generating and chunk_count == 0 and not result.text:
                    no_progress_count += 1
                    if no_progress_count >= 40:
                        self.logger.warning(
                            f"[{self.req_id}] ⚠️ 长时间无进展，可能生成失败"
                        )
                        raise Exception("生成无响应，可能失败")
                else:
                    no_progress_count = 0

            except Exception as e:
                if "ClientDisconnected" in str(type(e).__name__):
                    raise
                if "生成失败" in str(e) or "生成无响应" in str(e):
                    raise
                self.logger.warning(f"[{self.req_id}] 检查内容时出错: {e}")

            await asyncio.sleep(SLEEP_MEDIUM)

    async def _extract_images(self, count: int) -> List[GeneratedImage]:
        images = []
        image_chunk_locator = self.page.locator(NANO_IMAGE_CHUNK_SELECTOR)

        for i in range(count):
            try:
                chunk = image_chunk_locator.nth(i)
                img = chunk.locator("img.loaded-image")
                if await img.count() == 0:
                    img = chunk.locator("img")
                if await img.count() == 0:
                    continue

                extracted = await extract_image_from_locator(img.first)
                if not extracted:
                    continue

                src, image_bytes, mime_type = extracted
                self.logger.info(
                    f"[{self.req_id}] ✅ 图片 {i} 直接提取成功 ({src[:100]})"
                )
                images.append(
                    GeneratedImage(
                        image_bytes=image_bytes, mime_type=mime_type, index=i
                    )
                )
            except Exception as e:
                self.logger.warning(f"[{self.req_id}] 图片 {i} 直接提取失败: {e}")

        if len(images) >= count:
            return images

        self.logger.warning(
            f"[{self.req_id}] 直接提取仅成功 {len(images)}/{count} 张，回退下载按钮方案"
        )
        fallback_images = await self._extract_images_via_download(count)
        if len(fallback_images) > len(images):
            return fallback_images
        return images

    async def _check_for_error(self):
        error_selectors = [
            "mat-snack-bar-container .mdc-snackbar__label",
            ".error-toast span.content-text",
            'ms-callout[severity="error"] .content-container',
            ".mat-mdc-snack-bar-label",
        ]
        for sel in error_selectors:
            try:
                locator = self.page.locator(sel)
                if await locator.count() > 0 and await locator.first.is_visible():
                    text = await locator.first.inner_text(timeout=TIMEOUT_INNER_TEXT)
                    if text and text.strip():
                        return text.strip()
            except:
                pass
        return None

    async def _extract_images_via_download(self, count: int) -> List[GeneratedImage]:
        import tempfile
        import os

        images = []
        image_chunk_locator = self.page.locator(NANO_IMAGE_CHUNK_SELECTOR)

        for i in range(count):
            try:
                chunk = image_chunk_locator.nth(i)

                img = chunk.locator("img")
                if await img.count() > 0:
                    await img.first.hover()
                    await asyncio.sleep(SLEEP_MEDIUM)
                    await chunk.evaluate(
                        'el => el.dispatchEvent(new MouseEvent("mouseenter", {bubbles: true}))'
                    )
                    await asyncio.sleep(SLEEP_SHORT)

                download_btn = chunk.locator('button[aria-label="Download"]')
                if await download_btn.count() == 0:
                    download_btn = chunk.locator("button.download-button")
                if await download_btn.count() == 0:
                    download_btn = chunk.locator("button")

                if await download_btn.count() == 0:
                    self.logger.warning(f"[{self.req_id}] 图片 {i}: 未找到下载按钮")
                    continue

                temp_dir = tempfile.gettempdir()
                temp_file = os.path.join(temp_dir, f"nano_image_{self.req_id}_{i}.png")

                try:
                    async with self.page.expect_download(
                        timeout=TIMEOUT_DOWNLOAD
                    ) as download_info:
                        await download_btn.first.evaluate("el => el.click()")

                    download = await download_info.value
                    await download.save_as(temp_file)

                    if os.path.exists(temp_file):
                        with open(temp_file, "rb") as f:
                            image_bytes = f.read()
                        os.remove(temp_file)
                        images.append(
                            GeneratedImage(
                                image_bytes=image_bytes, mime_type="image/png", index=i
                            )
                        )
                        self.logger.info(
                            f"[{self.req_id}] ✅ 图片 {i} 下载成功 ({len(image_bytes)} bytes)"
                        )
                except Exception as dl_err:
                    self.logger.warning(f"[{self.req_id}] 图片 {i} 下载失败: {dl_err}")

            except Exception as e:
                self.logger.warning(f"[{self.req_id}] 提取图片 {i} 失败: {e}")

        return images
