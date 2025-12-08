import asyncio
import json
import time
import datetime
from typing import Any, Dict, List, Optional, AsyncGenerator
from asyncio import Queue
from models import Message
import re
import base64
import requests
import os
import hashlib
import threading

class RequestCancellationManager:

    def __init__(self):
        self._active_requests: Dict[str, Dict[str, Any]] = {}
        self._lock = threading.Lock()

    def register_request(self, req_id: str, request_info: Dict[str, Any]=None):
        with self._lock:
            self._active_requests[req_id] = {'cancelled': False, 'start_time': time.time(), 'info': request_info or {}}

    def cancel_request(self, req_id: str):
        with self._lock:
            if req_id in self._active_requests:
                self._active_requests[req_id]['cancelled'] = True
                self._active_requests[req_id]['cancel_time'] = time.time()
                return True
            return False

    def is_cancelled(self, req_id: str) -> bool:
        with self._lock:
            if req_id in self._active_requests:
                return self._active_requests[req_id]['cancelled']
            return False

    def unregister_request(self, req_id: str):
        with self._lock:
            self._active_requests.pop(req_id, None)

    def get_active_requests(self) -> List[Dict[str, Any]]:
        with self._lock:
            result = []
            for req_id, info in self._active_requests.items():
                result.append({'req_id': req_id, 'cancelled': info['cancelled'], 'duration': time.time() - info['start_time'], **info.get('info', {})})
            return result
request_manager = RequestCancellationManager()

def calculate_stream_max_retries(messages: List[Message]) -> int:
    base_retries = 300
    total_token_estimate = 0
    image_count = 0

    for msg in messages:
        content = msg.content
        if not content:
            continue
        
        if isinstance(content, str):
            total_token_estimate += len(content) / 3
        elif isinstance(content, list):
            for item in content:
                if hasattr(item, 'type') and item.type == 'text':
                    text = item.text or ''
                    total_token_estimate += len(text) / 3
                elif isinstance(item, dict) and item.get('type') == 'text':
                    text = item.get('text', '')
                    total_token_estimate += len(text) / 3
                
                if hasattr(item, 'type') and item.type == 'image_url':
                    image_count += 1
                elif isinstance(item, dict) and item.get('type') == 'image_url':
                    image_count += 1

    additional_retries = (image_count * 50) + int((total_token_estimate / 10000) * 20)
    total_retries = base_retries + additional_retries
    
    return min(1200, total_retries)

def generate_sse_chunk(delta: str, req_id: str, model: str) -> str:
    chunk_data = {'id': f'chatcmpl-{req_id}', 'object': 'chat.completion.chunk', 'created': int(time.time()), 'model': model, 'choices': [{'index': 0, 'delta': {'content': delta}, 'finish_reason': None}]}
    return f'data: {json.dumps(chunk_data)}\n\n'

def generate_sse_stop_chunk(req_id: str, model: str, reason: str='stop', usage: dict=None) -> str:
    stop_chunk_data = {'id': f'chatcmpl-{req_id}', 'object': 'chat.completion.chunk', 'created': int(time.time()), 'model': model, 'choices': [{'index': 0, 'delta': {}, 'finish_reason': reason}]}
    if usage:
        stop_chunk_data['usage'] = usage
    return f'data: {json.dumps(stop_chunk_data)}\n\ndata: [DONE]\n\n'

def generate_sse_error_chunk(message: str, req_id: str, error_type: str='server_error') -> str:
    error_chunk = {'error': {'message': message, 'type': error_type, 'param': None, 'code': req_id}}
    return f'data: {json.dumps(error_chunk)}\n\n'

