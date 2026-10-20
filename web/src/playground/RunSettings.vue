<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref, watch } from 'vue'
import { useI18n, type TranslationKey } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import GetCodeDialog from './GetCodeDialog.vue'
import JsonEditorDialog from './JsonEditorDialog.vue'
import ModelCard from './ModelCard.vue'
import RunSettingsFrame from './RunSettingsFrame.vue'
import SafetyDialog from './SafetyDialog.vue'
import SystemInstructions from './SystemInstructions.vue'
import ThinkingBudgetSetting from './ThinkingBudgetSetting.vue'
import { hasCode, thinkingLevelLabels, thinkingLevelTooltips } from './catalog'
import { protocolOptions, protocolsFor, protocolSupports, type ProtocolSetting } from './protocols'
import { defaultSettings, outputLimit, useCompare, usePlayground } from './session'
import { useTools } from './tools'
import type { ImageOutput, ImageQuality, MediaResolution } from './types'
import type { IconName } from '@/playground/ui/icons'

defineProps<{ open: boolean; narrow: boolean; title?: string | undefined }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const playground = usePlayground()
const { session, currentModel } = playground
const { panes, comparing, syncInstructions, setSync } = useCompare()
// paneIndex 是本运行设置所属窗格的序号
const paneIndex = computed(() => panes.findIndex((pane) => pane.session === session))
const { tools, setTool } = useTools()

const toolsOpen = ref(true)
const advancedOpen = ref(false)
const systemOpen = ref(false)
const safetyOpen = ref(false)
const schemaOpen = ref(false)
const functionsOpen = ref(false)
const codeOpen = ref(false)
const stopDraft = ref('')

const settings = computed(() => session.settings)
const model = computed(() => currentModel.value)
const can = (code: number) => hasCode(model.value, code)
// supports 判断所选协议能否表达某项设置
const supports = (setting: ProtocolSetting) => protocolSupports(settings.value.protocol, setting)
// protocolTip 是所选协议不支持某项设置时的说明
const protocolTip = computed(() =>
  t('pg.protocolUnsupported').replace(
    '{protocol}',
    protocolOptions.find((item) => item.value === settings.value.protocol)?.label ?? '',
  ),
)
// settingTip 返回设置项的说明，所选协议不支持时说明原因
const settingTip = (setting: ProtocolSetting, tip: string) =>
  supports(setting) ? tip : protocolTip.value
const mediaResolutions: { value: MediaResolution; label: string }[] = [
  { value: '', label: 'pg.default' },
  { value: 'MEDIA_RESOLUTION_LOW', label: 'pg.low' },
  { value: 'MEDIA_RESOLUTION_MEDIUM', label: 'pg.medium' },
  { value: 'MEDIA_RESOLUTION_HIGH', label: 'pg.high' },
]
// imageModel 表示所选模型输出图片
const imageModel = computed(() => can(21))
// videoOptions 是 Omni 模型目录中的视频输出选项，没有视频任务的模型为 undefined
const videoOptions = computed(() => {
  const options = model.value?.capability_options
  if (!options?.video_tasks) return undefined
  const range = Object.fromEntries(
    (options.video_duration_range ?? []).map((item) => {
      const [name = '', value = ''] = item.split('=')
      return [name, Number(value)]
    }),
  )
  const { min = 0, max = 0, step = 1 } = range
  const initial = range.default ?? max
  return {
    aspectRatios: options.video_aspect_ratios ?? [],
    resolutions: options.video_output_resolutions ?? [],
    frameRates: options.video_frame_rates ?? [],
    tasks: Object.keys(videoTaskLabels).filter((task) => options.video_tasks?.includes(task)),
    duration: { min, max, step, initial },
  }
})
// videoTaskLabels 是视频任务在官网下拉框中的名称
const videoTaskLabels: Record<string, TranslationKey> = {
  text_to_video: 'pg.videoTaskText',
  image_to_video: 'pg.videoTaskImage',
  reference_to_video: 'pg.videoTaskReference',
  edit: 'pg.videoTaskEdit',
  extend: 'pg.videoTaskExtend',
}

