// SnippetInput 是接入页自写示例所需的服务地址与模型 ID
export interface SnippetInput {
  origin: string
  modelID: string
}

// anthropicPython 生成 Anthropic Python SDK 的 Messages 示例
export function anthropicPython({ origin, modelID }: SnippetInput): string {
  return `import os

from anthropic import Anthropic

client = Anthropic(
    api_key=os.environ.get("ANTHROPIC_API_KEY"),
    base_url="${origin}",
)

message = client.messages.create(
    model="${modelID}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Explain how AI works in a few words"}],
)
print(message.content[0].text)
`
}

// anthropicTypescript 生成 Anthropic TypeScript SDK 的 Messages 示例
export function anthropicTypescript({ origin, modelID }: SnippetInput): string {
  return `import Anthropic from '@anthropic-ai/sdk';

const client = new Anthropic({
  apiKey: process.env.ANTHROPIC_API_KEY,
  baseURL: '${origin}',
});

const message = await client.messages.create({
  model: '${modelID}',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'Explain how AI works in a few words' }],
});
console.log(message.content[0].type === 'text' ? message.content[0].text : '');
`
}

// transcriptionCode 生成 OpenAI Audio 转写 audio.mp3 的示例
export function transcriptionCode(language: string, { origin, modelID }: SnippetInput): string {
  switch (language) {
    case 'openai-python':
      return `import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${origin}/v1",
)

with open("audio.mp3", "rb") as file:
    transcript = client.audio.transcriptions.create(model="${modelID}", file=file)
print(transcript.text)
`
    case 'openai-typescript':
      return `import fs from 'node:fs';
import OpenAI from 'openai';

const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: '${origin}/v1',
});

const transcript = await client.audio.transcriptions.create({
  model: '${modelID}',
  file: fs.createReadStream('audio.mp3'),
});
console.log(transcript.text);
`
    default:
      return `curl "${origin}/v1/audio/transcriptions" \\
  -H "Authorization: Bearer $OPENAI_API_KEY" \\
  -F "model=${modelID}" \\
  -F "file=@audio.mp3"
`
  }
}

// embeddingCode 生成 OpenAI Embeddings 示例
export function embeddingCode(language: string, { origin, modelID }: SnippetInput): string {
  switch (language) {
    case 'openai-python':
      return `import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${origin}/v1",
)

result = client.embeddings.create(model="${modelID}", input=["What is the meaning of life?"])
print(len(result.data[0].embedding))
`
    case 'openai-typescript':
      return `import OpenAI from 'openai';

const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: '${origin}/v1',
});

const result = await client.embeddings.create({
  model: '${modelID}',
  input: ['What is the meaning of life?'],
});
console.log(result.data[0].embedding.length);
`
    default:
      return `curl "${origin}/v1/embeddings" \\
  -H "Authorization: Bearer $OPENAI_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${modelID}", "input": ["What is the meaning of life?"]}'
`
  }
}

// filesCode 生成上传 document.pdf 后在提问中引用该文件的示例：Gemini SDK 经 Files API，OpenAI SDK 经 Files 与 Responses
export function filesCode(language: string, { origin, modelID }: SnippetInput): string {
  switch (language) {
    case 'python':
      return `import os

from google import genai
from google.genai import types

client = genai.Client(
    api_key=os.environ.get("GEMINI_API_KEY"),
    http_options=types.HttpOptions(base_url="${origin}"),
)

file = client.files.upload(file="document.pdf")
response = client.models.generate_content(
    model="${modelID}",
    contents=[file, "Summarize this document"],
)
print(response.text)
`
    case 'openai-python':
      return `import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${origin}/v1",
)

with open("document.pdf", "rb") as handle:
    file = client.files.create(file=handle, purpose="user_data")
response = client.responses.create(
    model="${modelID}",
    input=[
        {
            "role": "user",
            "content": [
                {"type": "input_file", "file_id": file.id},
                {"type": "input_text", "text": "Summarize this document"},
            ],
        }
    ],
)
print(response.output_text)
`
    default:
      return `curl "${origin}/v1/files" \\
  -H "Authorization: Bearer $OPENAI_API_KEY" \\
  -F "purpose=user_data" \\
  -F "file=@document.pdf"
`
  }
}
