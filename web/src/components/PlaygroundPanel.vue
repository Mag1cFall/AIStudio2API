<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { ApiError, runPlayground, type PlaygroundChunk } from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import type {
  Model,
  PlaygroundInput,
  PlaygroundMode,
  PlaygroundProtocol,
  PlaygroundResult,
  PlaygroundTool,
} from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  models: Model[]
  apiKey: string
}>()

const { t } = useI18n()
const protocols: { id: PlaygroundProtocol; label: string }[] = [
  { id: 'openai-chat', label: 'OpenAI Chat' },
  { id: 'openai-responses', label: 'OpenAI Responses' },
  { id: 'anthropic', label: 'Anthropic Messages' },
  { id: 'gemini', label: 'Gemini GenerateContent' },
]
const modes: { id: PlaygroundMode; label?: TranslationKey }[] = [
  { id: 'text', label: 'playground.modeText' },
  { id: 'image', label: 'playground.modeImage' },
  { id: 'speech', label: 'playground.modeSpeech' },
  { id: 'music', label: 'playground.modeMusic' },
  { id: 'video' },
]
const tools: { id: Exclude<PlaygroundTool, ''>; label: string; capability: string }[] = [
  { id: 'web_search', label: 'Google Search', capability: 'google_search' },
  { id: 'code_interpreter', label: 'Code Execution', capability: 'code_execution' },
  { id: 'url_context', label: 'URL Context', capability: 'browse' },
  { id: 'google_maps', label: 'Google Maps', capability: 'google_maps' },
]

const form = reactive<PlaygroundInput>({
  mode: 'text',
  protocol: 'openai-chat',
  model: '',
  prompt: '',
  system: '',
  stream: true,
  reasoning: '',
  tool: '',
  imageSize: 'auto',
  imageQuality: 'auto',
  voice: '',
  apiKey: props.apiKey,
})
const result = reactive<PlaygroundResult>({
  text: '',
  reasoning: '',
  tools: '',
  media: [],
  raw: '',
  durationMs: 0,
  status: 0,
})
const isRunning = ref(false)
const outputMode = ref<'output' | 'raw'>('output')
const copied = ref(false)
const hasRun = ref(false)
const submittedPrompt = ref('')
const submittedModel = ref('')
let controller: AbortController | undefined

const availableModels = computed(() =>
  props.models.filter((model) => {
    const capabilities = model.capabilities ?? {}
    if (form.mode === 'image') return capabilities.image_route === true
    if (form.mode === 'speech') return capabilities.speech_route === true
    if (form.mode === 'music') return capabilities.music_route === true
    if (form.mode === 'video') return capabilities.video_route === true
    return (
      capabilities.chat_model === true ||
      (model.methods.includes('generateContent') &&
        capabilities.image_route !== true &&
        capabilities.speech_route !== true &&
        capabilities.music_route !== true &&
        capabilities.video_route !== true)
    )
  }),
)
const selectedModel = computed(() => availableModels.value.find((model) => model.id === form.model))
const supportsThinking = computed(() => {
  const capabilities = selectedModel.value?.capabilities ?? {}
  return (
    capabilities.thinking === true ||
    capabilities.thinking_budget === true ||
    capabilities.thinking_level === true
  )
})
const availableTools = computed(() => {
  const capabilities = selectedModel.value?.capabilities ?? {}
  return tools.filter((tool) => capabilities[tool.capability] === true)
})
const voices = computed(() => selectedModel.value?.capability_options?.voices ?? [])

function modeLabel(mode: (typeof modes)[number]): string {
  return mode.label === undefined ? 'Veo' : t(mode.label)
}

watch(
  [() => props.models, () => form.mode],
  () => {
    if (!availableModels.value.some((model) => model.id === form.model)) {
      form.model = availableModels.value[0]?.id ?? ''
    }
  },
  { immediate: true },
)

watch(selectedModel, () => {
  if (!supportsThinking.value) form.reasoning = ''
  if (!availableTools.value.some((tool) => tool.id === form.tool)) form.tool = ''
  if (!voices.value.includes(form.voice)) form.voice = voices.value[0] ?? ''
})

watch(
  () => props.apiKey,
  (apiKey) => {
    form.apiKey = apiKey
  },
)

const endpoint = computed(() => {
  if (form.mode === 'image') return '/v1/images/generations'
  if (form.mode === 'speech') return '/v1/audio/speech'
  if (form.mode === 'music') return `/v1beta/models/${form.model || '{model}'}:generateContent`
  if (form.mode === 'video') return '/v1/videos'
  if (form.protocol === 'openai-chat') return '/v1/chat/completions'
  if (form.protocol === 'openai-responses') return '/v1/responses'
  if (form.protocol === 'anthropic') return '/v1/messages'
  const method = form.stream ? 'streamGenerateContent' : 'generateContent'
  return `/v1beta/models/${form.model || '{model}'}:${method}`
})

