from dataclasses import dataclass, field
from typing import Optional, List


@dataclass
class PrebuiltVoiceConfig:
    voice_name: str = 'Kore'


@dataclass
class VoiceConfig:
    prebuilt_voice_config: Optional[PrebuiltVoiceConfig] = None


@dataclass
class SpeakerVoiceConfig:
    speaker: str
    voice_config: VoiceConfig


@dataclass
class MultiSpeakerVoiceConfig:
    speaker_voice_configs: List[SpeakerVoiceConfig] = field(default_factory=list)


@dataclass
class SpeechConfig:
    voice_config: Optional[VoiceConfig] = None
    multi_speaker_voice_config: Optional[MultiSpeakerVoiceConfig] = None

    def is_multi_speaker(self) -> bool:
        return self.multi_speaker_voice_config is not None and len(self.multi_speaker_voice_config.speaker_voice_configs) > 0


def parse_speech_config(config_dict: Optional[dict]) -> Optional[SpeechConfig]:
    if not config_dict:
        return None
    
    speech_config_dict = config_dict.get('speechConfig') or config_dict.get('speech_config')
    if not speech_config_dict:
        return None
    
    voice_config = None
    voice_config_dict = speech_config_dict.get('voiceConfig') or speech_config_dict.get('voice_config')
    if voice_config_dict:
        prebuilt_dict = voice_config_dict.get('prebuiltVoiceConfig') or voice_config_dict.get('prebuilt_voice_config')
        if prebuilt_dict:
            voice_name = prebuilt_dict.get('voiceName') or prebuilt_dict.get('voice_name') or 'Kore'
            voice_config = VoiceConfig(prebuilt_voice_config=PrebuiltVoiceConfig(voice_name=voice_name))
    
    multi_speaker_config = None
    multi_dict = speech_config_dict.get('multiSpeakerVoiceConfig') or speech_config_dict.get('multi_speaker_voice_config')
    if multi_dict:
        speaker_configs = []
        configs_list = multi_dict.get('speakerVoiceConfigs') or multi_dict.get('speaker_voice_configs') or []
        for sc in configs_list:
            speaker = sc.get('speaker', '')
            vc_dict = sc.get('voiceConfig') or sc.get('voice_config') or {}
            prebuilt = vc_dict.get('prebuiltVoiceConfig') or vc_dict.get('prebuilt_voice_config') or {}
            vn = prebuilt.get('voiceName') or prebuilt.get('voice_name') or 'Kore'
            speaker_configs.append(SpeakerVoiceConfig(
                speaker=speaker,
                voice_config=VoiceConfig(prebuilt_voice_config=PrebuiltVoiceConfig(voice_name=vn))
            ))
        multi_speaker_config = MultiSpeakerVoiceConfig(speaker_voice_configs=speaker_configs)
    
    return SpeechConfig(voice_config=voice_config, multi_speaker_voice_config=multi_speaker_config)


def should_use_tts(model: str, generation_config: Optional[dict]) -> bool:
    if not model:
        return False
    if 'tts' in model.lower():
        return True
    if generation_config:
        modalities = generation_config.get('responseModalities') or generation_config.get('response_modalities') or []
        if 'AUDIO' in modalities:
            return True
    return False
