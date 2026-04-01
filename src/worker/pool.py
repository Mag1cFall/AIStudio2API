import asyncio
import json
import logging
import os
import platform
import subprocess
import sys
import time
from typing import Any, AsyncGenerator, Callable, Dict, List, Optional

import aiohttp

try:
    from ..config.settings import DATA_DIR, PROJECT_ROOT, SAVED_AUTH_DIR
    from ..config.timeouts import RECOVERY_HOURS, KEEPALIVE_TIMEOUT
except ImportError:
    from config.settings import DATA_DIR, PROJECT_ROOT, SAVED_AUTH_DIR
    from config.timeouts import RECOVERY_HOURS, KEEPALIVE_TIMEOUT

from .models import Worker

logger = logging.getLogger("WorkerPool")

SOURCE_DIR = os.path.join(PROJECT_ROOT, "src")
LAUNCH_CAMOUFOX_PY = os.path.join(SOURCE_DIR, "launch_camoufox.py")
WORKERS_CONFIG_PATH = os.path.join(DATA_DIR, "workers.json")


class WorkerPool:
    def __init__(self):
        self.workers: Dict[str, Worker] = {}
        self._lock = asyncio.Lock()
        self.recovery_hours = RECOVERY_HOURS
        self._session: Optional[aiohttp.ClientSession] = None
        self._connector: Optional[aiohttp.TCPConnector] = None
        self._status_listeners: List[Callable[[Dict[str, Any]], Any]] = []
        self._process_listeners: List[Callable[[Worker], Any]] = []
        self._runtime_config: Dict[str, Any] = {}
        self.health_check_interval = 30
        self.health_check_failure_threshold = 3
        self.auto_restart_crashed = True
        self.startup_delay_seconds = 3
        self._restart_tasks: Dict[str, asyncio.Task] = {}

    async def _get_session(self) -> aiohttp.ClientSession:
        if self._session is None or self._session.closed:
            self._connector = aiohttp.TCPConnector(
                limit=100,
                limit_per_host=20,
                keepalive_timeout=KEEPALIVE_TIMEOUT,
                enable_cleanup_closed=True,
            )
            timeout = aiohttp.ClientTimeout(total=300, connect=10)
            self._session = aiohttp.ClientSession(
                connector=self._connector, timeout=timeout
            )
        return self._session

    async def close(self):
        for worker_id in list(self._restart_tasks):
            self._cancel_restart(worker_id)
        if self._session and not self._session.closed:
            await self._session.close()
        if self._connector:
            await self._connector.close()

    def register_status_listener(self, listener: Callable[[Dict[str, Any]], Any]):
        if listener not in self._status_listeners:
            self._status_listeners.append(listener)

    def register_process_listener(self, listener: Callable[[Worker], Any]):
        if listener not in self._process_listeners:
            self._process_listeners.append(listener)

    def configure_runtime(self, config: Optional[Dict[str, Any]] = None):
        self._runtime_config = dict(config or {})

    def load_config(self) -> dict:
        if os.path.exists(WORKERS_CONFIG_PATH):
            try:
                with open(WORKERS_CONFIG_PATH, "r", encoding="utf-8") as f:
                    return json.load(f)
            except (OSError, json.JSONDecodeError) as exc:
                logger.warning(f"加载 Worker 配置失败: {exc}")
        return {"workers": [], "settings": {"recovery_hours": RECOVERY_HOURS}}

    def save_config(self):
        config = {
            "workers": [
                {
                    "id": w.id,
                    "profile": w.profile_name,
                    "port": w.port,
                    "camoufox_port": w.camoufox_port,
                    "rate_limited_models": w.rate_limited_models,
                }
                for w in self.workers.values()
            ],
            "settings": {"recovery_hours": self.recovery_hours},
        }
        os.makedirs(DATA_DIR, exist_ok=True)
        with open(WORKERS_CONFIG_PATH, "w", encoding="utf-8") as f:
            json.dump(config, f, indent=2, ensure_ascii=False)

    def init_from_config(self):
        config = self.load_config()
        self.recovery_hours = config.get("settings", {}).get(
            "recovery_hours", RECOVERY_HOURS
        )
        current_time = time.time()
        previous_workers = self.workers
        loaded_workers: Dict[str, Worker] = {}
        valid_workers = []
        for w_cfg in config.get("workers", []):
            profile_path = os.path.join(SAVED_AUTH_DIR, w_cfg["profile"])
            if not os.path.exists(profile_path):
                profile_path = os.path.join(
                    DATA_DIR, "auth_profiles", "workers", w_cfg["profile"]
                )
            if not os.path.exists(profile_path):
                logger.warning(
                    f"跳过Worker {w_cfg['id']}: 认证文件 {w_cfg['profile']} 不存在"
                )
                continue
            worker = Worker(
                id=w_cfg["id"],
                profile_name=w_cfg["profile"],
                profile_path=profile_path,
                port=w_cfg["port"],
                camoufox_port=w_cfg["camoufox_port"],
            )
            saved_limits = w_cfg.get("rate_limited_models", {})
            for model_id, recovery_time in saved_limits.items():
                if recovery_time > current_time:
                    worker.rate_limited_models[model_id] = recovery_time
            previous_worker = previous_workers.get(worker.id)
            if previous_worker:
                worker.process = previous_worker.process
                worker.status = previous_worker.status
                worker.request_count = previous_worker.request_count
                worker.active_requests = previous_worker.active_requests
                worker.health_failures = previous_worker.health_failures
                worker.last_health_check = previous_worker.last_health_check
                worker.last_error = previous_worker.last_error
                worker.restart_count = previous_worker.restart_count
            loaded_workers[worker.id] = worker
            valid_workers.append(w_cfg)
        self.workers = loaded_workers
        if len(valid_workers) != len(config.get("workers", [])):
            config["workers"] = valid_workers
            os.makedirs(DATA_DIR, exist_ok=True)
            with open(WORKERS_CONFIG_PATH, "w", encoding="utf-8") as f:
                json.dump(config, f, indent=2, ensure_ascii=False)
        logger.info(f"Loaded {len(self.workers)} workers from config")

    def _dispatch_listener_result(self, result: Any):
        import inspect
        if not inspect.isawaitable(result):
            return
        try:
            loop = asyncio.get_running_loop()
        except RuntimeError:
            return
        loop.create_task(result)

    def _notify_status_change(self, worker: Worker, event: str):
        payload = {
            "type": "worker_status",
            "event": event,
            "worker_id": worker.id,
            "status": worker.display_status(),
            "timestamp": time.time(),
            "worker": worker.to_dict(),
        }
        for listener in list(self._status_listeners):
            try:
                result = listener(payload)
                self._dispatch_listener_result(result)
            except Exception as exc:
                logger.warning(f"通知 Worker 状态监听器失败: {exc}")

    def _notify_process_started(self, worker: Worker):
        for listener in list(self._process_listeners):
            try:
                result = listener(worker)
                self._dispatch_listener_result(result)
            except Exception as exc:
                logger.warning(f"通知 Worker 进程监听器失败: {exc}")

    def _mode_flag(self) -> str:
        mode = self._runtime_config.get("launch_mode", "headless")
        if mode == "debug":
            return "--debug"
        if mode == "virtual_headless":
            return "--virtual-display"
        return "--headless"

    def _creation_flags(self) -> int:
        return (
            subprocess.CREATE_NEW_PROCESS_GROUP if platform.system() == "Windows" else 0
        )

    def _build_env(self) -> Dict[str, str]:
        env = os.environ.copy()
        env["PYTHONUNBUFFERED"] = "1"
        env["PYTHONIOENCODING"] = "utf-8"
        env["ENABLE_SCRIPT_INJECTION"] = (
            "true"
            if self._runtime_config.get("script_injection_enabled", False)
            else "false"
        )
        return env

    def _resolve_stream_port(self, worker: Worker) -> int:
        if not self._runtime_config.get("stream_port_enabled", True):
            return 0
        stream_base = int(self._runtime_config.get("stream_port", 3120))
        if worker.id.startswith("w") and worker.id[1:].isdigit():
            return stream_base + int(worker.id[1:]) - 1
        return stream_base

    def _build_worker_command(self, worker: Worker) -> List[str]:
        cmd = [
            sys.executable,
            LAUNCH_CAMOUFOX_PY,
            self._mode_flag(),
            "--server-port",
            str(worker.port),
            "--camoufox-debug-port",
            str(worker.camoufox_port),
            "--active-auth-json",
            worker.profile_path,
        ]
        if self._runtime_config.get("proxy_enabled"):
            proxy = self._runtime_config.get("proxy_address", "")
            if proxy:
                cmd.extend(["--internal-camoufox-proxy", proxy])
        stream_port = self._resolve_stream_port(worker)
        cmd.extend(["--stream-port", str(stream_port)])
        return cmd

    def _cancel_restart(self, worker_id: str):
        task = self._restart_tasks.pop(worker_id, None)
        if task and not task.done():
            task.cancel()

    _SAFE_PROCESS_NAMES = {"python", "python3", "pythonw", "camoufox", "firefox", "uv"}

    def _free_port(self, port: int) -> None:
        try:
            if platform.system() == "Windows":
                result = subprocess.run(
                    ["netstat", "-ano"],
                    capture_output=True, text=True
                )
                for line in result.stdout.splitlines():
                    if f":{port} " in line and "LISTENING" in line:
                        parts = line.split()
                        pid = parts[-1]
                        if pid.isdigit():
                            proc_check = subprocess.run(
                                ["tasklist", "/FI", f"PID eq {pid}", "/FO", "CSV", "/NH"],
                                capture_output=True, text=True
                            )
                            proc_name = proc_check.stdout.strip().split(",")[0].strip('"').lower().replace(".exe", "")
                            if proc_name in self._SAFE_PROCESS_NAMES:
                                subprocess.run(
                                    ["taskkill", "/PID", pid, "/F"],
                                    capture_output=True
                                )
                                logger.info(f"Freed port {port} (killed {proc_name} PID {pid})")
                            else:
                                logger.warning(f"Port {port} occupied by {proc_name} (PID {pid}), skipping")
            else:
                result = subprocess.run(
                    ["lsof", "-ti", f"tcp:{port}"],
                    capture_output=True, text=True
                )
                for pid in result.stdout.strip().splitlines():
                    if pid.isdigit():
                        proc_check = subprocess.run(
                            ["ps", "-p", pid, "-o", "comm="],
                            capture_output=True, text=True
                        )
                        proc_name = proc_check.stdout.strip().lower()
                        if any(safe in proc_name for safe in self._SAFE_PROCESS_NAMES):
                            subprocess.run(["kill", "-9", pid], capture_output=True)
                            logger.info(f"Freed port {port} (killed {proc_name} PID {pid})")
                        else:
                            logger.warning(f"Port {port} occupied by {proc_name} (PID {pid}), skipping")
        except Exception as e:
            logger.warning(f"Failed to free port {port}: {e}")

    def start_worker(self, worker_id: str) -> tuple[bool, str]:
        if worker_id not in self.workers:
            return False, "Worker not found"
        worker = self.workers[worker_id]
        if (
            worker.status == "running"
            and worker.process
            and worker.process.poll() is None
        ):
            return False, "Worker already running"
        self._cancel_restart(worker_id)
        self._free_port(worker.camoufox_port)
        self._free_port(worker.port)
        stream_port = self._resolve_stream_port(worker)
        if stream_port:
            self._free_port(stream_port)
        try:
            worker.process = subprocess.Popen(
                self._build_worker_command(worker),
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                env=self._build_env(),
                cwd=SOURCE_DIR,
                creationflags=self._creation_flags(),
            )
            worker.status = "running"
            worker.active_requests = 0
            worker.health_failures = 0
            worker.last_health_check = None
            worker.last_error = None
            worker._start_time = time.time()
            self._notify_process_started(worker)
            self._notify_status_change(worker, "started")
            logger.info(f"Started worker {worker_id} on port {worker.port}")
            return True, f"Worker started on port {worker.port}"
        except Exception as exc:
            worker.process = None
            worker.status = "crashed"
            worker.last_error = str(exc)
            self._notify_status_change(worker, "start_failed")
            logger.error(f"Failed to start worker {worker_id}: {exc}")
            return False, str(exc)

    def _terminate_worker_process(self, process: Optional[subprocess.Popen]):
        if process is None:
            return
        try:
            if platform.system() == "Windows":
                subprocess.run(
                    ["taskkill", "/PID", str(process.pid), "/T", "/F"],
                    capture_output=True,
                )
            else:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
        except Exception as exc:
            logger.warning(f"Terminate worker process failed: {exc}")

    async def stop_worker(
        self,
        worker_id: str,
        graceful_timeout: float = 30.0,
        poll_interval: float = 0.25,
    ) -> tuple[bool, str]:
        if worker_id not in self.workers:
            return False, "Worker not found"
        worker = self.workers[worker_id]
        if worker.process is None or worker.status not in {"running", "crashed"}:
            worker.status = "stopped"
            worker.process = None
            worker.active_requests = 0
            self._notify_status_change(worker, "stopped")
            return True, "Worker not running"

        self._cancel_restart(worker_id)
        timed_out = False
        deadline = time.monotonic() + graceful_timeout
        while worker.active_requests > 0 and time.monotonic() < deadline:
            await asyncio.sleep(poll_interval)
        if worker.active_requests > 0:
            timed_out = True

        try:
            self._terminate_worker_process(worker.process)
            worker.process = None
            worker.status = "stopped"
            worker.active_requests = 0
            worker.health_failures = 0
            worker.last_health_check = time.time()
            worker.last_error = None
            self._notify_status_change(worker, "stopped")
            logger.info(f"Stopped worker {worker_id}")
            if timed_out:
                return True, "Worker stopped after graceful timeout"
            return True, "Worker stopped"
        except Exception as exc:
            worker.last_error = str(exc)
            logger.error(f"Failed to stop worker {worker_id}: {exc}")
            return False, str(exc)

    def force_stop_worker(self, worker_id: str) -> tuple[bool, str]:
        if worker_id not in self.workers:
            return False, "Worker not found"
        worker = self.workers[worker_id]
        self._cancel_restart(worker_id)
        try:
            self._terminate_worker_process(worker.process)
            worker.process = None
            worker.status = "stopped"
            worker.active_requests = 0
            worker.health_failures = 0
            worker.last_error = None
            self._notify_status_change(worker, "stopped")
            return True, "Worker stopped"
        except Exception as exc:
            worker.last_error = str(exc)
            return False, str(exc)

    def force_stop_all(self):
        for worker_id in list(self.workers):
            self.force_stop_worker(worker_id)

    def get_worker_for_model(self, model_id: str) -> Optional[Worker]:
        available = [
            w
            for w in self.workers.values()
            if w.status == "running" and not w.is_model_limited(model_id)
        ]
        if not available:
            return None
        return min(available, key=lambda w: (w.active_requests, w.request_count))

    def mark_rate_limited(self, worker_id: str, model_id: str):
        if worker_id in self.workers:
            worker = self.workers[worker_id]
            worker.mark_model_limited(model_id, self.recovery_hours)
            logger.warning(f"Worker {worker_id} rate limited for model {model_id}")
            self.save_config()
            self._notify_status_change(worker, "rate_limited")

    def clear_rate_limits(self, worker_id: str) -> bool:
        if worker_id in self.workers:
            worker = self.workers[worker_id]
            worker.clear_rate_limits()
            self.save_config()
            self._notify_status_change(worker, "limits_cleared")
            return True
        return False

    def get_status(self) -> List[dict]:
        return [w.to_dict() for w in self.workers.values()]

    def _begin_request(self, worker: Worker):
        worker.request_count += 1
        worker.active_requests += 1

    def _finish_request(self, worker: Worker):
        worker.active_requests = max(0, worker.active_requests - 1)

    async def forward_request(
        self, worker: Worker, path: str, body: dict, headers: dict = None
    ) -> dict:
        url = f"http://127.0.0.1:{worker.port}{path}"
        session = await self._get_session()
        self._begin_request(worker)
        try:
            async with session.post(url, json=body, headers=headers or {}) as resp:
                return await resp.json()
        finally:
            self._finish_request(worker)

    async def forward_get(
        self, worker: Worker, path: str, headers: dict = None
    ) -> dict:
        url = f"http://127.0.0.1:{worker.port}{path}"
        session = await self._get_session()
        async with session.get(url, headers=headers or {}) as resp:
            return await resp.json()

    async def forward_stream(
        self, worker: Worker, path: str, body: dict, headers: dict = None
    ) -> AsyncGenerator[bytes, None]:
        url = f"http://127.0.0.1:{worker.port}{path}"
        session = await self._get_session()
        self._begin_request(worker)
        try:
            async with session.post(url, json=body, headers=headers or {}) as resp:
                async for chunk in resp.content.iter_any():
                    yield chunk
        finally:
            self._finish_request(worker)

    async def start_all(self, stagger_seconds: Optional[float] = None):
        delay = (
            self.startup_delay_seconds if stagger_seconds is None else stagger_seconds
        )
        worker_ids = list(self.workers.keys())
        for index, worker_id in enumerate(worker_ids):
            self.start_worker(worker_id)
            if index < len(worker_ids) - 1 and delay > 0:
                await asyncio.sleep(delay)

    async def stop_all(self, graceful_timeout: float = 30.0):
        for worker_id in list(self.workers.keys()):
            await self.stop_worker(worker_id, graceful_timeout=graceful_timeout)

    async def _probe_worker_health(self, worker: Worker) -> tuple[bool, Optional[str]]:
        if worker.process is None:
            return False, "worker process missing"
        exit_code = worker.process.poll()
        if exit_code is not None:
            return False, f"process exited with code {exit_code}"
        url = f"http://127.0.0.1:{worker.port}/health"
        session = await self._get_session()
        try:
            timeout = aiohttp.ClientTimeout(total=5, connect=2)
            async with session.get(url, timeout=timeout) as response:
                if response.status != 200:
                    return False, f"/health returned {response.status}"
                await response.read()
                return True, None
        except Exception as exc:
            return False, str(exc)

    def _mark_worker_crashed(self, worker: Worker, error: Optional[str]):
        if worker.process is not None:
            self._terminate_worker_process(worker.process)
        worker.process = None
        worker.status = "crashed"
        worker.active_requests = 0
        worker.last_error = error
        self._notify_status_change(worker, "crashed")

    def _schedule_restart(self, worker_id: str, delay: Optional[float] = None):
        existing = self._restart_tasks.get(worker_id)
        if existing and not existing.done():
            return
        task = asyncio.create_task(
            self._restart_worker(
                worker_id, self.startup_delay_seconds if delay is None else delay
            )
        )
        self._restart_tasks[worker_id] = task

    async def _restart_worker(self, worker_id: str, delay: float):
        try:
            if delay > 0:
                await asyncio.sleep(delay)
            worker = self.workers.get(worker_id)
            if worker is None or worker.status != "crashed":
                return
            worker.restart_count += 1
            worker.health_failures = 0
            success, message = self.start_worker(worker_id)
            if not success:
                worker.status = "crashed"
                worker.last_error = message
                self._notify_status_change(worker, "restart_failed")
        except asyncio.CancelledError:
            raise
        finally:
            self._restart_tasks.pop(worker_id, None)

    async def health_check(
        self,
        max_failures: Optional[int] = None,
        auto_restart: Optional[bool] = None,
    ):
        threshold = (
            self.health_check_failure_threshold
            if max_failures is None
            else max_failures
        )
        restart_enabled = (
            self.auto_restart_crashed if auto_restart is None else auto_restart
        )
        for worker in list(self.workers.values()):
            if worker.status != "running":
                continue
            # Grace period: skip health check for 60s after start
            if worker.last_health_check is None and worker.process is not None:
                start_time = getattr(worker, '_start_time', None)
                if start_time is None or time.time() - start_time < 60:
                    if start_time is None:
                        worker._start_time = time.time()
                    continue
            is_healthy, error = await self._probe_worker_health(worker)
            worker.last_health_check = time.time()
            if is_healthy:
                worker.health_failures = 0
                worker.last_error = None
                continue
            worker.health_failures += 1
            worker.last_error = error
            logger.warning(
                f"Worker {worker.id} 健康检查失败 {worker.health_failures}/{threshold}: {error}"
            )
            if worker.health_failures < threshold:
                continue
            self._mark_worker_crashed(worker, error)
            if restart_enabled:
                self._schedule_restart(worker.id)

    async def health_check_loop(self):
        await asyncio.sleep(90)
        while True:
            try:
                await self.health_check()
            except asyncio.CancelledError:
                raise
            except Exception as exc:
                logger.error(f"Worker health check loop error: {exc}")
            await asyncio.sleep(self.health_check_interval)


worker_pool = WorkerPool()
