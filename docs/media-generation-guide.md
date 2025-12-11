# 媒体生成使用指南

本指南介绍如何使用 AIStudio2API 的图片和视频生成功能。

## 支持的模型

### 图片生成
- **Imagen 3**: `imagen-3.0-generate-002`
- **Nano Banana (Gemini 2.5 Flash 图片)**: `gemini-2.5-flash-image`

### 视频生成
- **Veo 2**: `veo-2.0-generate-001`

## 端点

| 功能 | 端点 | 方法 |
|------|------|------|
| Imagen 图片生成 | `/generate-image` | POST |
| Veo 视频生成 | `/generate-video` | POST |
| Nano Banana 图片生成 | `/nano/generate` | POST |

---

## Imagen 图片生成

### 基础示例

```bash
curl -X POST http://localhost:2048/generate-image \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A beautiful sunset over mountains",
    "model": "imagen-3.0-generate-002",
    "number_of_images": 1,
    "aspect_ratio": "16:9"
  }'
```

### Python 示例

```python
import requests
import base64

url = "http://localhost:2048/generate-image"
payload = {
    "prompt": "A futuristic city at night",
    "model": "imagen-3.0-generate-002",
    "number_of_images": 2,
    "aspect_ratio": "1:1",
    "negative_prompt": "blurry, low quality"
}

response = requests.post(url, json=payload)
data = response.json()

for i, img in enumerate(data['generatedImages']):
    image_bytes = base64.b64decode(img['image']['imageBytes'])
    with open(f'imagen_output_{i}.png', 'wb') as f:
        f.write(image_bytes)
```

### PowerShell 示例

```powershell
$body = @{
    prompt = "A beautiful landscape"
    model = "imagen-3.0-generate-002"
    number_of_images = 1
    aspect_ratio = "16:9"
} | ConvertTo-Json

$response = Invoke-RestMethod -Uri "http://localhost:2048/generate-image" -Method Post -ContentType "application/json" -Body $body
$imageData = $response.generatedImages[0].image.imageBytes
[System.IO.File]::WriteAllBytes("C:\output.png", [Convert]::FromBase64String($imageData))
```

### 请求参数

| 参数 | 类型 | 必需 | 默认值 | 说明 |
|------|------|------|--------|------|
| prompt | string | ✅ | - | 图片生成提示词 |
| model | string | ❌ | imagen-3.0-generate-002 | 模型名称 |
| number_of_images | int | ❌ | 1 | 生成图片数量 (1-4) |
| aspect_ratio | string | ❌ | 1:1 | 宽高比 (1:1, 16:9, 9:16, 4:3, 3:4) |
| negative_prompt | string | ❌ | - | 负面提示词 |

---

## Veo 视频生成

### 基础示例

```bash
curl -X POST http://localhost:2048/generate-video \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A drone flying over a forest",
    "model": "veo-2.0-generate-001",
    "aspect_ratio": "16:9",
    "duration_seconds": 5
  }'
```

### Python 示例

```python
import requests
import base64

url = "http://localhost:2048/generate-video"
payload = {
    "prompt": "Ocean waves crashing on a beach at sunset",
    "model": "veo-2.0-generate-001",
    "number_of_videos": 1,
    "aspect_ratio": "16:9",
    "duration_seconds": 5
}

response = requests.post(url, json=payload, timeout=600)
data = response.json()

for i, vid in enumerate(data['generatedVideos']):
    video_bytes = base64.b64decode(vid['video']['videoBytes'])
    with open(f'veo_output_{i}.mp4', 'wb') as f:
        f.write(video_bytes)
```

### 图片转视频

```python
import requests
import base64

with open('input_image.jpg', 'rb') as f:
    image_base64 = base64.b64encode(f.read()).decode()

url = "http://localhost:2048/generate-video"
payload = {
    "prompt": "Make this image come alive with gentle movement",
    "model": "veo-2.0-generate-001",
    "aspect_ratio": "16:9",
    "duration_seconds": 5,
    "image": image_base64
}

response = requests.post(url, json=payload, timeout=600)
data = response.json()

video_bytes = base64.b64decode(data['generatedVideos'][0]['video']['videoBytes'])
with open('output_video.mp4', 'wb') as f:
    f.write(video_bytes)
```

### 请求参数

