import asyncio
from typing import Callable, List, Dict, Any, Optional
import base64
import tempfile
import re
import os
from playwright.async_api import Page as AsyncPage, expect as expect_async, TimeoutError, Locator
from config import TEMPERATURE_INPUT_SELECTOR, MAX_OUTPUT_TOKENS_SELECTOR, STOP_SEQUENCE_INPUT_SELECTOR, MAT_CHIP_REMOVE_BUTTON_SELECTOR, TOP_P_INPUT_SELECTOR, SUBMIT_BUTTON_SELECTOR, OVERLAY_SELECTOR, PROMPT_TEXTAREA_SELECTOR, RESPONSE_CONTAINER_SELECTOR, RESPONSE_TEXT_SELECTOR, EDIT_MESSAGE_BUTTON_SELECTOR, USE_URL_CONTEXT_SELECTOR, UPLOAD_BUTTON_SELECTOR, INSERT_BUTTON_SELECTOR, THINKING_MODE_TOGGLE_SELECTOR, SET_THINKING_BUDGET_TOGGLE_SELECTOR, THINKING_BUDGET_INPUT_SELECTOR, GROUNDING_WITH_GOOGLE_SEARCH_TOGGLE_SELECTOR, ZERO_STATE_SELECTOR, SYSTEM_INSTRUCTIONS_BUTTON_SELECTOR, SYSTEM_INSTRUCTIONS_TEXTAREA_SELECTOR, SKIP_PREFERENCE_VOTE_BUTTON_SELECTOR, CLICK_TIMEOUT_MS, WAIT_FOR_ELEMENT_TIMEOUT_MS, CLEAR_CHAT_VERIFY_TIMEOUT_MS, DEFAULT_TEMPERATURE, DEFAULT_MAX_OUTPUT_TOKENS, DEFAULT_STOP_SEQUENCES, DEFAULT_TOP_P, ENABLE_URL_CONTEXT, ENABLE_THINKING_BUDGET, DEFAULT_THINKING_BUDGET, ENABLE_GOOGLE_SEARCH
from models import ClientDisconnectedError, ElementClickError
from .operations import save_error_snapshot, _wait_for_response_completion, _get_final_response_content, click_element

