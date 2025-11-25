import asyncio
import json
import os
import random
import time
from typing import Optional, Tuple, Callable, AsyncGenerator
from asyncio import Event, Future
from fastapi import HTTPException, Request
from fastapi.responses import JSONResponse, StreamingResponse
from playwright.async_api import Page as AsyncPage, Locator, Error as PlaywrightAsyncError, expect as expect_async
from config import *
from models import ChatCompletionRequest, ClientDisconnectedError
from browser_utils import switch_ai_studio_model, save_error_snapshot
from .utils import validate_chat_request, prepare_combined_prompt, generate_sse_chunk, generate_sse_stop_chunk, use_stream_response, calculate_usage_stats, request_manager, calculate_stream_max_retries
from .abort_detector import AbortSignalDetector, AbortSignalHandler
from browser_utils.page_controller import PageController

async def _initialize_request_context(req_id: str, request: ChatCompletionRequest) -> dict:
    from server import logger, page_instance, is_page_ready, parsed_model_list, current_ai_studio_model_id, model_switching_lock, page_params_cache, params_cache_lock
    request_manager.register_request(req_id, {'model': request.model, 'stream': request.stream, 'message_count': len(request.messages)})
    logger.info(f'[{req_id}] 🚀 开始请求 | Model: {request.model} | Stream: {request.stream}')
    context = {'logger': logger, 'page': page_instance, 'is_page_ready': is_page_ready, 'parsed_model_list': parsed_model_list, 'current_ai_studio_model_id': current_ai_studio_model_id, 'model_switching_lock': model_switching_lock, 'page_params_cache': page_params_cache, 'params_cache_lock': params_cache_lock, 'is_streaming': request.stream, 'model_actually_switched': False, 'requested_model': request.model, 'model_id_to_use': None, 'needs_model_switching': False}
    return context

async def _analyze_model_requirements(req_id: str, context: dict, request: ChatCompletionRequest) -> dict:
    logger = context['logger']
    current_ai_studio_model_id = context['current_ai_studio_model_id']
    parsed_model_list = context['parsed_model_list']
    requested_model = request.model
    if requested_model and requested_model != MODEL_NAME:
        requested_model_id = requested_model.split('/')[-1]
        logger.info(f'[{req_id}] 请求使用模型: {requested_model_id}')
        if parsed_model_list:
            valid_model_ids = [m.get('id') for m in parsed_model_list]
            if requested_model_id not in valid_model_ids:
                raise HTTPException(status_code=400, detail=f"[{req_id}] Invalid model '{requested_model_id}'. Available models: {', '.join(valid_model_ids)}")
        context['model_id_to_use'] = requested_model_id
        if current_ai_studio_model_id != requested_model_id:
            context['needs_model_switching'] = True
            logger.info(f'[{req_id}] 需要切换模型: 当前={current_ai_studio_model_id} -> 目标={requested_model_id}')
    return context

async def _test_client_connection(req_id: str, http_request: Request) -> bool:
    from server import logger
    try:
        is_disconnected = await http_request.is_disconnected()
        if is_disconnected:
            logger.info(f'[{req_id}] 🚨 检测到客户端断开 - is_disconnected() = True')
            return False
        if hasattr(http_request, '_receive'):
            import asyncio
            try:
                receive_task = asyncio.create_task(http_request._receive())
                done, pending = await asyncio.wait([receive_task], timeout=0.05)
                if done:
                    message = receive_task.result()
                    message_type = message.get('type', 'unknown')
                    logger.info(f"[{req_id}] 🔍 收到ASGI消息: type={message_type}, body_size={len(message.get('body', b''))}, more_body={message.get('more_body', 'N/A')}")
                    if message_type == 'http.disconnect':
                        logger.info(f'[{req_id}] 🚨 Cherry Studio停止信号 - http.disconnect')
                        return False
                    if message_type in ['websocket.disconnect', 'websocket.close']:
                        logger.info(f'[{req_id}] 🚨 WebSocket断开信号 - {message_type}')
                        return False
                    if message_type == 'http.request':
                        body = message.get('body', b'')
                        more_body = message.get('more_body', True)
                        if body == b'' and (not more_body):
                            logger.info(f'[{req_id}] 🚨 空body停止信号')
                            return False
                        if body:
                            body_str = body.decode('utf-8', errors='ignore').lower()
                            if any((stop_word in body_str for stop_word in ['abort', 'cancel', 'stop'])):
                                logger.info(f'[{req_id}] 🚨 检测到停止关键词在body中: {body_str[:100]}')
                                return False
                else:
                    for task in pending:
                        task.cancel()
                        try:
                            await task
                        except asyncio.CancelledError:
                            pass
            except asyncio.TimeoutError:
                pass
            except Exception as e:
                logger.warning(f'[{req_id}] ASGI消息检测异常: {e}')
                error_msg = str(e).lower()
                if any((keyword in error_msg for keyword in ['disconnect', 'closed', 'abort', 'cancel', 'reset', 'broken'])):
                    logger.info(f'[{req_id}] 🚨 异常表示断开连接: {e}')
                    return False
        try:
            if hasattr(http_request, 'scope'):
                scope = http_request.scope
                transport = scope.get('transport')
                if transport:
                    if hasattr(transport, 'is_closing') and transport.is_closing():
                        logger.info(f'[{req_id}] 🚨 传输层正在关闭')
                        return False
                    if hasattr(transport, 'is_closed') and transport.is_closed():
                        logger.info(f'[{req_id}] 🚨 传输层已关闭')
                        return False
        except Exception:
            pass
        return True
    except Exception as e:
        logger.warning(f'[{req_id}] 连接检测总异常: {e}')
        return False

