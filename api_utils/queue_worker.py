import asyncio
import time
from fastapi import HTTPException

async def queue_worker():
    """队列工作器，处理请求队列中的任务"""
    from server import logger, request_queue, processing_lock, model_switching_lock, params_cache_lock
    logger.info('--- 队列 Worker 已启动 ---')
    if request_queue is None:
        logger.info('初始化 request_queue...')
        from asyncio import Queue
        request_queue = Queue()
    if processing_lock is None:
        logger.info('初始化 processing_lock...')
        from asyncio import Lock
        processing_lock = Lock()
    if model_switching_lock is None:
        logger.info('初始化 model_switching_lock...')
        from asyncio import Lock
        model_switching_lock = Lock()
    if params_cache_lock is None:
        logger.info('初始化 params_cache_lock...')
        from asyncio import Lock
        params_cache_lock = Lock()
    was_last_request_streaming = False
    last_request_completion_time = 0
    while True:
        request_item = None
        result_future = None
        req_id = 'UNKNOWN'
        completion_event = None
        try:
            queue_size = request_queue.qsize()
            if queue_size > 0:
                checked_count = 0
                items_to_requeue = []
                processed_ids = set()
                
                # 批量取出任务
                items_to_check = []
                while checked_count < queue_size and checked_count < 10:
                    try:
                        item = request_queue.get_nowait()
                        item_req_id = item.get('req_id', 'unknown')
                        
                        # 避免重复处理
                        if item_req_id in processed_ids:
                            items_to_requeue.append(item)
                            continue
                            
                        processed_ids.add(item_req_id)
                        items_to_check.append(item)
                        checked_count += 1
                    except asyncio.QueueEmpty:
                        break

                # 定义并发检查函数
                async def check_item_disconnect(item_data):
                    i_req_id = item_data.get('req_id', 'unknown')
                    if not item_data.get('cancelled', False):
                        i_http_request = item_data.get('http_request')
                        if i_http_request:
                            try:
                                if await i_http_request.is_disconnected():
                                    logger.info(f'[{i_req_id}] (Worker Queue Check) 检测到客户端已断开，标记为取消。')
                                    item_data['cancelled'] = True
                                    i_future = item_data.get('result_future')
                                    if i_future and (not i_future.done()):
                                        i_future.set_exception(HTTPException(status_code=499, detail=f'[{i_req_id}] Client disconnected while queued.'))
                            except Exception as check_err:
                                logger.error(f'[{i_req_id}] (Worker Queue Check) Error checking disconnect: {check_err}')
                    return item_data

                if items_to_check:
                    # 并行执行所有检查
                    checked_results = await asyncio.gather(*[check_item_disconnect(i) for i in items_to_check])
                    items_to_requeue.extend(checked_results)

                # 批量放回队列
                for item in items_to_requeue:
                    await request_queue.put(item)
            try:
                request_item = await asyncio.wait_for(request_queue.get(), timeout=5.0)
            except asyncio.TimeoutError:
                continue
            req_id = request_item['req_id']
            request_data = request_item['request_data']
            http_request = request_item['http_request']
            result_future = request_item['result_future']
            if request_item.get('cancelled', False):
                logger.info(f'[{req_id}] (Worker) 请求已取消，跳过。')
                if not result_future.done():
                    result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 请求已被用户取消'))
                request_queue.task_done()
                continue
            is_streaming_request = request_data.stream
            logger.info(f"[{req_id}] (Worker) 取出请求。模式: {('流式' if is_streaming_request else '非流式')}")
            from api_utils.request_processor import _test_client_connection
            is_connected = await _test_client_connection(req_id, http_request)
            if not is_connected:
                logger.info(f'[{req_id}] (Worker) ✅ 主动检测到客户端已断开，跳过处理节省资源')
                if not result_future.done():
                    result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 客户端在处理前已断开连接'))
                request_queue.task_done()
                continue
            current_time = time.time()
            if was_last_request_streaming and is_streaming_request and (current_time - last_request_completion_time < 1.0):
                delay_time = max(0.5, 1.0 - (current_time - last_request_completion_time))
                logger.info(f'[{req_id}] (Worker) 连续流式请求，添加 {delay_time:.2f}s 延迟...')
                await asyncio.sleep(delay_time)
            is_connected = await _test_client_connection(req_id, http_request)
            if not is_connected:
                logger.info(f'[{req_id}] (Worker) ✅ 等待锁时检测到客户端断开，取消处理')
                if not result_future.done():
                    result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 客户端关闭了请求'))
                request_queue.task_done()
                continue
            logger.info(f'[{req_id}] (Worker) 等待处理锁...')
            async with processing_lock:
                logger.info(f'[{req_id}] (Worker) 已获取处理锁。开始核心处理...')
                is_connected = await _test_client_connection(req_id, http_request)
                if not is_connected:
                    logger.info(f'[{req_id}] (Worker) ✅ 获取锁后检测到客户端断开，取消处理')
                    if not result_future.done():
                        result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 客户端关闭了请求'))
                elif result_future.done():
                    logger.info(f'[{req_id}] (Worker) Future 在处理前已完成/取消。跳过。')
                else:
                    try:
                        from server import page_instance, is_page_ready
                        if page_instance and (not page_instance.is_closed()) and is_page_ready:
                            from browser_utils.page_controller import PageController
                            from api_utils.request_processor import _setup_disconnect_monitoring
                            _, _, temp_check_disco = await _setup_disconnect_monitoring(req_id, http_request, result_future, page_instance)
                            page_controller = PageController(page_instance, logger, req_id)
                            logger.info(f'[{req_id}] (Worker) 在处理新请求前执行聊天历史清空...')
                            await page_controller.clear_chat_history(temp_check_disco)
                            logger.info(f'[{req_id}] (Worker) ✅ 聊天历史清空完成并验证成功。')
                        else:
                            logger.warning(f'[{req_id}] (Worker) 页面未就绪，跳过前置清空操作。')
                    except Exception as clear_err:
                        logger.error(f'[{req_id}] (Worker) 在处理前清空聊天历史时发生错误: {clear_err}', exc_info=True)
                        if not result_future.done():
                            result_future.set_exception(HTTPException(status_code=500, detail=f'[{req_id}] 聊天历史清空失败，无法继续处理请求'))
                        request_queue.task_done()
                        continue
                    try:
                        from api_utils import _process_request_refactored
                        returned_value = await _process_request_refactored(req_id, request_data, http_request, result_future)
                        completion_event, submit_btn_loc, client_disco_checker = (None, None, None)
                        current_request_was_streaming = False
                        if isinstance(returned_value, tuple) and len(returned_value) == 3:
                            completion_event, submit_btn_loc, client_disco_checker = returned_value
                            if completion_event is not None:
                                current_request_was_streaming = True
                                logger.info(f'[{req_id}] (Worker) _process_request_refactored returned stream info (event, locator, checker).')
                            else:
                                current_request_was_streaming = False
                                logger.info(f'[{req_id}] (Worker) _process_request_refactored returned a tuple, but completion_event is None (likely non-stream or early exit).')
                        elif returned_value is None:
                            current_request_was_streaming = False
                            logger.info(f'[{req_id}] (Worker) _process_request_refactored returned non-stream completion (None).')
                        else:
                            current_request_was_streaming = False
                            logger.warning(f'[{req_id}] (Worker) _process_request_refactored returned unexpected type: {type(returned_value)}')
                        if completion_event:
                            logger.info(f'[{req_id}] (Worker) 等待流式生成器完成信号...')
                            client_disconnected_early = False

                            async def enhanced_disconnect_monitor():
                                nonlocal client_disconnected_early
                                while not completion_event.is_set():
                                    try:
                                        is_connected = await _test_client_connection(req_id, http_request)
                                        if not is_connected:
                                            logger.info(f'[{req_id}] (Worker) ✅ 流式处理中检测到客户端断开，提前触发done信号')
                                            client_disconnected_early = True
                                            if not completion_event.is_set():
                                                completion_event.set()
                                            break
                                        await asyncio.sleep(0.3)
                                    except Exception as e:
                                        logger.error(f'[{req_id}] (Worker) 增强断开检测器错误: {e}')
                                        break
                            disconnect_monitor_task = asyncio.create_task(enhanced_disconnect_monitor())
                        else:
                            logger.info(f'[{req_id}] (Worker) 非流式模式，等待处理完成...')
                            client_disconnected_early = False

                            async def non_streaming_disconnect_monitor():
                                nonlocal client_disconnected_early
                                while not result_future.done():
                                    try:
                                        is_connected = await _test_client_connection(req_id, http_request)
                                        if not is_connected:
                                            logger.info(f'[{req_id}] (Worker) ✅ 非流式处理中检测到客户端断开，取消处理')
                                            client_disconnected_early = True
                                            if not result_future.done():
                                                result_future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] 客户端在非流式处理中断开连接'))
                                            break
                                        await asyncio.sleep(0.3)
                                    except Exception as e:
                                        logger.error(f'[{req_id}] (Worker) 非流式断开检测器错误: {e}')
                                        break
                            disconnect_monitor_task = asyncio.create_task(non_streaming_disconnect_monitor())
                        try:
                            if completion_event:
                                from server import RESPONSE_COMPLETION_TIMEOUT
                                await asyncio.wait_for(completion_event.wait(), timeout=RESPONSE_COMPLETION_TIMEOUT / 1000 + 60)
                                logger.info(f'[{req_id}] (Worker) ✅ 流式生成器完成信号收到。客户端提前断开: {client_disconnected_early}')
                            else:
                                from server import RESPONSE_COMPLETION_TIMEOUT
                                await asyncio.wait_for(asyncio.shield(result_future), timeout=RESPONSE_COMPLETION_TIMEOUT / 1000 + 60)
                                logger.info(f'[{req_id}] (Worker) ✅ 非流式处理完成。客户端提前断开: {client_disconnected_early}')
                            if client_disconnected_early:
                                logger.info(f'[{req_id}] (Worker) 客户端提前断开，跳过按钮状态处理')
                            elif submit_btn_loc and client_disco_checker and completion_event:
                                logger.info(f'[{req_id}] (Worker) 流式响应完成，检查并处理发送按钮状态...')
                                wait_timeout_ms = 30000
                                try:
                                    from playwright.async_api import expect as expect_async
                                    from api_utils.request_processor import ClientDisconnectedError
                                    client_disco_checker('流式响应后按钮状态检查 - 前置检查: ')
                                    await asyncio.sleep(0.5)
                                    logger.info(f'[{req_id}] (Worker) 检查发送按钮状态...')
                                    try:
                                        is_button_enabled = await submit_btn_loc.is_enabled(timeout=2000)
                                        logger.info(f'[{req_id}] (Worker) 发送按钮启用状态: {is_button_enabled}')
                                        if is_button_enabled:
                                            logger.info(f'[{req_id}] (Worker) 流式响应完成但按钮仍启用，主动点击按钮停止生成...')
                                            await submit_btn_loc.click(timeout=5000, force=True)
                                            logger.info(f'[{req_id}] (Worker) ✅ 发送按钮点击完成。')
                                        else:
                                            logger.info(f'[{req_id}] (Worker) 发送按钮已禁用，无需点击。')
                                    except Exception as button_check_err:
                                        logger.warning(f'[{req_id}] (Worker) 检查按钮状态失败: {button_check_err}')
                                    logger.info(f'[{req_id}] (Worker) 等待发送按钮最终禁用...')
                                    await expect_async(submit_btn_loc).to_be_disabled(timeout=wait_timeout_ms)
                                    logger.info(f'[{req_id}] ✅ 发送按钮已禁用。')
                                except Exception as e_pw_disabled:
                                    logger.warning(f'[{req_id}] ⚠️ 流式响应后按钮状态处理超时或错误: {e_pw_disabled}')
                                    from api_utils.request_processor import save_error_snapshot
                                    await save_error_snapshot(f'stream_post_submit_button_handling_timeout_{req_id}')
                                except ClientDisconnectedError:
                                    logger.info(f'[{req_id}] 客户端在流式响应后按钮状态处理时断开连接。')
                            elif completion_event and current_request_was_streaming:
                                logger.warning(f'[{req_id}] (Worker) 流式请求但 submit_btn_loc 或 client_disco_checker 未提供。跳过按钮禁用等待。')
                        except asyncio.TimeoutError:
                            logger.warning(f'[{req_id}] (Worker) ⚠️ 等待处理完成超时。')
                            if not result_future.done():
                                result_future.set_exception(HTTPException(status_code=504, detail=f'[{req_id}] Processing timed out waiting for completion.'))
                        except Exception as ev_wait_err:
                            logger.error(f'[{req_id}] (Worker) ❌ 等待处理完成时出错: {ev_wait_err}')
                            if not result_future.done():
                                result_future.set_exception(HTTPException(status_code=500, detail=f'[{req_id}] Error waiting for completion: {ev_wait_err}'))
                        finally:
                            if 'disconnect_monitor_task' in locals() and (not disconnect_monitor_task.done()):
                                disconnect_monitor_task.cancel()
                                try:
                                    await disconnect_monitor_task
                                except asyncio.CancelledError:
                                    pass
                    except Exception as process_err:
                        logger.error(f'[{req_id}] (Worker) _process_request_refactored execution error: {process_err}')
                        if not result_future.done():
                            result_future.set_exception(HTTPException(status_code=500, detail=f'[{req_id}] Request processing error: {process_err}'))
            logger.info(f'[{req_id}] (Worker) 释放处理锁。')
            try:
                from api_utils import clear_stream_queue
                await clear_stream_queue()
            except Exception as clear_err:
                logger.error(f'[{req_id}] (Worker) 清空操作时发生错误: {clear_err}', exc_info=True)
            was_last_request_streaming = is_streaming_request
            last_request_completion_time = time.time()
        except asyncio.CancelledError:
            logger.info('--- 队列 Worker 被取消 ---')
            if result_future and (not result_future.done()):
                result_future.cancel('Worker cancelled')
            break
        except Exception as e:
            logger.error(f'[{req_id}] (Worker) ❌ 处理请求时发生意外错误: {e}', exc_info=True)
            if result_future and (not result_future.done()):
                result_future.set_exception(HTTPException(status_code=500, detail=f'[{req_id}] 服务器内部错误: {e}'))
        finally:
            if request_item:
                request_queue.task_done()
    logger.info('--- 队列 Worker 已停止 ---')