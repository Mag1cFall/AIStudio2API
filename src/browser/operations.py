import asyncio
import time
import json
import os
import re
import logging
from typing import Optional, Any, List, Dict, Callable, Set
from playwright.async_api import Page as AsyncPage, Locator, Error as PlaywrightAsyncError
from config import *
from models import ClientDisconnectedError, ElementClickError
logger = logging.getLogger('AIStudioProxyServer')

async def get_model_name_from_page_parallel(page: AsyncPage, selectors: list, timeout: int = 2000, req_id: str = 'unknown', expected_model_name: str = None) -> Optional[str]:
    if not selectors:
        return None

    if expected_model_name:
        logger.info(f"[{req_id}] ⚡ 并行检测模型，期望: '{expected_model_name}'")

    async def check_selector(selector):
        try:
            text = await page.locator(selector).first.inner_text(timeout=timeout)
            if text and text.strip():
                return (selector, text.strip())
        except Exception:
            pass
        return None

    tasks = [asyncio.create_task(check_selector(sel)) for sel in selectors]
    
    first_valid_result = None
    
    try:
        for coro in asyncio.as_completed(tasks):
            result = await coro
            if result:
                selector, text = result
                
                if expected_model_name:
                    if text.lower() == expected_model_name.lower():
                        logger.info(f"[{req_id}] ✅ 并行匹配成功: '{text}'")
                        for t in tasks:
                            if not t.done(): t.cancel()
                        return text
                    else:
                        if first_valid_result is None:
                            first_valid_result = text
                            logger.info(f"[{req_id}] ⚠️ 找到但不匹配: '{text}' (期望 '{expected_model_name}')")
                else:
                    logger.info(f"[{req_id}] ✅ 发现模型: '{text}'")
                    for t in tasks:
                        if not t.done(): t.cancel()
                    return text

        if first_valid_result:
             logger.info(f"[{req_id}] 🏁 并行检测结束，返回: '{first_valid_result}'")
             return first_valid_result
             
    except Exception as e:
        logger.error(f"[{req_id}] ❌ 并行检测异常: {e}")
    
    for t in tasks:
        if not t.done(): t.cancel()
            
    return None

async def get_raw_text_content(response_element: Locator, previous_text: str, req_id: str) -> str:
    raw_text = previous_text
    try:
        await response_element.wait_for(state='attached', timeout=1000)
        pre_element = response_element.locator('pre').last
        pre_found_and_visible = False
        try:
            await pre_element.wait_for(state='visible', timeout=250)
            pre_found_and_visible = True
        except PlaywrightAsyncError:
            pass
        if pre_found_and_visible:
            try:
                raw_text = await pre_element.inner_text(timeout=500)
            except PlaywrightAsyncError as pre_err:
                if DEBUG_LOGS_ENABLED:
                    logger.debug(f'[{req_id}] (获取原始文本) 获取 pre 元素内部文本失败: {pre_err}')
        else:
            try:
                raw_text = await response_element.inner_text(timeout=500)
            except PlaywrightAsyncError as e_parent:
                if DEBUG_LOGS_ENABLED:
                    logger.debug(f'[{req_id}] (获取原始文本) 获取响应元素内部文本失败: {e_parent}')
    except PlaywrightAsyncError as e_parent:
        if DEBUG_LOGS_ENABLED:
            logger.debug(f'[{req_id}] (获取原始文本) 响应元素未准备好: {e_parent}')
    except Exception as e_unexpected:
        logger.warning(f'[{req_id}] (获取原始文本) 意外错误: {e_unexpected}')
    if raw_text != previous_text:
        if DEBUG_LOGS_ENABLED:
            preview = raw_text[:100].replace('\n', '\\n')
            logger.debug(f"[{req_id}] (获取原始文本) 文本已更新，长度: {len(raw_text)}，预览: '{preview}...'")
    return raw_text

def _parse_userscript_models(script_content: str):
    try:
        version_pattern = 'const\\s+SCRIPT_VERSION\\s*=\\s*[\\\'"]([^\\\'"]+)[\\\'"]'
        version_match = re.search(version_pattern, script_content)
        script_version = version_match.group(1) if version_match else 'v1.6'
        models_array_pattern = 'const\\s+MODELS_TO_INJECT\\s*=\\s*(\\[.*?\\]);'
        models_match = re.search(models_array_pattern, script_content, re.DOTALL)
        if not models_match:
            logger.warning('未找到 MODELS_TO_INJECT 数组')
            return []
        models_js_code = models_match.group(1)
        models_js_code = models_js_code.replace('${SCRIPT_VERSION}', script_version)
        models_js_code = re.sub('//.*?$', '', models_js_code, flags=re.MULTILINE)
        models_js_code = re.sub(',\\s*([}\\]])', '\\1', models_js_code)
        models_js_code = re.sub("(\\w+):\\s*'([^']*)'", '"\\1": "\\2"', models_js_code)
        models_js_code = re.sub('(\\w+):\\s*`([^`]*)`', '"\\1": "\\2"', models_js_code)
        models_js_code = re.sub('(\\w+):', '"\\1":', models_js_code)
        import json
        models_data = json.loads(models_js_code)
        models = []
        for model_obj in models_data:
            if isinstance(model_obj, dict) and 'name' in model_obj:
                models.append({'name': model_obj.get('name', ''), 'displayName': model_obj.get('displayName', ''), 'description': model_obj.get('description', '')})
        logger.info(f'成功解析 {len(models)} 个模型从油猴脚本')
        return models
    except Exception as e:
        logger.error(f'解析油猴脚本模型列表失败: {e}')
        return []