const visibleOutput = computed(() => {
  if (outputMode.value === 'raw') return result.raw
  return [result.text, result.reasoning, result.tools, ...result.media.map((media) => media.url)]
    .filter(Boolean)
    .join('\n')
})
const hasOutput = computed(
  () =>
    result.text !== '' || result.reasoning !== '' || result.tools !== '' || result.media.length > 0,
)

// clearMedia 释放浏览器创建的媒体资源
function clearMedia(): void {
  for (const media of result.media) {
    if (media.url.startsWith('blob:')) URL.revokeObjectURL(media.url)
  }
  result.media = []
}

// appendChunk 追加一次协议解析结果
function appendChunk(chunk: PlaygroundChunk): void {
  result.text += chunk.text
  result.reasoning += chunk.reasoning
  result.tools += chunk.tools
  result.media.push(...chunk.media)
}

// execute 发送公开协议请求并实时追加结果
async function execute(): Promise<void> {
  if (form.model === '' || form.prompt.trim() === '') return
  const input = { ...form }
  controller = new AbortController()
  submittedPrompt.value = form.prompt
  submittedModel.value = form.model
  hasRun.value = true
  form.prompt = ''
  clearMedia()
  result.text = ''
  result.reasoning = ''
  result.tools = ''
  result.raw = ''
  result.durationMs = 0
  result.status = 0
  outputMode.value = 'output'
  isRunning.value = true
  const started = performance.now()

  try {
    const response = await runPlayground(input, controller.signal, (chunk, raw) => {
      appendChunk(chunk)
      result.raw += `${raw}\n`
    })
    result.status = response.status
    if (response.chunk !== undefined) appendChunk(response.chunk)
    result.raw += response.raw ?? ''
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') return
    result.status = error instanceof ApiError ? error.status : 0
    result.raw = error instanceof Error ? error.message : t('common.error')
    outputMode.value = 'raw'
  } finally {
    result.durationMs = Math.round(performance.now() - started)
    isRunning.value = false
  }
}

// clearConversation 清空当前试用结果
function clearConversation(): void {
  controller?.abort()
  clearMedia()
  hasRun.value = false
  submittedPrompt.value = ''
  submittedModel.value = ''
  result.text = ''
  result.reasoning = ''
  result.tools = ''
  result.raw = ''
  result.durationMs = 0
  result.status = 0
}

// stop 中止当前浏览器请求并触发服务端取消
function stop(): void {
  controller?.abort()
}

// copyOutput 复制当前可见响应
async function copyOutput(): Promise<void> {
  await navigator.clipboard.writeText(visibleOutput.value)
  copied.value = true
  window.setTimeout(() => {
    copied.value = false
  }, 1200)
}

