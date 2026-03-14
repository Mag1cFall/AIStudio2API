import os

from fastapi import APIRouter
from fastapi.responses import FileResponse

try:
    from ...config.settings import PROJECT_ROOT
except ImportError:
    from config.settings import PROJECT_ROOT


router = APIRouter()

STATIC_DIR = os.path.join(PROJECT_ROOT, "src", "static")


@router.get("/")
async def read_root():
    return FileResponse(os.path.join(STATIC_DIR, "dashboard.html"))
