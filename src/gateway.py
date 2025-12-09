import os
import json
import asyncio
import logging
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
WORKERS_CONFIG_PATH = os.path.join(DATA_DIR, 'workers.json')

app = FastAPI(title="AIStudio2API Gateway")

workers = []
current_index = 0

def load_workers():
    global workers
    if os.path.exists(WORKERS_CONFIG_PATH):
        try:
            with open(WORKERS_CONFIG_PATH, 'r', encoding='utf-8') as f:
                config = json.load(f)
            workers = [w['port'] for w in config.get('workers', [])]
            logger.info(f"Loaded {len(workers)} workers: {workers}")
        except Exception as e:
            logger.error(f"Load workers failed: {e}")

def get_next_worker() -> Optional[int]:
    global current_index, workers
    if not workers:
        return None
    port = workers[current_index % len(workers)]
    current_index += 1
    return port

@app.on_event("startup")
async def startup():
    load_workers()
    logger.info(f"Gateway started with {len(workers)} workers")

@app.get("/")
async def root():
    return {"status": "ok", "mode": "gateway", "workers": len(workers)}

@app.get("/v1/models")
async def models():
    port = get_next_worker()
    if not port:
        raise HTTPException(status_code=503, detail="No workers available")
    
    url = f"http://127.0.0.1:{port}/v1/models"
    logger.info(f"GET /v1/models -> worker:{port}")
    
    timeout = aiohttp.ClientTimeout(total=60)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        try:
            async with session.get(url) as resp:
                content = await resp.read()
                return Response(content=content, status_code=resp.status, media_type=resp.content_type)
        except Exception as e:
            logger.error(f"Forward /v1/models failed: {e}")
            raise HTTPException(status_code=502, detail=str(e))

@app.post("/v1/chat/completions")
async def chat_completions(request: Request):
    body = await request.body()
    body_json = json.loads(body)
    is_stream = body_json.get("stream", False)
    
    port = get_next_worker()
    if not port:
        raise HTTPException(status_code=503, detail="No workers available")
    
    url = f"http://127.0.0.1:{port}/v1/chat/completions"
    req_id = f"gw-{current_index}"
    logger.info(f"[{req_id}] POST -> worker:{port} (stream={is_stream})")
    
    forward_headers = {'Content-Type': 'application/json'}
    for k, v in request.headers.items():
        k_lower = k.lower()
        if k_lower not in ('host', 'content-length', 'transfer-encoding', 'content-type'):
            forward_headers[k] = v
    
    if is_stream:
        async def stream_proxy() -> AsyncGenerator[bytes, None]:
            timeout = aiohttp.ClientTimeout(total=600, sock_read=300)
            connector = aiohttp.TCPConnector(force_close=True)
            async with aiohttp.ClientSession(timeout=timeout, connector=connector) as session:
                try:
                    async with session.post(url, data=body, headers=forward_headers) as resp:
                        logger.info(f"[{req_id}] Stream started, status={resp.status}")
                        chunk_count = 0
                        async for chunk in resp.content.iter_chunks():
                            data, end_of_chunk = chunk
                            if data:
                                chunk_count += 1
                                yield data
                        logger.info(f"[{req_id}] Stream completed, chunks={chunk_count}")
                except asyncio.CancelledError:
                    logger.warning(f"[{req_id}] Stream cancelled")
                except Exception as e:
                    logger.error(f"[{req_id}] Stream error: {e}")
        
        return StreamingResponse(
            stream_proxy(),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
                "X-Accel-Buffering": "no",
                "Transfer-Encoding": "chunked"
            }
        )
    else:
        timeout = aiohttp.ClientTimeout(total=300)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            try:
                async with session.post(url, data=body, headers=forward_headers) as resp:
                    content = await resp.read()
                    logger.info(f"[{req_id}] Non-stream response, status={resp.status}, len={len(content)}")
                    return Response(content=content, status_code=resp.status, media_type=resp.content_type)
            except Exception as e:
                logger.error(f"[{req_id}] Forward failed: {e}")
                raise HTTPException(status_code=502, detail=str(e))

@app.get("/health")
async def health():
    return {"status": "ok", "workers": workers}

def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--port', type=int, default=2048)
    args = parser.parse_args()
    
    logger.info(f"Starting Gateway on port {args.port}")
    uvicorn.run(app, host="0.0.0.0", port=args.port, log_level="info")

if __name__ == "__main__":
    main()
