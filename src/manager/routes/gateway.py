from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse, StreamingResponse

from ..service import WORKER_POOL_AVAILABLE, worker_pool


router = APIRouter(tags=["Gateway"])


def _require_worker_pool():
    if not WORKER_POOL_AVAILABLE or worker_pool is None or not worker_pool.workers:
        raise HTTPException(status_code=503, detail="No workers available")
    return worker_pool


@router.post("/v1/chat/completions")
async def gateway_chat_completions(request: Request):
    pool = _require_worker_pool()
    body = await request.json()
    model_id = body.get("model", "")
    is_stream = body.get("stream", False)

    worker = pool.get_worker_for_model(model_id)
    if worker is None:
        all_limited = all(
            worker_item.is_model_limited(model_id)
            for worker_item in pool.workers.values()
            if worker_item.status == "running"
        )
        if all_limited:
            raise HTTPException(
                status_code=429, detail=f"All workers rate limited for model {model_id}"
            )
        raise HTTPException(status_code=503, detail="No available workers")

    headers = {
        key: value
        for key, value in request.headers.items()
        if key.lower() not in ("host", "content-length")
    }

    if is_stream:

        async def stream_generator():
            async for chunk in pool.forward_stream(
                worker, "/v1/chat/completions", body, headers
            ):
                yield chunk

        return StreamingResponse(stream_generator(), media_type="text/event-stream")

    result = await pool.forward_request(worker, "/v1/chat/completions", body, headers)
    return JSONResponse(content=result)


@router.get("/v1/models")
async def gateway_models():
    pool = _require_worker_pool()

    for worker in pool.workers.values():
        if worker.status != "running":
            continue
        try:
            result = await pool.forward_get(worker, "/v1/models")
            return JSONResponse(content=result)
        except Exception:
            continue

    raise HTTPException(status_code=503, detail="No workers responding")
