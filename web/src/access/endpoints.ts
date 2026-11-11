import type { TranslationKey } from '@/i18n'

// Auth 是端点的认证方式：OpenAI 为 Bearer，Anthropic 为 x-api-key，Gemini 为 x-goog-api-key，none 不需要 key
export type Auth = 'openai' | 'anthropic' | 'gemini' | 'none'

// Endpoint 是一个公开端点：方法、路径、说明的文案键、官方文档、JSON 请求体、multipart 字段、下载文件名与 WebSocket 首帧
export interface Endpoint {
  method: string
  path: string
  summary: TranslationKey
  docs: string
  body?: Record<string, unknown>
  form?: [name: string, value: string][]
  output?: string
  query?: string
  socket?: Record<string, unknown>
  script?: (origin: string) => string
}

// EndpointGroup 是接入页端点表的一组：名称、用途说明的文案键、认证方式与各端点，路径与 docs/protocol.md 第 7 节一致
export interface EndpointGroup {
  name: string
  description: TranslationKey
  auth: Auth
  endpoints: Endpoint[]
}

// openai、gemini 与 project 是 OpenAI API 参考、Gemini API 参考与本项目协议文档的地址
const openai = 'https://developers.openai.com/api/reference/resources/'
const gemini = 'https://ai.google.dev/api/'
const project = 'https://github.com/Mag1cFall/AIStudio2API/blob/main/docs/protocol.md'

// text 是 Gemini 请求中一段用户文字
const text = [{ role: 'user', parts: [{ text: 'Hello' }] }]

