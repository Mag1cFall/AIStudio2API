import logging
import logging.handlers
import os
import sys
from typing import Tuple
from config import LOG_DIR, ACTIVE_AUTH_DIR, SAVED_AUTH_DIR, APP_LOG_FILE_PATH
from models import StreamToLogger, WebSocketLogHandler, WebSocketConnectionManager

def setup_server_logging(logger_instance: logging.Logger, log_ws_manager: WebSocketConnectionManager, log_level_name: str='INFO', redirect_print_str: str='false') -> Tuple[object, object]:
    log_level = getattr(logging, log_level_name.upper(), logging.INFO)
    redirect_print = redirect_print_str.lower() in ('true', '1', 'yes')
    os.makedirs(LOG_DIR, exist_ok=True)
    os.makedirs(ACTIVE_AUTH_DIR, exist_ok=True)
    os.makedirs(SAVED_AUTH_DIR, exist_ok=True)
    
    class EmojiFormatter(logging.Formatter):
        EMOJIS = {
            'DEBUG': '🐛',
            'INFO': 'ℹ️ ',
            'WARNING': '⚠️ ',
            'ERROR': '❌',
            'CRITICAL': '🔥'
        }

        def format(self, record):
            emoji = self.EMOJIS.get(record.levelname, '📝')
            record.levelname_emoji = f"{emoji} {record.levelname:<7}"
            return super().format(record)

    log_fmt_str = '%(asctime)s | %(levelname_emoji)s | %(message)s'
    file_log_formatter = EmojiFormatter(log_fmt_str, datefmt='%Y-%m-%d %H:%M:%S')

    if logger_instance.hasHandlers():
        logger_instance.handlers.clear()
    logger_instance.setLevel(log_level)
    logger_instance.propagate = False
    
    if os.path.exists(APP_LOG_FILE_PATH):
        try:
            os.remove(APP_LOG_FILE_PATH)
        except OSError as e:
            print(f"⚠️ (setup) 移除旧日志失败: {e}", file=sys.__stderr__)
            
    file_handler = logging.handlers.RotatingFileHandler(APP_LOG_FILE_PATH, maxBytes=5 * 1024 * 1024, backupCount=5, encoding='utf-8', mode='w')
    file_handler.setFormatter(file_log_formatter)
    logger_instance.addHandler(file_handler)
    
    if log_ws_manager is None:
        print('⚠️ (setup) WebSocket 日志管理器未初始化', file=sys.__stderr__)
    else:
        ws_handler = WebSocketLogHandler(log_ws_manager)
        ws_handler.setLevel(logging.INFO)
        ws_handler.setFormatter(file_log_formatter)
        logger_instance.addHandler(ws_handler)
    
    console_server_log_formatter = EmojiFormatter('%(asctime)s | %(levelname_emoji)s | %(message)s', datefmt='%H:%M:%S')
    console_handler = logging.StreamHandler(sys.stderr)
    console_handler.setFormatter(console_server_log_formatter)
    console_handler.setLevel(log_level)
    logger_instance.addHandler(console_handler)
    
    original_stdout = sys.stdout
    original_stderr = sys.stderr
    
    if redirect_print:
        print('--- 注意：server.py 正在将其 print 输出重定向到日志系统 (文件、WebSocket 和控制台记录器) ---', file=original_stderr)
        stdout_redirect_logger = logging.getLogger('AIStudioProxyServer.stdout')
        stdout_redirect_logger.setLevel(logging.INFO)
        stdout_redirect_logger.propagate = True
        sys.stdout = StreamToLogger(stdout_redirect_logger, logging.INFO)
        stderr_redirect_logger = logging.getLogger('AIStudioProxyServer.stderr')
        stderr_redirect_logger.setLevel(logging.ERROR)
        stderr_redirect_logger.propagate = True
        sys.stderr = StreamToLogger(stderr_redirect_logger, logging.ERROR)
    else:
        print('--- server.py 的 print 输出未被重定向到日志系统 (将使用原始 stdout/stderr) ---', file=original_stderr)
        
    logging.getLogger('uvicorn').setLevel(logging.WARNING)
    logging.getLogger('uvicorn.error').setLevel(logging.INFO)
    logging.getLogger('uvicorn.access').setLevel(logging.WARNING)
    logging.getLogger('websockets').setLevel(logging.WARNING)
    logging.getLogger('playwright').setLevel(logging.WARNING)
    logging.getLogger('asyncio').setLevel(logging.ERROR)
    
    logger_instance.info('🚀 AIStudioProxyServer 日志系统就绪')
    logger_instance.info(f'📝 Level: {logging.getLevelName(log_level)} | Path: {APP_LOG_FILE_PATH}')
    logger_instance.info(f"🖨️ Print Redirect: {('ON' if redirect_print else 'OFF')}")
    
    return (original_stdout, original_stderr)

def restore_original_streams(original_stdout: object, original_stderr: object) -> None:
    sys.stdout = original_stdout
    sys.stderr = original_stderr
    print('已恢复 server.py 的原始 stdout 和 stderr 流。', file=sys.__stderr__)