async def use_stream_response(req_id: str, max_empty_retries: int = 300) -> AsyncGenerator[Any, None]:
    from server import STREAM_QUEUE, logger
    import queue
    if STREAM_QUEUE is None:
        logger.warning(f'[{req_id}] ⚠️ STREAM_QUEUE is None, 无法使用流响应')
        return
    logger.info(f'[{req_id}] 🌊 开始使用流响应 (Max Retries: {max_empty_retries})')
    empty_count = 0
    data_received = False
    try:
        while True:
            try:
                data = STREAM_QUEUE.get_nowait()
                if data is None:
                    logger.info(f'[{req_id}] 🛑 接收到流结束标志')
                    break
                empty_count = 0
                data_received = True
                if isinstance(data, str):
                    try:
                        parsed_data = json.loads(data)
                        if parsed_data.get('done') is True:
                            logger.info(f'[{req_id}] ✅ 接收到JSON格式的完成标志')
                            yield parsed_data
                            break
                        else:
                            yield parsed_data
                    except json.JSONDecodeError:
                        logger.debug(f'[{req_id}] 返回非JSON字符串数据')
                        yield data
                else:
                    yield data
                    if isinstance(data, dict) and data.get('done') is True:
                        logger.info(f'[{req_id}] 接收到字典格式的完成标志')
                        break
            except (queue.Empty, asyncio.QueueEmpty):
                empty_count += 1
                if empty_count % 50 == 0:
                    logger.info(f'[{req_id}] 等待流数据... ({empty_count}/{max_empty_retries})')
                if empty_count >= max_empty_retries:
                    if not data_received:
                        logger.error(f'[{req_id}] 流响应队列空读取次数达到上限且未收到任何数据，可能是辅助流未启动或出错')
                    else:
                        logger.warning(f'[{req_id}] 流响应队列空读取次数达到上限 ({max_empty_retries})，结束读取')
                    yield {'done': True, 'reason': 'internal_timeout', 'body': '', 'function': []}
                    return
                await asyncio.sleep(0.1)
                continue
    except Exception as e:
        logger.error(f'[{req_id}] 使用流响应时出错: {e}')
        raise
    finally:
        logger.info(f'[{req_id}] 流响应使用完成，数据接收状态: {data_received}')

async def clear_stream_queue():
    from server import STREAM_QUEUE, logger
    import queue
    if STREAM_QUEUE is None:
        return
    while True:
        try:
            data_chunk = await asyncio.to_thread(STREAM_QUEUE.get_nowait)
        except queue.Empty:
            break
        except Exception as e:
            logger.error(f'❌ 清空流式队列时发生意外错误: {e}', exc_info=True)
            break
    logger.info('🧹 流式队列缓存清空完毕。')

async def use_helper_get_response(helper_endpoint: str, helper_sapisid: str) -> AsyncGenerator[str, None]:
    from server import logger
    import aiohttp
    logger.info(f'正在尝试使用Helper端点: {helper_endpoint}')
    try:
        async with aiohttp.ClientSession() as session:
            headers = {'Content-Type': 'application/json', 'Cookie': f'SAPISID={helper_sapisid}' if helper_sapisid else ''}
            async with session.get(helper_endpoint, headers=headers) as response:
                if response.status == 200:
                    async for chunk in response.content.iter_chunked(1024):
                        if chunk:
                            yield chunk.decode('utf-8', errors='ignore')
                else:
                    logger.error(f'Helper端点返回错误状态: {response.status}')
    except Exception as e:
        logger.error(f'使用Helper端点时出错: {e}')

def validate_chat_request(messages: List[Message], req_id: str) -> Dict[str, Optional[str]]:
    from server import logger
    if not messages:
        raise ValueError(f"[{req_id}] 无效请求: 'messages' 数组缺失或为空。")
    if not any((msg.role != 'system' for msg in messages)):
        raise ValueError(f'[{req_id}] 无效请求: 所有消息都是系统消息。至少需要一条用户或助手消息。')
    return {'error': None, 'warning': None}

def extract_base64_to_local(base64_data: str) -> str:
    output_dir = os.path.join(os.path.dirname(__file__), '..', 'upload_images')
    match = re.match('data:image/(\\w+);base64,(.*)', base64_data)
    if not match:
        print('错误: Base64 数据格式不正确。')
        return None
    image_type = match.group(1)
    encoded_image_data = match.group(2)
    try:
        decoded_image_data = base64.b64decode(encoded_image_data)
    except base64.binascii.Error as e:
        print(f'错误: Base64 解码失败 - {e}')
        return None
    md5_hash = hashlib.md5(decoded_image_data).hexdigest()
    file_extension = f'.{image_type}'
    output_filepath = os.path.join(output_dir, f'{md5_hash}{file_extension}')
    os.makedirs(output_dir, exist_ok=True)
    if os.path.exists(output_filepath):
        print(f'文件已存在，跳过保存: {output_filepath}')
        return output_filepath
    try:
        with open(output_filepath, 'wb') as f:
            f.write(decoded_image_data)
        print(f'图片已成功保存到: {output_filepath}')
        return output_filepath
    except IOError as e:
        print(f'错误: 保存文件失败 - {e}')
        return None