async def _setup_disconnect_monitoring(req_id: str, http_request: Request, result_future: Future, page: AsyncPage) -> Tuple[Event, asyncio.Task, Callable]:
    from server import logger
    client_disconnected_event = Event()
    page_controller = PageController(page, logger, req_id)
    logger.info(f'[{req_id}] 🚀 创建客户端断开监控任务')

    async def listen_for_disconnect():
        logger.info(f'[{req_id}] 👂 启动长连接断开监听 (Event-Driven)...')
        try:
            while not client_disconnected_event.is_set():
                # 直接等待 ASGI 消息，不再轮询
                message = await http_request.receive()
                if message['type'] == 'http.disconnect':
                    logger.warning(f'[{req_id}] 🔌 收到 http.disconnect 信号')
                    client_disconnected_event.set()
                    if not result_future.done():
                        result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 客户端关闭了请求'))
                    
                    logger.info(f'[{req_id}] 🛑 客户端断开，触发页面停止生成...')
                    try:
                        # 定义一个简易的检查函数，避免循环依赖
                        def simple_disconnect_check(stage=''): return False
                        await page_controller.stop_generation(simple_disconnect_check)
                        logger.info(f'[{req_id}] ✅ 页面停止生成命令执行成功')
                    except Exception as stop_err:
                        logger.error(f'[{req_id}] ❌ 页面停止生成失败: {stop_err}')
                    break
                # 如果收到其他类型的消息 (极少见，因为Body已被读取)，继续等待
        except asyncio.CancelledError:
            logger.info(f'[{req_id}] 📛 断开监听任务被取消')
        except Exception as e:
            # 某些情况下 receive() 可能会因为连接早已断开而抛出异常
            logger.warning(f'[{req_id}] ❌ 断开监听捕获异常 (可能连接已关闭): {e}')
            client_disconnected_event.set()
            if not result_future.done():
                result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] Client connection lost: {e}'))
    
    disconnect_check_task = asyncio.create_task(listen_for_disconnect())
    logger.info(f'[{req_id}] ✅ 监控任务已创建并启动: {disconnect_check_task}')

    def check_client_disconnected(stage: str=''):
        if request_manager.is_cancelled(req_id):
            logger.info(f"[{req_id}] 在 '{stage}' 检测到请求被用户取消。")
            raise ClientDisconnectedError(f'[{req_id}] Request cancelled by user at stage: {stage}')
        if client_disconnected_event.is_set():
            logger.info(f"[{req_id}] 在 '{stage}' 检测到客户端断开连接。")
            raise ClientDisconnectedError(f'[{req_id}] Client disconnected at stage: {stage}')
        return False
    return (client_disconnected_event, disconnect_check_task, check_client_disconnected)

async def _validate_page_status(req_id: str, context: dict, check_client_disconnected: Callable) -> None:
    page = context['page']
    is_page_ready = context['is_page_ready']
    if not page or page.is_closed() or (not is_page_ready):
        raise HTTPException(status_code=503, detail=f'[{req_id}] AI Studio 页面丢失或未就绪。', headers={'Retry-After': '30'})
    check_client_disconnected('Initial Page Check')

async def _handle_model_switching(req_id: str, context: dict, check_client_disconnected: Callable) -> dict:
    if not context['needs_model_switching']:
        return context
    logger = context['logger']
    page = context['page']
    model_switching_lock = context['model_switching_lock']
    model_id_to_use = context['model_id_to_use']
    import server
    async with model_switching_lock:
        if server.current_ai_studio_model_id != model_id_to_use:
            logger.info(f'[{req_id}] 🔄 切换模型: {server.current_ai_studio_model_id} -> {model_id_to_use}')
            switch_success = await switch_ai_studio_model(page, model_id_to_use, req_id)
            if switch_success:
                server.current_ai_studio_model_id = model_id_to_use
                context['model_actually_switched'] = True
                context['current_ai_studio_model_id'] = model_id_to_use
                logger.info(f'[{req_id}] ✅ 模型切换成功')
            else:
                await _handle_model_switch_failure(req_id, page, model_id_to_use, server.current_ai_studio_model_id, logger)
    return context

