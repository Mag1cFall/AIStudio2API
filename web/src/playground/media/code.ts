import type { CodeOption } from '../code'
import type { VideoSettings } from './video'

// MediaCodeLanguage 是媒体页获取代码的语言选项
export type MediaCodeLanguage =
  | 'python'
  | 'typescript'
  | 'rest'
  | 'openai-python'
  | 'openai-typescript'

// mediaCodeOptions 是媒体页获取代码的语言，Google SDK 在前，OpenAI SDK 在后
export const mediaCodeOptions: CodeOption<MediaCodeLanguage>[] = [
  { value: 'python', label: 'Python', highlight: 'python', file: 'main.py' },
  { value: 'typescript', label: 'TypeScript', highlight: 'typescript', file: 'main.ts' },
  { value: 'rest', label: 'REST', highlight: 'bash', file: 'request.sh' },
  { value: 'openai-python', label: 'OpenAI Python', highlight: 'python', file: 'main.py' },
  {
    value: 'openai-typescript',
    label: 'OpenAI TypeScript',
    highlight: 'typescript',
    file: 'main.ts',
  },
]

// pyTriple 把文本写为 Python 三引号字符串的内容
export function pyTriple(value: string): string {
  return value.replaceAll('\\', '\\\\').replaceAll('"""', '\\"\\"\\"')
}

// tsTemplate 把文本写为 TypeScript 模板字符串的内容
export function tsTemplate(value: string): string {
  return value.replaceAll('\\', '\\\\').replaceAll('`', '\\`').replaceAll('${', '\\${')
}

// heredoc 把 JSON 文本写为不加引号的 heredoc 内容，保留 JSON 转义
export function heredoc(value: string): string {
  return value.replaceAll('\\', '\\\\').replaceAll('$', '\\$').replaceAll('`', '\\`')
}

// supported 把取值列表写为官网示例的注释
function supported(values: readonly (string | number)[]): string {
  if (values.length === 0) return ''
  return ` # supported values: ${values.map((value) => (typeof value === 'string' ? `"${value}"` : value)).join(' or ')}`
}

// extension 返回图片 MIME 对应的文件扩展名
export function extension(mimeType: string): string {
  return mimeType.split('/')[1]!.replace('jpeg', 'jpg')
}

// VideoCodeInput 是视频页示例代码所需的输入、设置与服务地址
interface VideoCodeInput {
  baseURL: string
  prompt: string
  settings: VideoSettings
  aspectRatios: readonly string[]
  durations: readonly number[]
  resolutions: readonly string[]
}

// videoPrompt 返回示例代码中的提示词，空输入为官网占位
function videoPrompt(input: VideoCodeInput): string {
  return input.prompt.trim() === '' ? 'INSERT_INPUT_HERE' : input.prompt
}

// videoPython 按官网写法生成 google-genai Python 视频示例，结果经视频地址加 key 下载
function videoPython(input: VideoCodeInput): string {
  const { settings } = input
  const config = [
    '    person_generation="dont_allow", # supported values: "dont_allow" or "allow_adult" or "allow_all"',
    `    aspect_ratio="${settings.aspectRatio}",${supported(input.aspectRatios)}`,
    '    number_of_videos=1,',
    `    duration_seconds=${settings.durationSeconds},${supported(input.durations)}`,
    `    resolution="${settings.resolution}",${supported(input.resolutions)}`,
  ]
  return `"""
To run this code you need to install the following dependencies:
pip install google-genai
"""
import os
import time
import urllib.request

from google import genai
from google.genai import types

MODEL = "${settings.model}"
API_KEY = os.environ.get("GEMINI_API_KEY")

client = genai.Client(
    http_options={"api_version": "v1beta", "base_url": "${input.baseURL}"},
    api_key=API_KEY,
)

video_config = types.GenerateVideosConfig(
${config.join('\n')}
)

def generate():
    operation = client.models.generate_videos(
        model=MODEL,
        source=types.GenerateVideosSource(
            prompt="""${pyTriple(videoPrompt(input))}""",
        ),
        config=video_config,
    )

    # Waiting for the video(s) to be generated
    while not operation.done:
        print("Video has not been generated yet. Check again in 10 seconds...")
        time.sleep(10)
        operation = client.operations.get(operation)

    result = operation.result
    if not result:
        print("Error occurred while generating video.")
        return

    generated_videos = result.generated_videos
    if not generated_videos:
        print("No videos were generated.")
        return

    print(f"Generated {len(generated_videos)} video(s).")
    for n, generated_video in enumerate(generated_videos):
        assert generated_video.video is not None
        print(f"Video has been generated: {generated_video.video.uri}")
        urllib.request.urlretrieve(f"{generated_video.video.uri}&key={API_KEY}", f"video_{n}.mp4")
        print(f"Video {generated_video.video.uri} has been downloaded to video_{n}.mp4.")

if __name__ == "__main__":
    generate()
`
}

