<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref } from 'vue'
import {
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import type { TranslationKey } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import JsonEditorDialog from '../JsonEditorDialog.vue'
import ModelCard from '../ModelCard.vue'
import RunSettingsFrame from '../RunSettingsFrame.vue'
import { thinkingLevelLabels } from '../catalog'
import LiveSystemInstructions from './LiveSystemInstructions.vue'
import LiveVoiceSelect from './LiveVoiceSelect.vue'
import { minContext, useLive } from './live'

defineProps<{ open: boolean; narrow: boolean }>()
const emit = defineEmits<{ close: []; code: [] }>()
const { t } = useI18n()
const live = useLive()
const { state, currentModel, variant } = live
const settings = computed(() => state.settings)
const systemOpen = ref(false)
const functionsOpen = ref(false)
const contextOpen = ref(false)
const toolsOpen = ref(true)
const vocabularyDraft = ref('')

// locked 表示会话进行中，运行设置不可修改
const locked = computed(() => state.status === 'connecting' || state.status === 'live')
// lockedTip 在会话进行中的说明后附加官网提示
const lockedTip = (tip: string) => (locked.value ? `${tip} (${t('pg.liveLocked')})` : tip)
// maxContextLimit 是 Max context size 的上限：模型输入上限
const maxContextLimit = computed(() => currentModel.value?.input_token_limit ?? 131072)
// systemSubtitle 是系统指令卡片的副标题
const systemSubtitle = computed(
  () => settings.value.systemInstruction.trim() || t('pg.systemPlaceholder'),
)
// mediaOptions 是官网 Live 页的媒体分辨率选项
const mediaOptions: { value: 'low' | 'medium'; label: TranslationKey }[] = [
  { value: 'low', label: 'pg.liveMediaLow' },
  { value: 'medium', label: 'pg.liveMediaMedium' },
]
// languageLabel 是转录语言的选择结果：未选为 Detect，否则列出所选语言
const languageLabel = computed(() => {
  const selected = currentModel.value?.languages?.filter((language) =>
    settings.value.languages.includes(language.code),
  )
  if (!selected?.length) return t('pg.detect')
  return selected.map((language) => language.name).join(', ')
})

// setMaxContext 修改上下文上限，目标值不小于上限时按官网规则降到上限之下
function setMaxContext(value: number): void {
  settings.value.maxContext = value
  if (settings.value.targetContext >= value)
    settings.value.targetContext = Math.max(minContext, value - 1)
}

// setTargetContext 修改滑动后保留的上下文
function setTargetContext(value: number): void {
  settings.value.targetContext = Math.min(Math.max(value, minContext), settings.value.maxContext)
}

// numberInput 读取数字输入框的值
function numberInput(event: Event): number {
  return Math.round(Number((event.target as HTMLInputElement).value))
}

// toggleLanguage 选择或取消一种转录语言
function toggleLanguage(code: string, checked: boolean): void {
  const languages = settings.value.languages.filter((item) => item !== code)
  settings.value.languages = checked ? [...languages, code] : languages
}

// addVocabulary 把输入的词加入自定义词表
function addVocabulary(): void {
  const value = vocabularyDraft.value.trim()
  if (value !== '' && !settings.value.customVocabulary.includes(value))
    settings.value.customVocabulary.push(value)
  vocabularyDraft.value = ''
}
</script>

<template>
  <RunSettingsFrame
    :open="open"
    :narrow="narrow"
    @close="emit('close')"
    @code="emit('code')"
    @reset="!locked && live.resetSettings()"
  >
    <div class="field-group">
      <ModelCard
        :model="currentModel"
        kind="live"
        :has-content="state.turns.length > 0"
        :locked-tip="locked ? t('pg.liveModelLocked') : ''"
        @select="live.selectModel($event.id)"
      />
      <button
        v-tooltip.left="
          variant === 'transcription'
            ? t('pg.systemUnsupported')
            : locked
              ? t('pg.liveSystemLocked')
              : ''
        "
        type="button"
        class="settings-card system-instructions-card"
        :class="{ 'disabled-interactive': variant === 'transcription' || locked }"
        :aria-label="t('pg.systemInstructions')"
        @click="variant !== 'transcription' && !locked && (systemOpen = true)"
      >
        <span class="title">{{ t('pg.systemInstructions') }}</span>
        <span class="subtitle">{{ systemSubtitle }}</span>
      </button>
    </div>

    <div v-if="variant === 'conversation'" class="field-group">
      <div
        v-if="currentModel?.voice_details?.length"
        v-tooltip.left="lockedTip(t('pg.liveVoiceTip'))"
        class="settings-item-column"
      >
        <h3 class="item-description-title">{{ t('pg.voice') }}</h3>
        <LiveVoiceSelect
          v-model="settings.voice"
          :voices="currentModel.voice_details"
          :disabled="locked"
        />
      </div>
      <div
        v-if="currentModel?.capabilities?.media_resolution"
        v-tooltip.left="lockedTip(t('pg.mediaResolutionTip'))"
        class="settings-item-column"
      >
        <h3 class="item-description-title">{{ t('pg.mediaResolution') }}</h3>
        <UiSelect
          v-model="settings.mediaResolution"
          class="w-full"
          :disabled="locked"
          :aria-label="t('pg.mediaResolution')"
        >
          <option v-for="item in mediaOptions" :key="item.value" :value="item.value">
            {{ t(item.label) }}
          </option>
        </UiSelect>
      </div>
      <div
        v-if="settings.thinkingLevel"
        v-tooltip.left="lockedTip(t('pg.thinkingLevelTip'))"
        class="settings-item-column"
      >
        <h3 class="item-description-title">{{ t('pg.thinkingLevel') }}</h3>
        <UiSelect
          v-model="settings.thinkingLevel"
          class="w-full"
          :disabled="locked"
          :aria-label="t('pg.thinkingLevel')"
        >
          <option v-for="level in currentModel?.thinking_levels ?? []" :key="level" :value="level">
            {{ t(thinkingLevelLabels[level]) }}
          </option>
        </UiSelect>
      </div>
    </div>

    <div v-if="variant === 'conversation'" class="field-group">
      <button
        v-tooltip.left="t('pg.sessionContextTip')"
        type="button"
        class="settings-group-header"
        :class="{ expanded: contextOpen }"
        :aria-expanded="contextOpen"
        :aria-label="t('pg.toggleSessionContext')"
        @click="contextOpen = !contextOpen"
      >
        <span class="group-title">{{ t('pg.sessionContext') }}</span>
        <UiIcon name="chevronDown" :size="20" class="expand-icon" />
      </button>
      <template v-if="contextOpen">
        <div v-tooltip.left="lockedTip(t('pg.maxContextTip'))" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.maxContext') }}</h3>
          <div class="ms-slider">
            <UiSlider
              :model-value="settings.maxContext"
              :min="minContext"
              :max="maxContextLimit"
              :step="1"
              :disabled="locked"
              :label="t('pg.maxContext')"
              @update:model-value="setMaxContext"
            />
            <input
              ms-input
              class="small slider-number-input large-number"
              type="number"
              :min="minContext"
              :max="maxContextLimit"
              step="1"
              :disabled="locked"
              :aria-label="t('pg.maxContext')"
              :value="settings.maxContext"
              @change="
                setMaxContext(Math.min(Math.max(numberInput($event), minContext), maxContextLimit))
              "
            />
          </div>
        </div>
        <div v-tooltip.left="lockedTip(t('pg.targetContextTip'))" class="settings-item-column">
          <h3 class="item-description-title">{{ t('pg.targetContext') }}</h3>
          <div class="ms-slider">
            <UiSlider
              :model-value="settings.targetContext"
              :min="minContext"
              :max="settings.maxContext"
              :step="1"
              :disabled="locked"
              :label="t('pg.targetContext')"
              @update:model-value="setTargetContext"
            />
            <input
              ms-input
              class="small slider-number-input large-number"
              type="number"
              :min="minContext"
              :max="settings.maxContext"
              step="1"
              :disabled="locked"
              :aria-label="t('pg.targetContext')"
              :value="settings.targetContext"
              @change="setTargetContext(numberInput($event))"
            />
          </div>
        </div>
      </template>
    </div>

    <div v-if="variant === 'conversation'" class="field-group">
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
        <div v-tooltip.left="lockedTip(t('pg.functionCallingTip'))" class="toggle-row">
          <div class="label-container">
            <p class="label">{{ t('pg.functionCalling') }}</p>
          </div>
          <div class="actions">
            <button
              type="button"
              ms-button
              class="edit-button ms-button-borderless"
              :aria-label="t('pg.editFunctions')"
              :aria-disabled="!settings.functionCalling || locked"
              @click="settings.functionCalling && !locked && (functionsOpen = true)"
            >
              {{ t('pg.edit') }}
            </button>
            <UiSwitch
              v-model="settings.functionCalling"
              :disabled="locked"
              :label="t('pg.functionCalling')"
            />
          </div>
        </div>
        <div
          v-tooltip.left="
            lockedTip(
              settings.functionCalling
                ? t('pg.automaticResponseTip')
                : t('pg.automaticResponseOffTip'),
            )
          "
          class="toggle-row"
        >
          <div class="label-container">
            <p class="label">{{ t('pg.automaticResponse') }}</p>
          </div>
          <UiSwitch
            v-model="settings.automaticResponse"
            :disabled="locked || !settings.functionCalling"
            :label="t('pg.automaticResponse')"
          />
        </div>
        <div v-tooltip.left="lockedTip(t('pg.liveSearchTip'))" class="toggle-row">
          <div class="label-container">
            <p class="label">{{ t('pg.googleSearch') }}</p>
            <p v-if="settings.googleSearch" class="search-source">
              {{ t('pg.source') }} <UiIcon name="search" :size="12" /> Google Search
            </p>
          </div>
          <UiSwitch
            v-model="settings.googleSearch"
            :disabled="locked"
            :label="t('pg.googleSearch')"
          />
        </div>
      </template>
    </div>

    <div v-if="variant === 'translation'" class="field-group">
      <div v-tooltip.left="lockedTip(t('pg.targetLanguageTip'))" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.targetLanguage') }}</h3>
        <UiSelect
          v-model="settings.targetLanguage"
          icon="globe"
          class="w-full"
          :disabled="locked"
          :aria-label="t('pg.targetLanguage')"
        >
          <option
            v-for="language in currentModel?.languages ?? []"
            :key="language.code"
            :value="language.code"
          >
            {{ language.name }}
          </option>
        </UiSelect>
      </div>
      <div v-tooltip.left="lockedTip(t('pg.echoTargetLanguageTip'))" class="toggle-row">
        <div class="label-container">
          <p class="label">{{ t('pg.echoTargetLanguage') }}</p>
        </div>
        <UiSwitch
          v-model="settings.echoTargetLanguage"
          :disabled="locked"
          :label="t('pg.echoTargetLanguage')"
        />
      </div>
    </div>

    <div v-if="variant === 'transcription'" class="field-group">
      <div v-tooltip.left="lockedTip(t('pg.primaryLanguageTip'))" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.primaryLanguage') }}</h3>
        <DropdownMenuRoot :modal="false">
          <DropdownMenuTrigger
            class="ms-select-trigger w-full"
            :disabled="locked"
            :aria-label="t('pg.primaryLanguage')"
          >
            <UiIcon name="globe" :size="18" />
            <span class="ms-select-value">{{ languageLabel }}</span>
            <UiIcon name="chevronDown" :size="20" class="ms-select-arrow" />
          </DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent
              align="start"
              :side-offset="0"
              class="ms-select-panel language-panel"
            >
              <DropdownMenuCheckboxItem
                class="ms-select-option"
                :model-value="settings.languages.length === 0"
                @select.prevent
                @update:model-value="settings.languages = []"
              >
                <UiIcon
                  name="check"
                  :size="18"
                  :class="{ hidden: settings.languages.length !== 0 }"
                />
                {{ t('pg.detect') }}
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                class="ms-select-option"
                :model-value="settings.languages.length === (currentModel?.languages?.length ?? 0)"
                @select.prevent
                @update:model-value="
                  settings.languages = $event
                    ? (currentModel?.languages ?? []).map((language) => language.code)
                    : []
                "
              >
                <UiIcon
                  name="check"
                  :size="18"
                  :class="{
                    hidden: settings.languages.length !== (currentModel?.languages?.length ?? 0),
                  }"
                />
                {{ t('pg.selectAll') }}
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                v-for="language in currentModel?.languages ?? []"
                :key="language.code"
                class="ms-select-option"
                :model-value="settings.languages.includes(language.code)"
                @select.prevent
                @update:model-value="toggleLanguage(language.code, $event === true)"
              >
                <UiIcon
                  name="check"
                  :size="18"
                  :class="{ hidden: !settings.languages.includes(language.code) }"
                />
                {{ language.name }}
              </DropdownMenuCheckboxItem>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
      <div v-tooltip.left="lockedTip(t('pg.customVocabularyTip'))" class="vocabulary">
        <div class="settings-item">
          <label class="item-description-title" for="live-vocabulary">{{
            t('pg.customVocabulary')
          }}</label>
          <input
            id="live-vocabulary"
            v-model="vocabularyDraft"
            ms-input
            class="small vocabulary-input"
            :disabled="locked"
            :placeholder="t('pg.customVocabularyPlaceholder')"
            @keydown.enter.prevent="addVocabulary"
            @blur="addVocabulary"
          />
        </div>
        <div v-if="settings.customVocabulary.length" class="chips">
          <span v-for="(word, index) in settings.customVocabulary" :key="word" class="chip">
            <span class="chip-label">{{ word }}</span>
            <button
              type="button"
              class="chip-remove"
              :disabled="locked"
              :aria-label="t('pg.removeVocabulary').replace('{word}', word)"
              @click="settings.customVocabulary.splice(index, 1)"
            >
              <UiIcon name="close" :size="14" />
            </button>
          </span>
        </div>
      </div>
      <div v-tooltip.left="lockedTip(t('pg.smartTranscriptionTip'))" class="toggle-row">
        <div class="label-container">
          <p class="label">{{ t('pg.smartTranscription') }}</p>
        </div>
        <UiSwitch
          v-model="settings.smartTranscription"
          :disabled="locked"
          :label="t('pg.smartTranscription')"
        />
      </div>
    </div>
  </RunSettingsFrame>

  <LiveSystemInstructions v-model:open="systemOpen" v-model:text="settings.systemInstruction" />
  <JsonEditorDialog
    v-model:open="functionsOpen"
    v-model:text="settings.functionDeclarations"
    kind="functions"
  />
</template>

<style scoped>
.system-instructions-card {
  margin-block: 8px;
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

.large-number {
  --ms-input-width: 80px;
}

.vocabulary {
  margin-block: 8px;
}

.vocabulary-input {
  --ms-input-width: 140px;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  max-height: 140px;
  margin-top: 8px;
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

.language-panel {
  max-height: 320px;
  overflow-y: auto;
}

.language-panel .hidden {
  visibility: hidden;
}
</style>