| 参数 | 类型 | 必需 | 默认值 | 说明 |
|------|------|------|--------|------|
| prompt | string | ✅ | - | 视频生成提示词 |
| model | string | ❌ | veo-2.0-generate-001 | 模型名称 |
| number_of_videos | int | ❌ | 1 | 生成视频数量 |
| aspect_ratio | string | ❌ | 16:9 | 宽高比 |
| duration_seconds | int | ❌ | 5 | 视频时长 (秒) |
| negative_prompt | string | ❌ | - | 负面提示词 |
| image | string | ❌ | - | Base64 编码的输入图片 (图片转视频) |

---

## Nano Banana (Gemini 图片生成)

Nano Banana 使用 Gemini 2.5 Flash 模型进行图片生成，支持文本和图片输入。

### 基础示例

```bash
curl -X POST http://localhost:2048/nano/generate \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-image",
    "contents": [{
      "parts": [{"text": "A cute cat wearing a tiny hat"}]
    }]
  }'
```

### Python 示例

```python
import requests
import base64

url = "http://localhost:2048/nano/generate"
payload = {
    "model": "gemini-2.5-flash-image",
    "contents": [{
        "parts": [{"text": "A watercolor painting of a mountain lake"}]
    }]
}

response = requests.post(url, json=payload, timeout=200)
data = response.json()

parts = data['candidates'][0]['content']['parts']
for i, part in enumerate(parts):
    if 'inlineData' in part:
        image_bytes = base64.b64decode(part['inlineData']['data'])
        with open(f'nano_output_{i}.png', 'wb') as f:
            f.write(image_bytes)
```

### 图片编辑 (图片输入)

```python
import requests
import base64

with open('input_image.jpg', 'rb') as f:
    image_base64 = base64.b64encode(f.read()).decode()

url = "http://localhost:2048/nano/generate"
payload = {
    "model": "gemini-2.5-flash-image",
    "contents": [{
        "parts": [
            {"text": "Make this image look like a cartoon"},
            {
                "inlineData": {
                    "mimeType": "image/jpeg",
                    "data": image_base64
                }
            }
        ]
    }]
}

response = requests.post(url, json=payload, timeout=200)
data = response.json()

parts = data['candidates'][0]['content']['parts']
for i, part in enumerate(parts):
    if 'inlineData' in part:
        image_bytes = base64.b64decode(part['inlineData']['data'])
        with open(f'edited_output_{i}.png', 'wb') as f:
            f.write(image_bytes)
```

### 请求参数

| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| model | string | ❌ | 默认 gemini-2.5-flash-image |
| contents | array | ✅ | 内容数组，包含 parts |
| contents[].parts | array | ✅ | 内容部分，支持 text 和 inlineData |

---

## 响应格式

### Imagen 响应

```json
{
  "generatedImages": [
    {
      "image": {
        "imageBytes": "<Base64 编码的图片数据>",
        "mimeType": "image/png"
      }
    }
  ],
  "modelVersion": "imagen-3.0-generate-002"
}
```

### Veo 响应

```json
{
  "generatedVideos": [
    {
      "video": {
        "videoBytes": "<Base64 编码的视频数据>",
        "mimeType": "video/mp4"
      }
    }
  ],
  "modelVersion": "veo-2.0-generate-001"
}
```

### Nano Banana 响应

```json
{
  "candidates": [{
    "content": {
      "parts": [
        {
          "inlineData": {
            "mimeType": "image/png",
            "data": "<Base64 编码的图片数据>"
          }
        }
      ],
      "role": "model"
    },
    "finishReason": "STOP"
  }],
  "modelVersion": "gemini-2.5-flash-image"
}
```

---

## 限制

- **Imagen**: 单次最多生成 4 张图片
- **Veo**: 视频生成可能需要 2-5 分钟，请设置足够的超时时间
- **Nano Banana**: 支持文本+图片混合输入

## 故障排除

### 超时错误

视频生成需要较长时间，建议设置 600 秒以上的超时：

```python
response = requests.post(url, json=payload, timeout=600)
```

### 图片无法保存

确保正确解码 Base64 数据：

```python
import base64
image_bytes = base64.b64decode(base64_string)
```

### 模型不可用

检查模型名称是否正确：
- Imagen: `imagen-3.0-generate-002`
- Veo: `veo-2.0-generate-001`
- Nano Banana: `gemini-2.5-flash-image`