def _get_injected_models():
    try:
        enable_injection = os.environ.get('ENABLE_SCRIPT_INJECTION', 'true').lower() in ('true', '1', 'yes')
        if not enable_injection:
            return []
        script_path = os.environ.get('USERSCRIPT_PATH', 'browser/more_models.js')
        if not os.path.exists(script_path):
            return []
        with open(script_path, 'r', encoding='utf-8') as f:
            script_content = f.read()
        models = _parse_userscript_models(script_content)
        if not models:
            return []
        injected_models = []
        for model in models:
            model_name = model.get('name', '')
            if not model_name:
                continue
            if model_name.startswith('models/'):
                simple_id = model_name[7:]
            else:
                simple_id = model_name
            display_name = model.get('displayName', model.get('display_name', simple_id))
            description = model.get('description', f'Injected model: {simple_id}')
            model_entry = {'id': simple_id, 'object': 'model', 'created': int(time.time()), 'owned_by': 'ai_studio_injected', 'display_name': display_name, 'description': description, 'raw_model_path': model_name, 'default_temperature': 1.0, 'default_max_output_tokens': 65536, 'supported_max_output_tokens': 65536, 'default_top_p': 0.95, 'injected': True}
            injected_models.append(model_entry)
        return injected_models
    except Exception as e:
        return []

