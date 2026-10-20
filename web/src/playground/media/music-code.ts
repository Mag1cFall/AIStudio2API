import type { CodeOption } from '../code'
import type { InlineImage } from './api'
import { pyTriple, tsTemplate } from './code'

// MusicCodeLanguage 是音乐页获取代码的语言，与官网相同只有 Python 与 TypeScript
export type MusicCodeLanguage = 'python' | 'typescript'

// musicCodeOptions 是音乐页获取代码的语言选项
export const musicCodeOptions: CodeOption<MusicCodeLanguage>[] = [
  { value: 'python', label: 'Python', highlight: 'python', file: 'main.py' },
  { value: 'typescript', label: 'TypeScript', highlight: 'typescript', file: 'main.ts' },
]

// MusicCodeInput 是音乐页示例代码所需的提示词、参考图片与服务地址
export interface MusicCodeInput {
  baseURL: string
  model: string
  prompt: string
  images: readonly InlineImage[]
}

// imageFile 返回第 index 张参考图片在示例代码中的本地文件名
function imageFile(image: InlineImage, index: number): string {
  return `image_${index + 1}.${image.mimeType.split('/')[1]?.replace('jpeg', 'jpg') ?? 'png'}`
}

// musicPrompt 返回示例代码中的提示词，空输入为官网占位
function musicPrompt(input: MusicCodeInput): string {
  return input.prompt.trim() === '' ? 'INSERT_INPUT_HERE' : input.prompt
}

// musicPython 按官网写法生成 google-genai Python 音乐示例，经本服务地址流式生成并保存音频
function musicPython(input: MusicCodeInput): string {
  const images = input.images
    .map(
      (image, index) =>
        `                types.Part.from_bytes(\n                    data=open("${imageFile(image, index)}", "rb").read(),\n                    mime_type="${image.mimeType}",\n                ),\n`,
    )
    .join('')
  return `# To run this code you need to install the following dependencies:
# pip install google-genai

import mimetypes
import os
from google import genai
from google.genai import types


def save_binary_file(file_name, data):
    f = open(file_name, "wb")
    f.write(data)
    f.close()
    print(f"File saved to: {file_name}")


def generate():
    client = genai.Client(
        http_options={"api_version": "v1beta", "base_url": "${input.baseURL}"},
        api_key=os.environ.get("GEMINI_API_KEY"),
    )

    model = "${input.model}"
    contents = [
        types.Content(
            role="user",
            parts=[
${images}                types.Part.from_text(text="""${pyTriple(musicPrompt(input))}"""),
            ],
        ),
    ]
    generate_content_config = types.GenerateContentConfig(
        response_modalities=[
            "audio",
        ],
    )

    file_index = 0
    for chunk in client.models.generate_content_stream(
        model=model,
        contents=contents,
        config=generate_content_config,
    ):
        if (
            chunk.parts is None
        ):
            continue
        if chunk.parts[0].inline_data and chunk.parts[0].inline_data.data:
            file_name = f"ENTER_FILE_NAME_{file_index}"
            file_index += 1
            inline_data = chunk.parts[0].inline_data
            data_buffer = inline_data.data
            file_extension = mimetypes.guess_extension(inline_data.mime_type)
            save_binary_file(f"{file_name}{file_extension}", data_buffer)
        else:
            if text := chunk.text:
                print(text)

if __name__ == "__main__":
    generate()
`
}

// musicTypescript 按官网写法生成 @google/genai TypeScript 音乐示例，经本服务地址流式生成并保存音频
function musicTypescript(input: MusicCodeInput): string {
  const images = input.images
    .map(
      (image, index) =>
        `        {\n          inlineData: {\n            mimeType: '${image.mimeType}',\n            data: readFileSync('${imageFile(image, index)}').toString('base64'),\n          },\n        },\n`,
    )
    .join('')
  return `// To run this code you need to install the following dependencies:
// npm install @google/genai mime
// npm install -D @types/node

import {
  GoogleGenAI,
} from '@google/genai';
import mime from 'mime';
import { ${images ? 'readFileSync, ' : ''}writeFile } from 'fs';

function saveBinaryFile(fileName: string, content: Buffer) {
  writeFile(fileName, content, 'utf8', (err) => {
    if (err) {
      console.error(\`Error writing file \${fileName}:\`, err);
      return;
    }
    console.log(\`File \${fileName} saved to file system.\`);
  });
}

async function main() {
  const ai = new GoogleGenAI({
    apiKey: process.env['GEMINI_API_KEY'],
    httpOptions: { baseUrl: '${input.baseURL}' },
  });
  const config = {
    responseModalities: [
        'audio',
    ],
  };
  const model = '${input.model}';
  const contents = [
    {
      role: 'user',
      parts: [
${images}        {
          text: \`${tsTemplate(musicPrompt(input))}\`,
        },
      ],
    },
  ];

  const response = await ai.models.generateContentStream({
    model,
    config,
    contents,
  });
  let fileIndex = 0;
  for await (const chunk of response) {
    if (!chunk.candidates || !chunk.candidates[0].content || !chunk.candidates[0].content.parts) {
      continue;
    }
    if (chunk.candidates?.[0]?.content?.parts?.[0]?.inlineData) {
      const fileName = \`ENTER_FILE_NAME_\${fileIndex++}\`;
      const inlineData = chunk.candidates[0].content.parts[0].inlineData;
      const fileExtension = mime.getExtension(inlineData.mimeType || '');
      const buffer = Buffer.from(inlineData.data || '', 'base64');
      saveBinaryFile(\`\${fileName}.\${fileExtension}\`, buffer);
    }
    else {
      console.log(chunk.text);
    }
  }
}

main();
`
}

