import type { Model } from '@/types'
import {
  chatRequest,
  protocolCall,
  responsesRequest,
  type ImageOptions,
  type Protocol,
} from './protocols'
import type { GenerateRequest } from './request'

// CodeLanguage 是获取代码对话框的语言选项
export type CodeLanguage = 'rest' | 'python' | 'typescript' | 'openai-python' | 'openai-typescript'

// CodeInput 是生成示例代码所需的页面请求：协议、是否流式、模型、服务地址、Gemini API 请求与 OpenAI Images 参数
export interface CodeInput {
  model: Model | undefined
  modelID: string
  baseURL: string
  request: GenerateRequest
  protocol: Protocol
  stream: boolean
  images: ImageOptions
}

// apiKeyMarker 是代码中 API key 的占位，cURL 中换为对应协议的环境变量
const apiKeyMarker = '__API_KEY__'

// keyVariables 是各协议示例代码读取 API key 的环境变量
const keyVariables: Record<Protocol, string> = {
  gemini: 'GEMINI_API_KEY',
  'openai-chat': 'OPENAI_API_KEY',
  'openai-responses': 'OPENAI_API_KEY',
  'openai-images': 'OPENAI_API_KEY',
  anthropic: 'ANTHROPIC_API_KEY',
}

// Attachment 是请求中的一个内联附件及其在示例代码中的本地文件名
interface Attachment {
  file: string
  mimeType: string
  data: string
}

// attachmentsOf 返回请求中的内联附件，同一份数据只列一次
function attachmentsOf(request: GenerateRequest): Attachment[] {
  const result: Attachment[] = []
  for (const content of request.contents)
    for (const part of content.parts) {
      const inline = part.inlineData
      if (!inline || result.some((item) => item.data === inline.data)) continue
      const extension =
        inline.mimeType.split('/')[1]?.split(';')[0]?.replace('jpeg', 'jpg') ?? 'bin'
      result.push({
        file: `attachment_${result.length + 1}.${extension}`,
        mimeType: inline.mimeType,
        data: inline.data,
      })
    }
  return result
}

// formFiles 返回 OpenAI Images 编辑表单中各参考图片对应的附件文件名，顺序与表单中的文件项相同
function formFiles(request: GenerateRequest, attachments: Attachment[]): string[] {
  const parts =
    [...request.contents].reverse().find((content) => content.role === 'user')?.parts ?? []
  return parts.flatMap((part) => {
    const inline = part.inlineData
    if (!inline?.mimeType.startsWith('image/')) return []
    return attachments.filter((item) => item.data === inline.data).map((item) => item.file)
  })
}

// Marker 是请求正文中被替换为附件引用的字符串：raw 为 Base64 正文，url 为 data URL
interface Marker {
  index: number
  kind: 'raw' | 'url'
}

// markerText 返回附件引用在正文中的占位文本
function markerText(marker: Marker): string {
  return `__FILE_${marker.kind}_${marker.index}__`
}

// markerPattern 匹配序列化后正文中带引号的附件占位
const markerPattern = /(["'])__FILE_(raw|url)_(\d+)__\1/g

// markAttachments 把正文中附件的 Base64 正文与 data URL 换为占位
function markAttachments(value: unknown, attachments: readonly Attachment[]): unknown {
  if (typeof value === 'string') {
    const index = attachments.findIndex(
      (item) => value === item.data || value === `data:${item.mimeType};base64,${item.data}`,
    )
    if (index === -1) return value
    return markerText({ index, kind: value.startsWith('data:') ? 'url' : 'raw' })
  }
  if (Array.isArray(value)) return value.map((item) => markAttachments(item, attachments))
  if (value !== null && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, markAttachments(item, attachments)]),
    )
  return value
}

// opaqueKeys 是 Python 示例中保持原样的键：其值为用户填写的 Schema 或函数参数与结果
const opaqueKeys = new Set([
  'responseSchema',
  'responseJsonSchema',
  'parameters',
  'args',
  'response',
])

