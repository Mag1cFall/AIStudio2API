"""
FastAPI路由处理器模块
包含所有API端点的处理函数
"""

import asyncio
import os
import random
import time
import uuid
from typing import Dict, List, Any, Set
from asyncio import Queue, Future, Lock, Event
import logging

from fastapi import HTTPException, Request, WebSocket, WebSocketDisconnect, Depends
from fastapi.responses import JSONResponse, FileResponse
from pydantic import BaseModel
from playwright.async_api import Page as AsyncPage

# --- 配置模块导入 ---
from config import *

# --- models模块导入 ---
from models import ChatCompletionRequest, WebSocketConnectionManager

# --- browser_utils模块导入 ---
from browser_utils import _handle_model_list_response

# --- 依赖项导入 ---
from .dependencies import *


# --- 静态文件端点 ---
async def read_index(logger: logging.Logger = Depends(get_logger)):
    """返回主页面"""
    index_html_path = os.path.join(os.path.dirname(__file__), "..", "index.html")
    if not os.path.exists(index_html_path):
        logger.error(f"index.html not found at {index_html_path}")
        raise HTTPException(status_code=404, detail="index.html not found")
    return FileResponse(index_html_path)


async def get_css(logger: logging.Logger = Depends(get_logger)):
    """返回CSS文件"""
    css_path = os.path.join(os.path.dirname(__file__), "..", "webui.css")
    if not os.path.exists(css_path):
        logger.error(f"webui.css not found at {css_path}")
        raise HTTPException(status_code=404, detail="webui.css not found")
    return FileResponse(css_path, media_type="text/css")


async def get_js(logger: logging.Logger = Depends(get_logger)):
    """返回JavaScript文件"""
    js_path = os.path.join(os.path.dirname(__file__), "..", "webui.js")
    if not os.path.exists(js_path):
        logger.error(f"webui.js not found at {js_path}")
        raise HTTPException(status_code=404, detail="webui.js not found")
    return FileResponse(js_path, media_type="application/javascript")


# --- API信息端点 ---
async def get_api_info(request: Request, current_ai_studio_model_id: str = Depends(get_current_ai_studio_model_id)):
    """返回API信息"""
    from api_utils import auth_utils

    server_port = request.url.port or os.environ.get('SERVER_PORT_INFO', '8000')
    host = request.headers.get('host') or f"127.0.0.1:{server_port}"
    scheme = request.headers.get('x-forwarded-proto', 'http')
    base_url = f"{scheme}://{host}"
    api_base = f"{base_url}/v1"
    effective_model_name = current_ai_studio_model_id or MODEL_NAME

    api_key_required = bool(auth_utils.API_KEYS)
    api_key_count = len(auth_utils.API_KEYS)

    if api_key_required:
        message = f"API Key is required. {api_key_count} valid key(s) configured."
    else:
        message = "API Key is not required."

    return JSONResponse(content={
        "model_name": effective_model_name,
        "api_base_url": api_base,
        "server_base_url": base_url,
        "api_key_required": api_key_required,
        "api_key_count": api_key_count,
        "auth_header": "Authorization: Bearer <token> or X-API-Key: <token>" if api_key_required else None,
        "openai_compatible": True,
        "supported_auth_methods": ["Authorization: Bearer", "X-API-Key"] if api_key_required else [],
        "message": message
    })


