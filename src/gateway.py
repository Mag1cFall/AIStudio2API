import os
import json
import asyncio
import logging
import time
from typing import Optional, AsyncGenerator
from fastapi import FastAPI, Request, HTTPException
from fastapi.responses import StreamingResponse, Response
import aiohttp
import uvicorn

logging.basicConfig(level=logging.INFO, format='%(asctime)s - [Gateway] %(levelname)s - %(message)s')
logger = logging.getLogger('Gateway')

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_ROOT = os.path.dirname(SCRIPT_DIR)
DATA_DIR = os.path.join(PROJECT_ROOT, 'data')

MANAGER_URL = "http://127.0.0.1:9000"
RATE_LIMIT_KEYWORDS = [b"exceeded quota", b"out of free generations", b"rate limit"]

app = FastAPI(title="AIStudio2API Gateway")

_session: Optional[aiohttp.ClientSession] = None
_worker_cache = {"workers": [], "last_update": 0, "index": 0}
CACHE_TTL = 5

async def get_session() -> aiohttp.ClientSession:
    global _session
    if _session is None or _session.closed:
        connector = aiohttp.TCPConnector(limit=100, limit_per_host=20, keepalive_timeout=30)
        _session = aiohttp.ClientSession(connector=connector)
    return _session

async def refresh_workers():
    cache = _worker_cache
    if time.time() - cache["last_update"] < CACHE_TTL and cache["workers"]:
        return
    try:
        session = await get_session()
        async with session.get(f"{MANAGER_URL}/api/workers", timeout=aiohttp.ClientTimeout(total=5)) as resp:
            workers = await resp.json()
            cache["workers"] = [w for w in workers if w.get("status") == "running"]
            cache["last_update"] = time.time()
    except Exception as e:
        logger.warning(f"Refresh workers failed: {e}")

def get_next_worker(model: str = "") -> Optional[dict]:
    cache = _worker_cache
    workers = cache["workers"]
    if not workers:
        return None
    
    candidates = workers
    if model:
        current_time = time.time()
        candidates = []
        for w in workers:
            limits = w.get("rate_limited_models", {})
            if model in limits:
                if limits[model] > current_time:
                    continue
            candidates.append(w)
            
    if not candidates:
        return None
        
    worker = candidates[cache["index"] % len(candidates)]
    cache["index"] += 1
    return worker

async def report_rate_limit(worker_id: str, model: str):
    try:
        session = await get_session()
        await session.post(f"{MANAGER_URL}/api/workers/{worker_id}/rate-limit", json={"model": model}, timeout=aiohttp.ClientTimeout(total=2))
    except:
        pass

def check_rate_limit_in_response(content: bytes) -> bool:
    content_lower = content.lower()
    return any(kw in content_lower for kw in RATE_LIMIT_KEYWORDS)

@app.on_event("startup")
async def startup():
    await refresh_workers()
    logger.info(f"Gateway started")

@app.on_event("shutdown")
async def shutdown():
    global _session
    if _session and not _session.closed:
        await _session.close()

@app.get("/")
async def root():
    return {"status": "ok", "mode": "gateway", "workers": len(_worker_cache["workers"])}

@app.get("/v1/models")
async def models():
    await refresh_workers()
    worker = get_next_worker()
    if not worker:
        raise HTTPException(status_code=503, detail="No workers available")
    
    port = worker["port"]
    url = f"http://127.0.0.1:{port}/v1/models"
    
    session = await get_session()
    try:
        async with session.get(url, timeout=aiohttp.ClientTimeout(total=30)) as resp:
            content = await resp.read()
            return Response(content=content, status_code=resp.status, media_type=resp.content_type)
    except Exception as e:
        logger.error(f"Forward /v1/models failed: {e}")
        raise HTTPException(status_code=502, detail=str(e))

