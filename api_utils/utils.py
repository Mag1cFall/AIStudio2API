"""
API工具函数模块
包含SSE生成、流处理、token统计和请求验证等工具函数
"""

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


# 全局活动请求状态管理
class RequestCancellationManager:
    """请求取消管理器，用于跟踪和取消活动请求"""
    
    def __init__(self):
        self._active_requests: Dict[str, Dict[str, Any]] = {}
        self._lock = threading.Lock()
    
    def register_request(self, req_id: str, request_info: Dict[str, Any] = None):
        """注册一个活动请求"""
        with self._lock:
            self._active_requests[req_id] = {
                'cancelled': False,
                'start_time': time.time(),
                'info': request_info or {}
            }
    
    def cancel_request(self, req_id: str):
        """取消一个请求"""
        with self._lock:
            if req_id in self._active_requests:
                self._active_requests[req_id]['cancelled'] = True
                self._active_requests[req_id]['cancel_time'] = time.time()
                return True
            return False
    
    def is_cancelled(self, req_id: str) -> bool:
        """检查请求是否被取消"""
        with self._lock:
            if req_id in self._active_requests:
                return self._active_requests[req_id]['cancelled']
            return False
    
    def unregister_request(self, req_id: str):
        """注销一个请求"""
        with self._lock:
            self._active_requests.pop(req_id, None)
    
    def get_active_requests(self) -> List[Dict[str, Any]]:
        """获取所有活动请求列表"""
        with self._lock:
            result = []
            for req_id, info in self._active_requests.items():
                result.append({
                    'req_id': req_id,
                    'cancelled': info['cancelled'],
                    'duration': time.time() - info['start_time'],
                    **info.get('info', {})
                })
            return result


# 全局请求取消管理器实例
request_manager = RequestCancellationManager()


# --- SSE生成函数 ---
def generate_sse_chunk(delta: str, req_id: str, model: str) -> str:
    """生成SSE数据块"""
    chunk_data = {
        "id": f"chatcmpl-{req_id}",
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "delta": {"content": delta}, "finish_reason": None}]
    }
    return f"data: {json.dumps(chunk_data)}\n\n"


def generate_sse_stop_chunk(req_id: str, model: str, reason: str = "stop", usage: dict = None) -> str:
    """生成SSE停止块"""
    stop_chunk_data = {
        "id": f"chatcmpl-{req_id}",
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "delta": {}, "finish_reason": reason}]
    }
    
    # 添加usage信息（如果提供）
    if usage:
        stop_chunk_data["usage"] = usage
    
    return f"data: {json.dumps(stop_chunk_data)}\n\ndata: [DONE]\n\n"


def generate_sse_error_chunk(message: str, req_id: str, error_type: str = "server_error") -> str:
    """生成SSE错误块"""
    error_chunk = {"error": {"message": message, "type": error_type, "param": None, "code": req_id}}
    return f"data: {json.dumps(error_chunk)}\n\n"


