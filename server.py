import asyncio
import multiprocessing
import random
import time
import json
from typing import List, Optional, Dict, Any, Union, AsyncGenerator, Tuple, Callable, Set
import os
import traceback
from contextlib import asynccontextmanager
import sys
import platform
import logging
import logging.handlers
import socket # 保留 socket 以便在 __main__ 中进行简单的直接运行提示
from asyncio import Queue, Lock, Future, Task, Event

# 新增: 导入 load_dotenv
from dotenv import load_dotenv

# 新增: 在所有其他导入之前加载 .env 文件
load_dotenv()

from fastapi import FastAPI, Request, HTTPException
from fastapi.responses import JSONResponse, StreamingResponse, FileResponse
from fastapi import WebSocket, WebSocketDisconnect
from pydantic import BaseModel
from playwright.async_api import Page as AsyncPage, Browser as AsyncBrowser, Playwright as AsyncPlaywright, Error as PlaywrightAsyncError, expect as expect_async, BrowserContext as AsyncBrowserContext, Locator, TimeoutError
from playwright.async_api import async_playwright
from urllib.parse import urljoin, urlparse
import uuid
import datetime
import aiohttp
import stream
import queue

# --- 配置模块导入 ---
from config import *

# --- models模块导入 ---
from models import (
    FunctionCall,
    ToolCall,
    MessageContentItem, 
    Message,
    ChatCompletionRequest,
    ClientDisconnectedError,
    StreamToLogger,
    WebSocketConnectionManager,
    WebSocketLogHandler
)

# --- logging_utils模块导入 ---
from logging_utils import setup_server_logging, restore_original_streams

# --- browser_utils模块导入 ---
from browser_utils import (
    _initialize_page_logic,
    _close_page_logic,
    signal_camoufox_shutdown,
    _handle_model_list_response,
    detect_and_extract_page_error,
    save_error_snapshot,
    get_response_via_edit_button,
    get_response_via_copy_button,
    _wait_for_response_completion,
    _get_final_response_content,
    get_raw_text_content,
    switch_ai_studio_model,
    load_excluded_models,
    _handle_initial_model_state_and_storage,
    _set_model_from_page_display
)

# --- api_utils模块导入 ---
from api_utils import (
    generate_sse_chunk,
    generate_sse_stop_chunk, 
    generate_sse_error_chunk,
    use_helper_get_response,
    use_stream_response,
    clear_stream_queue,
    prepare_combined_prompt,
    validate_chat_request,
    _process_request_refactored,
    create_app,
    queue_worker
)

# --- stream queue ---
STREAM_QUEUE:Optional[multiprocessing.Queue] = None
STREAM_PROCESS = None

# --- Global State ---
playwright_manager: Optional[AsyncPlaywright] = None
browser_instance: Optional[AsyncBrowser] = None
page_instance: Optional[AsyncPage] = None
is_playwright_ready = False
is_browser_connected = False
is_page_ready = False
is_initializing = False

# --- 全局代理配置 ---
PLAYWRIGHT_PROXY_SETTINGS: Optional[Dict[str, str]] = None

global_model_list_raw_json: Optional[List[Any]] = None
parsed_model_list: List[Dict[str, Any]] = []
model_list_fetch_event = asyncio.Event()

current_ai_studio_model_id: Optional[str] = None
model_switching_lock: Optional[Lock] = None

excluded_model_ids: Set[str] = set()

request_queue: Optional[Queue] = None
processing_lock: Optional[Lock] = None
worker_task: Optional[Task] = None

page_params_cache: Dict[str, Any] = {}
params_cache_lock: Optional[Lock] = None

logger = logging.getLogger("AIStudioProxyServer")
log_ws_manager = None


# --- FastAPI App 定义 ---
app = create_app()

# --- Main Guard ---
if __name__ == "__main__":
    import uvicorn
    port = int(os.environ.get("PORT", 2048))
    uvicorn.run(
        "server:app",
        host="0.0.0.0",
        port=port,
        log_level="info",
        access_log=False
    )