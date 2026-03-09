import asyncio
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles

try:
    from ..config.settings import DATA_DIR, PROJECT_ROOT
except ImportError:
    from config.settings import DATA_DIR, PROJECT_ROOT

from .routes import (
    auth_router,
    config_router,
    control_router,
    gateway_router,
    root_router,
    system_router,
    websocket_router,
    workers_router,
)
from .service import WORKER_POOL_AVAILABLE, manager, worker_pool

STATIC_DIR = os.path.join(PROJECT_ROOT, "src", "static")


@asynccontextmanager
async def lifespan(app: FastAPI):
    manager.loop = asyncio.get_running_loop()
    config = manager.load_config()
    manager._log_enabled = config.get("log_enabled", True)
    if WORKER_POOL_AVAILABLE and worker_pool is not None:
        worker_pool.init_from_config()
    yield
    if manager.process or manager.worker_processes:
        manager.stop_service()
    manager.loop = None


def create_app() -> FastAPI:
    os.makedirs(DATA_DIR, exist_ok=True)
    app = FastAPI(lifespan=lifespan)

    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")
    app.include_router(root_router)
    app.include_router(config_router)
    app.include_router(control_router)
    app.include_router(system_router)
    app.include_router(auth_router)
    app.include_router(workers_router)
    app.include_router(gateway_router)
    app.include_router(websocket_router)
    return app


app = create_app()
