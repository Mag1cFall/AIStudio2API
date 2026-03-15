import asyncio
import json
import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncGenerator, Optional

import aiohttp
import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import Response, StreamingResponse


logger = logging.getLogger("Gateway")

MANAGER_URL = "http://127.0.0.1:9000"
RATE_LIMIT_KEYWORDS = [b"exceeded quota", b"out of free generations", b"rate limit"]

_session: Optional[aiohttp.ClientSession] = None
_worker_cache = {"workers": [], "last_update": 0, "index": 0}
CACHE_TTL = 5


async def get_session() -> aiohttp.ClientSession:
    global _session
    if _session is None or _session.closed:
        connector = aiohttp.TCPConnector(
            limit=100, limit_per_host=20, keepalive_timeout=30
        )
        _session = aiohttp.ClientSession(connector=connector)
    return _session


async def close_session() -> None:
    global _session
    if _session and not _session.closed:
        await _session.close()
    _session = None


async def refresh_workers() -> None:
    cache = _worker_cache
    if time.time() - cache["last_update"] < CACHE_TTL and cache["workers"]:
        return

    try:
        session = await get_session()
        timeout = aiohttp.ClientTimeout(total=5)
        async with session.get(
            f"{MANAGER_URL}/api/workers", timeout=timeout
        ) as response:
            workers = await response.json()
            cache["workers"] = [
                worker for worker in workers if worker.get("status") == "running"
            ]
            cache["last_update"] = time.time()
    except Exception as exc:
        logger.warning(f"Refresh workers failed: {exc}")


def get_next_worker(model: str = "") -> Optional[dict]:
    cache = _worker_cache
    workers = cache["workers"]
    if not workers:
        return None

    candidates = workers
    if model:
        current_time = time.time()
        candidates = []
        for worker in workers:
            limits = worker.get("rate_limited_models", {})
            if model in limits and limits[model] > current_time:
                continue
            candidates.append(worker)

    if not candidates:
        return None

    worker = candidates[cache["index"] % len(candidates)]
    cache["index"] += 1
    return worker


async def report_rate_limit(worker_id: str, model: str) -> None:
    try:
        session = await get_session()
        timeout = aiohttp.ClientTimeout(total=2)
        await session.post(
            f"{MANAGER_URL}/api/workers/{worker_id}/rate-limit",
            json={"model": model},
            timeout=timeout,
        )
    except Exception as exc:
        logger.warning(f"Report rate limit failed for worker {worker_id}: {exc}")


def check_rate_limit_in_response(content: bytes) -> bool:
    content_lower = content.lower()
    return any(keyword in content_lower for keyword in RATE_LIMIT_KEYWORDS)


@asynccontextmanager
async def lifespan(app: FastAPI):
    await refresh_workers()
    logger.info("Gateway started")
    yield
    await close_session()


