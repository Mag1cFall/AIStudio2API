<script setup lang="ts">
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { StylePreset } from './speech'

const props = defineProps<{ styles: StylePreset[]; custom: boolean }>()
const style = defineModel<string>({ required: true })
const { t } = useI18n()
const open = ref(false)
// draft 是自定义风格输入框的内容，打开菜单时预设风格不填入输入框
const draft = ref('')

// onOpen 与官网一致：当前风格不是预设时填入自定义输入框
function onOpen(value: boolean): void {
  open.value = value
  if (value) {
    const current = style.value.trim()
    draft.value = props.styles.some((preset) => preset.value === current) ? '' : current
  }
}

// choose 选择预设风格
function choose(preset: StylePreset): void {
  style.value = preset.value
  open.value = false
}

// onDraft 把自定义风格写回台词块
function onDraft(event: Event): void {
  draft.value = (event.target as HTMLTextAreaElement).value
  style.value = draft.value
}
</script>

<template>
  <div class="voice-style-dropdown">
    <PopoverRoot :open="open" @update:open="onOpen">
      <PopoverTrigger as-child>
        <button
          type="button"
          ms-button
          class="ms-button-filter-chip attribute-chip"
          :class="{ 'has-style': style.trim() !== '' }"
          :aria-label="t('pg.style')"
          :title="style.trim() || t('pg.style')"
        >
          <span class="chip-label">{{ style.trim() || t('pg.style') }}</span>
          <UiIcon name="arrowDropDown" :size="18" class="dropdown-icon" />
        </button>
      </PopoverTrigger>
      <PopoverPortal>
        <PopoverContent
          side="bottom"
          align="start"
          :side-offset="4"
          class="ms-menu-panel voice-style-menu"
          role="menu"
        >
          <div v-if="custom" role="group" :aria-label="t('pg.customStyle')" class="custom-style">
            <textarea
              class="custom-style-textarea"
              rows="2"
              :placeholder="t('pg.customStylePlaceholder')"
              :aria-label="t('pg.customStyle')"
              :value="draft"
              @input="onDraft"
              @keydown.enter.exact.prevent="open = false"
            ></textarea>
          </div>
          <button
            v-for="preset in styles"
            :key="preset.label"
            type="button"
            role="menuitem"
            class="ms-menu-item attribute-menu-item"
            :class="{ selected: style.trim() === preset.value }"
            @click="choose(preset)"
          >
            <span class="preset-option">
              <span class="preset-label">{{ preset.label }}</span>
              <span class="preset-description">{{ preset.value }}</span>
            </span>
          </button>
        </PopoverContent>
      </PopoverPortal>
    </PopoverRoot>
  </div>
</template>

<style scoped>
.voice-style-dropdown {
  display: block;
  min-width: 0;
  max-width: 100%;
}

.attribute-chip {
  justify-content: flex-start;
  width: 100%;
  max-width: 100%;
  height: 32px;
  border-radius: 12px;
}

.chip-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dropdown-icon {
  flex-shrink: 0;
  margin-left: auto;
}
</style>

<style>
.voice-style-menu {
  width: 280px;
  max-height: 420px;
  overflow-y: auto;
}

.voice-style-menu .custom-style {
  padding: 6px 4px;
}

.voice-style-menu .custom-style-textarea {
  box-sizing: border-box;
  width: 100%;
  height: 72px;
  padding: 6px 8px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 12px;
  outline: none;
  background-color: var(--color-v3-surface-container-high);
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  resize: none;
}

.voice-style-menu .custom-style-textarea::placeholder {
  color: var(--color-v3-text-var);
  white-space: pre-line;
}

.voice-style-menu .custom-style-textarea:focus {
  border-color: var(--color-v3-outline-accent);
}

.voice-style-menu .attribute-menu-item {
  width: 100%;
  height: auto;
  min-height: 48px;
  padding-block: 8px;
  border: none;
  background: none;
  text-align: left;
}

.voice-style-menu .attribute-menu-item:hover,
.voice-style-menu .attribute-menu-item:focus-visible {
  background-color: var(--color-v3-hover);
}

.voice-style-menu .attribute-menu-item.selected {
  background-color: var(--color-v3-surface-container-highest);
}

.voice-style-menu .preset-option {
  display: flex;
  flex-direction: column;
  gap: 4px;
  white-space: normal;
}

.voice-style-menu .preset-label {
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.voice-style-menu .preset-description {
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}
</style>
