import asyncio
import os
import random
import time
import uuid
from typing import Dict, List, Any, Set, Optional
from asyncio import Queue, Future, Lock, Event
import logging
from fastapi import HTTPException, Request, WebSocket, WebSocketDisconnect, Depends
from fastapi.responses import JSONResponse, FileResponse
from pydantic import BaseModel
from playwright.async_api import Page as AsyncPage
from config import *
from models import ChatCompletionRequest, WebSocketConnectionManager
from browser import _handle_model_list_response
from .dependencies import *

async def get_api_info(request: Request, current_ai_studio_model_id: str=Depends(get_current_ai_studio_model_id)):
    from api import auth_utils
    server_port = request.url.port or os.environ.get('SERVER_PORT_INFO', '8000')
    host = request.headers.get('host') or f'127.0.0.1:{server_port}'
    scheme = request.headers.get('x-forwarded-proto', 'http')
    base_url = f'{scheme}://{host}'
    api_base = f'{base_url}/v1'
    effective_model_name = current_ai_studio_model_id or MODEL_NAME
    api_key_required = bool(auth_utils.API_KEYS)
    api_key_count = len(auth_utils.API_KEYS)
    if api_key_required:
        message = f'API Key is required. {api_key_count} valid key(s) configured.'
    else:
        message = 'API Key is not required.'
    return JSONResponse(content={'model_name': effective_model_name, 'api_base_url': api_base, 'server_base_url': base_url, 'api_key_required': api_key_required, 'api_key_count': api_key_count, 'auth_header': 'Authorization: Bearer <token> or X-API-Key: <token>' if api_key_required else None, 'openai_compatible': True, 'supported_auth_methods': ['Authorization: Bearer', 'X-API-Key'] if api_key_required else [], 'message': message})

async def health_check(server_state: Dict[str, Any]=Depends(get_server_state), worker_task=Depends(get_worker_task), request_queue: Queue=Depends(get_request_queue)):
    is_worker_running = bool(worker_task and (not worker_task.done()))
    launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
    browser_page_critical = launch_mode != 'direct_debug_no_browser'
    core_ready_conditions = [not server_state['is_initializing'], server_state['is_playwright_ready']]
    if browser_page_critical:
        core_ready_conditions.extend([server_state['is_browser_connected'], server_state['is_page_ready']])
    is_core_ready = all(core_ready_conditions)
    status_val = 'OK' if is_core_ready and is_worker_running else 'Error'
    q_size = request_queue.qsize() if request_queue else -1
    status_message_parts = []
    if server_state['is_initializing']:
        status_message_parts.append('初始化进行中')
    if not server_state['is_playwright_ready']:
        status_message_parts.append('Playwright 未就绪')
    if browser_page_critical:
        if not server_state['is_browser_connected']:
            status_message_parts.append('浏览器未连接')
        if not server_state['is_page_ready']:
            status_message_parts.append('页面未就绪')
    if not is_worker_running:
        status_message_parts.append('Worker 未运行')
    status = {'status': status_val, 'message': '', 'details': {**server_state, 'workerRunning': is_worker_running, 'queueLength': q_size, 'launchMode': launch_mode, 'browserAndPageCritical': browser_page_critical}}
    if status_val == 'OK':
        status['message'] = f'服务运行中;队列长度: {q_size}。'
        return JSONResponse(content=status, status_code=200)
    else:
        status['message'] = f"服务不可用;问题: {', '.join(status_message_parts) or '未知原因'}. 队列长度: {q_size}."
        return JSONResponse(content=status, status_code=503)