def prepare_combined_prompt(messages: List[Message], req_id: str) -> tuple[str, str, list]:
    from server import logger
    logger.info(f'[{req_id}] 🧩 正在从 {len(messages)} 条消息准备组合提示...')
    system_prompts = []
    combined_parts = []
    images_list = []
    image_counter = 1
    role_map = {'user': '用户', 'assistant': '助手'}
    for i, msg in enumerate(messages):
        role = msg.role or 'unknown'
        if role == 'system':
            if isinstance(msg.content, str):
                system_prompts.append(msg.content.strip())
            continue
        role_prefix = f'{role_map.get(role, role.capitalize())}: '
        content = msg.content or ''
        content_str = ''
        message_images = []
        if isinstance(content, str):
            content_str = content.strip()
        elif isinstance(content, list):
            text_parts = []
            for item in content:
                if hasattr(item, 'type') and item.type == 'text':
                    text_parts.append(item.text or '')
                elif isinstance(item, dict) and item.get('type') == 'text':
                    text_parts.append(item.get('text', ''))
                elif hasattr(item, 'type') and item.type == 'image_url':
                    image_url_value = item.image_url.url
                    if image_url_value.startswith('data:image/'):
                        match = re.match('data:image/(\\w+);base64,', image_url_value)
                        if match:
                            img_type = match.group(1)
                            import random
                            import string
                            rand_suffix = ''.join(random.choices(string.ascii_lowercase + string.digits, k=8))
                            filename = f'tmp{rand_suffix}.{img_type}'
                        else:
                            filename = f'image_{image_counter}.png'
                        images_list.append(image_url_value)
                        image_tag = f'[{filename}]'
                        message_images.append(image_tag)
                        image_counter += 1
                        logger.info(f'[{req_id}] 🖼️ 为图片分配标识符: {image_tag}')
                    else:
                        images_list.append(image_url_value)
                        try:
                            filename = os.path.basename(image_url_value.split('?')[0])
                            if not filename or '.' not in filename:
                                filename = f'image_{image_counter}.png'
                        except:
                            filename = f'image_{image_counter}.png'
                        image_tag = f'[{filename}]'
                        message_images.append(image_tag)
                        image_counter += 1
                        logger.info(f'[{req_id}] 🖼️ 为图片URL分配标识符: {image_tag}')
                else:
                    logger.warning(f'[{req_id}] ⚠️ 忽略未知类型的 content item')
            content_str = '\n'.join(text_parts).strip()
        else:
            logger.warning(f'[{req_id}] ⚠️ 角色 {role} 内容类型意外: {type(content)}')
            content_str = str(content or '').strip()
        if content_str or message_images:
            message_content = content_str
            if message_images:
                image_tags_str = ' ' + ' '.join(message_images)
                if message_content:
                    message_content += image_tags_str
                else:
                    message_content = image_tags_str.strip()
            combined_parts.append(f'{role_prefix}{message_content}')
    final_prompt = '\n\n'.join(combined_parts)
    system_prompt = '\n\n'.join(system_prompts)
    if system_prompt:
        logger.info(f"[{req_id}] 📝 系统提示: '{system_prompt[:50]}...'")
    # preview_text = final_prompt[:200].replace('\n', '\\n')
    logger.info(f"[{req_id}] 🧩 组合提示 ({len(final_prompt)} chars, {len(images_list)} imgs)")
    return (system_prompt, final_prompt, images_list)

def _get_image_message_index(messages: List[Message], image_num: int) -> int:
    image_counter = 1
    for i, msg in enumerate(messages):
        content = msg.content or ''
        if isinstance(content, list):
            for item in content:
                if hasattr(item, 'type') and item.type == 'image_url':
                    if image_counter == image_num:
                        user_message_count = sum((1 for m in messages[:i + 1] if m.role == 'user'))
                        return user_message_count
                    image_counter += 1
    return 1

def estimate_tokens(text: str) -> int:
    if not text:
        return 0
    chinese_chars = sum((1 for char in text if '一' <= char <= '鿿' or '\u3000' <= char <= '〿' or '\uff00' <= char <= '\uffef'))
    non_chinese_chars = len(text) - chinese_chars
    chinese_tokens = chinese_chars / 1.5
    english_tokens = non_chinese_chars / 4.0
    return max(1, int(chinese_tokens + english_tokens))

def calculate_usage_stats(messages: List[dict], response_content: str, reasoning_content: str=None) -> dict:
    prompt_text = ''
    for message in messages:
        role = message.get('role', '')
        content = message.get('content', '')
        prompt_text += f'{role}: {content}\n'
    prompt_tokens = estimate_tokens(prompt_text)
    completion_text = response_content or ''
    if reasoning_content:
        completion_text += reasoning_content
    completion_tokens = estimate_tokens(completion_text)
    total_tokens = prompt_tokens + completion_tokens
    return {'prompt_tokens': prompt_tokens, 'completion_tokens': completion_tokens, 'total_tokens': total_tokens}

def generate_sse_stop_chunk_with_usage(req_id: str, model: str, usage_stats: dict, reason: str='stop') -> str:
    return generate_sse_stop_chunk(req_id, model, reason, usage_stats)