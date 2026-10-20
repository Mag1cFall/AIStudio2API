<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { nextTick, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import RunKeyHint from '../RunKeyHint.vue'
import { submits, useRunTooltip } from '../run-keys'

const draft = defineModel<string>({ required: true })
const props = withDefaults(
  defineProps<{
    placeholder: string
    label: string
    running: boolean
    canRun: boolean
    runLabel?: string
    showText?: boolean
  }>(),
  { runLabel: '', showText: true },
)
const emit = defineEmits<{ run: []; stop: []; files: [files: File[]] }>()
const { t } = useI18n()
const textarea = ref<HTMLTextAreaElement>()
const dragging = ref(false)

// resize 让输入框随内容增高，超过 210 px 后在框内滚动
function resize(): void {
  const element = textarea.value
  if (element === undefined) return
  element.style.height = 'auto'
  element.style.height = `${Math.min(Math.max(element.scrollHeight, 21), 210)}px`
}

watch(draft, () => void nextTick(resize))

// submit 运行或停止
function submit(): void {
  if (props.running) emit('stop')
  else if (props.canRun) emit('run')
}

// runTooltip 是运行按钮的悬停提示
const runTooltip = useRunTooltip(() => props.running)

// onKeydown 与官网 ms-prompt-box 一致：Escape 失焦，生成中或不能运行时快捷键不起作用，否则按提交键运行
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    textarea.value?.blur()
    return
  }
  if (props.running || !props.canRun) return
  if (submits(event)) {
    event.preventDefault()
    emit('run')
  }
}

// onDrop 把拖入的文件交给页面
function onDrop(event: DragEvent): void {
  dragging.value = false
  const files = [...(event.dataTransfer?.files ?? [])]
  if (files.length > 0) emit('files', files)
}

defineExpose({ focus: () => textarea.value?.focus() })
</script>

<template>
  <div class="ms-prompt-box" @dragenter.prevent="dragging = true">
    <div
      class="dragging-overlay"
      :class="{ dragging }"
      @dragover.prevent
      @dragleave="dragging = false"
      @drop.prevent="onDrop"
    >
      <UiIcon name="addCircle" :size="24" />
      {{ t('pg.dropFiles') }}
    </div>
    <div class="prompt-box-container">
      <div v-if="$slots.top" class="augmentation">
        <slot name="top" />
      </div>
      <div v-if="showText" class="textarea-row">
        <div class="text-wrapper">
          <textarea
            ref="textarea"
            v-model="draft"
            class="textarea"
            rows="1"
            :placeholder="placeholder"
            :aria-label="label"
            @keydown="onKeydown"
          ></textarea>
        </div>
      </div>
      <div v-if="$slots.media" class="multi-media-row">
        <slot name="media" />
      </div>
      <div class="buttons-row">
        <div class="button-row-left">
          <slot name="left" />
        </div>
        <div class="button-wrapper">
          <slot name="right" />
          <button
            v-tooltip="runTooltip"
            type="button"
            ms-button
            class="run-button ms-button-primary"
            :aria-label="running ? t('pg.stop') : runLabel || t('pg.run')"
            :aria-disabled="!running && !canRun"
            @click="submit"
          >
            <template v-if="running">
              <UiIcon name="refresh" :size="18" class="spin" />
              <span class="run-button-label">{{ t('pg.stop') }}</span>
            </template>
            <template v-else>
              <span class="run-button-label">{{ runLabel || t('pg.run') }}</span>
              <RunKeyHint />
            </template>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ms-prompt-box {
  position: relative;
  z-index: 1;
  display: block;
  width: 100%;
  max-width: 1000px;
  margin: 0 auto;
}

.dragging-overlay {
  position: absolute;
  z-index: 10;
  right: 0;
  bottom: 0;
  left: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 174px;
  margin: 0 12px 12px;
  border: 4px dashed var(--color-v3-text-link);
  border-radius: 12px;
  backdrop-filter: blur(5px);
  background-color: var(--color-v3-overlay-background);
  box-shadow: var(--shadow-v3-lg);
  font-weight: 500;
  line-height: 21px;
  pointer-events: none;
  visibility: hidden;
}

.dragging-overlay.dragging {
  pointer-events: auto;
  visibility: visible;
}

.prompt-box-container {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0 12px 12px;
  padding: 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container-high);
  box-shadow: var(--shadow-v3-md);
}

.augmentation {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 12px;
}

.multi-media-row {
  display: flex;
  flex-wrap: nowrap;
  gap: 8px;
  margin: 0 -2px;
  padding: 2px;
  overflow-x: auto;
}

.textarea-row {
  display: flex;
  align-items: flex-start;
}

.text-wrapper {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 36px;
}

.textarea {
  flex: 1 1 0;
  width: 100%;
  min-width: 0;
  min-height: 21px;
  max-height: 210px;
  padding: 0;
  border: none;
  background-color: transparent;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
  outline: none;
  overflow-wrap: anywhere;
  resize: none;
  white-space: pre-wrap;
}

.textarea::placeholder {
  color: var(--color-v3-text-var);
}

.buttons-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.button-row-left {
  display: flex;
  flex-grow: 1;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.button-wrapper {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
  height: 36px;
  margin-left: 8px;
}

.run-button .run-button-label {
  min-width: 28px;
}

.spin {
  animation: spin 0.66s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(1turn);
  }
}
</style>
