import base64
from typing import Dict, Any, Callable, Optional
from playwright.async_api import Page as AsyncPage

from .models import (
    NanoBananaConfig, ImageGenerationConfig, VideoGenerationConfig,
    GeneratedContent, GeneratedImage, GeneratedVideo,
    is_nano_model, is_imagen_model, is_veo_model
)
from .nano_controller import NanoController
from .imagen_controller import ImagenController
from .veo_controller import VeoController


async def process_nano_request(
    page: AsyncPage,
    config: NanoBananaConfig,
    logger,
    req_id: str,
    check_client_disconnected: Callable
) -> Dict[str, Any]:
    controller = NanoController(page, logger, req_id)
    
    await controller.navigate_to_nano_page(config.model, check_client_disconnected)
    
    if config.aspect_ratio and config.aspect_ratio != '1:1':
        await controller.set_aspect_ratio(config.aspect_ratio, check_client_disconnected)
    
    if config.image_bytes:
        await controller.upload_image(
            config.image_bytes,
            config.image_mime_type or 'image/png',
            check_client_disconnected
        )
    
    await controller.fill_prompt(config.prompt, check_client_disconnected)
    await controller.run_generation(check_client_disconnected)
    
    content = await controller.wait_for_content(check_client_disconnected)
    
    response_parts = []
    
    if content.text:
        response_parts.append({
            'text': content.text
        })
    
    for img in content.images:
        response_parts.append({
            'inlineData': {
                'mimeType': img.mime_type,
                'data': base64.b64encode(img.image_bytes).decode('utf-8')
            }
        })
    
    return {
        'candidates': [{
            'content': {
                'parts': response_parts,
                'role': 'model'
            },
            'finishReason': 'STOP'
        }],
        'modelVersion': config.model
    }


async def process_image_request(
    page: AsyncPage,
    config: ImageGenerationConfig,
    logger,
    req_id: str,
    check_client_disconnected: Callable
) -> Dict[str, Any]:
    controller = ImagenController(page, logger, req_id)
    
    await controller.navigate_to_imagen_page(config.model, check_client_disconnected)
    
    if config.number_of_images and config.number_of_images != 1:
        await controller.set_number_of_images(config.number_of_images, check_client_disconnected)
    
    if config.aspect_ratio and config.aspect_ratio != '1:1':
        await controller.set_aspect_ratio(config.aspect_ratio, check_client_disconnected)
    
    if config.negative_prompt:
        await controller.set_negative_prompt(config.negative_prompt, check_client_disconnected)
    
    await controller.fill_prompt(config.prompt, check_client_disconnected)
    await controller.run_generation(check_client_disconnected)
    
    images = await controller.wait_for_images(config.number_of_images, check_client_disconnected)
    
    generated_images = []
    for img in images:
        generated_images.append({
            'image': {
                'imageBytes': base64.b64encode(img.image_bytes).decode('utf-8'),
                'mimeType': img.mime_type
            }
        })
    
    return {
        'generatedImages': generated_images,
        'modelVersion': config.model
    }


async def process_video_request(
    page: AsyncPage,
    config: VideoGenerationConfig,
    logger,
    req_id: str,
    check_client_disconnected: Callable
) -> Dict[str, Any]:
    controller = VeoController(page, logger, req_id)
    
    await controller.navigate_to_veo_page(config.model, check_client_disconnected)
    
    if config.number_of_videos and config.number_of_videos != 1:
        await controller.set_number_of_videos(config.number_of_videos, check_client_disconnected)
    
    if config.aspect_ratio:
        await controller.set_aspect_ratio(config.aspect_ratio, check_client_disconnected)
    
    if config.duration_seconds:
        await controller.set_duration(config.duration_seconds, check_client_disconnected)
    
    if config.negative_prompt:
        await controller.set_negative_prompt(config.negative_prompt, check_client_disconnected)
    
    if config.image_bytes:
        await controller.upload_image(
            config.image_bytes,
            config.image_mime_type or 'image/png',
            check_client_disconnected
        )
    
    await controller.fill_prompt(config.prompt, check_client_disconnected)
    await controller.run_generation(check_client_disconnected)
    
    videos = await controller.wait_for_videos(config.number_of_videos, check_client_disconnected)
    
    generated_videos = []
    for vid in videos:
        generated_videos.append({
            'video': {
                'videoBytes': base64.b64encode(vid.video_bytes).decode('utf-8'),
                'mimeType': vid.mime_type
            }
        })
    
    return {
        'generatedVideos': generated_videos,
        'modelVersion': config.model
    }
