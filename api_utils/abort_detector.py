import logging
import re
from typing import Any, Optional
logger = logging.getLogger(__name__)

class AbortSignalDetector:

    @staticmethod
    def is_abort_error(error: Any) -> bool:
        if not error:
            return False
        try:
            error_message = getattr(error, 'message', str(error))
            if error_message == 'Request was aborted.':
                return True
            abort_patterns = ['signal is aborted without reason', 'AbortError', 'operation was aborted', 'request aborted', 'connection aborted', 'stream aborted', 'cancelled', 'interrupted', 'cherry studio abort', 'electron app closed', 'renderer process terminated', 'main process abort', 'ipc communication failed', 'response paused', 'stream terminated by user', 'client requested abort', 'abort controller signal', 'fetch operation aborted', 'clicked stop button', 'aborted by user', 'stop button clicked', 'user_cancelled', 'streaming_failed', 'task aborted', 'command execution timed out', 'the operation was aborted', 'fetch aborted', 'client closed request', 'client disconnected during', 'http disconnect', 'connection reset by peer', 'broken pipe']
            error_message_lower = error_message.lower()
            for pattern in abort_patterns:
                if pattern.lower() in error_message_lower:
                    return True
            error_name = getattr(error, 'name', '')
            if error_name == 'AbortError':
                return True
            error_class_name = error.__class__.__name__
            if 'abort' in error_class_name.lower():
                return True
            if 'ConnectionError' in error_class_name:
                if any((keyword in error_message_lower for keyword in ['aborted', 'cancelled', 'interrupted', 'closed'])):
                    return True
            if any((pattern in error_message_lower for pattern in ['clicked stop button', 'stop button clicked', 'aborted by user'])):
                return True
            if any((pattern in error_message_lower for pattern in ['task aborted', 'user_cancelled', 'streaming_failed'])):
                return True
            if any((pattern in error_message_lower for pattern in ['fetch aborted', 'xhr aborted', 'request timeout'])):
                return True
            status_code = getattr(error, 'status_code', None) or getattr(error, 'status', None)
            if status_code == 499:
                return True
            if hasattr(error, 'response'):
                response = error.response
                if hasattr(response, 'headers'):
                    user_agent = response.headers.get('user-agent', '').lower()
                    if any((client in user_agent for client in ['sillytavern', 'cherry-studio', 'chatbox', 'kilocode'])):
                        if any((keyword in error_message_lower for keyword in ['abort', 'cancel', 'stop', 'interrupt'])):
                            return True
            return False
        except Exception as e:
            logger.warning(f'检测abort信号时出错: {e}')
            return False

    @staticmethod
    def is_client_disconnect_error(error: Any) -> bool:
        if not error:
            return False
        try:
            error_message = str(error).lower()
            error_class_name = error.__class__.__name__.lower()
            disconnect_patterns = ['client disconnected', 'connection reset', 'broken pipe', 'connection lost', 'peer closed', 'socket closed', 'connection aborted', 'connection closed', 'disconnected', 'network error', 'failed to fetch', 'connection refused', 'timeout', 'connection timeout', 'stream closed', 'sse disconnected', 'websocket closed']
            for pattern in disconnect_patterns:
                if pattern in error_message or pattern in error_class_name:
                    return True
            return False
        except Exception as e:
            logger.warning(f'检测客户端断开信号时出错: {e}')
            return False

    @staticmethod
    def classify_stop_reason(error: Any) -> str:
        if AbortSignalDetector.is_abort_error(error):
            return 'user_abort'
        elif AbortSignalDetector.is_client_disconnect_error(error):
            return 'client_disconnect'
        else:
            return 'other'

    @staticmethod
    def should_treat_as_success(error: Any) -> bool:
        stop_reason = AbortSignalDetector.classify_stop_reason(error)
        return stop_reason in ['user_abort', 'client_disconnect']

class AbortSignalHandler:

    def __init__(self):
        self.detector = AbortSignalDetector()

    def handle_error(self, error: Any, request_id: Optional[str]=None) -> dict:
        stop_reason = self.detector.classify_stop_reason(error)
        is_success = self.detector.should_treat_as_success(error)
        response = {'stop_reason': stop_reason, 'is_success': is_success, 'error_message': str(error)}
        if request_id:
            response['request_id'] = request_id
        if stop_reason == 'user_abort':
            logger.info(f'[{request_id}] 检测到用户主动停止请求')
            response['message'] = 'Request stopped by user'
            response['status'] = 'paused'
        elif stop_reason == 'client_disconnect':
            logger.info(f'[{request_id}] 检测到客户端断开连接')
            response['message'] = 'Client disconnected'
            response['status'] = 'disconnected'
        else:
            logger.error(f'[{request_id}] 其他类型错误: {error}')
            response['message'] = 'Internal error'
            response['status'] = 'error'
        return response