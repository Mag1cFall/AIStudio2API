import asyncio
import json
import logging
import os
import platform
import subprocess
import sys
import threading
import time
from typing import Any, Dict, List, Optional

from fastapi import WebSocket

try:
    from ..config.settings import ACTIVE_AUTH_DIR, DATA_DIR, PROJECT_ROOT
except ImportError:
    from config.settings import ACTIVE_AUTH_DIR, DATA_DIR, PROJECT_ROOT

try:
    from ..worker.pool import worker_pool

    WORKER_POOL_AVAILABLE = True
except ImportError:
    try:
        from worker.pool import worker_pool

        WORKER_POOL_AVAILABLE = True
    except ImportError:
        worker_pool = None
        WORKER_POOL_AVAILABLE = False


logger = logging.getLogger("Manager")

SOURCE_DIR = os.path.join(PROJECT_ROOT, "src")
CONFIG_FILE_PATH = os.path.join(DATA_DIR, "gui_config.json")
LAUNCH_CAMOUFOX_PY = os.path.join(SOURCE_DIR, "launch_camoufox.py")
GATEWAY_ENTRYPOINT = os.path.join(SOURCE_DIR, "gateway.py")
PYTHON_EXECUTABLE = sys.executable


class ServiceManager:
    def __init__(self):
        self.process: Optional[subprocess.Popen] = None
        self.log_queue: asyncio.Queue = asyncio.Queue(maxsize=1000)
        self.active_connections: List[WebSocket] = []
        self.output_thread: Optional[threading.Thread] = None
        self.stop_event = threading.Event()
        self.service_status = "stopped"
        self.service_info: Dict[str, Any] = {}
        self.current_launch_mode: Optional[str] = None
        self._console_print_state = "default"
        self.worker_processes: Dict[int, subprocess.Popen] = {}
        self.output_threads: Dict[int, threading.Thread] = {}
        self.is_worker_mode = False
        self._log_enabled = True
        self.loop: Optional[asyncio.AbstractEventLoop] = None

    def load_config(self) -> Dict[str, Any]:
        if os.path.exists(CONFIG_FILE_PATH):
            try:
                with open(CONFIG_FILE_PATH, "r", encoding="utf-8") as file:
                    return json.load(file)
            except Exception:
                pass
        return {
            "fastapi_port": 2048,
            "camoufox_debug_port": 40222,
            "stream_port": 3120,
            "stream_port_enabled": True,
            "proxy_enabled": False,
            "proxy_address": "http://127.0.0.1:7890",
            "helper_enabled": False,
            "helper_endpoint": "",
            "launch_mode": "headless",
            "script_injection_enabled": False,
            "worker_mode_enabled": False,
            "worker_startup_interval": 5,
            "log_enabled": True,
        }

    def save_config(self, config: Dict[str, Any]) -> bool:
        try:
            with open(CONFIG_FILE_PATH, "w", encoding="utf-8") as file:
                json.dump(config, file, indent=2, ensure_ascii=False)
            return True
        except Exception as exc:
            logger.error(f"Save config failed: {exc}")
            return False

    async def broadcast_log(self, message: str, level: str = "INFO") -> None:
        if not self.active_connections or not self._log_enabled:
            return

        log_entry = json.dumps(
            {
                "type": "log",
                "time": time.strftime("%H:%M:%S"),
                "level": level,
                "message": message,
            }
        )

        to_remove: List[WebSocket] = []

        async def send_safe(connection: WebSocket) -> None:
            try:
                await connection.send_text(log_entry)
            except Exception:
                to_remove.append(connection)

        await asyncio.gather(
            *(send_safe(connection) for connection in self.active_connections)
        )

        for connection in to_remove:
            if connection in self.active_connections:
                self.active_connections.remove(connection)

    def _broadcast_from_thread(self, message: str, level: str) -> None:
        if self.loop and not self.loop.is_closed():
            asyncio.run_coroutine_threadsafe(
                self.broadcast_log(message, level), self.loop
            )

    def _broadcast_status_from_thread(self) -> None:
        if self.loop and not self.loop.is_closed():
            asyncio.run_coroutine_threadsafe(self.broadcast_status(), self.loop)

    def _broadcast_worker_snapshot_from_thread(self) -> None:
        if self.loop and not self.loop.is_closed():
            asyncio.run_coroutine_threadsafe(
                self.broadcast_worker_snapshot(), self.loop
            )

    def prune_worker_processes(self) -> None:
        stale_pids = [
            pid
            for pid, process in self.worker_processes.items()
            if process.poll() is not None
        ]
        for pid in stale_pids:
            self.worker_processes.pop(pid, None)
            self.output_threads.pop(pid, None)

    def attach_worker_process(
        self, process: Optional[subprocess.Popen], prefix: str
    ) -> None:
        if process is None:
            return
        self.prune_worker_processes()
        if process.pid in self.worker_processes:
            return
        self.worker_processes[process.pid] = process
        thread = threading.Thread(
            target=self._monitor_output,
            args=(process, prefix),
            daemon=True,
        )
        thread.start()
        self.output_threads[process.pid] = thread

    def unregister_worker_process(self, process: Optional[subprocess.Popen]) -> None:
        if process is None:
            return
        self.worker_processes.pop(process.pid, None)
        self.output_threads.pop(process.pid, None)

    async def _broadcast_message(self, payload: Dict[str, Any]) -> None:
        if not self.active_connections:
            return
        message = json.dumps(payload)
        to_remove: List[WebSocket] = []
        for connection in self.active_connections:
            try:
                await connection.send_text(message)
            except Exception:
                to_remove.append(connection)
        for connection in to_remove:
            if connection in self.active_connections:
                self.active_connections.remove(connection)

    def refresh_worker_service_info(self) -> None:
        if not self.is_worker_mode or not WORKER_POOL_AVAILABLE or worker_pool is None:
            return
        running_count = sum(
            1 for worker in worker_pool.workers.values() if worker.status == "running"
        )
        current_port = self.service_info.get(
            "port", self.load_config().get("fastapi_port", 2048)
        )
        self.service_info = {
            "mode": "worker",
            "worker_count": running_count,
            "port": current_port,
        }

    async def broadcast_worker_event(self, event: Dict[str, Any]) -> None:
        self.refresh_worker_service_info()
        await self._broadcast_message(event)
        await self.broadcast_status()

    async def broadcast_worker_snapshot(self) -> None:
        if not WORKER_POOL_AVAILABLE or worker_pool is None:
            return
        self.refresh_worker_service_info()
        await self._broadcast_message(
            {"type": "worker_snapshot", "workers": worker_pool.get_status()}
        )

    def handle_worker_status_event(self, event: Dict[str, Any]) -> None:
        if not self.loop or self.loop.is_closed():
            return
        try:
            running_loop = asyncio.get_running_loop()
        except RuntimeError:
            running_loop = None
        if running_loop is self.loop:
            self.loop.create_task(self.broadcast_worker_event(event))
            return
        asyncio.run_coroutine_threadsafe(self.broadcast_worker_event(event), self.loop)

    def handle_worker_process_started(self, worker) -> None:
        self.attach_worker_process(worker.process, f"Worker-{worker.id}")

    def _monitor_output(self, process: subprocess.Popen, prefix: str = "Main") -> None:
        try:
            for line in iter(process.stdout.readline, b""):
                if self.stop_event.is_set():
                    break
                if not line:
                    continue

                decoded_line = line.decode("utf-8", errors="replace").strip()

                if self.current_launch_mode == "debug":
                    if (
                        self._console_print_state == "default"
                        and "找到以下可用的认证文件" in decoded_line
                    ):
                        self._console_print_state = "printing_auth"

                    if self._console_print_state == "printing_auth":
                        print(decoded_line, flush=True)
                        if (
                            "好的，不加载认证文件或超时" in decoded_line
                            or "已选择加载" in decoded_line
                            or "将使用默认值" in decoded_line
                        ):
                            self._console_print_state = "default"
                    elif (
                        "==================== 需要操作 ===================="
                        in decoded_line
                    ):
                        print(
                            "==================== 需要操作 ====================",
                            flush=True,
                        )
                    elif "__USER_INPUT_START__" in decoded_line:
                        print("__USER_INPUT_START__", flush=True)
                    elif "检测到可能需要登录" in decoded_line:
                        print(
                            "检测到可能需要登录。如果浏览器显示登录页面，请在浏览器窗口中完成 Google 登录，然后在此处按 Enter 键继续...",
                            flush=True,
                        )
                    elif "__USER_INPUT_END__" in decoded_line:
                        print("__USER_INPUT_END__", flush=True)

                level = "INFO"
                upper_line = decoded_line.upper()
                if (
                    "ERROR" in upper_line
                    or "EXCEPTION" in upper_line
                    or "CRITICAL" in upper_line
                ):
                    level = "ERROR"
                elif "WARN" in upper_line:
                    level = "WARN"
                elif "DEBUG" in upper_line:
                    level = "DEBUG"

                log_msg = (
                    f"[{prefix}] {decoded_line}" if prefix != "Main" else decoded_line
                )
                self._broadcast_from_thread(log_msg, level)
        except Exception as exc:
            logger.error(f"Output monitor error ({prefix}): {exc}")
        finally:
            if not self.is_worker_mode:
                self.service_status = "stopped"
                self._broadcast_status_from_thread()
            else:
                self.unregister_worker_process(process)

    async def broadcast_status(self) -> None:
        self.refresh_worker_service_info()
        status_msg = json.dumps(
            {
                "type": "status",
                "status": self.service_status,
                "info": self.service_info,
            }
        )
        to_remove: List[WebSocket] = []
        for connection in self.active_connections:
            try:
                await connection.send_text(status_msg)
            except Exception:
                to_remove.append(connection)

        for connection in to_remove:
            if connection in self.active_connections:
                self.active_connections.remove(connection)

    async def start_service(self, config: Dict[str, Any]) -> tuple[bool, str]:
        self.prune_worker_processes()
        if self.process and self.process.poll() is None:
            return False, "服务已在运行"
        if self.service_status == "running" and self.is_worker_mode:
            return False, "Worker模式已在运行"

        self.service_status = "starting"
        self.stop_event.clear()
        self._console_print_state = "default"

        mode = config.get("launch_mode", "headless")
        self.current_launch_mode = mode
        mode_flag = "--headless"
        if mode == "debug":
            mode_flag = "--debug"
        elif mode == "virtual_headless":
            mode_flag = "--virtual-display"

        env = os.environ.copy()
        env["PYTHONUNBUFFERED"] = "1"
        env["PYTHONIOENCODING"] = "utf-8"
        env["ENABLE_SCRIPT_INJECTION"] = (
            "true" if config.get("script_injection_enabled", False) else "false"
        )

        creationflags = (
            subprocess.CREATE_NEW_PROCESS_GROUP if platform.system() == "Windows" else 0
        )

        if config.get("worker_mode_enabled", False):
            return await self._start_worker_mode(config, mode_flag, env, creationflags)
        return self._start_single_mode(config, mode_flag, env, creationflags)

    def _start_single_mode(
        self,
        config: Dict[str, Any],
        mode_flag: str,
        env: Dict[str, str],
        creationflags: int,
    ) -> tuple[bool, str]:
        self.is_worker_mode = False
        cmd = [
            PYTHON_EXECUTABLE,
            LAUNCH_CAMOUFOX_PY,
            mode_flag,
            "--server-port",
            str(config.get("fastapi_port", 2048)),
            "--camoufox-debug-port",
            str(config.get("camoufox_debug_port", 40222)),
        ]

        if config.get("proxy_enabled"):
            proxy = config.get("proxy_address", "")
            if proxy:
                cmd.extend(["--internal-camoufox-proxy", proxy])

        if config.get("stream_port_enabled"):
            cmd.extend(["--stream-port", str(config.get("stream_port", 3120))])
        else:
            cmd.extend(["--stream-port", "0"])

        if config.get("helper_enabled") and config.get("helper_endpoint"):
            cmd.extend(["--helper", config.get("helper_endpoint")])

        if os.path.exists(ACTIVE_AUTH_DIR):
            files = [
                file_name
                for file_name in os.listdir(ACTIVE_AUTH_DIR)
                if file_name.endswith(".json")
            ]
            if files:
                cmd.extend(
                    ["--active-auth-json", os.path.join(ACTIVE_AUTH_DIR, files[0])]
                )

        try:
            self.process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                env=env,
                cwd=SOURCE_DIR,
                creationflags=creationflags,
            )
            self.service_info = {
                "pid": self.process.pid,
                "port": config.get("fastapi_port", 2048),
                "mode": "single",
            }
            self.output_thread = threading.Thread(
                target=self._monitor_output,
                args=(self.process, "Main"),
                daemon=True,
            )
            self.output_thread.start()
            self.service_status = "running"
            return True, "服务启动成功"
        except Exception as exc:
            self.service_status = "stopped"
            logger.error(f"启动失败: {exc}")
            return False, str(exc)

    async def _start_worker_mode(
        self,
        config: Dict[str, Any],
        mode_flag: str,
        env: Dict[str, str],
        creationflags: int,
    ) -> tuple[bool, str]:
        if not WORKER_POOL_AVAILABLE or worker_pool is None:
            self.service_status = "stopped"
            return False, "Worker pool not available"

        self.is_worker_mode = True

        worker_pool.init_from_config()
        worker_pool.configure_runtime(config)
        if not worker_pool.workers:
            self.service_status = "stopped"
            return False, "未找到Worker配置，请先在Worker管理中添加Worker"

        self.worker_processes = {}
        self.output_threads = {}
        started_count = 0
        worker_ids = list(worker_pool.workers.keys())
        for index, worker_id in enumerate(worker_ids):
            if self.stop_event.is_set():
                logger.info("启动过程中收到停止信号，中断Worker启动")
                break
            success, message = worker_pool.start_worker(worker_id)
            worker = worker_pool.workers[worker_id]
            if not success or worker.process is None:
                logger.error(f"启动Worker {worker_id}失败: {message}")
                continue
            started_count += 1
            logger.info(f"启动Worker {worker_id} (端口:{worker.port})")
            if index < len(worker_ids) - 1:
                await asyncio.sleep(config.get("worker_startup_interval", 5))

        if self.stop_event.is_set():
            self.service_status = "stopped"
            self.is_worker_mode = False
            return False, "启动过程已被中断"

        if started_count == 0:
            self.service_status = "stopped"
            return False, "所有Worker启动失败"

        gateway_port = config.get("fastapi_port", 2048)
        gateway_cmd = [
            PYTHON_EXECUTABLE,
            GATEWAY_ENTRYPOINT,
            "--port",
            str(gateway_port),
        ]

        try:
            gateway_process = subprocess.Popen(
                gateway_cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                env=env,
                cwd=SOURCE_DIR,
                creationflags=creationflags,
            )
            self.attach_worker_process(gateway_process, "Gateway")
            logger.info(f"启动Gateway (端口:{gateway_port})")
        except Exception as exc:
            logger.error(f"启动Gateway失败: {exc}")

        self.service_info = {
            "mode": "worker",
            "worker_count": started_count,
            "port": gateway_port,
        }
        self.service_status = "running"
        return (
            True,
            f"Worker模式启动成功，共{started_count}个Worker，网关端口{gateway_port}",
        )

    def stop_service(self) -> tuple[bool, str]:
        self.prune_worker_processes()
        if not self.process and not self.worker_processes:
            return True, "服务未运行"

        self.service_status = "stopping"
        self.stop_event.set()

        def kill_process(process: subprocess.Popen) -> None:
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
            except Exception:
                pass

        try:
            if self.is_worker_mode:
                processes = list(self.worker_processes.values())
                if processes:
                    from concurrent.futures import ThreadPoolExecutor
                    with ThreadPoolExecutor(
                        max_workers=max(1, len(processes))
                    ) as executor:
                        executor.map(kill_process, processes)
                self.worker_processes = {}
                self.output_threads = {}
                self.is_worker_mode = False
                if WORKER_POOL_AVAILABLE and worker_pool is not None:
                    worker_pool.force_stop_all()
            elif self.process:
                kill_process(self.process)
                self.process = None

            self.service_status = "stopped"
            self.service_info = {}
            return True, "服务已停止"
        except Exception as exc:
            return False, str(exc)

    def check_port_usage(self, port: int) -> List[Dict[str, Any]]:
        pids = set()
        system = platform.system()
        try:
            if system == "Windows":
                command = f'netstat -ano -p TCP | findstr ":{port} "'
                result = subprocess.run(
                    command, shell=True, capture_output=True, text=True
                )
                if result.returncode == 0:
                    for line in result.stdout.strip().splitlines():
                        parts = line.split()
                        if len(parts) >= 5 and str(port) in parts[1]:
                            pids.add(int(parts[-1]))
            else:
                command = f"lsof -ti :{port} -sTCP:LISTEN"
                result = subprocess.run(
                    command, shell=True, capture_output=True, text=True
                )
                if result.returncode == 0:
                    for pid in result.stdout.strip().splitlines():
                        if pid.isdigit():
                            pids.add(int(pid))
        except Exception as exc:
            logger.error(f"Port check failed: {exc}")

        processes = []
        for pid in pids:
            name = "Unknown"
            try:
                if system == "Windows":
                    result = subprocess.run(
                        f'tasklist /FI "PID eq {pid}" /NH /FO CSV',
                        capture_output=True,
                        text=True,
                    )
                    if result.stdout.strip():
                        name = result.stdout.strip().split(",")[0].strip('"')
                else:
                    result = subprocess.run(
                        f"ps -p {pid} -o comm=",
                        shell=True,
                        capture_output=True,
                        text=True,
                    )
                    name = result.stdout.strip()
            except Exception:
                pass
            processes.append({"pid": pid, "name": name})
        return processes

    def kill_process(self, pid: int) -> tuple[bool, str]:
        system = platform.system()
        try:
            if system == "Windows":
                subprocess.run(
                    ["taskkill", "/PID", str(pid), "/T", "/F"],
                    check=True,
                    capture_output=True,
                )
            else:
                subprocess.run(
                    ["kill", "-9", str(pid)], check=True, capture_output=True
                )
            return True, "Process killed"
        except subprocess.CalledProcessError as exc:
            return False, str(exc)
        except Exception as exc:
            return False, str(exc)


manager = ServiceManager()
