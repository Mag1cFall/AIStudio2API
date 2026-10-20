import { heredoc, pyTriple, tsTemplate, type MediaCodeLanguage } from './code'

// SpeechCodeInput 是语音页示例代码所需的台词、音色、设置与服务地址
export interface SpeechCodeInput {
  baseURL: string
  model: string
  prompt: string
  temperature: number
  speakers: readonly { alias: string; voice: string }[]
  openAI: { input: string; voice: string; instructions?: string | undefined }
}

// pythonVoice 生成 Python 的预置音色设置
function pythonVoice(name: string, indent: string): string {
  return [
    `${indent}voice_config=types.VoiceConfig(`,
    `${indent}    prebuilt_voice_config=types.PrebuiltVoiceConfig(`,
    `${indent}        voice_name="${name}"`,
    `${indent}    )`,
    `${indent})`,
  ].join('\n')
}

// pythonSpeechConfig 按官网写法生成 Python 的单人或多人语音设置
function pythonSpeechConfig(input: SpeechCodeInput): string {
  if (input.speakers.length === 1)
    return [
      '        speech_config=types.SpeechConfig(',
      pythonVoice(input.speakers[0]!.voice, '            '),
      '        ),',
    ].join('\n')
  return [
    '        speech_config=types.SpeechConfig(',
    '            multi_speaker_voice_config=types.MultiSpeakerVoiceConfig(',
    '                speaker_voice_configs=[',
    ...input.speakers.flatMap((speaker) => [
      '                    types.SpeakerVoiceConfig(',
      `                        speaker="${speaker.alias}",`,
      `${pythonVoice(speaker.voice, '                        ')},`,
      '                    ),',
    ]),
    '                ]',
    '            ),',
    '        ),',
  ].join('\n')
}

// speechPython 按官网写法生成 google-genai Python 语音示例，经本服务地址流式生成并保存音频
function speechPython(input: SpeechCodeInput): string {
  return `# To run this code you need to install the following dependencies:
# pip install google-genai

import mimetypes
import os
import struct
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
                types.Part.from_text(text="""${pyTriple(input.prompt)}"""),
            ],
        ),
    ]
    generate_content_config = types.GenerateContentConfig(
        temperature=${input.temperature},
        response_modalities=[
            "audio",
        ],
${pythonSpeechConfig(input)}
    )

    audio_data = bytearray()
    mime_type = ""
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
            inline_data = chunk.parts[0].inline_data
            audio_data.extend(inline_data.data)
            mime_type = inline_data.mime_type
        else:
            if text := chunk.text:
                print(text)
    if audio_data:
        file_extension = mimetypes.guess_extension(mime_type)
        data_buffer = bytes(audio_data)
        if file_extension is None:
            file_extension = ".wav"
            data_buffer = convert_to_wav(bytes(audio_data), mime_type)
        save_binary_file(f"ENTER_FILE_NAME{file_extension}", data_buffer)

def convert_to_wav(audio_data: bytes, mime_type: str) -> bytes:
    """Generates a WAV file header for the given audio data and parameters."""
    parameters = parse_audio_mime_type(mime_type)
    bits_per_sample = parameters["bits_per_sample"]
    sample_rate = parameters["rate"]
    num_channels = 1
    data_size = len(audio_data)
    bytes_per_sample = bits_per_sample // 8
    block_align = num_channels * bytes_per_sample
    byte_rate = sample_rate * block_align
    chunk_size = 36 + data_size

    header = struct.pack(
        "<4sI4s4sIHHIIHH4sI",
        b"RIFF",
        chunk_size,
        b"WAVE",
        b"fmt ",
        16,
        1,
        num_channels,
        sample_rate,
        byte_rate,
        block_align,
        bits_per_sample,
        b"data",
        data_size
    )
    return header + audio_data

def parse_audio_mime_type(mime_type: str) -> dict[str, int | None]:
    """Parses bits per sample and rate from an audio MIME type string."""
    bits_per_sample = 16
    rate = 24000

    parts = mime_type.split(";")
    for param in parts:
        param = param.strip()
        if param.lower().startswith("rate="):
            try:
                rate = int(param.split("=", 1)[1])
            except (ValueError, IndexError):
                pass
        elif param.startswith("audio/L"):
            try:
                bits_per_sample = int(param.split("L", 1)[1])
            except (ValueError, IndexError):
                pass

    return {"bits_per_sample": bits_per_sample, "rate": rate}


if __name__ == "__main__":
    generate()
`
}

