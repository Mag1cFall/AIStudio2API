from fastapi import APIRouter, HTTPException

from ..service import WORKER_POOL_AVAILABLE, manager, worker_pool


router = APIRouter(tags=["System"])


@router.get("/api/status")
async def get_status():
    return {"status": manager.service_status, "info": manager.service_info}


@router.get("/api/system/ports")
async def check_all_ports():
    config = manager.load_config()
    ports_to_check = [
        {"label": "FastAPI 服务", "port": config.get("fastapi_port", 2048)},
        {"label": "Camoufox 调试", "port": config.get("camoufox_debug_port", 40222)},
    ]

    if config.get("stream_port_enabled"):
        ports_to_check.append(
            {"label": "流式代理", "port": config.get("stream_port", 3120)}
        )

    if (
        config.get("worker_mode_enabled")
        and WORKER_POOL_AVAILABLE
        and worker_pool is not None
    ):
        for worker in worker_pool.workers.values():
            ports_to_check.append(
                {"label": f"Worker-{worker.id} API", "port": worker.port}
            )
            ports_to_check.append(
                {"label": f"Worker-{worker.id} Camoufox", "port": worker.camoufox_port}
            )

    results = []
    for item in ports_to_check:
        usage = manager.check_port_usage(item["port"])
        results.append(
            {
                "label": item["label"],
                "port": item["port"],
                "in_use": len(usage) > 0,
                "processes": usage,
            }
        )
    return results


@router.get("/api/system/port/{port}")
async def check_port(port: int):
    usage = manager.check_port_usage(port)
    return {"port": port, "in_use": len(usage) > 0, "processes": usage}


@router.post("/api/system/kill/{pid}")
async def kill_process(pid: int):
    success, message = manager.kill_process(pid)
    if not success:
        raise HTTPException(status_code=500, detail=message)
    return {"success": True}