async def _handle_model_list_response(response: Any):
    import server
    global_model_list_raw_json = getattr(server, 'global_model_list_raw_json', None)
    parsed_model_list = getattr(server, 'parsed_model_list', [])
    model_list_fetch_event = getattr(server, 'model_list_fetch_event', None)
    excluded_model_ids = getattr(server, 'excluded_model_ids', set())
    if MODELS_ENDPOINT_URL_CONTAINS in response.url and response.ok:
        launch_mode = os.environ.get('LAUNCH_MODE', 'debug')
        is_in_login_flow = launch_mode in ['debug'] and (not getattr(server, 'is_page_ready', False))
        if is_in_login_flow:
            pass
        else:
            logger.info(f'捕获到潜在的模型列表响应来自: {response.url} (状态: {response.status})')
        try:
            data = await response.json()
            models_array_container = None
            if isinstance(data, list) and data:
                if isinstance(data[0], list) and data[0] and isinstance(data[0][0], list):
                    if not is_in_login_flow:
                        logger.info('检测到三层列表结构 data[0][0] is list. models_array_container 设置为 data[0]。')
                    models_array_container = data[0]
                elif isinstance(data[0], list) and data[0] and isinstance(data[0][0], str):
                    if not is_in_login_flow:
                        logger.info('检测到两层列表结构 data[0][0] is str. models_array_container 设置为 data。')
                    models_array_container = data
                elif isinstance(data[0], dict):
                    if not is_in_login_flow:
                        logger.info('检测到根列表，元素为字典。直接使用 data 作为 models_array_container。')
                    models_array_container = data
                else:
                    logger.warning(f"未知的列表嵌套结构。data[0] 类型: {(type(data[0]) if data else 'N/A')}。data[0] 预览: {(str(data[0])[:200] if data else 'N/A')}")
            elif isinstance(data, dict):
                if 'data' in data and isinstance(data['data'], list):
                    models_array_container = data['data']
                elif 'models' in data and isinstance(data['models'], list):
                    models_array_container = data['models']
                else:
                    for key, value in data.items():
                        if isinstance(value, list) and len(value) > 0 and isinstance(value[0], (dict, list)):
                            models_array_container = value
                            logger.info(f"模型列表数据在 '{key}' 键下通过启发式搜索找到。")
                            break
                    if models_array_container is None:
                        logger.warning('在字典响应中未能自动定位模型列表数组。')
                        if model_list_fetch_event and (not model_list_fetch_event.is_set()):
                            model_list_fetch_event.set()
                        return
            else:
                logger.warning(f'接收到的模型列表数据既不是列表也不是字典: {type(data)}')
                if model_list_fetch_event and (not model_list_fetch_event.is_set()):
                    model_list_fetch_event.set()
                return
            if models_array_container is not None:
                new_parsed_list = []
                for entry_in_container in models_array_container:
                    model_fields_list = None
                    if isinstance(entry_in_container, dict):
                        potential_id = entry_in_container.get('id', entry_in_container.get('model_id', entry_in_container.get('modelId')))
                        if potential_id:
                            model_fields_list = entry_in_container
                        else:
                            model_fields_list = list(entry_in_container.values())
                    elif isinstance(entry_in_container, list):
                        model_fields_list = entry_in_container
                    else:
                        logger.debug(f'Skipping entry of unknown type: {type(entry_in_container)}')
                        continue
                    if not model_fields_list:
                        logger.debug('Skipping entry because model_fields_list is empty or None.')
                        continue
                    model_id_path_str = None
                    display_name_candidate = ''
                    description_candidate = 'N/A'
                    default_max_output_tokens_val = None
                    default_top_p_val = None
                    default_temperature_val = 1.0
                    supported_max_output_tokens_val = None
                    current_model_id_for_log = 'UnknownModelYet'
                    try:
                        if isinstance(model_fields_list, list):
                            if not (len(model_fields_list) > 0 and isinstance(model_fields_list[0], (str, int, float))):
                                logger.debug(f'Skipping list-based model_fields due to invalid first element: {str(model_fields_list)[:100]}')
                                continue
                            model_id_path_str = str(model_fields_list[0])
                            current_model_id_for_log = model_id_path_str.split('/')[-1] if model_id_path_str and '/' in model_id_path_str else model_id_path_str
                            display_name_candidate = str(model_fields_list[3]) if len(model_fields_list) > 3 else ''
                            description_candidate = str(model_fields_list[4]) if len(model_fields_list) > 4 else 'N/A'
                            if len(model_fields_list) > 6 and model_fields_list[6] is not None:
                                try:
                                    val_int = int(model_fields_list[6])
                                    default_max_output_tokens_val = val_int
                                    supported_max_output_tokens_val = val_int
                                except (ValueError, TypeError):
                                    logger.warning(f"模型 {current_model_id_for_log}: 无法将列表索引6的值 '{model_fields_list[6]}' 解析为 max_output_tokens。")
                            if len(model_fields_list) > 9 and model_fields_list[9] is not None:
                                try:
                                    raw_top_p = float(model_fields_list[9])
                                    if not 0.0 <= raw_top_p <= 1.0:
                                        logger.warning(f'模型 {current_model_id_for_log}: 原始 top_p值 {raw_top_p} (来自列表索引9) 超出 [0,1] 范围，将裁剪。')
                                        default_top_p_val = max(0.0, min(1.0, raw_top_p))
                                    else:
                                        default_top_p_val = raw_top_p
                                except (ValueError, TypeError):
                                    logger.warning(f"模型 {current_model_id_for_log}: 无法将列表索引9的值 '{model_fields_list[9]}' 解析为 top_p。")
                        elif isinstance(model_fields_list, dict):
                            model_id_path_str = str(model_fields_list.get('id', model_fields_list.get('model_id', model_fields_list.get('modelId'))))
                            current_model_id_for_log = model_id_path_str.split('/')[-1] if model_id_path_str and '/' in model_id_path_str else model_id_path_str
                            display_name_candidate = str(model_fields_list.get('displayName', model_fields_list.get('display_name', model_fields_list.get('name', ''))))
                            description_candidate = str(model_fields_list.get('description', 'N/A'))
                            mot_parsed = model_fields_list.get('maxOutputTokens', model_fields_list.get('defaultMaxOutputTokens', model_fields_list.get('outputTokenLimit')))
                            if mot_parsed is not None:
                                try:
                                    val_int = int(mot_parsed)
                                    default_max_output_tokens_val = val_int
                                    supported_max_output_tokens_val = val_int
                                except (ValueError, TypeError):
                                    logger.warning(f"模型 {current_model_id_for_log}: 无法将字典值 '{mot_parsed}' 解析为 max_output_tokens。")
                            top_p_parsed = model_fields_list.get('topP', model_fields_list.get('defaultTopP'))
                            if top_p_parsed is not None:
                                try:
                                    raw_top_p = float(top_p_parsed)
                                    if not 0.0 <= raw_top_p <= 1.0:
                                        logger.warning(f'模型 {current_model_id_for_log}: 原始 top_p值 {raw_top_p} (来自字典) 超出 [0,1] 范围，将裁剪。')
                                        default_top_p_val = max(0.0, min(1.0, raw_top_p))
                                    else:
                                        default_top_p_val = raw_top_p
                                except (ValueError, TypeError):
                                    logger.warning(f"模型 {current_model_id_for_log}: 无法将字典值 '{top_p_parsed}' 解析为 top_p。")
                            temp_parsed = model_fields_list.get('temperature', model_fields_list.get('defaultTemperature'))
                            if temp_parsed is not None:
                                try:
                                    default_temperature_val = float(temp_parsed)
                                except (ValueError, TypeError):
                                    logger.warning(f"模型 {current_model_id_for_log}: 无法将字典值 '{temp_parsed}' 解析为 temperature。")
                        else:
                            logger.debug(f'Skipping entry because model_fields_list is not list or dict: {type(model_fields_list)}')
                            continue
                    except Exception as e_parse_fields:
                        logger.error(f'解析模型字段时出错 for entry {str(entry_in_container)[:100]}: {e_parse_fields}')
                        continue
                    if model_id_path_str and model_id_path_str.lower() != 'none':
                        simple_model_id_str = model_id_path_str.split('/')[-1] if '/' in model_id_path_str else model_id_path_str
                        if simple_model_id_str in excluded_model_ids:
                            if not is_in_login_flow:
                                logger.info(f"模型 '{simple_model_id_str}' 在排除列表 excluded_model_ids 中，已跳过。")
                            continue
                        final_display_name_str = display_name_candidate if display_name_candidate else simple_model_id_str.replace('-', ' ').title()
                        model_entry_dict = {'id': simple_model_id_str, 'object': 'model', 'created': int(time.time()), 'owned_by': 'ai_studio', 'display_name': final_display_name_str, 'description': description_candidate, 'raw_model_path': model_id_path_str, 'default_temperature': default_temperature_val, 'default_max_output_tokens': default_max_output_tokens_val, 'supported_max_output_tokens': supported_max_output_tokens_val, 'default_top_p': default_top_p_val}
                        new_parsed_list.append(model_entry_dict)
                    else:
                        logger.debug(f'Skipping entry due to invalid model_id_path: {model_id_path_str} from entry {str(entry_in_container)[:100]}')
                if new_parsed_list:
                    has_network_injected_models = False
                    if models_array_container:
                        for entry_in_container in models_array_container:
                            if isinstance(entry_in_container, list) and len(entry_in_container) > 10:
                                if '__NETWORK_INJECTED__' in entry_in_container:
                                    has_network_injected_models = True
                                    break
                    if has_network_injected_models and (not is_in_login_flow):
                        logger.info('检测到网络拦截已注入模型')
                    server.parsed_model_list = sorted(new_parsed_list, key=lambda m: m.get('display_name', '').lower())
                    server.global_model_list_raw_json = json.dumps({'data': server.parsed_model_list, 'object': 'list'})
                    if DEBUG_LOGS_ENABLED:
                        log_output = f'成功解析和更新模型列表。总共解析模型数: {len(server.parsed_model_list)}.\n'
                        for i, item in enumerate(server.parsed_model_list[:min(3, len(server.parsed_model_list))]):
                            log_output += f"  Model {i + 1}: ID={item.get('id')}, Name={item.get('display_name')}, Temp={item.get('default_temperature')}, MaxTokDef={item.get('default_max_output_tokens')}, MaxTokSup={item.get('supported_max_output_tokens')}, TopP={item.get('default_top_p')}\n"
                        logger.info(log_output)
                    if model_list_fetch_event and (not model_list_fetch_event.is_set()):
                        model_list_fetch_event.set()
                elif not server.parsed_model_list:
                    logger.warning('解析后模型列表仍然为空。')
                    if model_list_fetch_event and (not model_list_fetch_event.is_set()):
                        model_list_fetch_event.set()
            else:
                logger.warning('models_array_container 为 None，无法解析模型列表。')
                if model_list_fetch_event and (not model_list_fetch_event.is_set()):
                    model_list_fetch_event.set()
        except json.JSONDecodeError as json_err:
            logger.error(f'解析模型列表JSON失败: {json_err}. 响应 (前500字): {await response.text()[:500]}')
        except Exception as e_handle_list_resp:
            logger.exception(f'处理模型列表响应时发生未知错误: {e_handle_list_resp}')
        finally:
            if model_list_fetch_event and (not model_list_fetch_event.is_set()):
                logger.info('处理模型列表响应结束，强制设置 model_list_fetch_event。')
                model_list_fetch_event.set()