// typescriptVoice 生成 TypeScript 的预置音色设置
function typescriptVoice(name: string, indent: string): string {
  return [
    `${indent}voiceConfig: {`,
    `${indent}  prebuiltVoiceConfig: {`,
    `${indent}    voiceName: '${name}',`,
    `${indent}  }`,
    `${indent}}`,
  ].join('\n')
}

// typescriptSpeechConfig 按官网写法生成 TypeScript 的单人或多人语音设置
function typescriptSpeechConfig(input: SpeechCodeInput): string {
  if (input.speakers.length === 1)
    return [
      '    speechConfig: {',
      typescriptVoice(input.speakers[0]!.voice, '      '),
      '    },',
    ].join('\n')
  return [
    '    speechConfig: {',
    '      multiSpeakerVoiceConfig: {',
    '        speakerVoiceConfigs: [',
    ...input.speakers.flatMap((speaker) => [
      '          {',
      `            speaker: '${speaker.alias}',`,
      typescriptVoice(speaker.voice, '            '),
      '          },',
    ]),
    '        ]',
    '      },',
    '    },',
  ].join('\n')
}

// speechTypescript 按官网写法生成 @google/genai TypeScript 语音示例，经本服务地址流式生成并保存音频
function speechTypescript(input: SpeechCodeInput): string {
  return `// To run this code you need to install the following dependencies:
// npm install @google/genai mime
// npm install -D @types/node

import {
  GoogleGenAI,
} from '@google/genai';
import mime from 'mime';
import { writeFile } from 'fs';

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
    temperature: ${input.temperature},
    responseModalities: [
        'audio',
    ],
${typescriptSpeechConfig(input)}
  };
  const model = '${input.model}';
  const contents = [
    {
      role: 'user',
      parts: [
        {
          text: \`${tsTemplate(input.prompt)}\`,
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
      let fileExtension = mime.getExtension(inlineData.mimeType || '');
      let buffer = Buffer.from(inlineData.data || '', 'base64');
      if (!fileExtension) {
        fileExtension = 'wav';
        buffer = convertToWav(inlineData.data || '', inlineData.mimeType || '');
      }
      saveBinaryFile(\`\${fileName}.\${fileExtension}\`, buffer);
    }
    else {
      console.log(chunk.text);
    }
  }
}

main();

interface WavConversionOptions {
  numChannels : number,
  sampleRate: number,
  bitsPerSample: number
}

function convertToWav(rawData: string, mimeType: string) {
  const options = parseMimeType(mimeType)
  const buffer = Buffer.from(rawData, 'base64');
  const wavHeader = createWavHeader(buffer.length, options);

  return Buffer.concat([wavHeader, buffer]);
}

function parseMimeType(mimeType : string) {
  const [fileType, ...params] = mimeType.split(';').map(s => s.trim());
  const [_, format] = fileType.split('/');

  const options : Partial<WavConversionOptions> = {
    numChannels: 1,
  };

  if (format && format.startsWith('L')) {
    const bits = parseInt(format.slice(1), 10);
    if (!isNaN(bits)) {
      options.bitsPerSample = bits;
    }
  }

  for (const param of params) {
    const [key, value] = param.split('=').map(s => s.trim());
    if (key === 'rate') {
      options.sampleRate = parseInt(value, 10);
    }
  }

  return options as WavConversionOptions;
}

function createWavHeader(dataLength: number, options: WavConversionOptions) {
  const {
    numChannels,
    sampleRate,
    bitsPerSample,
  } = options;

  const byteRate = sampleRate * numChannels * bitsPerSample / 8;
  const blockAlign = numChannels * bitsPerSample / 8;
  const buffer = Buffer.alloc(44);

  buffer.write('RIFF', 0);
  buffer.writeUInt32LE(36 + dataLength, 4);
  buffer.write('WAVE', 8);
  buffer.write('fmt ', 12);
  buffer.writeUInt32LE(16, 16);
  buffer.writeUInt16LE(1, 20);
  buffer.writeUInt16LE(numChannels, 22);
  buffer.writeUInt32LE(sampleRate, 24);
  buffer.writeUInt32LE(byteRate, 28);
  buffer.writeUInt16LE(blockAlign, 32);
  buffer.writeUInt16LE(bitsPerSample, 34);
  buffer.write('data', 36);
  buffer.writeUInt32LE(dataLength, 40);

  return buffer;
}
`
}

