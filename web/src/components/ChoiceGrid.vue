<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'
import type { IconName } from '@/icons'
import UiIcon from './UiIcon.vue'

// ChoiceItem 是一个选项：svg 为品牌图标的 SVG 原文，icon 为界面图标名
export interface ChoiceItem {
  id: string
  label: string
  svg?: string
  icon?: IconName
}

const props = defineProps<{ groups: { label: string; items: ChoiceItem[] }[]; selected: string }>()
const emit = defineEmits<{ select: [id: string] }>()

// tileWidth 与 gap 是选项的最小宽度与间距，单位 px
const tileWidth = 104
const gap = 8

const root = useTemplateRef<HTMLElement>('root')
const width = ref(0)
let observer: ResizeObserver | undefined
onMounted(() => {
  width.value = root.value!.clientWidth
  observer = new ResizeObserver(([entry]) => (width.value = entry!.contentRect.width))
  observer.observe(root.value!)
})
onBeforeUnmount(() => observer?.disconnect())

// capacity 是一行能放下的选项数
const capacity = computed(() => Math.max(1, Math.floor((width.value + gap) / (tileWidth + gap))))

// columns 返回 count 个选项的列数：不超过一行容量、各行项数相差不超过一的最大列数
function columns(count: number): number {
  for (let value = Math.min(capacity.value, count); value > 1; value--) {
    const rest = count % value
    if (rest === 0 || rest >= value - 1) return value
  }
  return 1
}

// layouts 是各组的列数与共用的选项宽度：选项宽度按列数最多的一组铺满整行，其余各组按同一宽度对齐
const layouts = computed(() => {
  const counts = props.groups.map((group) => columns(group.items.length))
  const widest = Math.max(...counts)
  const track = (width.value - (widest - 1) * gap) / widest
  return props.groups.map((group, index) => ({
    ...group,
    style: { gridTemplateColumns: `repeat(${counts[index]}, ${track}px)`, gap: `${gap}px` },
  }))
})
</script>

<!-- ChoiceGrid 是分组的图标选项网格：各组选项等宽并按列对齐，每组按项数取列数使各行项数均衡，只有一组时不显示组名 -->
<template>
  <div ref="root" class="space-y-3">
    <div v-for="group in layouts" :key="group.label" role="group" :aria-label="group.label">
      <div v-if="groups.length > 1" class="mb-1.5 text-xs text-gray-500">{{ group.label }}</div>
      <div class="grid" :style="group.style">
        <button
          v-for="item in group.items"
          :key="item.id"
          type="button"
          class="flex min-h-16 min-w-0 flex-col items-center justify-center gap-1.5 rounded-lg border px-2 py-2.5 text-center text-[13px] leading-tight transition"
          :class="
            selected === item.id
              ? 'border-blue-500 bg-blue-600/15 text-white'
              : 'border-line text-gray-300 hover:bg-raised hover:text-white'
          "
          :aria-pressed="selected === item.id"
          @click="emit('select', item.id)"
        >
          <span
            v-if="item.svg"
            class="choice-svg flex h-5 w-5 items-center justify-center text-[20px]"
            aria-hidden="true"
            v-html="item.svg"
          ></span>
          <UiIcon v-else-if="item.icon" :name="item.icon" :size="20" />
          <span class="max-w-full break-words">{{ item.label }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.choice-svg :deep(svg) {
  width: 1em;
  height: 1em;
}
</style>