// snakeKeys 把 Gemini API 的字段名换为 google-genai Python SDK 的 snake_case 写法，用户内容保持原样
function snakeKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(snakeKeys)
  if (value === null || typeof value !== 'object') return value
  return Object.fromEntries(
    Object.entries(value).map(([key, item]) => [
      key.replace(/[A-Z]/g, (char) => `_${char.toLowerCase()}`),
      opaqueKeys.has(key) ? item : snakeKeys(item),
    ]),
  )
}

// pyString 把字符串写为单引号 Python 字面量
function pyString(value: string): string {
  return `'${JSON.stringify(value).slice(1, -1).replaceAll('\\"', '"').replaceAll("'", "\\'")}'`
}

// pyLiteral 把 JSON 值写为 Python 字面量
function pyLiteral(value: unknown, indent = 0): string {
  const pad = '    '.repeat(indent)
  const inner = '    '.repeat(indent + 1)
  if (value === null || value === undefined) return 'None'
  if (value === true) return 'True'
  if (value === false) return 'False'
  if (typeof value === 'string') return pyString(value)
  if (typeof value === 'number') return String(value)
  if (Array.isArray(value)) {
    if (value.length === 0) return '[]'
    return `[\n${value.map((item) => `${inner}${pyLiteral(item, indent + 1)},`).join('\n')}\n${pad}]`
  }
  const entries = Object.entries(value)
  if (entries.length === 0) return '{}'
  return `{\n${entries.map(([key, item]) => `${inner}${pyString(key)}: ${pyLiteral(item, indent + 1)},`).join('\n')}\n${pad}}`
}

// pySignatures 把 Python 字面量中的思考签名换为解码后的字节，SDK 中该字段为 bytes
function pySignatures(literal: string): string {
  return literal.replace(
    /('thought_signature': )('[^']*')/g,
    (_match, key: string, value: string) => `${key}base64.b64decode(${value})`,
  )
}

// pyAttachments 返回读取附件的 Python 语句与替换附件占位后的字面量，Gemini SDK 的内联数据为字节
function pyAttachments(
  literal: string,
  attachments: readonly Attachment[],
  bytes: boolean,
): { setup: string; literal: string } {
  const used = new Set<number>()
  const replaced = literal.replace(markerPattern, (_match, _quote, kind: string, index: string) => {
    used.add(Number(index))
    const name = `file_${Number(index) + 1}`
    if (kind === 'url')
      return `f"data:${attachments[Number(index)]?.mimeType};base64,{base64.b64encode(${name}).decode()}"`
    return bytes ? name : `base64.b64encode(${name}).decode()`
  })
  const setup = [...used]
    .sort((first, second) => first - second)
    .map(
      (index) =>
        `with open(${pyString(attachments[index]?.file ?? '')}, 'rb') as file:\n    file_${index + 1} = file.read()`,
    )
    .join('\n')
  return { setup, literal: replaced }
}

// tsAttachments 返回读取附件的 TypeScript 语句与替换附件占位后的字面量
function tsAttachments(
  literal: string,
  attachments: readonly Attachment[],
): { setup: string; literal: string } {
  const used = new Set<number>()
  const replaced = literal.replace(markerPattern, (_match, _quote, kind: string, index: string) => {
    used.add(Number(index))
    const name = `file${Number(index) + 1}`
    return kind === 'url'
      ? `\`data:${attachments[Number(index)]?.mimeType};base64,\${${name}}\``
      : name
  })
  const setup = [...used]
    .sort((first, second) => first - second)
    .map(
      (index) =>
        `const file${index + 1} = fs.readFileSync(${JSON.stringify(attachments[index]?.file ?? '')}).toString('base64');`,
    )
    .join('\n')
  return { setup, literal: replaced }
}

// tsLiteral 把 JSON 值写为缩进的 TypeScript 字面量
function tsLiteral(value: unknown, indent = 0): string {
  return JSON.stringify(value, null, 2).replaceAll('\n', `\n${'  '.repeat(indent)}`)
}

// geminiConfig 把 Gemini API 请求拆为 contents 与 SDK 的 config：生成参数并入系统指令、安全设置与工具
function geminiConfig(request: GenerateRequest): {
  contents: unknown
  config: Record<string, unknown>
} {
  const config: Record<string, unknown> = { ...request.generationConfig }
  if (request.systemInstruction) config.systemInstruction = request.systemInstruction
  if (request.safetySettings) config.safetySettings = request.safetySettings
  if (request.tools) config.tools = request.tools
  return { contents: request.contents, config }
}

