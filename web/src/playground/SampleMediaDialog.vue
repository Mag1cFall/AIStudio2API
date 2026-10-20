<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { sampleCategories, sampleVideos, type SampleVideo } from './sample-media'

// SampleSelection 是选中的一张示例图片地址或一个示例视频
export type SampleSelection = { image: string } | { video: SampleVideo }

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ select: [selection: SampleSelection] }>()
const { t } = useI18n()
// selected 与官网一致只保留一个选中项
const selected = ref<SampleSelection>()

watch(open, (value) => {
  if (value) selected.value = undefined
})

// isSelected 返回该示例是否为选中项
function isSelected(item: SampleSelection): boolean {
  const current = selected.value
  if (current === undefined) return false
  return 'image' in item
    ? 'image' in current && current.image === item.image
    : 'video' in current && current.video.id === item.video.id
}

// add 把选中项加入输入框并关闭
function add(): void {
  if (selected.value === undefined) return
  emit('select', selected.value)
  open.value = false
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.sampleMedia')" width="712px">
    <div class="category-name">{{ t('pg.sampleVideos') }}</div>
    <div class="sample-media">
      <button
        v-for="video in sampleVideos"
        :key="video.id"
        type="button"
        class="sample-video-wrapper"
        :class="{ selected: isSelected({ video }) }"
        :aria-label="t('pg.selectSampleVideo').replace('{name}', video.name)"
        @click="selected = { video }"
      >
        <img class="sample-video" :src="video.thumbnail" alt="" />
        <span class="checkmark"><UiIcon name="check" :size="18" /></span>
        <span class="duration">{{ video.duration }}</span>
        <span class="name">{{ video.name }}</span>
      </button>
    </div>
    <template v-for="category in sampleCategories" :key="category.name">
      <div class="category-name">{{ t(category.label) }}</div>
      <div class="sample-media">
        <button
          v-for="image in category.images"
          :key="image"
          type="button"
          class="sample-image-wrapper"
          :class="{ selected: isSelected({ image }) }"
          :aria-label="t('pg.selectSampleImage').replace('{category}', t(category.label))"
          @click="selected = { image }"
        >
          <img class="sample-image" :src="image" alt="" />
          <span class="checkmark"><UiIcon name="check" :size="18" /></span>
        </button>
      </div>
    </template>
    <template #actions>
      <div class="add-text">{{ t('pg.sampleMediaHint') }}</div>
      <button
        v-tooltip="selected ? '' : t('pg.sampleMediaHint')"
        type="button"
        ms-button
        class="ms-button-primary"
        aria-label="add to prompt"
        :aria-disabled="selected === undefined"
        @click="add"
      >
        {{ t('pg.addToPrompt') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.category-name {
  margin-bottom: 12px;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
}

.sample-media {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 16px;
  width: min(80vw - 44px, 664px);
  margin-bottom: 12px;
}

.sample-media:last-of-type {
  margin-bottom: 0;
}

.sample-image-wrapper,
.sample-video-wrapper {
  position: relative;
  width: 120px;
  padding: 0;
  border: none;
  border-radius: 4px;
  outline: 1px solid var(--color-v3-outline);
  outline-offset: -1px;
  background: none;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.sample-image-wrapper.selected,
.sample-video-wrapper.selected {
  outline: 4px solid var(--color-v3-text);
  outline-offset: -4px;
}

.sample-image-wrapper:focus-visible,
.sample-video-wrapper:focus-visible {
  outline: 4px solid var(--color-v3-outline-accent);
  outline-offset: -4px;
}

.checkmark {
  position: absolute;
  top: 5px;
  left: 5px;
  display: none;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--color-v3-text);
  color: var(--color-v3-surface-container);
}

.selected .checkmark {
  display: flex;
}

.sample-image-wrapper {
  height: 120px;
}

.sample-image {
  display: block;
  width: 100%;
  height: 100%;
  border-radius: 4px;
  object-fit: cover;
}

.sample-video-wrapper {
  display: flex;
  flex-direction: column;
  color: var(--color-v3-text);
  font-size: 14px;
}

.sample-video {
  width: 100%;
  height: 80px;
  border-radius: 4px 4px 0 0;
  object-fit: cover;
}

.duration {
  position: absolute;
  top: 55px;
  right: 7px;
  display: flex;
  justify-content: center;
  width: 33px;
  height: 20px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 4px;
  background-color: var(--color-v3-surface-container);
  font-size: 10px;
  line-height: 20px;
}

.name {
  display: flex;
  padding: 10px 16px 16px;
  font-size: 14px;
  line-height: 21px;
}

.add-text {
  margin-right: auto;
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
}
</style>
