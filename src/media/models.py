from dataclasses import dataclass, field
from typing import Optional, List, Literal, Union

IMAGEN_SUPPORTED_MODELS = ['imagen-4.0-generate-001', 'imagen-4.0-ultra-generate-001', 'imagen-4.0-fast-generate-001']
VEO_SUPPORTED_MODELS = ['veo-2.0-generate-001']
NANO_SUPPORTED_MODELS = ['gemini-2.5-flash-image']

IMAGEN_ASPECT_RATIOS = ['1:1', '3:4', '4:3', '9:16', '16:9']
VEO_ASPECT_RATIOS = ['16:9', '9:16']
VEO_DURATIONS = [5, 6, 8]

@dataclass
class ImageGenerationConfig:
    prompt: str
    model: str = 'imagen-4.0-generate-001'
    number_of_images: int = 1
    aspect_ratio: str = '1:1'
    negative_prompt: Optional[str] = None

@dataclass
class VideoGenerationConfig:
    prompt: str
    model: str = 'veo-2.0-generate-001'
    number_of_videos: int = 1
    aspect_ratio: str = '16:9'
    duration_seconds: int = 5
    negative_prompt: Optional[str] = None
    image_bytes: Optional[bytes] = None
    image_mime_type: Optional[str] = None

@dataclass
class NanoBananaConfig:
    prompt: str
    model: str = 'gemini-2.5-flash-image'
    aspect_ratio: str = '1:1'
    image_bytes: Optional[bytes] = None
    image_mime_type: Optional[str] = None

@dataclass
class GeneratedImage:
    image_bytes: bytes
    mime_type: str = 'image/png'
    index: int = 0

@dataclass
class GeneratedVideo:
    video_bytes: bytes
    mime_type: str = 'video/mp4'
    index: int = 0

@dataclass
class GeneratedContent:
    text: Optional[str] = None
    images: List[GeneratedImage] = field(default_factory=list)
    videos: List[GeneratedVideo] = field(default_factory=list)

def is_imagen_model(model: str) -> bool:
    return model in IMAGEN_SUPPORTED_MODELS

def is_veo_model(model: str) -> bool:
    return model in VEO_SUPPORTED_MODELS

def is_nano_model(model: str) -> bool:
    return model in NANO_SUPPORTED_MODELS

def is_media_model(model: str) -> bool:
    return is_imagen_model(model) or is_veo_model(model) or is_nano_model(model)