// pythonCode 生成 google-genai Python 的 generateContent 示例，正文与页面以 Gemini API 发送的请求相同
function pythonCode({ modelID, baseURL, request, stream }: CodeInput): string {
  const attachments = attachmentsOf(request)
  const { contents, config } = geminiConfig(request)
  const marked = snakeKeys(markAttachments({ contents, config }, attachments)) as {
    contents: unknown
    config: unknown
  }
  const files = pyAttachments(
    pySignatures(
      `contents = ${pyLiteral(marked.contents)}\n\nconfig = ${pyLiteral(marked.config)}`,
    ),
    attachments,
    true,
  )
  const base64 = files.literal.includes('base64.')
  const call = stream ? 'generate_content_stream' : 'generate_content'
  const output = stream
    ? `for chunk in response:\n    if chunk.text:\n        print(chunk.text, end="")\nprint()`
    : 'print(response.text)'
  return `${base64 ? 'import base64\n' : ''}import os

from google import genai
from google.genai import types

client = genai.Client(
    api_key=os.environ.get("GEMINI_API_KEY"),
    http_options=types.HttpOptions(base_url="${baseURL}"),
)
${files.setup ? `\n${files.setup}\n` : ''}
${files.literal}

response = client.models.${call}(
    model=${pyString(modelID)},
    contents=contents,
    config=config,
)
${output}
`
}

// typescriptCode 生成 @google/genai TypeScript 的 generateContent 示例，正文与页面以 Gemini API 发送的请求相同
function typescriptCode({ modelID, baseURL, request, stream }: CodeInput): string {
  const attachments = attachmentsOf(request)
  const { contents, config } = geminiConfig(request)
  const marked = markAttachments({ contents, config }, attachments) as {
    contents: unknown
    config: unknown
  }
  const files = tsAttachments(
    `const contents = ${tsLiteral(marked.contents)};\n\nconst config = ${tsLiteral(marked.config)};`,
    attachments,
  )
  const call = stream ? 'generateContentStream' : 'generateContent'
  const output = stream
    ? `for await (const chunk of response) {\n  process.stdout.write(chunk.text ?? '');\n}\nconsole.log();`
    : 'console.log(response.text);'
  return `${files.setup ? "import fs from 'node:fs';\n" : ''}import { GoogleGenAI } from '@google/genai';

const ai = new GoogleGenAI({
  apiKey: process.env.GEMINI_API_KEY,
  httpOptions: { baseUrl: '${baseURL}' },
});
${files.setup ? `\n${files.setup}\n` : ''}
${files.literal}

const response = await ai.models.${call}({
  model: ${JSON.stringify(modelID)},
  contents,
  config,
});
${output}
`
}

// shellJSON 把正文写为 here-document 中的 JSON：转义 shell 展开字符，附件占位换为读取本地文件的 Base64 命令
function shellJSON(body: unknown, attachments: readonly Attachment[]): string {
  return JSON.stringify(body, null, 2)
    .replace(/[\\$`]/g, '\\$&')
    .replace(markerPattern, (_match, _quote, kind: string, index: string) => {
      const attachment = attachments[Number(index)]
      const data = `$(base64 < ${attachment?.file} | tr -d '\\n')`
      return kind === 'url' ? `"data:${attachment?.mimeType};base64,${data}"` : `"${data}"`
    })
}

// restCode 生成与页面实际请求相同的 cURL 命令：所选协议的路径、请求头与正文，OpenAI Images 编辑为 multipart 表单
function restCode(input: CodeInput): string {
  const attachments = attachmentsOf(input.request)
  const call = protocolCall(
    input.protocol,
    input.model,
    input.modelID,
    apiKeyMarker,
    input.request,
    input.stream,
    input.images,
  )
  const variable = keyVariables[input.protocol]
  const headers = [...call.headers.entries()].map(
    ([name, value]) => `  -H "${name}: ${value.replace(apiKeyMarker, `$${variable}`)}"`,
  )
  const url = `${input.baseURL}${call.path}`
  if (call.body instanceof FormData) {
    const files = formFiles(input.request, attachments)
    const fields = [...call.body.entries()].map(([name, value]) =>
      typeof value === 'string'
        ? `  -F "${name}=${value.replace(/[\\"$`]/g, '\\$&')}"`
        : `  -F "${name}=@${files.shift() ?? value.name};type=${value.type}"`,
    )
    return `curl -X POST "${url}" \\\n${[...headers, ...fields].join(' \\\n')}\n`
  }
  const body = markAttachments(call.body, attachments)
  return `curl ${input.stream ? '-N ' : ''}-X POST "${url}" \\
${headers.join(' \\\n')} \\
  --data-binary @- <<EOF
${shellJSON(body, attachments)}
EOF
`
}

