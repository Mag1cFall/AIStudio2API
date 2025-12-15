import json
import logging
import re
import zlib
from typing import Dict, List, Tuple, Any, Optional
from urllib.parse import urlparse


def check_generate_endpoint(url: str) -> bool:
    return 'GenerateContent' in url


def extract_proxy_parts(proxy_url: str) -> Tuple:
    if not proxy_url:
        return None, None, None, None, None
    parsed = urlparse(proxy_url)
    return parsed.scheme, parsed.hostname, parsed.port, parsed.username, parsed.password


def create_logger(name: str, log_file: Optional[str] = None, level: int = logging.INFO):
    log = logging.getLogger(name)
    log.setLevel(level)
    fmt = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
    
    console = logging.StreamHandler()
    console.setFormatter(fmt)
    log.addHandler(console)
    
    if log_file:
        file_handler = logging.FileHandler(log_file)
        file_handler.setFormatter(fmt)
        log.addHandler(file_handler)
    
    return log


class ResponseHandler:

    def __init__(self, log_directory: str = 'logs'):
        self.log_directory = log_directory
        self.log = logging.getLogger('response_handler')
        self._configure_logging()

    @staticmethod
    def _configure_logging() -> None:
        logging.basicConfig(
            level=logging.INFO,
            format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
            handlers=[logging.StreamHandler()]
        )

    @staticmethod
    def is_target_path(host: str, path: str) -> bool:
        return 'GenerateContent' in path

    async def handle_request(self, data: bytes, host: str, path: str) -> bytes:
        if not self.is_target_path(host, path):
            return data
        self.log.info(f'Intercepted request to {host}{path}')
        return data

    async def handle_response(
        self,
        data: bytes,
        host: str,
        path: str,
        headers: Dict
    ) -> Dict[str, Any]:
        decoded, completed = self._unchunk(bytes(data))
        decoded = self._inflate(decoded)
        result = self._extract_content(decoded)
        result['done'] = completed
        return result

    def _extract_content(self, raw_data: bytes) -> Dict[str, Any]:
        pattern = rb'\[\[\[null,.*?]],"model"]'
        matches = list(re.finditer(pattern, raw_data))
        
        output = {'reason': '', 'body': '', 'function': []}
        
        for match in matches:
            try:
                parsed = json.loads(match.group(0))
                payload = parsed[0][0]
            except Exception:
                continue
            
            if len(payload) == 2:
                output['body'] += payload[1]
            elif len(payload) == 11 and payload[1] is None and isinstance(payload[10], list):
                tool_data = payload[10]
                fn_name = tool_data[0]
                fn_params = self._parse_tool_args(tool_data[1])
                output['function'].append({'name': fn_name, 'params': fn_params})
            elif len(payload) > 2:
                output['reason'] += payload[1]
        
        return output

    def _parse_tool_args(self, args: List) -> Dict:
        try:
            params = args[0]
            result = {}
            
            extractors = {
                1: lambda v: None,
                2: lambda v: v[1],
                3: lambda v: v[2],
                4: lambda v: v[3] == 1,
                5: lambda v: self._parse_tool_args(v[4]),
            }
            
            for param in params:
                name, value = param[0], param[1]
                if isinstance(value, list):
                    extractor = extractors.get(len(value))
                    if extractor:
                        result[name] = extractor(value)
            
            return result
        except Exception as e:
            raise e

    @staticmethod
    def _inflate(compressed: bytes) -> bytes:
        decompressor = zlib.decompressobj(wbits=zlib.MAX_WBITS | 32)
        return decompressor.decompress(compressed)

    @staticmethod
    def _unchunk(body: bytes) -> Tuple[bytes, bool]:
        mv = memoryview(body)
        result = bytearray()
        offset = 0
        body_len = len(mv)
        
        while offset < body_len:
            crlf_pos = body.find(b'\r\n', offset)
            if crlf_pos == -1:
                break
            
            try:
                chunk_size = int(mv[offset:crlf_pos].tobytes(), 16)
            except ValueError as e:
                logging.error(f'Chunk parse error: {e}')
                break
            
            if chunk_size == 0:
                return bytes(result), True
            
            data_start = crlf_pos + 2
            data_end = data_start + chunk_size
            
            if data_end > body_len:
                break
            
            result.extend(mv[data_start:data_end])
            offset = data_end + 2
        
        return bytes(result), False