async def detect_and_extract_page_error(page: AsyncPage, req_id: str) -> Optional[str]:
    error_toast_locator = page.locator(ERROR_TOAST_SELECTOR).last
    try:
        await error_toast_locator.wait_for(state='visible', timeout=500)
        message_locator = error_toast_locator.locator('span.content-text')
        error_message = await message_locator.text_content(timeout=500)
        if error_message:
            logger.error(f'[{req_id}]    检测到并提取错误消息: {error_message}')
            return error_message.strip()
        else:
            logger.warning(f'[{req_id}]    检测到错误提示框，但无法提取消息。')
            return '检测到错误提示框，但无法提取特定消息。'
    except PlaywrightAsyncError:
        return None
    except Exception as e:
        logger.warning(f'[{req_id}]    检查页面错误时出错: {e}')
        return None

async def detect_rate_limit(page: AsyncPage, req_id: str) -> tuple:
    try:
        locator = page.locator(RATE_LIMIT_CALLOUT_SELECTOR).last
        if await locator.count() > 0:
            text = await locator.text_content(timeout=500)
            if text:
                for kw in RATE_LIMIT_KEYWORDS:
                    if kw in text.lower():
                        logger.warning(f'[{req_id}] ⚠️ Rate Limit detected: {text.strip()}')
                        return True, text.strip()
    except:
        pass
    return False, ""

