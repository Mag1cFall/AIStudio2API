import asyncio
import multiprocessing
import os
import sys
from contextlib import asynccontextmanager
from typing import Optional
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.types import ASGIApp
from typing import Callable, Awaitable
from playwright.async_api import Browser as AsyncBrowser, Playwright as AsyncPlaywright
from config import *
from models import WebSocketConnectionManager
from logging_utils import setup_server_logging, restore_original_streams
from browser_utils import _initialize_page_logic, _close_page_logic, load_excluded_models, _handle_initial_model_state_and_storage
import stream
from asyncio import Queue, Lock
from . import auth_utils
playwright_manager: Optional[AsyncPlaywright] = None
browser_instance: Optional[AsyncBrowser] = None
page_instance = None
is_playwright_ready = False
is_browser_connected = False
is_page_ready = False
is_initializing = False
global_model_list_raw_json = None
parsed_model_list = []
model_list_fetch_event = None
current_ai_studio_model_id = None
model_switching_lock = None
excluded_model_ids = set()
request_queue = None
processing_lock = None
worker_task = None
page_params_cache = {}
params_cache_lock = None
log_ws_manager = None
STREAM_QUEUE = None
STREAM_PROCESS = None

def _setup_logging():
    import server
    log_level_env = os.environ.get('SERVER_LOG_LEVEL', 'INFO')
    redirect_print_env = os.environ.get('SERVER_REDIRECT_PRINT', 'false')
    server.log_ws_manager = WebSocketConnectionManager()
    return setup_server_logging(logger_instance=server.logger, log_ws_manager=server.log_ws_manager, log_level_name=log_level_env, redirect_print_str=redirect_print_env)

def _initialize_globals():
    import server
    server.request_queue = Queue()
    server.processing_lock = Lock()
    server.model_switching_lock = Lock()
    server.params_cache_lock = Lock()
    auth_utils.initialize_keys()
    server.logger.info('API keys and global locks initialized.')

def _initialize_proxy_settings():
    import server
    STREAM_PORT = os.environ.get('STREAM_PORT')
    if STREAM_PORT == '0':
        PROXY_SERVER_ENV = os.environ.get('HTTPS_PROXY') or os.environ.get('HTTP_PROXY')
    else:
        PROXY_SERVER_ENV = f'http://127.0.0.1:{STREAM_PORT or 3120}/'
    if PROXY_SERVER_ENV:
        server.PLAYWRIGHT_PROXY_SETTINGS = {'server': PROXY_SERVER_ENV}
        if NO_PROXY_ENV:
            server.PLAYWRIGHT_PROXY_SETTINGS['bypass'] = NO_PROXY_ENV.replace(',', ';')
        server.logger.info(f'Playwright proxy settings configured: {server.PLAYWRIGHT_PROXY_SETTINGS}')
    else:
        server.logger.info('No proxy configured for Playwright.')

async def _start_stream_proxy():
    import server
    STREAM_PORT = os.environ.get('STREAM_PORT')
    if STREAM_PORT != '0':
        port = int(STREAM_PORT or 3120)
        STREAM_PROXY_SERVER_ENV = os.environ.get('UNIFIED_PROXY_CONFIG') or os.environ.get('HTTPS_PROXY') or os.environ.get('HTTP_PROXY')
        server.logger.info(f'Starting STREAM proxy on port {port} with upstream proxy: {STREAM_PROXY_SERVER_ENV}')
        server.STREAM_QUEUE = multiprocessing.Queue()
        server.STREAM_PROCESS = multiprocessing.Process(target=stream.start, args=(server.STREAM_QUEUE, port, STREAM_PROXY_SERVER_ENV))
        server.STREAM_PROCESS.start()
        server.logger.info('STREAM proxy process started.')

async def _initialize_browser_and_page():
    import server
    from playwright.async_api import async_playwright
    server.logger.info('Starting Playwright...')
    server.playwright_manager = await async_playwright().start()
    server.is_playwright_ready = True
    server.logger.info('Playwright started.')
    ws_endpoint = os.environ.get('CAMOUFOX_WS_ENDPOINT')
    launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
    if not ws_endpoint and launch_mode != 'direct_debug_no_browser':
        raise ValueError('CAMOUFOX_WS_ENDPOINT environment variable is missing.')
    if ws_endpoint:
        server.logger.info(f'Connecting to browser at: {ws_endpoint}')
        server.browser_instance = await server.playwright_manager.firefox.connect(ws_endpoint, timeout=30000)
        server.is_browser_connected = True
        server.logger.info(f'Connected to browser: {server.browser_instance.version}')
        server.page_instance, server.is_page_ready = await _initialize_page_logic(server.browser_instance)
        if server.is_page_ready:
            await _handle_initial_model_state_and_storage(server.page_instance)
            server.logger.info('Page initialized successfully.')
        else:
            server.logger.error('Page initialization failed.')
    if not server.model_list_fetch_event.is_set():
        server.model_list_fetch_event.set()

