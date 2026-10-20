<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import { clock, maxSections, seconds, sectionTypes, useMusic, type SectionType } from './music'

const { t } = useI18n()
const music = useMusic()
const { state } = music
// dragging 是正在拖动的段落序号
const dragging = ref(-1)
const over = ref(-1)

// setType 修改段落类型
function setType(index: number, type: SectionType): void {
  state.sections[index]!.type = type
  music.syncSections()
}

// setTime 修改段落起止时间，非法输入恢复原值
function setTime(index: number, field: 'startTime' | 'endTime', event: Event): void {
  const input = event.target as HTMLInputElement
  const section = state.sections[index]!
  if (/^\d+:\d{2}$/.test(input.value.trim()))
    section[field] = Math.max(0, seconds(input.value.trim()))
  input.value = clock(section[field])
  music.syncSections()
}

// setVolume 修改段落强度
function setVolume(index: number, value: number): void {
  state.sections[index]!.volume = value
  music.syncSections()
}

// setDescription 修改段落描述
function setDescription(index: number, event: Event): void {
  state.sections[index]!.description = (event.target as HTMLTextAreaElement).value
  music.syncSections()
}

// endDrag 结束拖动
function endDrag(): void {
  dragging.value = -1
  over.value = -1
}

// drop 把拖动的段落放到目标位置
function drop(index: number): void {
  if (dragging.value !== -1 && dragging.value !== index) music.moveSection(dragging.value, index)
  endDrag()
}
</script>

<template>
  <div class="composer-container">
    <div class="song-structure-header">
      <div class="header-label">
        <UiIcon name="layout" :size="16" />
        <span>{{ t('pg.songStructure') }}</span>
      </div>
      <DropdownMenuRoot :modal="false">
        <DropdownMenuTrigger as-child>
          <button
            type="button"
            ms-button
            class="ms-button-filter-chip add-block-select"
            :disabled="state.sections.length >= maxSections"
          >
            <UiIcon name="add" :size="18" />
            {{ t('pg.addSection') }}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuPortal>
          <DropdownMenuContent align="end" :side-offset="4" class="ms-menu-panel">
            <DropdownMenuItem
              v-for="type in sectionTypes"
              :key="type"
              class="ms-menu-item"
              @select="music.addSection(type)"
            >
              {{ type }}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenuPortal>
      </DropdownMenuRoot>
      <div class="add-block-buttons">
        <button
          v-for="type in sectionTypes"
          :key="type"
          type="button"
          ms-button
          class="ms-button-filter-chip"
          :disabled="state.sections.length >= maxSections"
          @click="music.addSection(type)"
        >
          <UiIcon name="add" :size="18" />
          {{ type }}
        </button>
      </div>
    </div>
    <div class="block-list">
      <div v-if="state.sections.length === 0" class="composer-empty-state">
        <UiIcon name="modeMusic" :size="48" />
        <p>{{ t('pg.composerEmpty') }}</p>
      </div>
      <div
        v-for="(section, index) in state.sections"
        :key="section.id"
        class="block-card"
        :class="{ 'drag-over': over === index && dragging !== index, dragging: dragging === index }"
        @dragover.prevent="over = index"
        @dragleave="over = over === index ? -1 : over"
        @drop.prevent="drop(index)"
      >
        <div class="block-card-content">
          <div class="block-card-controls">
            <div class="block-card-top">
              <span
                class="drag-handle"
                draggable="true"
                :aria-label="t('pg.dragSection')"
                @dragstart="dragging = index"
                @dragend="endDrag"
              >
                <UiIcon name="dragIndicator" :size="20" />
              </span>
              <DropdownMenuRoot :modal="false">
                <DropdownMenuTrigger as-child>
                  <button type="button" ms-button class="ms-button-filter-chip type-selector-field">
                    {{ section.type }}
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuPortal>
                  <DropdownMenuContent align="start" :side-offset="4" class="ms-menu-panel">
                    <DropdownMenuItem
                      v-for="type in sectionTypes"
                      :key="type"
                      class="ms-menu-item"
                      @select="setType(index, type)"
                    >
                      {{ type }}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenuPortal>
              </DropdownMenuRoot>
              <div class="block-actions">
                <button
                  type="button"
                  class="a11y-move-button"
                  :aria-label="t('pg.moveSectionUp')"
                  :disabled="index === 0"
                  @click="music.moveSection(index, index - 1)"
                ></button>
                <button
                  type="button"
                  class="a11y-move-button"
                  :aria-label="t('pg.moveSectionDown')"
                  :disabled="index === state.sections.length - 1"
                  @click="music.moveSection(index, index + 1)"
                ></button>
              </div>
            </div>
            <div class="block-card-bottom">
              <div class="time-range">
                <UiIcon name="schedule" :size="20" />
                <input
                  class="time-input"
                  :value="clock(section.startTime)"
                  :aria-label="t('pg.sectionStart')"
                  @change="setTime(index, 'startTime', $event)"
                />
                <span class="time-separator">-</span>
                <input
                  class="time-input"
                  :value="clock(section.endTime)"
                  :aria-label="t('pg.sectionEnd')"
                  @change="setTime(index, 'endTime', $event)"
                />
              </div>
              <div class="volume-control">
                <UiIcon v-tooltip="t('pg.sectionIntensity')" name="options" :size="20" />
                <UiSlider
                  class="volume-slider"
                  :model-value="section.volume"
                  :min="0"
                  :max="10"
                  :step="1"
                  :label="t('pg.sectionIntensity')"
                  @update:model-value="setVolume(index, $event)"
                />
              </div>
            </div>
          </div>
          <textarea
            class="block-description"
            :value="section.description"
            :placeholder="t('pg.sectionPlaceholder').replace('{type}', section.type.toLowerCase())"
            :aria-label="t('pg.sectionDescription')"
            @input="setDescription(index, $event)"
          ></textarea>
        </div>
        <button
          type="button"
          ms-button
          class="ms-button-borderless ms-button-small remove-button desktop-remove"
          @mousedown.prevent
          @click="music.removeSection(index)"
        >
          <UiIcon name="close" :size="18" />
          {{ t('pg.remove') }}
        </button>
        <button
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon ms-button-small remove-button mobile-remove"
          :aria-label="t('pg.removeBlock')"
          @mousedown.prevent
          @click="music.removeSection(index)"
        >
          <UiIcon name="delete" :size="18" />
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.composer-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin: 16px;
  padding: 20px 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  transition: border-color 0.2s ease;
}