// endpointGroups 是本服务的全部公开端点，按协议与能力分组
export const endpointGroups: EndpointGroup[] = [
  {
    name: 'Health',
    description: 'access.endpoint.health',
    auth: 'none',
    endpoints: [{ method: 'GET', path: '/health', summary: 'access.ep.health', docs: project }],
  },
  {
    name: 'Models',
    description: 'access.endpoint.models',
    auth: 'openai',
    endpoints: [
      {
        method: 'GET',
        path: '/v1/models',
        summary: 'access.ep.modelsList',
        docs: `${openai}models/methods/list`,
      },
      {
        method: 'GET',
        path: '/v1/models/{model}',
        summary: 'access.ep.modelsGet',
        docs: `${openai}models/methods/retrieve`,
      },
      {
        method: 'GET',
        path: '/v1beta/models',
        summary: 'access.ep.modelsList',
        docs: `${gemini}models#method:-models.list`,
      },
      {
        method: 'GET',
        path: '/v1beta/models/{model}',
        summary: 'access.ep.modelsGet',
        docs: `${gemini}models#method:-models.get`,
      },
    ],
  },
  {
    name: 'OpenAI Chat Completions',
    description: 'access.endpoint.chat',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/chat/completions',
        summary: 'access.ep.chat',
        docs: `${openai}chat/subresources/completions/methods/create`,
        body: { model: '{model}', messages: [{ role: 'user', content: 'Hello' }] },
      },
    ],
  },
  {
    name: 'OpenAI Responses',
    description: 'access.endpoint.responses',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/responses',
        summary: 'access.ep.responsesCreate',
        docs: `${openai}responses/methods/create`,
        body: { model: '{model}', input: 'Hello' },
      },
      {
        method: 'POST',
        path: '/v1/responses/input_tokens',
        summary: 'access.ep.responsesTokens',
        docs: `${openai}responses/subresources/input_tokens/methods/count`,
        body: { model: '{model}', input: 'Hello' },
      },
      {
        method: 'POST',
        path: '/v1/responses/compact',
        summary: 'access.ep.responsesCompact',
        docs: `${openai}responses/methods/compact`,
        body: { model: '{model}', input: [{ role: 'user', content: 'Hello' }] },
      },
      {
        method: 'GET',
        path: '/v1/responses/{id}',
        summary: 'access.ep.responsesGet',
        docs: `${openai}responses/methods/retrieve`,
      },
      {
        method: 'DELETE',
        path: '/v1/responses/{id}',
        summary: 'access.ep.responsesDelete',
        docs: `${openai}responses/methods/delete`,
      },
      {
        method: 'GET',
        path: '/v1/responses/{id}/input_items',
        summary: 'access.ep.responsesItems',
        docs: `${openai}responses/subresources/input_items/methods/list`,
      },
      {
        method: 'POST',
        path: '/v1/responses/{id}/cancel',
        summary: 'access.ep.responsesCancel',
        docs: `${openai}responses/methods/cancel`,
      },
    ],
  },
  {
    name: 'Anthropic Messages',
    description: 'access.endpoint.anthropic',
    auth: 'anthropic',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/messages',
        summary: 'access.ep.messages',
        docs: 'https://platform.claude.com/docs/en/api/messages/create',
        body: {
          model: '{model}',
          max_tokens: 1024,
          messages: [{ role: 'user', content: 'Hello' }],
        },
      },
      {
        method: 'POST',
        path: '/v1/messages/count_tokens',
        summary: 'access.ep.messagesTokens',
        docs: 'https://platform.claude.com/docs/en/api/messages/count_tokens',
        body: { model: '{model}', messages: [{ role: 'user', content: 'Hello' }] },
      },
    ],
  },
  {
    name: 'Gemini API',
    description: 'access.endpoint.gemini',
    auth: 'gemini',
    endpoints: [
      {
        method: 'POST',
        path: '/v1beta/models/{model}:generateContent',
        summary: 'access.ep.generate',
        docs: `${gemini}generate-content#method:-models.generatecontent`,
        body: { contents: text },
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:streamGenerateContent',
        summary: 'access.ep.stream',
        docs: `${gemini}generate-content#method:-models.streamgeneratecontent`,
        query: 'alt=sse',
        body: { contents: text },
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:countTokens',
        summary: 'access.ep.countTokens',
        docs: `${gemini}tokens#method:-models.counttokens`,
        body: { contents: text },
      },
      {
        method: 'POST',
        path: '/v1beta/cachedContents',
        summary: 'access.ep.cacheCreate',
        docs: `${gemini}caching#method:-cachedcontents.create`,
        body: { model: 'models/{model}', contents: text, ttl: '300s' },
      },
      {
        method: 'GET',
        path: '/v1beta/cachedContents',
        summary: 'access.ep.cacheList',
        docs: `${gemini}caching#method:-cachedcontents.list`,
      },
      {
        method: 'GET',
        path: '/v1beta/cachedContents/{id}',
        summary: 'access.ep.cacheGet',
        docs: `${gemini}caching#method:-cachedcontents.get`,
      },
      {
        method: 'PATCH',
        path: '/v1beta/cachedContents/{id}',
        summary: 'access.ep.cacheUpdate',
        docs: `${gemini}caching#method:-cachedcontents.patch`,
        body: { ttl: '600s' },
      },
      {
        method: 'DELETE',
        path: '/v1beta/cachedContents/{id}',
        summary: 'access.ep.cacheDelete',
        docs: `${gemini}caching#method:-cachedcontents.delete`,
      },
    ],
  },
  {
    name: 'Gemini Interactions',
    description: 'access.endpoint.interactions',
    auth: 'gemini',
    endpoints: [
      {
        method: 'POST',
        path: '/v1beta/interactions',
        summary: 'access.ep.interactionCreate',
        docs: `${gemini}interactions-api`,
        body: { model: '{model}', input: 'Hello' },
      },
      {
        method: 'GET',
        path: '/v1beta/interactions/{id}',
        summary: 'access.ep.interactionGet',
        docs: `${gemini}interactions-api`,
      },
      {
        method: 'DELETE',
        path: '/v1beta/interactions/{id}',
        summary: 'access.ep.interactionDelete',
        docs: `${gemini}interactions-api`,
      },
      {
        method: 'POST',
        path: '/v1beta/interactions/{id}/cancel',
        summary: 'access.ep.interactionCancel',
        docs: `${gemini}interactions-api`,
      },
    ],
  },
  {
    name: 'Gemini Decisions',
    description: 'access.endpoint.decisions',
    auth: 'gemini',
    endpoints: [
      {
        method: 'POST',
        path: '/v1beta/decisions',
        summary: 'access.ep.decisionCreate',
        docs: 'https://ai.google.dev/gemini-api/docs/decisions-api',
        body: {
          model: '{model}',
          input: 'I was charged twice for my order.',
          questions: {
            is_refund: { type: 'noul', instructions: 'Is the customer asking for money back?' },
          },
        },
      },
    ],
  },
  {
    name: 'Embeddings',
    description: 'access.endpoint.embeddings',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/embeddings',
        summary: 'access.ep.embeddings',
        docs: `${openai}embeddings/methods/create`,
        body: { model: '{model}', input: ['Hello'] },
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:embedContent',
        summary: 'access.ep.embedContent',
        docs: `${gemini}embeddings#method:-models.embedcontent`,
        body: { content: { parts: [{ text: 'Hello' }] } },
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:batchEmbedContents',
        summary: 'access.ep.batchEmbed',
        docs: `${gemini}embeddings#method:-models.batchembedcontents`,
        body: { requests: [{ model: 'models/{model}', content: { parts: [{ text: 'Hello' }] } }] },
      },
    ],
  },
  {
    name: 'Images',
    description: 'access.endpoint.images',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/images/generations',
        summary: 'access.ep.imagesCreate',
        docs: `${openai}images/methods/generate`,
        body: { model: '{model}', prompt: 'A paper boat on a calm pond' },
      },
      {
        method: 'POST',
        path: '/v1/images/edits',
        summary: 'access.ep.imagesEdit',
        docs: `${openai}images/methods/edit`,
        form: [
          ['model', '{model}'],
          ['image', '@image.png'],
          ['prompt', 'Add a rainbow'],
        ],
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:predict',
        summary: 'access.ep.imagen',
        docs: 'https://ai.google.dev/gemini-api/docs/imagen',
        body: {
          instances: [{ prompt: 'A paper boat on a calm pond' }],
          parameters: { sampleCount: 1 },
        },
      },
    ],
  },
  {
    name: 'Audio',
    description: 'access.endpoint.audio',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/audio/speech',
        summary: 'access.ep.speech',
        docs: `${openai}audio/subresources/speech/methods/create`,
        body: { model: '{model}', input: 'Have a wonderful day!', voice: 'Kore' },
        output: 'speech.mp3',
      },
      {
        method: 'POST',
        path: '/v1/audio/transcriptions',
        summary: 'access.ep.transcription',
        docs: `${openai}audio/subresources/transcriptions/methods/create`,
        form: [
          ['model', '{model}'],
          ['file', '@audio.mp3'],
        ],
      },
      {
        method: 'POST',
        path: '/v1/audio/translations',
        summary: 'access.ep.translation',
        docs: `${openai}audio/subresources/translations/methods/create`,
        form: [
          ['model', '{model}'],
          ['file', '@audio.mp3'],
        ],
      },
    ],
  },
  {
    name: 'Videos',
    description: 'access.endpoint.videos',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/videos',
        summary: 'access.ep.videoCreate',
        docs: `${openai}videos/methods/create`,
        body: { model: '{model}', prompt: 'A drone flying over a forest at sunrise' },
      },
      {
        method: 'GET',
        path: '/v1/videos',
        summary: 'access.ep.videoList',
        docs: `${openai}videos/methods/list`,
      },
      {
        method: 'GET',
        path: '/v1/videos/{id}',
        summary: 'access.ep.videoGet',
        docs: `${openai}videos/methods/retrieve`,
      },
      {
        method: 'DELETE',
        path: '/v1/videos/{id}',
        summary: 'access.ep.videoDelete',
        docs: `${openai}videos/methods/delete`,
      },
      {
        method: 'GET',
        path: '/v1/videos/{id}/content',
        summary: 'access.ep.videoContent',
        docs: `${openai}videos/methods/download_content`,
        output: 'video.mp4',
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:predictLongRunning',
        summary: 'access.ep.omniVideo',
        docs: 'https://ai.google.dev/gemini-api/docs/video',
        body: { instances: [{ prompt: 'A drone flying over a forest at sunrise' }] },
      },
      {
        method: 'GET',
        path: '/v1beta/operations/{id}',
        summary: 'access.ep.operation',
        docs: 'https://ai.google.dev/gemini-api/docs/video',
      },
      {
        method: 'GET',
        path: '/v1beta/models/{model}/operations/{id}',
        summary: 'access.ep.operation',
        docs: 'https://ai.google.dev/gemini-api/docs/video',
      },
    ],
  },
  {
    name: 'Files',
    description: 'access.endpoint.files',
    auth: 'openai',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/files',
        summary: 'access.ep.fileUpload',
        docs: `${openai}files/methods/create`,
        form: [
          ['purpose', 'user_data'],
          ['file', '@document.pdf'],
        ],
      },
      {
        method: 'GET',
        path: '/v1/files',
        summary: 'access.ep.fileList',
        docs: `${openai}files/methods/list`,
      },
      {
        method: 'GET',
        path: '/v1/files/{id}',
        summary: 'access.ep.fileGet',
        docs: `${openai}files/methods/retrieve`,
      },
      {
        method: 'GET',
        path: '/v1/files/{id}/content',
        summary: 'access.ep.fileContent',
        docs: `${openai}files/methods/content`,
        output: 'document.pdf',
      },
      {
        method: 'DELETE',
        path: '/v1/files/{id}',
        summary: 'access.ep.fileDelete',
        docs: `${openai}files/methods/delete`,
      },
      {
        method: 'POST',
        path: '/upload/v1beta/files',
        summary: 'access.ep.fileUpload',
        docs: `${gemini}files#method:-media.upload`,
        script: (origin) => `curl "${origin}/upload/v1beta/files" \\
  -H "x-goog-api-key: $GEMINI_API_KEY" \\
  -H "X-Goog-Upload-Protocol: resumable" \\
  -H "X-Goog-Upload-Command: start" \\
  -H "X-Goog-Upload-Header-Content-Length: $(wc -c < document.pdf)" \\
  -H "X-Goog-Upload-Header-Content-Type: application/pdf" \\
  -H "Content-Type: application/json" \\
  -d '{"file": {"display_name": "document.pdf"}}' \\
  -D upload-headers.txt
UPLOAD_URL=$(grep -i "x-goog-upload-url: " upload-headers.txt | cut -d" " -f2 | tr -d "\\r")
curl "$UPLOAD_URL" \\
  -H "X-Goog-Upload-Command: upload, finalize" \\
  -H "X-Goog-Upload-Offset: 0" \\
  --data-binary "@document.pdf"
`,
      },
      {
        method: 'GET',
        path: '/v1beta/files',
        summary: 'access.ep.fileList',
        docs: `${gemini}files#method:-files.list`,
      },
      {
        method: 'GET',
        path: '/v1beta/files/{id}',
        summary: 'access.ep.fileGet',
        docs: `${gemini}files#method:-files.get`,
      },
      {
        method: 'DELETE',
        path: '/v1beta/files/{id}',
        summary: 'access.ep.fileDelete',
        docs: `${gemini}files#method:-files.delete`,
      },
    ],
  },
  {
    name: 'Media',
    description: 'access.endpoint.media',
    auth: 'none',
    endpoints: [
      {
        method: 'GET',
        path: '/media/{file}',
        summary: 'access.ep.media',
        docs: project,
        output: 'image.png',
      },
    ],
  },
  {
    name: 'Live',
    description: 'access.endpoint.live',
    auth: 'gemini',
    endpoints: [
      {
        method: 'GET',
        path: '/v1/live',
        summary: 'access.ep.live',
        docs: `${gemini}live`,
        socket: {
          type: 'setup',
          model: '{model}',
          input_modalities: ['text'],
          output_modalities: ['text'],
        },
      },
      {
        method: 'GET',
        path: '/v1/robotics/stream',
        summary: 'access.ep.robotics',
        docs: project,
        socket: { type: 'setup', model: '{model}' },
      },
    ],
  },
]