# --- 流处理工具函数 ---
async def use_stream_response(req_id: str) -> AsyncGenerator[Any, None]:
    """使用流响应（从服务器的全局队列获取数据）"""
    from server import STREAM_QUEUE, logger
    import queue
    
    if STREAM_QUEUE is None:
        logger.warning(f"[{req_id}] STREAM_QUEUE is None, 无法使用流响应")
        return
    
    logger.info(f"[{req_id}] 开始使用流响应")
    
    empty_count = 0
    max_empty_retries = 300  # 30秒超时
    data_received = False
    
    try:
        while True:
            try:
                # 从队列中获取数据
                data = STREAM_QUEUE.get_nowait()
                if data is None:  # 结束标志
                    logger.info(f"[{req_id}] 接收到流结束标志")
                    break
                
                # 重置空计数器
                empty_count = 0
                data_received = True
                logger.debug(f"[{req_id}] 接收到流数据: {type(data)} - {str(data)[:200]}...")
                
                # 检查是否是JSON字符串形式的结束标志
                if isinstance(data, str):
                    try:
                        parsed_data = json.loads(data)
                        if parsed_data.get("done") is True:
                            logger.info(f"[{req_id}] 接收到JSON格式的完成标志")
                            yield parsed_data
                            break
                        else:
                            yield parsed_data
                    except json.JSONDecodeError:
                        # 如果不是JSON，直接返回字符串
                        logger.debug(f"[{req_id}] 返回非JSON字符串数据")
                        yield data
                else:
                    # 直接返回数据
                    yield data
                    
                    # 检查字典类型的结束标志
                    if isinstance(data, dict) and data.get("done") is True:
                        logger.info(f"[{req_id}] 接收到字典格式的完成标志")
                        break
                
            except (queue.Empty, asyncio.QueueEmpty):
                empty_count += 1
                if empty_count % 50 == 0:  # 每5秒记录一次等待状态
                    logger.info(f"[{req_id}] 等待流数据... ({empty_count}/{max_empty_retries})")
                
                if empty_count >= max_empty_retries:
                    if not data_received:
                        logger.error(f"[{req_id}] 流响应队列空读取次数达到上限且未收到任何数据，可能是辅助流未启动或出错")
                    else:
                        logger.warning(f"[{req_id}] 流响应队列空读取次数达到上限 ({max_empty_retries})，结束读取")
                    
                    # 返回超时完成信号，而不是简单退出
                    yield {"done": True, "reason": "internal_timeout", "body": "", "function": []}
                    return
                    
                await asyncio.sleep(0.1)  # 100ms等待
                continue
                
    except Exception as e:
        logger.error(f"[{req_id}] 使用流响应时出错: {e}")
        raise
    finally:
        logger.info(f"[{req_id}] 流响应使用完成，数据接收状态: {data_received}")


async def clear_stream_queue():
    """清空流队列（与原始参考文件保持一致）"""
    from server import STREAM_QUEUE, logger
    import queue

    if STREAM_QUEUE is None:
        logger.info("流队列未初始化或已被禁用，跳过清空操作。")
        return

    while True:
        try:
            data_chunk = await asyncio.to_thread(STREAM_QUEUE.get_nowait)
            # logger.info(f"清空流式队列缓存，丢弃数据: {data_chunk}")
        except queue.Empty:
            logger.info("流式队列已清空 (捕获到 queue.Empty)。")
            break
        except Exception as e:
            logger.error(f"清空流式队列时发生意外错误: {e}", exc_info=True)
            break
    logger.info("流式队列缓存清空完毕。")


# --- Helper response generator ---
async def use_helper_get_response(helper_endpoint: str, helper_sapisid: str) -> AsyncGenerator[str, None]:
    """使用Helper服务获取响应的生成器"""
    from server import logger
    import aiohttp

    logger.info(f"正在尝试使用Helper端点: {helper_endpoint}")

    try:
        async with aiohttp.ClientSession() as session:
            headers = {
                'Content-Type': 'application/json',
                'Cookie': f'SAPISID={helper_sapisid}' if helper_sapisid else ''
            }
            
            async with session.get(helper_endpoint, headers=headers) as response:
                if response.status == 200:
                    async for chunk in response.content.iter_chunked(1024):
                        if chunk:
                            yield chunk.decode('utf-8', errors='ignore')
                else:
                    logger.error(f"Helper端点返回错误状态: {response.status}")
                    
    except Exception as e:
        logger.error(f"使用Helper端点时出错: {e}")


# --- 请求验证函数 ---
def validate_chat_request(messages: List[Message], req_id: str) -> Dict[str, Optional[str]]:
    """验证聊天请求"""
    from server import logger
    
    if not messages:
        raise ValueError(f"[{req_id}] 无效请求: 'messages' 数组缺失或为空。")
    
    if not any(msg.role != 'system' for msg in messages):
        raise ValueError(f"[{req_id}] 无效请求: 所有消息都是系统消息。至少需要一条用户或助手消息。")
    
    # 返回验证结果
    return {
        "error": None,
        "warning": None
    }


