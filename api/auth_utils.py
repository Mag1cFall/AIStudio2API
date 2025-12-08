import os
from typing import Set

_keys: Set[str] = set()
_key_file = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'key.txt')


def load_keys():
    global _keys
    _keys.clear()
    if os.path.exists(_key_file):
        with open(_key_file, 'r') as f:
            for line in f:
                stripped = line.strip()
                if stripped:
                    _keys.add(stripped)


def init_keys():
    if not os.path.exists(_key_file):
        with open(_key_file, 'w') as f:
            pass
    load_keys()


def check_key(header_key: str) -> bool:
    if not _keys:
        return True
    return header_key in _keys


API_KEYS = _keys
KEY_FILE_PATH = _key_file
load_api_keys = load_keys
initialize_keys = init_keys
verify_api_key = check_key