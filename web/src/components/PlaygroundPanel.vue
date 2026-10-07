<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import {
  ApiError,
  playgroundPath,
  playgroundProtocols,
  playgroundSettings,
  runPlayground,
  type PlaygroundChunk,
} from '@/api'
import { useI18n, type TranslationKey } from '@/i18n'
import type { IconName } from '@/icons'
import type {
  Model,
  PlaygroundInput,
  PlaygroundMode,
  PlaygroundProtocol,
  PlaygroundResult,
  PlaygroundTool,
} from '@/types'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'
import UiSwitch from './UiSwitch.vue'

const props = defineProps<{
  models: Model[]
  apiKey: string
}>()

const { t, errorText } = useI18n()
const protocolLabels: Record<PlaygroundProtocol, string> = {
  'openai-chat': 'OpenAI Chat',
  'openai-responses': 'OpenAI Responses',
  anthropic: 'Anthropic Messages',
  gemini: 'Gemini API',
  'openai-images': 'OpenAI Images',
  'openai-speech': 'OpenAI Speech',
  'openai-videos': 'OpenAI Videos',
}
const modes: { id: PlaygroundMode; label?: TranslationKey; icon: IconName }[] = [
  { id: 'text', label: 'playground.modeText', icon: 'chat' },
  { id: 'image', label: 'playground.modeImage', icon: 'modeImage' },
  { id: 'speech', label: 'playground.modeSpeech', icon: 'modeSpeech' },
  { id: 'music', label: 'playground.modeMusic', icon: 'modeMusic' },
  { id: 'video', icon: 'modeVideo' },
]
const tools: { id: PlaygroundTool; label: string; capability: string }[] = [
  { id: 'web_search', label: 'Google Search', capability: 'google_search' },
  { id: 'image_search', label: 'Image Search', capability: 'image_search' },
  { id: 'code_interpreter', label: 'Code Execution', capability: 'code_execution' },
  { id: 'url_context', label: 'URL Context', capability: 'browse' },
  { id: 'google_maps', label: 'Google Maps', capability: 'google_maps' },
]
type NumericSetting = 'temperature' | 'topP' | 'topK' | 'maxOutputTokens' | 'seed'
// numericFields 是生成参数中的数值项，空值使用模型默认值
const numericFields: {
  key: NumericSetting
  label: TranslationKey
  min?: number
  max?: number
  step: number
}[] = [
  { key: 'temperature', label: 'playground.temperature', min: 0, max: 2, step: 0.05 },
  { key: 'topP', label: 'playground.topP', min: 0, max: 1, step: 0.05 },
  { key: 'topK', label: 'playground.topK', min: 1, step: 1 },
  { key: 'maxOutputTokens', label: 'playground.maxOutputTokens', min: 1, step: 1 },
  { key: 'seed', label: 'playground.seed', step: 1 },
]
// pill 是输入框底栏中紧凑选择器的样式
const pill =
  'max-w-48 rounded-md border border-white/10 bg-transparent px-3 py-1 text-xs text-gray-300 transition-colors hover:bg-white/5'
// field 是参数浮层中输入控件的样式
const field =
  'w-full rounded-lg border border-line bg-canvas px-2.5 py-1.5 text-xs text-white focus:border-blue-500 focus:outline-none'

// markdown 渲染回复正文，原始 HTML 按文本转义，链接在新标签页打开
const markdown = new MarkdownIt({ linkify: true, breaks: true })
const renderLink =
  markdown.renderer.rules.link_open ??
  ((tokens, index, options, _env, self) => self.renderToken(tokens, index, options))
markdown.renderer.rules.link_open = (tokens, index, options, env, self) => {
  tokens[index]?.attrSet('target', '_blank')
  tokens[index]?.attrSet('rel', 'noreferrer')
  return renderLink(tokens, index, options, env, self)
}

