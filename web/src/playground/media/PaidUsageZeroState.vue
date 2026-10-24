<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'

// Suggestion 是空态中的一张示例卡片：image 为卡片图，reference 为叠在左下角的参考图，caption 为卡片下方文字
export interface Suggestion {
  image: string
  label: string
  prompt: string
  caption?: string
  reference?: string
}

// layout 与官网 ms-suggestion-gallery 一致：mosaic 为两列拼贴，single-row 为单行横向滚动；600 px 以下都为单行
defineProps<{
  title: string
  subtitle?: string
  layout: 'mosaic' | 'single-row'
  icon: IconName
  suggestions: readonly Suggestion[]
}>()
const emit = defineEmits<{ select: [item: Suggestion] }>()
const { t } = useI18n()

// gallery 为示例卡片的容器，内容超出时显示左右滚动按钮
const gallery = ref<HTMLElement>()
const scroll = reactive({ left: false, right: false })
let observer: ResizeObserver | undefined

// updateScroll 按滚动位置决定是否显示左右滚动按钮
function updateScroll(): void {
  const node = gallery.value
  if (!node) return
  scroll.left = node.scrollLeft > 0
  scroll.right = node.scrollLeft + node.clientWidth < node.scrollWidth - 1
}

// scrollGallery 向左或向右滚动一张卡片
function scrollGallery(direction: 1 | -1): void {
  gallery.value?.scrollBy({ left: direction * 178, behavior: 'smooth' })
}

watch(gallery, (node) => {
  observer?.disconnect()
  if (!node) return
  observer = new ResizeObserver(updateScroll)
  observer.observe(node)
})
onUnmounted(() => observer?.disconnect())
</script>

<template>
  <div class="zero-state-content">
    <div class="paid-usage-zero-state">
      <div class="left-container">
        <div class="title-content-container">
          <h1 class="hero-text">{{ title }}</h1>
          <span v-if="subtitle" class="subtitle-container">{{ subtitle }}</span>
        </div>
      </div>
      <div class="suggestion-gallery-host" :data-layout="layout">
        <div ref="gallery" class="suggestion-gallery" @scroll="updateScroll">
          <button
            v-for="item in suggestions"
            :key="item.image"
            v-tooltip="item.label"
            type="button"
            class="suggestion-card"
            :aria-label="item.label"
            @click="emit('select', item)"
          >
            <span class="display-media-container">
              <img class="display-media" :src="item.image" alt="" @load="updateScroll" />
              <span class="media-type-icon"><UiIcon :name="icon" :size="18" /></span>
              <img v-if="item.reference" class="reference-image" :src="item.reference" alt="" />
            </span>
            <span v-if="item.caption" class="content-text">{{ item.caption }}</span>
          </button>
        </div>
        <div class="gallery-overlay">
          <div v-if="scroll.left" class="left-controls-container">
            <button
              v-tooltip="t('pg.scrollLeft')"
              type="button"
              ms-button
              class="ms-button-icon ms-button-primary"
              :aria-label="t('pg.scrollLeft')"
              @click="scrollGallery(-1)"
            >
              <UiIcon name="chevronLeft" :size="20" />
            </button>
          </div>
          <div v-if="scroll.right" class="right-controls-container">
            <button
              v-tooltip="t('pg.scrollRight')"
              type="button"
              ms-button
              class="ms-button-icon ms-button-primary"
              :aria-label="t('pg.scrollRight')"
              @click="scrollGallery(1)"
            >
              <UiIcon name="chevronRight" :size="20" />
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.zero-state-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 0;
  overflow: auto;
  padding-inline: 20px;
}

.paid-usage-zero-state {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 24px;
  align-items: center;
  width: 100%;
  max-width: 1000px;
  margin: 0 auto;
  padding: 24px 20px 0;
}

.left-container {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  max-width: 500px;
}

.title-content-container {
  display: flex;
  flex-direction: column;
  margin-bottom: 32px;
  padding-top: 12px;
}