// setVideoDuration 设置视频时长，与默认值相同时不发送
function setVideoDuration(value: number): void {
  if (!videoOptions.value) return
  const { min, max, initial } = videoOptions.value.duration
  const seconds = Math.min(max, Math.max(min, Math.round(value)))
  settings.value.videoDuration = seconds === initial ? null : seconds
}
// imagesProtocol 表示以 OpenAI Images 协议发送，只显示该协议的图片参数
const imagesProtocol = computed(() => settings.value.protocol === 'openai-images')
// imageOutputs 是官网输出格式切换组的选项
const imageOutputs: { value: ImageOutput; label: string; icon: IconName }[] = [
  { value: 'IMAGE_TEXT', label: 'pg.imagesAndText', icon: 'imagesAndText' },
  { value: 'IMAGE', label: 'pg.imagesOnly', icon: 'modeImage' },
]
// aspectRatioOrder 是官网宽高比下拉的顺序
const aspectRatioOrder = [
  '1:1',
  '9:16',
  '16:9',
  '3:4',
  '4:3',
  '3:2',
  '2:3',
  '5:4',
  '4:5',
  '21:9',
  '4:1',
  '1:4',
  '8:1',
  '1:8',
]
// aspectRatios 是模型支持的宽高比，按官网顺序排列
const aspectRatios = computed(() => {
  const ratios = model.value?.capability_options?.image_aspect_ratios ?? []
  const rank = (ratio: string) => {
    const index = aspectRatioOrder.indexOf(ratio)
    return index === -1 ? aspectRatioOrder.length : index
  }
  return [...ratios].sort((first, second) => rank(first) - rank(second))
})
// imageSizes 是模型支持的分辨率，按像素从小到大排列
const imageSizes = computed(() =>
  [...(model.value?.capability_options?.image_output_resolutions ?? [])].sort(
    (first, second) =>
      Number.parseFloat(first) * (first.endsWith('K') ? 1024 : 1) -
      Number.parseFloat(second) * (second.endsWith('K') ? 1024 : 1),
  ),
)
// openAISizes 是 OpenAI Images 协议常用的图片尺寸
const openAISizes = ['auto', '1024x1024', '1536x1024', '1024x1536', '1792x1024', '1024x1792']
// imageQualities 是 OpenAI Images 协议的质量选项
const imageQualities: { value: ImageQuality; label: string }[] = [
  { value: 'auto', label: 'playground.auto' },
  { value: 'low', label: 'pg.low' },
  { value: 'medium', label: 'pg.medium' },
  { value: 'high', label: 'pg.high' },
]

// systemSubtitle 是系统指令卡片的副标题
const systemSubtitle = computed(
  () => settings.value.systemInstruction.trim().split('\n')[0] || t('pg.systemHint'),
)
// temperatureTooltip 是温度滑块的说明
const temperatureTooltip = computed(() => {
  if (can(27)) return t('pg.temperatureFixed')
  return /gemini-3/.test(model.value?.id ?? '')
    ? t('pg.temperatureGemini3')
    : t('pg.temperatureTip')
})

// numberInput 把数值输入框内容换成设置值，空值时为 null
function numberInput(event: Event): number | null {
  const value = (event.target as HTMLInputElement).value
  return value === '' ? null : Number(value)
}

// setOutputLength 按官网规则把输出长度限制为 1 到模型上限之间的整数，清空时恢复模型默认值
function setOutputLength(event: Event): void {
  const input = event.target as HTMLInputElement
  const value = numberInput(event)
  const limit = outputLimit(model.value) ?? Number.MAX_SAFE_INTEGER
  settings.value.maxOutputTokens =
    value === null
      ? (model.value?.output_token_limit ?? null)
      : Math.min(Math.max(Math.round(value), 1), limit)
  input.value = String(settings.value.maxOutputTokens ?? '')
}

// setTopK 按官网规则只保留正整数的 Top K，其他输入清空
function setTopK(event: Event): void {
  const input = event.target as HTMLInputElement
  const value = numberInput(event)
  settings.value.topK = value !== null && value > 0 && Number.isInteger(value) ? value : null
  input.value = String(settings.value.topK ?? '')
}

// addStop 把输入的停止序列加入列表
function addStop(): void {
  const value = stopDraft.value
  if (value !== '' && !settings.value.stopSequences.includes(value))
    settings.value.stopSequences.push(value)
  stopDraft.value = ''
}