async def _handle_model_switch_failure(req_id: str, page: AsyncPage, model_id_to_use: str, model_before_switch: str, logger) -> None:
    import server
    logger.warning(f'[{req_id}] ❌ 模型切换至 {model_id_to_use} 失败。')
    server.current_ai_studio_model_id = model_before_switch
    raise HTTPException(status_code=422, detail=f"[{req_id}] 未能切换到模型 '{model_id_to_use}'。请确保模型可用。")

async def _handle_parameter_cache(req_id: str, context: dict) -> None:
    logger = context['logger']
    params_cache_lock = context['params_cache_lock']
    page_params_cache = context['page_params_cache']
    current_ai_studio_model_id = context['current_ai_studio_model_id']
    model_actually_switched = context['model_actually_switched']
    async with params_cache_lock:
        cached_model_for_params = page_params_cache.get('last_known_model_id_for_params')
        if model_actually_switched or current_ai_studio_model_id != cached_model_for_params:
            page_params_cache.clear()
            page_params_cache['last_known_model_id_for_params'] = current_ai_studio_model_id

async def _prepare_and_validate_request(req_id: str, request: ChatCompletionRequest, check_client_disconnected: Callable) -> Tuple[str, str, list]:
    from server import logger
    try:
        validate_chat_request(request.messages, req_id)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=f'[{req_id}] 无效请求: {e}')
    system_prompt, prepared_prompt, final_image_list = prepare_combined_prompt(request.messages, req_id)
    check_client_disconnected('After Prompt Prep')
    if final_image_list:
        logger.info(f'[{req_id}] 🖼️ 准备上传 {len(final_image_list)} 张图片')
    return (system_prompt, prepared_prompt, final_image_list)

async def _handle_response_processing(req_id: str, request: ChatCompletionRequest, page: AsyncPage, context: dict, result_future: Future, submit_button_locator: Locator, check_client_disconnected: Callable, disconnect_check_task: Optional[asyncio.Task]) -> Optional[Tuple[Event, Locator, Callable]]:
    from server import logger
    is_streaming = request.stream
    current_ai_studio_model_id = context.get('current_ai_studio_model_id')
    stream_port = os.environ.get('STREAM_PORT')
    use_stream = stream_port != '0'
    if use_stream:
        return await _handle_auxiliary_stream_response(req_id, request, context, result_future, submit_button_locator, check_client_disconnected, disconnect_check_task)
    else:
        return await _handle_playwright_response(req_id, request, page, context, result_future, submit_button_locator, check_client_disconnected)