onUnmounted(() => {
  controller?.abort()
  clearMedia()
})
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-hidden md:flex-row">
    <div class="flex min-w-0 flex-1 flex-col bg-[#0d1117]">
      <div class="flex-1 space-y-4 overflow-auto p-4">
        <div v-if="!hasRun" class="flex h-full flex-col items-center justify-center text-gray-500">
          <UiIcon name="chatBubble" :size="64" />
          <p>{{ t('playground.waiting') }}</p>
        </div>

        <div v-else class="mx-auto flex max-w-3xl gap-4">
          <div
            class="flex h-8 w-8 shrink-0 select-none items-center justify-center rounded-full bg-blue-600"
          >
            <span class="text-xs font-bold">U</span>
          </div>
          <div class="min-w-0 flex-1 space-y-2">
            <div
              class="rounded-lg border border-[#30363d] bg-[#161b22] p-3 text-sm leading-relaxed text-gray-200 shadow-sm"
            >
              <div class="whitespace-pre-wrap">{{ submittedPrompt }}</div>
            </div>
          </div>
        </div>

        <div v-if="hasRun" class="mx-auto flex max-w-3xl gap-4">
          <div
            class="flex h-8 w-8 shrink-0 select-none items-center justify-center rounded-full bg-green-600"
            :class="{ 'animate-pulse': isRunning }"
          >
            <span class="text-xs font-bold">AI</span>
          </div>
          <div class="min-w-0 flex-1 space-y-2">
            <details
              v-if="result.reasoning"
              class="group/thinking overflow-hidden rounded-lg border border-blue-900/30 bg-[#0d1117]"
              open
            >
              <summary
                class="flex cursor-pointer list-none items-center gap-2 bg-[#161b22] px-3 py-2 text-xs font-medium text-gray-400 transition select-none hover:bg-[#1c2128]"
              >
                <UiIcon name="chevronRight" :size="12" />
                <span class="text-blue-400">{{ t('playground.reasoningOutput') }}</span>
                <span v-if="isRunning" class="animate-pulse">...</span>
              </summary>
              <pre
                class="border-t border-[#30363d] bg-[#0d1117]/50 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-gray-400"
                >{{ result.reasoning }}</pre>
            </details>

            <div
              class="relative rounded-lg border border-[#30363d] bg-[#161b22] p-3 text-sm leading-relaxed text-gray-200 shadow-sm"
            >
              <pre
                v-if="outputMode === 'raw'"
                class="overflow-x-auto font-mono text-xs whitespace-pre-wrap text-gray-300"
                >{{ result.raw }}</pre>
              <pre v-else-if="result.text" class="font-sans whitespace-pre-wrap text-gray-200">{{
                result.text
              }}</pre>
              <span v-else-if="isRunning" class="animate-pulse text-gray-500">...</span>
              <pre
                v-else-if="result.raw && !hasOutput"
                class="font-mono text-xs whitespace-pre-wrap text-red-300"
                >{{ result.raw }}</pre>
            </div>

            <details
              v-if="result.tools"
              class="overflow-hidden rounded-lg border border-[#30363d] bg-[#0d1117]"
            >
              <summary
                class="cursor-pointer bg-[#161b22] px-3 py-2 text-xs font-medium text-gray-400 hover:bg-[#1c2128]"
              >
                {{ t('playground.toolOutput') }}
              </summary>
              <pre
                class="border-t border-[#30363d] p-3 font-mono text-xs whitespace-pre-wrap text-gray-400"
                >{{ result.tools }}</pre>
            </details>

            <div v-if="result.media.length" class="space-y-3">
              <template v-for="(media, index) in result.media" :key="`${media.url}:${index}`">
                <img
                  v-if="media.mime.startsWith('image/')"
                  :src="media.url"
                  alt=""
                  class="max-h-[560px] max-w-full rounded-md border border-[#30363d] object-contain"
                />
                <audio
                  v-else-if="media.mime.startsWith('audio/')"
                  :src="media.url"
                  class="w-full"
                  controls
                ></audio>
                <video
                  v-else-if="media.mime.startsWith('video/')"
                  :src="media.url"
                  class="max-h-[560px] w-full rounded-md border border-[#30363d]"
                  controls
                ></video>
              </template>
            </div>

            <div class="flex items-center justify-between gap-3 text-xs text-gray-600">
              <span>
                {{ submittedModel }}
                <template v-if="result.status"> · HTTP {{ result.status }}</template>
                <template v-if="result.durationMs"> · {{ result.durationMs }} ms</template>
              </span>
              <div class="flex gap-2">
                <button
                  class="rounded px-2 py-1 text-gray-500 hover:bg-[#30363d] hover:text-white"
                  type="button"
                  @click="outputMode = outputMode === 'output' ? 'raw' : 'output'"
                >
                  {{ outputMode === 'output' ? t('playground.raw') : t('playground.output') }}
                </button>
                <button
                  class="rounded px-2 py-1 text-gray-500 hover:bg-[#30363d] hover:text-white"
                  type="button"
                  :disabled="visibleOutput === ''"
                  @click="copyOutput"
                >
                  {{ copied ? t('common.copied') : t('common.copy') }}
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="border-t border-[#30363d] bg-[#161b22] p-4">
        <div class="mx-auto flex max-w-3xl gap-2">
          <textarea
            v-model="form.prompt"
            class="h-[42px] max-h-[200px] flex-1 resize-none rounded border border-[#30363d] bg-[#0d1117] p-2 text-sm text-white focus:border-blue-500 focus:outline-none"
            :placeholder="t('playground.placeholder')"
            @keydown.ctrl.enter.prevent="execute"
          ></textarea>
          <button
            v-if="isRunning"
            class="flex items-center gap-2 rounded bg-red-600 px-4 py-2 font-medium text-white transition hover:bg-red-500"
            type="button"
            @click="stop"
          >
            <UiIcon name="stopAnim" :size="16" />
            {{ t('playground.stop') }}
          </button>
          <button
            v-else
            class="rounded bg-blue-600 px-4 py-2 font-medium text-white transition hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
            type="button"
            :disabled="form.model === '' || form.prompt.trim() === ''"
            @click="execute"
          >
            {{ t('playground.send') }}
          </button>
        </div>
      </div>
    </div>

    <aside
      class="flex max-h-[45%] w-full shrink-0 flex-col space-y-5 overflow-auto border-t border-[#30363d] bg-[#161b22] p-4 md:max-h-none md:w-64 md:border-t-0 md:border-l"
    >
      <div>
        <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
          t('playground.mode')
        }}</label>
        <select
          v-model="form.mode"
          class="w-full appearance-none rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white focus:border-blue-500 focus:outline-none"
        >
          <option v-for="mode in modes" :key="mode.id" :value="mode.id">
            {{ modeLabel(mode) }}
          </option>
        </select>
      </div>

      <div>
        <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
          t('playground.model')
        }}</label>
        <select
          v-model="form.model"
          class="w-full appearance-none rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white focus:border-blue-500 focus:outline-none"
        >
          <option v-for="model in availableModels" :key="model.id" :value="model.id">
            {{ model.name }}
          </option>
        </select>
      </div>

      <template v-if="form.mode === 'text'">
        <div>
          <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
            t('playground.protocol')
          }}</label>
          <select
            v-model="form.protocol"
            class="w-full appearance-none rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white focus:border-blue-500 focus:outline-none"
          >
            <option v-for="protocol in protocols" :key="protocol.id" :value="protocol.id">
              {{ protocol.label }}
            </option>
          </select>
        </div>

        <div>
          <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
            t('playground.reasoning')
          }}</label>
          <select
            v-model="form.reasoning"
            class="w-full appearance-none rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white focus:border-blue-500 focus:outline-none disabled:opacity-50"
            :disabled="!supportsThinking"
          >
            <option value="">{{ t('playground.reasoningOff') }}</option>
            <option value="low">{{ t('playground.reasoningLow') }}</option>
            <option value="medium">{{ t('playground.reasoningMedium') }}</option>
            <option value="high">{{ t('playground.reasoningHigh') }}</option>
          </select>
        </div>

        <div>
          <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
            t('playground.tool')
          }}</label>
          <select
            v-model="form.tool"
            class="w-full appearance-none rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white focus:border-blue-500 focus:outline-none"
          >
            <option value="">{{ t('playground.toolOff') }}</option>
            <option v-for="tool in availableTools" :key="tool.id" :value="tool.id">
              {{ tool.label }}
            </option>
          </select>
        </div>
      </template>

      <div v-else-if="form.mode === 'image'" class="space-y-4">
        <label class="block">
          <span class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
            t('playground.imageSize')
          }}</span>
          <select
            v-model="form.imageSize"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white"
          >
            <option value="auto">Auto</option>
            <option value="1024x1024">1024 × 1024</option>
            <option value="1536x1024">1536 × 1024</option>
            <option value="1024x1536">1024 × 1536</option>
          </select>
        </label>
        <label class="block">
          <span class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
            t('playground.imageQuality')
          }}</span>
          <select
            v-model="form.imageQuality"
            class="w-full rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white"
          >
            <option value="auto">Auto</option>
            <option value="low">1K</option>
            <option value="medium">2K</option>
            <option value="high">4K</option>
          </select>
        </label>
      </div>

      <div v-else-if="form.mode === 'speech'">
        <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
          t('playground.voice')
        }}</label>
        <select
          v-model="form.voice"
          class="w-full rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white"
        >
          <option v-for="voice in voices" :key="voice" :value="voice">{{ voice }}</option>
        </select>
      </div>

      <div v-if="form.mode === 'text' || form.mode === 'speech'">
        <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">{{
          t('playground.system')
        }}</label>
        <textarea
          v-model="form.system"
          class="h-24 w-full resize-none rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white focus:border-blue-500 focus:outline-none"
          :placeholder="t('playground.systemPlaceholder')"
        ></textarea>
      </div>

      <div>
        <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">API</label>
        <code class="block break-all text-xs text-blue-400">{{ endpoint }}</code>
      </div>

      <div>
        <label class="mb-2 block text-xs font-bold text-gray-500 uppercase">API Key</label>
        <input
          v-model="form.apiKey"
          class="w-full rounded border border-[#30363d] bg-[#0d1117] px-2 py-1 text-xs text-white"
          type="password"
        />
      </div>

      <label v-if="form.mode === 'text'" class="flex items-center justify-between">
        <span class="text-xs font-bold text-gray-500 uppercase">{{ t('playground.stream') }}</span>
        <input v-model="form.stream" class="h-4 w-4 accent-blue-600" type="checkbox" />
      </label>

      <div class="border-t border-[#30363d] pt-4">
        <button
          class="w-full rounded border border-[#30363d] py-2 text-xs text-red-400 transition hover:bg-red-900/20"
          type="button"
          @click="clearConversation"
        >
          {{ t('playground.clear') }}
        </button>
      </div>
    </aside>
  </section>
</template>