# --- 健康检查端点 ---
async def health_check(
    server_state: Dict[str, Any] = Depends(get_server_state),
    worker_task = Depends(get_worker_task),
    request_queue: Queue = Depends(get_request_queue)
):
    """健康检查"""
    is_worker_running = bool(worker_task and not worker_task.done())
    launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
    browser_page_critical = launch_mode != "direct_debug_no_browser"
    
    core_ready_conditions = [not server_state["is_initializing"], server_state["is_playwright_ready"]]
    if browser_page_critical:
        core_ready_conditions.extend([server_state["is_browser_connected"], server_state["is_page_ready"]])
    
    is_core_ready = all(core_ready_conditions)
    status_val = "OK" if is_core_ready and is_worker_running else "Error"
    q_size = request_queue.qsize() if request_queue else -1
    
    status_message_parts = []
    if server_state["is_initializing"]: status_message_parts.append("初始化进行中")
    if not server_state["is_playwright_ready"]: status_message_parts.append("Playwright 未就绪")
    if browser_page_critical:
        if not server_state["is_browser_connected"]: status_message_parts.append("浏览器未连接")
        if not server_state["is_page_ready"]: status_message_parts.append("页面未就绪")
    if not is_worker_running: status_message_parts.append("Worker 未运行")
    
    status = {
        "status": status_val,
        "message": "",
        "details": {**server_state, "workerRunning": is_worker_running, "queueLength": q_size, "launchMode": launch_mode, "browserAndPageCritical": browser_page_critical}
    }
    
    if status_val == "OK":
        status["message"] = f"服务运行中;队列长度: {q_size}。"
        return JSONResponse(content=status, status_code=200)
    else:
        status["message"] = f"服务不可用;问题: {(', '.join(status_message_parts) or '未知原因')}. 队列长度: {q_size}."
        return JSONResponse(content=status, status_code=503)


# --- 模型列表端点 ---
async def list_models(
    logger: logging.Logger = Depends(get_logger),
    model_list_fetch_event: Event = Depends(get_model_list_fetch_event),
    page_instance: AsyncPage = Depends(get_page_instance),
    parsed_model_list: List[Dict[str, Any]] = Depends(get_parsed_model_list),
    excluded_model_ids: Set[str] = Depends(get_excluded_model_ids)
):
    """获取模型列表"""
    logger.info("[API] 收到 /v1/models 请求。")
    
    if not model_list_fetch_event.is_set() and page_instance and not page_instance.is_closed():
        logger.info("/v1/models: 模型列表事件未设置，尝试刷新页面...")
        try:
            await page_instance.reload(wait_until="domcontentloaded", timeout=20000)
            await asyncio.wait_for(model_list_fetch_event.wait(), timeout=10.0)
        except Exception as e:
            logger.error(f"/v1/models: 刷新或等待模型列表时出错: {e}")
        finally:
            if not model_list_fetch_event.is_set():
                model_list_fetch_event.set()
    
    if parsed_model_list:
        final_model_list = [m for m in parsed_model_list if m.get("id") not in excluded_model_ids]
        return {"object": "list", "data": final_model_list}
    else:
        logger.warning("模型列表为空，返回默认后备模型。")
        return {"object": "list", "data": [{
            "id": DEFAULT_FALLBACK_MODEL_ID, "object": "model", "created": int(time.time()),
            "owned_by": "camoufox-proxy-fallback"
        }]}


# --- 聊天完成端点 ---
async def chat_completions(
    request: ChatCompletionRequest,
    http_request: Request,
    logger: logging.Logger = Depends(get_logger),
    request_queue: Queue = Depends(get_request_queue),
    server_state: Dict[str, Any] = Depends(get_server_state),
    worker_task = Depends(get_worker_task)
):
    """处理聊天完成请求"""
    req_id = ''.join(random.choices('abcdefghijklmnopqrstuvwxyz0123456789', k=7))
    logger.info(f"[{req_id}] 收到 /v1/chat/completions 请求 (Stream={request.stream})")
    
    launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
    browser_page_critical = launch_mode != "direct_debug_no_browser"
    
    service_unavailable = server_state["is_initializing"] or \
                          not server_state["is_playwright_ready"] or \
                          (browser_page_critical and (not server_state["is_page_ready"] or not server_state["is_browser_connected"])) or \
                          not worker_task or worker_task.done()
    
    if service_unavailable:
        raise HTTPException(status_code=503, detail=f"[{req_id}] 服务当前不可用。请稍后重试。", headers={"Retry-After": "30"})
    
    result_future = Future()
    await request_queue.put({
        "req_id": req_id, "request_data": request, "http_request": http_request,
        "result_future": result_future, "enqueue_time": time.time(), "cancelled": False
    })
    
    try:
        timeout_seconds = RESPONSE_COMPLETION_TIMEOUT / 1000 + 120
        return await asyncio.wait_for(result_future, timeout=timeout_seconds)
    except asyncio.TimeoutError:
        raise HTTPException(status_code=504, detail=f"[{req_id}] 请求处理超时。")
    except asyncio.CancelledError:
        raise HTTPException(status_code=499, detail=f"[{req_id}] 请求被客户端取消。")
    except HTTPException as http_exc:
        # 对于客户端断开连接的情况，使用更友好的日志级别
        if http_exc.status_code == 499:
            logger.info(f"[{req_id}] 客户端断开连接: {http_exc.detail}")
        else:
            logger.warning(f"[{req_id}] HTTP异常: {http_exc.detail}")
        raise http_exc
    except Exception as e:
        logger.exception(f"[{req_id}] 等待Worker响应时出错")
        raise HTTPException(status_code=500, detail=f"[{req_id}] 服务器内部错误: {e}")