async def _handle_auxiliary_stream_response(req_id: str, request: ChatCompletionRequest, context: dict, result_future: Future, submit_button_locator: Locator, check_client_disconnected: Callable, disconnect_check_task: Optional[asyncio.Task]) -> Optional[Tuple[Event, Locator, Callable]]:
    from server import logger
    is_streaming = request.stream
    current_ai_studio_model_id = context.get('current_ai_studio_model_id')

    def generate_random_string(length):
        charset = 'abcdefghijklmnopqrstuvwxyz0123456789'
        return ''.join((random.choice(charset) for _ in range(length)))
    if is_streaming:
        try:
            completion_event = Event()
            max_stream_retries = calculate_stream_max_retries(request.messages)
            logger.info(f"[{req_id}] 动态流式超时设置 - Max Retries: {max_stream_retries}")

            async def create_stream_generator_from_helper(event_to_set: Event, task_to_cancel: Optional[asyncio.Task], page_controller: PageController) -> AsyncGenerator[str, None]:
                skip_button_stop_event = asyncio.Event()
                skip_monitor_task = asyncio.create_task(page_controller.continuously_handle_skip_button(skip_button_stop_event, check_client_disconnected))
                last_reason_pos = 0
                last_body_pos = 0
                model_name_for_stream = current_ai_studio_model_id or MODEL_NAME
                chat_completion_id = f'{CHAT_COMPLETION_ID_PREFIX}{req_id}-{int(time.time())}-{random.randint(100, 999)}'
                created_timestamp = int(time.time())
                full_reasoning_content = ''
                full_body_content = ''
                data_receiving = False
                try:
                    async for raw_data in use_stream_response(req_id, max_stream_retries):
                        data_receiving = True
                        try:
                            check_client_disconnected(f'流式生成器循环 ({req_id}): ')
                        except ClientDisconnectedError:
                            logger.info(f'[{req_id}] 🚨 流式生成器检测到客户端断开连接（通过事件）')
                            if data_receiving and (not event_to_set.is_set()):
                                logger.info(f'[{req_id}] 数据接收中客户端断开，立即设置done信号')
                                event_to_set.set()
                            try:
                                stop_chunk = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [{'index': 0, 'delta': {'role': 'assistant'}, 'finish_reason': 'stop', 'native_finish_reason': 'stop'}]}
                                yield f"data: {json.dumps(stop_chunk, ensure_ascii=False, separators=(',', ':'))}\n\n"
                                yield 'data: [DONE]\n\n'
                            except Exception:
                                pass
                            break
                        try:
                            import server
                            if hasattr(server, 'current_http_requests') and req_id in server.current_http_requests:
                                current_http_request = server.current_http_requests[req_id]
                                is_connected = await _test_client_connection(req_id, current_http_request)
                                if not is_connected:
                                    logger.info(f'[{req_id}] 🚨 流式生成器独立检测到客户端断开！')
                                    if data_receiving and (not event_to_set.is_set()):
                                        event_to_set.set()
                                    try:
                                        stop_chunk = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [{'index': 0, 'delta': {'role': 'assistant'}, 'finish_reason': 'stop', 'native_finish_reason': 'stop'}]}
                                        yield f"data: {json.dumps(stop_chunk, ensure_ascii=False, separators=(',', ':'))}\n\n"
                                        yield 'data: [DONE]\n\n'
                                    except Exception:
                                        pass
                                    break
                        except Exception as direct_check_err:
                            pass
                        if isinstance(raw_data, str):
                            try:
                                data = json.loads(raw_data)
                            except json.JSONDecodeError:
                                logger.warning(f'[{req_id}] 无法解析流数据JSON: {raw_data}')
                                continue
                        elif isinstance(raw_data, dict):
                            data = raw_data
                        else:
                            logger.warning(f'[{req_id}] 未知的流数据类型: {type(raw_data)}')
                            continue
                        if not isinstance(data, dict):
                            logger.warning(f'[{req_id}] 数据不是字典类型: {data}')
                            continue
                        reason = data.get('reason', '')
                        body = data.get('body', '')
                        done = data.get('done', False)
                        function = data.get('function', [])
                        if reason:
                            full_reasoning_content = reason
                        if body:
                            full_body_content = body
                        if len(reason) > last_reason_pos:
                            output = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [{'index': 0, 'delta': {'role': 'assistant', 'content': None, 'reasoning_content': reason[last_reason_pos:]}, 'finish_reason': None, 'native_finish_reason': None}]}
                            last_reason_pos = len(reason)
                            yield f"data: {json.dumps(output, ensure_ascii=False, separators=(',', ':'))}\n\n"
                        if len(body) > last_body_pos:
                            finish_reason_val = None
                            if done:
                                finish_reason_val = 'stop'
                            delta_content = {'role': 'assistant', 'content': body[last_body_pos:]}
                            choice_item = {'index': 0, 'delta': delta_content, 'finish_reason': finish_reason_val, 'native_finish_reason': finish_reason_val}
                            if done and function and (len(function) > 0):
                                tool_calls_list = []
                                for func_idx, function_call_data in enumerate(function):
                                    tool_calls_list.append({'id': f'call_{generate_random_string(24)}', 'index': func_idx, 'type': 'function', 'function': {'name': function_call_data['name'], 'arguments': json.dumps(function_call_data['params'])}})
                                delta_content['tool_calls'] = tool_calls_list
                                choice_item['finish_reason'] = 'tool_calls'
                                choice_item['native_finish_reason'] = 'tool_calls'
                                delta_content['content'] = None
                            output = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [choice_item]}
                            last_body_pos = len(body)
                            yield f"data: {json.dumps(output, ensure_ascii=False, separators=(',', ':'))}\n\n"
                        elif done:
                            if function and len(function) > 0:
                                delta_content = {'role': 'assistant', 'content': None}
                                tool_calls_list = []
                                for func_idx, function_call_data in enumerate(function):
                                    tool_calls_list.append({'id': f'call_{generate_random_string(24)}', 'index': func_idx, 'type': 'function', 'function': {'name': function_call_data['name'], 'arguments': json.dumps(function_call_data['params'])}})
                                delta_content['tool_calls'] = tool_calls_list
                                choice_item = {'index': 0, 'delta': delta_content, 'finish_reason': 'tool_calls', 'native_finish_reason': 'tool_calls'}
                            else:
                                choice_item = {'index': 0, 'delta': {'role': 'assistant'}, 'finish_reason': 'stop', 'native_finish_reason': 'stop'}
                            output = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [choice_item]}
                            yield f"data: {json.dumps(output, ensure_ascii=False, separators=(',', ':'))}\n\n"
                except ClientDisconnectedError as disconnect_err:
                    abort_handler = AbortSignalHandler()
                    disconnect_info = abort_handler.handle_error(disconnect_err, req_id)
                    logger.info(f'[{req_id}] 流式生成器中检测到客户端断开连接')
                    logger.info(f'[{req_id}] 停止原因分析: {disconnect_info}')
                    if data_receiving and (not event_to_set.is_set()):
                        logger.info(f'[{req_id}] 客户端断开异常处理中立即设置done信号')
                        event_to_set.set()
                except Exception as e:
                    abort_handler = AbortSignalHandler()
                    error_info = abort_handler.handle_error(e, req_id)
                    if error_info['stop_reason'] in ['user_abort', 'client_disconnect']:
                        logger.info(f'[{req_id}] 检测到停止信号: {error_info}')
                        if data_receiving and (not event_to_set.is_set()):
                            event_to_set.set()
                    else:
                        logger.error(f'[{req_id}] 流式生成器处理过程中发生错误: {e}', exc_info=True)
                    try:
                        error_chunk = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [{'index': 0, 'delta': {'role': 'assistant', 'content': f'\n\n[错误: {str(e)}]'}, 'finish_reason': 'stop', 'native_finish_reason': 'stop'}]}
                        yield f"data: {json.dumps(error_chunk, ensure_ascii=False, separators=(',', ':'))}\n\n"
                    except Exception:
                        pass
                finally:
                    logger.info(f"[{req_id}] 流式生成器结束，正在停止 'Skip' 按钮监控...")
                    skip_button_stop_event.set()
                    try:
                        await asyncio.wait_for(skip_monitor_task, timeout=2.0)
                        logger.info(f"[{req_id}] 'Skip' 按钮监控任务已成功清理。")
                    except asyncio.TimeoutError:
                        logger.warning(f"[{req_id}] 'Skip' 按钮监控任务关闭超时。")
                    except Exception as e_clean_skip:
                        logger.error(f"[{req_id}] 清理 'Skip' 按钮监控任务时出错: {e_clean_skip}")
                    try:
                        usage_stats = calculate_usage_stats([msg.model_dump() for msg in request.messages], full_body_content, full_reasoning_content)
                        logger.info(f'[{req_id}] 计算的token使用统计: {usage_stats}')
                        final_chunk = {'id': chat_completion_id, 'object': 'chat.completion.chunk', 'model': model_name_for_stream, 'created': created_timestamp, 'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop', 'native_finish_reason': 'stop'}], 'usage': usage_stats}
                        yield f"data: {json.dumps(final_chunk, ensure_ascii=False, separators=(',', ':'))}\n\n"
                        logger.info(f'[{req_id}] 已发送带usage统计的最终chunk')
                    except Exception as usage_err:
                        logger.error(f'[{req_id}] 计算或发送usage统计时出错: {usage_err}')
                    try:
                        logger.info(f'[{req_id}] 流式生成器完成，发送 [DONE] 标记')
                        yield 'data: [DONE]\n\n'
                    except Exception as done_err:
                        logger.error(f'[{req_id}] 发送 [DONE] 标记时出错: {done_err}')
                    if not event_to_set.is_set():
                        event_to_set.set()
                        logger.info(f'[{req_id}] 流式生成器完成事件已设置')
                    logger.info(f'[{req_id}] 流式生成器结束，开始清理资源...')
                    import server
                    if hasattr(server, 'current_http_requests'):
                        server.current_http_requests.pop(req_id, None)
                        logger.info(f'[{req_id}] ✅ 已清理全局HTTP请求状态')
                    if task_to_cancel and (not task_to_cancel.done()):
                        task_to_cancel.cancel()
                        logger.info(f'[{req_id}] ✅ 已发送取消信号到监控任务')
                    else:
                        logger.info(f'[{req_id}] ✅ 监控任务无需取消（可能已完成或不存在）')
            page_controller = PageController(context['page'], logger, req_id)
            stream_gen_func = create_stream_generator_from_helper(completion_event, disconnect_check_task, page_controller)
            if not result_future.done():
                result_future.set_result(StreamingResponse(stream_gen_func, media_type='text/event-stream'))
            elif not completion_event.is_set():
                completion_event.set()
            return (completion_event, submit_button_locator, check_client_disconnected)
        except Exception as e:
            logger.error(f'[{req_id}] 从队列获取流式数据时出错: {e}', exc_info=True)
            if completion_event and (not completion_event.is_set()):
                completion_event.set()
            raise
    else:
        content = None
        reasoning_content = None
        functions = None
        final_data_from_aux_stream = None
        max_stream_retries = calculate_stream_max_retries(request.messages)
        logger.info(f"[{req_id}] 动态非流式超时设置 - Max Retries: {max_stream_retries}")

        async for raw_data in use_stream_response(req_id, max_stream_retries):
            check_client_disconnected(f'非流式辅助流 - 循环中 ({req_id}): ')
            if isinstance(raw_data, str):
                try:
                    data = json.loads(raw_data)
                except json.JSONDecodeError:
                    logger.warning(f'[{req_id}] 无法解析非流式数据JSON: {raw_data}')
                    continue
            elif isinstance(raw_data, dict):
                data = raw_data
            else:
                logger.warning(f'[{req_id}] 非流式未知数据类型: {type(raw_data)}')
                continue
            if not isinstance(data, dict):
                logger.warning(f'[{req_id}] 非流式数据不是字典类型: {data}')
                continue
            final_data_from_aux_stream = data
            if data.get('done'):
                content = data.get('body')
                reasoning_content = data.get('reason')
                functions = data.get('function')
                break
        if final_data_from_aux_stream and final_data_from_aux_stream.get('reason') == 'internal_timeout':
            logger.error(f'[{req_id}] 非流式请求通过辅助流失败: 内部超时')
            raise HTTPException(status_code=502, detail=f'[{req_id}] 辅助流处理错误 (内部超时)')
        if final_data_from_aux_stream and final_data_from_aux_stream.get('done') is True and (content is None):
            logger.error(f'[{req_id}] 非流式请求通过辅助流完成但未提供内容')
            raise HTTPException(status_code=502, detail=f'[{req_id}] 辅助流完成但未提供内容')
        model_name_for_json = current_ai_studio_model_id or MODEL_NAME
        message_payload = {'role': 'assistant', 'content': content}
        finish_reason_val = 'stop'
        if functions and len(functions) > 0:
            tool_calls_list = []
            for func_idx, function_call_data in enumerate(functions):
                tool_calls_list.append({'id': f'call_{generate_random_string(24)}', 'index': func_idx, 'type': 'function', 'function': {'name': function_call_data['name'], 'arguments': json.dumps(function_call_data['params'])}})
            message_payload['tool_calls'] = tool_calls_list
            finish_reason_val = 'tool_calls'
            message_payload['content'] = None
        if reasoning_content:
            message_payload['reasoning_content'] = reasoning_content
        usage_stats = calculate_usage_stats([msg.model_dump() for msg in request.messages], content or '', reasoning_content)
        response_payload = {'id': f'{CHAT_COMPLETION_ID_PREFIX}{req_id}-{int(time.time())}', 'object': 'chat.completion', 'created': int(time.time()), 'model': model_name_for_json, 'choices': [{'index': 0, 'message': message_payload, 'finish_reason': finish_reason_val, 'native_finish_reason': finish_reason_val}], 'usage': usage_stats}
        if not result_future.done():
            result_future.set_result(JSONResponse(content=response_payload))
        return None

