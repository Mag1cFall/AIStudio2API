import os
import sys
import json
import asyncio
import logging
import subprocess
import platform
from typing import Dict, List, Optional, AsyncGenerator
import aiohttp
from .models import Worker

logger = logging.getLogger('WorkerPool')

SCRIPT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LAUNCH_CAMOUFOX_PY = os.path.join(SCRIPT_DIR, 'launch_camoufox.py')
PROJECT_ROOT = os.path.dirname(SCRIPT_DIR)
DATA_DIR = os.path.join(PROJECT_ROOT, 'data')
WORKERS_CONFIG_PATH = os.path.join(DATA_DIR, 'workers.json')

class WorkerPool:
    def __init__(self):
        self.workers: Dict[str, Worker] = {}
        self._lock = asyncio.Lock()
        self.recovery_hours = 6.0
        self._session: Optional[aiohttp.ClientSession] = None
        self._connector: Optional[aiohttp.TCPConnector] = None
    
    async def _get_session(self) -> aiohttp.ClientSession:
        if self._session is None or self._session.closed:
            self._connector = aiohttp.TCPConnector(
                limit=100,
                limit_per_host=20,
                keepalive_timeout=30,
                enable_cleanup_closed=True
            )
            timeout = aiohttp.ClientTimeout(total=300, connect=10)
            self._session = aiohttp.ClientSession(
                connector=self._connector,
                timeout=timeout
            )
        return self._session
    
    async def close(self):
        if self._session and not self._session.closed:
            await self._session.close()
        if self._connector:
            await self._connector.close()
    
    def load_config(self) -> dict:
        if os.path.exists(WORKERS_CONFIG_PATH):
            try:
                with open(WORKERS_CONFIG_PATH, 'r', encoding='utf-8') as f:
                    return json.load(f)
            except:
                pass
        return {"workers": [], "settings": {"recovery_hours": 6}}
    
    def save_config(self):
        config = {
            "workers": [
                {
                    "id": w.id,
                    "profile": w.profile_name,
                    "port": w.port,
                    "camoufox_port": w.camoufox_port
                }
                for w in self.workers.values()
            ],
            "settings": {"recovery_hours": self.recovery_hours}
        }
        os.makedirs(DATA_DIR, exist_ok=True)
        with open(WORKERS_CONFIG_PATH, 'w', encoding='utf-8') as f:
            json.dump(config, f, indent=2, ensure_ascii=False)
    
    def init_from_config(self):
        config = self.load_config()
        self.recovery_hours = config.get("settings", {}).get("recovery_hours", 6.0)
        valid_workers = []
        for w_cfg in config.get("workers", []):
            profile_path = os.path.join(DATA_DIR, 'auth_profiles', 'saved', w_cfg['profile'])
            if not os.path.exists(profile_path):
                profile_path = os.path.join(DATA_DIR, 'auth_profiles', 'workers', w_cfg['profile'])
            if not os.path.exists(profile_path):
                logger.warning(f"跳过Worker {w_cfg['id']}: 认证文件 {w_cfg['profile']} 不存在")
                continue
            worker = Worker(
                id=w_cfg['id'],
                profile_name=w_cfg['profile'],
                profile_path=profile_path,
                port=w_cfg['port'],
                camoufox_port=w_cfg['camoufox_port']
            )
            self.workers[worker.id] = worker
            valid_workers.append(w_cfg)
        if len(valid_workers) != len(config.get("workers", [])):
            config["workers"] = valid_workers
            os.makedirs(DATA_DIR, exist_ok=True)
            with open(WORKERS_CONFIG_PATH, 'w', encoding='utf-8') as f:
                json.dump(config, f, indent=2, ensure_ascii=False)
        logger.info(f"Loaded {len(self.workers)} workers from config")
    
    def start_worker(self, worker_id: str) -> tuple:
        if worker_id not in self.workers:
            return False, "Worker not found"
        worker = self.workers[worker_id]
        if worker.status == "running":
            return False, "Worker already running"
        
        cmd = [
            sys.executable, LAUNCH_CAMOUFOX_PY, '--headless',
            '--server-port', str(worker.port),
            '--camoufox-debug-port', str(worker.camoufox_port),
            '--active-auth-json', worker.profile_path
        ]
        
        env = os.environ.copy()
        env['PYTHONUNBUFFERED'] = '1'
        env['PYTHONIOENCODING'] = 'utf-8'
        
        try:
            creationflags = subprocess.CREATE_NEW_PROCESS_GROUP if platform.system() == 'Windows' else 0
            worker.process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                env=env,
                cwd=SCRIPT_DIR,
                creationflags=creationflags
            )
            worker.status = "running"
            logger.info(f"Started worker {worker_id} on port {worker.port}")
            return True, f"Worker started on port {worker.port}"
        except Exception as e:
            logger.error(f"Failed to start worker {worker_id}: {e}")
            return False, str(e)
    
    def stop_worker(self, worker_id: str) -> tuple:
        if worker_id not in self.workers:
            return False, "Worker not found"
        worker = self.workers[worker_id]
        if worker.status != "running" or not worker.process:
            return True, "Worker not running"
        
        try:
            if platform.system() == 'Windows':
                subprocess.run(['taskkill', '/PID', str(worker.process.pid), '/T', '/F'], capture_output=True)
            else:
                worker.process.terminate()
                try:
                    worker.process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    worker.process.kill()
            worker.process = None
            worker.status = "stopped"
            logger.info(f"Stopped worker {worker_id}")
            return True, "Worker stopped"
        except Exception as e:
            logger.error(f"Failed to stop worker {worker_id}: {e}")
            return False, str(e)
    
    def get_worker_for_model(self, model_id: str) -> Optional[Worker]:
        available = [
            w for w in self.workers.values()
            if w.status == "running" and not w.is_model_limited(model_id)
        ]
        if not available:
            return None
        return min(available, key=lambda w: w.request_count)
    
    def mark_rate_limited(self, worker_id: str, model_id: str):
        if worker_id in self.workers:
            self.workers[worker_id].mark_model_limited(model_id, self.recovery_hours)
            logger.warning(f"Worker {worker_id} rate limited for model {model_id}")
    
    def clear_rate_limits(self, worker_id: str) -> bool:
        if worker_id in self.workers:
            self.workers[worker_id].clear_rate_limits()
            return True
        return False
    
    def get_status(self) -> List[dict]:
        return [w.to_dict() for w in self.workers.values()]
    
    async def forward_request(self, worker: Worker, path: str, body: dict, headers: dict = None) -> dict:
        url = f"http://127.0.0.1:{worker.port}{path}"
        session = await self._get_session()
        worker.request_count += 1
        async with session.post(url, json=body, headers=headers or {}) as resp:
            return await resp.json()
    
    async def forward_get(self, worker: Worker, path: str, headers: dict = None) -> dict:
        url = f"http://127.0.0.1:{worker.port}{path}"
        session = await self._get_session()
        async with session.get(url, headers=headers or {}) as resp:
            return await resp.json()
    
    async def forward_stream(self, worker: Worker, path: str, body: dict, headers: dict = None) -> AsyncGenerator[bytes, None]:
        url = f"http://127.0.0.1:{worker.port}{path}"
        session = await self._get_session()
        worker.request_count += 1
        async with session.post(url, json=body, headers=headers or {}) as resp:
            async for chunk in resp.content.iter_any():
                yield chunk
    
    def start_all(self):
        for worker_id in self.workers:
            self.start_worker(worker_id)
    
    def stop_all(self):
        for worker_id in self.workers:
            self.stop_worker(worker_id)

worker_pool = WorkerPool()

