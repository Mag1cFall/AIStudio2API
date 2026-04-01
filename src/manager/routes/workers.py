import os

from fastapi import APIRouter, Body, HTTPException

try:
    from ...config.settings import SAVED_AUTH_DIR
except ImportError:
    from config.settings import SAVED_AUTH_DIR

from ..service import WORKER_POOL_AVAILABLE, manager, worker_pool


router = APIRouter(prefix="/api/workers", tags=["Workers"])


def _require_worker_pool():
    if not WORKER_POOL_AVAILABLE or worker_pool is None:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    return worker_pool


@router.post("/add")
async def add_worker(profile: str = Body(..., embed=True)):
    pool = _require_worker_pool()

    existing_ids = [
        int(worker.id[1:])
        for worker in pool.workers.values()
        if worker.id.startswith("w") and worker.id[1:].isdigit()
    ]
    next_id = max(existing_ids, default=0) + 1
    worker_id = f"w{next_id}"

    existing_ports = [worker.port for worker in pool.workers.values()]
    existing_camoufox_ports = [worker.camoufox_port for worker in pool.workers.values()]
    port = max(existing_ports, default=3000) + 1
    camoufox_port = max(existing_camoufox_ports, default=40221) + 1

    profile_path = os.path.join(SAVED_AUTH_DIR, profile)
    if not os.path.exists(profile_path):
        raise HTTPException(status_code=404, detail="Profile文件不存在")

    try:
        from ...worker.models import Worker
    except ImportError:
        from worker.models import Worker

    worker = Worker(
        id=worker_id,
        profile_name=profile,
        profile_path=profile_path,
        port=port,
        camoufox_port=camoufox_port,
    )
    pool.workers[worker_id] = worker
    pool.save_config()
    if manager.is_worker_mode and manager.service_status == "running":
        pool.configure_runtime(manager.load_config())
        success, message = pool.start_worker(worker_id)
        if not success:
            raise HTTPException(status_code=500, detail=message)
    await manager.broadcast_worker_snapshot()
    return {"success": True, "worker": worker.to_dict()}


@router.delete("/{worker_id}")
async def remove_worker(worker_id: str):
    pool = _require_worker_pool()

    if worker_id not in pool.workers:
        raise HTTPException(status_code=404, detail="Worker not found")

    worker = pool.workers[worker_id]
    process = worker.process
    if worker.status == "running":
        success, message = await pool.stop_worker(worker_id)
        if not success:
            raise HTTPException(status_code=500, detail=message)
        manager.unregister_worker_process(process)

    del pool.workers[worker_id]
    pool.save_config()
    await manager.broadcast_worker_snapshot()
    return {"success": True}


@router.get("")
async def list_workers():
    if not WORKER_POOL_AVAILABLE or worker_pool is None:
        return []
    if not worker_pool.workers:
        worker_pool.init_from_config()
    return worker_pool.get_status()


@router.post("/init")
async def init_workers():
    pool = _require_worker_pool()
    pool.init_from_config()
    await manager.broadcast_worker_snapshot()
    return {"success": True, "count": len(pool.workers)}


@router.post("/save")
async def save_workers_config():
    pool = _require_worker_pool()
    try:
        pool.save_config()
        await manager.broadcast_worker_snapshot()
        return {"success": True, "count": len(pool.workers)}
    except Exception as exc:
        return {"success": False, "error": str(exc)}


@router.get("/next")
async def get_next_available_worker(model: str = ""):
    pool = _require_worker_pool()
    worker = pool.get_worker_for_model(model)
    if worker:
        worker.request_count += 1
        return {"port": worker.port, "worker_id": worker.id}

    all_limited = all(
        worker.is_model_limited(model)
        for worker in pool.workers.values()
        if worker.status == "running"
    )
    if all_limited:
        return {
            "error": "all_rate_limited",
            "message": f"All workers rate limited for model {model}",
        }
    return {"error": "no_workers", "message": "No available workers"}


@router.post("/{worker_id}/rate-limit")
async def mark_worker_rate_limited(worker_id: str, model: str = Body(..., embed=True)):
    pool = _require_worker_pool()
    pool.mark_rate_limited(worker_id, model)
    await manager.broadcast_worker_snapshot()
    return {"success": True}


@router.post("/{worker_id}/start")
async def start_worker_api(worker_id: str):
    pool = _require_worker_pool()
    pool.configure_runtime(manager.load_config())
    success, message = pool.start_worker(worker_id)
    if not success:
        raise HTTPException(status_code=400, detail=message)
    await manager.broadcast_worker_snapshot()
    return {"success": True, "message": message}


@router.post("/{worker_id}/stop")
async def stop_worker_api(worker_id: str):
    pool = _require_worker_pool()
    process = pool.workers.get(worker_id).process if worker_id in pool.workers else None
    success, message = await pool.stop_worker(worker_id)
    if not success:
        raise HTTPException(status_code=400, detail=message)
    manager.unregister_worker_process(process)
    await manager.broadcast_worker_snapshot()
    return {"success": True, "message": message}


@router.post("/{worker_id}/clear-limits")
async def clear_worker_limits(worker_id: str):
    pool = _require_worker_pool()
    if pool.clear_rate_limits(worker_id):
        await manager.broadcast_worker_snapshot()
        return {"success": True}
    raise HTTPException(status_code=404, detail="Worker not found")


@router.post("/start-all")
async def start_all_workers():
    pool = _require_worker_pool()
    pool.configure_runtime(manager.load_config())
    await pool.start_all()
    await manager.broadcast_worker_snapshot()
    return {"success": True}


@router.post("/stop-all")
async def stop_all_workers():
    pool = _require_worker_pool()
    processes = [worker.process for worker in pool.workers.values() if worker.process]
    await pool.stop_all()
    for process in processes:
        manager.unregister_worker_process(process)
    await manager.broadcast_worker_snapshot()
    return {"success": True}