// speechRest 按官网写法生成语音 REST 示例，请求发往本服务地址
function speechRest(input: SpeechCodeInput): string {
  const voice = (name: string) => ({ prebuilt_voice_config: { voice_name: name } })
  const speechConfig =
    input.speakers.length === 1
      ? { voice_config: voice(input.speakers[0]!.voice) }
      : {
          multi_speaker_voice_config: {
            speaker_voice_configs: input.speakers.map((speaker) => ({
              speaker: speaker.alias,
              voice_config: voice(speaker.voice),
            })),
          },
        }
  const body = JSON.stringify(
    {
      contents: [{ role: 'user', parts: [{ text: input.prompt }] }],
      generationConfig: {
        responseModalities: ['audio'],
        temperature: input.temperature,
        speech_config: speechConfig,
      },
    },
    null,
    2,
  )
  return `#!/bin/bash
set -e -E

GEMINI_API_KEY="$GEMINI_API_KEY"
BASE_URL="${input.baseURL}"
MODEL_ID="${input.model}"
GENERATE_CONTENT_API="streamGenerateContent"

cat << EOF > request.json
${heredoc(body)}
EOF

curl \\
-X POST \\
-H "Content-Type: application/json" \\
"\${BASE_URL}/v1beta/models/\${MODEL_ID}:\${GENERATE_CONTENT_API}?key=\${GEMINI_API_KEY}" -d '@request.json'
`
}

// speechOpenAIPython 生成 OpenAI Python SDK 语音示例，台词合为 input，风格为 instructions
function speechOpenAIPython(input: SpeechCodeInput): string {
  const { openAI } = input
  const instructions = openAI.instructions
    ? `\n    instructions="""${pyTriple(openAI.instructions)}""",`
    : ''
  return `import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${input.baseURL}/v1",
)

with client.audio.speech.with_streaming_response.create(
    model="${input.model}",
    voice="${openAI.voice}",
    input="""${pyTriple(openAI.input)}""",${instructions}
    response_format="wav",
) as response:
    response.stream_to_file("speech.wav")
print("Audio has been saved to speech.wav.")
`
}

// speechOpenAITypescript 生成 OpenAI TypeScript SDK 语音示例，台词合为 input，风格为 instructions
function speechOpenAITypescript(input: SpeechCodeInput): string {
  const { openAI } = input
  const instructions = openAI.instructions
    ? `\n  instructions: \`${tsTemplate(openAI.instructions)}\`,`
    : ''
  return `import OpenAI from 'openai';
import { writeFile } from 'fs/promises';

const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: '${input.baseURL}/v1',
});

const response = await client.audio.speech.create({
  model: '${input.model}',
  voice: '${openAI.voice}',
  input: \`${tsTemplate(openAI.input)}\`,${instructions}
  response_format: 'wav',
});

await writeFile('speech.wav', Buffer.from(await response.arrayBuffer()));
console.log('Audio has been saved to speech.wav.');
`
}

// speechCode 按语言生成语音页当前台词与设置的示例代码
export function speechCode(language: MediaCodeLanguage, input: SpeechCodeInput): string {
  switch (language) {
    case 'python':
      return speechPython(input)
    case 'typescript':
      return speechTypescript(input)
    case 'rest':
      return speechRest(input)
    case 'openai-python':
      return speechOpenAIPython(input)
    case 'openai-typescript':
      return speechOpenAITypescript(input)
  }
}
