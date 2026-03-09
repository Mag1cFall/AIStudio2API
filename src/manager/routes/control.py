from typing import Any, Dict

from fastapi import APIRouter, Body, HTTPException

from ..service import manager


router = APIRouter(prefix="/api/control", tags=["Control"])


@router.post("/start")
async def start_service(config: Dict[str, Any] = Body(...)):
    success, message = await manager.start_service(config)
    await manager.broadcast_status()
    await manager.broadcast_worker_snapshot()
    if not success:
        raise HTTPException(status_code=400, detail=message)
    return {"success": True, "message": message}


@router.post("/stop")
async def stop_service():
    success, message = manager.stop_service()
    await manager.broadcast_status()
    await manager.broadcast_worker_snapshot()
    if not success:
        raise HTTPException(status_code=500, detail=message)
    return {"success": True, "message": message}
