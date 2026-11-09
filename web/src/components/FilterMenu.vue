<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import UiIcon from './UiIcon.vue'
import { selectTriggerClass } from './UiSelect.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  label: string
  options: string[]
  selected: string[]
  format: (value: string) => string
  searchLabel: string
  emptyLabel: string
  clearLabel: string
  disabled?: boolean
  // field 表示触发器使用表单下拉框的样式
  field?: boolean
}>()
const emit = defineEmits<{ change: [values: string[]] }>()

const id = useId()
const query = ref('')
const visible = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const values = [...new Set([...props.selected, ...props.options])]
  return needle === ''
    ? values
    : values.filter((value) => props.format(value).toLowerCase().includes(needle))
})

// toggle 切换一个取值的选中状态
function toggle(value: string, checked: boolean): void {
  emit(
    'change',
    checked ? [...props.selected, value] : props.selected.filter((item) => item !== value),
  )
}
</script>

<template>
  <PopoverRoot @update:open="(value: boolean) => value && (query = '')">
    <PopoverTrigger
      v-bind="$attrs"
      :class="
        field
          ? selectTriggerClass
          : [
              'flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs transition-colors data-[state=open]:border-blue-500',
              selected.length > 0
                ? 'border-blue-500/50 bg-blue-600/10 text-blue-200'
                : 'border-line bg-canvas text-gray-300 hover:border-gray-600',
            ]
      "
    >
      <span class="min-w-0 truncate" :class="{ 'flex-1': field }">{{ label }}</span>
      <span
        v-if="selected.length > 0"
        class="rounded bg-blue-600/30 px-1 text-xs text-blue-100 tabular-nums"
        >{{ selected.length }}</span
      >
      <UiIcon name="chevronDown" :size="12" class="text-gray-500" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="start"
        :side-offset="6"
        :collision-padding="12"
        class="ui-popover z-[80] w-80 max-w-[var(--reka-popover-content-available-width)] min-w-[var(--reka-popover-trigger-width)] rounded-lg border border-line bg-panel p-2 shadow-xl shadow-black/40"
      >
        <label class="relative mb-2 block">
          <span class="sr-only">{{ searchLabel }}</span>
          <UiIcon
            name="search"
            :size="13"
            class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-gray-500"
          />
          <input
            v-model="query"
            type="search"
            :placeholder="searchLabel"
            class="w-full rounded-md border border-line bg-canvas py-1.5 pr-2 pl-7 text-xs text-white focus:border-blue-500 focus:outline-none"
          />
        </label>
        <fieldset class="max-h-64 min-w-0 overflow-y-auto">
          <legend class="sr-only">{{ label }}</legend>
          <p v-if="visible.length === 0" class="px-2 py-3 text-center text-xs text-gray-500">
            {{ emptyLabel }}
          </p>
          <label
            v-for="(value, index) in visible"
            :key="value"
            :for="`${id}-${index}`"
            class="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-xs text-gray-300 hover:bg-raised"
          >
            <input
              :id="`${id}-${index}`"
              type="checkbox"
              :checked="selected.includes(value)"
              :disabled="disabled"
              @change="toggle(value, ($event.target as HTMLInputElement).checked)"
            />
            <span v-tooltip.overflow="format(value)" class="min-w-0 flex-1 truncate">{{
              format(value)
            }}</span>
          </label>
        </fieldset>
        <div v-if="selected.length > 0" class="mt-2 border-t border-line pt-2">
          <button
            type="button"
            class="w-full rounded px-2 py-1 text-left text-xs text-gray-400 hover:bg-raised hover:text-white"
            :disabled="disabled"
            @click="emit('change', [])"
          >
            {{ clearLabel }}
          </button>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