async def _handle_playwright_response(req_id: str, request: ChatCompletionRequest, page: AsyncPage, context: dict, result_future: Future, submit_button_locator: Locator, check_client_disconnected: Callable) -> Optional[Tuple[Event, Locator, Callable]]:
    from server import logger
    is_streaming = request.stream
    current_ai_studio_model_id = context.get('current_ai_studio_model_id')
    logger.info(f'[{req_id}] 定位响应元素...')
    response_container = page.locator(RESPONSE_CONTAINER_SELECTOR).last
    response_element = response_container.locator(RESPONSE_TEXT_SELECTOR)
    try:
        await expect_async(response_container).to_be_attached(timeout=20000)
        check_client_disconnected('After Response Container Attached: ')
        await expect_async(response_element).to_be_attached(timeout=90000)
        logger.info(f'[{req_id}] 响应元素已定位。')
    except (PlaywrightAsyncError, asyncio.TimeoutError, ClientDisconnectedError) as locate_err:
        if isinstance(locate_err, ClientDisconnectedError):
            raise
        logger.error(f'[{req_id}] ❌ 错误: 定位响应元素失败或超时: {locate_err}')
        await save_error_snapshot(f'response_locate_error_{req_id}')
        raise HTTPException(status_code=502, detail=f'[{req_id}] 定位AI Studio响应元素失败: {locate_err}')
    except Exception as locate_exc:
        logger.exception(f'[{req_id}] ❌ 错误: 定位响应元素时意外错误')
        await save_error_snapshot(f'response_locate_unexpected_{req_id}')
        raise HTTPException(status_code=500, detail=f'[{req_id}] 定位响应元素时意外错误: {locate_exc}')
    check_client_disconnected('After Response Element Located: ')
    if is_streaming:
        completion_event = Event()

        async def create_response_stream_generator():
            data_receiving = False
            page_controller = PageController(page, logger, req_id)
            skip_button_stop_event = asyncio.Event()
            skip_monitor_task = asyncio.create_task(page_controller.continuously_handle_skip_button(skip_button_stop_event, check_client_disconnected))
            try:
                final_content = await page_controller.get_response(check_client_disconnected)
                data_receiving = True
                lines = final_content.split('\n')
                for line_idx, line in enumerate(lines):
                    try:
                        check_client_disconnected(f'Playwright流式生成器循环 ({req_id}): ')
                    except ClientDisconnectedError:
                        logger.info(f'[{req_id}] Playwright流式生成器中检测到客户端断开连接')
                        if data_receiving and (not completion_event.is_set()):
                            logger.info(f'[{req_id}] Playwright数据接收中客户端断开，立即设置done信号')
                            completion_event.set()
                        try:
                            yield generate_sse_stop_chunk(req_id, current_ai_studio_model_id or MODEL_NAME, 'stop')
                        except Exception:
                            pass
                        break
                    if line:
                        chunk_size = 5
                        for i in range(0, len(line), chunk_size):
                            chunk = line[i:i + chunk_size]
                            yield generate_sse_chunk(chunk, req_id, current_ai_studio_model_id or MODEL_NAME)
                            await asyncio.sleep(0.03)
                    if line_idx < len(lines) - 1:
                        yield generate_sse_chunk('\n', req_id, current_ai_studio_model_id or MODEL_NAME)
                        await asyncio.sleep(0.01)
                usage_stats = calculate_usage_stats([msg.model_dump() for msg in request.messages], final_content, '')
                logger.info(f'[{req_id}] Playwright非流式计算的token使用统计: {usage_stats}')
                yield generate_sse_stop_chunk(req_id, current_ai_studio_model_id or MODEL_NAME, 'stop', usage_stats)
            except ClientDisconnectedError as disconnect_err:
                abort_handler = AbortSignalHandler()
                disconnect_info = abort_handler.handle_error(disconnect_err, req_id)
                logger.info(f'[{req_id}] Playwright流式生成器中检测到客户端断开连接')
                logger.info(f'[{req_id}] 停止原因分析: {disconnect_info}')
                if data_receiving and (not completion_event.is_set()):
                    logger.info(f'[{req_id}] Playwright客户端断开异常处理中立即设置done信号')
                    completion_event.set()
            except Exception as e:
                abort_handler = AbortSignalHandler()
                error_info = abort_handler.handle_error(e, req_id)
                if error_info['stop_reason'] in ['user_abort', 'client_disconnect']:
                    logger.info(f'[{req_id}] Playwright检测到停止信号: {error_info}')
                    if data_receiving and (not completion_event.is_set()):
                        completion_event.set()
                else:
                    logger.error(f'[{req_id}] Playwright流式生成器处理过程中发生错误: {e}', exc_info=True)
                try:
                    yield generate_sse_chunk(f'\n\n[错误: {str(e)}]', req_id, current_ai_studio_model_id or MODEL_NAME)
                    yield generate_sse_stop_chunk(req_id, current_ai_studio_model_id or MODEL_NAME)
                except Exception:
                    pass
            finally:
                logger.info(f"[{req_id}] Playwright流式生成器结束，正在停止 'Skip' 按钮监控...")
                skip_button_stop_event.set()
                try:
                    await asyncio.wait_for(skip_monitor_task, timeout=2.0)
                    logger.info(f"[{req_id}] Playwright 'Skip' 按钮监控任务已成功清理。")
                except asyncio.TimeoutError:
                    logger.warning(f"[{req_id}] Playwright 'Skip' 按钮监控任务关闭超时。")
                except Exception as e_clean_skip:
                    logger.error(f"[{req_id}] 清理 Playwright 'Skip' 按钮监控任务时出错: {e_clean_skip}")
                if not completion_event.is_set():
                    completion_event.set()
                    logger.info(f'[{req_id}] Playwright流式生成器完成事件已设置')
        stream_gen_func = create_response_stream_generator()
        if not result_future.done():
            result_future.set_result(StreamingResponse(stream_gen_func, media_type='text/event-stream'))
        return (completion_event, submit_button_locator, check_client_disconnected)
    else:
        page_controller = PageController(page, logger, req_id)
        final_content = await page_controller.get_response(check_client_disconnected)
        usage_stats = calculate_usage_stats([msg.model_dump() for msg in request.messages], final_content, '')
        logger.info(f'[{req_id}] Playwright非流式计算的token使用统计: {usage_stats}')
        response_payload = {'id': f'{CHAT_COMPLETION_ID_PREFIX}{req_id}-{int(time.time())}', 'object': 'chat.completion', 'created': int(time.time()), 'model': current_ai_studio_model_id or MODEL_NAME, 'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': final_content}, 'finish_reason': 'stop'}], 'usage': usage_stats}
        if not result_future.done():
            result_future.set_result(JSONResponse(content=response_payload))
        return None

