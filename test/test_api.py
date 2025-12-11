#!/usr/bin/env python3
import asyncio
import aiohttp
import base64
import sys
import os
import time
from pathlib import Path
from typing import Tuple, List
from dataclasses import dataclass, field

BASE_URL = os.environ.get("API_BASE_URL", "http://localhost:2048")
OUTPUT_DIR = Path(__file__).parent / "test_output"
TIMEOUT_SHORT = 30
TIMEOUT_MEDIUM = 120
TIMEOUT_LONG = 600


@dataclass
class TestResult:
    passed: int = 0
    failed: int = 0
    results: List[Tuple[str, bool, str, float]] = field(default_factory=list)
    
    def record(self, name: str, success: bool, message: str = "", duration: float = 0):
        icon = "[OK]" if success else "[X]"
        self.results.append((name, success, message, duration))
        if success:
            self.passed += 1
            print(f"  {icon} {name} ({duration:.2f}s)")
        else:
            self.failed += 1
            print(f"  {icon} {name}: {message}")
        return success


def ensure_output_dir():
    OUTPUT_DIR.mkdir(exist_ok=True)


async def test_health(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        async with session.get(f"{BASE_URL}/health", timeout=aiohttp.ClientTimeout(total=TIMEOUT_SHORT)) as resp:
            data = await resp.json()
            return True, f"status={data.get('status')}", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def test_models(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        async with session.get(f"{BASE_URL}/v1/models", timeout=aiohttp.ClientTimeout(total=TIMEOUT_SHORT)) as resp:
            data = await resp.json()
            count = len(data.get("data", []))
            return True, f"count={count}", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def test_chat(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        payload = {
            "model": "gemini-2.5-flash",
            "messages": [{"role": "user", "content": "Say 'test ok' in 2 words."}],
            "max_tokens": 50
        }
        async with session.post(
            f"{BASE_URL}/v1/chat/completions",
            json=payload,
            timeout=aiohttp.ClientTimeout(total=TIMEOUT_MEDIUM)
        ) as resp:
            data = await resp.json()
            content = data["choices"][0]["message"]["content"][:30]
            return True, f"response={content}", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def test_tts(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        payload = {
            "model": "gemini-2.5-flash-preview-tts",
            "contents": "Hello, this is a test.",
            "generationConfig": {
                "responseModalities": ["AUDIO"],
                "speechConfig": {
                    "voiceConfig": {
                        "prebuiltVoiceConfig": {"voiceName": "Kore"}
                    }
                }
            }
        }
        async with session.post(
            f"{BASE_URL}/generate-speech",
            json=payload,
            timeout=aiohttp.ClientTimeout(total=TIMEOUT_MEDIUM)
        ) as resp:
            data = await resp.json()
            audio_b64 = data["candidates"][0]["content"]["parts"][0]["inlineData"]["data"]
            audio_bytes = base64.b64decode(audio_b64)
            output_path = OUTPUT_DIR / "tts_output.wav"
            output_path.write_bytes(audio_bytes)
            return True, f"size={len(audio_bytes)}", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def test_imagen(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        payload = {
            "prompt": "A mountain landscape at sunset",
            "model": "imagen-3.0-generate-002",
            "number_of_images": 1,
            "aspect_ratio": "16:9"
        }
        async with session.post(
            f"{BASE_URL}/generate-image",
            json=payload,
            timeout=aiohttp.ClientTimeout(total=TIMEOUT_LONG)
        ) as resp:
            data = await resp.json()
            img_b64 = data["generatedImages"][0]["image"]["imageBytes"]
            img_bytes = base64.b64decode(img_b64)
            output_path = OUTPUT_DIR / "imagen_output.png"
            output_path.write_bytes(img_bytes)
            return True, f"size={len(img_bytes)}", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def test_nano(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        payload = {
            "model": "gemini-2.5-flash-image",
            "contents": [{"parts": [{"text": "A cute cartoon cat"}]}]
        }
        async with session.post(
            f"{BASE_URL}/nano/generate",
            json=payload,
            timeout=aiohttp.ClientTimeout(total=TIMEOUT_LONG)
        ) as resp:
            data = await resp.json()
            parts = data["candidates"][0]["content"]["parts"]
            for i, part in enumerate(parts):
                if "inlineData" in part:
                    img_bytes = base64.b64decode(part["inlineData"]["data"])
                    output_path = OUTPUT_DIR / f"nano_output_{i}.png"
                    output_path.write_bytes(img_bytes)
                    return True, f"size={len(img_bytes)}", time.time() - start
            return False, "No image in response", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def test_veo(session: aiohttp.ClientSession) -> Tuple[bool, str, float]:
    start = time.time()
    try:
        payload = {
            "prompt": "Ocean waves on beach",
            "model": "veo-2.0-generate-001",
            "aspect_ratio": "16:9",
            "duration_seconds": 5
        }
        async with session.post(
            f"{BASE_URL}/generate-video",
            json=payload,
            timeout=aiohttp.ClientTimeout(total=TIMEOUT_LONG)
        ) as resp:
            data = await resp.json()
            vid_b64 = data["generatedVideos"][0]["video"]["videoBytes"]
            vid_bytes = base64.b64decode(vid_b64)
            output_path = OUTPUT_DIR / "veo_output.mp4"
            output_path.write_bytes(vid_bytes)
            return True, f"size={len(vid_bytes)}", time.time() - start
    except Exception as e:
        return False, str(e), time.time() - start


async def run_concurrent_tests(skip_veo: bool = True):
    print("=" * 50)
    print(" AIStudio2API Concurrent Tests")
    print(f" Base URL: {BASE_URL}")
    print(f" Mode: CONCURRENT (all tests run in parallel)")
    print("=" * 50)
    
    ensure_output_dir()
    
    async with aiohttp.ClientSession() as session:
        tests = [
            ("Health", test_health(session)),
            ("Models", test_models(session)),
            ("Chat", test_chat(session)),
            ("TTS", test_tts(session)),
            ("Imagen", test_imagen(session)),
            ("Nano", test_nano(session)),
        ]
        
        if not skip_veo:
            tests.append(("Veo", test_veo(session)))
        
        print(f"\nRunning {len(tests)} tests concurrently...")
        start_all = time.time()
        
        tasks = [t[1] for t in tests]
        names = [t[0] for t in tests]
        
        results = await asyncio.gather(*tasks, return_exceptions=True)
        
        total_time = time.time() - start_all
        
        print("\n=== Results ===")
        result = TestResult()
        for name, res in zip(names, results):
            if isinstance(res, Exception):
                result.record(name, False, str(res), 0)
            else:
                success, msg, duration = res
                result.record(name, success, msg, duration)
        
        if skip_veo:
            print("  [--] Veo: SKIPPED (use --veo to include)")
    
    print("\n" + "=" * 50)
    total = result.passed + result.failed
    print(f" Results: {result.passed}/{total} passed")
    print(f" Total time: {total_time:.2f}s (concurrent)")
    print("=" * 50)
    
    return result.failed == 0


def main():
    skip_veo = "--veo" not in sys.argv
    success = asyncio.run(run_concurrent_tests(skip_veo))
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
