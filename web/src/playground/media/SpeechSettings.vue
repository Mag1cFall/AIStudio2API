<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { nextTick, ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import ModelCard from '../ModelCard.vue'
import RunSettingsFrame from '../RunSettingsFrame.vue'
import { speechProtocols, useSpeech } from './speech'

defineProps<{ open: boolean; narrow: boolean }>()
const emit = defineEmits<{ close: []; code: []; voice: [speaker: number] }>()
const { t } = useI18n()
const speech = useSpeech()
const { state, currentModel, daikon, multi, openAIMulti, usedSpeakers } = speech
const modelOpen = ref(false)
// editing 是正在编辑名称的说话人序号
const editing = ref(-1)
const aliasInput = ref<HTMLInputElement[]>([])

// setTemperature 写入温度，空值时恢复模型默认
function setTemperature(event: Event): void {
  const value = (event.target as HTMLInputElement).value
  state.settings.temperature = value === '' ? null : Number(value)
}

// characteristics 与官网一致返回说话人卡片的描述与音高
function characteristics(voice: string): string {
  const detail = speech.voiceDetail(voice)
  if (!detail) return ''
  return [...(detail.descriptions ?? []), detail.pitch ? `${detail.pitch} pitch` : '']
    .filter(Boolean)
    .join(', ')
}

// editAlias 开始编辑说话人名称
function editAlias(index: number): void {
  editing.value = index
  void nextTick(() => aliasInput.value[0]?.select())
}

// saveAlias 保存说话人名称，空名称或与另一位重名时恢复默认名称
function saveAlias(index: number, event: Event): void {
  const value = (event.target as HTMLInputElement).value.trim()
  const other = state.speakers[index === 0 ? 1 : 0]!.alias
  state.speakers[index]!.alias = value && value !== other ? value : `Speaker ${index + 1}`
  editing.value = -1
}
</script>

<template>
  <RunSettingsFrame
    :open="open"
    :narrow="narrow"
    @close="emit('close')"
    @code="emit('code')"
    @reset="speech.reset()"
  >
    <div class="field-group">
      <ModelCard
        :model="currentModel"
        kind="speech"
        :has-content="state.blocks.length > 0"
        @select="speech.setModel($event.id)"
      />
      <div
        v-tooltip.left="openAIMulti ? t('pg.openAISpeechSingle') : ''"
        class="settings-item-column protocol-setting"
      >
        <h3 class="item-description-title">{{ t('playground.protocol') }}</h3>
        <UiSelect
          v-model="state.settings.protocol"
          class="w-full"
          :aria-label="t('playground.protocol')"
        >
          <option v-for="item in speechProtocols" :key="item.value" :value="item.value">
            {{ item.label }}
          </option>
        </UiSelect>
      </div>
    </div>

    <div class="field-group">
      <button
        type="button"
        class="settings-group-header model-settings-header"
        :class="{ expanded: modelOpen }"
        :aria-expanded="modelOpen"
        @click="modelOpen = !modelOpen"
      >
        <span class="group-title">{{ t('pg.modelSettings') }}</span>
        <UiIcon name="chevronDown" :size="20" class="expand-icon" />
      </button>
      <div v-if="modelOpen" v-tooltip.left="t('pg.temperatureTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.temperature') }}</h3>
        <div class="ms-slider">
          <UiSlider
            :model-value="state.settings.temperature ?? currentModel?.temperature ?? 1"
            :min="0"
            :max="currentModel?.max_temperature ?? 2"
            :step="0.05"
            :label="t('pg.temperature')"
            @update:model-value="state.settings.temperature = $event"
          />
          <input
            ms-input
            class="small slider-number-input"
            type="number"
            :min="0"
            :max="currentModel?.max_temperature ?? 2"
            step="0.05"
            :aria-label="t('pg.temperature')"
            :value="state.settings.temperature ?? currentModel?.temperature ?? 1"
            @change="setTemperature"
          />
        </div>
      </div>
    </div>

    <div v-if="state.blocks.length > 0" class="field-group speaker-settings">
      <h3 class="speaker-settings-title">{{ t('pg.speakerSettings') }}</h3>
      <div v-for="index in usedSpeakers" :key="index" class="active-voice-card">
        <button
          type="button"
          class="active-voice-card-trigger"
          :aria-label="`${state.speakers[index]!.alias} - ${state.speakers[index]!.voice}`"
          @click="emit('voice', index)"
        ></button>
        <div class="text-info">
          <div class="voice-speaker-name-row">
            <span class="voice-display-name">{{ state.speakers[index]!.voice }}</span>
            <input
              v-if="editing === index"
              ref="aliasInput"
              ms-input
              class="small speaker-alias-input"
              :value="state.speakers[index]!.alias"
              :aria-label="t('pg.speakerName')"
              @blur="saveAlias(index, $event)"
              @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
              @keydown.escape.prevent="editing = -1"
            />
            <button
              v-else
              v-tooltip.below="multi ? t('pg.renameSpeaker') : ''"
              type="button"
              class="speaker-alias-tag"
              :disabled="!multi"
              @click="editAlias(index)"
            >
              {{ state.speakers[index]!.alias }}
            </button>
          </div>
          <div class="voice-characteristics">
            {{ characteristics(state.speakers[index]!.voice) }}
          </div>
        </div>
      </div>
      <div v-if="daikon && multi" v-tooltip.left="t('pg.fillerWordsTip')" class="toggle-row">
        <div class="label-container">
          <p class="label">{{ t('pg.fillerWords') }}</p>
        </div>
        <UiSwitch v-model="state.settings.conversational" :label="t('pg.fillerWords')" />
      </div>
    </div>
  </RunSettingsFrame>
</template>

<style scoped>
.model-settings-header .group-title {
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
}

.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
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

.protocol-setting {
  margin-top: 8px;
}

.speaker-settings {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.speaker-settings-title {
  margin: 8px 0 0;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.active-voice-card {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container-highest);
  cursor: pointer;
}

.active-voice-card-trigger {
  position: absolute;
  z-index: 1;
  inset: 0;
  border: none;
  border-radius: inherit;
  background: transparent;
  cursor: pointer;
}

.text-info {
  display: flex;
  flex-direction: column;
  width: 100%;
  min-width: 0;
}

.voice-speaker-name-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.voice-display-name {
  margin-right: 8px;
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.speaker-alias-tag {
  position: relative;
  z-index: 2;
  max-width: 100%;
  overflow: hidden;
  padding: 2px 8px;
  border: none;
  border-radius: 4px;
  background: none;
  color: var(--color-v3-text-var);
  font: inherit;
  font-size: 12px;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.speaker-alias-tag:not(:disabled) {
  cursor: text;
}

.speaker-alias-tag:not(:disabled):hover {
  background: var(--color-v3-surface-container);
}

.speaker-alias-input {
  position: relative;
  z-index: 2;
  width: 120px;
  animation: fade-in 0.2s ease-in-out;
}

@keyframes fade-in {
  from {
    opacity: 0;
  }

  to {
    opacity: 1;
  }
}

.voice-characteristics {
  margin-top: 4px;
  overflow: hidden;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
  overflow-wrap: anywhere;
  text-overflow: ellipsis;
}
</style>
