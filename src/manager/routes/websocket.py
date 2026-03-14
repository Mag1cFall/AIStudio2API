import json

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

from ..service import manager


router = APIRouter(tags=["WebSocket"])


@router.websocket("/ws/logs")
async def websocket_logs(websocket: WebSocket):
    await websocket.accept()
    manager.active_connections.append(websocket)
    try:
        await websocket.send_text(
            json.dumps(
                {
                    "type": "status",
                    "status": manager.service_status,
                    "info": manager.service_info,
                }
            )
        )
        await manager.broadcast_worker_snapshot()
        while True:
            await websocket.receive_text()
    except WebSocketDisconnect:
        pass
    finally:
        if websocket in manager.active_connections:
            manager.active_connections.remove(websocket)
