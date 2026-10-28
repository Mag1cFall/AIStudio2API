import { computed, reactive } from 'vue'
import { useConsole } from '@/console'
import { keepPage } from '@/desktop'
import type { Model } from '@/types'
import type { CodeOption } from '../code'
import { newTurnID } from '../session'
import { predictImages } from './api'
import { pyTriple, tsTemplate } from './code'
import { modelMatches } from '../catalog'
import { pageKindOf } from './kinds'
import { requestTitle } from '../title'

// ImagenTurn 是 Imagen 页的一轮：用户轮次记录提示词，模型轮次记录生成的图片
export interface ImagenTurn {
  id: string
  role: 'user' | 'model'
  prompt?: string
  model?: string
  count?: number
  images?: string[]
  duration?: number
}

// ImagenLanguage 是 Imagen 页获取代码的语言
export type ImagenLanguage = 'python' | 'typescript' | 'rest'

// imagenCodeOptions 是 Imagen 页获取代码的语言选项
export const imagenCodeOptions: CodeOption<ImagenLanguage>[] = [
  { value: 'python', label: 'Python', highlight: 'python', file: 'main.py' },
  {
    value: 'typescript',
    label: 'TypeScript',
    highlight: 'typescript',
    file: 'main.ts',
  },
  { value: 'rest', label: 'REST', highlight: 'bash', file: 'request.sh' },
]

// imagenAspectRatios 是官网 Imagen 页的宽高比选项
const imagenAspectRatios = ['1:1', '9:16', '16:9', '4:3', '3:4']

const state = reactive({
  model: '',
  title: '',
  draft: '',
  count: 1,
  aspectRatio: '1:1',
  running: false,
  started: 0,
  turns: [] as ImagenTurn[],
})
let controller: AbortController | undefined

// 关闭桌面窗口时，生成中与只存在于页面的图片保留页面
keepPage(() => state.running || state.turns.length > 0)

// imagenModels 返回目录中归属 Imagen 页的模型
export function imagenModels(): Model[] {
  return useConsole().models.value.filter((model) => pageKindOf(model) === 'image')
}

// generateTitle 与官网一样在会话未命名时于提交时以提示词生成标题，失败时保持未命名
async function generateTitle(model: string, prompt: string): Promise<void> {
  const title = await requestTitle(model, prompt, '', () => ({
    owner: state.turns,
    title: state.title,
  }))
  if (title !== '') state.title = title
}

// run 以当前提示词与设置调用本服务的 predict 接口，结果写入新的模型轮次；与官网一样失败时撤下本轮并抛出上游说明，取消时只撤下本轮
async function run(): Promise<void> {
  const prompt = state.draft.trim()
  if (prompt === '' || state.running || state.model === '') return
  const model = imagenModels().find((item) => item.id === state.model)
  if (state.title === '') void generateTitle(state.model, prompt)
  state.turns.push({ id: newTurnID(), role: 'user', prompt })
  const turn = reactive<ImagenTurn>({
    id: newTurnID(),
    role: 'model',
    model: model?.name ?? state.model,
    count: state.count,
  })
  state.turns.push(turn)
  state.running = true
  state.started = performance.now()
  controller = new AbortController()
  const signal = controller.signal
  try {
    turn.images = await predictImages(
      state.model,
      { prompt, sampleCount: state.count, aspectRatio: state.aspectRatio },
      signal,
    )
  } catch (error) {
    state.turns.splice(-2)
    if (!signal.aborted) throw error
  } finally {
    turn.duration = performance.now() - state.started
    state.running = false
    controller = undefined
  }
}

// imagenCode 按语言生成当前提示词与设置的 Imagen 示例代码
export function imagenCode(language: ImagenLanguage, baseURL: string): string {
  const { model, draft, count, aspectRatio } = state
  switch (language) {
    case 'python':
      return `# pip install google-genai
import os
from google import genai
from google.genai import types

client = genai.Client(
    api_key=os.environ.get("AISTUDIO2API_KEY"),
    http_options=types.HttpOptions(base_url="${baseURL}"),
)

result = client.models.generate_images(
    model="${model}",
    prompt="""${pyTriple(draft)}""",
    config=types.GenerateImagesConfig(
        number_of_images=${count},
        aspect_ratio="${aspectRatio}",
    ),
)

for index, generated in enumerate(result.generated_images):
    generated.image.save(f"image_{index}.png")
`
    case 'typescript':
      return `// npm install @google/genai
import { GoogleGenAI } from '@google/genai';
import { writeFileSync } from 'node:fs';

const ai = new GoogleGenAI({
  apiKey: process.env.AISTUDIO2API_KEY,
  httpOptions: { baseUrl: '${baseURL}' },
});

const result = await ai.models.generateImages({
  model: '${model}',
  prompt: \`${tsTemplate(draft)}\`,
  config: { numberOfImages: ${count}, aspectRatio: '${aspectRatio}' },
});

result.generatedImages?.forEach((generated, index) => {
  writeFileSync(\`image_\${index}.png\`, Buffer.from(generated.image?.imageBytes ?? '', 'base64'));
});
`
    case 'rest':
      return `curl "${baseURL}/v1beta/models/${model}:predict" \\
  -H "x-goog-api-key: $AISTUDIO2API_KEY" \\
  -H "Content-Type: application/json" \\
  -X POST \\
  -d '${JSON.stringify({ instances: [{ prompt: draft }], parameters: { sampleCount: count, aspectRatio } }).replaceAll("'", "'\\''")}'
`
  }
}

// useImagen 返回 Imagen 页的状态与操作
export function useImagen() {
  const currentModel = computed(() => imagenModels().find((model) => model.id === state.model))
  return {
    state,
    currentModel,
    aspectRatios: imagenAspectRatios,
    // ensureModel 在目录变化后选定模型：地址中的模型优先，其次保留当前模型，否则用第一个 Imagen 模型
    ensureModel(requested?: string): void {
      const list = imagenModels()
      const wanted =
        list.find((model) => modelMatches(model, requested)) ??
        list.find((model) => model.id === state.model) ??
        list[0]
      if (wanted) state.model = wanted.id
    },
    // reset 恢复默认运行设置
    reset(): void {
      state.count = 1
      state.aspectRatio = '1:1'
    },
    // newSession 清空当前会话
    newSession(): void {
      controller?.abort()
      state.turns = []
      state.title = ''
      state.draft = ''
    },
    run,
    // stop 取消进行中的生成
    stop(): void {
      controller?.abort()
    },
  }
}