async def list_models(logger: logging.Logger=Depends(get_logger), model_list_fetch_event: Event=Depends(get_model_list_fetch_event), page_instance: AsyncPage=Depends(get_page_instance), parsed_model_list: List[Dict[str, Any]]=Depends(get_parsed_model_list), excluded_model_ids: Set[str]=Depends(get_excluded_model_ids)):
    logger.info('[API] 收到 /v1/models 请求。')
    if not model_list_fetch_event.is_set() and page_instance and (not page_instance.is_closed()):
        logger.info('/v1/models: 模型列表事件未设置，尝试刷新页面...')
        try:
            await page_instance.reload(wait_until='domcontentloaded', timeout=20000)
            await asyncio.wait_for(model_list_fetch_event.wait(), timeout=10.0)
        except Exception as e:
            logger.error(f'/v1/models: 刷新或等待模型列表时出错: {e}')
        finally:
            if not model_list_fetch_event.is_set():
                model_list_fetch_event.set()
    if parsed_model_list:
        final_model_list = [m for m in parsed_model_list if m.get('id') not in excluded_model_ids]
        return {'object': 'list', 'data': final_model_list}
    else:
        logger.warning('模型列表为空，返回默认后备模型。')
        return {'object': 'list', 'data': [{'id': DEFAULT_FALLBACK_MODEL_ID, 'object': 'model', 'created': int(time.time()), 'owned_by': 'camoufox-proxy-fallback'}]}

async def chat_completions(request: ChatCompletionRequest, http_request: Request, logger: logging.Logger=Depends(get_logger), request_queue: Queue=Depends(get_request_queue), server_state: Dict[str, Any]=Depends(get_server_state), worker_task=Depends(get_worker_task)):
    req_id = ''.join(random.choices('abcdefghijklmnopqrstuvwxyz0123456789', k=7))
    logger.info(f'[{req_id}] 📨 收到 /v1/chat/completions 请求 (Stream={request.stream})')
    launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
    browser_page_critical = launch_mode != 'direct_debug_no_browser'
    service_unavailable = server_state['is_initializing'] or not server_state['is_playwright_ready'] or (browser_page_critical and (not server_state['is_page_ready'] or not server_state['is_browser_connected'])) or (not worker_task) or worker_task.done()
    if service_unavailable:
        raise HTTPException(status_code=503, detail=f'[{req_id}] 服务当前不可用。请稍后重试。', headers={'Retry-After': '30'})
    result_future = Future()
    await request_queue.put({'req_id': req_id, 'request_data': request, 'http_request': http_request, 'result_future': result_future, 'enqueue_time': time.time(), 'cancelled': False})
    try:
        timeout_seconds = RESPONSE_COMPLETION_TIMEOUT / 1000 + 120
        return await asyncio.wait_for(result_future, timeout=timeout_seconds)
    except asyncio.TimeoutError:
        raise HTTPException(status_code=504, detail=f'[{req_id}] 请求处理超时。')
    except asyncio.CancelledError:
        raise HTTPException(status_code=499, detail=f'[{req_id}] 请求被客户端取消。')
    except HTTPException as http_exc:
        if http_exc.status_code == 499:
            logger.warning(f'[{req_id}] 🔌 客户端断开连接: {http_exc.detail}')
        else:
            logger.warning(f'[{req_id}] ⚠️ HTTP异常: {http_exc.detail}')
        raise http_exc
    except Exception as e:
        logger.exception(f'[{req_id}] ❌ 等待Worker响应时出错')
        raise HTTPException(status_code=500, detail=f'[{req_id}] 服务器内部错误: {e}')

async def cancel_queued_request(req_id: str, request_queue: Queue, logger: logging.Logger) -> bool:
    items_to_requeue = []
    found = False
    try:
        while not request_queue.empty():
            item = request_queue.get_nowait()
            if item.get('req_id') == req_id:
                logger.info(f'[{req_id}] 🗑️ 在队列中找到请求，标记为已取消。')
                item['cancelled'] = True
                if (future := item.get('result_future')) and (not future.done()):
                    future.set_exception(HTTPException(status_code=499, detail=f'[{req_id}] Request cancelled.'))
                found = True
            items_to_requeue.append(item)
    finally:
        for item in items_to_requeue:
            await request_queue.put(item)
    return found

async def cancel_request(req_id: str, logger: logging.Logger=Depends(get_logger), request_queue: Queue=Depends(get_request_queue)):
    from api.utils import request_manager
    logger.info(f'[{req_id}] 收到取消请求。')
    if request_manager.cancel_request(req_id):
        logger.info(f'[{req_id}] 正在处理的请求已标记为取消。')
        return JSONResponse(content={'success': True, 'message': f'Active request {req_id} marked as cancelled.', 'type': 'active_request'})
    if await cancel_queued_request(req_id, request_queue, logger):
        return JSONResponse(content={'success': True, 'message': f'Queued request {req_id} marked as cancelled.', 'type': 'queued_request'})
    else:
        return JSONResponse(status_code=404, content={'success': False, 'message': f'Request {req_id} not found in queue or active requests.', 'type': 'not_found'})

