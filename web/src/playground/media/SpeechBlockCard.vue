<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import StyleDropdown from './StyleDropdown.vue'
import SttButton from './SttButton.vue'
import { useSpeech } from './speech'

const props = defineProps<{ index: number }>()
const emit = defineEmits<{ voice: []; keydown: [event: KeyboardEvent] }>()
const { t } = useI18n()
const speech = useSpeech()
const { state, daikon, tags, styles } = speech
const block = computed(() => state.blocks[props.index]!)
const speaker = computed(() => state.speakers[block.value.speaker]!)
const textarea = ref<HTMLTextAreaElement>()
// stripOpen 表示表情标签条已展开
const stripOpen = ref(false)
// triggerStart 是输入 < 或 [ 触发标签条时触发字符的位置，由按钮展开时为 -1
const triggerStart = ref(-1)
const query = ref('')
const open = computed(() => (daikon.value ? '<' : '['))
const close = computed(() => (daikon.value ? '>' : ']'))
const filteredTags = computed(() => {
  const value = query.value.toLowerCase()
  return value === '' ? tags.value : tags.value.filter((tag) => tag.includes(value))
})
const placeholder = computed(() =>
  props.index === 0
    ? t('pg.speechPlaceholderFirst')
        .replace(
          '{example}',
          daikon.value
            ? "<gasp> That's a great idea! <laugh>"
            : "[amused] That's a great idea! [laughs]",
        )
        .replace('{open}', open.value)
    : t('pg.speechPlaceholder').replace('{open}', open.value),
)
const previewing = computed(() => state.previewing === block.value.id)
const previewLoading = computed(() => state.previewLoading === block.value.id)
const hasText = computed(() => block.value.text.trim() !== '')
const previewTip = computed(() =>
  previewLoading.value
    ? t('pg.loadingPreview')
    : previewing.value
      ? t('pg.stopPreview')
      : hasText.value
        ? t('pg.previewBlock')
        : t('pg.addTextToPreview'),
)

// resize 按内容调整输入框高度，范围由样式的 3 至 10 行限定
function resize(): void {
  const element = textarea.value
  if (!element) return
  element.style.height = 'auto'
  element.style.height = `${element.scrollHeight}px`
}

// toggleStrip 由 + Expression 按钮展开或收起标签条
function toggleStrip(): void {
  stripOpen.value = !stripOpen.value
  triggerStart.value = -1
  query.value = ''
}

// onKeyUp 与官网一致：光标前有未闭合的 < 或 [ 时按其后的文字筛选标签
function onKeyUp(): void {
  const element = textarea.value
  if (!element) return
  const before = block.value.text.slice(0, element.selectionStart)
  const start = before.lastIndexOf(open.value)
  const typed = start === -1 ? '' : before.slice(start + 1)
  if (start !== -1 && !typed.includes(close.value) && !/\s{2}|\n/.test(typed)) {
    stripOpen.value = true
    triggerStart.value = start
    query.value = typed
  } else if (triggerStart.value !== -1) {
    stripOpen.value = false
    triggerStart.value = -1
    query.value = ''
  }
}

// insertTag 在光标处插入标签；由输入触发时替换已输入的部分
function insertTag(tag: string): void {
  const element = textarea.value
  const text = block.value.text
  const value = `${open.value}${tag}${close.value}`
  let before: string
  let after: string
  if (triggerStart.value !== -1) {
    before = text.slice(0, triggerStart.value)
    after = text.slice(element?.selectionStart ?? text.length)
  } else {
    const cursor = element?.selectionStart ?? text.length
    before = text.slice(0, cursor)
    after = text.slice(cursor)
  }
  const head = `${before.length > 0 && !before.endsWith(' ') ? `${before} ` : before}${value} `
  const tail = after.length > 0 && !after.startsWith(' ') ? after : after.trimStart()
  block.value.text = head + tail
  stripOpen.value = false
  triggerStart.value = -1
  query.value = ''
  void nextTick(() => {
    element?.focus()
    element?.setSelectionRange(head.length, head.length)
    resize()
  })
}

// appendText 把语音转写结果追加到台词
function appendText(text: string): void {
  const current = block.value.text
  block.value.text = current ? `${current.trimEnd()} ${text}` : text
  void nextTick(resize)
}

// onKeydown 处理 Esc 收起标签条，其余按键交给页面处理运行快捷键
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape' && stripOpen.value) {
    event.preventDefault()
    stripOpen.value = false
    triggerStart.value = -1
    return
  }
  emit('keydown', event)
}

// focus 聚焦台词输入框
function focus(): void {
  textarea.value?.focus()
}

watch(
  () => block.value.text,
  () => void nextTick(resize),
)
onMounted(resize)
defineExpose({ focus })
</script>