async def _shutdown_resources():
    import server
    logger = server.logger
    logger.info('Shutting down resources...')
    if server.STREAM_PROCESS:
        server.STREAM_PROCESS.terminate()
        logger.info('STREAM proxy terminated.')
    if server.worker_task and (not server.worker_task.done()):
        server.worker_task.cancel()
        try:
            await asyncio.wait_for(server.worker_task, timeout=5.0)
        except (asyncio.TimeoutError, asyncio.CancelledError):
            pass
        logger.info('Worker task stopped.')
    if server.page_instance:
        await _close_page_logic()
    if server.browser_instance and server.browser_instance.is_connected():
        await server.browser_instance.close()
        logger.info('Browser connection closed.')
    if server.playwright_manager:
        await server.playwright_manager.stop()
        logger.info('Playwright stopped.')

@asynccontextmanager
async def lifespan(app: FastAPI):
    """FastAPI application life cycle management"""
    import server
    from server import queue_worker
    original_streams = (sys.stdout, sys.stderr)
    initial_stdout, initial_stderr = _setup_logging()
    logger = server.logger
    _initialize_globals()
    _initialize_proxy_settings()
    load_excluded_models(EXCLUDED_MODELS_FILENAME)
    server.is_initializing = True
    logger.info('Starting AI Studio Proxy Server...')
    try:
        await _start_stream_proxy()
        await _initialize_browser_and_page()
        launch_mode = os.environ.get('LAUNCH_MODE', 'unknown')
        if server.is_page_ready or launch_mode == 'direct_debug_no_browser':
            server.worker_task = asyncio.create_task(queue_worker())
            logger.info('Request processing worker started.')
        else:
            raise RuntimeError('Failed to initialize browser/page, worker not started.')
        logger.info('Server startup complete.')
        server.is_initializing = False
        yield
    except Exception as e:
        logger.critical(f'Application startup failed: {e}', exc_info=True)
        await _shutdown_resources()
        raise RuntimeError(f'Application startup failed: {e}') from e
    finally:
        logger.info('Shutting down server...')
        await _shutdown_resources()
        restore_original_streams(initial_stdout, initial_stderr)
        restore_original_streams(*original_streams)
        logger.info('Server shutdown complete.')

class APIKeyAuthMiddleware(BaseHTTPMiddleware):

    def __init__(self, app: ASGIApp):
        super().__init__(app)
        self.excluded_paths = ['/v1/models', '/health', '/docs', '/openapi.json', '/redoc', '/favicon.ico']

    async def dispatch(self, request: Request, call_next: Callable[[Request], Awaitable]):
        if not auth_utils.API_KEYS:
            return await call_next(request)
        if not request.url.path.startswith('/v1/'):
            return await call_next(request)
        for excluded_path in self.excluded_paths:
            if request.url.path == excluded_path or request.url.path.startswith(excluded_path + '/'):
                return await call_next(request)
        api_key = None
        auth_header = request.headers.get('Authorization')
        if auth_header and auth_header.startswith('Bearer '):
            api_key = auth_header[7:]
        if not api_key:
            api_key = request.headers.get('X-API-Key')
        if not api_key or not auth_utils.verify_api_key(api_key):
            return JSONResponse(status_code=401, content={'error': {'message': "Invalid or missing API key. Please provide a valid API key using 'Authorization: Bearer <your_key>' or 'X-API-Key: <your_key>' header.", 'type': 'invalid_request_error', 'param': None, 'code': 'invalid_api_key'}})
        return await call_next(request)

def create_app() -> FastAPI:
    app = FastAPI(title='AI Studio Proxy Server (集成模式)', description='通过 Playwright与 AI Studio 交互的代理服务器。', version='0.6.0-integrated', lifespan=lifespan)
    
    # 添加 CORS 支持，允许前端 Playground (端口9000) 访问 API (端口2048)
    from fastapi.middleware.cors import CORSMiddleware
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )
    
    app.add_middleware(APIKeyAuthMiddleware)
    from .routes import get_api_info, health_check, list_models, chat_completions, cancel_request, get_queue_status, websocket_log_endpoint, get_api_keys, add_api_key, test_api_key, delete_api_key
    from fastapi.responses import FileResponse
    app.get('/api/info')(get_api_info)
    app.get('/health')(health_check)
    app.get('/v1/models')(list_models)
    app.post('/v1/chat/completions')(chat_completions)
    app.post('/v1/cancel/{req_id}')(cancel_request)
    app.get('/v1/queue')(get_queue_status)
    app.websocket('/ws/logs')(websocket_log_endpoint)
    app.get('/api/keys')(get_api_keys)
    app.post('/api/keys')(add_api_key)
    app.post('/api/keys/test')(test_api_key)
    app.delete('/api/keys')(delete_api_key)
    return app