async def get_queue_status(request_queue: Queue=Depends(get_request_queue), processing_lock: Lock=Depends(get_processing_lock)):
    from api.utils import request_manager
    queue_items = list(request_queue._queue)
    active_requests = request_manager.get_active_requests()
    return JSONResponse(content={'queue_length': len(queue_items), 'active_requests_count': len(active_requests), 'is_processing_locked': processing_lock.locked(), 'queued_items': sorted([{'req_id': item.get('req_id', 'unknown'), 'enqueue_time': item.get('enqueue_time', 0), 'wait_time_seconds': round(time.time() - item.get('enqueue_time', 0), 2), 'is_streaming': item.get('request_data').stream, 'cancelled': item.get('cancelled', False)} for item in queue_items], key=lambda x: x.get('enqueue_time', 0)), 'active_items': sorted(active_requests, key=lambda x: x.get('duration', 0), reverse=True)})

async def websocket_log_endpoint(websocket: WebSocket, logger: logging.Logger=Depends(get_logger), log_ws_manager: WebSocketConnectionManager=Depends(get_log_ws_manager)):
    if not log_ws_manager:
        await websocket.close(code=1011)
        return
    client_id = str(uuid.uuid4())
    try:
        await log_ws_manager.connect(client_id, websocket)
        while True:
            await websocket.receive_text()
    except WebSocketDisconnect:
        pass
    except Exception as e:
        logger.error(f'日志 WebSocket (客户端 {client_id}) 发生异常: {e}', exc_info=True)
    finally:
        log_ws_manager.disconnect(client_id)

class ApiKeyRequest(BaseModel):
    key: str

class ApiKeyTestRequest(BaseModel):
    key: str

async def get_api_keys(logger: logging.Logger=Depends(get_logger)):
    from api import auth_utils
    try:
        auth_utils.initialize_keys()
        keys_info = [{'value': key, 'status': '有效'} for key in auth_utils.API_KEYS]
        return JSONResponse(content={'success': True, 'keys': keys_info, 'total_count': len(keys_info)})
    except Exception as e:
        logger.error(f'获取API密钥列表失败: {e}')
        raise HTTPException(status_code=500, detail=str(e))

async def add_api_key(request: ApiKeyRequest, logger: logging.Logger=Depends(get_logger)):
    from api import auth_utils
    key_value = request.key.strip()
    if not key_value or len(key_value) < 8:
        raise HTTPException(status_code=400, detail='无效的API密钥格式。')
    auth_utils.initialize_keys()
    if key_value in auth_utils.API_KEYS:
        raise HTTPException(status_code=400, detail='该API密钥已存在。')
    try:
        script_dir = os.path.dirname(os.path.abspath(__file__))
        project_root = os.path.dirname(os.path.dirname(script_dir))
        key_file_path = os.path.join(project_root, 'data', 'key.txt')
        with open(key_file_path, 'a+', encoding='utf-8') as f:
            f.seek(0)
            if f.read():
                f.write('\n')
            f.write(key_value)
        auth_utils.initialize_keys()
        logger.info(f'API密钥已添加: {key_value[:4]}...{key_value[-4:]}')
        return JSONResponse(content={'success': True, 'message': 'API密钥添加成功', 'key_count': len(auth_utils.API_KEYS)})
    except Exception as e:
        logger.error(f'添加API密钥失败: {e}')
        raise HTTPException(status_code=500, detail=str(e))

async def test_api_key(request: ApiKeyTestRequest, logger: logging.Logger=Depends(get_logger)):
    from api import auth_utils
    key_value = request.key.strip()
    if not key_value:
        raise HTTPException(status_code=400, detail='API密钥不能为空。')
    auth_utils.initialize_keys()
    is_valid = auth_utils.verify_api_key(key_value)
    logger.info(f"API密钥测试: {key_value[:4]}...{key_value[-4:]} - {('有效' if is_valid else '无效')}")
    return JSONResponse(content={'success': True, 'valid': is_valid, 'message': '密钥有效' if is_valid else '密钥无效或不存在'})

