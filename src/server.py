import asyncio
import multiprocessing
import os
import logging
from typing import List, Optional, Dict, Any, Set
from asyncio import Queue, Lock, Task, Event
from dotenv import load_dotenv
load_dotenv()
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles
from playwright.async_api import Page as AsyncPage, Browser as AsyncBrowser, Playwright as AsyncPlaywright
from config import *
from models import WebSocketConnectionManager
from api import create_app, queue_worker

STREAM_QUEUE: Optional[multiprocessing.Queue] = None
STREAM_PROCESS = None
STREAM_PORT_ACTUAL: Optional[int] = None
playwright_manager: Optional[AsyncPlaywright] = None
browser_instance: Optional[AsyncBrowser] = None
page_instance: Optional[AsyncPage] = None
is_playwright_ready = False
is_browser_connected = False
is_page_ready = False
is_initializing = False
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
logger = logging.getLogger('AIStudioProxyServer')
log_ws_manager: Optional[WebSocketConnectionManager] = None
app = create_app()

STATIC_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'static')
os.makedirs(STATIC_DIR, exist_ok=True)
app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")

@app.get("/")
async def read_root():
    return JSONResponse(content={
        "status": "running",
        "service": "AI Studio Proxy API",
        "message": "Service is running normally. Please use the AI Studio Manager for control and logs."
    })

if __name__ == '__main__':
    import uvicorn
    port = int(os.environ.get('PORT', 2048))
    logger.info(f"✨ Dashboard available at: http://localhost:{port}")
    uvicorn.run('server:app', host='0.0.0.0', port=port, log_level='info', access_log=False)