// reset 恢复当前模型的默认运行设置，保留系统指令
function reset(): void {
  session.settings = {
    ...defaultSettings(model.value),
    systemInstruction: settings.value.systemInstruction,
  }
}

// defaultResponseSchema 是官网关闭 Schema 编辑器时为空 Schema 填入的默认值
const defaultResponseSchema = `${JSON.stringify(
  { type: 'object', properties: { response: { type: 'string' } } },
  null,
  2,
)}
`

// toggleTool 开启或关闭工具，与官网一样开启结构化输出时打开 Schema 编辑器
function toggleTool(id: (typeof tools.value)[number]['id'], enabled: boolean): void {
  setTool(id, enabled)
  if (id === 'structured_output' && enabled) schemaOpen.value = true
}

watch(schemaOpen, (open) => {
  if (
    !open &&
    settings.value.tools.includes('structured_output') &&
    settings.value.responseSchema.trim() === ''
  )
    settings.value.responseSchema = defaultResponseSchema
})
</script>

<template>
  <RunSettingsFrame
    :open="open"
    :narrow="narrow"
    :title="title"
    @close="emit('close')"
    @code="codeOpen = true"
    @reset="reset"
  >
    <div class="field-group">
      <ModelCard
        :model="model"
        kind="chat"
        :has-content="session.turns.length > 0"
        @select="playground.selectModel($event.id)"
      />
      <div class="system-instructions-panel">
        <button
          v-tooltip.left="can(8) ? '' : t('pg.systemUnsupported')"
          type="button"
          class="settings-card system-instructions-card"
          :class="{ 'disabled-interactive': !can(8) }"
          :aria-label="t('pg.systemInstructions')"
          @click="can(8) && (systemOpen = true)"
        >
          <span class="title">{{ t('pg.systemInstructions') }}</span>
          <span class="subtitle">{{ systemSubtitle }}</span>
        </button>
        <label
          v-if="comparing"
          v-tooltip.below="t('pg.syncInstructionsTip')"
          class="sync-button"
          :class="{ disabled: !can(8) }"
        >
          <input
            type="checkbox"
            :checked="can(8) && syncInstructions"
            :disabled="!can(8)"
            :aria-label="t('pg.syncInstructions')"
            @change="setSync(paneIndex, ($event.target as HTMLInputElement).checked)"
          />
          <span class="sync-checkbox-label">{{ t('pg.sync') }}</span>
        </label>
      </div>
    </div>

    <div v-if="imagesProtocol" class="field-group">
      <div v-tooltip.left="t('pg.openAISizeTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('playground.imageSize') }}</h3>
        <UiSelect
          v-model="settings.openAISize"
          class="w-full"
          :aria-label="t('playground.imageSize')"
        >
          <option v-for="size in openAISizes" :key="size" :value="size">
            {{ size === 'auto' ? t('playground.auto') : size }}
          </option>
        </UiSelect>
      </div>
      <div v-tooltip.left="t('pg.imageQualityTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('playground.imageQuality') }}</h3>
        <UiSelect
          v-model="settings.imageQuality"
          class="w-full"
          :aria-label="t('playground.imageQuality')"
        >
          <option v-for="item in imageQualities" :key="item.value" :value="item.value">
            {{ t(item.label as 'pg.low') }}
          </option>
        </UiSelect>
      </div>
      <div v-tooltip.left="t('pg.imageCountTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.numberOfResults') }}</h3>
        <div class="ms-slider">
          <UiSlider
            v-model="settings.imageCount"
            :min="1"
            :max="4"
            :step="1"
            :label="t('pg.numberOfResults')"
          />
          <input
            ms-input
            class="small slider-number-input"
            type="number"
            min="1"
            max="4"
            step="1"
            :aria-label="t('pg.numberOfResults')"
            :value="settings.imageCount"
            @change="
              settings.imageCount = Math.min(Math.max(Math.round(numberInput($event) ?? 1), 1), 4)
            "
          />
        </div>
      </div>
      <div class="toggle-row">
        <div class="label-container">
          <p class="label">{{ t('playground.stream') }}</p>
        </div>
        <UiSwitch v-model="settings.stream" :label="t('playground.stream')" />
      </div>
    </div>

    <template v-else>
      <div v-if="imageModel" class="field-group">
        <div v-tooltip.left="t('pg.outputFormatTip')" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.outputFormat') }}</h3>
          <div role="radiogroup" class="output-format" :aria-label="t('pg.outputFormat')">
            <button
              v-for="option in imageOutputs"
              :key="option.value"
              type="button"
              role="radio"
              class="output-format-option"
              :class="{ active: settings.imageOutput === option.value }"
              :aria-checked="settings.imageOutput === option.value"
              @click="settings.imageOutput = option.value"
            >
              <UiIcon :name="option.icon" :size="20" />
              <span class="toggle-label">{{ t(option.label as 'pg.imagesOnly') }}</span>
            </button>
          </div>
        </div>
      </div>

      <div
        v-if="can(62) || (imageModel && (aspectRatios.length || imageSizes.length))"
        class="field-group"
      >
        <div v-if="can(62)" v-tooltip.left="temperatureTooltip" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.temperature') }}</h3>
          <div class="ms-slider">
            <UiSlider
              :model-value="settings.temperature ?? model?.temperature ?? 1"
              :min="0"
              :max="model?.max_temperature ?? 1"
              :step="0.05"
              :disabled="can(27)"
              :label="t('pg.temperature')"
              @update:model-value="settings.temperature = $event"
            />
            <input
              ms-input
              class="small slider-number-input"
              type="number"
              :min="0"
              :max="model?.max_temperature ?? 1"
              step="0.05"
              :disabled="can(27)"
              :aria-label="t('pg.temperature')"
              :value="settings.temperature ?? model?.temperature ?? 1"
              @change="settings.temperature = numberInput($event)"
            />
          </div>
        </div>
        <div
          v-if="imageModel && aspectRatios.length"
          v-tooltip.left="t('pg.aspectRatioTip')"
          class="settings-item-column"
        >
          <h3 class="item-description-title">{{ t('pg.aspectRatio') }}</h3>
          <UiSelect v-model="settings.aspectRatio" class="w-full" :aria-label="t('pg.aspectRatio')">
            <option value="">{{ t('playground.auto') }}</option>
            <option v-for="ratio in aspectRatios" :key="ratio" :value="ratio">{{ ratio }}</option>
          </UiSelect>
        </div>
        <div
          v-if="imageModel && imageSizes.length"
          v-tooltip.left="t('pg.resolutionTip')"
          class="settings-item-column"
        >
          <h3 class="item-description-title">{{ t('pg.resolution') }}</h3>
          <UiSelect v-model="settings.imageSize" class="w-full" :aria-label="t('pg.resolution')">
            <option v-for="size in imageSizes" :key="size" :value="size">{{ size }}</option>
          </UiSelect>
        </div>
      </div>

      <div v-if="videoOptions" class="field-group">
        <div class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.aspectRatio') }}</h3>
          <UiSelect
            v-model="settings.videoAspectRatio"
            class="w-full"
            :aria-label="t('pg.aspectRatio')"
          >
            <option value="">{{ t('playground.auto') }}</option>
            <option v-for="ratio in videoOptions.aspectRatios" :key="ratio" :value="ratio">
              {{ ratio }}
            </option>
          </UiSelect>
        </div>
        <div class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.videoDuration') }}</h3>
          <div class="ms-slider">
            <UiSlider
              :model-value="settings.videoDuration ?? videoOptions.duration.initial"
              :min="videoOptions.duration.min"
              :max="videoOptions.duration.max"
              :step="videoOptions.duration.step"
              :label="t('pg.videoDurationSeconds')"
              @update:model-value="setVideoDuration"
            />
            <input
              ms-input
              class="small slider-number-input"
              type="number"
              :min="videoOptions.duration.min"
              :max="videoOptions.duration.max"
              :step="videoOptions.duration.step"
              :aria-label="t('pg.videoDurationSeconds')"
              :value="settings.videoDuration ?? videoOptions.duration.initial"
              @change="setVideoDuration(Number(($event.target as HTMLInputElement).value))"
            />
          </div>
        </div>
        <div v-if="videoOptions.resolutions.length" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.resolution') }}</h3>
          <UiSelect
            v-model="settings.videoResolution"
            class="w-full"
            :disabled="videoOptions.resolutions.length === 1"
            :aria-label="t('pg.videoResolution')"
          >
            <option v-for="size in videoOptions.resolutions" :key="size" :value="size">
              {{ size }}
            </option>
          </UiSelect>
        </div>
        <div v-if="videoOptions.frameRates.length" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.frameRate') }}</h3>
          <UiSelect
            :model-value="videoOptions.frameRates[0] ?? ''"
            class="w-full"
            :disabled="videoOptions.frameRates.length === 1"
            :aria-label="t('pg.videoFrameRate')"
          >
            <option v-for="rate in videoOptions.frameRates" :key="rate" :value="rate">
              {{ rate }}
            </option>
          </UiSelect>
        </div>
      </div>

      <div v-if="can(52) && model?.thinking_levels?.length" class="field-group">
        <div v-tooltip.left="t('pg.thinkingLevelTip')" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.thinkingLevel') }}</h3>
          <UiSelect
            :model-value="settings.thinkingLevel ?? model.default_thinking_level ?? ''"
            class="w-full"
            :aria-label="t('pg.thinkingLevel')"
            @update:model-value="
              settings.thinkingLevel = String($event) as typeof settings.thinkingLevel
            "
          >
            <option
              v-for="level in model.thinking_levels"
              :key="level"
              :value="level"
              :title="thinkingLevelTooltips[level] && t(thinkingLevelTooltips[level])"
            >
              {{ t(thinkingLevelLabels[level]) }}
            </option>
          </UiSelect>
        </div>
      </div>
      <div v-else-if="can(35)" class="field-group">
        <ThinkingBudgetSetting
          v-model="settings.thinkingBudget"
          :config="model?.thinking_budget"
          :disabled="!supports('thinkingBudget')"
          :disabled-tip="protocolTip"
        />
      </div>

      <div v-if="tools.length" class="field-group">
        <button
          type="button"
          class="settings-group-header"
          :class="{ expanded: toolsOpen }"
          :aria-expanded="toolsOpen"
          :aria-label="t('pg.toggleTools')"
          @click="toolsOpen = !toolsOpen"
        >
          <span class="group-title">{{ t('pg.tools') }}</span>
          <UiIcon name="chevronDown" :size="20" class="expand-icon" />
        </button>
        <template v-if="toolsOpen">
          <div
            v-for="tool in tools"
            :key="tool.id"
            v-tooltip.left="
              tool.id === 'structured_output'
                ? settingTip('structuredOutput', t(tool.tooltip))
                : t(tool.tooltip)
            "
            class="toggle-row"
          >
            <div class="label-container">
              <p class="label">{{ t(tool.label) }}</p>
              <p v-if="tool.id === 'google_search' && tool.enabled" class="search-source">
                {{ t('pg.source') }} <UiIcon name="search" :size="12" /> Google Search
              </p>
            </div>
            <div class="actions">
              <button
                v-if="tool.editable"
                type="button"
                ms-button
                class="edit-button ms-button-borderless"
                :aria-label="
                  tool.id === 'structured_output' ? t('pg.editSchema') : t('pg.editFunctions')
                "
                :aria-disabled="!tool.enabled"
                @click="
                  tool.enabled &&
                  (tool.id === 'structured_output' ? (schemaOpen = true) : (functionsOpen = true))
                "
              >
                {{ t('pg.edit') }}
              </button>
              <UiSwitch
                :model-value="tool.enabled"
                :disabled="
                  tool.disabled ||
                  (tool.id === 'structured_output' && !supports('structuredOutput'))
                "
                :label="t(tool.label)"
                @update:model-value="toggleTool(tool.id, $event)"
              />
            </div>
          </div>
        </template>
      </div>

      <div class="field-group advanced-settings">
        <button
          type="button"
          class="settings-group-header"
          :class="{ expanded: advancedOpen }"
          :aria-expanded="advancedOpen"
          :aria-label="t('pg.toggleAdvanced')"
          @click="advancedOpen = !advancedOpen"
        >
          <span class="group-title">{{ t('pg.advancedSettings') }}</span>
          <UiIcon name="chevronDown" :size="20" class="expand-icon" />
        </button>
        <template v-if="advancedOpen">
          <div
            v-if="can(43)"
            v-tooltip.left="settingTip('mediaResolution', t('pg.mediaResolutionTip'))"
            class="settings-item-column"
          >
            <h3 class="item-description-title">{{ t('pg.mediaResolution') }}</h3>
            <UiSelect
              v-model="settings.mediaResolution"
              class="w-full"
              :disabled="!supports('mediaResolution')"
              :aria-label="t('pg.mediaResolution')"
            >
              <option v-for="item in mediaResolutions" :key="item.value" :value="item.value">
                {{ t(item.label as 'pg.default') }}
              </option>
            </UiSelect>
          </div>
          <div
            v-if="can(28)"
            v-tooltip.left="settingTip('safety', t('pg.safetyTip'))"
            class="settings-item"
          >
            <h3 class="item-description-title">{{ t('pg.safetySettings') }}</h3>
            <button
              type="button"
              ms-button
              class="ms-button-borderless"
              :aria-label="t('pg.editSafety')"
              :aria-disabled="!supports('safety')"
              @click="supports('safety') && (safetyOpen = true)"
            >
              {{ t('pg.edit') }}
            </button>
          </div>
          <div
            v-if="can(63)"
            v-tooltip.left="settingTip('stopSequences', t('pg.stopTip'))"
            class="stop-sequences"
          >
            <div class="settings-item">
              <label class="item-description-title" for="stop-sequence">{{
                t('pg.addStop')
              }}</label>
              <input
                id="stop-sequence"
                v-model="stopDraft"
                ms-input
                class="small stop-input"
                :disabled="!supports('stopSequences')"
                :placeholder="t('pg.addStopPlaceholder')"
                @keydown.enter.prevent="addStop"
              />
            </div>
            <div v-if="settings.stopSequences.length" class="chips">
              <span
                v-for="(sequence, index) in settings.stopSequences"
                :key="sequence"
                class="chip"
              >
                <span class="chip-label">{{ sequence }}</span>
                <button
                  type="button"
                  class="chip-remove"
                  :aria-label="t('pg.removeStop')"
                  @click="settings.stopSequences.splice(index, 1)"
                >
                  <UiIcon name="close" :size="14" />
                </button>
              </span>
            </div>
          </div>
          <div
            v-if="can(64)"
            v-tooltip.left="t('pg.outputLengthTip')"
            class="settings-item output-length"
          >
            <h3 class="item-description-title">{{ t('pg.outputLength') }}</h3>
            <input
              ms-input
              class="small"
              type="number"
              min="1"
              step="1"
              :max="outputLimit(model)"
              :aria-label="t('pg.maxOutputTokens')"
              :value="settings.maxOutputTokens ?? outputLimit(model)"
              @change="setOutputLength"
            />
            <p v-if="model?.capabilities?.output_continuation" class="output-length-hint">
              {{ t('pg.outputContinuation') }}
            </p>
          </div>
          <div
            v-if="can(24)"
            v-tooltip.left="settingTip('topK', t('pg.topKTip'))"
            class="settings-item"
          >
            <h3 class="item-description-title">{{ t('pg.topK') }}</h3>
            <input
              ms-input
              class="small number-input"
              type="number"
              min="1"
              step="1"
              :disabled="!supports('topK')"
              :aria-label="t('pg.topK')"
              :value="settings.topK"
              @change="setTopK"
            />
          </div>
          <div v-if="can(23)" v-tooltip.left="t('pg.topPTip')" class="settings-item-column">
            <h3 class="item-description-title">{{ t('pg.topP') }}</h3>
            <div class="ms-slider">
              <UiSlider
                :model-value="settings.topP ?? model?.top_p ?? 0.95"
                :min="0"
                :max="1"
                :step="0.05"
                :label="t('pg.topP')"
                @update:model-value="settings.topP = $event"
              />
              <input
                ms-input
                class="small slider-number-input"
                type="number"
                min="0"
                max="1"
                step="0.05"
                :aria-label="t('pg.topP')"
                :value="settings.topP ?? model?.top_p ?? 0.95"
                @change="settings.topP = numberInput($event)"
              />
            </div>
          </div>
          <div v-if="videoOptions" class="settings-item-column">
            <h3 class="item-description-title">{{ t('pg.videoTask') }}</h3>
            <UiSelect
              v-model="settings.videoTask"
              class="w-full"
              :aria-label="t('pg.videoTaskLabel')"
            >
              <option value="">{{ t('pg.videoTaskUnspecified') }}</option>
              <option v-for="task in videoOptions.tasks" :key="task" :value="task">
                {{ t(videoTaskLabels[task] ?? 'pg.videoTaskUnspecified') }}
              </option>
            </UiSelect>
          </div>
          <div class="settings-item-column protocol-setting">
            <h3 class="item-description-title">{{ t('playground.protocol') }}</h3>
            <UiSelect
              v-model="settings.protocol"
              class="w-full"
              :aria-label="t('playground.protocol')"
            >
              <option v-for="item in protocolsFor(model)" :key="item.value" :value="item.value">
                {{ item.label }}
              </option>
            </UiSelect>
          </div>
          <div class="toggle-row">
            <div class="label-container">
              <p class="label">{{ t('playground.stream') }}</p>
            </div>
            <UiSwitch v-model="settings.stream" :label="t('playground.stream')" />
          </div>
        </template>
      </div>
    </template>
  </RunSettingsFrame>
  <SystemInstructions v-model:open="systemOpen" />
  <SafetyDialog v-model:open="safetyOpen" />
  <JsonEditorDialog
    v-model:open="schemaOpen"
    v-model:text="settings.responseSchema"
    kind="schema"
  />
  <JsonEditorDialog
    v-model:open="functionsOpen"
    v-model:text="settings.functionDeclarations"
    kind="functions"
  />
  <GetCodeDialog v-model:open="codeOpen" />
