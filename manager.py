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
CONFIG_FILE_PATH = os.path.join(SCRIPT_DIR, 'gui_config.json')
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
            'script_injection_enabled': False
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

    def _monitor_output(self, process):
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
                        
                    asyncio.run_coroutine_threadsafe(
                        self.broadcast_log(decoded_line, level), loop
                    )
        except Exception as e:
            logger.error(f"Output monitor error: {e}")
        finally:
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

        cmd = [
            PYTHON_EXECUTABLE,
            LAUNCH_CAMOUFOX_PY,
            mode_flag,
            '--server-port', str(config.get('fastapi_port', 2048)),
            '--camoufox-debug-port', str(config.get('camoufox_debug_port', 9222))
        ]
        
        env = os.environ.copy()
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

        active_dir = os.path.join(SCRIPT_DIR, 'auth_profiles', 'active')
        if os.path.exists(active_dir):
            files = [f for f in os.listdir(active_dir) if f.endswith('.json')]
            if files:
                cmd.extend(['--active-auth-json', os.path.join(active_dir, files[0])])

        try:
            env['PYTHONUNBUFFERED'] = '1'
            env['PYTHONIOENCODING'] = 'utf-8'
            env['ENABLE_SCRIPT_INJECTION'] = 'true' if config.get('script_injection_enabled', False) else 'false'
            
            if platform.system() == 'Windows':
                creationflags = subprocess.CREATE_NEW_PROCESS_GROUP
            else:
                creationflags = 0

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
                "port": config.get('fastapi_port', 2048)
            }
            
            self.output_thread = threading.Thread(
                target=self._monitor_output, 
                args=(self.process,), 
                daemon=True
            )
            self.output_thread.start()
            
            self.service_status = "running"
            return True, "服务启动成功"
        except Exception as e:
            self.service_status = "stopped"
            logger.error(f"启动失败: {e}")
            return False, str(e)

    def stop_service(self):
        if not self.process:
            return True, "服务未运行"
        
        self.service_status = "stopping"
        self.stop_event.set()
        
        try:
            if platform.system() == 'Windows':
                subprocess.run(['taskkill', '/PID', str(self.process.pid), '/T', '/F'], capture_output=True)
            else:
                self.process.terminate()
                try:
                    self.process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    self.process.kill()
            
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
    profiles_dir = os.path.join(SCRIPT_DIR, 'auth_profiles')
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
    profiles_dir = os.path.join(SCRIPT_DIR, 'auth_profiles')
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
    profiles_dir = os.path.join(SCRIPT_DIR, 'auth_profiles')
    active_dir = os.path.join(profiles_dir, 'active')
    
    if os.path.exists(active_dir):
        for f in os.listdir(active_dir):
            if f.endswith('.json'):
                try:
                    os.remove(os.path.join(active_dir, f))
                except Exception as e:
                    raise HTTPException(status_code=500, detail=f"删除文件失败: {e}")
    return {"success": True}

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