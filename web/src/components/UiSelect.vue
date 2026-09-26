<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useAttrs, useSlots, type VNode } from 'vue'
import UiIcon from './UiIcon.vue'

defineOptions({ inheritAttrs: false })

const model = defineModel<string | number>()
const props = defineProps<{ disabled?: boolean }>()
const attrs = useAttrs()
const slots = useSlots()

interface SelectOption {
  value: string | number
  label: string
}

const open = ref(false)
const active = ref(-1)
const trigger = ref<HTMLButtonElement>()
const list = ref<HTMLUListElement>()
const position = ref({ top: 0, left: 0, width: 0, maxHeight: 280, above: false })
const listId = `ui-select-${Math.random().toString(36).slice(2)}`

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

// place 按触发器位置计算浮层，空间不足时向上展开
function place(): void {
  const rect = trigger.value?.getBoundingClientRect()
  if (!rect) return
  const below = window.innerHeight - rect.bottom - 8
  const above = rect.top - 8
  const upward = below < 160 && above > below
  position.value = {
    top: upward ? rect.top - 4 : rect.bottom + 4,
    left: rect.left,
    width: rect.width,
    maxHeight: Math.min(280, upward ? above : below),
    above: upward,
  }
}

function close(): void {
  open.value = false
  window.removeEventListener('scroll', onViewportChange, true)
  window.removeEventListener('resize', onViewportChange)
  document.removeEventListener('pointerdown', onOutside, true)
}

function onViewportChange(event: Event): void {
  if (event.type === 'scroll' && list.value?.contains(event.target as Node)) return
  close()
}

function onOutside(event: PointerEvent): void {
  const target = event.target as Node
  if (!trigger.value?.contains(target) && !list.value?.contains(target)) close()
}

async function show(): Promise<void> {
  if (props.disabled || open.value || options.value.length === 0) return
  place()
  open.value = true
  active.value = Math.max(selectedIndex.value, 0)
  window.addEventListener('scroll', onViewportChange, true)
  window.addEventListener('resize', onViewportChange)
  document.addEventListener('pointerdown', onOutside, true)
  await nextTick()
  scrollActive()
}

function choose(index: number): void {
  const option = options.value[index]
  if (option) model.value = option.value
  close()
  trigger.value?.focus()
}

function scrollActive(): void {
  list.value
    ?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)
    ?.scrollIntoView({ block: 'nearest' })
}

function move(step: number): void {
  const count = options.value.length
  if (count === 0) return
  active.value = (active.value + step + count) % count
  void nextTick(scrollActive)
}

// onKeydown 提供与原生下拉一致的键盘操作
function onKeydown(event: KeyboardEvent): void {
  if (props.disabled) return
  if (!open.value) {
    if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {
      event.preventDefault()
      void show()
    }
    return
  }
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      move(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      move(-1)
      break
    case 'Home':
      event.preventDefault()
      active.value = 0
      void nextTick(scrollActive)
      break
    case 'End':
      event.preventDefault()
      active.value = options.value.length - 1
      void nextTick(scrollActive)
      break
    case 'Enter':
    case ' ':
      event.preventDefault()
      choose(active.value)
      break
    case 'Escape':
      event.preventDefault()
      close()
      break
    case 'Tab':
      close()
      break
    default:
      if (event.key.length === 1) {
        const key = event.key.toLowerCase()
        const start = active.value + 1
        const ordered = [...options.value.slice(start), ...options.value.slice(0, start)]
        const found = ordered.findIndex((option) => option.label.toLowerCase().startsWith(key))
        if (found >= 0) {
          active.value = (start + found) % options.value.length
          void nextTick(scrollActive)
        }
      }
  }
}

onBeforeUnmount(close)
</script>

<template>
  <button
    ref="trigger"
    v-bind="attrs"
    type="button"
    role="combobox"
    :aria-expanded="open"
    :aria-controls="listId"
    aria-haspopup="listbox"
    :disabled="disabled"
    class="ui-select flex items-center justify-between gap-2 text-left transition-colors hover:border-[#4b5563] disabled:opacity-50"
    :class="{ 'border-blue-500!': open }"
    @click="open ? close() : show()"
    @keydown="onKeydown"
  >
    <span class="min-w-0 flex-1 truncate">{{ selectedLabel }}</span>
    <UiIcon
      name="chevronDown"
      :size="12"
      class="shrink-0 text-gray-500 transition-transform duration-150"
      :class="{ 'rotate-180': open }"
    />
  </button>
  <Teleport to="body">
    <Transition name="ui-select">
      <ul
        v-if="open"
        :id="listId"
        ref="list"
        role="listbox"
        class="fixed z-[70] overflow-auto rounded-md border border-[#30363d] bg-[#161b22] py-1 text-xs shadow-xl shadow-black/40"
        :class="position.above ? 'origin-bottom -translate-y-full' : 'origin-top'"
        :style="{
          top: `${position.top}px`,
          left: `${position.left}px`,
          minWidth: `${position.width}px`,
          maxHeight: `${position.maxHeight}px`,
        }"
      >
        <li
          v-for="(option, index) in options"
          :key="`${option.value}`"
          role="option"
          :data-index="index"
          :aria-selected="index === selectedIndex"
          class="flex cursor-pointer items-center justify-between gap-3 px-3 py-1.5 whitespace-nowrap transition-colors"
          :class="[
            index === active ? 'bg-[#1f6feb33] text-white' : 'text-gray-300',
            index === selectedIndex ? 'font-medium text-blue-300' : '',
          ]"
          @pointerenter="active = index"
          @click="choose(index)"
        >
          <span>{{ option.label }}</span>
          <UiIcon v-if="index === selectedIndex" name="check" :size="12" class="text-blue-400" />
        </li>
      </ul>
    </Transition>
  </Teleport>
</template>
