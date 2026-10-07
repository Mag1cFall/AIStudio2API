<script setup lang="ts">
import { computed, useSlots, type VNode } from 'vue'
import {
  SelectContent,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectViewport,
} from 'reka-ui'
import UiIcon from './UiIcon.vue'

defineOptions({ inheritAttrs: false })

const model = defineModel<string | number>()
defineProps<{ disabled?: boolean }>()
const slots = useSlots()

interface SelectOption {
  value: string | number
  label: string
}

// vnodeText 拼接 option 子节点中的文本
function vnodeText(children: unknown): string {
  if (typeof children === 'string') return children
  if (Array.isArray(children))
    return children.map((child) => vnodeText((child as VNode)?.children ?? child)).join('')
  return ''
}

// collectOptions 从默认插槽展开 option 与 v-for 片段
function collectOptions(nodes: VNode[], result: SelectOption[]): SelectOption[] {
  for (const node of nodes) {
    if (node.type === 'option') {
      const label = vnodeText(node.children).trim()
      const value = (node.props?.value as string | number | undefined) ?? label
      result.push({ value, label })
    } else if (Array.isArray(node.children)) {
      collectOptions(node.children as VNode[], result)
    }
  }
  return result
}

const options = computed(() => collectOptions(slots.default?.() ?? [], []))
const selectedIndex = computed(() =>
  options.value.findIndex((option) => option.value === model.value),
)
const selectedLabel = computed(() => options.value[selectedIndex.value]?.label ?? '')

// keepPrimarySelect 拦截非左键松开，中键滚动结束时不选中停留项
function keepPrimarySelect(event: PointerEvent) {
  if (event.button !== 0) event.stopPropagation()
}
// selectedKey 以选项下标作为 Reka 选项值，使空字符串与数字值都能被选择
const selectedKey = computed({
  get: () => (selectedIndex.value < 0 ? '' : String(selectedIndex.value)),
  set: (key: string) => {
    const option = key === '' ? undefined : options.value[Number(key)]
    if (option) model.value = option.value
  },
})
</script>

<template>
  <SelectRoot v-model="selectedKey" :disabled="disabled">
    <SelectTrigger
      v-bind="$attrs"
      class="group flex items-center justify-between gap-2 text-left transition-colors hover:border-gray-600 disabled:opacity-50 data-[state=open]:border-blue-500"
    >
      <span class="min-w-0 flex-1 truncate">{{ selectedLabel }}</span>
      <UiIcon
        name="chevronDown"
        :size="12"
        class="text-gray-500 transition-transform duration-150 group-data-[state=open]:rotate-180"
      />
    </SelectTrigger>
    <SelectPortal>
      <SelectContent
        position="popper"
        :side-offset="4"
        class="ui-select-content z-[70] max-h-[min(280px,var(--reka-select-content-available-height))] min-w-[var(--reka-select-trigger-width)] overflow-hidden rounded-md border border-line bg-panel text-xs shadow-xl shadow-black/40"
      >
        <SelectViewport class="py-1" @pointerup.capture="keepPrimarySelect">
          <SelectItem
            v-for="(option, index) in options"
            :key="`${index}:${option.value}`"
            :value="String(index)"
            class="flex cursor-pointer items-center justify-between gap-3 px-3 py-1.5 whitespace-nowrap text-gray-300 outline-none select-none data-[highlighted]:bg-raised data-[highlighted]:text-white data-[state=checked]:font-medium data-[state=checked]:text-blue-300"
          >
            <SelectItemText>{{ option.label }}</SelectItemText>
            <SelectItemIndicator>
              <UiIcon name="check" :size="12" class="text-blue-400" />
            </SelectItemIndicator>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
