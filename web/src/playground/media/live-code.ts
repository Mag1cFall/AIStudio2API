import type { CodeOption } from '../code'

// LiveCodeLanguage 是 Live 页示例代码的语言
export type LiveCodeLanguage = 'python' | 'typescript'

// liveCodeOptions 是 Live 页获取代码弹窗的语言选项
export const liveCodeOptions: CodeOption<LiveCodeLanguage>[] = [
  { value: 'python', label: 'Python', highlight: 'python', file: 'main.py' },
  { value: 'typescript', label: 'TypeScript', highlight: 'typescript', file: 'main.ts' },
]

// LiveCodeInput 是 Live 示例代码所需的服务地址、setup 帧与提示词
export interface LiveCodeInput {
  baseURL: string
  setup: Record<string, unknown>
  prompt: string
}

// audioInput 表示会话以音频文件为输入：实时翻译与转录模型只接受音频
function audioInput(input: LiveCodeInput): boolean {
  return !(input.setup.input_modalities as string[]).includes('text')
}

// pythonCode 生成经本服务 /v1/live 发送提示词或音频文件、保存回复音频并打印转写的 Python 示例
function pythonCode(input: LiveCodeInput): string {
  const url = input.baseURL.replace(/^http/, 'ws')
  const setup = JSON.stringify(input.setup, null, 4).replace(/\n/g, '\n        ')
  const send = audioInput(input)
    ? `        # input.wav: 16 kHz, 16-bit, mono PCM
        with wave.open("input.wav", "rb") as source:
            while chunk := source.readframes(1600):
                await socket.send(json.dumps({"type": "audio", "mime_type": "audio/pcm", "data": base64.b64encode(chunk).decode()}))
                await asyncio.sleep(0.1)
        await socket.send(json.dumps({"type": "media_end"}))`
    : `        await socket.send(json.dumps({"type": "text", "text": ${JSON.stringify(input.prompt || 'INSERT_INPUT_HERE')}}))`
  return `# To run this code you need to install the following dependencies:
# pip install websockets

import asyncio
import base64
import json
import os
import wave

import websockets

URL = "${url}/v1/live?key=" + os.environ.get("GEMINI_API_KEY", "")


async def main():
    async with websockets.connect(URL, max_size=None) as socket:
        await socket.send(json.dumps(${setup}))
        while json.loads(await socket.recv())["type"] != "setup_complete":
            pass
${send}

        with wave.open("output.wav", "wb") as output:
            output.setnchannels(1)
            output.setsampwidth(2)
            output.setframerate(24000)
            async for raw in socket:
                message = json.loads(raw)
                if message["type"] == "media":
                    output.writeframes(base64.b64decode(message["data"]))
                elif message["type"] in ("text", "input_transcription", "output_transcription"):
                    print(message["type"], message.get("text", ""))
                elif message["type"] in ("turn_complete", "error", "closed"):
                    if message["type"] == "error":
                        print(message)
                    break
        await socket.send(json.dumps({"type": "close"}))


asyncio.run(main())
`
}

// typescriptCode 生成经本服务 /v1/live 发送提示词或音频文件、保存回复音频并打印转写的 TypeScript 示例
function typescriptCode(input: LiveCodeInput): string {
  const url = input.baseURL.replace(/^http/, 'ws')
  const setup = JSON.stringify(input.setup, null, 2).replace(/\n/g, '\n  ')
  const send = audioInput(input)
    ? `    // input.wav: 16 kHz, 16-bit, mono PCM
    const pcm = readFileSync('input.wav').subarray(44);
    for (let offset = 0; offset < pcm.length; offset += 3200) {
      socket.send(JSON.stringify({ type: 'audio', mime_type: 'audio/pcm', data: pcm.subarray(offset, offset + 3200).toString('base64') }));
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    socket.send(JSON.stringify({ type: 'media_end' }));`
    : `    socket.send(JSON.stringify({ type: 'text', text: ${JSON.stringify(input.prompt || 'INSERT_INPUT_HERE')} }));`
  return `// To run this code you need to install the following dependencies:
// npm install ws
// npm install -D @types/ws @types/node

import { readFileSync, writeFileSync } from 'fs';
import WebSocket from 'ws';

const socket = new WebSocket(\`${url}/v1/live?key=\${process.env['GEMINI_API_KEY'] ?? ''}\`);
const chunks: Buffer[] = [];

socket.on('open', () => {
  socket.send(JSON.stringify(${setup}));
});

socket.on('message', async (raw) => {
  const message = JSON.parse(raw.toString());
  if (message.type === 'setup_complete') {
${send}
  } else if (message.type === 'media') {
    chunks.push(Buffer.from(message.data, 'base64'));
  } else if (['text', 'input_transcription', 'output_transcription'].includes(message.type)) {
    console.log(message.type, message.text ?? '');
  } else if (['turn_complete', 'error', 'closed'].includes(message.type)) {
    if (message.type === 'error') console.error(message);
    finish();
  }
});

function finish() {
  socket.send(JSON.stringify({ type: 'close' }));
  socket.close();
  const bytes = chunks.reduce((total, chunk) => total + chunk.length, 0);
  const header = Buffer.alloc(44);
  header.write('RIFF', 0);
  header.writeUInt32LE(36 + bytes, 4);
  header.write('WAVE', 8);
  header.write('fmt ', 12);
  header.writeUInt32LE(16, 16);
  header.writeUInt16LE(1, 20);
  header.writeUInt16LE(1, 22);
  header.writeUInt32LE(24000, 24);
  header.writeUInt32LE(48000, 28);
  header.writeUInt16LE(2, 32);
  header.writeUInt16LE(16, 34);
  header.write('data', 36);
  header.writeUInt32LE(bytes, 40);
  writeFileSync('output.wav', Buffer.concat([header, ...chunks]));
}
`
}

// liveCode 按语言生成 Live 示例代码
export function liveCode(language: LiveCodeLanguage, input: LiveCodeInput): string {
  return language === 'python' ? pythonCode(input) : typescriptCode(input)
}