</template>

<style scoped>
.system-instructions-panel {
  display: grid;
  align-items: start;
  margin-block: 8px;
}

.system-instructions-card,
.sync-button {
  grid-column: 1;
  grid-row: 1;
}

.sync-button {
  display: flex;
  align-items: center;
  justify-self: end;
  height: 40px;
  padding-right: 12px;
  color: var(--color-run-settings-text);
  cursor: pointer;
}

.sync-button input {
  width: 18px;
  height: 18px;
  margin: 11px;
  accent-color: var(--color-v3-outline-accent);
  cursor: inherit;
}

.sync-button.disabled {
  cursor: not-allowed;
  pointer-events: none;
}

.sync-checkbox-label {
  font-size: 12px;
  line-height: 16px;
}

.protocol-setting {
  margin-bottom: 8px;
}

.output-format {
  position: relative;
  display: flex;
  box-sizing: border-box;
  width: 100%;
  padding: 2px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
}

.output-format-option {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 8px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--color-v3-text-var);
  text-align: center;
  cursor: pointer;
  transition:
    color 0.2s ease,
    background-color 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.output-format-option:hover:not(.active) {
  color: var(--color-v3-text);
}

.output-format-option.active {
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text);
}

.toggle-label {
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
}

.system-instructions-card.disabled-interactive {
  background: transparent;
  cursor: not-allowed;
}

.system-instructions-card.disabled-interactive :is(.title, .subtitle) {
  color: var(--color-v3-text-disable);
}

.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  margin-block: 12px;
}

.label-container {
  flex: 1;
  min-width: 0;
}

.label {
  margin: 0;
  font-size: 14px;
  line-height: 21px;
}

.search-source {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 0;
  font-size: 11px;
  line-height: 18px;
}

.actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.edit-button {
  transform: translateY(2px);
}

.stop-sequences {
  margin-bottom: 8px;
}

.stop-input {
  --ms-input-width: 112px;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  max-height: 140px;
  overflow-y: auto;
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 28px;
  padding: 0 4px 0 8px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  font-size: 12px;
  line-height: 18px;
}

.chip-label {
  max-width: 150px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chip-remove {
  display: flex;
  padding: 2px;
  border: 0;
  border-radius: 50%;
  background: none;
  color: inherit;
  cursor: pointer;
}

.output-length {
  --ms-input-width: 150px;
  flex-wrap: wrap;
}

.output-length-hint {
  width: 100%;
  margin: 4px 0 0;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.number-input {
  --ms-input-width: 80px;
}
</style>