const form = reactive<PlaygroundInput>({
  mode: 'text',
  protocol: 'openai-chat',
  model: '',
  prompt: '',
  system: '',
  stream: true,
  reasoning: '',
  tools: [],
  temperature: null,
  topP: null,
  topK: null,
  maxOutputTokens: null,
  seed: null,
  stopSequences: [],
  structuredOutput: false,
  jsonSchema: '',
  functionCalling: false,
  functions: '',
  imageSize: 'auto',
  imageQuality: 'auto',
  aspectRatio: '',
  resolution: '',
  videoSeconds: '',
  voice: '',
  speakers: [],
  safety: {},
  mediaResolution: '',
  imageOnly: false,
  apiKey: props.apiKey,
})
const stopDraft = ref('')
// settingsOpen 控制运行设置面板，宽屏默认展开
const settingsOpen = ref(window.matchMedia('(min-width: 1024px)').matches)
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
const promptInput = ref<HTMLTextAreaElement>()
const scroller = ref<HTMLElement>()
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
const supportsMediaResolution = computed(
  () => selectedModel.value?.capabilities?.media_resolution === true,
)
// harmCategories 为官网运行设置的四个安全类别
const harmCategories: { id: string; label: TranslationKey }[] = [
  { id: 'HARM_CATEGORY_HARASSMENT', label: 'playground.harassment' },
  { id: 'HARM_CATEGORY_HATE_SPEECH', label: 'playground.hateSpeech' },
  { id: 'HARM_CATEGORY_SEXUALLY_EXPLICIT', label: 'playground.sexuallyExplicit' },
  { id: 'HARM_CATEGORY_DANGEROUS_CONTENT', label: 'playground.dangerousContent' },
]
// harmThresholds 按官网滑块从关闭到最严格排列
const harmThresholds: { value: string; label: TranslationKey }[] = [
  { value: 'OFF', label: 'playground.blockOff' },
  { value: 'BLOCK_NONE', label: 'playground.blockNone' },
  { value: 'BLOCK_ONLY_HIGH', label: 'playground.blockFew' },
  { value: 'BLOCK_MEDIUM_AND_ABOVE', label: 'playground.blockSome' },
  { value: 'BLOCK_LOW_AND_ABOVE', label: 'playground.blockMost' },
]
const mediaResolutions: { value: string; label: TranslationKey }[] = [
  { value: 'MEDIA_RESOLUTION_LOW', label: 'playground.low' },
  { value: 'MEDIA_RESOLUTION_MEDIUM', label: 'playground.medium' },
  { value: 'MEDIA_RESOLUTION_HIGH', label: 'playground.high' },
]
const settings = computed(() => playgroundSettings(form.mode, form.protocol))
const protocolOptions = computed(() => playgroundProtocols(form.mode))
const numericSettings = computed(() => numericFields.filter((item) => settings.value.has(item.key)))
// modelOptions 读取当前模型在实时目录中的取值列表
function modelOptions(key: string): string[] {
  return selectedModel.value?.capability_options?.[key] ?? []
}
const aspectRatios = computed(() =>
  modelOptions(form.mode === 'video' ? 'video_aspect_ratios' : 'image_aspect_ratios'),
)
const resolutions = computed(() => {
  const values = modelOptions(
    form.mode === 'video' ? 'video_output_resolutions' : 'image_output_resolutions',
  )
  if (form.mode === 'video' && form.videoSeconds !== '' && form.videoSeconds !== '8')
    return values.filter((value) => value === '720p')
  return form.protocol === 'openai-videos'
    ? values.filter((value) => value === '720p' || value === '1080p')
    : values
})
// videoDurations 与官网一致，720p 以上的分辨率只配 8 秒
const videoDurations = computed(() => {
  const values = modelOptions('video_durations_seconds')
  return form.resolution !== '' && form.resolution !== '720p'
    ? values.filter((value) => value === '8')
    : values
})
const multiSpeaker = computed({
  get: () => form.speakers.length > 1,
  set: (enabled: boolean) => {
    form.speakers = enabled
      ? [
          { name: 'Speaker 1', voice: voices.value[0] ?? '' },
          { name: 'Speaker 2', voice: voices.value[1] ?? voices.value[0] ?? '' },
        ]
      : []
  },
})

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

watch(
  () => form.mode,
  () => {
    if (!protocolOptions.value.includes(form.protocol)) form.protocol = protocolOptions.value[0]!
  },
)

