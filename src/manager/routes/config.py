from typing import Any, Dict

from fastapi import APIRouter, Body, HTTPException

from ..service import manager


router = APIRouter(prefix="/api/config", tags=["Config"])


@router.get("")
async def get_config():
    return manager.load_config()


@router.post("")
async def save_config(config: Dict[str, Any] = Body(...)):
    manager._log_enabled = config.get("log_enabled", True)
    if manager.save_config(config):
        return {"success": True}
    raise HTTPException(status_code=500, detail="保存失败")
