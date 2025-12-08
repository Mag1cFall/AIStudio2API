import logging
from typing import Any, Optional

log = logging.getLogger(__name__)

ABORT_PATTERNS = [
    'signal is aborted without reason', 'aborterror', 'operation was aborted',
    'request aborted', 'connection aborted', 'stream aborted', 'cancelled',
    'interrupted', 'cherry studio abort', 'electron app closed',
    'renderer process terminated', 'main process abort', 'ipc communication failed',
    'response paused', 'stream terminated by user', 'client requested abort',
    'abort controller signal', 'fetch operation aborted', 'clicked stop button',
    'aborted by user', 'stop button clicked', 'user_cancelled', 'streaming_failed',
    'task aborted', 'command execution timed out', 'the operation was aborted',
    'fetch aborted', 'client closed request', 'client disconnected during',
    'http disconnect', 'connection reset by peer', 'broken pipe'
]

DISCONNECT_PATTERNS = [
    'client disconnected', 'connection reset', 'broken pipe', 'connection lost',
    'peer closed', 'socket closed', 'connection aborted', 'connection closed',
    'disconnected', 'network error', 'failed to fetch', 'connection refused',
    'timeout', 'connection timeout', 'stream closed', 'sse disconnected',
    'websocket closed'
]


class AbortSignalDetector:

    @staticmethod
    def is_abort_error(err: Any) -> bool:
        if not err:
            return False
        try:
            msg = getattr(err, 'message', str(err))
            if msg == 'Request was aborted.':
                return True
            
            msg_lower = msg.lower()
            for pattern in ABORT_PATTERNS:
                if pattern in msg_lower:
                    return True
            
            name = getattr(err, 'name', '')
            if name == 'AbortError':
                return True
            
            cls = err.__class__.__name__
            if 'abort' in cls.lower():
                return True
            
            if 'ConnectionError' in cls:
                if any(kw in msg_lower for kw in ['aborted', 'cancelled', 'interrupted', 'closed']):
                    return True
            
            code = getattr(err, 'status_code', None) or getattr(err, 'status', None)
            if code == 499:
                return True
            
            if hasattr(err, 'response'):
                resp = err.response
                if hasattr(resp, 'headers'):
                    ua = resp.headers.get('user-agent', '').lower()
                    clients = ['sillytavern', 'cherry-studio', 'chatbox', 'kilocode']
                    if any(c in ua for c in clients):
                        if any(kw in msg_lower for kw in ['abort', 'cancel', 'stop', 'interrupt']):
                            return True
            
            return False
        except Exception as e:
            log.warning(f'检测abort信号时出错: {e}')
            return False

    @staticmethod
    def is_client_disconnect_error(err: Any) -> bool:
        if not err:
            return False
        try:
            msg = str(err).lower()
            cls = err.__class__.__name__.lower()
            
            for pattern in DISCONNECT_PATTERNS:
                if pattern in msg or pattern in cls:
                    return True
            return False
        except Exception as e:
            log.warning(f'检测客户端断开信号时出错: {e}')
            return False

    @staticmethod
    def classify_stop_reason(err: Any) -> str:
        if AbortSignalDetector.is_abort_error(err):
            return 'user_abort'
        elif AbortSignalDetector.is_client_disconnect_error(err):
            return 'client_disconnect'
        return 'other'

    @staticmethod
    def should_treat_as_success(err: Any) -> bool:
        reason = AbortSignalDetector.classify_stop_reason(err)
        return reason in ['user_abort', 'client_disconnect']


class AbortSignalHandler:

    def __init__(self):
        self.detector = AbortSignalDetector()

    def handle_error(self, err: Any, req_id: Optional[str] = None) -> dict:
        reason = self.detector.classify_stop_reason(err)
        success = self.detector.should_treat_as_success(err)
        
        result = {
            'stop_reason': reason,
            'is_success': success,
            'error_message': str(err)
        }
        
        if req_id:
            result['request_id'] = req_id
        
        if reason == 'user_abort':
            log.info(f'[{req_id}] 检测到用户主动停止请求')
            result['message'] = 'Request stopped by user'
            result['status'] = 'paused'
        elif reason == 'client_disconnect':
            log.info(f'[{req_id}] 检测到客户端断开连接')
            result['message'] = 'Client disconnected'
            result['status'] = 'disconnected'
        else:
            log.error(f'[{req_id}] 其他类型错误: {err}')
            result['message'] = 'Internal error'
            result['status'] = 'error'
        
        return result