async def _cleanup_request_resources(req_id: str, disconnect_check_task: Optional[asyncio.Task], completion_event: Optional[Event], result_future: Future, is_streaming: bool) -> None:
    from server import logger
    if is_streaming:
        logger.info(f'[{req_id}] 流式响应：监控任务将在生成完成后自然结束')
        if result_future.done() and result_future.exception() is not None:
            logger.warning(f'[{req_id}] 流式请求发生异常，取消监控任务')
            if disconnect_check_task and (not disconnect_check_task.done()):
                disconnect_check_task.cancel()
                try:
                    await disconnect_check_task
                except asyncio.CancelledError:
                    pass
                except Exception as task_clean_err:
                    logger.error(f'[{req_id}] 清理异常监控任务时出错: {task_clean_err}')
        else:
            logger.info(f'[{req_id}] 正常流式响应：保持监控任务活跃状态')
    elif disconnect_check_task and (not disconnect_check_task.done()):
        logger.info(f'[{req_id}] 非流式响应：取消监控任务')
        disconnect_check_task.cancel()
        try:
            await disconnect_check_task
        except asyncio.CancelledError:
            pass
        except Exception as task_clean_err:
            logger.error(f'[{req_id}] 清理任务时出错: {task_clean_err}')
    logger.info(f'[{req_id}] 处理完成。')
    if is_streaming and completion_event and (not completion_event.is_set()) and (result_future.done() and result_future.exception() is not None):
        logger.warning(f'[{req_id}] 流式请求异常，确保完成事件已设置。')
        completion_event.set()

