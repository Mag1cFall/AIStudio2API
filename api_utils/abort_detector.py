"""
基于主流聊天客户端源码分析的通用停止信号检测器

根据对Cherry Studio、Chatbox等客户端的源码分析，
实现了多种停止信号检测方式以提供最佳的兼容性。
"""

import logging
import re
from typing import Any, Optional

logger = logging.getLogger(__name__)

class AbortSignalDetector:
    """
    通用的停止信号检测器
    
    基于对主流聊天客户端的源码分析：
    - Cherry Studio: 检测特定错误类型和消息
    - Chatbox: 使用AbortController和signal检查
    - 通用模式: HTTP连接断开检测
    """
    
    @staticmethod
    def is_abort_error(error: Any) -> bool:
        """
        检测是否为客户端主动停止请求产生的错误
        
        基于Cherry Studio的isAbortError函数实现：
        - 检测DOMException AbortError
        - 检测特定的错误消息
        - 检测OpenAI相关的abort信号
        
        Args:
            error: 异常对象或错误信息
            
        Returns:
            bool: 如果是停止信号则返回True
        """
        if not error:
            return False
            
        try:
            # 1. 检查错误消息 (Cherry Studio模式)
            error_message = getattr(error, 'message', str(error))
            
            # 标准的abort消息
            if error_message == 'Request was aborted.':
                return True
                
            # OpenAI/Anthropic等常见的abort信号
            abort_patterns = [
                'signal is aborted without reason',
                'AbortError',
                'operation was aborted',
                'request aborted',
                'connection aborted',
                'stream aborted',
                'cancelled',
                'interrupted',
                # Cherry Studio特定模式 - 根据实际使用反馈增强
                'cherry studio abort',
                'electron app closed',
                'renderer process terminated',
                'main process abort',
                'ipc communication failed',
                'response paused',  # Cherry Studio显示"回應已暫停"时的内部状态
                'stream terminated by user',
                'client requested abort',
                'abort controller signal',
                'fetch operation aborted',
                # SillyTavern特定模式
                'clicked stop button',
                'aborted by user',
                'stop button clicked',
                # Kilocode特定模式
                'user_cancelled',
                'streaming_failed',
                'task aborted',
                'command execution timed out',
                # 通用Web API模式
                'the operation was aborted',
                'fetch aborted',
                # HTTP客户端断开相关
                'client closed request',
                'client disconnected during',
                'http disconnect',
                'connection reset by peer',
                'broken pipe'
            ]
            
            error_message_lower = error_message.lower()
            for pattern in abort_patterns:
                if pattern.lower() in error_message_lower:
                    return True
            
            # 2. 检查异常类型 (标准Web API)
            error_name = getattr(error, 'name', '')
            if error_name == 'AbortError':
                return True
                
            # 3. 检查异常类的名称
            error_class_name = error.__class__.__name__
            if 'abort' in error_class_name.lower():
                return True
                
            # 4. 检查是否为ConnectionError相关
            if 'ConnectionError' in error_class_name:
                # 进一步检查是否为主动断开
                if any(keyword in error_message_lower for keyword in
                       ['aborted', 'cancelled', 'interrupted', 'closed']):
                    return True
            
            # 5. 检查特定的客户端停止模式
            # SillyTavern模式: 按钮触发的停止
            if any(pattern in error_message_lower for pattern in [
                'clicked stop button', 'stop button clicked', 'aborted by user']):
                return True
                
            # Kilocode模式: 任务级别停止
            if any(pattern in error_message_lower for pattern in [
                'task aborted', 'user_cancelled', 'streaming_failed']):
                return True
                
            # 通用fetch/XMLHttpRequest abort模式
            if any(pattern in error_message_lower for pattern in [
                'fetch aborted', 'xhr aborted', 'request timeout']):
                return True
            
            # 6. 检查HTTP状态码相关
            status_code = getattr(error, 'status_code', None) or getattr(error, 'status', None)
            if status_code == 499:  # Client Closed Request
                return True
            
            # 7. 检查是否包含特定的客户端标识
            # 某些客户端会在User-Agent或其他header中包含特定信息
            if hasattr(error, 'response'):
                response = error.response
                if hasattr(response, 'headers'):
                    user_agent = response.headers.get('user-agent', '').lower()
                    if any(client in user_agent for client in [
                        'sillytavern', 'cherry-studio', 'chatbox', 'kilocode']):
                        # 这些客户端的错误更可能是用户停止操作
                        if any(keyword in error_message_lower for keyword in [
                            'abort', 'cancel', 'stop', 'interrupt']):
                            return True
                
            return False
            
        except Exception as e:
            logger.warning(f"检测abort信号时出错: {e}")
            return False
    
    @staticmethod
    def is_client_disconnect_error(error: Any) -> bool:
        """
        检测是否为客户端断开连接的错误
        
        Args:
            error: 异常对象或错误信息
            
        Returns:
            bool: 如果是客户端断开则返回True
        """
        if not error:
            return False
            
        try:
            error_message = str(error).lower()
            error_class_name = error.__class__.__name__.lower()
            
            # 客户端断开连接的常见模式
            disconnect_patterns = [
                'client disconnected',
                'connection reset',
                'broken pipe',
                'connection lost',
                'peer closed',
                'socket closed',
                'connection aborted',
                'connection closed',
                'disconnected',
                # Web客户端特定模式
                'network error',
                'failed to fetch',
                'connection refused',
                'timeout',
                'connection timeout',
                # 流式连接断开
                'stream closed',
                'sse disconnected',
                'websocket closed'
            ]
            
            for pattern in disconnect_patterns:
                if pattern in error_message or pattern in error_class_name:
                    return True
                    
            return False
            
        except Exception as e:
            logger.warning(f"检测客户端断开信号时出错: {e}")
            return False
    
    @staticmethod
    def classify_stop_reason(error: Any) -> str:
        """
        分类停止原因
        
        Args:
            error: 异常对象或错误信息
            
        Returns:
            str: 停止原因类型 ('user_abort', 'client_disconnect', 'other')
        """
        if AbortSignalDetector.is_abort_error(error):
            return 'user_abort'
        elif AbortSignalDetector.is_client_disconnect_error(error):
            return 'client_disconnect'
        else:
            return 'other'
    
    @staticmethod
    def should_treat_as_success(error: Any) -> bool:
        """
        判断是否应该将错误视为成功（暂停）
        
        基于Cherry Studio的处理逻辑：
        abort错误应该被视为暂停而非失败
        
        Args:
            error: 异常对象或错误信息
            
        Returns:
            bool: 如果应该视为成功则返回True
        """
        stop_reason = AbortSignalDetector.classify_stop_reason(error)
        return stop_reason in ['user_abort', 'client_disconnect']


class AbortSignalHandler:
    """停止信号处理器"""
    
    def __init__(self):
        self.detector = AbortSignalDetector()
    
    def handle_error(self, error: Any, request_id: Optional[str] = None) -> dict:
        """
        处理错误并返回适当的响应
        
        Args:
            error: 异常对象
            request_id: 请求ID
            
        Returns:
            dict: 包含状态和消息的响应
        """
        stop_reason = self.detector.classify_stop_reason(error)
        is_success = self.detector.should_treat_as_success(error)
        
        response = {
            'stop_reason': stop_reason,
            'is_success': is_success,
            'error_message': str(error)
        }
        
        if request_id:
            response['request_id'] = request_id
        
        if stop_reason == 'user_abort':
            logger.info(f"[{request_id}] 检测到用户主动停止请求")
            response['message'] = 'Request stopped by user'
            response['status'] = 'paused'
        elif stop_reason == 'client_disconnect':
            logger.info(f"[{request_id}] 检测到客户端断开连接")
            response['message'] = 'Client disconnected'
            response['status'] = 'disconnected'
        else:
            logger.error(f"[{request_id}] 其他类型错误: {error}")
            response['message'] = 'Internal error'
            response['status'] = 'error'
        
        return response