watch([selectedModel, () => form.protocol], () => {
  if (!supportsThinking.value) form.reasoning = ''
  form.tools = form.tools.filter((tool) => availableTools.value.some((item) => item.id === tool))
  if (!voices.value.includes(form.voice)) form.voice = voices.value[0] ?? ''
  for (const speaker of form.speakers) {
    if (!voices.value.includes(speaker.voice)) speaker.voice = voices.value[0] ?? ''
  }
  if (!aspectRatios.value.includes(form.aspectRatio)) form.aspectRatio = ''
  if (!resolutions.value.includes(form.resolution)) form.resolution = ''
  if (!videoDurations.value.includes(form.videoSeconds)) form.videoSeconds = ''
})

// numberValue 把数值输入框内容转换为参数，空值表示使用模型默认值
function numberValue(event: Event): number | null {
  const value = (event.target as HTMLInputElement).value
  return value === '' ? null : Number(value)
}

// numberPlaceholder 显示数值参数省略时实际使用的值
function numberPlaceholder(key: NumericSetting): string {
  if (key !== 'maxOutputTokens') return t('playground.default')
  if (form.protocol === 'anthropic') return '1024'
  return String(selectedModel.value?.output_token_limit ?? t('playground.default'))
}

// addStopSequence 把输入框中的文本加入停止序列
function addStopSequence(): void {
  const value = stopDraft.value
  if (value !== '' && !form.stopSequences.includes(value)) form.stopSequences.push(value)
  stopDraft.value = ''
}

// toggleTool 开启或关闭一个内置工具
function toggleTool(tool: PlaygroundTool, enabled: boolean): void {
  form.tools = enabled ? [...form.tools, tool] : form.tools.filter((item) => item !== tool)
}

watch(
  () => props.apiKey,
  (apiKey) => {
    form.apiKey = apiKey
  },
)

const endpoint = computed(() => playgroundPath(form))

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
const renderedText = computed(() => markdown.render(result.text))

// resizePrompt 让输入框随内容增高，超过上限后在框内滚动
function resizePrompt(): void {
  const input = promptInput.value
  if (input === undefined) return
  input.style.height = 'auto'
  input.style.height = `${input.scrollHeight}px`
}

watch(
  () => form.prompt,
  () => void nextTick(resizePrompt),
)

// followOutput 在阅读位置接近底部时随新输出滚动
watch(
  () => [result.text.length, result.reasoning.length, result.media.length, result.raw.length],
  () => {
    const element = scroller.value
    if (element === undefined) return
    const nearBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 120
    if (nearBottom) void nextTick(() => element.scrollTo({ top: element.scrollHeight }))
  },
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
  if (isRunning.value || form.model === '' || form.prompt.trim() === '') return
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
    result.raw = errorText(error)
    outputMode.value = 'raw'
  } finally {
    result.durationMs = Math.round(performance.now() - started)
    isRunning.value = false
  }
}

