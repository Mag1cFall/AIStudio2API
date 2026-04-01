from typing import Any, Dict, List, Optional, Union

from pydantic import BaseModel, ConfigDict, model_validator

from config import MODEL_NAME


class ClientDisconnectedError(Exception):
    pass


class ElementClickError(Exception):
    pass


class FunctionCall(BaseModel):
    name: str
    arguments: str


class ToolCall(BaseModel):
    id: str
    type: str = "function"
    function: FunctionCall


class ImageURL(BaseModel):
    url: str


class MessageContentItem(BaseModel):
    type: str
    text: Optional[str] = None
    image_url: Optional[ImageURL] = None


class Message(BaseModel):
    role: str
    content: Union[str, List[MessageContentItem], None] = None
    name: Optional[str] = None
    tool_calls: Optional[List[ToolCall]] = None
    tool_call_id: Optional[str] = None


class ChatCompletionRequest(BaseModel):
    model_config = ConfigDict(extra="allow", populate_by_name=True)

    messages: List[Message]
    model: Optional[str] = MODEL_NAME
    stream: bool = False
    temperature: Optional[float] = None
    max_output_tokens: Optional[int] = None
    stop: Optional[Union[str, List[str]]] = None
    top_p: Optional[float] = None
    reasoning_effort: Optional[Union[int, str]] = None
    tools: Optional[List[Dict[str, Any]]] = None

    @model_validator(mode="before")
    @classmethod
    def _normalize_max_tokens(cls, data):
        if isinstance(data, dict) and "max_tokens" in data and "max_output_tokens" not in data:
            data["max_output_tokens"] = data.pop("max_tokens")
        return data


class TTSRequest(BaseModel):
    model_config = ConfigDict(extra="allow")

    model: str = "gemini-2.5-flash-preview-tts"
    contents: Any = None
    generationConfig: Optional[Dict[str, Any]] = None
    generation_config: Optional[Dict[str, Any]] = None


class ImagenRequest(BaseModel):
    model_config = ConfigDict(extra="allow")

    prompt: str
    model: str = "imagen-4.0-generate-001"
    number_of_images: int = 1
    aspect_ratio: str = "1:1"
    negative_prompt: Optional[str] = None


class VeoRequest(BaseModel):
    model_config = ConfigDict(extra="allow")

    prompt: str
    model: str = "veo-2.0-generate-001"
    number_of_videos: int = 1
    aspect_ratio: str = "16:9"
    duration_seconds: int = 5
    negative_prompt: Optional[str] = None
    image: Optional[Union[str, Dict[str, Any]]] = None


class NanoRequest(BaseModel):
    model_config = ConfigDict(extra="allow")

    model: str = "gemini-2.5-flash-image"
    contents: Any = None
    prompt: Optional[str] = None
    aspect_ratio: str = "1:1"
    image: Optional[Union[str, Dict[str, Any]]] = None
    generationConfig: Optional[Dict[str, Any]] = None
    generation_config: Optional[Dict[str, Any]] = None