async def save_error_snapshot(error_name: str='error'):
    import server
    name_parts = error_name.split('_')
    req_id = name_parts[-1] if len(name_parts) > 1 and len(name_parts[-1]) == 7 else None
    base_error_name = error_name if not req_id else '_'.join(name_parts[:-1])
    log_prefix = f'[{req_id}]' if req_id else '[无请求ID]'
    page_to_snapshot = server.page_instance
    if not server.browser_instance or not server.browser_instance.is_connected() or (not page_to_snapshot) or page_to_snapshot.is_closed():
        logger.warning(f'{log_prefix} 无法保存快照 ({base_error_name})，浏览器/页面不可用。')
        return
    logger.info(f'{log_prefix} 📸 保存错误快照 ({base_error_name})...')
    timestamp = int(time.time() * 1000)
    script_dir = os.path.dirname(os.path.abspath(__file__))
    project_root = os.path.dirname(os.path.dirname(script_dir))
    error_dir = os.path.join(project_root, 'data', 'errors_py')
    try:
        os.makedirs(error_dir, exist_ok=True)
        filename_suffix = f'{req_id}_{timestamp}' if req_id else f'{timestamp}'
        filename_base = f'{base_error_name}_{filename_suffix}'
        screenshot_path = os.path.join(error_dir, f'{filename_base}.png')
        html_path = os.path.join(error_dir, f'{filename_base}.html')
        try:
            await page_to_snapshot.screenshot(path=screenshot_path, full_page=True, timeout=15000)
            logger.info(f'{log_prefix} 📸 快照: {os.path.basename(screenshot_path)}')
        except Exception as ss_err:
            logger.error(f'{log_prefix} ❌ 截图失败: {ss_err}')
        try:
            content = await page_to_snapshot.content()
            f = None
            try:
                f = open(html_path, 'w', encoding='utf-8')
                f.write(content)
            except Exception as write_err:
                logger.error(f'{log_prefix} ❌ HTML保存失败: {write_err}')
            finally:
                if f:
                    try:
                        f.close()
                        logger.debug(f'{log_prefix}   HTML 文件已正确关闭')
                    except Exception as close_err:
                        logger.error(f'{log_prefix}   关闭 HTML 文件时出错: {close_err}')
        except Exception as html_err:
            logger.error(f'{log_prefix}   获取页面内容失败 ({base_error_name}): {html_err}')
    except Exception as dir_err:
        logger.error(f'{log_prefix}   创建错误目录或保存快照时发生其他错误 ({base_error_name}): {dir_err}')

async def click_element(page: AsyncPage, locator: Locator, element_name: str, req_id: str, internal_timeout: int=2000) -> bool:
    last_error = None
    fast_click_timeout = 500
    try:
        await locator.wait_for(state='visible', timeout=internal_timeout)
        logger.info(f"[{req_id}] '{element_name}' 元素状态验证通过")
    except Exception as e:
        logger.warning(f"[{req_id}] '{element_name}' 元素在点击前验证失败 (不可见): {e}")
        raise ElementClickError(f"Element '{element_name}' not visible before click attempt.") from e
    try:
        await locator.click(timeout=fast_click_timeout)
        return True
    except Exception as e:
        last_error = e
    await asyncio.sleep(0.1)
    try:
        logger.info(f"[{req_id}] 🖱️ 尝试强制点击 '{element_name}'")
        await locator.click(timeout=fast_click_timeout, force=True)
        logger.info(f"[{req_id}] ✅ '{element_name}' 强制点击成功")
        return True
    except Exception as e:
        last_error = e
    await asyncio.sleep(0.1)
    try:
        logger.info(f"[{req_id}] 🖱️ 尝试JS点击 '{element_name}'")
        await locator.evaluate('element => element.click()')
        logger.info(f"[{req_id}] ✅ '{element_name}' JS点击成功")
        return True
    except Exception as e:
        last_error = e
    logger.error(f"[{req_id}] ❌ 所有点击 '{element_name}' 的尝试都失败了。")
    raise ElementClickError(f"All click attempts for '{element_name}' failed.") from last_error

