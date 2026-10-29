<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'

// SpeechCarouselSection 是语音页空白态的一组横向卡片：标题、翻页按钮、新建卡片与示例卡片
const props = defineProps<{
  title: string
  icon: IconName
  regionLabel: string
  previousLabel: string
  nextLabel: string
  actionLabel: string
  actionUnavailable?: string
}>()
const emit = defineEmits<{ action: [] }>()
const carousel = ref<HTMLElement>()
const fadeLeft = ref(false)
const fadeRight = ref(false)

// updateFades 按轮播滚动位置显示两侧渐隐与翻页按钮状态
function updateFades(): void {
  const element = carousel.value!
  fadeLeft.value = element.scrollLeft > 1
  fadeRight.value = element.scrollLeft + element.clientWidth < element.scrollWidth - 1
}

// scrollCarousel 按一屏宽度翻页
function scrollCarousel(direction: -1 | 1): void {
  const element = carousel.value!
  element.scrollBy({
    left: direction * element.clientWidth * 0.8,
    behavior: 'smooth',
  })
}

// runAction 在新建卡片可用时触发新建
function runAction(): void {
  if (!props.actionUnavailable) emit('action')
}

onMounted(() => {
  updateFades()
  window.addEventListener('resize', updateFades)
})
onUnmounted(() => window.removeEventListener('resize', updateFades))
</script>

<template>
  <section class="zero-state-section">
    <div class="section-header">
      <div class="section-title-group">
        <UiIcon :name="icon" :size="16" class="section-icon" />
        <h2 class="section-title">{{ title }}</h2>
      </div>
      <div class="carousel-controls">
        <button
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon carousel-nav-button"
          :aria-label="previousLabel"
          :aria-disabled="!fadeLeft"
          @click="scrollCarousel(-1)"
        >
          <UiIcon name="chevronLeft" :size="18" />
        </button>
        <button
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon carousel-nav-button"
          :aria-label="nextLabel"
          :aria-disabled="!fadeRight"
          @click="scrollCarousel(1)"
        >
          <UiIcon name="chevronRight" :size="18" />
        </button>
      </div>
    </div>
    <div
      ref="carousel"
      role="region"
      class="cards-carousel"
      :class="{ 'fade-left': fadeLeft, 'fade-right': fadeRight }"
      :aria-label="regionLabel"
      @scroll="updateFades"
    >
      <button
        v-tooltip.below="actionUnavailable"
        type="button"
        class="action-card"
        :aria-disabled="Boolean(actionUnavailable)"
        @click="runAction"
      >
        <span class="action-card-icon"><UiIcon name="add" :size="20" /></span>
        <span class="action-card-label">{{ actionLabel }}</span>
      </button>
      <slot />
    </div>
  </section>
</template>

<style scoped>
.zero-state-section {
  display: flex;
  flex-direction: column;
  width: 100%;
  margin-bottom: 40px;
}

.zero-state-section:last-child {
  margin-bottom: 0;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 28px;
  margin-bottom: 12px;
}

.section-title-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.section-icon {
  color: var(--color-v3-text-var);
}

.section-title {
  margin: 0;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.carousel-controls {
  display: flex;
  align-items: center;
  gap: 4px;
}

.carousel-nav-button {
  width: 28px;
  height: 28px;
  color: var(--color-v3-text-var);
}

.carousel-nav-button[aria-disabled='true'] {
  background: transparent;
  color: var(--color-v3-text-disable);
  cursor: default;
}

.cards-carousel {
  display: flex;
  align-items: stretch;
  gap: 12px;
  width: 100%;
  margin: -4px 0 0;
  padding: 4px 2px 8px;
  overflow-x: auto;
  scroll-behavior: smooth;
  scroll-padding-inline: 2px;
  scroll-snap-type: x mandatory;
  scrollbar-width: none;
}

.cards-carousel::-webkit-scrollbar {
  display: none;
}

.cards-carousel.fade-left {
  mask-image: linear-gradient(90deg, transparent, #000 3%);
}

.cards-carousel.fade-right {
  mask-image: linear-gradient(90deg, #000 97%, transparent);
}

.cards-carousel.fade-left.fade-right {
  mask-image: linear-gradient(90deg, transparent, #000 3%, #000 97%, transparent);
}

.cards-carousel > .action-card,
.cards-carousel > :slotted(*) {
  flex: 0 0 180px;
  height: 148px;
  scroll-snap-align: start;
}

.action-card {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 148px;
  padding: 16px;
  border: 1.5px dashed var(--color-v3-outline);
  border-radius: 12px;
  outline: none;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.23, 1, 0.32, 1);
}

.action-card:not([aria-disabled='true']):hover {
  border-color: var(--color-v3-outline-active);
  background: var(--color-v3-surface-container-high);
  transform: translateY(-2px);
}

.action-card[aria-disabled='true'] {
  cursor: default;
}

.action-card:focus-visible {
  border-color: var(--color-v3-outline-active);
}

.action-card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  margin-bottom: 10px;
  border-radius: 50%;
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text-var);
}

.action-card-label {
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}
</style>