# --- 取消请求相关 ---
async def cancel_queued_request(req_id: str, request_queue: Queue, logger: logging.Logger) -> bool:
    """取消队列中的请求"""
    items_to_requeue = []
    found = False
    try:
        while not request_queue.empty():
            item = request_queue.get_nowait()
            if item.get("req_id") == req_id:
                logger.info(f"[{req_id}] 在队列中找到请求，标记为已取消。")
                item["cancelled"] = True
                if (future := item.get("result_future")) and not future.done():
                    future.set_exception(HTTPException(status_code=499, detail=f"[{req_id}] Request cancelled."))
                found = True
            items_to_requeue.append(item)
    finally:
        for item in items_to_requeue:
            await request_queue.put(item)
    return found


async def cancel_request(
    req_id: str,
    logger: logging.Logger = Depends(get_logger),
    request_queue: Queue = Depends(get_request_queue)
):
    """取消请求端点 - 支持取消队列中和正在处理的请求"""
    from api_utils.utils import request_manager
    
    logger.info(f"[{req_id}] 收到取消请求。")
    
    # 首先尝试取消正在处理的请求
    if request_manager.cancel_request(req_id):
        logger.info(f"[{req_id}] 正在处理的请求已标记为取消。")
        return JSONResponse(content={
            "success": True,
            "message": f"Active request {req_id} marked as cancelled.",
            "type": "active_request"
        })
    
    # 如果没有找到活动请求，尝试取消队列中的请求
    if await cancel_queued_request(req_id, request_queue, logger):
        return JSONResponse(content={
            "success": True,
            "message": f"Queued request {req_id} marked as cancelled.",
            "type": "queued_request"
        })
    else:
        return JSONResponse(status_code=404, content={
            "success": False,
            "message": f"Request {req_id} not found in queue or active requests.",
            "type": "not_found"
        })


# --- 队列状态端点 ---
async def get_queue_status(
    request_queue: Queue = Depends(get_request_queue),
    processing_lock: Lock = Depends(get_processing_lock)
):
    """获取队列状态"""
    from api_utils.utils import request_manager
    
    queue_items = list(request_queue._queue)
    active_requests = request_manager.get_active_requests()
    
    return JSONResponse(content={
        "queue_length": len(queue_items),
        "active_requests_count": len(active_requests),
        "is_processing_locked": processing_lock.locked(),
        "queued_items": sorted([
            {
                "req_id": item.get("req_id", "unknown"),
                "enqueue_time": item.get("enqueue_time", 0),
                "wait_time_seconds": round(time.time() - item.get("enqueue_time", 0), 2),
                "is_streaming": item.get("request_data").stream,
                "cancelled": item.get("cancelled", False)
            } for item in queue_items
        ], key=lambda x: x.get("enqueue_time", 0)),
        "active_items": sorted(active_requests, key=lambda x: x.get("duration", 0), reverse=True)
    })