.composer-container:focus-within {
  border-color: var(--color-v3-outline-accent);
}

.song-structure-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 8px;
}

.header-label {
  display: flex;
  align-self: center;
  align-items: center;
  gap: 4px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.add-block-select {
  display: none;
}

.add-block-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

@container (max-width: 600px) {
  .add-block-select {
    display: inline-flex;
  }

  .add-block-buttons {
    display: none;
  }
}

.block-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.composer-empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 20px 0;
  color: var(--color-v3-text-var);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.composer-empty-state :deep(svg) {
  color: var(--color-v3-surface-container-highest);
}

.composer-empty-state p {
  margin: 0;
}

.block-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 0;
  animation: slide-in 0.25s cubic-bezier(0.25, 1, 0.5, 1) forwards;
}

.block-card + .block-card {
  padding-top: 12px;
  border-top: 1px solid var(--color-v3-outline-var);
}

.block-card.dragging {
  opacity: 0.4;
}

.block-card.drag-over {
  box-shadow: 0 -2px 0 var(--color-v3-outline-accent);
}

@keyframes slide-in {
  from {
    opacity: 0;
    transform: translateY(-8px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.block-card-content {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 8px;
}

.block-card-controls {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.block-card-top {
  display: flex;
  align-items: center;
  gap: 8px;
}

.block-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
}

.a11y-move-button {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0 0 0 0);
  border: 0;
  white-space: nowrap;
}

.drag-handle {
  display: inline-flex;
  flex-shrink: 0;
  color: var(--color-v3-text-var);
  cursor: grab;
}

.drag-handle:active {
  cursor: grabbing;
}

.block-card-bottom {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
}

.time-range,
.volume-control {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.volume-control {
  max-height: 28px;
}

.volume-slider {
  width: 120px;
}

.time-input {
  width: 50px;
  padding: 2px 4px;
  border: none;
  background: transparent;
  color: var(--color-v3-text);
  font-family: 'DM Mono', monospace;
  font-size: 12px;
  line-height: 18px;
  text-align: center;
}

.time-input:focus-visible {
  border-bottom: 1px solid var(--color-v3-outline-accent);
  outline: none;
}

.time-separator {
  color: var(--color-v3-text-var);
}

.block-description {
  min-height: 40px;
  border: none;
  background: transparent;
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
  line-height: 21px;
  resize: none;
  field-sizing: content;
}

.block-description::placeholder {
  color: var(--color-v3-text-var);
}

.block-description:focus-visible {
  outline: none;
}

.remove-button {
  align-self: flex-end;
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.desktop-remove {
  opacity: 0;
  transition: opacity 0.15s ease;
}

.mobile-remove {
  display: none;
}

[ms-button].remove-button,
[ms-button].remove-button:hover,
[ms-button].remove-button:focus-visible {
  background-color: transparent;
  color: var(--color-error);
}

.desktop-remove:focus-visible,
.block-card:hover .desktop-remove {
  opacity: 1;
}

@container (max-width: 600px) {
  .composer-container {
    margin: 4px;
  }

  .block-card-content {
    grid-template-columns: 1fr;
  }

  .block-card-controls {
    display: contents;
  }

  .block-card-top {
    order: 1;
  }

  .block-description {
    order: 2;
  }

  .block-card-bottom {
    flex-wrap: wrap;
    order: 3;
  }

  .desktop-remove {
    display: none;
  }

  .mobile-remove {
    display: inline-flex;
  }
}
</style>
