<script setup lang="ts" generic="Item extends { key: number }">
import { onUnmounted, ref, watch } from 'vue'
import UiCollapse from '@/components/UiCollapse.vue'

// wrap 表示各段收在步骤组主体中，open 表示步骤组已展开
const props = defineProps<{ items: Item[]; wrap: boolean; open: boolean }>()
defineSlots<{ default(props: Item): unknown }>()
const body = ref<HTMLElement>()
const content = ref<HTMLElement>()
// fadeTop 与 fadeBottom 表示步骤组主体上方与下方还有可滚动的内容
const fadeTop = ref(false)
const fadeBottom = ref(false)

// measure 按滚动位置更新上下边缘的渐隐
function measure(): void {
  const element = body.value
  if (element === undefined) return
  fadeTop.value = element.scrollTop > 0
  fadeBottom.value = element.scrollTop + element.clientHeight < element.scrollHeight - 1
}
const observer = new ResizeObserver(measure)
watch(content, (element) => {
  observer.disconnect()
  if (element) observer.observe(element)
})
onUnmounted(() => observer.disconnect())
</script>

<template>
  <UiCollapse v-if="props.wrap" :open="props.open">
    <div
      ref="body"
      class="step-group-body"
      :data-fade-top="fadeTop || undefined"
      :data-fade-bottom="fadeBottom || undefined"
      @scroll="measure"
    >
      <div ref="content" class="step-group-content">
        <template v-for="item in props.items" :key="item.key"><slot v-bind="item" /></template>
      </div>
    </div>
  </UiCollapse>
  <template v-else>
    <template v-for="item in props.items" :key="item.key"><slot v-bind="item" /></template>
  </template>
</template>

<style scoped>
/* step-group-body 为展开的步骤组主体：与组标题的间距、限高滚动与上下边缘渐隐取 dsh ChatGroupSeat */
.step-group-body {
  margin-top: 8px;
  max-height: min(400px, 50vh);
  overflow-y: auto;
  overscroll-behavior-y: auto;
}

.step-group-body[data-fade-top] {
  mask-image: linear-gradient(transparent 0, #000 24px 100%);
}

.step-group-body[data-fade-bottom] {
  mask-image: linear-gradient(#000 0 calc(100% - 24px), transparent 100%);
}

.step-group-body[data-fade-top][data-fade-bottom] {
  mask-image: linear-gradient(transparent 0, #000 24px calc(100% - 24px), transparent 100%);
}

.step-group-content {
  display: flow-root;
}
</style>
