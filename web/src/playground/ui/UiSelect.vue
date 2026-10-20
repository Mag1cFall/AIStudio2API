<script setup lang="ts">
import { vTooltip } from './tooltip'
import { computed, useSlots, type VNode } from 'vue'
import {
  SelectContent,
  SelectItem,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectViewport,
} from 'reka-ui'
import type { IconName } from './icons'
import UiIcon from './UiIcon.vue'

defineOptions({ inheritAttrs: false })

const model = defineModel<string | number>()
defineProps<{ disabled?: boolean; icon?: IconName }>()
const slots = useSlots()

interface SelectOption {
  value: string | number
  label: string
  disabled: boolean
  title: string
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
      const disabled = node.props?.disabled
      result.push({
        value,
        label,
        disabled: disabled !== undefined && disabled !== false,
        title: String(node.props?.title ?? ''),
      })
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
    <SelectTrigger v-bind="$attrs" class="ms-select-trigger">
      <UiIcon v-if="icon" :name="icon" :size="18" />
      <span class="ms-select-value">{{ selectedLabel }}</span>
      <UiIcon name="chevronDown" :size="20" class="ms-select-arrow" />
    </SelectTrigger>
    <SelectPortal>
      <SelectContent position="popper" :side-offset="0" class="ms-select-panel">
        <SelectViewport @pointerup.capture="keepPrimarySelect">
          <SelectItem
            v-for="(option, index) in options"
            :key="`${index}:${option.value}`"
            v-tooltip.left="option.title"
            :value="String(index)"
            :disabled="option.disabled"
            class="ms-select-option"
          >
            <SelectItemText v-tooltip.overflow="option.label" class="ms-select-option-text">{{
              option.label
            }}</SelectItemText>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>

<style>
.ms-select-trigger {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 32px;
  min-width: 0;
  padding: 0 4px 0 8px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container-high);
  color: var(--color-on-surface);
  cursor: pointer;
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
  text-align: left;
}

.ms-select-trigger:hover {
  border-color: var(--color-v3-outline);
}

.ms-select-trigger[data-state='open'],
.ms-select-trigger:focus-visible {
  border-color: var(--color-v3-text-var);
  outline: none;
}

.ms-select-trigger[data-disabled] {
  background-color: var(--color-v3-surface-container);
  color: var(--color-v3-outline);
  cursor: default;
}

.ms-select-value {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ms-select-arrow {
  flex-shrink: 0;
  color: var(--color-v3-text);
  transition: transform 80ms linear;
}

.ms-select-trigger[data-state='open'] .ms-select-arrow {
  transform: rotate(180deg);
}

.ms-select-panel {
  position: relative;
  z-index: 1000;
  box-sizing: border-box;
  min-width: var(--reka-select-trigger-width);
  max-width: var(--reka-select-content-available-width);
  max-height: min(275px, var(--reka-select-content-available-height));
  overflow: hidden;
  padding: 0 4px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 0 0 8px 8px;
  background-color: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-dropdown);
  color: var(--color-v3-text);
  transform-origin: top center;
  animation: ms-select-enter 120ms cubic-bezier(0, 0, 0.2, 1) forwards;
}

.ms-select-panel[data-side='top'] {
  border-radius: 4px 4px 0 0;
  transform-origin: bottom center;
}

@keyframes ms-select-enter {
  from {
    opacity: 0;
    transform: scaleY(0.8);
  }

  to {
    opacity: 1;
    transform: none;
  }
}

.ms-select-option {
  display: flex;
  align-items: center;
  min-height: 36px;
  margin: 4px 0;
  padding: 6px 8px;
  border-radius: 4px;
  color: rgb(0 0 0 / 87%);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  white-space: nowrap;
  cursor: pointer;
  outline: none;
  user-select: none;
}

:root[data-theme='dark'] .ms-select-option {
  color: #fff;
}

.ms-select-option[data-highlighted],
.ms-select-option[data-state='checked'] {
  background-color: var(--color-v3-hover);
}

.ms-select-option[data-disabled] {
  color: var(--color-v3-text-disable);
  cursor: default;
}

.ms-select-option-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