# --- WebSocket日志端点 ---
async def websocket_log_endpoint(
    websocket: WebSocket,
    logger: logging.Logger = Depends(get_logger),
    log_ws_manager: WebSocketConnectionManager = Depends(get_log_ws_manager)
):
    """WebSocket日志端点"""
    if not log_ws_manager:
        await websocket.close(code=1011)
        return
    
    client_id = str(uuid.uuid4())
    try:
        await log_ws_manager.connect(client_id, websocket)
        while True:
            await websocket.receive_text() # Keep connection alive
    except WebSocketDisconnect:
        pass
    except Exception as e:
        logger.error(f"日志 WebSocket (客户端 {client_id}) 发生异常: {e}", exc_info=True)
    finally:
        log_ws_manager.disconnect(client_id)


# --- API密钥管理数据模型 ---
class ApiKeyRequest(BaseModel):
    key: str

class ApiKeyTestRequest(BaseModel):
    key: str


# --- API密钥管理端点 ---
async def get_api_keys(logger: logging.Logger = Depends(get_logger)):
    """获取API密钥列表"""
    from api_utils import auth_utils
    try:
        auth_utils.initialize_keys()
        keys_info = [{"value": key, "status": "有效"} for key in auth_utils.API_KEYS]
        return JSONResponse(content={"success": True, "keys": keys_info, "total_count": len(keys_info)})
    except Exception as e:
        logger.error(f"获取API密钥列表失败: {e}")
        raise HTTPException(status_code=500, detail=str(e))


async def add_api_key(request: ApiKeyRequest, logger: logging.Logger = Depends(get_logger)):
    """添加API密钥"""
    from api_utils import auth_utils
    key_value = request.key.strip()
    if not key_value or len(key_value) < 8:
        raise HTTPException(status_code=400, detail="无效的API密钥格式。")
    
    auth_utils.initialize_keys()
    if key_value in auth_utils.API_KEYS:
        raise HTTPException(status_code=400, detail="该API密钥已存在。")

    try:
        key_file_path = os.path.join(os.path.dirname(__file__), "..", "key.txt")
        with open(key_file_path, 'a+', encoding='utf-8') as f:
            f.seek(0)
            if f.read(): f.write("\n")
            f.write(key_value)
        
        auth_utils.initialize_keys()
        logger.info(f"API密钥已添加: {key_value[:4]}...{key_value[-4:]}")
        return JSONResponse(content={"success": True, "message": "API密钥添加成功", "key_count": len(auth_utils.API_KEYS)})
    except Exception as e:
        logger.error(f"添加API密钥失败: {e}")
        raise HTTPException(status_code=500, detail=str(e))


async def test_api_key(request: ApiKeyTestRequest, logger: logging.Logger = Depends(get_logger)):
    """测试API密钥"""
    from api_utils import auth_utils
    key_value = request.key.strip()
    if not key_value:
        raise HTTPException(status_code=400, detail="API密钥不能为空。")
    
    auth_utils.initialize_keys()
    is_valid = auth_utils.verify_api_key(key_value)
    logger.info(f"API密钥测试: {key_value[:4]}...{key_value[-4:]} - {'有效' if is_valid else '无效'}")
    return JSONResponse(content={"success": True, "valid": is_valid, "message": "密钥有效" if is_valid else "密钥无效或不存在"})


async def delete_api_key(request: ApiKeyRequest, logger: logging.Logger = Depends(get_logger)):
    """删除API密钥"""
    from api_utils import auth_utils
    key_value = request.key.strip()
    if not key_value:
        raise HTTPException(status_code=400, detail="API密钥不能为空。")

    auth_utils.initialize_keys()
    if key_value not in auth_utils.API_KEYS:
        raise HTTPException(status_code=404, detail="API密钥不存在。")

    try:
        key_file_path = os.path.join(os.path.dirname(__file__), "..", "key.txt")
        with open(key_file_path, 'r', encoding='utf-8') as f:
            lines = f.readlines()
        
        with open(key_file_path, 'w', encoding='utf-8') as f:
            f.writelines(line for line in lines if line.strip() != key_value)
            
        auth_utils.initialize_keys()
        logger.info(f"API密钥已删除: {key_value[:4]}...{key_value[-4:]}")
        return JSONResponse(content={"success": True, "message": "API密钥删除成功", "key_count": len(auth_utils.API_KEYS)})
    except Exception as e:
        logger.error(f"删除API密钥失败: {e}")
        raise HTTPException(status_code=500, detail=str(e))