class PageController:

    def __init__(self, page: AsyncPage, logger, req_id: str):
        self.page = page
        self.logger = logger
        self.req_id = req_id

    async def _check_disconnect(self, check_client_disconnected: Callable, stage: str):
        """检查客户端是否断开连接或请求是否被取消。"""
        if check_client_disconnected(stage):
            raise ClientDisconnectedError(f'[{self.req_id}] Client disconnected or request cancelled at stage: {stage}')

    async def _click_and_verify(self, trigger_locator: Locator, expected_locator: Locator, trigger_name: str, expected_name: str, max_retries: int=3, delay_between_retries: float=0.5) -> None:
        """
        点击一个元素并验证另一个元素是否出现，包含重试逻辑。
        """
        for attempt in range(max_retries):
            self.logger.info(f"[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 点击 '{trigger_name}'...")
            try:
                await click_element(self.page, trigger_locator, trigger_name, self.req_id)
                self.logger.info(f"[{self.req_id}] 等待 '{expected_name}' 出现...")
                await expect_async(expected_locator).to_be_visible(timeout=1000)
                self.logger.info(f"[{self.req_id}] ✅ '{expected_name}' 已出现。")
                return
            except (ElementClickError, TimeoutError) as e:
                self.logger.warning(f"[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 失败: '{expected_name}' did not appear after clicking. Error: {type(e).__name__}")
                if attempt < max_retries - 1:
                    await asyncio.sleep(delay_between_retries)
                else:
                    self.logger.error(f"[{self.req_id}] 达到最大重试次数，未能打开 '{expected_name}'。")
                    raise ElementClickError(f"Failed to reveal '{expected_name}' after {max_retries} attempts.") from e
            except Exception as e:
                self.logger.error(f'[{self.req_id}] _click_and_verify 中发生意外错误: {e}')
                raise

    async def continuously_handle_skip_button(self, stop_event: asyncio.Event, check_client_disconnected: Callable):
        """在后台持续监控并处理“跳过”按钮，直到收到停止信号。"""
        self.logger.info(f"[{self.req_id}] 'Skip'按钮后台监控任务已启动。")
        while not stop_event.is_set():
            try:
                skip_button_locator = self.page.locator(SKIP_PREFERENCE_VOTE_BUTTON_SELECTOR)
                await expect_async(skip_button_locator).to_be_visible(timeout=1000)
                self.logger.info(f"[{self.req_id}] (监控) 检测到'Skip'按钮，尝试点击...")
                try:
                    await click_element(self.page, skip_button_locator, 'Skip Preference Vote Button', self.req_id)
                    self.logger.info(f"[{self.req_id}] (监控) 'Skip'按钮已成功点击。")
                except Exception as click_err:
                    self.logger.error(f"[{self.req_id}] (监控) 'Skip'按钮点击失败: {click_err}。即将刷新页面...")
                    await self.clear_chat_history(check_client_disconnected)
                    self.logger.info(f'[{self.req_id}] (监控) 页面已刷新。')
            except TimeoutError:
                self.logger.debug(f"[{self.req_id}] (监控) 'Skip'按钮未找到，继续轮询。")
            except Exception as e:
                if not stop_event.is_set():
                    if 'Timeout' in type(e).__name__:
                        self.logger.debug(f"[{self.req_id}] (监控) 'Skip'按钮检查超时: {e}")
                    else:
                        self.logger.warning(f"[{self.req_id}] (监控) 处理'Skip'按钮时发生意外错误: {e}")
            await asyncio.sleep(2)
        self.logger.info(f"[{self.req_id}] 'Skip'按钮后台监控任务已停止。")

    async def adjust_parameters(self, request_params: Dict[str, Any], page_params_cache: Dict[str, Any], params_cache_lock: asyncio.Lock, model_id_to_use: str, parsed_model_list: List[Dict[str, Any]], check_client_disconnected: Callable):
        """调整所有请求参数。"""
        self.logger.info(f'[{self.req_id}] 开始调整所有请求参数...')
        await self._check_disconnect(check_client_disconnected, 'Start Parameter Adjustment')
        temp_to_set = request_params.get('temperature', DEFAULT_TEMPERATURE)
        await self._adjust_temperature(temp_to_set, page_params_cache, params_cache_lock, check_client_disconnected)
        max_tokens_to_set = request_params.get('max_output_tokens', DEFAULT_MAX_OUTPUT_TOKENS)
        await self._adjust_max_tokens(max_tokens_to_set, page_params_cache, params_cache_lock, model_id_to_use, parsed_model_list, check_client_disconnected)
        stop_to_set = request_params.get('stop', DEFAULT_STOP_SEQUENCES)
        await self._adjust_stop_sequences(stop_to_set, page_params_cache, params_cache_lock, check_client_disconnected)
        top_p_to_set = request_params.get('top_p', DEFAULT_TOP_P)
        await self._adjust_top_p(top_p_to_set, check_client_disconnected)
        await self._ensure_tools_panel_expanded(check_client_disconnected)
        if ENABLE_URL_CONTEXT:
            await self._open_url_content(check_client_disconnected)
        else:
            self.logger.info(f'[{self.req_id}] URL Context 功能已禁用，跳过调整。')
        await self._handle_thinking_budget(request_params, check_client_disconnected)
        await self._adjust_google_search(request_params, check_client_disconnected)

    async def set_system_instructions(self, system_prompt: str, check_client_disconnected: Callable):
        """设置系统指令。"""
        if not system_prompt:
            return
        self.logger.info(f'[{self.req_id}] 正在设置系统指令...')
        await self._check_disconnect(check_client_disconnected, 'Start System Instructions')
        try:
            sys_prompt_button = self.page.locator(SYSTEM_INSTRUCTIONS_BUTTON_SELECTOR)
            sys_prompt_textarea = self.page.locator(SYSTEM_INSTRUCTIONS_TEXTAREA_SELECTOR)
            await self._click_and_verify(sys_prompt_button, sys_prompt_textarea, 'System Instructions Button', 'System Instructions Textarea')
            await expect_async(sys_prompt_textarea).to_be_editable(timeout=5000)
            await sys_prompt_textarea.fill(system_prompt, timeout=5000)
            await expect_async(sys_prompt_textarea).to_have_value(system_prompt, timeout=5000)
            self.logger.info(f'[{self.req_id}] 系统指令已成功填充并验证。')
        except Exception as e:
            self.logger.error(f'[{self.req_id}] 设置系统指令时出错: {e}')
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _control_thinking_mode_toggle(self, should_be_checked: bool, check_client_disconnected: Callable):
        """根据 should_be_checked 的值，控制 "Thinking Mode" 主开关的状态。"""
        toggle_selector = THINKING_MODE_TOGGLE_SELECTOR
        self.logger.info(f"[{self.req_id}] 控制 'Thinking Mode' 开关，期望状态: {('启用' if should_be_checked else '禁用')}...")
        try:
            toggle_locator = self.page.locator(toggle_selector)
            await expect_async(toggle_locator).to_be_visible(timeout=7000)
            await self._check_disconnect(check_client_disconnected, '思考模式开关 - 元素可见后')
            parent_toggle_locator = toggle_locator.locator('xpath=../..')
            is_disabled_class = await parent_toggle_locator.get_attribute('class')
            if is_disabled_class and 'mat-mdc-slide-toggle-disabled' in is_disabled_class:
                self.logger.info(f"[{self.req_id}] 'Thinking Mode' 开关当前被禁用，跳过操作。")
                return
            is_checked_str = await toggle_locator.get_attribute('aria-checked')
            current_state_is_checked = is_checked_str == 'true'
            self.logger.info(f"[{self.req_id}] 'Thinking Mode' 开关当前 'aria-checked' 状态: {is_checked_str}")
            if current_state_is_checked != should_be_checked:
                action = '启用' if should_be_checked else '禁用'
                self.logger.info(f"[{self.req_id}] 'Thinking Mode' 开关与期望不符，正在点击以{action}...")
                await click_element(self.page, toggle_locator, 'Thinking Mode Toggle', self.req_id)
                await self._check_disconnect(check_client_disconnected, f'思考模式开关 - 点击{action}后')
                await asyncio.sleep(0.5)
                new_state_str = await toggle_locator.get_attribute('aria-checked')
                if (new_state_str == 'true') == should_be_checked:
                    self.logger.info(f"[{self.req_id}]  'Thinking Mode' 开关已成功{action}。")
                else:
                    self.logger.warning(f"[{self.req_id}]  'Thinking Mode' 开关{action}后验证失败。当前状态: '{new_state_str}'")
            else:
                self.logger.info(f"[{self.req_id}] 'Thinking Mode' 开关已处于期望状态，无需操作。")
        except Exception as e:
            self.logger.error(f"[{self.req_id}]  操作 'Thinking Mode toggle' 开关时发生错误: {e}")
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _handle_thinking_budget(self, request_params: Dict[str, Any], check_client_disconnected: Callable):
        """处理思考预算的调整逻辑。"""
        reasoning_effort = request_params.get('reasoning_effort')
        should_enable_thinking_mode = True
        if isinstance(reasoning_effort, str) and reasoning_effort.lower() == 'none':
            should_enable_thinking_mode = False
        elif reasoning_effort is None and (not ENABLE_THINKING_BUDGET):
            should_enable_thinking_mode = False
        self.logger.info(f"[{self.req_id}] 根据请求和配置，'Thinking Mode' 应处于 {('启用' if should_enable_thinking_mode else '禁用')} 状态。")
        await self._control_thinking_mode_toggle(should_be_checked=should_enable_thinking_mode, check_client_disconnected=check_client_disconnected)
        if not should_enable_thinking_mode:
            self.logger.info(f"[{self.req_id}] 'Thinking Mode' 已禁用，跳过预算设置。")
            return
        self.logger.info(f"[{self.req_id}] 'Thinking Mode' 已启用，现在确保手动预算已开启。")
        await self._control_thinking_budget_toggle(should_be_checked=True, check_client_disconnected=check_client_disconnected)
        await self._adjust_thinking_budget(reasoning_effort, check_client_disconnected)

    def _parse_thinking_budget(self, reasoning_effort: Optional[Any]) -> Optional[int]:
        token_budget = None
        if reasoning_effort is None:
            token_budget = DEFAULT_THINKING_BUDGET
            self.logger.info(f"[{self.req_id}] 'reasoning_effort' 为空，使用默认思考预算: {token_budget}")
        elif isinstance(reasoning_effort, int):
            token_budget = reasoning_effort
        elif isinstance(reasoning_effort, str):
            if reasoning_effort.lower() == 'none':
                token_budget = DEFAULT_THINKING_BUDGET
                self.logger.info(f"[{self.req_id}] 'reasoning_effort' 为 'none' 字符串，使用默认思考预算: {token_budget}")
            else:
                effort_map = {'low': 1000, 'medium': 8000, 'high': 24000}
                token_budget = effort_map.get(reasoning_effort.lower())
                if token_budget is None:
                    try:
                        token_budget = int(reasoning_effort)
                    except (ValueError, TypeError):
                        pass
        if token_budget is None:
            self.logger.warning(f"[{self.req_id}] 无法从 '{reasoning_effort}' (类型: {type(reasoning_effort)}) 解析出有效的 token_budget。")
        return token_budget

    async def _adjust_thinking_budget(self, reasoning_effort: Optional[Any], check_client_disconnected: Callable):
        """根据 reasoning_effort 调整思考预算。"""
        self.logger.info(f'[{self.req_id}] 检查并调整思考预算，输入值: {reasoning_effort}')
        token_budget = self._parse_thinking_budget(reasoning_effort)
        if token_budget is None:
            self.logger.warning(f"[{self.req_id}] 无效的 reasoning_effort 值: '{reasoning_effort}'。跳过调整。")
            return
        budget_input_locator = self.page.locator(THINKING_BUDGET_INPUT_SELECTOR)
        try:
            await expect_async(budget_input_locator).to_be_visible(timeout=5000)
            await self._check_disconnect(check_client_disconnected, '思考预算调整 - 输入框可见后')
            self.logger.info(f'[{self.req_id}] 设置思考预算为: {token_budget}')
            await budget_input_locator.fill(str(token_budget), timeout=5000)
            await self._check_disconnect(check_client_disconnected, '思考预算调整 - 填充输入框后')
            await asyncio.sleep(0.1)
            new_value_str = await budget_input_locator.input_value(timeout=3000)
            if int(new_value_str) == token_budget:
                self.logger.info(f'[{self.req_id}]  思考预算已成功更新为: {new_value_str}')
            else:
                self.logger.warning(f'[{self.req_id}]  思考预算更新后验证失败。页面显示: {new_value_str}, 期望: {token_budget}')
        except Exception as e:
            self.logger.error(f'[{self.req_id}]  调整思考预算时出错: {e}')
            if isinstance(e, ClientDisconnectedError):
                raise

    def _should_enable_google_search(self, request_params: Dict[str, Any]) -> bool:
        if 'tools' in request_params and request_params.get('tools') is not None:
            tools = request_params.get('tools')
            has_google_search_tool = False
            if isinstance(tools, list):
                for tool in tools:
                    if isinstance(tool, dict):
                        if tool.get('google_search_retrieval') is not None:
                            has_google_search_tool = True
                            break
                        if tool.get('function', {}).get('name') == 'googleSearch':
                            has_google_search_tool = True
                            break
            self.logger.info(f"[{self.req_id}] 请求中包含 'tools' 参数。检测到 Google Search 工具: {has_google_search_tool}。")
            return has_google_search_tool
        else:
            self.logger.info(f"[{self.req_id}] 请求中不包含 'tools' 参数。使用默认配置 ENABLE_GOOGLE_SEARCH: {ENABLE_GOOGLE_SEARCH}。")
            return ENABLE_GOOGLE_SEARCH

    async def _adjust_google_search(self, request_params: Dict[str, Any], check_client_disconnected: Callable):
        """根据请求参数或默认配置，双向控制 Google Search 开关。"""
        self.logger.info(f'[{self.req_id}] 检查并调整 Google Search 开关...')
        should_enable_search = self._should_enable_google_search(request_params)
        toggle_selector = GROUNDING_WITH_GOOGLE_SEARCH_TOGGLE_SELECTOR
        try:
            toggle_locator = self.page.locator(toggle_selector)
            await expect_async(toggle_locator).to_be_visible(timeout=5000)
            await self._check_disconnect(check_client_disconnected, 'Google Search 开关 - 元素可见后')
            is_checked_str = await toggle_locator.get_attribute('aria-checked')
            is_currently_checked = is_checked_str == 'true'
            self.logger.info(f"[{self.req_id}] Google Search 开关当前状态: '{is_checked_str}'。期望状态: {should_enable_search}")
            if should_enable_search != is_currently_checked:
                action = '打开' if should_enable_search else '关闭'
                self.logger.info(f'[{self.req_id}] Google Search 开关状态与期望不符。正在点击以{action}...')
                await click_element(self.page, toggle_locator, 'Google Search Toggle', self.req_id)
                await self._check_disconnect(check_client_disconnected, f'Google Search 开关 - 点击{action}后')
                await asyncio.sleep(0.5)
                new_state = await toggle_locator.get_attribute('aria-checked')
                if (new_state == 'true') == should_enable_search:
                    self.logger.info(f'[{self.req_id}]  Google Search 开关已成功{action}。')
                else:
                    self.logger.warning(f"[{self.req_id}]  Google Search 开关{action}失败。当前状态: '{new_state}'")
            else:
                self.logger.info(f'[{self.req_id}] Google Search 开关已处于期望状态，无需操作。')
        except Exception as e:
            self.logger.error(f"[{self.req_id}]  操作 'Google Search toggle' 开关时发生错误: {e}")
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _ensure_tools_panel_expanded(self, check_client_disconnected: Callable):
        """确保包含高级工具（URL上下文、思考预算等）的面板是展开的。"""
        self.logger.info(f'[{self.req_id}] 检查并确保工具面板已展开...')
        try:
            collapse_tools_locator = self.page.locator('button[aria-label="Expand or collapse tools"]')
            await expect_async(collapse_tools_locator).to_be_visible(timeout=5000)
            grandparent_locator = collapse_tools_locator.locator('xpath=../..')
            class_string = await grandparent_locator.get_attribute('class', timeout=3000)
            if class_string and 'expanded' not in class_string.split():
                self.logger.info(f'[{self.req_id}] 工具面板未展开，正在点击以展开...')
                await click_element(self.page, collapse_tools_locator, 'Expand/Collapse Tools Button', self.req_id)
                await self._check_disconnect(check_client_disconnected, '展开工具面板后')
                await expect_async(grandparent_locator).to_have_class(re.compile('.*expanded.*'), timeout=5000)
                self.logger.info(f'[{self.req_id}]  工具面板已成功展开。')
            else:
                self.logger.info(f'[{self.req_id}] 工具面板已处于展开状态。')
        except Exception as e:
            self.logger.error(f'[{self.req_id}]  展开工具面板时发生错误: {e}')
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _open_url_content(self, check_client_disconnected: Callable):
        """仅负责打开 URL Context 开关，前提是面板已展开。"""
        try:
            self.logger.info(f'[{self.req_id}] 检查并启用 URL Context 开关...')
            use_url_content_selector = self.page.locator(USE_URL_CONTEXT_SELECTOR)
            await expect_async(use_url_content_selector).to_be_visible(timeout=5000)
            is_checked = await use_url_content_selector.get_attribute('aria-checked')
            if 'false' == is_checked:
                self.logger.info(f'[{self.req_id}] URL Context 开关未开启，正在点击以开启...')
                await click_element(self.page, use_url_content_selector, 'URL Context Toggle', self.req_id)
                await self._check_disconnect(check_client_disconnected, '点击URLCONTEXT后')
                self.logger.info(f'[{self.req_id}]  URL Context 开关已点击。')
            else:
                self.logger.info(f'[{self.req_id}] URL Context 开关已处于开启状态。')
        except Exception as e:
            self.logger.error(f'[{self.req_id}]  操作 USE_URL_CONTEXT_SELECTOR 时发生错误:{e}。')
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _control_thinking_budget_toggle(self, should_be_checked: bool, check_client_disconnected: Callable):
        """
        根据 should_be_checked 的值，控制 "Set Thinking Budget" (手动预算) 滑块开关的状态。
        """
        toggle_selector = SET_THINKING_BUDGET_TOGGLE_SELECTOR
        self.logger.info(f"[{self.req_id}] 控制 'Set Thinking Budget' 开关，期望状态: {('选中' if should_be_checked else '未选中')}...")
        try:
            toggle_locator = self.page.locator(toggle_selector)
            await expect_async(toggle_locator).to_be_visible(timeout=5000)
            await self._check_disconnect(check_client_disconnected, '手动预算开关 - 元素可见后')
            is_checked_str = await toggle_locator.get_attribute('aria-checked')
            current_state_is_checked = is_checked_str == 'true'
            self.logger.info(f"[{self.req_id}] 手动预算开关当前 'aria-checked' 状态: {is_checked_str} (当前是否选中: {current_state_is_checked})")
            if current_state_is_checked != should_be_checked:
                action = '启用' if should_be_checked else '禁用'
                self.logger.info(f'[{self.req_id}] 手动预算开关当前状态与期望不符，正在点击以{action}...')
                await click_element(self.page, toggle_locator, 'Set Thinking Budget Toggle', self.req_id)
                await self._check_disconnect(check_client_disconnected, f'手动预算开关 - 点击{action}后')
                await asyncio.sleep(0.5)
                new_state_str = await toggle_locator.get_attribute('aria-checked')
                new_state_is_checked = new_state_str == 'true'
                if new_state_is_checked == should_be_checked:
                    self.logger.info(f"[{self.req_id}]  'Set Thinking Budget' 开关已成功{action}。新状态: {new_state_str}")
                else:
                    self.logger.warning(f"[{self.req_id}]  'Set Thinking Budget' 开关{action}后验证失败。期望状态: '{should_be_checked}', 实际状态: '{new_state_str}'")
            else:
                self.logger.info(f"[{self.req_id}] 'Set Thinking Budget' 开关已处于期望状态，无需操作。")
        except Exception as e:
            self.logger.error(f"[{self.req_id}]  操作 'Set Thinking Budget toggle' 开关时发生错误: {e}")
            if isinstance(e, ClientDisconnectedError):
                raise

    async def _adjust_temperature(self, temperature: float, page_params_cache: dict, params_cache_lock: asyncio.Lock, check_client_disconnected: Callable):
        """调整温度参数。"""
        async with params_cache_lock:
            self.logger.info(f'[{self.req_id}] 检查并调整温度设置...')
            clamped_temp = max(0.0, min(2.0, temperature))
            if clamped_temp != temperature:
                self.logger.warning(f'[{self.req_id}] 请求的温度 {temperature} 超出范围 [0, 2]，已调整为 {clamped_temp}')
            cached_temp = page_params_cache.get('temperature')
            if cached_temp is not None and abs(cached_temp - clamped_temp) < 0.001:
                self.logger.info(f'[{self.req_id}] 温度 ({clamped_temp}) 与缓存值 ({cached_temp}) 一致。跳过页面交互。')
                return
            self.logger.info(f'[{self.req_id}] 请求温度 ({clamped_temp}) 与缓存值 ({cached_temp}) 不一致或缓存中无值。需要与页面交互。')
            temp_input_locator = self.page.locator(TEMPERATURE_INPUT_SELECTOR)
            try:
                await expect_async(temp_input_locator).to_be_visible(timeout=5000)
                await self._check_disconnect(check_client_disconnected, '温度调整 - 输入框可见后')
                current_temp_str = await temp_input_locator.input_value(timeout=3000)
                await self._check_disconnect(check_client_disconnected, '温度调整 - 读取输入框值后')
                current_temp_float = float(current_temp_str)
                self.logger.info(f'[{self.req_id}] 页面当前温度: {current_temp_float}, 请求调整后温度: {clamped_temp}')
                if abs(current_temp_float - clamped_temp) < 0.001:
                    self.logger.info(f'[{self.req_id}] 页面当前温度 ({current_temp_float}) 与请求温度 ({clamped_temp}) 一致。更新缓存并跳过写入。')
                    page_params_cache['temperature'] = current_temp_float
                else:
                    self.logger.info(f'[{self.req_id}] 页面温度 ({current_temp_float}) 与请求温度 ({clamped_temp}) 不同，正在更新...')
                    await temp_input_locator.fill(str(clamped_temp), timeout=5000)
                    await self._check_disconnect(check_client_disconnected, '温度调整 - 填充输入框后')
                    await asyncio.sleep(0.1)
                    new_temp_str = await temp_input_locator.input_value(timeout=3000)
                    new_temp_float = float(new_temp_str)
                    if abs(new_temp_float - clamped_temp) < 0.001:
                        self.logger.info(f'[{self.req_id}]  温度已成功更新为: {new_temp_float}。更新缓存。')
                        page_params_cache['temperature'] = new_temp_float
                    else:
                        self.logger.warning(f'[{self.req_id}]  温度更新后验证失败。页面显示: {new_temp_float}, 期望: {clamped_temp}。清除缓存中的温度。')
                        page_params_cache.pop('temperature', None)
                        await save_error_snapshot(f'temperature_verify_fail_{self.req_id}')
            except ValueError as ve:
                self.logger.error(f'[{self.req_id}] 转换温度值为浮点数时出错. 错误: {ve}。清除缓存中的温度。')
                page_params_cache.pop('temperature', None)
                await save_error_snapshot(f'temperature_value_error_{self.req_id}')
            except Exception as pw_err:
                self.logger.error(f'[{self.req_id}]  操作温度输入框时发生错误: {pw_err}。清除缓存中的温度。')
                page_params_cache.pop('temperature', None)
                await save_error_snapshot(f'temperature_playwright_error_{self.req_id}')
                if isinstance(pw_err, ClientDisconnectedError):
                    raise

    async def _adjust_max_tokens(self, max_tokens: int, page_params_cache: dict, params_cache_lock: asyncio.Lock, model_id_to_use: str, parsed_model_list: list, check_client_disconnected: Callable):
        """调整最大输出Token参数。"""
        async with params_cache_lock:
            self.logger.info(f'[{self.req_id}] 检查并调整最大输出 Token 设置...')
            min_val_for_tokens = 1
            max_val_for_tokens_from_model = 65536
            if model_id_to_use and parsed_model_list:
                current_model_data = next((m for m in parsed_model_list if m.get('id') == model_id_to_use), None)
                if current_model_data and current_model_data.get('supported_max_output_tokens') is not None:
                    try:
                        supported_tokens = int(current_model_data['supported_max_output_tokens'])
                        if supported_tokens > 0:
                            max_val_for_tokens_from_model = supported_tokens
                        else:
                            self.logger.warning(f'[{self.req_id}] 模型 {model_id_to_use} supported_max_output_tokens 无效: {supported_tokens}')
                    except (ValueError, TypeError):
                        self.logger.warning(f'[{self.req_id}] 模型 {model_id_to_use} supported_max_output_tokens 解析失败')
            clamped_max_tokens = max(min_val_for_tokens, min(max_val_for_tokens_from_model, max_tokens))
            if clamped_max_tokens != max_tokens:
                self.logger.warning(f'[{self.req_id}] 请求的最大输出 Tokens {max_tokens} 超出模型范围，已调整为 {clamped_max_tokens}')
            cached_max_tokens = page_params_cache.get('max_output_tokens')
            if cached_max_tokens is not None and cached_max_tokens == clamped_max_tokens:
                self.logger.info(f'[{self.req_id}] 最大输出 Tokens ({clamped_max_tokens}) 与缓存值一致。跳过页面交互。')
                return
            max_tokens_input_locator = self.page.locator(MAX_OUTPUT_TOKENS_SELECTOR)
            try:
                await expect_async(max_tokens_input_locator).to_be_visible(timeout=5000)
                await self._check_disconnect(check_client_disconnected, '最大输出Token调整 - 输入框可见后')
                current_max_tokens_str = await max_tokens_input_locator.input_value(timeout=3000)
                current_max_tokens_int = int(current_max_tokens_str)
                if current_max_tokens_int == clamped_max_tokens:
                    self.logger.info(f'[{self.req_id}] 页面当前最大输出 Tokens ({current_max_tokens_int}) 与请求值 ({clamped_max_tokens}) 一致。更新缓存并跳过写入。')
                    page_params_cache['max_output_tokens'] = current_max_tokens_int
                else:
                    self.logger.info(f'[{self.req_id}] 页面最大输出 Tokens ({current_max_tokens_int}) 与请求值 ({clamped_max_tokens}) 不同，正在更新...')
                    await max_tokens_input_locator.fill(str(clamped_max_tokens), timeout=5000)
                    await self._check_disconnect(check_client_disconnected, '最大输出Token调整 - 填充输入框后')
                    await asyncio.sleep(0.1)
                    new_max_tokens_str = await max_tokens_input_locator.input_value(timeout=3000)
                    new_max_tokens_int = int(new_max_tokens_str)
                    if new_max_tokens_int == clamped_max_tokens:
                        self.logger.info(f'[{self.req_id}]  最大输出 Tokens 已成功更新为: {new_max_tokens_int}')
                        page_params_cache['max_output_tokens'] = new_max_tokens_int
                    else:
                        self.logger.warning(f'[{self.req_id}]  最大输出 Tokens 更新后验证失败。页面显示: {new_max_tokens_int}, 期望: {clamped_max_tokens}。清除缓存。')
                        page_params_cache.pop('max_output_tokens', None)
                        await save_error_snapshot(f'max_tokens_verify_fail_{self.req_id}')
            except (ValueError, TypeError) as ve:
                self.logger.error(f'[{self.req_id}] 转换最大输出 Tokens 值时出错: {ve}。清除缓存。')
                page_params_cache.pop('max_output_tokens', None)
                await save_error_snapshot(f'max_tokens_value_error_{self.req_id}')
            except Exception as e:
                self.logger.error(f'[{self.req_id}]  调整最大输出 Tokens 时出错: {e}。清除缓存。')
                page_params_cache.pop('max_output_tokens', None)
                await save_error_snapshot(f'max_tokens_error_{self.req_id}')
                if isinstance(e, ClientDisconnectedError):
                    raise

    async def _adjust_stop_sequences(self, stop_sequences, page_params_cache: dict, params_cache_lock: asyncio.Lock, check_client_disconnected: Callable):
        """调整停止序列参数。"""
        async with params_cache_lock:
            self.logger.info(f'[{self.req_id}] 检查并设置停止序列...')
            normalized_requested_stops = set()
            if stop_sequences is not None:
                if isinstance(stop_sequences, str):
                    if stop_sequences.strip():
                        normalized_requested_stops.add(stop_sequences.strip())
                elif isinstance(stop_sequences, list):
                    for s in stop_sequences:
                        if isinstance(s, str) and s.strip():
                            normalized_requested_stops.add(s.strip())
            cached_stops_set = page_params_cache.get('stop_sequences')
            if cached_stops_set is not None and cached_stops_set == normalized_requested_stops:
                self.logger.info(f'[{self.req_id}] 请求的停止序列与缓存值一致。跳过页面交互。')
                return
            stop_input_locator = self.page.locator(STOP_SEQUENCE_INPUT_SELECTOR)
            remove_chip_buttons_locator = self.page.locator(MAT_CHIP_REMOVE_BUTTON_SELECTOR)
            try:
                initial_chip_count = await remove_chip_buttons_locator.count()
                removed_count = 0
                max_removals = initial_chip_count + 5
                while await remove_chip_buttons_locator.count() > 0 and removed_count < max_removals:
                    await self._check_disconnect(check_client_disconnected, '停止序列清除 - 循环开始')
                    try:
                        await click_element(self.page, remove_chip_buttons_locator.first, 'Remove Stop Sequence Chip', self.req_id)
                        removed_count += 1
                        await asyncio.sleep(0.15)
                    except Exception:
                        break
                if normalized_requested_stops:
                    await expect_async(stop_input_locator).to_be_visible(timeout=5000)
                    for seq in normalized_requested_stops:
                        await stop_input_locator.fill(seq, timeout=3000)
                        await stop_input_locator.press('Enter', timeout=3000)
                        await asyncio.sleep(0.2)
                page_params_cache['stop_sequences'] = normalized_requested_stops
                self.logger.info(f'[{self.req_id}]  停止序列已成功设置。缓存已更新。')
            except Exception as e:
                self.logger.error(f'[{self.req_id}]  设置停止序列时出错: {e}')
                page_params_cache.pop('stop_sequences', None)
                await save_error_snapshot(f'stop_sequence_error_{self.req_id}')
                if isinstance(e, ClientDisconnectedError):
                    raise

    async def _adjust_top_p(self, top_p: float, check_client_disconnected: Callable):
        """调整Top P参数。"""
        self.logger.info(f'[{self.req_id}] 检查并调整 Top P 设置...')
        clamped_top_p = max(0.0, min(1.0, top_p))
        if abs(clamped_top_p - top_p) > 1e-09:
            self.logger.warning(f'[{self.req_id}] 请求的 Top P {top_p} 超出范围 [0, 1]，已调整为 {clamped_top_p}')
        top_p_input_locator = self.page.locator(TOP_P_INPUT_SELECTOR)
        try:
            await expect_async(top_p_input_locator).to_be_visible(timeout=5000)
            await self._check_disconnect(check_client_disconnected, 'Top P 调整 - 输入框可见后')
            current_top_p_str = await top_p_input_locator.input_value(timeout=3000)
            current_top_p_float = float(current_top_p_str)
            if abs(current_top_p_float - clamped_top_p) > 1e-09:
                self.logger.info(f'[{self.req_id}] 页面 Top P ({current_top_p_float}) 与请求值 ({clamped_top_p}) 不同，正在更新...')
                await top_p_input_locator.fill(str(clamped_top_p), timeout=5000)
                await self._check_disconnect(check_client_disconnected, 'Top P 调整 - 填充输入框后')
                await asyncio.sleep(0.1)
                new_top_p_str = await top_p_input_locator.input_value(timeout=3000)
                new_top_p_float = float(new_top_p_str)
                if abs(new_top_p_float - clamped_top_p) <= 1e-09:
                    self.logger.info(f'[{self.req_id}]  Top P 已成功更新为: {new_top_p_float}')
                else:
                    self.logger.warning(f'[{self.req_id}]  Top P 更新后验证失败。页面显示: {new_top_p_float}, 期望: {clamped_top_p}')
                    await save_error_snapshot(f'top_p_verify_fail_{self.req_id}')
            else:
                self.logger.info(f'[{self.req_id}] 页面 Top P ({current_top_p_float}) 与请求值 ({clamped_top_p}) 一致，无需更改')
        except (ValueError, TypeError) as ve:
            self.logger.error(f'[{self.req_id}] 转换 Top P 值时出错: {ve}')
            await save_error_snapshot(f'top_p_value_error_{self.req_id}')
        except Exception as e:
            self.logger.error(f'[{self.req_id}]  调整 Top P 时出错: {e}')
            await save_error_snapshot(f'top_p_error_{self.req_id}')
            if isinstance(e, ClientDisconnectedError):
                raise

    async def clear_chat_history(self, check_client_disconnected: Callable):
        """通过直接导航到 new_chat URL 来清空聊天记录，并包含重试逻辑。"""
        self.logger.info(f'[{self.req_id}] 开始清空聊天记录 (通过导航)...')
        await self._check_disconnect(check_client_disconnected, 'Start Clear Chat')
        new_chat_url = 'https://aistudio.google.com/prompts/new_chat'
        max_retries = 3
        for attempt in range(max_retries):
            try:
                self.logger.info(f'[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 导航到: {new_chat_url}')
                await self.page.goto(new_chat_url, timeout=15000, wait_until='domcontentloaded')
                await self._check_disconnect(check_client_disconnected, '清空聊天 - 导航后')
                await self._verify_chat_cleared(check_client_disconnected)
                self.logger.info(f'[{self.req_id}] 聊天记录已成功清空并验证。')
                return
            except Exception as e:
                self.logger.warning(f'[{self.req_id}] (尝试 {attempt + 1}/{max_retries}) 清空聊天失败: {e}')
                await self._check_disconnect(check_client_disconnected, f'清空聊天 - 尝试 {attempt + 1} 失败后')
                if attempt < max_retries - 1:
                    await asyncio.sleep(2.0)
                else:
                    self.logger.error(f'[{self.req_id}] 达到最大重试次数，清空聊天失败。')
                    if not (isinstance(e, ClientDisconnectedError) or (hasattr(e, 'name') and 'Disconnect' in e.name)):
                        await save_error_snapshot(f'clear_chat_fatal_error_{self.req_id}')
                    raise

    async def _verify_chat_cleared(self, check_client_disconnected: Callable):
        """验证聊天已清空"""
        self.logger.info(f'[{self.req_id}] 验证聊天是否已清空...')
        await self._check_disconnect(check_client_disconnected, 'Start Verify Clear Chat')
        try:
            await expect_async(self.page).to_have_url(re.compile('.*/prompts/new_chat.*'), timeout=CLEAR_CHAT_VERIFY_TIMEOUT_MS)
            self.logger.info(f'[{self.req_id}]   - URL验证成功: 页面已导航到 new_chat。')
            zero_state_locator = self.page.locator(ZERO_STATE_SELECTOR)
            await expect_async(zero_state_locator).to_be_visible(timeout=5000)
            self.logger.info(f'[{self.req_id}]   - UI验证成功: “零状态”元素可见。')
            self.logger.info(f'[{self.req_id}] 聊天已成功清空 (验证通过)。')
        except Exception as verify_err:
            self.logger.error(f'[{self.req_id}] 错误: 清空聊天验证失败: {verify_err}')
            await save_error_snapshot(f'clear_chat_verify_fail_{self.req_id}')
            self.logger.warning(f'[{self.req_id}] 警告: 清空聊天验证失败，但将继续执行。后续操作可能会受影响。')

    async def submit_prompt(self, prompt: str, image_list: List, check_client_disconnected: Callable):
        """提交提示到页面，并处理文件上传。"""
        self.logger.info(f'[{self.req_id}] 填充并提交提示 ({len(prompt)} chars)...')
        prompt_textarea_locator = self.page.locator(PROMPT_TEXTAREA_SELECTOR)
        autosize_wrapper_locator = self.page.locator('ms-prompt-input-wrapper ms-autosize-textarea')
        submit_button_locator = self.page.locator(SUBMIT_BUTTON_SELECTOR)
        temp_files_to_upload = []
        try:
            await expect_async(prompt_textarea_locator).to_be_visible(timeout=5000)
            await self._check_disconnect(check_client_disconnected, 'After Input Visible')
            await prompt_textarea_locator.evaluate('(element, text) => { element.value = text; element.dispatchEvent(new Event("input", { bubbles: true })); }', prompt)
            await autosize_wrapper_locator.evaluate('(element, text) => { element.setAttribute("data-value", text); }', prompt)
            await self._check_disconnect(check_client_disconnected, 'After Input Fill')
            if image_list:
                self.logger.info(f'[{self.req_id}] 检测到 {len(image_list)} 个文件需要上传。')
                await self._check_disconnect(check_client_disconnected, 'Before Image Upload')
                local_file_paths = []
                for idx, image_source in enumerate(image_list):
                    if image_source.startswith('data:image'):
                        try:
                            match = re.match('data:image/(?P<type>\\w+);base64,(?P<data>.*)', image_source)
                            if match:
                                img_type = match.group('type')
                                img_data = base64.b64decode(match.group('data'))
                                with tempfile.NamedTemporaryFile(suffix=f'.{img_type}', delete=False, mode='wb') as temp_file:
                                    temp_file.write(img_data)
                                    temp_files_to_upload.append(temp_file.name)
                                    local_file_paths.append(temp_file.name)
                                self.logger.info(f'[{self.req_id}] 已将图片{idx + 1} data URI 转换为临时文件: {temp_file.name}')
                            else:
                                self.logger.warning(f'[{self.req_id}] 无法解析的 data URI 格式，已跳过。')
                        except Exception as e_dec:
                            self.logger.error(f'[{self.req_id}] 解码或保存 data URI 时出错: {e_dec}')
                    else:
                        local_file_paths.append(image_source)
                if not local_file_paths:
                    raise Exception('没有可用于上传的有效文件路径。')
                insert_button_locator = self.page.locator(INSERT_BUTTON_SELECTOR)
                upload_button_locator = self.page.locator(UPLOAD_BUTTON_SELECTOR)
                try:
                    await self._click_and_verify(insert_button_locator, upload_button_locator, 'Insert Assets Button', 'Upload File Button')
                except ElementClickError as e:
                    self.logger.error(f"[{self.req_id}] 'Insert Assets' 或 'Upload File' 按钮处理失败: {e}。将刷新会话并重试。")
                    await self.clear_chat_history(check_client_disconnected)
                    raise ElementClickError('Failed to open upload dialog, session refreshed.') from e
                await self._check_disconnect(check_client_disconnected, 'Before File Upload')
                async with self.page.expect_file_chooser(timeout=30000) as fc_info:
                    await click_element(self.page, upload_button_locator, 'Upload File Button', self.req_id)
                file_chooser = await fc_info.value
                await file_chooser.set_files(local_file_paths)
                self.logger.info(f'[{self.req_id}] 已将 {len(local_file_paths)} 个文件设置到文件选择器。')
                copyright_ack_button = self.page.locator('button[aria-label="Agree to the copyright acknowledgement"]')
                try:
                    await click_element(self.page, copyright_ack_button, 'Copyright Acknowledgement Button', self.req_id)
                    self.logger.info(f'[{self.req_id}] 已点击版权确认按钮。')
                    await asyncio.sleep(0.5)
                except (ElementClickError, TimeoutError):
                    self.logger.info(f'[{self.req_id}] 未检测到版权确认按钮或点击失败，跳过。')
                await self._verify_images_uploaded(len(local_file_paths), check_client_disconnected)
                await self._check_disconnect(check_client_disconnected, 'After Image Upload Complete')
            wait_timeout_ms_submit_enabled = 100000
            try:
                await self._check_disconnect(check_client_disconnected, '填充提示后等待发送按钮启用 - 前置检查')
                await expect_async(submit_button_locator).to_be_enabled(timeout=wait_timeout_ms_submit_enabled)
                self.logger.info(f'[{self.req_id}]  发送按钮已启用。')
            except Exception as e_pw_enabled:
                self.logger.error(f'[{self.req_id}]  等待发送按钮启用超时或错误: {e_pw_enabled}')
                await save_error_snapshot(f'submit_button_enable_timeout_{self.req_id}')
                raise
            await self._check_disconnect(check_client_disconnected, 'After Submit Button Enabled')
            await asyncio.sleep(0.3)
            submitted_successfully = await self._try_shortcut_submit(prompt_textarea_locator, check_client_disconnected)
            if not submitted_successfully:
                self.logger.info(f'[{self.req_id}] 快捷键提交失败，尝试点击提交按钮...')
                await click_element(self.page, submit_button_locator, 'Submit Button', self.req_id)
                self.logger.info(f'[{self.req_id}]  提交按钮点击完成。')
            await self._check_disconnect(check_client_disconnected, 'After Submit')
        except Exception as e_input_submit:
            self.logger.error(f'[{self.req_id}] 输入和提交过程中发生错误: {e_input_submit}')
            if not isinstance(e_input_submit, ClientDisconnectedError):
                await save_error_snapshot(f'input_submit_error_{self.req_id}')
            raise
        finally:
            if temp_files_to_upload:
                self.logger.info(f'[{self.req_id}] 清理 {len(temp_files_to_upload)} 个临时文件...')
                for temp_path in temp_files_to_upload:
                    try:
                        os.remove(temp_path)
                        self.logger.info(f'[{self.req_id}]   - 已移除: {temp_path}')
                    except Exception as e_clean:
                        self.logger.warning(f'[{self.req_id}]   - 移除临时文件 {temp_path} 失败: {e_clean}')

    async def _verify_images_uploaded(self, expected_count: int, check_client_disconnected: Callable):
        """强化验证图片是否成功上传到对话中"""
        self.logger.info(f'[{self.req_id}] 开始验证 {expected_count} 张图片的上传状态...')
        max_wait_time = 20.0
        check_interval = 0.3
        max_checks = int(max_wait_time / check_interval)
        consecutive_success_required = 3
        consecutive_success_count = 0
        for attempt in range(max_checks):
            try:
                await self._check_disconnect(check_client_disconnected, f'图片上传验证 - 第{attempt + 1}次检查')
                error_indicators = ['[class*="error"]', '[data-testid*="error"]', 'mat-error', '.upload-error']
                for error_selector in error_indicators:
                    try:
                        error_locator = self.page.locator(error_selector)
                        if await error_locator.count() > 0:
                            error_text = await error_locator.first.inner_text(timeout=1000)
                            if 'upload' in error_text.lower() or 'file' in error_text.lower():
                                self.logger.error(f'[{self.req_id}] 检测到上传错误: {error_text}')
                                raise Exception(f'文件上传失败: {error_text}')
                    except Exception:
                        continue
                uploaded_images = 0
                priority_selectors = ['ms-prompt-input-wrapper img', '.prompt-input img', 'textarea[data-test-ms-prompt-textarea] ~ * img', '[data-testid="prompt-input"] img']
                for selector in priority_selectors:
                    try:
                        locator = self.page.locator(selector)
                        count = await locator.count()
                        if count > 0:
                            for i in range(count):
                                img = locator.nth(i)
                                src = await img.get_attribute('src', timeout=1000)
                                if src and ('blob:' in src or 'data:' in src or 'googleusercontent.com' in src):
                                    uploaded_images += 1
                    except Exception:
                        continue
                if uploaded_images < expected_count:
                    backup_selectors = ['img[alt*="Uploaded"]', 'img[src*="blob:"]', '.image-preview img', '[data-testid*="image"] img', 'img[src*="googleusercontent.com"]', '.uploaded-image']
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
                    self.logger.info(f'[{self.req_id}] ✅ 第{consecutive_success_count}次检测到 {uploaded_images}/{expected_count} 张图片')
                    if consecutive_success_count >= consecutive_success_required:
                        self.logger.info(f'[{self.req_id}] ✅ 连续{consecutive_success_required}次成功验证，图片上传稳定')
                        return
                else:
                    consecutive_success_count = 0
                loading_indicators = ['.uploading', '.loading', '[aria-label*="uploading"]', '[data-testid*="upload-progress"]', 'mat-progress-spinner', '.spinner']
                still_uploading = False
                for indicator_selector in loading_indicators:
                    try:
                        indicator = self.page.locator(indicator_selector)
                        if await indicator.count() > 0:
                            still_uploading = True
                            self.logger.info(f'[{self.req_id}] 检测到上传进度指示器: {indicator_selector}')
                            break
                    except Exception:
                        continue
                if still_uploading:
                    self.logger.info(f'[{self.req_id}] 上传仍在进行，继续等待...')
                else:
                    self.logger.info(f'[{self.req_id}] 当前检测到 {uploaded_images}/{expected_count} 张图片 (需连续{consecutive_success_required}次成功)')
                await asyncio.sleep(check_interval)
            except Exception as e_verify:
                self.logger.warning(f'[{self.req_id}] 图片上传验证第{attempt + 1}次检查时出错: {e_verify}')
                if '文件上传失败' in str(e_verify):
                    raise
                if attempt < max_checks - 1:
                    await asyncio.sleep(check_interval)
                    continue
                else:
                    break
        self.logger.error(f'[{self.req_id}] ❌ 图片上传验证失败：在{max_wait_time}秒内未能确认{expected_count}张图片上传成功')
        try:
            await save_error_snapshot(f'image_upload_verify_fail_{self.req_id}')
            all_images = await self.page.locator('img').count()
            self.logger.error(f'[{self.req_id}] 调试信息：页面中共有 {all_images} 个img元素')
        except Exception:
            pass
        raise Exception(f'图片上传验证失败：期望{expected_count}张图片，验证超时（{max_wait_time}秒）')

    async def _try_shortcut_submit(self, prompt_textarea_locator, check_client_disconnected: Callable) -> bool:
        """尝试使用快捷键提交"""
        import os
        try:
            host_os_from_launcher = os.environ.get('HOST_OS_FOR_SHORTCUT')
            is_mac_determined = False
            if host_os_from_launcher == 'Darwin':
                is_mac_determined = True
            elif host_os_from_launcher in ['Windows', 'Linux']:
                is_mac_determined = False
            else:
                try:
                    user_agent_data_platform = await self.page.evaluate("() => navigator.userAgentData?.platform || ''")
                except Exception:
                    user_agent_string = await self.page.evaluate("() => navigator.userAgent || ''")
                    user_agent_string_lower = user_agent_string.lower()
                    if 'macintosh' in user_agent_string_lower or 'mac os x' in user_agent_string_lower:
                        user_agent_data_platform = 'macOS'
                    else:
                        user_agent_data_platform = 'Other'
                is_mac_determined = 'mac' in user_agent_data_platform.lower()
            shortcut_modifier = 'Meta' if is_mac_determined else 'Control'
            shortcut_key = 'Enter'
            self.logger.info(f'[{self.req_id}] 使用快捷键: {shortcut_modifier}+{shortcut_key}')
            await prompt_textarea_locator.focus(timeout=5000)
            await self._check_disconnect(check_client_disconnected, 'After Input Focus')
            await asyncio.sleep(0.1)
            original_content = ''
            try:
                original_content = await prompt_textarea_locator.input_value(timeout=2000) or ''
            except Exception:
                pass
            try:
                await self.page.keyboard.press(f'{shortcut_modifier}+{shortcut_key}')
            except Exception:
                await self.page.keyboard.down(shortcut_modifier)
                await asyncio.sleep(0.05)
                await self.page.keyboard.press(shortcut_key)
                await asyncio.sleep(0.05)
                await self.page.keyboard.up(shortcut_modifier)
            await self._check_disconnect(check_client_disconnected, 'After Shortcut Press')
            await asyncio.sleep(2.0)
            submission_success = False
            try:
                current_content = await prompt_textarea_locator.last.input_value(timeout=2000) or ''
                if original_content and (not current_content.strip()):
                    self.logger.info(f'[{self.req_id}] 验证方法1: 输入框已清空，快捷键提交成功')
                    submission_success = True
                if not submission_success:
                    submit_button_locator = self.page.locator(SUBMIT_BUTTON_SELECTOR)
                    try:
                        is_disabled = await submit_button_locator.is_disabled(timeout=2000)
                        if is_disabled:
                            self.logger.info(f'[{self.req_id}] 验证方法2: 提交按钮已禁用，快捷键提交成功')
                            submission_success = True
                    except Exception:
                        pass
                if not submission_success:
                    try:
                        response_container = self.page.locator(RESPONSE_CONTAINER_SELECTOR)
                        container_count = await response_container.count()
                        if container_count > 0:
                            last_container = response_container.last
                            if await last_container.is_visible(timeout=1000):
                                self.logger.info(f'[{self.req_id}] 验证方法3: 检测到响应容器，快捷键提交成功')
                                submission_success = True
                    except Exception:
                        pass
            except Exception as verify_err:
                self.logger.warning(f'[{self.req_id}] 快捷键提交验证过程出错: {verify_err}')
                submission_success = True
            if submission_success:
                self.logger.info(f'[{self.req_id}]  快捷键提交成功')
                return True
            else:
                self.logger.warning(f'[{self.req_id}]  快捷键提交验证失败')
                return False
        except Exception as shortcut_err:
            self.logger.warning(f'[{self.req_id}] 快捷键提交失败: {shortcut_err}')
            return False

    async def stop_generation(self, check_client_disconnected: Callable):
        """
        通过导航到新的聊天URL来停止当前的生成。
        这是根据用户反馈的最有效的方法。
        """
        self.logger.info(f'[{self.req_id}] 通过导航到新聊天来停止生成...')
        try:
            await self.clear_chat_history(check_client_disconnected)
            self.logger.info(f'[{self.req_id}] 成功导航到新聊天以停止生成。')
        except Exception as e:
            self.logger.error(f'[{self.req_id}] 通过导航到新聊天停止生成失败: {e}')

    async def get_response(self, check_client_disconnected: Callable) -> str:
        """获取响应内容。"""
        self.logger.info(f'[{self.req_id}] 等待并获取响应...')
        try:
            await self._check_disconnect(check_client_disconnected, '获取响应 - 开始前')
            response_container_locator = self.page.locator(RESPONSE_CONTAINER_SELECTOR).last
            response_element_locator = response_container_locator.locator(RESPONSE_TEXT_SELECTOR)
            self.logger.info(f'[{self.req_id}] 等待响应元素附加到DOM...')
            await expect_async(response_element_locator).to_be_attached(timeout=90000)
            await self._check_disconnect(check_client_disconnected, '获取响应 - 响应元素已附加')
            submit_button_locator = self.page.locator(SUBMIT_BUTTON_SELECTOR)
            edit_button_locator = self.page.locator(EDIT_MESSAGE_BUTTON_SELECTOR)
            input_field_locator = self.page.locator(PROMPT_TEXTAREA_SELECTOR)
            self.logger.info(f'[{self.req_id}] 等待响应完成...')
            await self._check_disconnect(check_client_disconnected, '获取响应 - 开始等待完成前')
            completion_detected = await _wait_for_response_completion(self.page, input_field_locator, submit_button_locator, edit_button_locator, self.req_id, check_client_disconnected, None)
            await self._check_disconnect(check_client_disconnected, '获取响应 - 完成检测后')
            if not completion_detected:
                self.logger.warning(f'[{self.req_id}] 响应完成检测失败，尝试获取当前内容')
            else:
                self.logger.info(f'[{self.req_id}]  响应完成检测成功')
            await self._check_disconnect(check_client_disconnected, '获取响应 - 获取最终内容前')
            final_content = await _get_final_response_content(self.page, self.req_id, check_client_disconnected)
            await self._check_disconnect(check_client_disconnected, '获取响应 - 获取最终内容后')
            if not final_content or not final_content.strip():
                self.logger.warning(f'[{self.req_id}]  获取到的响应内容为空')
                await save_error_snapshot(f'empty_response_{self.req_id}')
                return ''
            self.logger.info(f'[{self.req_id}]  成功获取响应内容 ({len(final_content)} chars)')
            return final_content
        except ClientDisconnectedError:
            self.logger.info(f'[{self.req_id}]  获取响应过程中客户端断开连接')
            raise
        except Exception as e:
            self.logger.error(f'[{self.req_id}]  获取响应时出错: {e}')
            if not isinstance(e, ClientDisconnectedError):
                await save_error_snapshot(f'get_response_error_{self.req_id}')
            raise