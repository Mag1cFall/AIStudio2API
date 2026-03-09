from .auth import router as auth_router
from .config import router as config_router
from .control import router as control_router
from .gateway import router as gateway_router
from .root import router as root_router
from .system import router as system_router
from .websocket import router as websocket_router
from .workers import router as workers_router

__all__ = [
    "auth_router",
    "config_router",
    "control_router",
    "gateway_router",
    "root_router",
    "system_router",
    "websocket_router",
    "workers_router",
]
