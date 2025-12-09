import os
import sys
import json
import time
import signal
import socket
import logging
import asyncio
import threading
import subprocess
import platform
from typing import Optional, List, Dict, Any
from contextlib import asynccontextmanager

from fastapi import FastAPI, WebSocket, WebSocketDisconnect, HTTPException, Body
from fastapi.responses import FileResponse, JSONResponse
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s')
logger = logging.getLogger('Manager')

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_ROOT = os.path.dirname(SCRIPT_DIR)
DATA_DIR = os.path.join(PROJECT_ROOT, 'data')
CONFIG_FILE_PATH = os.path.join(DATA_DIR, 'gui_config.json')
LAUNCH_CAMOUFOX_PY = os.path.join(SCRIPT_DIR, 'launch_camoufox.py')
PYTHON_EXECUTABLE = sys.executable

class ServiceManager:
    def __init__(self):
        self.process: Optional[subprocess.Popen] = None
        self.log_queue: asyncio.Queue = asyncio.Queue()
        self.active_connections: List[WebSocket] = []
        self.output_thread: Optional[threading.Thread] = None
        self.stop_event = threading.Event()
        self.service_status = "stopped"
        self.service_info = {}
        self.current_launch_mode = None
        self._console_print_state = "default"
        self.worker_processes: List[subprocess.Popen] = []
        self.output_threads: List[threading.Thread] = []
        self.is_worker_mode = False

    def load_config(self):
        if os.path.exists(CONFIG_FILE_PATH):
            try:
                with open(CONFIG_FILE_PATH, 'r', encoding='utf-8') as f:
                    return json.load(f)
            except:
                pass
        return {
            'fastapi_port': 2048,
            'camoufox_debug_port': 9222,
            'stream_port': 3120,
            'stream_port_enabled': True,
            'proxy_enabled': False,
            'proxy_address': 'http://127.0.0.1:7890',
            'helper_enabled': False,
            'helper_endpoint': '',
            'launch_mode': 'headless',
            'script_injection_enabled': False,
            'worker_mode_enabled': False
        }

    def save_config(self, config):
        try:
            with open(CONFIG_FILE_PATH, 'w', encoding='utf-8') as f:
                json.dump(config, f, indent=2, ensure_ascii=False)
            return True
        except Exception as e:
            logger.error(f"Save config failed: {e}")
            return False

    async def broadcast_log(self, message: str, level: str = "INFO"):
        timestamp = time.strftime("%H:%M:%S")
        log_entry = json.dumps({
            "type": "log",
            "time": timestamp,
            "level": level,
            "message": message
        })
        
        if not self.active_connections:
            return

        to_remove = []
        
        async def send_safe(connection):
            try:
                await connection.send_text(log_entry)
            except:
                to_remove.append(connection)

        await asyncio.gather(*(send_safe(c) for c in self.active_connections))
        
        for c in to_remove:
            if c in self.active_connections:
                self.active_connections.remove(c)

    def _monitor_output(self, process, prefix="Main"):
        try:
            for line in iter(process.stdout.readline, b''):
                if self.stop_event.is_set(): break
                if line:
                    decoded_line = line.decode('utf-8', errors='replace').strip()
                    
                    if self.current_launch_mode == 'debug':
                        if self._console_print_state == "default" and "找到以下可用的认证文件" in decoded_line:
                            self._console_print_state = "printing_auth"

                        if self._console_print_state == "printing_auth":
                            print(decoded_line, flush=True)
                            if "好的，不加载认证文件或超时" in decoded_line or "已选择加载" in decoded_line or "将使用默认值" in decoded_line:
                                self._console_print_state = "default"
                        
                        elif "==================== 需要操作 ====================" in decoded_line:
                            print("==================== 需要操作 ====================", flush=True)
                        elif "__USER_INPUT_START__" in decoded_line:
                            print("__USER_INPUT_START__", flush=True)
                        elif "检测到可能需要登录" in decoded_line:
                            print("检测到可能需要登录。如果浏览器显示登录页面，请在浏览器窗口中完成 Google 登录，然后在此处按 Enter 键继续...", flush=True)
                        elif "__USER_INPUT_END__" in decoded_line:
                            print("__USER_INPUT_END__", flush=True)

                    level = "INFO"
                    upper_line = decoded_line.upper()
                    if "ERROR" in upper_line or "EXCEPTION" in upper_line or "CRITICAL" in upper_line:
                        level = "ERROR"
                    elif "WARN" in upper_line:
                        level = "WARN"
                    elif "DEBUG" in upper_line:
                        level = "DEBUG"
                    
                    log_msg = f"[{prefix}] {decoded_line}" if prefix != "Main" else decoded_line
                    asyncio.run_coroutine_threadsafe(
                        self.broadcast_log(log_msg, level), loop
                    )
        except Exception as e:
            logger.error(f"Output monitor error ({prefix}): {e}")
        finally:
            if not self.is_worker_mode:
                self.service_status = "stopped"
                asyncio.run_coroutine_threadsafe(
                    self.broadcast_status(), loop
                )

    async def broadcast_status(self):
        status_msg = json.dumps({
            "type": "status",
            "status": self.service_status,
            "info": self.service_info
        })
        for connection in self.active_connections:
            try:
                await connection.send_text(status_msg)
            except:
                pass

    def start_service(self, config):
        if self.process and self.process.poll() is None:
            return False, "服务已在运行"
        if self.worker_processes:
            return False, "Worker模式已在运行"

        self.service_status = "starting"
        self.stop_event.clear()
        self._console_print_state = "default"
        
        mode = config.get('launch_mode', 'headless')
        self.current_launch_mode = mode
        mode_flag = '--headless'
        if mode == 'debug':
            mode_flag = '--debug'
        elif mode == 'virtual_headless':
            mode_flag = '--virtual-display'

        env = os.environ.copy()
        env['PYTHONUNBUFFERED'] = '1'
        env['PYTHONIOENCODING'] = 'utf-8'
        env['ENABLE_SCRIPT_INJECTION'] = 'true' if config.get('script_injection_enabled', False) else 'false'
        
        if platform.system() == 'Windows':
            creationflags = subprocess.CREATE_NEW_PROCESS_GROUP
        else:
            creationflags = 0

        if config.get('worker_mode_enabled', False):
            return self._start_worker_mode(config, mode_flag, env, creationflags)
        else:
            return self._start_single_mode(config, mode_flag, env, creationflags)

    def _start_single_mode(self, config, mode_flag, env, creationflags):
        self.is_worker_mode = False
        cmd = [
            PYTHON_EXECUTABLE,
            LAUNCH_CAMOUFOX_PY,
            mode_flag,
            '--server-port', str(config.get('fastapi_port', 2048)),
            '--camoufox-debug-port', str(config.get('camoufox_debug_port', 9222))
        ]
        
        if config.get('proxy_enabled'):
            proxy = config.get('proxy_address', '')
            if proxy:
                cmd.extend(['--internal-camoufox-proxy', proxy])
        
        if config.get('stream_port_enabled'):
            cmd.extend(['--stream-port', str(config.get('stream_port', 3120))])
        else:
            cmd.extend(['--stream-port', '0'])

        if config.get('helper_enabled') and config.get('helper_endpoint'):
            cmd.extend(['--helper', config.get('helper_endpoint')])

        active_dir = os.path.join(DATA_DIR, 'auth_profiles', 'active')
        if os.path.exists(active_dir):
            files = [f for f in os.listdir(active_dir) if f.endswith('.json')]
            if files:
                cmd.extend(['--active-auth-json', os.path.join(active_dir, files[0])])

        try:
            self.process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                env=env,
                cwd=SCRIPT_DIR,
                creationflags=creationflags
            )
            
            self.service_info = {
                "pid": self.process.pid,
                "port": config.get('fastapi_port', 2048),
                "mode": "single"
            }
            
            self.output_thread = threading.Thread(
                target=self._monitor_output, 
                args=(self.process, "Main"), 
                daemon=True
            )
            self.output_thread.start()
            
            self.service_status = "running"
            return True, "服务启动成功"
        except Exception as e:
            self.service_status = "stopped"
            logger.error(f"启动失败: {e}")
            return False, str(e)

    def _start_worker_mode(self, config, mode_flag, env, creationflags):
        from worker.pool import worker_pool
        from concurrent.futures import ThreadPoolExecutor, as_completed
        self.is_worker_mode = True
        
        worker_pool.init_from_config()
        if not worker_pool.workers:
            self.service_status = "stopped"
            return False, "未找到Worker配置，请先在Worker管理中添加Worker"
        
        self.worker_processes = []
        self.output_threads = []
        
        def start_single_worker(worker_id, worker):
            cmd = [
                PYTHON_EXECUTABLE,
                LAUNCH_CAMOUFOX_PY,
                mode_flag,
                '--server-port', str(worker.port),
                '--camoufox-debug-port', str(worker.camoufox_port),
                '--active-auth-json', worker.profile_path
            ]
            
            if config.get('proxy_enabled'):
                proxy = config.get('proxy_address', '')
                if proxy:
                    cmd.extend(['--internal-camoufox-proxy', proxy])
            
            if config.get('stream_port_enabled'):
                stream_base = config.get('stream_port', 3120)
                worker_stream_port = stream_base + int(worker_id.replace('w', '')) - 1
                cmd.extend(['--stream-port', str(worker_stream_port)])
            else:
                cmd.extend(['--stream-port', '0'])
            
            try:
                proc = subprocess.Popen(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    env=env,
                    cwd=SCRIPT_DIR,
                    creationflags=creationflags
                )
                worker.process = proc
                worker.status = "running"
                return (worker_id, proc, None)
            except Exception as e:
                return (worker_id, None, str(e))
        
        started_count = 0
        with ThreadPoolExecutor(max_workers=len(worker_pool.workers)) as executor:
            futures = {executor.submit(start_single_worker, wid, w): wid for wid, w in worker_pool.workers.items()}
            for future in as_completed(futures):
                worker_id, proc, error = future.result()
                if proc:
                    self.worker_processes.append(proc)
                    thread = threading.Thread(
                        target=self._monitor_output,
                        args=(proc, f"Worker-{worker_id}"),
                        daemon=True
                    )
                    thread.start()
                    self.output_threads.append(thread)
                    started_count += 1
                    logger.info(f"启动Worker {worker_id} (端口:{worker_pool.workers[worker_id].port})")
                else:
                    logger.error(f"启动Worker {worker_id}失败: {error}")
        
        if started_count == 0:
            self.service_status = "stopped"
            return False, "所有Worker启动失败"
        
        gateway_port = config.get('fastapi_port', 2048)
        gateway_cmd = [
            PYTHON_EXECUTABLE,
            os.path.join(SCRIPT_DIR, 'gateway.py'),
            '--port', str(gateway_port)
        ]
        
        try:
            gateway_proc = subprocess.Popen(
                gateway_cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                env=env,
                cwd=SCRIPT_DIR,
                creationflags=creationflags
            )
            self.worker_processes.append(gateway_proc)
            
            gateway_thread = threading.Thread(
                target=self._monitor_output,
                args=(gateway_proc, "Gateway"),
                daemon=True
            )
            gateway_thread.start()
            self.output_threads.append(gateway_thread)
            
            logger.info(f"启动Gateway (端口:{gateway_port})")
        except Exception as e:
            logger.error(f"启动Gateway失败: {e}")
        
        self.service_info = {
            "mode": "worker",
            "worker_count": started_count,
            "port": gateway_port
        }
        self.service_status = "running"
        return True, f"Worker模式启动成功，共{started_count}个Worker，网关端口{gateway_port}"

    def stop_service(self):
        if not self.process and not self.worker_processes:
            return True, "服务未运行"
        
        self.service_status = "stopping"
        self.stop_event.set()
        
        def kill_process(proc):
            try:
                if platform.system() == 'Windows':
                    subprocess.run(['taskkill', '/PID', str(proc.pid), '/T', '/F'], capture_output=True)
                else:
                    proc.terminate()
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        proc.kill()
            except:
                pass
        
        try:
            if self.is_worker_mode:
                from concurrent.futures import ThreadPoolExecutor
                with ThreadPoolExecutor(max_workers=len(self.worker_processes)) as executor:
                    executor.map(kill_process, self.worker_processes)
                self.worker_processes = []
                self.output_threads = []
                self.is_worker_mode = False
                from worker.pool import worker_pool
                for w in worker_pool.workers.values():
                    w.status = "stopped"
                    w.process = None
            else:
                if self.process:
                    kill_process(self.process)
                    self.process = None
            
            self.service_status = "stopped"
            return True, "服务已停止"
        except Exception as e:
            return False, str(e)

    def check_port_usage(self, port: int) -> List[Dict[str, Any]]:
        """检测端口占用情况，返回 PID 和进程名列表"""
        pids = set()
        system = platform.system()
        try:
            if system == 'Windows':
                cmd = f'netstat -ano -p TCP | findstr ":{port} "'
                res = subprocess.run(cmd, shell=True, capture_output=True, text=True)
                if res.returncode == 0:
                    for line in res.stdout.strip().splitlines():
                        parts = line.split()
                        if len(parts) >= 5 and str(port) in parts[1]:
                            pids.add(int(parts[-1]))
            else:
                cmd = f'lsof -ti :{port} -sTCP:LISTEN'
                res = subprocess.run(cmd, shell=True, capture_output=True, text=True)
                if res.returncode == 0:
                    for p in res.stdout.strip().splitlines():
                        if p.isdigit(): pids.add(int(p))
        except Exception as e:
            logger.error(f"Port check failed: {e}")

        result = []
        for pid in pids:
            name = "Unknown"
            try:
                if system == 'Windows':
                    r = subprocess.run(f'tasklist /FI "PID eq {pid}" /NH /FO CSV', capture_output=True, text=True)
                    if r.stdout.strip():
                        name = r.stdout.strip().split(',')[0].strip('"')
                else:
                    r = subprocess.run(f'ps -p {pid} -o comm=', shell=True, capture_output=True, text=True)
                    name = r.stdout.strip()
            except: pass
            result.append({"pid": pid, "name": name})
        return result

    def kill_process(self, pid: int) -> tuple[bool, str]:
        """强制终止指定进程"""
        system = platform.system()
        try:
            if system == 'Windows':
                subprocess.run(['taskkill', '/PID', str(pid), '/T', '/F'], check=True, capture_output=True)
            else:
                subprocess.run(['kill', '-9', str(pid)], check=True, capture_output=True)
            return True, "Process killed"
        except subprocess.CalledProcessError as e:
            return False, str(e)
        except Exception as e:
            return False, str(e)

