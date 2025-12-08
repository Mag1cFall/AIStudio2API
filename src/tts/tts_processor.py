import asyncio
import time
from typing import Dict, Any, Optional, Callable
from playwright.async_api import Page as AsyncPage
from .tts_controller import TTSController
from .models import SpeechConfig, parse_speech_config, should_use_tts


async def process_tts_request(
    req_id: str,
    page: AsyncPage,
    logger,
    request_data: Dict[str, Any],
    check_client_disconnected: Callable
) -> Dict[str, Any]:
    controller = TTSController(page, logger, req_id)
    
    model = request_data.get('model', 'gemini-2.5-flash-preview-tts')
    contents = request_data.get('contents', '')
    generation_config = request_data.get('generationConfig') or request_data.get('generation_config') or {}
    
    if isinstance(contents, list) and len(contents) > 0:
        first_content = contents[0]
        if isinstance(first_content, dict):
            parts = first_content.get('parts', [])
            if parts and isinstance(parts[0], dict):
                contents = parts[0].get('text', '')
            elif parts and isinstance(parts[0], str):
                contents = parts[0]
        elif isinstance(first_content, str):
            contents = first_content
    elif isinstance(contents, dict):
        parts = contents.get('parts', [])
        if parts and isinstance(parts[0], dict):
            contents = parts[0].get('text', '')
    
    speech_config = parse_speech_config(generation_config)
    is_multi_speaker = speech_config and speech_config.is_multi_speaker()
    
    logger.info(f'[{req_id}] 🎤 处理 TTS 请求 | Model: {model} | Multi-speaker: {is_multi_speaker}')
    
    await controller.navigate_to_tts_page(model, check_client_disconnected)
    
    if is_multi_speaker:
        await controller.set_tts_mode(True, check_client_disconnected)
        for idx, speaker_config in enumerate(speech_config.multi_speaker_voice_config.speaker_voice_configs):
            voice_name = speaker_config.voice_config.prebuilt_voice_config.voice_name
            await controller.set_voice(voice_name, idx, check_client_disconnected)
        await controller.fill_multi_speaker_text(contents, check_client_disconnected)
    else:
        await controller.set_tts_mode(False, check_client_disconnected)
        if speech_config and speech_config.voice_config:
            voice_name = speech_config.voice_config.prebuilt_voice_config.voice_name
            await controller.set_voice(voice_name, 0, check_client_disconnected)
        await controller.fill_single_speaker_text(contents, '', check_client_disconnected)
    
    await controller.run_generation(check_client_disconnected)
    
    audio_base64 = await controller.wait_for_audio(check_client_disconnected)
    
    response = {
        'candidates': [{
            'content': {
                'parts': [{
                    'inlineData': {
                        'mimeType': 'audio/wav',
                        'data': audio_base64
                    }
                }],
                'role': 'model'
            },
            'finishReason': 'STOP',
            'index': 0
        }],
        'usageMetadata': {
            'promptTokenCount': len(contents.split()) if isinstance(contents, str) else 0,
            'candidatesTokenCount': 0,
            'totalTokenCount': len(contents.split()) if isinstance(contents, str) else 0
        },
        'modelVersion': model
    }
    
    logger.info(f'[{req_id}] ✅ TTS 请求完成')
    return response