@app.post("/v1/chat/completions")
async def chat_completions(request: Request):
    await refresh_workers()
    body = await request.body()
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
    
    forward_headers = {'Content-Type': 'application/json'}
    for k, v in request.headers.items():
        if k.lower() not in ('host', 'content-length', 'transfer-encoding', 'content-type'):
            forward_headers[k] = v
    
    session = await get_session()
    
    if is_stream:
        async def stream_proxy() -> AsyncGenerator[bytes, None]:
            rate_limited = False
            check_count = 0
            try:
                async with session.post(url, data=body, headers=forward_headers, timeout=aiohttp.ClientTimeout(total=600, sock_read=300)) as resp:
                    async for chunk in resp.content.iter_chunks():
                        data, _ = chunk
                        if data:
                            check_count += 1
                            if not rate_limited:
                                if check_rate_limit_in_response(data):
                                    rate_limited = True
                            yield data
                    if rate_limited and worker_id and model_id:
                        asyncio.create_task(report_rate_limit(worker_id, model_id))
            except asyncio.CancelledError:
                pass
            except Exception as e:
                logger.error(f"[{req_id}] Stream error: {e}")
        
        return StreamingResponse(stream_proxy(), media_type="text/event-stream", headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"})
    else:
        try:
            async with session.post(url, data=body, headers=forward_headers, timeout=aiohttp.ClientTimeout(total=300)) as resp:
                content = await resp.read()
                if check_rate_limit_in_response(content) and worker_id and model_id:
                    asyncio.create_task(report_rate_limit(worker_id, model_id))
                return Response(content=content, status_code=resp.status, media_type=resp.content_type)
        except Exception as e:
            logger.error(f"[{req_id}] Forward failed: {e}")
            raise HTTPException(status_code=502, detail=str(e))

@app.get("/health")
async def health():
    return {"status": "ok", "workers": len(_worker_cache["workers"])}

async def forward_media_request(request: Request, path: str):
    await refresh_workers()
    worker = get_next_worker()
    if not worker:
        raise HTTPException(status_code=503, detail="No workers available")
    
    port = worker["port"]
    url = f"http://127.0.0.1:{port}{path}"
    
    body = await request.body()
    forward_headers = {}
    for k, v in request.headers.items():
        if k.lower() not in ('host', 'content-length', 'transfer-encoding'):
            forward_headers[k] = v
    
    session = await get_session()
    try:
        async with session.post(url, data=body, headers=forward_headers, timeout=aiohttp.ClientTimeout(total=300)) as resp:
            content = await resp.read()
            return Response(content=content, status_code=resp.status, media_type=resp.content_type)
    except Exception as e:
        logger.error(f"Forward {path} failed: {e}")
        raise HTTPException(status_code=502, detail=str(e))

@app.post("/generate-speech")
async def generate_speech(request: Request):
    return await forward_media_request(request, "/generate-speech")

@app.post("/generate-image")
async def generate_image(request: Request):
    return await forward_media_request(request, "/generate-image")

@app.post("/generate-video")
async def generate_video(request: Request):
    return await forward_media_request(request, "/generate-video")

@app.post("/nano/generate")
async def nano_generate(request: Request):
    return await forward_media_request(request, "/nano/generate")

@app.post("/v1beta/models/{model}:generateContent")
async def v1beta_generate_content(request: Request, model: str):
    return await forward_media_request(request, f"/v1beta/models/{model}:generateContent")

@app.post("/v1beta/models/{model}:predict")
async def v1beta_predict(request: Request, model: str):
    return await forward_media_request(request, f"/v1beta/models/{model}:predict")

@app.post("/v1beta/models/{model}:predictLongRunning")
async def v1beta_predict_long_running(request: Request, model: str):
    return await forward_media_request(request, f"/v1beta/models/{model}:predictLongRunning")

def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--port', type=int, default=2048)
    args = parser.parse_args()
    uvicorn.run(app, host="0.0.0.0", port=args.port, log_level="warning")

if __name__ == "__main__":
    main()