manager = ServiceManager()
loop = None

@asynccontextmanager
async def lifespan(app: FastAPI):
    global loop
    loop = asyncio.get_running_loop()
    if WORKER_POOL_AVAILABLE:
        worker_pool.init_from_config()
    yield
    if manager.process:
        manager.stop_service()

app = FastAPI(lifespan=lifespan)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

STATIC_DIR = os.path.join(SCRIPT_DIR, 'static')
os.makedirs(DATA_DIR, exist_ok=True)
app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")

@app.get("/")
async def read_root():
    return FileResponse(os.path.join(STATIC_DIR, 'dashboard.html'))

@app.get("/api/config")
async def get_config():
    return manager.load_config()

@app.post("/api/config")
async def save_config(config: Dict[str, Any] = Body(...)):
    if manager.save_config(config):
        return {"success": True}
    raise HTTPException(status_code=500, detail="保存失败")

@app.get("/api/status")
async def get_status():
    return {
        "status": manager.service_status,
        "info": manager.service_info
    }

@app.post("/api/control/start")
async def start_service(config: Dict[str, Any] = Body(...)):
    success, msg = manager.start_service(config)
    await manager.broadcast_status()
    if not success:
        raise HTTPException(status_code=400, detail=msg)
    return {"success": True, "message": msg}

