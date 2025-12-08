# TTS 语音生成使用指南

本指南介绍如何使用 AIStudio2API 的 TTS (Text-to-Speech) 功能生成语音。

## 支持的模型

- `gemini-2.5-flash-preview-tts`
- `gemini-2.5-pro-preview-tts`

## 端点

- `POST /generate-speech`
- `POST /v1beta/models/{model}:generateContent` (兼容官方 API)

## 单说话人语音生成

### 基础示例

```bash
curl -X POST http://localhost:2048/generate-speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-preview-tts",
    "contents": "Hello, this is a test.",
    "generationConfig": {
      "responseModalities": ["AUDIO"],
      "speechConfig": {
        "voiceConfig": {
          "prebuiltVoiceConfig": {"voiceName": "Kore"}
        }
      }
    }
  }'
```

### Python 示例

```python
import requests
import base64

url = "http://localhost:2048/generate-speech"
payload = {
    "model": "gemini-2.5-flash-preview-tts",
    "contents": "Hello, this is a test.",
    "generationConfig": {
        "responseModalities": ["AUDIO"],
        "speechConfig": {
            "voiceConfig": {
                "prebuiltVoiceConfig": {"voiceName": "Kore"}
            }
        }
    }
}

response = requests.post(url, json=payload)
data = response.json()

# 提取 Base64 音频数据
audio_base64 = data['candidates'][0]['content']['parts'][0]['inlineData']['data']

# 保存为 WAV 文件
with open('output.wav', 'wb') as f:
    f.write(base64.b64decode(audio_base64))
```

### PowerShell 示例

```powershell
$url = "http://localhost:2048/generate-speech"
$body = @{
    model = "gemini-2.5-flash-preview-tts"
    contents = "Hello world"
    generationConfig = @{
        responseModalities = @("AUDIO")
        speechConfig = @{
            voiceConfig = @{
                prebuiltVoiceConfig = @{
                    voiceName = "Kore"
                }
            }
        }
    }
} | ConvertTo-Json -Depth 5

$response = Invoke-RestMethod -Uri $url -Method Post -ContentType "application/json" -Body $body
$base64Audio = $response.candidates[0].content.parts[0].inlineData.data
[System.IO.File]::WriteAllBytes("C:\output.wav", [Convert]::FromBase64String($base64Audio))
```

## 多说话人语音生成

### 基础示例

```bash
curl -X POST http://localhost:2048/generate-speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-preview-tts",
    "contents": "Joe: How are you today?\nJane: I am doing great, thanks!",
    "generationConfig": {
      "responseModalities": ["AUDIO"],
      "speechConfig": {
        "multiSpeakerVoiceConfig": {
          "speakerVoiceConfigs": [
            {
              "speaker": "Joe",
              "voiceConfig": {
                "prebuiltVoiceConfig": {"voiceName": "Kore"}
              }
            },
            {
              "speaker": "Jane",
              "voiceConfig": {
                "prebuiltVoiceConfig": {"voiceName": "Puck"}
              }
            }
          ]
        }
      }
    }
  }'
```

### Python 示例

```python
import requests
import base64

url = "http://localhost:2048/generate-speech"
payload = {
    "model": "gemini-2.5-flash-preview-tts",
    "contents": "Joe: How are you?\nJane: I'm fine, thanks!",
    "generationConfig": {
        "responseModalities": ["AUDIO"],
        "speechConfig": {
            "multiSpeakerVoiceConfig": {
                "speakerVoiceConfigs": [
                    {
                        "speaker": "Joe",
                        "voiceConfig": {
                            "prebuiltVoiceConfig": {"voiceName": "Kore"}
                        }
                    },
                    {
                        "speaker": "Jane",
                        "voiceConfig": {
                            "prebuiltVoiceConfig": {"voiceName": "Puck"}
                        }
                    }
                ]
            }
        }
    }
}

response = requests.post(url, json=payload)
data = response.json()
audio_base64 = data['candidates'][0]['content']['parts'][0]['inlineData']['data']

with open('conversation.wav', 'wb') as f:
    f.write(base64.b64decode(audio_base64))
```

## 可用语音列表

共 30 种预置语音可选：

| 语音名称 | 风格 | 语音名称 | 风格 |
|---------|------|---------|------|
| Zephyr | Bright | Puck | Upbeat |
| Charon | Informative | Kore | Firm |
| Fenrir | Excitable | Leda | Youthful |
| Orus | Firm | Aoede | Breezy |
| Callirrhoe | Easy-going | Autonoe | Bright |
| Enceladus | Breathy | Iapetus | Clear |
| Umbriel | Easy-going | Algieba | Smooth |
| Despina | Smooth | Erinome | Clear |
| Algenib | Gravelly | Rasalgethi | Informative |
| Laomedeia | Upbeat | Achernar | Soft |
| Alnilam | Firm | Schedar | Even |
| Gacrux | Mature | Pulcherrima | Forward |
| Achird | Friendly | Zubenelgenubi | Casual |
| Vindemiatrix | Gentle | Sadachbia | Lively |
| Sadaltager | Knowledgeable | Sulafat | Warm |

## 响应格式

成功的响应格式如下：

```json
{
  "candidates": [{
    "content": {
      "parts": [{
        "inlineData": {
          "mimeType": "audio/wav",
          "data": "<Base64 编码的 WAV 音频数据>"
        }
      }],
      "role": "model"
    },
    "finishReason": "STOP",
    "index": 0
  }],
  "usageMetadata": {
    "promptTokenCount": 3,
    "candidatesTokenCount": 0,
    "totalTokenCount": 3
  },
  "modelVersion": "gemini-2.5-flash-preview-tts"
}
```

## 支持的语言

TTS 模型自动检测输入语言，支持以下 24 种语言：

- 阿拉伯语（埃及）`ar-EG`
- 英语（美国）`en-US`
- 法语（法国）`fr-FR`
- 德语（德国）`de-DE`
- 西班牙语（美国）`es-US`
- 印地语（印度）`hi-IN`
- 印尼语（印度尼西亚）`id-ID`
- 意大利语（意大利）`it-IT`
- 日语（日本）`ja-JP`
- 韩语（韩国）`ko-KR`
- 葡萄牙语（巴西）`pt-BR`
- 俄语（俄罗斯）`ru-RU`
- 荷兰语（荷兰）`nl-NL`
- 波兰语（波兰）`pl-PL`
- 泰语（泰国）`th-TH`
- 土耳其语（土耳其）`tr-TR`
- 越南语（越南）`vi-VN`
- 罗马尼亚语（罗马尼亚）`ro-RO`
- 乌克兰语（乌克兰）`uk-UA`
- 孟加拉语（孟加拉国）`bn-BD`
- 英语（印度）`en-IN`
- 马拉地语（印度）`mr-IN`
- 泰米尔语（印度）`ta-IN`
- 泰卢固语（印度）`te-IN`

## 限制

- TTS 模型仅接受文本输入，生成音频输出
- 上下文窗口限制为 32k tokens
- 音频格式为 WAV，采样率 24kHz，单声道，16-bit PCM

## 故障排除

### 音频无法播放

确保正确解码 Base64 数据：

```python
import base64
audio_data = base64.b64decode(base64_string)
```

### 语音名称无效

检查语音名称拼写是否正确，区分大小写。

### 模型不可用

确保使用的是 TTS 专用模型：
- `gemini-2.5-flash-preview-tts`
- `gemini-2.5-pro-preview-tts`