async def get_response_via_edit_button(page: AsyncPage, req_id: str, check_client_disconnected: Callable) -> Optional[str]:
    logger.info(f'[{req_id}] (Helper) 尝试通过编辑按钮获取响应...')
    last_message_container = page.locator('ms-chat-turn').last
    edit_button = last_message_container.locator(EDIT_MESSAGE_BUTTON_SELECTOR)
    finish_edit_button = last_message_container.locator(FINISH_EDIT_BUTTON_SELECTOR)
    autosize_textarea_locator = last_message_container.locator('ms-autosize-textarea')
    actual_textarea_locator = autosize_textarea_locator.locator('textarea')
    try:
        logger.info(f"[{req_id}]   - 尝试悬停最后一条消息以显示 'Edit' 按钮...")
        try:
            await last_message_container.hover(timeout=CLICK_TIMEOUT_MS / 2)
            await asyncio.sleep(0.3)
            check_client_disconnected('编辑响应 - 悬停后: ')
        except Exception as hover_err:
            logger.warning(f'[{req_id}]   - (get_response_via_edit_button) 悬停最后一条消息失败 (忽略): {type(hover_err).__name__}')
        logger.info(f"[{req_id}]   - 定位并点击 'Edit' 按钮...")
        try:
            from playwright.async_api import expect as expect_async
            await expect_async(edit_button).to_be_visible(timeout=CLICK_TIMEOUT_MS)
            check_client_disconnected("编辑响应 - 'Edit' 按钮可见后: ")
            await edit_button.click(timeout=CLICK_TIMEOUT_MS)
            logger.info(f"[{req_id}]   - 'Edit' 按钮已点击。")
        except Exception as edit_btn_err:
            logger.error(f"[{req_id}]   - 'Edit' 按钮不可见或点击失败: {edit_btn_err}")
            await save_error_snapshot(f'edit_response_edit_button_failed_{req_id}')
            return None
        check_client_disconnected("编辑响应 - 点击 'Edit' 按钮后: ")
        await asyncio.sleep(0.3)
        check_client_disconnected("编辑响应 - 点击 'Edit' 按钮后延时后: ")
        logger.info(f'[{req_id}]   - 从文本区域获取内容...')
        response_content = None
        textarea_failed = False
        try:
            await expect_async(autosize_textarea_locator).to_be_visible(timeout=CLICK_TIMEOUT_MS)
            check_client_disconnected('编辑响应 - autosize-textarea 可见后: ')
            try:
                data_value_content = await autosize_textarea_locator.get_attribute('data-value')
                check_client_disconnected('编辑响应 - get_attribute data-value 后: ')
                if data_value_content is not None:
                    response_content = str(data_value_content)
                    logger.info(f'[{req_id}]   - 从 data-value 获取内容成功。')
            except Exception as data_val_err:
                logger.warning(f'[{req_id}]   - 获取 data-value 失败: {data_val_err}')
                check_client_disconnected('编辑响应 - get_attribute data-value 错误后: ')
            if response_content is None:
                logger.info(f'[{req_id}]   - data-value 获取失败或为None，尝试从内部 textarea 获取 input_value...')
                try:
                    await expect_async(actual_textarea_locator).to_be_visible(timeout=CLICK_TIMEOUT_MS / 2)
                    input_val_content = await actual_textarea_locator.input_value(timeout=CLICK_TIMEOUT_MS / 2)
                    check_client_disconnected('编辑响应 - input_value 后: ')
                    if input_val_content is not None:
                        response_content = str(input_val_content)
                        logger.info(f'[{req_id}]   - 从 input_value 获取内容成功。')
                except Exception as input_val_err:
                    logger.warning(f'[{req_id}]   - 获取 input_value 也失败: {input_val_err}')
                    check_client_disconnected('编辑响应 - input_value 错误后: ')
            if response_content is not None:
                response_content = response_content.strip()
                content_preview = response_content[:100].replace('\\n', '\\\\n')
                logger.info(f"[{req_id}]   - ✅ 最终获取内容 (长度={len(response_content)}): '{content_preview}...'")
            else:
                logger.warning(f'[{req_id}]   - 所有方法 (data-value, input_value) 内容获取均失败或返回 None。')
                textarea_failed = True
        except Exception as textarea_err:
            logger.error(f'[{req_id}]   - 定位或处理文本区域时失败: {textarea_err}')
            textarea_failed = True
            response_content = None
            check_client_disconnected('编辑响应 - 获取文本区域错误后: ')
        if not textarea_failed:
            logger.info(f"[{req_id}]   - 定位并点击 'Stop editing' 按钮...")
            try:
                await expect_async(finish_edit_button).to_be_visible(timeout=CLICK_TIMEOUT_MS)
                check_client_disconnected("编辑响应 - 'Stop editing' 按钮可见后: ")
                await finish_edit_button.click(timeout=CLICK_TIMEOUT_MS)
                logger.info(f"[{req_id}]   - 'Stop editing' 按钮已点击。")
            except Exception as finish_btn_err:
                logger.warning(f"[{req_id}]   - 'Stop editing' 按钮不可见或点击失败: {finish_btn_err}")
                await save_error_snapshot(f'edit_response_finish_button_failed_{req_id}')
            check_client_disconnected("编辑响应 - 点击 'Stop editing' 后: ")
            await asyncio.sleep(0.2)
            check_client_disconnected("编辑响应 - 点击 'Stop editing' 后延时后: ")
        else:
            logger.info(f"[{req_id}]   - 跳过点击 'Stop editing' 按钮，因为文本区域读取失败。")
        return response_content
    except ClientDisconnectedError:
        logger.info(f'[{req_id}] (Helper Edit) 客户端断开连接。')
        raise
    except Exception as e:
        logger.exception(f'[{req_id}] 通过编辑按钮获取响应过程中发生意外错误')
        await save_error_snapshot(f'edit_response_unexpected_error_{req_id}')
        return None