@app.post("/api/control/stop")
async def stop_service():
    success, msg = manager.stop_service()
    await manager.broadcast_status()
    if not success:
        raise HTTPException(status_code=500, detail=msg)
    return {"success": True, "message": msg}

@app.get("/api/system/ports")
async def check_all_ports():
    config = manager.load_config()
    ports_to_check = [
        {"label": "FastAPI 服务", "port": config.get('fastapi_port', 2048)},
        {"label": "Camoufox 调试", "port": config.get('camoufox_debug_port', 9222)},
    ]
    
    if config.get('stream_port_enabled'):
        ports_to_check.append({"label": "流式代理", "port": config.get('stream_port', 3120)})
        
    results = []
    for item in ports_to_check:
        usage = manager.check_port_usage(item['port'])
        results.append({
            "label": item['label'],
            "port": item['port'],
            "in_use": len(usage) > 0,
            "processes": usage
        })
    return results

@app.get("/api/system/port/{port}")
async def check_port(port: int):
    usage = manager.check_port_usage(port)
    return {"port": port, "in_use": len(usage) > 0, "processes": usage}

@app.post("/api/system/kill/{pid}")
async def kill_process(pid: int):
    success, msg = manager.kill_process(pid)
    if not success:
        raise HTTPException(status_code=500, detail=msg)
    return {"success": True}