def extract_base64_to_local(base64_data: str) -> str:
    output_dir = os.path.join(os.path.dirname(__file__), '..', 'upload_images')
    match = re.match(r"data:image/(\w+);base64,(.*)", base64_data)
    if not match:
        print("错误: Base64 数据格式不正确。")
        return None

    image_type = match.group(1)  # 例如 "png", "jpeg"
    encoded_image_data = match.group(2)

    try:
        # 解码 Base64 字符串
        decoded_image_data = base64.b64decode(encoded_image_data)
    except base64.binascii.Error as e:
        print(f"错误: Base64 解码失败 - {e}")
        return None

    # 计算图片数据的 MD5 值
    md5_hash = hashlib.md5(decoded_image_data).hexdigest()

    # 确定文件扩展名和完整文件路径
    file_extension = f".{image_type}"
    output_filepath = os.path.join(output_dir, f"{md5_hash}{file_extension}")

    # 确保输出目录存在
    os.makedirs(output_dir, exist_ok=True)

    if os.path.exists(output_filepath):
        print(f"文件已存在，跳过保存: {output_filepath}")
        return output_filepath

    # 保存图片到文件
    try:
        with open(output_filepath, "wb") as f:
            f.write(decoded_image_data)
        print(f"图片已成功保存到: {output_filepath}")
        return output_filepath
    except IOError as e:
        print(f"错误: 保存文件失败 - {e}")
        return None


# --- 提示准备函数 ---
def prepare_combined_prompt(messages: List[Message], req_id: str) -> tuple[str, str, list]:
    """
    准备组合提示，为每个角色添加前缀并拼接所有消息。
    现在支持图片标记功能，为每张图片分配具体文件名标识符并在对应消息中标注。
    同时，它会分离出系统提示。
    """
    from server import logger
    
    logger.info(f"[{req_id}] (准备提示) 正在从 {len(messages)} 条消息准备组合提示 (包括历史)。")
    
    system_prompts = []
    combined_parts = []
    images_list = []
    image_counter = 1  # 全局图片计数器
    role_map = {"user": "用户", "assistant": "助手"}  # "system" 角色被特殊处理

    for i, msg in enumerate(messages):
        role = msg.role or 'unknown'

        if role == 'system':
            if isinstance(msg.content, str):
                system_prompts.append(msg.content.strip())
            continue  # 系统提示不加入主提示

        # 获取角色前缀，如果未知则使用首字母大写的角色名
        role_prefix = f"{role_map.get(role, role.capitalize())}: "
        
        content = msg.content or ''
        content_str = ""
        message_images = []  # 当前消息中的图片标识符

        if isinstance(content, str):
            content_str = content.strip()
        elif isinstance(content, list):
            # 处理多模态内容
            text_parts = []
            for item in content:
                if hasattr(item, 'type') and item.type == 'text':
                    text_parts.append(item.text or '')
                elif isinstance(item, dict) and item.get('type') == 'text':
                    text_parts.append(item.get('text', ''))
                elif hasattr(item, 'type') and item.type == 'image_url':
                    image_url_value = item.image_url.url
                    if image_url_value.startswith("data:image/"):
                        # 提取图片类型
                        match = re.match(r"data:image/(\w+);base64,", image_url_value)
                        if match:
                            img_type = match.group(1)
                            # 生成模拟临时文件名格式，与实际临时文件命名一致
                            # 使用请求ID的一部分和图片计数器来模拟 tempfile 的命名模式
                            import random
                            import string
                            rand_suffix = ''.join(random.choices(string.ascii_lowercase + string.digits, k=8))
                            filename = f"tmp{rand_suffix}.{img_type}"
                        else:
                            filename = f"image_{image_counter}.png"
                        
                        images_list.append(image_url_value)  # 保持原始base64格式以确保上传顺序
                        image_tag = f"[{filename}]"
                        message_images.append(image_tag)
                        image_counter += 1
                        logger.info(f"[{req_id}] 为图片分配标识符: {image_tag}")
                    else:
                        # 处理非base64的图片URL
                        images_list.append(image_url_value)
                        # 从URL中提取文件名，如果无法提取则使用通用标识符
                        try:
                            filename = os.path.basename(image_url_value.split('?')[0])  # 去除查询参数
                            if not filename or '.' not in filename:
                                filename = f"image_{image_counter}.png"
                        except:
                            filename = f"image_{image_counter}.png"
                        
                        image_tag = f"[{filename}]"
                        message_images.append(image_tag)
                        image_counter += 1
                        logger.info(f"[{req_id}] 为图片URL分配标识符: {image_tag}")
                else:
                    logger.warning(f"[{req_id}] (准备提示) 警告: 在索引 {i} 的消息中忽略非文本或未知类型的 content item")
            content_str = "\n".join(text_parts).strip()
        else:
            logger.warning(f"[{req_id}] (准备提示) 警告: 角色 {role} 在索引 {i} 的内容类型意外 ({type(content)}) 或为 None。")
            content_str = str(content or "").strip()

        if content_str or message_images:
            # 构建消息内容，如果有图片则添加图片标记
            message_content = content_str
            if message_images:
                # 在消息内容后面添加图片标记，用换行符分隔以确保清晰显示
                image_tags_str = " " + " ".join(message_images)
                if message_content:
                    message_content += image_tags_str
                else:
                    message_content = image_tags_str.strip()
            
            # 将前缀和内容结合
            combined_parts.append(f"{role_prefix}{message_content}")

    # 使用两个换行符来分隔不同的消息轮次
    final_prompt = "\n\n".join(combined_parts)
    
    system_prompt = "\n\n".join(system_prompts)
    if system_prompt:
        logger.info(f"[{req_id}] 提取到组合后的系统提示: '{system_prompt[:100]}...'")
    
    # 不再在提示末尾添加图片说明，图片标记已直接嵌入在对应消息中
    
    preview_text = final_prompt[:500].replace('\n', '\\n')
    logger.info(f"[{req_id}] (准备提示) 组合提示长度: {len(final_prompt)}，包含 {len(images_list)} 张图片。预览: '{preview_text}...'")
    
    # 返回处理后的提示和图片列表
    return system_prompt, final_prompt, images_list