// videoTypescript 按官网写法生成 @google/genai TypeScript 视频示例
function videoTypescript(input: VideoCodeInput): string {
  const { settings } = input
  const config = [
    '        numberOfVideos: 1,',
    `        aspectRatio: '${settings.aspectRatio}',`,
    `        resolution: '${settings.resolution}',`,
    "        personGeneration: 'dont_allow',",
    `        durationSeconds: ${settings.durationSeconds},`,
  ]
  return `// To run this code you need to install the following dependencies:
// npm install @google/genai
// npm install -D @types/node

import {
  GoogleGenAI,
} from '@google/genai';

import {writeFile} from 'fs/promises';

async function main() {
  const ai = new GoogleGenAI({
    apiKey: process.env['GEMINI_API_KEY'],
    httpOptions: { baseUrl: '${input.baseURL}' },
  });

  let operation = await ai.models.generateVideos({
    model: '${settings.model}',
    source: {
      prompt: \`${tsTemplate(videoPrompt(input))}\`,
    },
    config: {
${config.join('\n')}
    },
  });

  while (!operation.done) {
    console.log(\`Video \${operation.name} has not been generated yet. Check again in 10 seconds...\`);
    await new Promise((resolve) => { setTimeout(resolve, 10000); });
    operation = await ai.operations.getVideosOperation({
      operation: operation,
    });
  }

  console.log(\`Generated \${operation.response?.generatedVideos?.length ?? 0} video(s).\`);

  operation.response?.generatedVideos?.forEach(async (generatedVideo, i) => {
    console.log(\`Video has been generated: \${generatedVideo?.video?.uri}\`);
    const response = await fetch(\`\${generatedVideo?.video?.uri}&key=\${process.env['GEMINI_API_KEY']}\`);
    const buffer = await response.arrayBuffer();
    await writeFile(\`video_\${i}.mp4\`, Buffer.from(buffer));
    console.log(\`Video \${generatedVideo?.video?.uri} has been downloaded to video_\${i}.mp4.\`);
  });
}

main();
`
}

// videoRest 按官网写法生成 predictLongRunning 的 bash 示例
function videoRest(input: VideoCodeInput): string {
  const { settings } = input
  const body = {
    instances: [{ prompt: videoPrompt(input) }],
    parameters: {
      personGeneration: 'dont_allow',
      aspectRatio: settings.aspectRatio,
      sampleCount: 1,
      durationSeconds: settings.durationSeconds,
      resolution: settings.resolution,
    },
  }
  const json = heredoc(JSON.stringify(body, null, 4))
  return `#!/bin/bash
set -e -E

# To run this script, install jq for JSON processing

GEMINI_API_KEY="$GEMINI_API_KEY"
BASE_URL="${input.baseURL}"
MODEL_ID="${settings.model}"

cat << EOF > request.json
${json}
EOF

curl \\
-s \\
-X POST \\
-H "Content-Type: application/json" \\
"\${BASE_URL}/v1beta/models/\${MODEL_ID}:predictLongRunning?key=\${GEMINI_API_KEY}" \\
-d '@request.json' | tee result.json | jq .name | sed 's/"//g' > op_name

# Obtain operation name to download video.
op_name=$(cat op_name)

# Check against status of operation.
while true; do
  is_done=$(curl -s "\${BASE_URL}/v1beta/\${op_name}?key=\${GEMINI_API_KEY}" | tee op_check.json | jq .done)

  if [[ "\${is_done}" = "true" ]]; then
    for (( i=0; i<1; i++ ));
    do
        video_url=$(cat op_check.json | jq ".response.generateVideoResponse.generatedSamples[\${i}].video.uri" | tr -d '"')
        echo "Video has been generated: \${video_url}."
        curl -L -s "\${video_url}&key=\${GEMINI_API_KEY}" --output "video_\${i}.mp4"
        echo "Video \${video_url} has been downloaded to video_\${i}.mp4."
    done;
    break
  fi

  echo "Video \${op_name} has not been generated yet. Check again in 10 seconds..."

  sleep 10

done
`
}

// openAIVideoSize 把宽高比与分辨率换为 OpenAI 视频尺寸
function openAIVideoSize({ aspectRatio, resolution }: VideoSettings): string {
  const sizes: Record<string, [number, number]> = {
    '720p': [1280, 720],
    '1080p': [1920, 1080],
    '4k': [3840, 2160],
  }
  const [long, short] = sizes[resolution] ?? [1280, 720]
  return aspectRatio === '9:16' ? `${short}x${long}` : `${long}x${short}`
}

// videoOpenAIPython 生成 OpenAI Python SDK 视频示例
function videoOpenAIPython(input: VideoCodeInput): string {
  return `import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${input.baseURL}/v1",
)

video = client.videos.create_and_poll(
    model="${input.settings.model}",
    prompt="""${pyTriple(videoPrompt(input))}""",
    seconds="${input.settings.durationSeconds}",
    size="${openAIVideoSize(input.settings)}",
)

if video.status == "failed":
    print(video.error)
else:
    client.videos.download_content(video.id).write_to_file("video.mp4")
    print("Video has been downloaded to video.mp4.")
`
}

// videoOpenAITypescript 生成 OpenAI TypeScript SDK 视频示例
function videoOpenAITypescript(input: VideoCodeInput): string {
  return `import OpenAI from 'openai';
import { writeFile } from 'fs/promises';

const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: '${input.baseURL}/v1',
});

const video = await client.videos.createAndPoll({
  model: '${input.settings.model}',
  prompt: \`${tsTemplate(videoPrompt(input))}\`,
  seconds: '${input.settings.durationSeconds}',
  size: '${openAIVideoSize(input.settings)}',
});

if (video.status === 'failed') {
  console.error(video.error);
} else {
  const content = await client.videos.downloadContent(video.id);
  await writeFile('video.mp4', Buffer.from(await content.arrayBuffer()));
  console.log('Video has been downloaded to video.mp4.');
}
`
}

// videoCode 按语言生成视频页的示例代码
export function videoCode(language: MediaCodeLanguage, input: VideoCodeInput): string {
  switch (language) {
    case 'python':
      return videoPython(input)
    case 'typescript':
      return videoTypescript(input)
    case 'rest':
      return videoRest(input)
    case 'openai-python':
      return videoOpenAIPython(input)
    case 'openai-typescript':
      return videoOpenAITypescript(input)
  }
}