@app.get("/api/auth/files")
async def list_auth_files():
    profiles_dir = os.path.join(DATA_DIR, 'auth_profiles')
    active_dir = os.path.join(profiles_dir, 'active')
    saved_dir = os.path.join(profiles_dir, 'saved')
    
    active_file = None
    if os.path.exists(active_dir):
        files = [f for f in os.listdir(active_dir) if f.endswith('.json')]
        if files: active_file = files[0]
            
    saved_files = []
    if os.path.exists(saved_dir):
        saved_files = [f for f in os.listdir(saved_dir) if f.endswith('.json')]
        
    return {
        "active": active_file,
        "saved": saved_files
    }

@app.post("/api/auth/activate")
async def activate_auth(filename: str = Body(..., embed=True)):
    profiles_dir = os.path.join(DATA_DIR, 'auth_profiles')
    active_dir = os.path.join(profiles_dir, 'active')
    saved_dir = os.path.join(profiles_dir, 'saved')
    
    os.makedirs(active_dir, exist_ok=True)
    
    for f in os.listdir(active_dir):
        if f.endswith('.json'):
            os.remove(os.path.join(active_dir, f))
            
    src = os.path.join(saved_dir, filename)
    if not os.path.exists(src):
        src = os.path.join(active_dir, filename)
        if not os.path.exists(src):
             raise HTTPException(status_code=404, detail="文件不存在")
    
    import shutil
    shutil.copy2(src, os.path.join(active_dir, filename))
    return {"success": True}