// musicCode 按语言生成音乐页当前提示词的示例代码
export function musicCode(language: MusicCodeLanguage, input: MusicCodeInput): string {
  return language === 'python' ? musicPython(input) : musicTypescript(input)
}

// RealtimeMusicCodeInput 是实时音乐示例代码所需的提示词、生成参数与服务地址
export interface RealtimeMusicCodeInput {
  baseURL: string
  model: string
  prompts: readonly { text: string; weight: number }[]
  config: Record<string, unknown>
}

// realtimeFrames 返回实时音乐会话开始后依次发送的提示词、参数与播放帧
function realtimeFrames(input: RealtimeMusicCodeInput): { prompts: string; config: string } {
  const prompts = input.prompts
    .filter((prompt) => prompt.text.trim() !== '')
    .map((prompt) => ({ text: prompt.text.trim(), weight: prompt.weight }))
  const config = Object.fromEntries(
    Object.entries(input.config).filter(([, value]) => value !== null && value !== ''),
  )
  return { prompts: JSON.stringify(prompts), config: JSON.stringify(config) }
}

// realtimePython 生成经本服务 /v1/live 连接实时音乐会话、保存 30 秒 WAV 的 Python 示例
function realtimePython(input: RealtimeMusicCodeInput): string {
  const frames = realtimeFrames(input)
  const url = input.baseURL.replace(/^http/, 'ws')
  return `# To run this code you need to install the following dependencies:
# pip install websockets

import asyncio
import base64
import json
import os
import wave

import websockets

MODEL = "${input.model}"
URL = "${url}/v1/live?key=" + os.environ.get("GEMINI_API_KEY", "")


async def main():
    async with websockets.connect(URL, max_size=None) as socket:
        await socket.send(json.dumps({
            "type": "setup",
            "model": MODEL,
            "input_modalities": ["text"],
            "output_modalities": ["audio"],
        }))
        while json.loads(await socket.recv())["type"] != "setup_complete":
            pass
        await socket.send(json.dumps({"type": "music_prompts", "weighted_prompts": ${frames.prompts}}))
        await socket.send(json.dumps({"type": "music_config", "music_config": ${frames.config}}))
        await socket.send(json.dumps({"type": "playback", "playback_control": "play"}))

        with wave.open("music.wav", "wb") as output:
            output.setnchannels(2)
            output.setsampwidth(2)
            output.setframerate(48000)
            seconds = 0.0
            async for raw in socket:
                message = json.loads(raw)
                if message["type"] == "media":
                    data = base64.b64decode(message["data"])
                    output.writeframes(data)
                    seconds += len(data) / (48000 * 2 * 2)
                    if seconds >= 30:
                        break
                elif message["type"] == "error":
                    print(message)
                    break
        await socket.send(json.dumps({"type": "close"}))
    print("Music has been saved to music.wav.")


asyncio.run(main())
`
}

// realtimeTypescript 生成经本服务 /v1/live 连接实时音乐会话、保存 30 秒 WAV 的 TypeScript 示例
function realtimeTypescript(input: RealtimeMusicCodeInput): string {
  const frames = realtimeFrames(input)
  const url = input.baseURL.replace(/^http/, 'ws')
  return `// To run this code you need to install the following dependencies:
// npm install ws
// npm install -D @types/ws @types/node

import { writeFileSync } from 'fs';
import WebSocket from 'ws';

const socket = new WebSocket(\`${url}/v1/live?key=\${process.env['GEMINI_API_KEY'] ?? ''}\`);
const chunks: Buffer[] = [];
let bytes = 0;

socket.on('open', () => {
  socket.send(JSON.stringify({
    type: 'setup',
    model: '${input.model}',
    input_modalities: ['text'],
    output_modalities: ['audio'],
  }));
});

socket.on('message', (raw) => {
  const message = JSON.parse(raw.toString());
  if (message.type === 'setup_complete') {
    socket.send(JSON.stringify({ type: 'music_prompts', weighted_prompts: ${frames.prompts} }));
    socket.send(JSON.stringify({ type: 'music_config', music_config: ${frames.config} }));
    socket.send(JSON.stringify({ type: 'playback', playback_control: 'play' }));
  } else if (message.type === 'media') {
    const data = Buffer.from(message.data, 'base64');
    chunks.push(data);
    bytes += data.length;
    if (bytes >= 48000 * 2 * 2 * 30) finish();
  } else if (message.type === 'error') {
    console.error(message);
    finish();
  }
});

function finish() {
  socket.send(JSON.stringify({ type: 'close' }));
  socket.close();
  const header = Buffer.alloc(44);
  header.write('RIFF', 0);
  header.writeUInt32LE(36 + bytes, 4);
  header.write('WAVE', 8);
  header.write('fmt ', 12);
  header.writeUInt32LE(16, 16);
  header.writeUInt16LE(1, 20);
  header.writeUInt16LE(2, 22);
  header.writeUInt32LE(48000, 24);
  header.writeUInt32LE(48000 * 4, 28);
  header.writeUInt16LE(4, 32);
  header.writeUInt16LE(16, 34);
  header.write('data', 36);
  header.writeUInt32LE(bytes, 40);
  writeFileSync('music.wav', Buffer.concat([header, ...chunks]));
  console.log('Music has been saved to music.wav.');
}
`
}

// realtimeMusicCode 按语言生成实时音乐示例代码
export function realtimeMusicCode(
  language: MusicCodeLanguage,
  input: RealtimeMusicCodeInput,
): string {
  return language === 'python' ? realtimePython(input) : realtimeTypescript(input)
}