// authHeaders 是各认证方式在 curl 中的请求头
const authHeaders: Record<Auth, string[]> = {
  openai: ['Authorization: Bearer $OPENAI_API_KEY'],
  anthropic: ['x-api-key: $ANTHROPIC_API_KEY', 'anthropic-version: 2023-06-01'],
  gemini: ['x-goog-api-key: $GEMINI_API_KEY'],
  none: [],
}

// pathParams 返回路径中的参数名
export function pathParams(path: string): string[] {
  return [...path.matchAll(/\{(\w+)\}/g)].map((match) => match[1]!)
}

// fill 把示例中的 {model} 换成所选模型，其余路径参数换成大写占位
function fill(value: string, model: string): string {
  return value
    .replaceAll('{model}', model)
    .replace(/\{(\w+)\}/g, (_, name: string) => name.toUpperCase())
}

// bodyFields 返回请求体的顶层字段与 JSON 类型
export function bodyFields(endpoint: Endpoint): [name: string, type: string][] {
  if (endpoint.form)
    return endpoint.form.map(([name, value]) => [name, value.startsWith('@') ? 'file' : 'string'])
  return Object.entries(endpoint.body ?? {}).map(([name, value]) => [
    name,
    Array.isArray(value) ? 'array' : value === null ? 'null' : typeof value,
  ])
}