app = FastAPI(
    title="AIStudio2API Gateway",
    description="多Worker负载均衡网关，将请求转发到后端Worker实例",
    version="1.0.0",
    docs_url="/docs",
    redoc_url="/redoc",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.get("/", tags=["System"], summary="网关状态")
async def root():
    return {"status": "ok", "mode": "gateway", "workers": len(_worker_cache["workers"])}


@app.get("/v1/models", tags=["Chat"], summary="获取模型列表")
async def models():
    await refresh_workers()
    worker = get_next_worker()
    if not worker:
        raise HTTPException(status_code=503, detail="No workers available")

    url = f"http://127.0.0.1:{worker['port']}/v1/models"
    session = await get_session()
    try:
        async with session.get(
            url, timeout=aiohttp.ClientTimeout(total=30)
        ) as response:
            content = await response.read()
            return Response(
                content=content,
                status_code=response.status,
                media_type=response.content_type,
            )
    except Exception as exc:
        logger.error(f"Forward /v1/models failed: {exc}")
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@app.post("/v1/chat/completions", tags=["Chat"], summary="聊天对话")
async def chat_completions(request: Request):
    await refresh_workers()
    body = await request.body()
    if not body:
        raise HTTPException(status_code=400, detail="Request body is empty")
    body_json = json.loads(body)
    is_stream = body_json.get("stream", False)
    model_id = body_json.get("model", "")

    worker = get_next_worker(model_id)
    if not worker:
        raise HTTPException(status_code=503, detail="No workers available")

    port = worker["port"]
    worker_id = worker.get("id", "")
    url = f"http://127.0.0.1:{port}/v1/chat/completions"
    req_id = f"gw-{worker_id}"
    logger.info(f"[{req_id}] POST -> worker:{port} (stream={is_stream})")

    forward_headers = {"Content-Type": "application/json"}
    for key, value in request.headers.items():
        if key.lower() not in (
            "host",
            "content-length",
            "transfer-encoding",
            "content-type",
        ):
            forward_headers[key] = value

    session = await get_session()

    if is_stream:

        async def stream_proxy() -> AsyncGenerator[bytes, None]:
            rate_limited = False
            try:
                timeout = aiohttp.ClientTimeout(total=600, sock_read=300)
                async with session.post(
                    url, data=body, headers=forward_headers, timeout=timeout
                ) as response:
                    async for chunk in response.content.iter_chunks():
                        data, _ = chunk
                        if not data:
                            continue
                        if not rate_limited and check_rate_limit_in_response(data):
                            rate_limited = True
                        yield data

                    if rate_limited and worker_id and model_id:
                        asyncio.create_task(report_rate_limit(worker_id, model_id))
            except asyncio.CancelledError:
                logger.info(f"[{req_id}] Stream cancelled")
                raise
            except Exception as exc:
                logger.error(f"[{req_id}] Stream error: {exc}")

        return StreamingResponse(
            stream_proxy(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    try:
        async with session.post(
            url,
            data=body,
            headers=forward_headers,
            timeout=aiohttp.ClientTimeout(total=300),
        ) as response:
            content = await response.read()
            if check_rate_limit_in_response(content) and worker_id and model_id:
                asyncio.create_task(report_rate_limit(worker_id, model_id))
            return Response(
                content=content,
                status_code=response.status,
                media_type=response.content_type,
            )
    except Exception as exc:
        logger.error(f"[{req_id}] Forward failed: {exc}")
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@app.get("/health", tags=["System"], summary="健康检查")
async def health():
    return {"status": "ok", "workers": len(_worker_cache["workers"])}


async def forward_media_request(request: Request, path: str):
    await refresh_workers()
    worker = get_next_worker()
    if not worker:
        raise HTTPException(status_code=503, detail="No workers available")

    url = f"http://127.0.0.1:{worker['port']}{path}"
    body = await request.body()
    forward_headers = {}
    for key, value in request.headers.items():
        if key.lower() not in ("host", "content-length", "transfer-encoding"):
            forward_headers[key] = value

    session = await get_session()
    try:
        async with session.post(
            url,
            data=body,
            headers=forward_headers,
            timeout=aiohttp.ClientTimeout(total=300),
        ) as response:
            content = await response.read()
            return Response(
                content=content,
                status_code=response.status,
                media_type=response.content_type,
            )
    except Exception as exc:
        logger.error(f"Forward {path} failed: {exc}")
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@app.post("/generate-speech", tags=["Media"], summary="TTS语音合成")
async def generate_speech(request: Request):
    return await forward_media_request(request, "/generate-speech")


@app.post("/generate-image", tags=["Media"], summary="Imagen图片生成")
async def generate_image(request: Request):
    return await forward_media_request(request, "/generate-image")


@app.post("/generate-video", tags=["Media"], summary="Veo视频生成")
async def generate_video(request: Request):
    return await forward_media_request(request, "/generate-video")


@app.post("/nano/generate", tags=["Media"], summary="Nano图片生成")
async def nano_generate(request: Request):
    return await forward_media_request(request, "/nano/generate")


@app.post("/v1beta/models/{model}:generateContent")
async def v1beta_generate_content(request: Request, model: str):
    return await forward_media_request(
        request, f"/v1beta/models/{model}:generateContent"
    )


@app.post("/v1beta/models/{model}:predict")
async def v1beta_predict(request: Request, model: str):
    return await forward_media_request(request, f"/v1beta/models/{model}:predict")


@app.post("/v1beta/models/{model}:predictLongRunning")
async def v1beta_predict_long_running(request: Request, model: str):
    return await forward_media_request(
        request, f"/v1beta/models/{model}:predictLongRunning"
    )


def configure_logging() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - [Gateway] %(levelname)s - %(message)s",
    )


def main() -> None:
    import argparse

    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=2048)
    args = parser.parse_args()

    configure_logging()
    uvicorn.run(app, host="0.0.0.0", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()