<template>
  <div tabindex="-1" role="region" :aria-label="t('pg.speechBlock')" class="speech-block-card">
    <div class="block-header">
      <div class="chip-row">
        <button
          type="button"
          ms-button
          class="voice-chip voice-playground-chip ms-button-filter-chip"
          @click="emit('voice')"
        >
          <span
            aria-hidden="true"
            class="speaker-dot"
            :class="{ 'speaker-two': block.speaker === 1 }"
          ></span>
          <span class="voice-chip-label">{{ speaker.alias }} - {{ speaker.voice }}</span>
          <UiIcon name="arrowDropDown" :size="18" class="voice-chip-chevron" />
        </button>
        <StyleDropdown
          v-if="daikon"
          v-model="block.style"
          class="style-dropdown"
          :styles="styles"
          custom
        />
        <button
          v-tooltip.below="
            t('pg.insertExpression').replace(
              '{examples}',
              daikon ? '<laugh>, <cough>' : '[laughs], [sigh]',
            )
          "
          type="button"
          ms-button
          class="add-expression-btn ms-button-filter-chip"
          :class="{ active: stripOpen }"
          @click="toggleStrip"
        >
          <span class="tag-brackets">{{ open }} {{ close }}</span>
          <span>{{ t('pg.addExpression') }}</span>
        </button>
      </div>
      <div v-if="stripOpen" role="toolbar" :aria-label="t('pg.expressionTags')" class="tag-strip">
        <button
          v-for="(tag, tagIndex) in filteredTags"
          :key="tag"
          type="button"
          ms-button
          class="tag-chip ms-button-filter-chip"
          :style="{ '--tag-index': tagIndex }"
          @mousedown.prevent
          @click="insertTag(tag)"
        >
          {{ tag }}
        </button>
        <div v-if="filteredTags.length === 0 && query" class="no-matching-tags">
          {{ t('pg.noMatchingTags').replace('{query}', query) }}
        </div>
      </div>
      <div class="block-actions">
        <span v-tooltip.below="previewTip" class="play-block-tooltip-host">
          <button
            type="button"
            class="play-block-btn"
            :class="{ 'is-playing': previewing, 'is-loading': previewLoading }"
            :aria-label="previewTip"
            :disabled="previewLoading || (!hasText && !previewing)"
            @click="speech.preview(block)"
          >
            <UiIcon
              :name="previewLoading ? 'spinner' : previewing ? 'stop' : 'play'"
              :size="18"
              class="audio-preview-icon"
              :class="{ spin: previewLoading }"
            />
          </button>
        </span>
        <SttButton @text="appendText" />
        <button
          v-if="state.blocks.length > 1"
          v-tooltip.below="t('pg.deleteSpeechBlock')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :aria-label="t('pg.deleteSpeechBlock')"
          @click="speech.removeBlock(index)"
        >
          <UiIcon name="close" :size="20" />
        </button>
      </div>
    </div>
    <div class="textarea-wrapper" @keyup="onKeyUp">
      <textarea
        ref="textarea"
        v-model="block.text"
        rows="1"
        class="textarea"
        spellcheck="false"
        :placeholder="placeholder"
        :aria-label="t('pg.speechBlockText')"
        @keydown="onKeydown"
        @click="onKeyUp"
      ></textarea>
    </div>
  </div>
</template>

<style scoped>
.speech-block-card {
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  transition: border-color 0.2s ease-in-out;
}

.speech-block-card:hover {
  border-color: var(--color-v3-outline);
}

.speech-block-card:focus {
  outline: none;
}

.speech-block-card:focus-within {
  border-color: var(--color-v3-outline-accent);
}

.speech-block-card:focus-within .block-actions,
.speech-block-card:hover .block-actions {
  opacity: 1;
  pointer-events: auto;
}

@media (pointer: coarse) {
  .speech-block-card .block-actions {
    opacity: 1;
    pointer-events: auto;
  }
}

.block-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
  border-bottom: 1px solid var(--color-v3-outline-var);
}

.chip-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

@media screen and (max-width: 600px) {
  .chip-row {
    display: contents;
  }
}

.style-dropdown {
  max-width: 165px;
}

.voice-chip {
  max-width: fit-content;
}

.voice-playground-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.speaker-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-v3-outline-accent);
}

.speaker-dot.speaker-two {
  background: var(--color-v3-accent-1);
}

.voice-chip-chevron {
  color: var(--color-v3-text-var);
}

.add-expression-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: fit-content;
}

.add-expression-btn .tag-brackets {
  color: var(--color-v3-text-var);
  font-family: monospace;
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
}

.add-expression-btn.active {
  border-color: var(--color-v3-outline-accent);
  background: var(--color-v3-surface-container-highest);
}

.tag-strip {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
  mask-image: linear-gradient(90deg, #000 94%, transparent);
}

.tag-strip::-webkit-scrollbar {
  display: none;
}

@media screen and (max-width: 480px) {
  .tag-strip {
    display: none;
  }
}

.tag-chip {
  flex-shrink: 0;
  padding: 4px 10px;
  animation: tag-enter 0.2s cubic-bezier(0, 0, 0.2, 1) both;
  animation-delay: calc(min(var(--tag-index), 12) * 15ms);
}

@keyframes tag-enter {
  from {
    opacity: 0;
    transform: translateY(4px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.no-matching-tags {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
}

.block-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.2s ease-in-out;
}

.play-block-tooltip-host {
  display: inline-flex;
}

.play-block-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  height: 32px;
  aspect-ratio: 1;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 50%;
  background-color: transparent;
  color: var(--color-v3-text);
  cursor: pointer;
  transition:
    background-color 0.15s ease-in-out,
    color 0.15s ease-in-out;
}

.play-block-btn:hover:not(:disabled),
.play-block-btn:focus-visible:not(:disabled) {
  background-color: var(--color-v3-button-container);
  color: var(--color-v3-text-on-button);
}

.play-block-btn:disabled {
  color: var(--color-v3-text-disable);
  cursor: not-allowed;
}

.play-block-btn.is-loading {
  cursor: progress;
}

.textarea-wrapper {
  padding: 12px;
}

.textarea {
  box-sizing: border-box;
  width: 100%;
  min-height: 3lh;
  max-height: 10lh;
  padding: 0;
  border: none;
  outline: none;
  background-color: transparent;
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
  line-height: 21px;
  resize: none;
}

.textarea::placeholder {
  color: var(--color-v3-text-var);
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