// endpointAuth 返回端点的认证方式：/v1beta 与 /upload 下的 Gemini 端点用 x-goog-api-key，其余取所在组的方式
export function endpointAuth(group: EndpointGroup, endpoint: Endpoint): Auth {
  return group.auth !== 'none' && /^\/(v1beta|upload)\//.test(endpoint.path) ? 'gemini' : group.auth
}

// endpointExample 生成端点的请求示例：HTTP 端点为 curl，WebSocket 端点为 websocat 与首个 setup 帧
export function endpointExample(
  origin: string,
  group: EndpointGroup,
  endpoint: Endpoint,
  model: string,
): string {
  if (endpoint.script) return endpoint.script(origin)
  const path = fill(endpoint.path, model)
  if (endpoint.socket) {
    const socket = origin.replace(/^http/, 'ws')
    return `websocat "${socket}${path}?key=$GEMINI_API_KEY"\n${fill(JSON.stringify(endpoint.socket), model)}\n`
  }
  // implicit 表示 curl 由是否带请求体即可推出方法
  const implicit =
    endpoint.method === 'GET' || (endpoint.method === 'POST' && (endpoint.body || endpoint.form))
  const lines = [
    `curl${implicit ? '' : ` -X ${endpoint.method}`} "${origin}${path}${endpoint.query ? `?${endpoint.query}` : ''}"`,
    ...authHeaders[endpointAuth(group, endpoint)].map((header) => `-H "${header}"`),
  ]
  if (endpoint.body)
    lines.push(
      '-H "Content-Type: application/json"',
      `-d '${fill(JSON.stringify(endpoint.body, null, 2), model).replaceAll('\n', '\n  ')}'`,
    )
  for (const [name, value] of endpoint.form ?? []) lines.push(`-F "${name}=${fill(value, model)}"`)
  if (endpoint.output) lines.push(`--output ${endpoint.output}`)
  return `${lines.join(' \\\n  ')}\n`
}
