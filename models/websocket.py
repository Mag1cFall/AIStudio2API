import asyncio
import datetime
import json
import logging
import sys
from typing import Dict

from fastapi import WebSocket, WebSocketDisconnect


class StreamToLogger:

    def __init__(self, target_logger, level=logging.INFO):
        self.target = target_logger
        self.level = level
        self.buffer = ''

    def write(self, text):
        try:
            combined = self.buffer + text
            self.buffer = ''
            for line in combined.splitlines(True):
                if line.endswith(('\n', '\r')):
                    self.target.log(self.level, line.rstrip())
                else:
                    self.buffer += line
        except Exception as err:
            print(f'StreamToLogger 错误: {err}', file=sys.__stderr__)

    def flush(self):
        try:
            if self.buffer:
                self.target.log(self.level, self.buffer.rstrip())
            self.buffer = ''
        except Exception as err:
            print(f'StreamToLogger Flush 错误: {err}', file=sys.__stderr__)

    def isatty(self):
        return False


class WebSocketConnectionManager:

    def __init__(self):
        self.clients: Dict[str, WebSocket] = {}

    async def connect(self, client_id: str, socket: WebSocket):
        await socket.accept()
        self.clients[client_id] = socket
        log = logging.getLogger('AIStudioProxyServer')
        log.info(f'WebSocket 日志客户端已连接: {client_id}')
        try:
            welcome = {
                'type': 'connection_status',
                'status': 'connected',
                'message': '已连接到实时日志流。',
                'timestamp': datetime.datetime.now().isoformat()
            }
            await socket.send_text(json.dumps(welcome))
        except Exception as err:
            log.warning(f'向 WebSocket 客户端 {client_id} 发送欢迎消息失败: {err}')

    def disconnect(self, client_id: str):
        if client_id in self.clients:
            del self.clients[client_id]
            log = logging.getLogger('AIStudioProxyServer')
            log.info(f'WebSocket 日志客户端已断开: {client_id}')

    async def broadcast(self, message: str):
        if not self.clients:
            return
        
        stale = []
        snapshot = list(self.clients.items())
        log = logging.getLogger('AIStudioProxyServer')
        
        for cid, conn in snapshot:
            try:
                await conn.send_text(message)
            except WebSocketDisconnect:
                log.info(f'[WS Broadcast] 客户端 {cid} 在广播期间断开连接。')
                stale.append(cid)
            except RuntimeError as err:
                if 'Connection is closed' in str(err):
                    log.info(f'[WS Broadcast] 客户端 {cid} 的连接已关闭。')
                    stale.append(cid)
                else:
                    log.error(f'广播到 WebSocket {cid} 时发生运行时错误: {err}')
                    stale.append(cid)
            except Exception as err:
                log.error(f'广播到 WebSocket {cid} 时发生未知错误: {err}')
                stale.append(cid)
        
        for cid in stale:
            self.disconnect(cid)


class WebSocketLogHandler(logging.Handler):

    def __init__(self, manager: WebSocketConnectionManager):
        super().__init__()
        self.manager = manager
        self.pending = asyncio.Queue()
        self.worker = None
        self.event_loop = None

    def emit(self, record: logging.LogRecord):
        if not self.manager or not self.manager.clients:
            return
        
        try:
            if not self.event_loop:
                try:
                    self.event_loop = asyncio.get_running_loop()
                except RuntimeError:
                    return

            if self.event_loop.is_closed():
                return

            entry = {
                'time': datetime.datetime.fromtimestamp(record.created).strftime('%H:%M:%S'),
                'level': record.levelname,
                'message': record.getMessage()
            }
            
            self.event_loop.call_soon_threadsafe(self.pending.put_nowait, entry)

            if self.worker is None or self.worker.done():
                self.worker = self.event_loop.create_task(self._dispatch())
                
        except Exception:
            pass

    async def _dispatch(self):
        while True:
            try:
                batch = [await self.pending.get()]
                
                for _ in range(199):
                    try:
                        batch.append(self.pending.get_nowait())
                    except asyncio.QueueEmpty:
                        break
                
                if batch:
                    payload = json.dumps({
                        'type': 'log_batch',
                        'entries': batch
                    })
                    await self.manager.broadcast(payload)

                await asyncio.sleep(0.25)
                
            except Exception as err:
                print(f"WebSocketLogHandler Error: {err}", file=sys.stderr)
                await asyncio.sleep(1)
