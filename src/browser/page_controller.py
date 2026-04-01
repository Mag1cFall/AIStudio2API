import asyncio
from typing import Callable, List, Dict, Any, Optional
import base64
import tempfile
import re
import os
from playwright.async_api import (
    Page as AsyncPage,
    expect as expect_async,
    TimeoutError,
    Locator,
)
import json as json_module
from config import (
    TEMPERATURE_INPUT_SELECTOR,
    MAX_OUTPUT_TOKENS_SELECTOR,
    STOP_SEQUENCE_INPUT_SELECTOR,
    MAT_CHIP_REMOVE_BUTTON_SELECTOR,
    TOP_P_INPUT_SELECTOR,
    SUBMIT_BUTTON_SELECTOR,
    SUBMIT_BUTTON_SELECTORS,
    OVERLAY_SELECTOR,
    PROMPT_TEXTAREA_SELECTOR,
    PROMPT_TEXTAREA_SELECTORS,
    RESPONSE_CONTAINER_SELECTOR,
    RESPONSE_TEXT_SELECTOR,
    EDIT_MESSAGE_BUTTON_SELECTOR,
    USE_URL_CONTEXT_SELECTOR,
    UPLOAD_BUTTON_SELECTOR,
    UPLOAD_BUTTON_SELECTORS,
    INSERT_BUTTON_SELECTOR,
    INSERT_BUTTON_SELECTORS,
    HIDDEN_FILE_INPUT_SELECTORS,
    THINKING_MODE_TOGGLE_SELECTOR,
    SET_THINKING_BUDGET_TOGGLE_SELECTOR,
    THINKING_BUDGET_INPUT_SELECTOR,
    GROUNDING_WITH_GOOGLE_SEARCH_TOGGLE_SELECTOR,
    ZERO_STATE_SELECTOR,
    SYSTEM_INSTRUCTIONS_BUTTON_SELECTOR,
    SYSTEM_INSTRUCTIONS_TEXTAREA_SELECTOR,
    SKIP_PREFERENCE_VOTE_BUTTON_SELECTOR,
    CLICK_TIMEOUT_MS,
    WAIT_FOR_ELEMENT_TIMEOUT_MS,
    CLEAR_CHAT_VERIFY_TIMEOUT_MS,
    DEFAULT_TEMPERATURE,
    DEFAULT_MAX_OUTPUT_TOKENS,
    DEFAULT_STOP_SEQUENCES,
    DEFAULT_TOP_P,
    ENABLE_URL_CONTEXT,
    ENABLE_THINKING_BUDGET,
    DEFAULT_THINKING_BUDGET,
    ENABLE_GOOGLE_SEARCH,
    THINKING_LEVEL_SELECT_SELECTOR,
    THINKING_LEVEL_OPTIONS,
    DEFAULT_THINKING_LEVEL,
    ADVANCED_SETTINGS_EXPANDER_SELECTOR,
)
from config.timeouts import (
    MAX_RETRIES,
    SLEEP_RETRY,
    SLEEP_SHORT,
    SLEEP_MEDIUM,
    SLEEP_LONG,
    SLEEP_TICK,
    SLEEP_IMAGE_UPLOAD,
    SLEEP_CLEANUP,
    SLEEP_NAVIGATION,
    TIMEOUT_PAGE_NAVIGATION,
    TIMEOUT_ELEMENT_ATTACHED,
    TIMEOUT_ELEMENT_VISIBLE,
    TIMEOUT_ELEMENT_ENABLED,
    TIMEOUT_SUBMIT_ENABLED,
    TIMEOUT_INNER_TEXT,
    TIMEOUT_INPUT_VALUE,
    DELAY_AFTER_CLICK,
    DELAY_AFTER_FILL,
    DELAY_AFTER_TOGGLE,
    DELAY_BETWEEN_RETRIES,
    MAX_WAIT_UPLOAD_VERIFY,
    NEW_CHAT_URL,
)
from models import ClientDisconnectedError, ElementClickError
from .operations import (
    save_error_snapshot,
    _wait_for_response_completion,
    _get_final_response_content,
    click_element,
)
from .thinking_normalizer import parse_reasoning_param, describe_config
from .selector_utils import wait_for_any_selector, get_first_visible_locator
from debug.dom_snapshot import dump_page


