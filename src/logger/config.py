from typing import Tuple
import logging
import logging.handlers
import os
import sys

from config import LOG_DIR, ACTIVE_AUTH_DIR, SAVED_AUTH_DIR, APP_LOG_FILE_PATH
from models import StreamToLogger, WebSocketLogHandler, WebSocketConnectionManager


class LogFormatter(logging.Formatter):
    
    LEVEL_ICONS = {
        'DEBUG': '🐛',
        'INFO': 'ℹ️ ',
        'WARNING': '⚠️ ',
        'ERROR': '❌',
        'CRITICAL': '🔥'
    }

    def format(self, record: logging.LogRecord) -> str:
        icon = self.LEVEL_ICONS.get(record.levelname, '📝')
        record.level_display = f"{icon} {record.levelname:<7}"
        return super().format(record)


def _ensure_directories() -> None:
    for directory in [LOG_DIR, ACTIVE_AUTH_DIR, SAVED_AUTH_DIR]:
        os.makedirs(directory, exist_ok=True)


def _clear_old_log() -> None:
    if os.path.exists(APP_LOG_FILE_PATH):
        try:
            os.remove(APP_LOG_FILE_PATH)
        except OSError as err:
            print(f"⚠️ 清除旧日志失败: {err}", file=sys.__stderr__)


def _create_file_handler(fmt: LogFormatter) -> logging.Handler:
    handler = logging.handlers.RotatingFileHandler(
        APP_LOG_FILE_PATH,
        maxBytes=5 * 1024 * 1024,
        backupCount=5,
        encoding='utf-8',
        mode='w'
    )
    handler.setFormatter(fmt)
    return handler


def _create_console_handler(fmt: LogFormatter, level: int) -> logging.Handler:
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(fmt)
    handler.setLevel(level)
    return handler


def _create_websocket_handler(
    ws_manager: WebSocketConnectionManager,
    fmt: LogFormatter
) -> logging.Handler:
    handler = WebSocketLogHandler(ws_manager)
    handler.setLevel(logging.INFO)
    handler.setFormatter(fmt)
    return handler


def _configure_third_party_loggers() -> None:
    silent_loggers = ['uvicorn', 'uvicorn.access', 'websockets', 'playwright']
    for name in silent_loggers:
        logging.getLogger(name).setLevel(logging.WARNING)
    logging.getLogger('uvicorn.error').setLevel(logging.INFO)
    logging.getLogger('asyncio').setLevel(logging.ERROR)


def _redirect_streams(
    target_logger: logging.Logger,
    enable: bool,
    original_stderr: object
) -> None:
    if not enable:
        print('--- 标准输出未重定向 ---', file=original_stderr)
        return
    
    print('--- 标准输出已重定向至日志系统 ---', file=original_stderr)
    
    stdout_logger = logging.getLogger('AIStudioProxyServer.stdout')
    stdout_logger.setLevel(logging.INFO)
    stdout_logger.propagate = True
    sys.stdout = StreamToLogger(stdout_logger, logging.INFO)
    
    stderr_logger = logging.getLogger('AIStudioProxyServer.stderr')
    stderr_logger.setLevel(logging.ERROR)
    stderr_logger.propagate = True
    sys.stderr = StreamToLogger(stderr_logger, logging.ERROR)


def initialize_logging(
    target_logger: logging.Logger,
    ws_manager: WebSocketConnectionManager,
    level_name: str = 'INFO',
    redirect_output: str = 'false'
) -> Tuple[object, object]:
    level = getattr(logging, level_name.upper(), logging.INFO)
    should_redirect = redirect_output.lower() in ('true', '1', 'yes')
    
    _ensure_directories()
    _clear_old_log()
    
    if target_logger.hasHandlers():
        target_logger.handlers.clear()
    target_logger.setLevel(level)
    target_logger.propagate = False
    
    file_fmt = LogFormatter(
        '%(asctime)s | %(level_display)s | %(message)s',
        datefmt='%Y-%m-%d %H:%M:%S'
    )
    console_fmt = LogFormatter(
        '%(asctime)s | %(level_display)s | %(message)s',
        datefmt='%H:%M:%S'
    )
    
    target_logger.addHandler(_create_file_handler(file_fmt))
    target_logger.addHandler(_create_console_handler(console_fmt, level))
    
    if ws_manager:
        target_logger.addHandler(_create_websocket_handler(ws_manager, file_fmt))
    else:
        print('⚠️ WebSocket 管理器未初始化', file=sys.__stderr__)
    
    _configure_third_party_loggers()
    
    saved_stdout = sys.stdout
    saved_stderr = sys.stderr
    
    _redirect_streams(target_logger, should_redirect, saved_stderr)
    
    target_logger.info('🚀 日志系统初始化完成')
    target_logger.info(f'📝 级别: {logging.getLevelName(level)} | 路径: {APP_LOG_FILE_PATH}')
    target_logger.info(f"🖨️ 输出重定向: {'启用' if should_redirect else '禁用'}")
    
    return saved_stdout, saved_stderr


def restore_streams(saved_stdout: object, saved_stderr: object) -> None:
    sys.stdout = saved_stdout
    sys.stderr = saved_stderr
    print('已恢复原始标准输出流。', file=sys.__stderr__)