@app.post("/api/auth/deactivate")
async def deactivate_auth():
    profiles_dir = os.path.join(DATA_DIR, 'auth_profiles')
    active_dir = os.path.join(profiles_dir, 'active')
    
    if os.path.exists(active_dir):
        for f in os.listdir(active_dir):
            if f.endswith('.json'):
                try:
                    os.remove(os.path.join(active_dir, f))
                except Exception as e:
                    raise HTTPException(status_code=500, detail=f"删除文件失败: {e}")
    return {"success": True}

@app.post("/api/auth/rename")
async def rename_auth(old_name: str = Body(..., embed=True), new_name: str = Body(..., embed=True)):
    profiles_dir = os.path.join(DATA_DIR, 'auth_profiles')
    saved_dir = os.path.join(profiles_dir, 'saved')
    
    if not new_name.endswith('.json'):
        new_name = new_name + '.json'
    
    old_path = os.path.join(saved_dir, old_name)
    new_path = os.path.join(saved_dir, new_name)
    
    if not os.path.exists(old_path):
        raise HTTPException(status_code=404, detail="文件不存在")
    if os.path.exists(new_path):
        raise HTTPException(status_code=400, detail="目标文件名已存在")
    
    try:
        os.rename(old_path, new_path)
        return {"success": True, "new_name": new_name}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

try:
    from worker.pool import worker_pool
    WORKER_POOL_AVAILABLE = True
except ImportError:
    WORKER_POOL_AVAILABLE = False
    worker_pool = None

@app.post("/api/workers/add")
async def add_worker(profile: str = Body(..., embed=True)):
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    
    existing_ids = [int(w.id[1:]) for w in worker_pool.workers.values() if w.id.startswith('w') and w.id[1:].isdigit()]
    next_id = max(existing_ids, default=0) + 1
    worker_id = f"w{next_id}"
    
    existing_ports = [w.port for w in worker_pool.workers.values()]
    existing_camoufox = [w.camoufox_port for w in worker_pool.workers.values()]
    port = max(existing_ports, default=3000) + 1
    camoufox_port = max(existing_camoufox, default=9221) + 1
    
    profiles_dir = os.path.join(DATA_DIR, 'auth_profiles')
    profile_path = os.path.join(profiles_dir, 'saved', profile)
    if not os.path.exists(profile_path):
        raise HTTPException(status_code=404, detail="Profile文件不存在")
    
    from worker.models import Worker
    worker = Worker(
        id=worker_id,
        profile_name=profile,
        profile_path=profile_path,
        port=port,
        camoufox_port=camoufox_port
    )
    worker_pool.workers[worker_id] = worker
    worker_pool.save_config()
    
    return {"success": True, "worker": worker.to_dict()}