def _get_image_message_index(messages: List[Message], image_num: int) -> int:
    """
    获取指定图片在第几轮对话中出现
    """
    image_counter = 1
    for i, msg in enumerate(messages):
        content = msg.content or ''
        if isinstance(content, list):
            for item in content:
                if hasattr(item, 'type') and item.type == 'image_url':
                    if image_counter == image_num:
                        # 只计算用户消息的轮次
                        user_message_count = sum(1 for m in messages[:i+1] if m.role == 'user')
                        return user_message_count
                    image_counter += 1
    return 1  # 默认返回第1轮


def estimate_tokens(text: str) -> int:
    """
    估算文本的token数量
    使用简单的字符计数方法：
    - 英文：大约4个字符 = 1个token
    - 中文：大约1.5个字符 = 1个token  
    - 混合文本：采用加权平均
    """
    if not text:
        return 0
    
    # 统计中文字符数量（包括中文标点）
    chinese_chars = sum(1 for char in text if '\u4e00' <= char <= '\u9fff' or '\u3000' <= char <= '\u303f' or '\uff00' <= char <= '\uffef')
    
    # 统计非中文字符数量
    non_chinese_chars = len(text) - chinese_chars
    
    # 计算token估算
    chinese_tokens = chinese_chars / 1.5  # 中文大约1.5字符/token
    english_tokens = non_chinese_chars / 4.0  # 英文大约4字符/token
    
    return max(1, int(chinese_tokens + english_tokens))


def calculate_usage_stats(messages: List[dict], response_content: str, reasoning_content: str = None) -> dict:
    """
    计算token使用统计
    
    Args:
        messages: 请求中的消息列表
        response_content: 响应内容
        reasoning_content: 推理内容（可选）
    
    Returns:
        包含token使用统计的字典
    """
    # 计算输入token（prompt tokens）
    prompt_text = ""
    for message in messages:
        role = message.get("role", "")
        content = message.get("content", "")
        prompt_text += f"{role}: {content}\n"
    
    prompt_tokens = estimate_tokens(prompt_text)
    
    # 计算输出token（completion tokens）
    completion_text = response_content or ""
    if reasoning_content:
        completion_text += reasoning_content
    
    completion_tokens = estimate_tokens(completion_text)
    
    # 总token数
    total_tokens = prompt_tokens + completion_tokens
    
    return {
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "total_tokens": total_tokens
    } 


def generate_sse_stop_chunk_with_usage(req_id: str, model: str, usage_stats: dict, reason: str = "stop") -> str:
    """生成带usage统计的SSE停止块"""
    return generate_sse_stop_chunk(req_id, model, reason, usage_stats) 