.title-content-container > * {
  margin: 0 0 20px;
}

/* hero-text 的颜色为官网 --color-logo-lockup */
.hero-text {
  color: #191919;
  font-family: var(--font-heading);
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
}

:root[data-theme='dark'] .hero-text {
  color: #fcfcfc;
}

.title-content-container > .subtitle-container {
  max-width: 68ch;
  margin: 0;
  font-size: 14px;
  line-height: 21px;
}

.suggestion-gallery-host {
  position: relative;
  width: 100%;
  margin: 32px 0 16px;
}

.gallery-overlay {
  display: none;
}

.suggestion-card {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  margin: 8px;
  padding: 0;
  border: none;
  background: none;
  text-align: start;
  cursor: pointer;
  transition: transform 0.1s ease-in;
}

.suggestion-card:hover {
  transform: translateY(-4px);
}

.display-media-container {
  position: relative;
  display: flex;
}

.display-media {
  display: block;
  max-width: 100%;
  max-height: 162px;
  border-radius: 8px;
  object-fit: cover;
}

.media-type-icon {
  position: absolute;
  top: 8px;
  left: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 24px;
  background: var(--color-surface-brighter);
  color: var(--color-on-surface);
}

.reference-image {
  position: absolute;
  bottom: 8px;
  left: 8px;
  width: 48px;
  height: 48px;
  border-radius: 8px;
  box-shadow:
    0 1px 3px 1px rgb(0 0 0 / 15%),
    0 1px 2px 0 rgb(0 0 0 / 30%);
  object-fit: cover;
}

.content-text {
  width: 100%;
  margin: 8px 0 12px 8px;
  color: var(--color-on-surface-variant);
  font-size: 12px;
  line-height: 18px;
}

[data-layout='mosaic'] .suggestion-gallery {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 8px;
  width: 100%;
}

[data-layout='mosaic'] .suggestion-card {
  grid-column: span 3;
  align-self: start;
}

[data-layout='mosaic'] .suggestion-card:nth-child(1),
[data-layout='mosaic'] .suggestion-card:nth-child(3) {
  justify-self: end;
}

[data-layout='mosaic'] .suggestion-card:nth-child(1),
[data-layout='mosaic'] .suggestion-card:nth-child(2) {
  align-self: end;
}

[data-layout='mosaic'] .suggestion-card:nth-child(2),
[data-layout='mosaic'] .suggestion-card:nth-child(4) {
  justify-self: start;
}

.gallery-overlay button {
  pointer-events: auto;
}

.left-controls-container,
.right-controls-container {
  position: absolute;
  top: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 162px;
}

.left-controls-container {
  left: 0;
}

.right-controls-container {
  right: 0;
}

[data-layout='single-row'] .gallery-overlay {
  position: absolute;
  top: 0;
  display: flex;
  width: 100%;
  height: 100%;
  pointer-events: none;
}

[data-layout='single-row'] .suggestion-gallery {
  display: flex;
  gap: 8px;
  margin-top: -4px;
  padding-top: 4px;
  overflow-x: auto;
  scrollbar-width: none;
}

[data-layout='single-row'] .suggestion-card {
  flex: 0 0 auto;
  box-sizing: border-box;
  width: 304px;
  height: 240px;
  margin: 0;
  padding: 8px;
  border-radius: 12px;
}

@media screen and (max-width: 600px) {
  .zero-state-content {
    justify-content: flex-start;
    padding: 16px 0 0;
  }

  .paid-usage-zero-state {
    grid-template-columns: minmax(0, 1fr);
    row-gap: 24px;
    padding-inline: 16px;
  }

  .gallery-overlay {
    position: absolute;
    top: 0;
    display: flex;
    width: 100%;
    height: 100%;
    pointer-events: none;
  }

  [data-layout='mosaic'] .suggestion-gallery {
    display: flex;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .suggestion-card {
    flex: 0 0 auto;
  }
}
</style>