async def get_response_via_copy_button(page: AsyncPage, req_id: str, check_client_disconnected: Callable) -> Optional[str]:
    logger.info(f'[{req_id}] (Helper) 尝试通过复制按钮获取响应...')
    last_message_container = page.locator('ms-chat-turn').last
    more_options_button = last_message_container.locator(MORE_OPTIONS_BUTTON_SELECTOR)
    copy_markdown_button = page.locator(COPY_MARKDOWN_BUTTON_SELECTOR)
    try:
        logger.info(f'[{req_id}]   - 尝试悬停最后一条消息以显示选项...')
        await last_message_container.hover(timeout=CLICK_TIMEOUT_MS)
        check_client_disconnected('复制响应 - 悬停后: ')
        await asyncio.sleep(0.5)
        check_client_disconnected('复制响应 - 悬停后延时后: ')
        logger.info(f'[{req_id}]   - 已悬停。')
        logger.info(f"[{req_id}]   - 定位并点击 '更多选项' 按钮...")
        try:
            from playwright.async_api import expect as expect_async
            await expect_async(more_options_button).to_be_visible(timeout=CLICK_TIMEOUT_MS)
            check_client_disconnected('复制响应 - 更多选项按钮可见后: ')
            await more_options_button.click(timeout=CLICK_TIMEOUT_MS)
            logger.info(f"[{req_id}]   - '更多选项' 已点击 (通过 get_by_label)。")
        except Exception as more_opts_err:
            logger.error(f"[{req_id}]   - '更多选项' 按钮 (通过 get_by_label) 不可见或点击失败: {more_opts_err}")
            await save_error_snapshot(f'copy_response_more_options_failed_{req_id}')
            return None
        check_client_disconnected('复制响应 - 点击更多选项后: ')
        await asyncio.sleep(0.5)
        check_client_disconnected('复制响应 - 点击更多选项后延时后: ')
        logger.info(f"[{req_id}]   - 定位并点击 '复制 Markdown' 按钮...")
        copy_success = False
        try:
            await expect_async(copy_markdown_button).to_be_visible(timeout=CLICK_TIMEOUT_MS)
            check_client_disconnected('复制响应 - 复制按钮可见后: ')
            await copy_markdown_button.click(timeout=CLICK_TIMEOUT_MS, force=True)
            copy_success = True
            logger.info(f"[{req_id}]   - 已点击 '复制 Markdown' (通过 get_by_role)。")
        except Exception as copy_err:
            logger.error(f"[{req_id}]   - '复制 Markdown' 按钮 (通过 get_by_role) 点击失败: {copy_err}")
            await save_error_snapshot(f'copy_response_copy_button_failed_{req_id}')
            return None
        if not copy_success:
            logger.error(f"[{req_id}]   - 未能点击 '复制 Markdown' 按钮。")
            return None
        check_client_disconnected('复制响应 - 点击复制按钮后: ')
        await asyncio.sleep(0.5)
        check_client_disconnected('复制响应 - 点击复制按钮后延时后: ')
        logger.info(f'[{req_id}]   - 正在读取剪贴板内容...')
        try:
            clipboard_content = await page.evaluate('navigator.clipboard.readText()')
            check_client_disconnected('复制响应 - 读取剪贴板后: ')
            if clipboard_content:
                content_preview = clipboard_content[:100].replace('\n', '\\\\n')
                logger.info(f"[{req_id}]   - ✅ 成功获取剪贴板内容 (长度={len(clipboard_content)}): '{content_preview}...'")
                return clipboard_content
            else:
                logger.error(f'[{req_id}]   - 剪贴板内容为空。')
                return None
        except Exception as clipboard_err:
            if 'clipboard-read' in str(clipboard_err):
                logger.error(f'[{req_id}]   - 读取剪贴板失败: 可能是权限问题。错误: {clipboard_err}')
            else:
                logger.error(f'[{req_id}]   - 读取剪贴板失败: {clipboard_err}')
            await save_error_snapshot(f'copy_response_clipboard_read_failed_{req_id}')
            return None
    except ClientDisconnectedError:
        logger.info(f'[{req_id}] (Helper Copy) 客户端断开连接。')
        raise
    except Exception as e:
        logger.exception(f'[{req_id}] 复制响应过程中发生意外错误')
        await save_error_snapshot(f'copy_response_unexpected_error_{req_id}')
        return None

