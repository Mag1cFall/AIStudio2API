# AIStudio2API Test Suite

## Quick Start

```bash
# Run all tests (skip slow Veo)
python test_api.py --skip-veo

# Run all tests including Veo (5+ min)
python test_api.py

# Custom API URL
API_BASE_URL=http://localhost:2048 python test_api.py
```

## Test Coverage

| Endpoint | Test |
|----------|------|
| `/health` | Health check |
| `/v1/models` | Model list |
| `/v1/chat/completions` | Chat (non-stream) |
| `/generate-speech` | TTS single speaker |
| `/generate-image` | Imagen |
| `/nano/generate` | Nano text-to-image |
| `/generate-video` | Veo text-to-video |

## Output Files

Test outputs are saved to `test_output/`:
- `tts_output.wav`
- `imagen_output.png`
- `nano_output_0.png`
- `veo_output.mp4`

## Future Tests

- [ ] Streaming chat completions
- [ ] TTS multi-speaker
- [ ] Nano image editing (with input image)
- [ ] Veo image-to-video
- [ ] Concurrent stress tests
- [ ] Unit tests with pytest
