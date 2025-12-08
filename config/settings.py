import os
from dotenv import load_dotenv
load_dotenv()
DEBUG_LOGS_ENABLED = os.environ.get('DEBUG_LOGS_ENABLED', 'false').lower() in ('true', '1', 'yes')
TRACE_LOGS_ENABLED = os.environ.get('TRACE_LOGS_ENABLED', 'false').lower() in ('true', '1', 'yes')
AUTO_SAVE_AUTH = os.environ.get('AUTO_SAVE_AUTH', '').lower() in ('1', 'true', 'yes')
AUTH_SAVE_TIMEOUT = int(os.environ.get('AUTH_SAVE_TIMEOUT', '30'))
AUTO_CONFIRM_LOGIN = os.environ.get('AUTO_CONFIRM_LOGIN', 'true').lower() in ('1', 'true', 'yes')
AUTH_PROFILES_DIR = os.path.join(os.path.dirname(__file__), '..', 'auth_profiles')
ACTIVE_AUTH_DIR = os.path.join(AUTH_PROFILES_DIR, 'active')
SAVED_AUTH_DIR = os.path.join(AUTH_PROFILES_DIR, 'saved')
LOG_DIR = os.path.join(os.path.dirname(__file__), '..', 'logs')
APP_LOG_FILE_PATH = os.path.join(LOG_DIR, 'app.log')

def get_environment_variable(key: str, default: str='') -> str:
    return os.environ.get(key, default)

def get_boolean_env(key: str, default: bool=False) -> bool:
    value = os.environ.get(key, '').lower()
    if default:
        return value not in ('false', '0', 'no', 'off')
    else:
        return value in ('true', '1', 'yes', 'on')

def get_int_env(key: str, default: int=0) -> int:
    try:
        return int(os.environ.get(key, str(default)))
    except (ValueError, TypeError):
        return default
NO_PROXY_ENV = os.environ.get('NO_PROXY')
ENABLE_SCRIPT_INJECTION = get_boolean_env('ENABLE_SCRIPT_INJECTION', True)
USERSCRIPT_PATH = get_environment_variable('USERSCRIPT_PATH', 'browser/more_models.js')