@app.delete("/api/workers/{worker_id}")
async def remove_worker(worker_id: str):
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    
    if worker_id not in worker_pool.workers:
        raise HTTPException(status_code=404, detail="Worker not found")
    
    worker = worker_pool.workers[worker_id]
    if worker.status == "running":
        worker_pool.stop_worker(worker_id)
    
    del worker_pool.workers[worker_id]
    worker_pool.save_config()
    
    return {"success": True}

@app.get("/api/workers")
async def list_workers():
    if not WORKER_POOL_AVAILABLE:
        return []
    if not worker_pool.workers:
        worker_pool.init_from_config()
    return worker_pool.get_status()

@app.post("/api/workers/init")
async def init_workers():
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    worker_pool.init_from_config()
    return {"success": True, "count": len(worker_pool.workers)}

@app.post("/api/workers/save")
async def save_workers_config():
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    try:
        worker_pool.save_config()
        return {"success": True, "count": len(worker_pool.workers)}
    except Exception as e:
        return {"success": False, "error": str(e)}

@app.post("/api/workers/{worker_id}/start")
async def start_worker_api(worker_id: str):
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    success, msg = worker_pool.start_worker(worker_id)
    if not success:
        raise HTTPException(status_code=400, detail=msg)
    return {"success": True, "message": msg}

@app.post("/api/workers/{worker_id}/stop")
async def stop_worker_api(worker_id: str):
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    success, msg = worker_pool.stop_worker(worker_id)
    if not success:
        raise HTTPException(status_code=400, detail=msg)
    return {"success": True, "message": msg}

@app.post("/api/workers/{worker_id}/clear-limits")
async def clear_worker_limits(worker_id: str):
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    if worker_pool.clear_rate_limits(worker_id):
        return {"success": True}
    raise HTTPException(status_code=404, detail="Worker not found")

@app.post("/api/workers/start-all")
async def start_all_workers():
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    worker_pool.start_all()
    return {"success": True}

@app.post("/api/workers/stop-all")
async def stop_all_workers():
    if not WORKER_POOL_AVAILABLE:
        raise HTTPException(status_code=500, detail="Worker pool not available")
    worker_pool.stop_all()
    return {"success": True}

from fastapi import Request
from fastapi.responses import StreamingResponse

@app.post("/v1/chat/completions")
async def gateway_chat_completions(request: Request):
    if not WORKER_POOL_AVAILABLE or not worker_pool.workers:
        raise HTTPException(status_code=503, detail="No workers available")
    
    body = await request.json()
    model_id = body.get("model", "")
    is_stream = body.get("stream", False)
    
    worker = worker_pool.get_worker_for_model(model_id)
    if not worker:
        all_limited = all(w.is_model_limited(model_id) for w in worker_pool.workers.values() if w.status == "running")
        if all_limited:
            raise HTTPException(status_code=429, detail=f"All workers rate limited for model {model_id}")
        raise HTTPException(status_code=503, detail="No available workers")
    
    headers = {k: v for k, v in request.headers.items() if k.lower() not in ('host', 'content-length')}
    
    if is_stream:
        async def stream_generator():
            async for chunk in worker_pool.forward_stream(worker, "/v1/chat/completions", body, headers):
                yield chunk
        return StreamingResponse(stream_generator(), media_type="text/event-stream")
    else:
        result = await worker_pool.forward_request(worker, "/v1/chat/completions", body, headers)
        return JSONResponse(content=result)

@app.get("/v1/models")
async def gateway_models():
    if not WORKER_POOL_AVAILABLE or not worker_pool.workers:
        raise HTTPException(status_code=503, detail="No workers available")
    
    for worker in worker_pool.workers.values():
        if worker.status == "running":
            try:
                result = await worker_pool.forward_get(worker, "/v1/models")
                return JSONResponse(content=result)
            except:
                continue
    raise HTTPException(status_code=503, detail="No workers responding")

@app.websocket("/ws/logs")
async def websocket_logs(websocket: WebSocket):
    await websocket.accept()
    manager.active_connections.append(websocket)
    try:
        await websocket.send_text(json.dumps({
            "type": "status", 
            "status": manager.service_status,
            "info": manager.service_info
        }))
        while True:
            await websocket.receive_text()
    except WebSocketDisconnect:
        manager.active_connections.remove(websocket)

if __name__ == '__main__':
    import uvicorn
    host = os.environ.get('MANAGER_HOST', '127.0.0.1')
    uvicorn.run(app, host=host, port=9000, log_level="error")