// openAIBody 返回 OpenAI SDK 示例的接口与正文：所选协议为 Responses 时与页面以 Responses 发送的请求相同，其余为页面以 Chat Completions 发送的请求
function openAIBody({ protocol, modelID, request, stream }: CodeInput): {
  responses: boolean
  body: Record<string, unknown>
} {
  if (protocol === 'openai-responses')
    return { responses: true, body: responsesRequest(modelID, request, stream) }
  return { responses: false, body: chatRequest(modelID, request, stream) }
}

// imagesFields 返回 OpenAI Images 示例的方法与参数：带参考图片时为编辑，参数与页面发送的表单或正文相同
function imagesFields(input: CodeInput): {
  method: 'generate' | 'edit'
  fields: Record<string, unknown>
  files: string[]
} {
  const call = protocolCall(
    'openai-images',
    input.model,
    input.modelID,
    '',
    input.request,
    input.stream,
    input.images,
  )
  const attachments = attachmentsOf(input.request)
  if (!(call.body instanceof FormData))
    return { method: 'generate', fields: call.body as Record<string, unknown>, files: [] }
  const fields: Record<string, unknown> = {}
  for (const [name, value] of call.body.entries())
    if (typeof value === 'string' && name !== 'stream')
      fields[name] = name === 'n' ? Number(value) : value
  return { method: 'edit', fields, files: formFiles(input.request, attachments) }
}

// imagesPythonCode 生成 OpenAI Python SDK 的图片生成或编辑示例，结果保存为本地文件
function imagesPythonCode(input: CodeInput): string {
  const { method, fields, files } = imagesFields(input)
  const { stream: _stream, ...args } = fields
  const lines = Object.entries(args).map(([key, value]) => `    ${key}=${pyLiteral(value, 1)},`)
  if (files.length > 0)
    lines.unshift(`    image=[${files.map((file) => `open(${pyString(file)}, "rb")`).join(', ')}],`)
  return `import base64
import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${input.baseURL}/v1",
)

result = client.images.${method}(
${lines.join('\n')}
)
for index, image in enumerate(result.data):
    with open(f"image_{index}.png", "wb") as file:
        file.write(base64.b64decode(image.b64_json))
`
}

// imagesTypescriptCode 生成 OpenAI TypeScript SDK 的图片生成或编辑示例，结果保存为本地文件
function imagesTypescriptCode(input: CodeInput): string {
  const { method, fields, files } = imagesFields(input)
  const { stream: _stream, ...args } = fields
  const lines = Object.entries(args).map(([key, value]) => `  ${key}: ${tsLiteral(value, 1)},`)
  if (files.length > 0)
    lines.unshift(
      `  image: await Promise.all(${JSON.stringify(files)}.map((file) => toFile(fs.createReadStream(file)))),`,
    )
  return `import fs from 'node:fs';
import OpenAI${files.length > 0 ? ', { toFile }' : ''} from 'openai';

const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: '${input.baseURL}/v1',
});

const result = await client.images.${method}({
${lines.join('\n')}
});
result.data?.forEach((image, index) => {
  fs.writeFileSync(\`image_\${index}.png\`, Buffer.from(image.b64_json ?? '', 'base64'));
});
`
}