// submitOnEnter 以 Enter 发送，Shift + Enter 与输入法组字时保留换行和候选
function submitOnEnter(event: KeyboardEvent): void {
  if (event.shiftKey || event.isComposing) return
  event.preventDefault()
  void execute()
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
  <section class="flex min-h-0 flex-1 bg-canvas">
    <div class="flex min-w-0 flex-1 flex-col">
      <header class="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3 md:px-4">
        <div
          class="flex min-w-0 items-center gap-1 overflow-x-auto"
          role="group"
          :aria-label="t('playground.mode')"
        >
          <button
            v-for="mode in modes"
            :key="mode.id"
            type="button"
            :aria-pressed="form.mode === mode.id"
            class="flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1 text-xs transition-colors"
            :class="
              form.mode === mode.id
                ? 'bg-white/10 text-white'
                : 'text-gray-400 hover:bg-white/5 hover:text-gray-200'
            "
            @click="form.mode = mode.id"
          >
            <UiIcon :name="mode.icon" :size="13" />
            {{ modeLabel(mode) }}
          </button>
        </div>
        <div class="hidden min-w-0 flex-1 justify-end sm:flex">
          <code
            class="min-w-0 truncate rounded-md border border-line px-2 py-1 font-mono text-[11px] text-gray-400"
            ><span class="text-blue-400">POST</span> {{ endpoint }}</code
          >
        </div>
        <button
          v-tooltip="t('playground.clear')"
          type="button"
          class="shrink-0 rounded-md p-1.5 text-gray-400 transition-colors hover:bg-white/5 hover:text-white disabled:opacity-40 max-sm:ml-auto"
          :aria-label="t('playground.clear')"
          :disabled="!hasRun"
          @click="clearConversation"
        >
          <UiIcon name="newChat" :size="16" />
        </button>
      </header>

      <div ref="scroller" class="min-h-0 flex-1 overflow-y-auto">
        <div
          v-if="!hasRun"
          class="flex h-full flex-col items-center justify-center px-6 text-center"
        >
          <h2 class="text-2xl font-semibold text-gray-100">{{ t('playground.emptyTitle') }}</h2>
          <p class="mt-2 max-w-md text-sm text-gray-500">
            {{ t('playground.emptyHelp').replace('{endpoint}', endpoint) }}
          </p>
        </div>

        <div v-else class="mx-auto w-full max-w-3xl space-y-6 px-4 pt-6 pb-10">
          <div class="flex justify-end">
            <div
              class="max-w-[85%] rounded-2xl bg-white/[0.07] px-4 py-2.5 text-sm leading-6 break-words whitespace-pre-wrap text-gray-100"
              v-text="submittedPrompt"
            ></div>
          </div>

          <article class="space-y-3" :aria-busy="isRunning">
            <details v-if="result.reasoning" class="group/thinking" open>
              <summary
                class="inline-flex cursor-pointer list-none items-center gap-1.5 rounded-md px-2 py-1 text-xs text-gray-400 transition-colors select-none hover:bg-white/5 hover:text-gray-200"
              >
                <UiIcon name="reasoning" :size="13" />
                {{ t('playground.reasoningOutput') }}
                <UiIcon
                  name="chevronRight"
                  :size="12"
                  class="transition-transform group-open/thinking:rotate-90"
                />
              </summary>
              <div
                class="mt-2 border-l-2 border-line pl-4 text-[13px] leading-6 whitespace-pre-wrap text-gray-400"
                v-text="result.reasoning"
              ></div>
            </details>

            <pre
              v-if="outputMode === 'raw'"
              class="overflow-x-auto rounded-xl border border-line bg-panel p-4 font-mono text-xs leading-5 whitespace-pre-wrap text-gray-300"
              >{{ result.raw }}</pre>
            <template v-else>
              <div
                v-if="result.text"
                class="markdown text-[15px] leading-7 break-words text-gray-100"
                v-html="renderedText"
              ></div>
              <div v-else-if="isRunning && !hasOutput" class="flex gap-1 py-2" aria-hidden="true">
                <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-gray-500"></span>
                <span
                  class="h-1.5 w-1.5 animate-pulse rounded-full bg-gray-500 [animation-delay:150ms]"
                ></span>
                <span
                  class="h-1.5 w-1.5 animate-pulse rounded-full bg-gray-500 [animation-delay:300ms]"
                ></span>
              </div>
            </template>

            <details v-if="result.tools" class="group/tools">
              <summary
                class="inline-flex cursor-pointer list-none items-center gap-1.5 rounded-md px-2 py-1 text-xs text-gray-400 transition-colors select-none hover:bg-white/5 hover:text-gray-200"
              >
                <UiIcon name="tool" :size="13" />
                {{ t('playground.toolOutput') }}
                <UiIcon
                  name="chevronRight"
                  :size="12"
                  class="transition-transform group-open/tools:rotate-90"
                />
              </summary>
              <pre
                class="mt-2 overflow-x-auto rounded-xl border border-line bg-panel p-4 font-mono text-xs whitespace-pre-wrap text-gray-400"
                >{{ result.tools }}</pre>
            </details>

            <div v-if="result.media.length" class="space-y-3">
              <template v-for="(media, index) in result.media" :key="`${media.url}:${index}`">
                <img
                  v-if="media.mime.startsWith('image/')"
                  :src="media.url"
                  alt=""
                  class="max-h-[560px] max-w-full rounded-xl border border-line object-contain"
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
                  class="max-h-[560px] w-full rounded-xl border border-line"
                  controls
                ></video>
              </template>
            </div>

            <div v-if="!isRunning" class="flex min-w-0 items-center gap-0.5 text-xs text-gray-500">
              <button
                v-tooltip="copied ? t('common.copied') : t('common.copy')"
                type="button"
                class="rounded-md p-1.5 transition-colors hover:bg-white/5 hover:text-gray-200 disabled:opacity-40"
                :aria-label="copied ? t('common.copied') : t('common.copy')"
                :disabled="visibleOutput === ''"
                @click="copyOutput"
              >
                <UiIcon :name="copied ? 'check' : 'copy'" :size="14" />
              </button>
              <button
                v-tooltip="t('playground.raw')"
                type="button"
                class="rounded-md p-1.5 transition-colors hover:bg-white/5 hover:text-gray-200"
                :class="{ 'bg-white/10 text-gray-200': outputMode === 'raw' }"
                :aria-label="t('playground.raw')"
                :aria-pressed="outputMode === 'raw'"
                @click="outputMode = outputMode === 'output' ? 'raw' : 'output'"
              >
                <UiIcon name="raw" :size="14" />
              </button>
              <span class="ml-1.5 truncate tabular-nums">
                {{ submittedModel }}
                <template v-if="result.status"> · HTTP {{ result.status }}</template>
                <template v-if="result.durationMs"> · {{ result.durationMs }} ms</template>
              </span>
            </div>
          </article>
        </div>
      </div>

      <div class="shrink-0 px-3 pb-3 md:px-4 md:pb-4">
        <form
          class="mx-auto w-full max-w-3xl rounded-3xl border border-white/[0.08] bg-panel shadow-lg shadow-black/30 transition-colors focus-within:border-white/15"
          @submit.prevent="execute"
        >
          <textarea
            ref="promptInput"
            v-model="form.prompt"
            rows="1"
            class="block max-h-60 min-h-[52px] w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-sm leading-6 text-gray-100 placeholder:text-gray-500 focus:outline-none"
            :aria-label="t('playground.placeholder')"
            :placeholder="t('playground.placeholder')"
            @keydown.enter="submitOnEnter"
          ></textarea>
          <div class="flex flex-wrap items-center gap-1.5 px-2.5 pb-2.5">
            <UiSelect v-model="form.model" :aria-label="t('playground.model')" :class="pill">
              <option v-for="model in availableModels" :key="model.id" :value="model.id">
                {{ model.name }}
              </option>
            </UiSelect>
            <UiSelect
              v-if="settings.has('reasoning') && supportsThinking"
              v-model="form.reasoning"
              :aria-label="t('playground.reasoning')"
              :class="pill"
            >
              <option value="">
                {{ t('playground.reasoning') }} · {{ t('playground.reasoningOff') }}
              </option>
              <option value="minimal">
                {{ t('playground.reasoning') }} · {{ t('playground.reasoningMinimal') }}
              </option>
              <option value="low">
                {{ t('playground.reasoning') }} · {{ t('playground.reasoningLow') }}
              </option>
              <option value="medium">
                {{ t('playground.reasoning') }} · {{ t('playground.reasoningMedium') }}
              </option>
              <option value="high">
                {{ t('playground.reasoning') }} · {{ t('playground.reasoningHigh') }}
              </option>
            </UiSelect>
            <template v-if="settings.has('imageSize')">
              <UiSelect
                v-model="form.imageSize"
                :aria-label="t('playground.imageSize')"
                :class="pill"
              >
                <option value="auto">
                  {{ t('playground.imageSize') }} · {{ t('playground.auto') }}
                </option>
                <option value="1024x1024">1024 × 1024</option>
                <option value="1536x1024">1536 × 1024</option>
                <option value="1024x1536">1024 × 1536</option>
              </UiSelect>
              <UiSelect
                v-model="form.imageQuality"
                :aria-label="t('playground.imageQuality')"
                :class="pill"
              >
                <option value="auto">
                  {{ t('playground.imageQuality') }} · {{ t('playground.auto') }}
                </option>
                <option value="low">1K</option>
                <option value="medium">2K</option>
                <option value="high">4K</option>
              </UiSelect>
            </template>
            <UiSelect
              v-if="settings.has('aspectRatio') && aspectRatios.length > 0"
              v-model="form.aspectRatio"
              :aria-label="t('playground.aspectRatio')"
              :class="pill"
            >
              <option value="">
                {{ t('playground.aspectRatio') }} · {{ t('playground.auto') }}
              </option>
              <option v-for="ratio in aspectRatios" :key="ratio" :value="ratio">{{ ratio }}</option>
            </UiSelect>
            <UiSelect
              v-if="settings.has('resolution') && resolutions.length > 0"
              v-model="form.resolution"
              :aria-label="t('playground.resolution')"
              :class="pill"
            >
              <option value="">
                {{ t('playground.resolution') }} · {{ t('playground.default') }}
              </option>
              <option v-for="value in resolutions" :key="value" :value="value">{{ value }}</option>
            </UiSelect>
            <UiSelect
              v-if="settings.has('videoSeconds') && videoDurations.length > 0"
              v-model="form.videoSeconds"
              :aria-label="t('playground.duration')"
              :class="pill"
            >
              <option value="">
                {{ t('playground.duration') }} · {{ t('playground.default') }}
              </option>
              <option v-for="value in videoDurations" :key="value" :value="value">
                {{ value }} s
              </option>
            </UiSelect>
            <UiSelect
              v-if="settings.has('voice') && voices.length > 0 && !multiSpeaker"
              v-model="form.voice"
              :aria-label="t('playground.voice')"
              :class="pill"
            >
              <option v-for="voice in voices" :key="voice" :value="voice">{{ voice }}</option>
            </UiSelect>

            <button
              v-tooltip="t('playground.options')"
              type="button"
              class="rounded-md border border-white/10 p-1.5 text-gray-400 transition-colors hover:bg-white/5 hover:text-gray-200"
              :class="{ 'bg-white/10 text-white': settingsOpen }"
              :aria-label="t('playground.options')"
              :aria-pressed="settingsOpen"
              @click="settingsOpen = !settingsOpen"
            >
              <UiIcon name="options" :size="14" />
            </button>

            <div class="ml-auto flex items-center gap-2">
              <span class="hidden text-[11px] text-gray-600 sm:inline">
                {{ t('playground.enterHint') }}
              </span>
              <button
                v-if="isRunning"
                type="button"
                class="flex h-8 w-8 items-center justify-center rounded-md bg-gray-200 text-gray-900 transition-colors hover:bg-white"
                :aria-label="t('playground.stop')"
                @click="stop"
              >
                <UiIcon name="stop" :size="12" class="fill-current" />
              </button>
              <button
                v-else
                type="submit"
                class="flex h-8 w-8 items-center justify-center rounded-md bg-blue-600 text-white transition-colors hover:bg-blue-500 disabled:bg-white/10 disabled:text-gray-500"
                :aria-label="t('playground.send')"
                :disabled="form.model === '' || form.prompt.trim() === ''"
              >
                <UiIcon name="send" :size="16" />
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
    <div
      v-if="settingsOpen"
      class="fixed inset-0 z-40 bg-black/50 lg:hidden"
      aria-hidden="true"
      @click="settingsOpen = false"
    ></div>
    <aside
      v-if="settingsOpen"
      :aria-label="t('playground.options')"
      class="fixed inset-y-0 right-0 z-50 flex w-[min(22rem,100vw)] flex-col border-l border-line bg-panel shadow-xl shadow-black/40 lg:static lg:z-auto lg:w-80 lg:shrink-0 lg:shadow-none"
    >
      <div class="flex h-12 shrink-0 items-center justify-between border-b border-line px-4">
        <h2 class="text-sm font-medium text-gray-200">{{ t('playground.options') }}</h2>
        <button
          type="button"
          class="rounded-md p-1.5 text-gray-400 transition-colors hover:bg-white/5 hover:text-white"
          :aria-label="t('common.close')"
          @click="settingsOpen = false"
        >
          <UiIcon name="close" :size="14" />
        </button>
      </div>
      <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 text-xs">
        <section class="space-y-2.5">
          <label v-if="protocolOptions.length > 1" class="block space-y-1.5">
            <span class="text-gray-400">{{ t('playground.protocol') }}</span>
            <UiSelect v-model="form.protocol" :class="field">
              <option v-for="protocol in protocolOptions" :key="protocol" :value="protocol">
                {{ protocolLabels[protocol] }}
              </option>
            </UiSelect>
          </label>
          <div v-if="settings.has('stream')" class="flex items-center justify-between">
            <label for="playground-stream" class="text-gray-400">
              {{ t('playground.stream') }}
            </label>
            <UiSwitch id="playground-stream" v-model="form.stream" />
          </div>
          <div v-if="settings.has('imageOutput')" class="flex items-center justify-between">
            <label for="playground-image-only" class="text-gray-400">
              {{ t('playground.imageOnly') }}
            </label>
            <UiSwitch id="playground-image-only" v-model="form.imageOnly" />
          </div>
          <label v-if="settings.has('system')" class="block space-y-1.5">
            <span class="text-gray-400">{{ t('playground.system') }}</span>
            <textarea
              v-model="form.system"
              :class="field"
              class="h-20 resize-y"
              :placeholder="t('playground.systemPlaceholder')"
            ></textarea>
          </label>
        </section>

        <section
          v-if="numericSettings.length > 0 || settings.has('stopSequences')"
          class="space-y-2.5 border-t border-line pt-3"
        >
          <h3 class="font-medium text-gray-300">{{ t('playground.generation') }}</h3>
          <div class="grid grid-cols-2 gap-2.5">
            <label v-for="item in numericSettings" :key="item.key" class="space-y-1.5">
              <span class="text-gray-400">{{ t(item.label) }}</span>
              <input
                type="number"
                :min="item.min"
                :max="item.max"
                :step="item.step"
                :value="form[item.key] ?? ''"
                :placeholder="numberPlaceholder(item.key)"
                :class="field"
                @input="form[item.key] = numberValue($event)"
              />
            </label>
          </div>
          <div v-if="settings.has('stopSequences')" class="space-y-1.5">
            <label for="playground-stop" class="text-gray-400">
              {{ t('playground.stopSequences') }}
            </label>
            <div v-if="form.stopSequences.length > 0" class="flex flex-wrap gap-1.5">
              <span
                v-for="(value, index) in form.stopSequences"
                :key="value"
                class="inline-flex max-w-full items-center gap-1 rounded-md bg-white/[0.07] py-0.5 pr-0.5 pl-2 font-mono text-gray-200"
              >
                <span class="truncate">{{ value }}</span>
                <button
                  type="button"
                  class="rounded-sm p-0.5 text-gray-500 hover:bg-white/10 hover:text-white"
                  :aria-label="t('playground.removeStop')"
                  @click="form.stopSequences.splice(index, 1)"
                >
                  <UiIcon name="close" :size="10" />
                </button>
              </span>
            </div>
            <input
              id="playground-stop"
              v-model="stopDraft"
              :class="field"
              :placeholder="t('playground.stopPlaceholder')"
              @keydown.enter.prevent="addStopSequence"
            />
          </div>
        </section>

        <section
          v-if="
            (settings.has('tools') && availableTools.length > 0) ||
            settings.has('structuredOutput') ||
            settings.has('functionCalling')
          "
          class="space-y-2.5 border-t border-line pt-3"
        >
          <h3 class="font-medium text-gray-300">{{ t('playground.tool') }}</h3>
          <template v-if="settings.has('tools')">
            <div
              v-for="tool in availableTools"
              :key="tool.id"
              class="flex items-center justify-between"
            >
              <label :for="`playground-tool-${tool.id}`" class="text-gray-400">
                {{ tool.label }}
              </label>
              <UiSwitch
                :id="`playground-tool-${tool.id}`"
                :model-value="form.tools.includes(tool.id)"
                @update:model-value="toggleTool(tool.id, $event)"
              />
            </div>
          </template>
          <template v-if="settings.has('structuredOutput')">
            <div class="flex items-center justify-between">
              <label for="playground-structured" class="text-gray-400">
                {{ t('playground.structuredOutput') }}
              </label>
              <UiSwitch id="playground-structured" v-model="form.structuredOutput" />
            </div>
            <textarea
              v-if="form.structuredOutput"
              v-model="form.jsonSchema"
              :class="field"
              class="h-28 resize-y font-mono"
              aria-label="JSON Schema"
              placeholder='{"type": "object", "properties": {}}'
              spellcheck="false"
            ></textarea>
          </template>
          <template v-if="settings.has('functionCalling')">
            <div class="flex items-center justify-between">
              <label for="playground-functions" class="text-gray-400">
                {{ t('playground.functionCalling') }}
              </label>
              <UiSwitch id="playground-functions" v-model="form.functionCalling" />
            </div>
            <textarea
              v-if="form.functionCalling"
              v-model="form.functions"
              :class="field"
              class="h-28 resize-y font-mono"
              :aria-label="t('playground.functionCalling')"
              :placeholder="t('playground.functionsPlaceholder')"
              spellcheck="false"
            ></textarea>
          </template>
        </section>

        <section
          v-if="
            settings.has('safety') || (settings.has('mediaResolution') && supportsMediaResolution)
          "
          class="space-y-2.5 border-t border-line pt-3"
        >
          <label
            v-if="settings.has('mediaResolution') && supportsMediaResolution"
            class="block space-y-1.5"
          >
            <span class="text-gray-400">{{ t('playground.mediaResolution') }}</span>
            <UiSelect v-model="form.mediaResolution" :class="field">
              <option value="">{{ t('playground.default') }}</option>
              <option v-for="item in mediaResolutions" :key="item.value" :value="item.value">
                {{ t(item.label) }}
              </option>
            </UiSelect>
          </label>
          <template v-if="settings.has('safety')">
            <h3 class="font-medium text-gray-300">{{ t('playground.safety') }}</h3>
            <label
              v-for="category in harmCategories"
              :key="category.id"
              class="grid grid-cols-[1fr_minmax(0,9rem)] items-center gap-2"
            >
              <span class="text-gray-400">{{ t(category.label) }}</span>
              <UiSelect
                :model-value="form.safety[category.id] ?? 'OFF'"
                :class="field"
                :aria-label="t(category.label)"
                @update:model-value="form.safety[category.id] = String($event)"
              >
                <option v-for="item in harmThresholds" :key="item.value" :value="item.value">
                  {{ t(item.label) }}
                </option>
              </UiSelect>
            </label>
          </template>
        </section>

        <section
          v-if="settings.has('speakers') && voices.length > 0"
          class="space-y-2.5 border-t border-line pt-3"
        >
          <div class="flex items-center justify-between">
            <label for="playground-speakers" class="text-gray-400">
              {{ t('playground.multiSpeaker') }}
            </label>
            <UiSwitch id="playground-speakers" v-model="multiSpeaker" />
          </div>
          <div
            v-for="(speaker, index) in form.speakers"
            :key="index"
            class="grid grid-cols-2 gap-2"
          >
            <input
              v-model="speaker.name"
              :class="field"
              :aria-label="`${t('playground.speaker')} ${index + 1}`"
            />
            <UiSelect
              v-model="speaker.voice"
              :class="field"
              :aria-label="`${t('playground.voice')} ${index + 1}`"
            >
              <option v-for="voice in voices" :key="voice" :value="voice">
                {{ voice }}
              </option>
            </UiSelect>
          </div>
        </section>

        <section class="space-y-2.5 border-t border-line pt-3">
          <label class="block space-y-1.5">
            <span class="text-gray-400">API Key</span>
            <input v-model="form.apiKey" :class="field" type="password" autocomplete="off" />
          </label>
          <p class="font-mono break-all text-gray-500">
            <span class="text-blue-400">POST</span> {{ endpoint }}
          </p>
        </section>
      </div>
    </aside>
  </section>
</template>