async def _wait_for_response_completion(page: AsyncPage, prompt_textarea_locator: Locator, submit_button_locator: Locator, edit_button_locator: Locator, req_id: str, check_client_disconnected_func: Callable, current_chat_id: Optional[str], timeout_ms=RESPONSE_COMPLETION_TIMEOUT, initial_wait_ms=INITIAL_WAIT_MS_BEFORE_POLLING) -> bool:
    from playwright.async_api import TimeoutError
    logger.info(f'[{req_id}] (WaitV3) 开始等待响应完成... (超时: {timeout_ms}ms)')
    await asyncio.sleep(initial_wait_ms / 1000)
    start_time = time.time()
    wait_timeout_ms_short = 3000
    consecutive_empty_input_submit_disabled_count = 0
    while True:
        try:
            check_client_disconnected_func('等待响应完成 - 循环开始')
        except ClientDisconnectedError:
            logger.info(f'[{req_id}] (WaitV3) 客户端断开连接，中止等待。')
            return False
        current_time_elapsed_ms = (time.time() - start_time) * 1000
        if current_time_elapsed_ms > timeout_ms:
            logger.error(f'[{req_id}] (WaitV3) 等待响应完成超时 ({timeout_ms}ms)。')
            await save_error_snapshot(f'wait_completion_v3_overall_timeout_{req_id}')
            return False
        try:
            check_client_disconnected_func('等待响应完成 - 超时检查后')
        except ClientDisconnectedError:
            return False
        is_input_empty = await prompt_textarea_locator.input_value() == ''
        is_submit_disabled = False
        try:
            is_submit_disabled = await submit_button_locator.is_disabled(timeout=wait_timeout_ms_short)
        except TimeoutError:
            logger.warning(f'[{req_id}] (WaitV3) 检查提交按钮是否禁用超时。为本次检查假定其未禁用。')
        try:
            check_client_disconnected_func('等待响应完成 - 按钮状态检查后')
        except ClientDisconnectedError:
            return False
        if is_input_empty and is_submit_disabled:
            consecutive_empty_input_submit_disabled_count += 1
            if DEBUG_LOGS_ENABLED:
                logger.debug(f'[{req_id}] (WaitV3) 主要条件满足: 输入框空，提交按钮禁用 (计数: {consecutive_empty_input_submit_disabled_count})。')
            try:
                if await edit_button_locator.is_visible(timeout=wait_timeout_ms_short):
                    logger.info(f'[{req_id}] (WaitV3) ✅ 响应完成: 输入框空，提交按钮禁用，编辑按钮可见。')
                    return True
            except TimeoutError:
                if DEBUG_LOGS_ENABLED:
                    logger.debug(f'[{req_id}] (WaitV3) 主要条件满足后，检查编辑按钮可见性超时。')
            try:
                check_client_disconnected_func('等待响应完成 - 编辑按钮检查后')
            except ClientDisconnectedError:
                return False
            if consecutive_empty_input_submit_disabled_count >= 3:
                logger.warning(f'[{req_id}] (WaitV3) 响应可能已完成 (启发式): 输入框空，提交按钮禁用，但在 {consecutive_empty_input_submit_disabled_count} 次检查后编辑按钮仍未出现。假定完成。后续若内容获取失败，可能与此有关。')
                return True
        else:
            consecutive_empty_input_submit_disabled_count = 0
            if DEBUG_LOGS_ENABLED:
                reasons = []
                if not is_input_empty:
                    reasons.append('输入框非空')
                if not is_submit_disabled:
                    reasons.append('提交按钮非禁用')
                logger.debug(f"[{req_id}] (WaitV3) 主要条件未满足 ({', '.join(reasons)}). 继续轮询...")
        await asyncio.sleep(0.5)

async def _get_final_response_content(page: AsyncPage, req_id: str, check_client_disconnected: Callable) -> Optional[str]:
    logger.info(f'[{req_id}] (Helper GetContent) 开始获取最终响应内容...')
    response_content = await get_response_via_edit_button(page, req_id, check_client_disconnected)
    if response_content is not None:
        logger.info(f'[{req_id}] (Helper GetContent) ✅ 成功通过编辑按钮获取内容。')
        return response_content
    logger.warning(f'[{req_id}] (Helper GetContent) 编辑按钮方法失败或返回空，回退到复制按钮方法...')
    response_content = await get_response_via_copy_button(page, req_id, check_client_disconnected)
    if response_content is not None:
        logger.info(f'[{req_id}] (Helper GetContent) ✅ 成功通过复制按钮获取内容。')
        return response_content
    logger.error(f'[{req_id}] (Helper GetContent) 所有获取响应内容的方法均失败。')
    await save_error_snapshot(f'get_content_all_methods_failed_{req_id}')
    return None