class PageController:
    def __init__(self, page: AsyncPage, logger, req_id: str):
        self.page = page
        self.logger = logger
        self.req_id = req_id

    async def _check_disconnect(self, check_client_disconnected: Callable, stage: str):
        if check_client_disconnected(stage):
            raise ClientDisconnectedError(
                f"[{self.req_id}] Client disconnected or request cancelled at stage: {stage}"
            )

    async def _click_and_verify(
        self,
        trigger_locator: Locator,
        expected_locator: Locator,
        trigger_name: str,
        expected_name: str,
        max_retries: int = 3,
        delay_between_retries: float = 0.5,
    ) -> None:
        for attempt in range(max_retries):
            self.logger.info(
                f"[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 点击 '{trigger_name}'..."
            )
            try:
                await click_element(
                    self.page, trigger_locator, trigger_name, self.req_id
                )
                self.logger.info(f"[{self.req_id}] 等待 '{expected_name}' 出现...")
                await expect_async(expected_locator).to_be_visible(timeout=1000)
                self.logger.info(f"[{self.req_id}] ✅ '{expected_name}' 已出现。")
                return
            except (ElementClickError, TimeoutError) as e:
                self.logger.warning(
                    f"[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 失败: '{expected_name}' did not appear after clicking. Error: {type(e).__name__}"
                )
                if attempt < max_retries - 1:
                    await asyncio.sleep(delay_between_retries)
                else:
                    self.logger.error(
                        f"[{self.req_id}] 达到最大重试次数，未能打开 '{expected_name}'。"
                    )
                    raise ElementClickError(
                        f"Failed to reveal '{expected_name}' after {max_retries} attempts."
                    ) from e
            except Exception as e:
                self.logger.error(
                    f"[{self.req_id}] _click_and_verify 中发生意外错误: {e}"
                )
                raise

    async def continuously_handle_skip_button(
        self, stop_event: asyncio.Event, check_client_disconnected: Callable
    ):

        await stop_event.wait()

    async def adjust_parameters(
        self,
        request_params: Dict[str, Any],
        page_params_cache: Dict[str, Any],
        params_cache_lock: asyncio.Lock,
        model_id_to_use: str,
        parsed_model_list: List[Dict[str, Any]],
        check_client_disconnected: Callable,
    ):
        self.logger.info(f"[{self.req_id}] ⚙️ 并发调整参数...")
        await self._check_disconnect(
            check_client_disconnected, "Start Parameter Adjustment"
        )

        temp_to_set = request_params.get("temperature", DEFAULT_TEMPERATURE)
        max_tokens_to_set = request_params.get(
            "max_output_tokens", DEFAULT_MAX_OUTPUT_TOKENS
        )
        stop_to_set = request_params.get("stop", DEFAULT_STOP_SEQUENCES)
        top_p_to_set = request_params.get("top_p", DEFAULT_TOP_P)

        await self._ensure_advanced_settings_expanded(check_client_disconnected)

        async def handle_tools_panel():
            await self._ensure_tools_panel_expanded(check_client_disconnected)
            if ENABLE_URL_CONTEXT:
                await self._open_url_content(check_client_disconnected)
            else:
                self.logger.info(f"[{self.req_id}] URL Context 功能已禁用，跳过调整。")
            # NOTE: Function Calling 改为合并到 system prompt，不再通过 UI 填写

        tasks = [
            self._adjust_temperature(
                temp_to_set,
                page_params_cache,
                params_cache_lock,
                check_client_disconnected,
            ),
            self._adjust_max_tokens(
                max_tokens_to_set,
                page_params_cache,
                params_cache_lock,
                model_id_to_use,
                parsed_model_list,
                check_client_disconnected,
            ),
            self._adjust_stop_sequences(
                stop_to_set,
                page_params_cache,
                params_cache_lock,
                check_client_disconnected,
            ),
            self._adjust_top_p(top_p_to_set, check_client_disconnected),
            self._adjust_google_search(request_params, check_client_disconnected),
            self._handle_thinking_budget(
                request_params, model_id_to_use, check_client_disconnected
            ),
            handle_tools_panel(),
        ]

        await asyncio.gather(*tasks)
        await dump_page(self.page, f"chat_params_ready_{self.req_id}", self.logger)

    async def set_system_instructions(
        self, system_prompt: str, check_client_disconnected: Callable
    ):
        if not system_prompt:
            return
        self.logger.info(
            f"[{self.req_id}] 正在设置系统指令 (长度: {len(system_prompt)} chars)..."
        )
        await self._check_disconnect(
            check_client_disconnected, "Start System Instructions"
        )
        try:
            sys_prompt_button = self.page.locator(SYSTEM_INSTRUCTIONS_BUTTON_SELECTOR)
            sys_prompt_textarea = self.page.locator(
                SYSTEM_INSTRUCTIONS_TEXTAREA_SELECTOR
            )
            await self._click_and_verify(
                sys_prompt_button,
                sys_prompt_textarea,
                "System Instructions Button",
                "System Instructions Textarea",
            )
            await expect_async(sys_prompt_textarea).to_be_editable(
                timeout=TIMEOUT_ELEMENT_VISIBLE
            )
            await sys_prompt_textarea.fill(system_prompt)
            await asyncio.sleep(DELAY_AFTER_FILL)
            filled_value = await sys_prompt_textarea.input_value(
                timeout=TIMEOUT_INPUT_VALUE
            )
            if len(filled_value) >= len(system_prompt) * 0.9:
                self.logger.info(
                    f"[{self.req_id}] ✅ 系统指令已填充 (验证长度: {len(filled_value)} chars)"
                )
            else:
                self.logger.warning(
                    f"[{self.req_id}] ⚠️ 系统指令填充可能不完整 (期望: {len(system_prompt)}, 实际: {len(filled_value)})"
                )
            await dump_page(
                self.page, f"chat_system_instructions_{self.req_id}", self.logger
            )
            for close_attempt in range(1, 4):
                try:
                    if not await sys_prompt_textarea.is_visible():
                        self.logger.info(f"[{self.req_id}] ✅ 系统指令面板已关闭。")
                        break
                    # Try clicking the button again to close the panel
                    await sys_prompt_button.click(timeout=2000)
                    await asyncio.sleep(DELAY_AFTER_FILL)
                    if not await sys_prompt_textarea.is_visible():
                        self.logger.info(f"[{self.req_id}] ✅ 系统指令面板已关闭。")
                        break
                    # Fallback: Escape key
                    await self.page.keyboard.press("Escape")
                    await asyncio.sleep(DELAY_AFTER_FILL)
                    if not await sys_prompt_textarea.is_visible():
                        self.logger.info(f"[{self.req_id}] ✅ 系统指令面板已关闭。")
                        break
                    self.logger.warning(
                        f"[{self.req_id}] 系统指令面板关闭验证失败 (嘗試 {close_attempt})"
                    )
                except Exception:
                    pass
        except Exception as e:
            err_msg = str(e)
            if len(err_msg) > 200:
                err_msg = err_msg[:200] + "...[truncated]"
            await dump_page(
                self.page, f"chat_system_instructions_error_{self.req_id}", self.logger
            )
            self.logger.error(f"[{self.req_id}] 设置系统指令时出错: {err_msg}")
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _control_thinking_mode_toggle(
        self, should_be_checked: bool, check_client_disconnected: Callable
    ) -> bool:
        toggle_selector = THINKING_MODE_TOGGLE_SELECTOR
        action = "啟用" if should_be_checked else "停用"
        state_label = "on" if should_be_checked else "off"
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                self.logger.info(
                    f"[{self.req_id}] (嘗試 {attempt}/{max_retries}) 控制 Thinking Mode 開關: {action}..."
                )
                toggle_locator = self.page.locator(toggle_selector)
                await expect_async(toggle_locator).to_be_visible(timeout=7000)
                await self._check_disconnect(
                    check_client_disconnected, "思考模式開關 - 元素可見後"
                )
                toggle_class = await toggle_locator.get_attribute("class") or ""
                if "mat-mdc-slide-toggle-disabled" in toggle_class:
                    self.logger.info(
                        f"[{self.req_id}] Thinking Mode 開關當前被禁用，跳過操作。"
                    )
                    return False
                current_state_is_checked = (
                    "mat-mdc-slide-toggle-checked" in toggle_class
                )
                if current_state_is_checked == should_be_checked:
                    self.logger.info(f"[{self.req_id}] ✅ Thinking Mode 已就緒。")
                    await dump_page(
                        self.page,
                        f"chat_thinking_toggle_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return True
                inner_btn = toggle_locator.locator('button[role="switch"]')
                if await inner_btn.count() > 0:
                    await click_element(
                        self.page, inner_btn, "Thinking Mode Toggle Button", self.req_id
                    )
                else:
                    await click_element(
                        self.page, toggle_locator, "Thinking Mode Toggle", self.req_id
                    )
                await self._check_disconnect(
                    check_client_disconnected, f"思考模式開關 - 點擊{action}後"
                )
                await asyncio.sleep(SLEEP_LONG)
                new_class = await toggle_locator.get_attribute("class") or ""
                new_state_is_checked = "mat-mdc-slide-toggle-checked" in new_class
                if new_state_is_checked == should_be_checked:
                    self.logger.info(f"[{self.req_id}] ✅ Thinking Mode 已{action}。")
                    await dump_page(
                        self.page,
                        f"chat_thinking_toggle_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return True
                else:
                    self.logger.warning(
                        f"[{self.req_id}] ⚠️ Thinking Mode {action}驗證失敗 (嘗試 {attempt})"
                    )
                    if attempt < max_retries:
                        await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] Thinking Mode 操作失敗 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
        self.logger.error(
            f"[{self.req_id}] ❌ Thinking Mode 設定失敗，已重試 {max_retries} 次"
        )
        return False

    def _is_gemini3_series(self, model_id: Optional[str]) -> bool:
        """判斷是否為 Gemini 3 系列（使用等級選擇器而非預算開關）"""
        mid = (model_id or "").lower()
        return "gemini-3" in mid

    async def _check_level_dropdown_exists(self) -> bool:
        """檢查等級下拉選單是否存在"""
        try:
            locator = self.page.locator(THINKING_LEVEL_SELECT_SELECTOR)
            return await locator.count() > 0
        except Exception:
            return False

    def _determine_level_from_effort(self, reasoning_effort: Any) -> Optional[str]:
        if isinstance(reasoning_effort, str):
            rs = reasoning_effort.strip().lower()
            if rs in ["minimal", "low", "medium", "high"]:
                return rs
            if rs in ["none", "-1"]:
                return "high"
            try:
                val = int(rs)
                if val >= 16384:
                    return "high"
                elif val >= 8192:
                    return "medium"
                elif val >= 4096:
                    return "low"
                else:
                    return "minimal"
            except Exception:
                return None
        if isinstance(reasoning_effort, int):
            if reasoning_effort == -1 or reasoning_effort >= 16384:
                return "high"
            elif reasoning_effort >= 8192:
                return "medium"
            elif reasoning_effort >= 4096:
                return "low"
            else:
                return "minimal"
        return None

    def _apply_model_budget_cap(self, value: int, model_id: Optional[str]) -> int:
        """根據模型類型限制預算上限"""
        mid = (model_id or "").lower()
        if "gemini-2.5-pro" in mid:
            return min(value, 32768)
        if "flash-lite" in mid:
            return min(value, 24576)
        if "flash" in mid:
            return min(value, 24576)
        return value

    async def _select_thinking_level(
        self, level: str, check_client_disconnected: Callable
    ):
        level = level.lower()
        if level not in THINKING_LEVEL_OPTIONS:
            self.logger.warning(f"[{self.req_id}] 未知等級 '{level}'，使用預設 'high'")
            level = "high"
        target_selector = THINKING_LEVEL_OPTIONS[level]
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                self.logger.info(
                    f"[{self.req_id}] (嘗試 {attempt}/{max_retries}) 設定推理等級 {level}..."
                )
                trigger = self.page.locator(THINKING_LEVEL_SELECT_SELECTOR)
                if await trigger.count() == 0:
                    self.logger.warning(
                        f"[{self.req_id}] 等級選擇器未找到，可能當前模型不支援"
                    )
                    raise Exception("等級選擇器不存在")
                await click_element(
                    self.page, trigger, "Thinking Level Selector", self.req_id
                )
                await self._check_disconnect(
                    check_client_disconnected, "等級選單展開後"
                )
                await asyncio.sleep(DELAY_AFTER_TOGGLE)

                option = self.page.locator(target_selector)
                option_count = await option.count()
                if option_count == 0:
                    self.logger.warning(
                        f"[{self.req_id}] 等級選項 {level} 未找到，等待加載..."
                    )
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
                    option_count = await option.count()

                if option_count == 0:
                    self.logger.warning(f"[{self.req_id}] 等級選項 {level} 仍未找到")
                    try:
                        await self.page.keyboard.press("Escape")
                    except Exception:
                        pass
                    raise Exception(f"等級選項 {level} 不存在")

                await click_element(
                    self.page, option.first, f"Thinking Level {level}", self.req_id
                )
                await asyncio.sleep(DELAY_AFTER_TOGGLE)
                current_text = await trigger.inner_text(timeout=2000)
                if level.lower() in current_text.lower():
                    self.logger.info(f"[{self.req_id}] ✓ 推理等級已設定為 {level}")
                    await dump_page(
                        self.page,
                        f"chat_thinking_level_{level}_{self.req_id}",
                        self.logger,
                    )
                    return
                self.logger.warning(
                    f"[{self.req_id}] 等級驗證失敗 (嘗試 {attempt}): 當前顯示 '{current_text}'"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 設定等級失敗 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(SLEEP_LONG)
        self.logger.error(
            f"[{self.req_id}] ❌ 推理等級設定失敗，已重試 {max_retries} 次"
        )
        raise Exception(f"推理等級 {level} 設定失敗")

    async def _set_budget_value(
        self, token_budget: int, check_client_disconnected: Callable
    ):
        budget_input = self.page.locator(THINKING_BUDGET_INPUT_SELECTOR)
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                self.logger.info(
                    f"[{self.req_id}] (嘗試 {attempt}/{max_retries}) 設定推理預算為: {token_budget} tokens"
                )
                await expect_async(budget_input).to_be_visible(timeout=5000)
                await self._check_disconnect(
                    check_client_disconnected, "預算輸入框可見後"
                )
                await budget_input.fill(str(token_budget), timeout=5000)
                await self._check_disconnect(check_client_disconnected, "預算填充後")
                await asyncio.sleep(DELAY_AFTER_FILL)
                actual_val = await budget_input.input_value(timeout=3000)
                if int(actual_val) == token_budget:
                    self.logger.info(f"[{self.req_id}] ✓ 預算已更新為 {actual_val}")
                    return True
                self.logger.warning(
                    f"[{self.req_id}] 預算驗證失敗 (嘗試 {attempt}): 實際 {actual_val}, 預期 {token_budget}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 設定預算失敗 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
        self.logger.error(f"[{self.req_id}] ❌ 預算設定失敗，已重試 {max_retries} 次")
        return False

    async def _handle_thinking_budget(
        self,
        request_params: Dict[str, Any],
        model_id_to_use: Optional[str],
        check_client_disconnected: Callable,
    ):
        reasoning_effort = request_params.get("reasoning_effort")
        cfg = parse_reasoning_param(reasoning_effort)
        self.logger.info(f"[{self.req_id}] 推理配置: {describe_config(cfg)}")

        if not cfg.enable_reasoning:
            self.logger.info(f"[{self.req_id}] 推理模式已停用，跳過相關設定")
            return

        try:
            is_gemini3 = self._is_gemini3_series(model_id_to_use)

            if is_gemini3:
                level = (
                    self._determine_level_from_effort(reasoning_effort)
                    or DEFAULT_THINKING_LEVEL
                )
                self.logger.info(
                    f"[{self.req_id}] Gemini 3 系列，使用等級模式: {level}"
                )
                try:
                    await self._select_thinking_level(level, check_client_disconnected)
                except Exception as e:
                    self.logger.warning(
                        f"[{self.req_id}] 設定推理等級 {level} 失敗: {e}"
                    )
                    if level == "low":
                        self.logger.info(
                            f"[{self.req_id}] low 選項不存在，嘗試使用 high"
                        )
                        try:
                            await self._select_thinking_level(
                                "high", check_client_disconnected
                            )
                        except Exception as e2:
                            self.logger.warning(
                                f"[{self.req_id}] high 選項也失敗: {e2}"
                            )
                return

            await self._control_thinking_mode_toggle(
                should_be_checked=True,
                check_client_disconnected=check_client_disconnected,
            )

            if cfg.use_budget_limit and cfg.budget_tokens:
                capped_val = self._apply_model_budget_cap(
                    cfg.budget_tokens, model_id_to_use
                )
                self.logger.info(f"[{self.req_id}] 啟用預算限制，數值: {capped_val}")
                await self._control_thinking_budget_toggle(
                    should_be_checked=True,
                    check_client_disconnected=check_client_disconnected,
                )
                await self._set_budget_value(capped_val, check_client_disconnected)
            else:
                self.logger.info(f"[{self.req_id}] 推理已啟用，無預算限制")
                await self._control_thinking_budget_toggle(
                    should_be_checked=False,
                    check_client_disconnected=check_client_disconnected,
                )
        except Exception as e:
            self.logger.error(f"[{self.req_id}] 處理推理模式時發生錯誤: {e}")
            if isinstance(e, ClientDisconnectedError):
                raise

    def _should_enable_google_search(self, request_params: Dict[str, Any]) -> bool:
        if "tools" in request_params and request_params.get("tools") is not None:
            tools = request_params.get("tools")
            has_google_search_tool = False
            if isinstance(tools, list):
                for tool in tools:
                    if isinstance(tool, dict):
                        if tool.get("google_search_retrieval") is not None:
                            has_google_search_tool = True
                            break
                        if tool.get("function", {}).get("name") == "googleSearch":
                            has_google_search_tool = True
                            break
            self.logger.info(
                f"[{self.req_id}] 请求中包含 'tools' 参数。检测到 Google Search 工具: {has_google_search_tool}。"
            )
            return has_google_search_tool
        else:
            self.logger.info(
                f"[{self.req_id}] 请求中不包含 'tools' 参数。使用默认配置 ENABLE_GOOGLE_SEARCH: {ENABLE_GOOGLE_SEARCH}。"
            )
            return ENABLE_GOOGLE_SEARCH

    async def _adjust_google_search(
        self, request_params: Dict[str, Any], check_client_disconnected: Callable
    ):
        should_enable_search = self._should_enable_google_search(request_params)
        toggle_selector = GROUNDING_WITH_GOOGLE_SEARCH_TOGGLE_SELECTOR
        state_label = "on" if should_enable_search else "off"
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                toggle_locator = self.page.locator(toggle_selector)
                if await toggle_locator.count() == 0:
                    self.logger.debug(
                        f"[{self.req_id}] Google Search 开关不存在，跳过。"
                    )
                    return
                await expect_async(toggle_locator).to_be_visible(timeout=5000)
                await self._check_disconnect(
                    check_client_disconnected, "Google Search 開關 - 元素可見後"
                )
                is_checked_str = await toggle_locator.get_attribute("aria-checked")
                is_currently_checked = is_checked_str == "true"
                if should_enable_search == is_currently_checked:
                    self.logger.info(f"[{self.req_id}] ✅ Google Search 已就緒。")
                    await dump_page(
                        self.page,
                        f"chat_google_search_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return
                action = "打開" if should_enable_search else "關閉"
                self.logger.info(
                    f"[{self.req_id}] 🌍 (嘗試 {attempt}/{max_retries}) 正在{action} Google Search..."
                )
                await toggle_locator.scroll_into_view_if_needed(timeout=3000)
                await toggle_locator.click(force=True, timeout=3000)
                await self._check_disconnect(
                    check_client_disconnected, f"Google Search 開關 - 點擊{action}後"
                )
                await asyncio.sleep(1.0)
                new_state = await toggle_locator.get_attribute("aria-checked")
                if (new_state == "true") == should_enable_search:
                    self.logger.info(f"[{self.req_id}] ✅ Google Search 已{action}。")
                    await dump_page(
                        self.page,
                        f"chat_google_search_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return
                # Force via JS click on parent label
                await toggle_locator.evaluate(
                    'el => (el.closest("label") || el).click()'
                )
                await asyncio.sleep(1.0)
                new_state = await toggle_locator.get_attribute("aria-checked")
                if (new_state == "true") == should_enable_search:
                    self.logger.info(
                        f"[{self.req_id}] ✅ Google Search 已{action} (JS)。"
                    )
                    await dump_page(
                        self.page,
                        f"chat_google_search_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return
                self.logger.warning(
                    f"[{self.req_id}] ⚠️ Google Search {action}失敗 (嘗試 {attempt}): '{new_state}'"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] Google Search 操作失敗 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
        self.logger.error(
            f"[{self.req_id}] ❌ Google Search 設定失敗，已重試 {max_retries} 次"
        )

    # NOTE: _extract_function_declarations 和 _adjust_function_calling 已移除
    # Function Calling 改为将 tools 定义合并到 system prompt 中

    async def _ensure_advanced_settings_expanded(
        self, check_client_disconnected: Callable
    ):
        max_retries = MAX_RETRIES
        expander_locator = self.page.locator(ADVANCED_SETTINGS_EXPANDER_SELECTOR)

        async def is_expanded() -> bool:
            try:
                grandparent = expander_locator.locator("xpath=../..")
                class_str = await grandparent.get_attribute("class", timeout=2000)
                return class_str and "expanded" in class_str.split()
            except Exception:
                return False

        for attempt in range(1, max_retries + 1):
            try:
                await self._check_disconnect(
                    check_client_disconnected, f"高级设置展开 - 尝试 {attempt}"
                )

                if await expander_locator.count() == 0:
                    self.logger.info(
                        f"[{self.req_id}] 高级设置展开按钮不存在，可能已是新版布局，跳过。"
                    )
                    return

                if await is_expanded():
                    self.logger.info(f"[{self.req_id}] ✅ 高级设置面板已展开。")
                    return

                self.logger.info(
                    f"[{self.req_id}] 🔧 (尝试 {attempt}/{max_retries}) 正在展开高级设置面板..."
                )

                try:
                    await click_element(
                        self.page, expander_locator, "高级设置展开按钮", self.req_id
                    )
                except ElementClickError as e:
                    self.logger.warning(
                        f"[{self.req_id}] 高级设置展开按钮点击失败: {e}"
                    )
                    if attempt < max_retries:
                        await asyncio.sleep(DELAY_AFTER_TOGGLE)
                    continue

                await asyncio.sleep(DELAY_AFTER_TOGGLE)

                if await is_expanded():
                    self.logger.info(f"[{self.req_id}] ✅ 高级设置面板已展开。")
                    return

                self.logger.warning(
                    f"[{self.req_id}] 高级设置展开验证失败 (尝试 {attempt})"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)

            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 展开高级设置失败 (尝试 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)

        self.logger.error(
            f"[{self.req_id}] ❌ 高级设置展开失败，已重试 {max_retries} 次"
        )

    async def _ensure_tools_panel_expanded(self, check_client_disconnected: Callable):
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                collapse_tools_locator = self.page.locator(
                    'button[aria-label="Expand or collapse tools"]'
                )
                if await collapse_tools_locator.count() == 0:
                    self.logger.info(f"[{self.req_id}] 工具面板展开按钮不存在，跳过。")
                    return
                await expect_async(collapse_tools_locator).to_be_visible(timeout=5000)
                grandparent_locator = collapse_tools_locator.locator("xpath=../..")
                class_string = await grandparent_locator.get_attribute(
                    "class", timeout=3000
                )
                if class_string and "expanded" in class_string.split():
                    self.logger.info(f"[{self.req_id}] ✅ 工具面板已展开。")
                    return
                self.logger.info(
                    f"[{self.req_id}] 🔧 (嘗試 {attempt}/{max_retries}) 正在展开工具面板..."
                )
                await click_element(
                    self.page,
                    collapse_tools_locator,
                    "Expand/Collapse Tools Button",
                    self.req_id,
                )
                await self._check_disconnect(
                    check_client_disconnected, "展开工具面板后"
                )
                await asyncio.sleep(DELAY_AFTER_TOGGLE)
                new_class = await grandparent_locator.get_attribute(
                    "class", timeout=3000
                )
                if new_class and "expanded" in new_class.split():
                    self.logger.info(f"[{self.req_id}] ✅ 工具面板已展开。")
                    return
                self.logger.warning(
                    f"[{self.req_id}] 工具面板展开验证失败 (嘗試 {attempt})"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] 展开工具面板失败 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
        self.logger.error(
            f"[{self.req_id}] ❌ 工具面板展开失败，已重试 {max_retries} 次"
        )

    async def _open_url_content(self, check_client_disconnected: Callable):
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                self.logger.info(
                    f"[{self.req_id}] (嘗試 {attempt}/{max_retries}) 检查并启用 URL Context 开关..."
                )
                use_url_content_selector = self.page.locator(USE_URL_CONTEXT_SELECTOR)
                await expect_async(use_url_content_selector).to_be_visible(timeout=5000)
                is_checked = await use_url_content_selector.get_attribute(
                    "aria-checked"
                )
                if is_checked == "true":
                    self.logger.info(
                        f"[{self.req_id}] ✅ URL Context 开关已处于开启状态。"
                    )
                    return
                await click_element(
                    self.page,
                    use_url_content_selector,
                    "URL Context Toggle",
                    self.req_id,
                )
                await self._check_disconnect(
                    check_client_disconnected, "点击URLCONTEXT后"
                )
                await asyncio.sleep(DELAY_AFTER_TOGGLE)
                new_state = await use_url_content_selector.get_attribute("aria-checked")
                if new_state == "true":
                    self.logger.info(f"[{self.req_id}] ✅ URL Context 开关已开启。")
                    return
                self.logger.warning(
                    f"[{self.req_id}] URL Context 验证失败 (嘗試 {attempt}): '{new_state}'"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] URL Context 操作失败 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
        self.logger.error(
            f"[{self.req_id}] ❌ URL Context 设定失败，已重试 {max_retries} 次"
        )

    async def _control_thinking_budget_toggle(
        self, should_be_checked: bool, check_client_disconnected: Callable
    ) -> bool:
        toggle_selector = SET_THINKING_BUDGET_TOGGLE_SELECTOR
        action = "啟用" if should_be_checked else "停用"
        state_label = "on" if should_be_checked else "off"
        max_retries = MAX_RETRIES
        for attempt in range(1, max_retries + 1):
            try:
                self.logger.info(
                    f"[{self.req_id}] (嘗試 {attempt}/{max_retries}) 控制 Set Thinking Budget 開關: {action}..."
                )
                toggle_locator = self.page.locator(toggle_selector)
                await expect_async(toggle_locator).to_be_visible(timeout=5000)
                await self._check_disconnect(
                    check_client_disconnected, "手動預算開關 - 元素可見後"
                )
                toggle_class = await toggle_locator.get_attribute("class") or ""
                current_state_is_checked = (
                    "mat-mdc-slide-toggle-checked" in toggle_class
                )
                if current_state_is_checked == should_be_checked:
                    self.logger.info(
                        f"[{self.req_id}] ✅ Set Thinking Budget 開關已就緒。"
                    )
                    await dump_page(
                        self.page,
                        f"chat_thinking_budget_toggle_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return True
                inner_btn = toggle_locator.locator('button[role="switch"]')
                if await inner_btn.count() > 0:
                    await click_element(
                        self.page,
                        inner_btn,
                        "Set Thinking Budget Toggle Button",
                        self.req_id,
                    )
                else:
                    await click_element(
                        self.page,
                        toggle_locator,
                        "Set Thinking Budget Toggle",
                        self.req_id,
                    )
                await self._check_disconnect(
                    check_client_disconnected, f"手動預算開關 - 點擊{action}後"
                )
                await asyncio.sleep(SLEEP_LONG)
                new_class = await toggle_locator.get_attribute("class") or ""
                new_state_is_checked = "mat-mdc-slide-toggle-checked" in new_class
                if new_state_is_checked == should_be_checked:
                    self.logger.info(
                        f"[{self.req_id}] ✅ Set Thinking Budget 開關已{action}。"
                    )
                    await dump_page(
                        self.page,
                        f"chat_thinking_budget_toggle_{state_label}_{self.req_id}",
                        self.logger,
                    )
                    return True
                else:
                    self.logger.warning(
                        f"[{self.req_id}] ⚠️ Set Thinking Budget {action}驗證失敗 (嘗試 {attempt})"
                    )
                    if attempt < max_retries:
                        await asyncio.sleep(DELAY_AFTER_TOGGLE)
            except Exception as e:
                if isinstance(e, ClientDisconnectedError):
                    raise
                self.logger.warning(
                    f"[{self.req_id}] Set Thinking Budget 操作失敗 (嘗試 {attempt}): {e}"
                )
                if attempt < max_retries:
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)
        self.logger.error(
            f"[{self.req_id}] ❌ Set Thinking Budget 設定失敗，已重試 {max_retries} 次"
        )
        return False

    async def _set_parameter_with_retry(
        self,
        locator: Locator,
        target_value: str,
        param_name: str,
        check_client_disconnected: Callable,
    ) -> bool:
        def is_equal(val1, val2):
            import re

            def extract_float(s):
                m = re.search(r"[-+]?\d*\.?\d+", str(s))
                return float(m.group()) if m else None

            try:
                f1 = extract_float(val1)
                f2 = float(val2)
                if f1 is not None:
                    return abs(f1 - f2) < 0.001
            except (ValueError, TypeError):
                pass
            return str(val1).strip() == str(val2).strip()

        max_retries = MAX_RETRIES
        for attempt in range(max_retries):
            strategy_name = "Unknown"
            try:
                await self._check_disconnect(
                    check_client_disconnected, f"设置 {param_name} - 尝试 {attempt + 1}"
                )

                if attempt == 0:
                    try:
                        await expect_async(locator).to_be_visible(timeout=5000)
                    except Exception:
                        # Panel might be collapsed - try to expand and retry visibility
                        await self._ensure_advanced_settings_expanded(
                            check_client_disconnected
                        )
                        await expect_async(locator).to_be_visible(timeout=5000)
                    await asyncio.sleep(DELAY_AFTER_TOGGLE)

                if attempt == 0:
                    strategy_name = "JS Injection"
                    await locator.evaluate(
                        '(el, val) => { el.value = val; el.dispatchEvent(new Event("input", {bubbles: true})); el.dispatchEvent(new Event("change", {bubbles: true})); el.dispatchEvent(new Event("blur", {bubbles: true})); }',
                        str(target_value),
                    )
                    await asyncio.sleep(DELAY_AFTER_FILL)
                    await locator.press("Tab")
                elif attempt == 1:
                    strategy_name = "Ctrl+A Fill"
                    await locator.click()
                    await locator.press("Control+a")
                    await locator.fill(str(target_value))
                    await locator.dispatch_event("input")
                    await locator.dispatch_event("change")
                    await locator.press("Tab")
                    await asyncio.sleep(DELAY_AFTER_FILL)
                else:
                    strategy_name = "Triple Click Fill"
                    await locator.click(click_count=3)
                    await locator.fill(str(target_value))
                    await locator.dispatch_event("input")
                    await locator.dispatch_event("change")
                    await locator.press("Tab")

                await asyncio.sleep(SLEEP_LONG)

                final_val = await locator.input_value(timeout=5000)
                if is_equal(final_val, target_value):
                    self.logger.info(
                        f"[{self.req_id}] {param_name} 成功设置为 {final_val} (策略: {strategy_name})。"
                    )
                    return True

                self.logger.warning(
                    f"[{self.req_id}] {param_name} 验证失败 (尝试 {attempt + 1}, 策略: {strategy_name})。页面显示: {final_val}, 期望: {target_value}"
                )

            except Exception as e:
                self.logger.warning(
                    f"[{self.req_id}] {param_name} 设置发生异常 (尝试 {attempt + 1}): {e}"
                )
                if isinstance(e, ClientDisconnectedError):
                    raise

            await asyncio.sleep(SLEEP_LONG)

        self.logger.error(
            f"[{self.req_id}] {param_name} 最终设置失败，已耗尽所有策略。"
        )
        return False

    async def _adjust_temperature(
        self,
        temperature: float,
        page_params_cache: dict,
        params_cache_lock: asyncio.Lock,
        check_client_disconnected: Callable,
    ):
        self.logger.info(f"[{self.req_id}] 检查并调整温度设置...")
        clamped_temp = max(0.0, min(2.0, temperature))
        if clamped_temp != temperature:
            self.logger.warning(
                f"[{self.req_id}] 请求的温度 {temperature} 超出范围，已调整为 {clamped_temp}"
            )

        temp_input_locator = self.page.locator(TEMPERATURE_INPUT_SELECTOR)
        success = await self._set_parameter_with_retry(
            temp_input_locator,
            str(clamped_temp),
            "Temperature",
            check_client_disconnected,
        )

        if success:
            page_params_cache["temperature"] = clamped_temp
            await dump_page(
                self.page,
                f"chat_temperature_{clamped_temp}_{self.req_id}",
                self.logger,
            )
        else:
            self.logger.error(f"[{self.req_id}] 温度设置彻底失败，清除缓存。")
            page_params_cache.pop("temperature", None)
            await save_error_snapshot(f"temperature_set_fail_{self.req_id}")

    async def _adjust_max_tokens(
        self,
        max_tokens: int,
        page_params_cache: dict,
        params_cache_lock: asyncio.Lock,
        model_id_to_use: str,
        parsed_model_list: list,
        check_client_disconnected: Callable,
    ):
        self.logger.info(f"[{self.req_id}] 检查并调整最大输出 Token 设置...")

        min_val_for_tokens = 1
        max_val_for_tokens_from_model = 65536

        if model_id_to_use and parsed_model_list:
            current_model_data = next(
                (m for m in parsed_model_list if m.get("id") == model_id_to_use), None
            )
            if (
                current_model_data
                and current_model_data.get("supported_max_output_tokens") is not None
            ):
                try:
                    supported_tokens = int(
                        current_model_data["supported_max_output_tokens"]
                    )
                    if supported_tokens > 0:
                        max_val_for_tokens_from_model = supported_tokens
                except (ValueError, TypeError):
                    self.logger.warning(
                        f"[{self.req_id}] 模型 {model_id_to_use} supported_max_output_tokens 解析失败"
                    )

        clamped_max_tokens = max(
            min_val_for_tokens, min(max_val_for_tokens_from_model, max_tokens)
        )
        if clamped_max_tokens != max_tokens:
            self.logger.warning(
                f"[{self.req_id}] 请求的最大输出 Tokens {max_tokens} 超出模型范围，已调整为 {clamped_max_tokens}"
            )

        max_tokens_input_locator = self.page.locator(MAX_OUTPUT_TOKENS_SELECTOR)
        success = await self._set_parameter_with_retry(
            max_tokens_input_locator,
            str(clamped_max_tokens),
            "Max Output Tokens",
            check_client_disconnected,
        )

        if success:
            page_params_cache["max_output_tokens"] = clamped_max_tokens
            await dump_page(
                self.page,
                f"chat_max_tokens_{clamped_max_tokens}_{self.req_id}",
                self.logger,
            )
        else:
            page_params_cache.pop("max_output_tokens", None)
            await save_error_snapshot(f"max_tokens_set_fail_{self.req_id}")

    async def _adjust_stop_sequences(
        self,
        stop_sequences,
        page_params_cache: dict,
        params_cache_lock: asyncio.Lock,
        check_client_disconnected: Callable,
    ):
        self.logger.info(f"[{self.req_id}] 检查并设置停止序列...")
        normalized_requested_stops = set()
        if stop_sequences is not None:
            if isinstance(stop_sequences, str):
                if stop_sequences.strip():
                    normalized_requested_stops.add(stop_sequences.strip())
            elif isinstance(stop_sequences, list):
                for s in stop_sequences:
                    if isinstance(s, str) and s.strip():
                        normalized_requested_stops.add(s.strip())
        cached_stops_set = page_params_cache.get("stop_sequences")
        if (
            cached_stops_set is not None
            and cached_stops_set == normalized_requested_stops
        ):
            self.logger.info(
                f"[{self.req_id}] 请求的停止序列与缓存值一致。跳过页面交互。"
            )
            return
        stop_input_locator = self.page.locator(STOP_SEQUENCE_INPUT_SELECTOR)
        remove_chip_buttons_locator = self.page.locator(MAT_CHIP_REMOVE_BUTTON_SELECTOR)
        try:
            initial_chip_count = await remove_chip_buttons_locator.count()
            removed_count = 0
            max_removals = initial_chip_count + 5
            while (
                await remove_chip_buttons_locator.count() > 0
                and removed_count < max_removals
            ):
                await self._check_disconnect(
                    check_client_disconnected, "停止序列清除 - 循环开始"
                )
                try:
                    await click_element(
                        self.page,
                        remove_chip_buttons_locator.first,
                        "Remove Stop Sequence Chip",
                        self.req_id,
                    )
                    removed_count += 1
                    await asyncio.sleep(SLEEP_SHORT)
                except Exception:
                    break
            if normalized_requested_stops:
                try:
                    await expect_async(stop_input_locator).to_be_visible(timeout=5000)
                except Exception:
                    await self._ensure_advanced_settings_expanded(
                        check_client_disconnected
                    )
                    await expect_async(stop_input_locator).to_be_visible(timeout=5000)
                await stop_input_locator.scroll_into_view_if_needed(timeout=3000)
                for seq in normalized_requested_stops:
                    await stop_input_locator.click(timeout=3000)
                    await stop_input_locator.fill(seq, timeout=5000)
                    await stop_input_locator.press("Enter", timeout=5000)
                    await asyncio.sleep(DELAY_AFTER_FILL)
            page_params_cache["stop_sequences"] = normalized_requested_stops
            self.logger.info(f"[{self.req_id}]  停止序列已成功设置。缓存已更新。")
        except Exception as e:
            self.logger.error(f"[{self.req_id}]  设置停止序列时出错: {e}")
            page_params_cache.pop("stop_sequences", None)
            await save_error_snapshot(f"stop_sequence_error_{self.req_id}")
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _adjust_top_p(self, top_p: float, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 检查并调整 Top P 设置...")
        clamped_top_p = max(0.0, min(1.0, top_p))
        if abs(clamped_top_p - top_p) > 1e-09:
            self.logger.warning(
                f"[{self.req_id}] 请求的 Top P {top_p} 超出范围，已调整为 {clamped_top_p}"
            )

        top_p_input_locator = self.page.locator(TOP_P_INPUT_SELECTOR)
        success = await self._set_parameter_with_retry(
            top_p_input_locator, str(clamped_top_p), "Top P", check_client_disconnected
        )

        if success:
            await dump_page(
                self.page,
                f"chat_top_p_{clamped_top_p}_{self.req_id}",
                self.logger,
            )
        else:
            await save_error_snapshot(f"top_p_set_fail_{self.req_id}")

    async def clear_chat_history(self, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 开始清空聊天记录 (通过导航)...")
        await self._check_disconnect(check_client_disconnected, "Start Clear Chat")
        new_chat_url = NEW_CHAT_URL
        max_retries = MAX_RETRIES
        for attempt in range(max_retries):
            try:
                self.logger.info(
                    f"[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 导航到: {new_chat_url}"
                )
                await self.page.goto(
                    new_chat_url, timeout=15000, wait_until="domcontentloaded"
                )
                await self._check_disconnect(
                    check_client_disconnected, "清空聊天 - 导航后"
                )
                await self._verify_chat_cleared(check_client_disconnected)
                await dump_page(self.page, f"chat_cleared_{self.req_id}", self.logger)
                self.logger.info(f"[{self.req_id}] 聊天记录已成功清空并验证。")
                return
            except Exception as e:
                self.logger.warning(
                    f"[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 清空聊天失败: {e}"
                )
                await self._check_disconnect(
                    check_client_disconnected, f"清空聊天 - 尝试 {attempt + 1} 失败后"
                )
                if attempt < max_retries - 1:
                    await asyncio.sleep(2.0)
                else:
                    self.logger.error(
                        f"[{self.req_id}] 达到最大重试次数，清空聊天失败。"
                    )
                    if not (
                        isinstance(e, ClientDisconnectedError)
                        or (hasattr(e, "name") and "Disconnect" in e.name)
                    ):
                        await save_error_snapshot(
                            f"clear_chat_fatal_error_{self.req_id}"
                        )
                    raise

    async def _verify_chat_cleared(self, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 验证聊天是否已清空...")
        await self._check_disconnect(
            check_client_disconnected, "Start Verify Clear Chat"
        )
        try:
            await expect_async(self.page).to_have_url(
                re.compile(".*/prompts/new_chat.*"),
                timeout=CLEAR_CHAT_VERIFY_TIMEOUT_MS,
            )
            self.logger.info(
                f"[{self.req_id}]   - URL验证成功: 页面已导航到 new_chat。"
            )
            zero_state_locator = self.page.locator(ZERO_STATE_SELECTOR)
            try:
                await expect_async(zero_state_locator).to_be_visible(timeout=5000)
                self.logger.info(f'[{self.req_id}]   - UI验证成功: "零状态"元素可见。')
            except Exception:
                self.logger.debug(
                    f"[{self.req_id}]   - zero_state not visible, waiting for textarea..."
                )
                try:
                    await expect_async(
                        self.page.locator(PROMPT_TEXTAREA_SELECTOR)
                    ).to_be_visible(timeout=10000)
                    self.logger.info(
                        f"[{self.req_id}]   - Textarea visible, page ready."
                    )
                except Exception:
                    self.logger.debug(
                        f"[{self.req_id}]   - Textarea also not visible, continuing anyway."
                    )
            self.logger.info(f"[{self.req_id}] 聊天已成功清空 (验证通过)。")
            await dump_page(self.page, f"chat_nav_ready_{self.req_id}", self.logger)
        except Exception as verify_err:
            self.logger.error(f"[{self.req_id}] 错误: 清空聊天验证失败: {verify_err}")
            await save_error_snapshot(f"clear_chat_verify_fail_{self.req_id}")
            self.logger.warning(
                f"[{self.req_id}] 警告: 清空聊天验证失败，但将继续执行。后续操作可能会受影响。"
            )

    async def _robust_click_insert_assets(
        self, check_client_disconnected: Callable
    ) -> bool:
        self.logger.info(f"[{self.req_id}] 开始寻找并点击媒体添加按钮...")

        trigger_selectors = INSERT_BUTTON_SELECTORS + [
            'button[aria-label*="Insert"]',
            'button[iconname="add_circle"]',
            'button[iconname="note_add"]',
        ]

        trigger_btn = None
        for sel in trigger_selectors:
            if await self.page.locator(sel).count() > 0:
                trigger_btn = self.page.locator(sel).first
                self.logger.info(f"[{self.req_id}] 找到媒体按钮: {sel}")
                break

        if not trigger_btn:
            self.logger.warning(f"[{self.req_id}] 未找到媒体添加按钮。")
            return False

        upload_menu_locator, _ = await get_first_visible_locator(
            self.page, UPLOAD_BUTTON_SELECTORS, timeout=1000
        )

        max_attempts = 3
        for attempt in range(1, max_attempts + 1):
            await self._check_disconnect(
                check_client_disconnected, f"点击媒体按钮 - 尝试 {attempt}"
            )

            self.logger.info(
                f"[{self.req_id}] (尝试 {attempt}/{max_attempts}) 点击媒体添加按钮..."
            )

            try:
                await click_element(self.page, trigger_btn, "媒体添加按钮", self.req_id)
            except ElementClickError as e:
                self.logger.warning(f"[{self.req_id}] 媒体按钮点击失败: {e}")
                if attempt < max_attempts:
                    await asyncio.sleep(SLEEP_LONG)
                continue

            for _ in range(10):
                try:
                    (
                        upload_menu_locator,
                        matched_upload,
                    ) = await get_first_visible_locator(
                        self.page, UPLOAD_BUTTON_SELECTORS, timeout=500
                    )
                    if upload_menu_locator and await upload_menu_locator.is_visible():
                        self.logger.info(
                            f"[{self.req_id}] ✅ 'Upload file' 菜单项已检测到开启 (匹配: {matched_upload})。"
                        )
                        return True
                except Exception:
                    pass
                await asyncio.sleep(DELAY_AFTER_FILL)

            self.logger.warning(
                f"[{self.req_id}] (尝试 {attempt}/{max_attempts}) 菜单仍未开启。"
            )
            if attempt < max_attempts:
                await asyncio.sleep(DELAY_AFTER_TOGGLE)

        self.logger.error(f"[{self.req_id}] 多次尝试后仍无法打开媒体菜单。")
        return False

    async def _upload_images_via_file_input(
        self, images: List[Dict[str, str]], check_client_disconnected: Callable
    ) -> bool:
        self.logger.info(f"[{self.req_id}] 准备上传 {len(images)} 张图片...")
        temp_files = []

        try:
            for idx, img in enumerate(images):
                mime = img["mime"]
                try:
                    data = base64.b64decode(img["data"])
                except Exception:
                    self.logger.warning(
                        f"[{self.req_id}] 图片 {idx} base64 解码失败，跳过"
                    )
                    continue

                ext = mime.split("/")[-1] if "/" in mime else "png"
                if ext == "jpeg":
                    ext = "jpg"

                tf = tempfile.NamedTemporaryFile(
                    delete=False,
                    suffix=f".{ext}",
                    prefix=f"upload_{self.req_id}_{idx}_",
                )
                tf.write(data)
                tf.close()
                temp_files.append(tf.name)

            if not temp_files:
                return False

            await self._check_disconnect(check_client_disconnected, "上传图片前")

            """
            # 旧批量上传逻辑（AI Studio新前端不支援multiple属性）
            menu_opened = await self._robust_click_insert_assets(check_client_disconnected)
            if not menu_opened:
                self.logger.warning(f"[{self.req_id}] 未能打开菜单，尝试直接查找 input...")
            
            file_input = None
            for selector in HIDDEN_FILE_INPUT_SELECTORS:
                loc = self.page.locator(selector)
                if await loc.count() > 0:
                    file_input = loc.first
                    break
            if not file_input:
                loc = self.page.locator('input[type="file"]')
                if await loc.count() > 0:
                    file_input = loc.first
            
            if not file_input:
                self.logger.warning(f"[{self.req_id}] 未找到文件输入框")
                asyncio.create_task(self._cleanup_temp_files(temp_files))
                return False
            
            try:
                self.logger.info(f"[{self.req_id}] 尝试批量上传 {len(temp_files)} 张图片...")
                await file_input.set_input_files(temp_files)
                await asyncio.sleep(DELAY_AFTER_TOGGLE)
                await self.page.keyboard.press('Escape')
                self.logger.info(f"[{self.req_id}] ✅ 批量上传成功 ({len(temp_files)} 张)")
                asyncio.create_task(self._cleanup_temp_files(temp_files))
                return True
            except Exception as batch_err:
                self.logger.warning(f"[{self.req_id}] 批量上传失败: {batch_err}，尝试逐个上传...")
            """

            uploaded_count = 0
            for idx, tf_path in enumerate(temp_files):
                await self._check_disconnect(
                    check_client_disconnected, f"上传图片 {idx + 1}/{len(temp_files)}"
                )

                menu_opened = await self._robust_click_insert_assets(
                    check_client_disconnected
                )
                if not menu_opened:
                    continue

                file_input = None
                for selector in HIDDEN_FILE_INPUT_SELECTORS:
                    loc = self.page.locator(selector)
                    if await loc.count() > 0:
                        file_input = loc.first
                        break
                if not file_input:
                    loc = self.page.locator('input[type="file"]')
                    if await loc.count() > 0:
                        file_input = loc.first

                if not file_input:
                    self.logger.warning(
                        f"[{self.req_id}] 第{idx + 1}张图片：未找到文件输入框"
                    )
                    continue

                try:
                    self.logger.info(
                        f"[{self.req_id}] 上传图片 {idx + 1}/{len(temp_files)}..."
                    )
                    await file_input.set_input_files(tf_path)
                    uploaded_count += 1
                    await asyncio.sleep(SLEEP_IMAGE_UPLOAD)
                except Exception as single_err:
                    self.logger.warning(
                        f"[{self.req_id}] 单张上传失败 {idx + 1}: {single_err}"
                    )

            asyncio.create_task(self._cleanup_temp_files(temp_files))

            if uploaded_count > 0:
                self.logger.info(
                    f"[{self.req_id}] ✅ 逐个上传完成 {uploaded_count}/{len(temp_files)} 张"
                )
                await dump_page(
                    self.page,
                    f"chat_images_uploaded_{uploaded_count}_{self.req_id}",
                    self.logger,
                )
                return True
            return False

        except Exception as e:
            self.logger.error(f"[{self.req_id}] 文件上传失败: {e}")
            asyncio.create_task(self._cleanup_temp_files(temp_files))
            return False

    async def _cleanup_temp_files(self, file_paths: List[str]):
        await asyncio.sleep(SLEEP_CLEANUP)
        for path in file_paths:
            try:
                if os.path.exists(path):
                    os.remove(path)
            except Exception:
                pass

    async def _paste_images_via_event(
        self,
        images: List[Dict[str, str]],
        target_locator: Locator,
        check_client_disconnected: Callable,
    ) -> bool:
        self.logger.info(
            f"[{self.req_id}] (备选) 正在通过虚拟粘贴事件上传 {len(images)} 张图片..."
        )
        expected_count = len(images)

        try:
            await self._check_disconnect(check_client_disconnected, "虚拟粘贴")

            await self.page.evaluate(
                'document.querySelector("ms-prompt-box textarea")?.focus()'
            )

            script = """
            async (images) => {
                try {
                    const dataTransfer = new DataTransfer();
                    for (let i = 0; i < images.length; i++) {
                        const img = images[i];
                        try {
                            const byteCharacters = atob(img.data);
                            const byteNumbers = new Array(byteCharacters.length);
                            for (let j = 0; j < byteCharacters.length; j++) {
                                byteNumbers[j] = byteCharacters.charCodeAt(j);
                            }
                            const byteArray = new Uint8Array(byteNumbers);
                            const blob = new Blob([byteArray], { type: img.mime });
                            const ext = img.mime.split('/')[1] || 'png';
                            const filename = `pasted_image_${i + 1}.${ext}`;
                            const file = new File([blob], filename, { type: img.mime, lastModified: Date.now() });
                            dataTransfer.items.add(file);
                        } catch (err) {
                            console.error(`[Paste] 处理图片 ${i + 1} 失败:`, err);
                        }
                    }
                    if (dataTransfer.files.length === 0) {
                        return { success: false, error: "没有文件被添加到 DataTransfer" };
                    }
                    const pasteEvent = new ClipboardEvent('paste', { bubbles: true, cancelable: true });
                    Object.defineProperty(pasteEvent, 'clipboardData', { value: dataTransfer, writable: false, configurable: true });
                    const textarea = document.querySelector('ms-prompt-box textarea');
                    if (textarea) { textarea.focus(); textarea.dispatchEvent(pasteEvent); }
                    document.dispatchEvent(pasteEvent);
                    return { success: true, count: dataTransfer.files.length };
                } catch (err) {
                    return { success: false, error: err.message };
                }
            }
            """
            result = await self.page.evaluate(script, images)
            if not result or not result.get("success"):
                self.logger.error(
                    f"[{self.req_id}] 虚拟粘贴触发失败: {result.get('error', '未知')}"
                )
                return False

            self.logger.info(f"[{self.req_id}] 虚拟粘贴事件已触发")
            await asyncio.sleep(SLEEP_IMAGE_UPLOAD)

            uploaded_images = 0
            for selector in [
                "ms-prompt-box img",
                ".prompt-input img",
                'img[src*="blob:"]',
            ]:
                try:
                    locator = self.page.locator(selector)
                    count = await locator.count()
                    if count >= expected_count:
                        uploaded_images = count
                        break
                except Exception:
                    pass

            if uploaded_images >= expected_count:
                self.logger.info(
                    f"[{self.req_id}] ✅ 虚拟粘贴成功，检测到 {uploaded_images} 张图片"
                )
                await dump_page(
                    self.page,
                    f"chat_images_pasted_{uploaded_images}_{self.req_id}",
                    self.logger,
                )
                return True
            else:
                self.logger.warning(
                    f"[{self.req_id}] 虚拟粘贴验证失败: 检测到{uploaded_images}/{expected_count}张"
                )
                return False

        except Exception as e:
            if isinstance(e, ClientDisconnectedError):
                raise
            self.logger.error(f"[{self.req_id}] ❌ 虚拟粘贴异常: {e}")
            return False

    async def submit_prompt(
        self, prompt: str, image_list: List, check_client_disconnected: Callable
    ):
        self.logger.info(f"[{self.req_id}] 📤 提交提示 ({len(prompt)} chars)...")
        prompt_textarea_locator, matched_selector = await get_first_visible_locator(
            self.page, PROMPT_TEXTAREA_SELECTORS, timeout=15000
        )
        if not prompt_textarea_locator:
            self.logger.warning(f"[{self.req_id}] 未找到输入框，尝试默认选择器")
            prompt_textarea_locator = self.page.locator(PROMPT_TEXTAREA_SELECTOR)
        else:
            self.logger.info(f"[{self.req_id}] 找到输入框 (匹配: {matched_selector})")
        autosize_wrapper_selectors = [
            "ms-prompt-input-wrapper .text-wrapper",
            "ms-prompt-box .text-wrapper",
            ".prompt-input-wrapper-container",
        ]
        autosize_wrapper_locator = None
        for ws in autosize_wrapper_selectors:
            loc = self.page.locator(ws)
            if await loc.count() > 0:
                autosize_wrapper_locator = loc
                break
        submit_button_locator, submit_matched = await get_first_visible_locator(
            self.page, SUBMIT_BUTTON_SELECTORS, timeout=3000
        )
        if not submit_button_locator:
            submit_button_locator = self.page.locator(SUBMIT_BUTTON_SELECTOR)
        else:
            self.logger.info(f"[{self.req_id}] 找到提交按钮 (匹配: {submit_matched})")
        try:
            await expect_async(prompt_textarea_locator).to_be_visible(timeout=5000)
            await self._check_disconnect(check_client_disconnected, "输入框可见后")

            if image_list:
                self.logger.info(
                    f"[{self.req_id}] 开始为 {len(image_list)} 张图片执行批量上传。"
                )
                processed_images = []
                for index, image_url in enumerate(image_list):
                    match = re.match("data:(image/\\w+);base64,(.*)", image_url)
                    if not match:
                        self.logger.warning(
                            f"[{self.req_id}]  图片 {index + 1} 的 base64 格式无效，已跳过。"
                        )
                        continue
                    processed_images.append(
                        {"mime": match.group(1), "data": match.group(2)}
                    )

                if processed_images:
                    try:
                        upload_success = await self._upload_images_via_file_input(
                            processed_images, check_client_disconnected
                        )

                        if not upload_success:
                            self.logger.info(f"[{self.req_id}] 回退到虚拟粘贴模式...")
                            await self._paste_images_via_event(
                                processed_images,
                                prompt_textarea_locator,
                                check_client_disconnected,
                            )

                        await asyncio.sleep(SLEEP_LONG)

                    except Exception as upload_err:
                        self.logger.error(
                            f"[{self.req_id}] 图片上传整体流程异常: {upload_err}。继续提交文字。"
                        )

            self.logger.info(f"[{self.req_id}] 正在填充文字内容...")
            await prompt_textarea_locator.evaluate(
                '(element, text) => { element.value = text; element.dispatchEvent(new Event("input", { bubbles: true })); }',
                prompt,
            )
            if autosize_wrapper_locator:
                try:
                    await autosize_wrapper_locator.evaluate(
                        '(element, text) => { element.setAttribute("data-value", text); }',
                        prompt,
                    )
                except Exception:
                    pass
            await dump_page(self.page, f"chat_prompt_filled_{self.req_id}", self.logger)

            await self._check_disconnect(check_client_disconnected, "输入框填充后")
            self.logger.info(f"[{self.req_id}] 文字填充完成，等待发送按钮...")
            try:
                await expect_async(submit_button_locator).to_be_enabled(timeout=15000)
                self.logger.info(f"[{self.req_id}]  发送按钮已启用。")
            except Exception as e_pw_enabled:
                self.logger.warning(
                    f"[{self.req_id}]  等待发送按钮启用超时: {e_pw_enabled}，尝试继续提交..."
                )
            await self._check_disconnect(check_client_disconnected, "发送按钮启用后")
            await asyncio.sleep(SLEEP_TICK)
            submitted_successfully = await self._try_shortcut_submit(
                prompt_textarea_locator, check_client_disconnected
            )
            if not submitted_successfully:
                # Check if response already started (submission may have succeeded despite verification failure)
                response_container = self.page.locator(RESPONSE_CONTAINER_SELECTOR)
                if (
                    await response_container.count() > 0
                    and await response_container.last.is_visible(timeout=2000)
                ):
                    self.logger.info(
                        f"[{self.req_id}] 快捷键验证失败但响应已开始，视为提交成功。"
                    )
                else:
                    self.logger.info(
                        f"[{self.req_id}] 快捷键提交失败，尝试点击提交按钮..."
                    )
                    await click_element(
                        self.page,
                        submit_button_locator,
                        "Submit Button",
                        self.req_id,
                        internal_timeout=10000,
                    )
                    self.logger.info(f"[{self.req_id}]  提交按钮点击完成。")
            await self._check_disconnect(check_client_disconnected, "提交后")
            await dump_page(self.page, f"chat_submit_{self.req_id}", self.logger)

        except Exception as e_input_submit:
            await dump_page(self.page, f"chat_submit_error_{self.req_id}", self.logger)
            self.logger.error(
                f"[{self.req_id}] 输入和提交过程中发生错误: {e_input_submit}"
            )
            if not isinstance(e_input_submit, ClientDisconnectedError):
                await save_error_snapshot(f"input_submit_error_{self.req_id}")
            raise

    async def _verify_images_uploaded(
        self, expected_count: int, check_client_disconnected: Callable
    ):
        self.logger.info(
            f"[{self.req_id}] 开始验证 {expected_count} 张图片的上传状态..."
        )
        max_wait_time = 10.0
        check_interval = 0.5
        max_checks = int(max_wait_time / check_interval)
        consecutive_success_required = 2
        consecutive_success_count = 0
        for attempt in range(max_checks):
            try:
                await self._check_disconnect(
                    check_client_disconnected, f"图片上传验证 - 第{attempt + 1}次检查"
                )
                error_indicators = [
                    '[class*="error"]',
                    '[data-testid*="error"]',
                    "mat-error",
                    ".upload-error",
                ]
                for error_selector in error_indicators:
                    try:
                        error_locator = self.page.locator(error_selector)
                        if await error_locator.count() > 0:
                            error_text = await error_locator.first.inner_text(
                                timeout=1000
                            )
                            if (
                                "upload" in error_text.lower()
                                or "file" in error_text.lower()
                            ):
                                self.logger.error(
                                    f"[{self.req_id}] 检测到上传错误: {error_text}"
                                )
                                raise Exception(f"文件上传失败: {error_text}")
                    except Exception:
                        continue
                uploaded_images = 0
                priority_selectors = [
                    "ms-prompt-box img",
                    ".prompt-input img",
                    "textarea[data-test-ms-prompt-textarea] ~ * img",
                    '[data-testid="prompt-input"] img',
                ]
                for selector in priority_selectors:
                    try:
                        locator = self.page.locator(selector)
                        count = await locator.count()
                        if count > 0:
                            for i in range(count):
                                img = locator.nth(i)
                                src = await img.get_attribute("src", timeout=1000)
                                if src and (
                                    "blob:" in src
                                    or "data:" in src
                                    or "googleusercontent.com" in src
                                ):
                                    uploaded_images += 1
                    except Exception:
                        continue
                if uploaded_images < expected_count:
                    backup_selectors = [
                        'img[alt*="Uploaded"]',
                        'img[src*="blob:"]',
                        ".image-preview img",
                        '[data-testid*="image"] img',
                        'img[src*="googleusercontent.com"]',
                        ".uploaded-image",
                    ]
                    for selector in backup_selectors:
                        try:
                            locator = self.page.locator(selector)
                            count = await locator.count()
                            uploaded_images = max(uploaded_images, count)
                            if uploaded_images >= expected_count:
                                break
                        except Exception:
                            continue
                if uploaded_images >= expected_count:
                    consecutive_success_count += 1
                    self.logger.info(
                        f"[{self.req_id}] ✅ 第{consecutive_success_count}次检测到 {uploaded_images}/{expected_count} 张图片"
                    )
                    if consecutive_success_count >= consecutive_success_required:
                        self.logger.info(
                            f"[{self.req_id}] ✅ 连续{consecutive_success_required}次成功验证，图片上传稳定"
                        )
                        return
                else:
                    consecutive_success_count = 0

                await asyncio.sleep(check_interval)
            except Exception as e_verify:
                self.logger.warning(
                    f"[{self.req_id}] 图片上传验证第{attempt + 1}次检查时出错: {e_verify}"
                )
                if "文件上传失败" in str(e_verify):
                    raise
                if attempt < max_checks - 1:
                    await asyncio.sleep(check_interval)
                    continue
                else:
                    break
        raise Exception(f"图片上传验证超时（{max_wait_time}秒），但将尝试继续提交。")

    async def _verify_submission(
        self, prompt_textarea_locator: Locator, original_content: str
    ) -> bool:
        try:
            current_content = (
                await prompt_textarea_locator.last.input_value(timeout=3000) or ""
            )
            if original_content and not current_content.strip():
                self.logger.info(
                    f"[{self.req_id}] Verification Method 1: Textarea cleared, submission successful."
                )
                return True
            submit_button_locator = self.page.locator(SUBMIT_BUTTON_SELECTOR)
            if await submit_button_locator.is_disabled(timeout=3000):
                self.logger.info(
                    f"[{self.req_id}] Verification Method 2: Submit button is disabled, submission successful."
                )
                return True
            response_container = self.page.locator(RESPONSE_CONTAINER_SELECTOR)
            if (
                await response_container.count() > 0
                and await response_container.last.is_visible(timeout=2000)
            ):
                self.logger.info(
                    f"[{self.req_id}] Verification Method 3: New response container detected, submission successful."
                )
                return True
        except Exception as verify_err:
            self.logger.warning(
                f"[{self.req_id}] Could not confirm submission during verification: {type(verify_err).__name__}"
            )
            # Even if verification timed out, check if response already started
            try:
                response_container = self.page.locator(RESPONSE_CONTAINER_SELECTOR)
                if (
                    await response_container.count() > 0
                    and await response_container.last.is_visible(timeout=1000)
                ):
                    self.logger.info(
                        f"[{self.req_id}] Verification fallback: Response container visible, submission successful."
                    )
                    return True
            except Exception:
                pass
            return False
        return False

    async def _try_shortcut_submit(
        self, prompt_textarea_locator, check_client_disconnected: Callable
    ) -> bool:
        import os

        self.logger.info(
            f"[{self.req_id}] Attempting to submit using keyboard shortcuts..."
        )
        try:
            host_os_from_launcher = os.environ.get("HOST_OS_FOR_SHORTCUT")
            is_mac_determined = False
            if host_os_from_launcher == "Darwin":
                is_mac_determined = True
            elif host_os_from_launcher in ["Windows", "Linux"]:
                is_mac_determined = False
            else:
                try:
                    user_agent_data_platform = await self.page.evaluate(
                        "() => navigator.userAgentData?.platform || ''"
                    )
                except Exception:
                    user_agent_string = await self.page.evaluate(
                        "() => navigator.userAgent || ''"
                    )
                    user_agent_string_lower = user_agent_string.lower()
                    if (
                        "macintosh" in user_agent_string_lower
                        or "mac os x" in user_agent_string_lower
                    ):
                        user_agent_data_platform = "macOS"
                    else:
                        user_agent_data_platform = "Other"
                is_mac_determined = "mac" in user_agent_data_platform.lower()
            shortcut_modifier = "Meta" if is_mac_determined else "Control"
            await prompt_textarea_locator.focus(timeout=15000)
            await self._check_disconnect(check_client_disconnected, "After Input Focus")
            original_content = (
                await prompt_textarea_locator.input_value(timeout=2000) or ""
            )
            self.logger.info(
                f"[{self.req_id}]   - Attempting {shortcut_modifier}+Enter..."
            )
            await self.page.keyboard.press(f"{shortcut_modifier}+Enter")
            await asyncio.sleep(1.5)
            if await self._verify_submission(prompt_textarea_locator, original_content):
                self.logger.info(
                    f"[{self.req_id}]   ✅ Success with {shortcut_modifier}+Enter."
                )
                return True
            self.logger.warning(
                f"[{self.req_id}]   - {shortcut_modifier}+Enter submission failed verification."
            )
            self.logger.info(f"[{self.req_id}]   - Attempting Enter...")
            await prompt_textarea_locator.focus(timeout=5000)
            await self.page.keyboard.press("Enter")
            await asyncio.sleep(1.5)
            if await self._verify_submission(prompt_textarea_locator, original_content):
                self.logger.info(f"[{self.req_id}]   ✅ Success with Enter.")
                return True
            self.logger.warning(
                f"[{self.req_id}]   - Enter submission failed verification."
            )
            self.logger.error(
                f"[{self.req_id}] All shortcut submission attempts failed."
            )
            return False
        except Exception as shortcut_err:
            self.logger.error(
                f"[{self.req_id}] Exception during shortcut submission: {shortcut_err}"
            )
            return False

    async def stop_generation(self, check_client_disconnected: Callable):
        self.logger.info(f"[{self.req_id}] 通过导航到新聊天来停止生成...")
        try:
            await self.clear_chat_history(check_client_disconnected)
            self.logger.info(f"[{self.req_id}] 成功导航到新聊天以停止生成。")
        except Exception as e:
            self.logger.error(f"[{self.req_id}] 通过导航到新聊天停止生成失败: {e}")

    async def get_response(self, check_client_disconnected: Callable) -> str:
        self.logger.info(f"[{self.req_id}] 📥 等待响应...")
        try:
            await self._check_disconnect(check_client_disconnected, "获取响应 - 开始前")
            response_container_locator = self.page.locator(
                RESPONSE_CONTAINER_SELECTOR
            ).last
            response_element_locator = response_container_locator.locator(
                RESPONSE_TEXT_SELECTOR
            )
            await expect_async(response_element_locator).to_be_attached(timeout=90000)
            await dump_page(
                self.page, f"chat_response_visible_{self.req_id}", self.logger
            )
            await self._check_disconnect(
                check_client_disconnected, "获取响应 - 响应元素已附加"
            )
            submit_button_locator = self.page.locator(SUBMIT_BUTTON_SELECTOR)
            edit_button_locator = self.page.locator("ms-chat-turn").last.locator(
                EDIT_MESSAGE_BUTTON_SELECTOR
            )
            input_field_locator = self.page.locator(PROMPT_TEXTAREA_SELECTOR)
            await self._check_disconnect(
                check_client_disconnected, "获取响应 - 开始等待完成前"
            )
            completion_detected = await _wait_for_response_completion(
                self.page,
                input_field_locator,
                submit_button_locator,
                edit_button_locator,
                self.req_id,
                check_client_disconnected,
                None,
            )
            await self._check_disconnect(
                check_client_disconnected, "获取响应 - 完成检测后"
            )
            if not completion_detected:
                self.logger.warning(
                    f"[{self.req_id}] 响应完成检测失败，尝试获取当前内容"
                )
            else:
                self.logger.info(f"[{self.req_id}]  响应完成检测成功")
            await self._check_disconnect(
                check_client_disconnected, "获取响应 - 获取最终内容前"
            )
            final_content = await _get_final_response_content(
                self.page, self.req_id, check_client_disconnected
            )
            await self._check_disconnect(
                check_client_disconnected, "获取响应 - 获取最终内容后"
            )
            if not final_content or not final_content.strip():
                self.logger.warning(f"[{self.req_id}]  获取到的响应内容为空")
                await save_error_snapshot(f"empty_response_{self.req_id}")
                return ""
            self.logger.info(
                f"[{self.req_id}]  成功获取响应内容 ({len(final_content)} chars)"
            )
            return final_content
        except ClientDisconnectedError:
            self.logger.info(f"[{self.req_id}]  获取响应过程中客户端断开连接")
            raise
        except TimeoutError as e:
            await dump_page(self.page, f"chat_timeout_{self.req_id}", self.logger)
            self.logger.error(f"[{self.req_id}]  获取响应超时: {e}")
            await save_error_snapshot(f"get_response_timeout_{self.req_id}")
            raise
        except Exception as e:
            await dump_page(
                self.page, f"chat_response_error_{self.req_id}", self.logger
            )
            self.logger.error(f"[{self.req_id}]  获取响应时出错: {e}")
            if not isinstance(e, ClientDisconnectedError):
                await save_error_snapshot(f"get_response_error_{self.req_id}")
            raise