async def _process_request_refactored(req_id: str, request: ChatCompletionRequest, http_request: Request, result_future: Future) -> Optional[Tuple[Event, Locator, Callable[[str], bool]]]:
    import server
    if not hasattr(server, 'current_http_requests'):
        server.current_http_requests = {}
    server.current_http_requests[req_id] = http_request
    is_connected = await _test_client_connection(req_id, http_request)
    if not is_connected:
        from server import logger
        logger.info(f'[{req_id}]  核心处理前检测到客户端断开，提前退出节省资源')
        server.current_http_requests.pop(req_id, None)
        if not result_future.done():
            result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 客户端在处理开始前已断开连接'))
        return None
    context = await _initialize_request_context(req_id, request)
    context = await _analyze_model_requirements(req_id, context, request)
    page = context['page']
    client_disconnected_event, disconnect_check_task, check_client_disconnected = await _setup_disconnect_monitoring(req_id, http_request, result_future, page)
    submit_button_locator = page.locator(SUBMIT_BUTTON_SELECTOR) if page else None
    completion_event = None
    try:
        await _validate_page_status(req_id, context, check_client_disconnected)
        page_controller = PageController(page, context['logger'], req_id)

        model_switch_task = asyncio.create_task(_handle_model_switching(req_id, context, check_client_disconnected))
        prep_task = asyncio.create_task(_prepare_and_validate_request(req_id, request, check_client_disconnected))
        
        context = await model_switch_task
        system_prompt, prepared_prompt, image_list = await prep_task

        await _handle_parameter_cache(req_id, context)
        await page_controller.adjust_parameters(request.model_dump(exclude_none=True), context['page_params_cache'], context['params_cache_lock'], context['model_id_to_use'], context['parsed_model_list'], check_client_disconnected)
        await page_controller.set_system_instructions(system_prompt, check_client_disconnected)
        check_client_disconnected('提交提示前最终检查')
        await page_controller.submit_prompt(prepared_prompt, image_list, check_client_disconnected)
        response_result = await _handle_response_processing(req_id, request, page, context, result_future, submit_button_locator, check_client_disconnected, disconnect_check_task)
        if response_result:
            completion_event, _, _ = response_result
        return (completion_event, submit_button_locator, check_client_disconnected)
    except ClientDisconnectedError as disco_err:
        context['logger'].info(f'[{req_id}] 捕获到客户端断开连接信号: {disco_err}')
        if not result_future.done():
            result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] Client disconnected during processing.'))
    except HTTPException as http_err:
        context['logger'].warning(f'[{req_id}] 捕获到 HTTP 异常: {http_err.status_code} - {http_err.detail}')
        if not result_future.done():
            result_future.set_exception(http_err)
    except PlaywrightAsyncError as pw_err:
        context['logger'].error(f'[{req_id}] 捕获到 Playwright 错误: {pw_err}')
        await save_error_snapshot(f'process_playwright_error_{req_id}')
        if not result_future.done():
            result_future.set_exception(HTTPException(status_code=502, detail=f'[{req_id}] Playwright interaction failed: {pw_err}'))
    except Exception as e:
        context['logger'].exception(f'[{req_id}] 捕获到意外错误')
        await save_error_snapshot(f'process_unexpected_error_{req_id}')
        if not result_future.done():
            result_future.set_exception(HTTPException(status_code=500, detail=f'[{req_id}] Unexpected server error: {e}'))
    finally:
        request_manager.unregister_request(req_id)
        await _cleanup_request_resources(req_id, disconnect_check_task, completion_event, result_future, request.stream)