import asyncio
from typing import Callable, Optional, List
from playwright.async_api import Page as AsyncPage, Locator, expect as expect_async
from config.imagen_selectors import (
    IMAGEN_PAGE_URL_TEMPLATE,
    IMAGEN_SUPPORTED_MODELS,
    IMAGEN_ROOT_SELECTOR,
    IMAGEN_PROMPT_INPUT_SELECTOR,
    IMAGEN_PROMPT_INPUT_SELECTORS,
    IMAGEN_RUN_BUTTON_SELECTOR,
    IMAGEN_RUN_BUTTON_SELECTORS,
    IMAGEN_GALLERY_CONTAINER_SELECTOR,
    IMAGEN_GALLERY_ITEM_SELECTOR,
    IMAGEN_GENERATED_IMAGE_SELECTOR,
    IMAGEN_SETTINGS_PANEL_SELECTOR,
    IMAGEN_SETTINGS_NUM_RESULTS_INPUT_SELECTOR,
    IMAGEN_SETTINGS_ASPECT_RATIO_BUTTON_SELECTOR,
    IMAGEN_SETTINGS_NEGATIVE_PROMPT_SELECTOR,
    IMAGEN_PAID_USAGE_DIALOG_SELECTOR,
    IMAGEN_PAID_USAGE_CLOSE_BUTTON_SELECTOR,
)
from config.timeouts import (
    MAX_RETRIES,
    SLEEP_RETRY,
    SLEEP_SHORT,
    SLEEP_LONG,
    SLEEP_TICK,
    TIMEOUT_PAGE_NAVIGATION,
    TIMEOUT_ELEMENT_ATTACHED,
    TIMEOUT_ELEMENT_ENABLED,
)
from browser.operations import safe_click
from browser.selector_utils import wait_for_any_selector, get_first_visible_locator
from .models import ImageGenerationConfig, GeneratedImage, PaidApiKeyRequiredError
from .image_utils import extract_image_from_locator
from models import ClientDisconnectedError
from debug.dom_snapshot import dump_page