async def delete_api_key(request: ApiKeyRequest, logger: logging.Logger=Depends(get_logger)):
    from api import auth_utils
    key_value = request.key.strip()
    if not key_value:
        raise HTTPException(status_code=400, detail='API密钥不能为空。')
    auth_utils.initialize_keys()
    if key_value not in auth_utils.API_KEYS:
        raise HTTPException(status_code=404, detail='API密钥不存在。')
    try:
        script_dir = os.path.dirname(os.path.abspath(__file__))
        project_root = os.path.dirname(os.path.dirname(script_dir))
        key_file_path = os.path.join(project_root, 'data', 'key.txt')
        with open(key_file_path, 'r', encoding='utf-8') as f:
            lines = f.readlines()
        with open(key_file_path, 'w', encoding='utf-8') as f:
            f.writelines((line for line in lines if line.strip() != key_value))
        auth_utils.initialize_keys()
        logger.info(f'API密钥已删除: {key_value[:4]}...{key_value[-4:]}')
        return JSONResponse(content={'success': True, 'message': 'API密钥删除成功', 'key_count': len(auth_utils.API_KEYS)})
    except Exception as e:
        logger.error(f'删除API密钥失败: {e}')
        raise HTTPException(status_code=500, detail=str(e))


class GenerateSpeechRequest(BaseModel):
    model: str = 'gemini-2.5-flash-preview-tts'
    contents: Any = None
    generationConfig: Optional[Dict[str, Any]] = None
    generation_config: Optional[Dict[str, Any]] = None

    class Config:
        extra = 'allow'


async def generate_speech(request: GenerateSpeechRequest, http_request: Request, logger: logging.Logger=Depends(get_logger), request_queue: Queue=Depends(get_request_queue), server_state: Dict[str, Any]=Depends(get_server_state), worker_task=Depends(get_worker_task), page_instance: AsyncPage=Depends(get_page_instance)):
    req_id = ''.join(random.choices('abcdefghijklmnopqrstuvwxyz0123456789', k=7))
    logger.info(f'[{req_id}] 🎤 收到 TTS 请求 | Model: {request.model}')
    
    launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
    browser_page_critical = launch_mode != 'direct_debug_no_browser'
    service_unavailable = server_state['is_initializing'] or not server_state['is_playwright_ready'] or (browser_page_critical and (not server_state['is_page_ready'] or not server_state['is_browser_connected'])) or (not worker_task) or worker_task.done()
    
    if service_unavailable:
        raise HTTPException(status_code=503, detail=f'[{req_id}] 服务当前不可用。请稍后重试。', headers={'Retry-After': '30'})
    
    if not page_instance or page_instance.is_closed():
        raise HTTPException(status_code=503, detail=f'[{req_id}] 浏览器页面不可用。')
    
    from tts import process_tts_request
    from models import ClientDisconnectedError
    
    def check_client_disconnected(stage: str = '') -> bool:
        return False
    
    try:
        request_data = request.model_dump()
        if request.generation_config and not request.generationConfig:
            request_data['generationConfig'] = request.generation_config
        
        result = await process_tts_request(
            req_id=req_id,
            page=page_instance,
            logger=logger,
            request_data=request_data,
            check_client_disconnected=check_client_disconnected
        )
        return JSONResponse(content=result)
    except ClientDisconnectedError as e:
        logger.warning(f'[{req_id}] 客户端断开: {e}')
        raise HTTPException(status_code=499, detail=str(e))
    except TimeoutError as e:
        logger.error(f'[{req_id}] TTS 生成超时: {e}')
        raise HTTPException(status_code=504, detail=f'[{req_id}] TTS 生成超时: {e}')
    except Exception as e:
        logger.exception(f'[{req_id}] TTS 处理错误')
        raise HTTPException(status_code=500, detail=f'[{req_id}] TTS 处理错误: {e}')
