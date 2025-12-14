import os
import json
import asyncio
import logging
import time
from typing import Optional, AsyncGenerator, List, Dict, Any, Union
from fastapi import FastAPI, Request, HTTPException
from fastapi.responses import StreamingResponse, Response
from pydantic import BaseModel, Field
import aiohttp
import uvicorn

logging.basicConfig(level=logging.INFO, format='%(asctime)s - [Gateway] %(levelname)s - %(message)s')
logger = logging.getLogger('Gateway')

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_ROOT = os.path.dirname(SCRIPT_DIR)
DATA_DIR = os.path.join(PROJECT_ROOT, 'data')

MANAGER_URL = "http://127.0.0.1:9000"
RATE_LIMIT_KEYWORDS = [b"exceeded quota", b"out of free generations", b"rate limit"]


class MessageContent(BaseModel):
    type: str = "text"
    text: Optional[str] = None
    image_url: Optional[Dict[str, str]] = None

class Message(BaseModel):
    role: str = Field(..., description="角色: system, user, assistant")
    content: Union[str, List[MessageContent]] = Field(..., description="消息内容")

class ChatCompletionRequest(BaseModel):
    model: str = Field("gemini-2.0-flash", description="模型ID")
    messages: List[Message] = Field(..., description="消息列表")
    stream: bool = Field(False, description="是否流式响应")
    temperature: Optional[float] = Field(1.0, ge=0, le=2)
    max_tokens: Optional[int] = Field(65536)
    top_p: Optional[float] = Field(0.95, ge=0, le=1)
    stop: Optional[List[str]] = None
    reasoning_effort: Optional[Union[int, str]] = Field(None, description="思考预算")

    class Config:
        extra = "allow"

class TTSRequest(BaseModel):
    model: str = Field("gemini-2.5-flash-preview-tts")
    contents: Any = Field(..., description="文本内容")
    generationConfig: Optional[Dict[str, Any]] = None

    class Config:
        extra = "allow"

class ImagenRequest(BaseModel):
    prompt: str = Field(..., description="图片描述")
    model: str = Field("imagen-3.0-generate-002")
    number_of_images: int = Field(1, ge=1, le=4)
    aspect_ratio: str = Field("1:1")
    negative_prompt: Optional[str] = None

    class Config:
        extra = "allow"

class VeoRequest(BaseModel):
    prompt: str = Field(..., description="视频描述")
    model: str = Field("veo-2.0-generate-001")
    duration_seconds: int = Field(5)
    aspect_ratio: str = Field("16:9")
    negative_prompt: Optional[str] = None
    image: Optional[str] = Field(None, description="Base64参考图")

    class Config:
        extra = "allow"

class NanoRequest(BaseModel):
    model: str = Field("gemini-2.5-flash-image")
    prompt: str = Field(..., description="图片描述")
    aspect_ratio: str = Field("1:1")
    image: Optional[str] = None

    class Config:
        extra = "allow"


app = FastAPI(
    title="AIStudio2API Gateway",
    description="多Worker负载均衡网关，将请求转发到后端Worker实例",
    version="1.0.0",
    docs_url="/docs",
    redoc_url="/redoc",
)

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

@app.get("/", tags=["System"], summary="网关状态")
async def root():
    """返回网关运行状态和可用Worker数量"""
    return {"status": "ok", "mode": "gateway", "workers": len(_worker_cache["workers"])}

@app.get("/v1/models", tags=["Chat"], summary="获取模型列表")
async def models():
    """返回所有可用的AI模型列表"""
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

@app.post("/v1/chat/completions", tags=["Chat"], summary="聊天对话")
async def chat_completions(request: Request):
    """OpenAI兼容的聊天接口，支持流式和非流式响应"""
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

@app.get("/health", tags=["System"], summary="健康检查")
async def health():
    """返回网关健康状态"""
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

@app.post("/generate-speech", tags=["Media"], summary="TTS语音合成")
async def generate_speech(request: Request):
    """Gemini 2.5 TTS语音生成"""
    return await forward_media_request(request, "/generate-speech")

@app.post("/generate-image", tags=["Media"], summary="Imagen图片生成")
async def generate_image(request: Request):
    """Imagen 3图片生成"""
    return await forward_media_request(request, "/generate-image")

@app.post("/generate-video", tags=["Media"], summary="Veo视频生成")
async def generate_video(request: Request):
    """Veo 2视频生成"""
    return await forward_media_request(request, "/generate-video")

@app.post("/nano/generate", tags=["Media"], summary="Nano图片生成")
async def nano_generate(request: Request):
    """Gemini 2.5 Flash原生图片生成"""
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