class ImagenController:
    def __init__(self, page: AsyncPage, logger, req_id: str):
        self.page = page
        self.logger = logger
        self.req_id = req_id

    async def _check_disconnect(self, check_client_disconnected: Callable, stage: str):
        if check_client_disconnected(stage):
            raise ClientDisconnectedError(
                f"[{self.req_id}] Client disconnected at stage: {stage}"
            )

    async def _handle_paid_usage_dialog_if_visible(self) -> None:
        dialog = self.page.locator(IMAGEN_PAID_USAGE_DIALOG_SELECTOR)
        if await dialog.count() == 0 or not await dialog.first.is_visible():
            return

        self.logger.warning(f"[{self.req_id}] 检测到 paid API key 对话框，尝试关闭")
        await dump_page(self.page, f"imagen_paid_dialog_{self.req_id}", self.logger)

        close_btn = self.page.locator(IMAGEN_PAID_USAGE_CLOSE_BUTTON_SELECTOR)
        if await close_btn.count() == 0:
            raise PaidApiKeyRequiredError(
                "Imagen 页面要求绑定付费 API key，当前环境无法继续生成"
            )

        closed = await safe_click(
            close_btn.first, "Paid API key 对话框关闭按钮", self.req_id
        )
        if closed:
            await asyncio.sleep(SLEEP_SHORT)
            await dump_page(
                self.page, f"imagen_paid_dialog_closed_{self.req_id}", self.logger
            )
        raise PaidApiKeyRequiredError(
            "Imagen 页面要求绑定付费 API key，当前环境无法继续生成"
        )

    async def navigate_to_imagen_page(
        self, model: str, check_client_disconnected: Callable
    ):
        if model not in IMAGEN_SUPPORTED_MODELS:
            model = IMAGEN_SUPPORTED_MODELS[0]
        url = IMAGEN_PAGE_URL_TEMPLATE.format(model=model)
        self.logger.info(f"[{self.req_id}] 🎨 导航到 Imagen 页面: {url}")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.goto(
                    url, timeout=TIMEOUT_PAGE_NAVIGATION, wait_until="domcontentloaded"
                )
                await self._check_disconnect(
                    check_client_disconnected, "Imagen 页面导航后"
                )
                root = self.page.locator(IMAGEN_ROOT_SELECTOR)
                await expect_async(root).to_be_visible(timeout=TIMEOUT_ELEMENT_ATTACHED)
                self.logger.info(f"[{self.req_id}] ✅ Imagen 页面已加载")
                await dump_page(self.page, f"imagen_nav_{self.req_id}", self.logger)
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] Imagen 页面加载失败 (尝试 {attempt}): {e}"
                )
                if attempt < MAX_RETRIES:
                    await asyncio.sleep(SLEEP_RETRY)
        raise Exception(f"Imagen 页面加载失败，已重试 {MAX_RETRIES} 次")

    async def set_number_of_images(
        self, count: int, check_client_disconnected: Callable
    ):
        self.logger.info(f"[{self.req_id}] 设置图片数量: {count}")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                input_locator = self.page.locator(
                    IMAGEN_SETTINGS_NUM_RESULTS_INPUT_SELECTOR
                )
                if await input_locator.count() == 0:
                    self.logger.warning(f"[{self.req_id}] 未找到数量输入框")
                    await dump_page(
                        self.page, f"imagen_no_count_input_{self.req_id}", self.logger
                    )
                    return
                await input_locator.fill(str(count))
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f"[{self.req_id}] ✅ 图片数量已设置: {count}")
                await dump_page(
                    self.page, f"imagen_set_count_{count}_{self.req_id}", self.logger
                )
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 设置数量失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)

    async def set_aspect_ratio(
        self, aspect_ratio: str, check_client_disconnected: Callable
    ):
        self.logger.info(f"[{self.req_id}] 设置宽高比: {aspect_ratio}")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                btn = self.page.locator(
                    f'{IMAGEN_SETTINGS_ASPECT_RATIO_BUTTON_SELECTOR}:has-text("{aspect_ratio}")'
                )
                if await btn.count() > 0:
                    if await safe_click(
                        btn.first, f"宽高比按钮 {aspect_ratio}", self.req_id
                    ):
                        self.logger.info(
                            f"[{self.req_id}] ✅ 宽高比已设置: {aspect_ratio}"
                        )
                        await dump_page(
                            self.page, f"imagen_set_ratio_{self.req_id}", self.logger
                        )
                        return
                else:
                    self.logger.warning(
                        f"[{self.req_id}] 未找到宽高比按钮: {aspect_ratio}"
                    )
                    await dump_page(
                        self.page, f"imagen_no_ratio_btn_{self.req_id}", self.logger
                    )
                    return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 设置宽高比失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)

    async def set_negative_prompt(
        self, negative_prompt: str, check_client_disconnected: Callable
    ):
        if not negative_prompt:
            return
        self.logger.info(f"[{self.req_id}] 设置负面提示词")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                textarea = self.page.locator(IMAGEN_SETTINGS_NEGATIVE_PROMPT_SELECTOR)
                if await textarea.count() == 0:
                    self.logger.warning(f"[{self.req_id}] 未找到负面提示词输入框")
                    return
                await textarea.fill(negative_prompt)
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f"[{self.req_id}] ✅ 负面提示词已设置")
                await dump_page(
                    self.page, f"imagen_neg_prompt_{self.req_id}", self.logger
                )
                return
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 设置负面提示词失败 (尝试 {attempt}): {e}"
                )
            if attempt < MAX_RETRIES:
                await asyncio.sleep(SLEEP_SHORT)

    async def fill_prompt(self, prompt: str, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 填充提示词 ({len(prompt)} chars)")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.keyboard.press("Escape")
                await asyncio.sleep(SLEEP_SHORT)
                text_input, matched = await get_first_visible_locator(
                    self.page, IMAGEN_PROMPT_INPUT_SELECTORS
                )
                if not text_input:
                    raise Exception("未找到输入框")
                self.logger.info(f"[{self.req_id}] 找到输入框 (匹配: {matched})")
                await safe_click(text_input, "输入框", self.req_id)
                await text_input.fill(prompt)
                await asyncio.sleep(SLEEP_TICK)
                self.logger.info(f"[{self.req_id}] ✅ 提示词已填充")
                await dump_page(self.page, f"imagen_prompt_{self.req_id}", self.logger)
                return
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
        self.logger.info(f"[{self.req_id}] 🚀 开始生成图片...")
        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self.page.keyboard.press("Escape")
                await asyncio.sleep(SLEEP_SHORT)
                run_btn, matched = await wait_for_any_selector(
                    self.page,
                    IMAGEN_RUN_BUTTON_SELECTORS,
                    timeout=TIMEOUT_ELEMENT_ENABLED,
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
                await dump_page(self.page, f"imagen_run_{self.req_id}", self.logger)
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

    async def wait_for_images(
        self,
        expected_count: int,
        check_client_disconnected: Callable,
        timeout_seconds: int = 180,
    ) -> List[GeneratedImage]:
        self.logger.info(
            f"[{self.req_id}] ⏳ 等待图片生成 (期望 {expected_count} 张)..."
        )
        image_locator = self.page.locator(IMAGEN_GENERATED_IMAGE_SELECTOR)
        start_time = asyncio.get_event_loop().time()

        while True:
            elapsed = asyncio.get_event_loop().time() - start_time
            if elapsed > timeout_seconds:
                await dump_page(self.page, f"imagen_timeout_{self.req_id}", self.logger)
                raise TimeoutError(f"图片生成超时 ({timeout_seconds}s)")
            await self._check_disconnect(
                check_client_disconnected, f"等待图片 ({int(elapsed)}s)"
            )

            try:
                await self._handle_paid_usage_dialog_if_visible()

                current_count = await image_locator.count()
                if current_count >= expected_count:
                    await dump_page(
                        self.page,
                        f"imagen_found_{current_count}_{self.req_id}",
                        self.logger,
                    )
                    images = []
                    for i in range(current_count):
                        img = image_locator.nth(i)
                        src = await img.evaluate(
                            "img => img.currentSrc || img.getAttribute('src') || img.src || ''"
                        )
                        self.logger.info(
                            f"[{self.req_id}] 图片 {i} src 类型: {src[:100] if src else '空'}"
                        )
                        extracted = await extract_image_from_locator(img)
                        if extracted:
                            _, image_bytes, mime_type = extracted
                            images.append(
                                GeneratedImage(
                                    image_bytes=image_bytes,
                                    mime_type=mime_type,
                                    index=i,
                                )
                            )
                    if len(images) >= expected_count:
                        self.logger.info(
                            f"[{self.req_id}] ✅ 图片生成完成 ({len(images)} 张)"
                        )
                        return images
            except Exception as e:
                if isinstance(e, PaidApiKeyRequiredError):
                    raise
                self.logger.warning(f"[{self.req_id}] 检查图片时出错: {e}")

            await asyncio.sleep(SLEEP_RETRY)