// openAIPythonCode 生成 OpenAI Python SDK 示例，正文与页面以对应 OpenAI 协议发送的请求相同
function openAIPythonCode(input: CodeInput): string {
  if (input.protocol === 'openai-images') return imagesPythonCode(input)
  const attachments = attachmentsOf(input.request)
  const { responses, body } = openAIBody(input)
  const marked = markAttachments(body, attachments) as Record<string, unknown>
  const files = pyAttachments(
    Object.entries(marked)
      .map(([key, value]) =>
        key === 'extra_body'
          ? `    extra_body=${pyLiteral({ extra_body: value }, 1)},`
          : `    ${key}=${pyLiteral(value, 1)},`,
      )
      .join('\n'),
    attachments,
    false,
  )
  const call = responses ? 'client.responses.create' : 'client.chat.completions.create'
  let output: string
  if (responses)
    output = input.stream
      ? 'for event in response:\n    if event.type == "response.output_text.delta":\n        print(event.delta, end="")\nprint()'
      : 'print(response.output_text)'
  else
    output = input.stream
      ? 'for chunk in response:\n    if chunk.choices and chunk.choices[0].delta.content:\n        print(chunk.choices[0].delta.content, end="")\nprint()'
      : 'print(response.choices[0].message.content)'
  return `${files.setup ? 'import base64\n' : ''}import os

from openai import OpenAI

client = OpenAI(
    api_key=os.environ.get("OPENAI_API_KEY"),
    base_url="${input.baseURL}/v1",
)
${files.setup ? `\n${files.setup}\n` : ''}
response = ${call}(
${files.literal}
)
${output}
`
}

// openAITypescriptCode 生成 OpenAI TypeScript SDK 示例，正文与页面以对应 OpenAI 协议发送的请求相同
function openAITypescriptCode(input: CodeInput): string {
  if (input.protocol === 'openai-images') return imagesTypescriptCode(input)
  const attachments = attachmentsOf(input.request)
  const { responses, body } = openAIBody(input)
  const files = tsAttachments(tsLiteral(markAttachments(body, attachments)), attachments)
  const call = responses ? 'client.responses.create' : 'client.chat.completions.create'
  let output: string
  if (responses)
    output = input.stream
      ? "for await (const event of response) {\n  if (event.type === 'response.output_text.delta') process.stdout.write(event.delta);\n}\nconsole.log();"
      : 'console.log(response.output_text);'
  else
    output = input.stream
      ? "for await (const chunk of response) {\n  process.stdout.write(chunk.choices[0]?.delta?.content ?? '');\n}\nconsole.log();"
      : 'console.log(response.choices[0]?.message.content);'
  return `${files.setup ? "import fs from 'node:fs';\n" : ''}import OpenAI from 'openai';

const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: '${input.baseURL}/v1',
});
${files.setup ? `\n${files.setup}\n` : ''}
const response = await ${call}(${files.literal});
${output}
`
}

// generateCode 按语言生成示例代码
export function generateCode(language: CodeLanguage, input: CodeInput): string {
  switch (language) {
    case 'rest':
      return restCode(input)
    case 'python':
      return pythonCode(input)
    case 'typescript':
      return typescriptCode(input)
    case 'openai-python':
      return openAIPythonCode(input)
    case 'openai-typescript':
      return openAITypescriptCode(input)
  }
}

// CodeOption 是获取代码对话框的一种语言：显示名、高亮语言与下载文件名
export interface CodeOption<Value extends string = string> {
  value: Value
  label: string
  highlight: string
  file: string
}

// codeOptions 是对话页获取代码的语言
export const codeOptions: CodeOption<CodeLanguage>[] = [
  { value: 'python', label: 'Python', highlight: 'python', file: 'main.py' },
  { value: 'typescript', label: 'TypeScript', highlight: 'typescript', file: 'main.ts' },
  { value: 'rest', label: 'REST (cURL)', highlight: 'bash', file: 'request.sh' },
  { value: 'openai-python', label: 'OpenAI Python', highlight: 'python', file: 'main.py' },
  {
    value: 'openai-typescript',
    label: 'OpenAI TypeScript',
    highlight: 'typescript',
    file: 'main.ts',
  },
]
