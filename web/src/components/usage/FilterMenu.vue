<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from '../UiIcon.vue'

const props = defineProps<{
  label: string
  options: string[]
  selected: string[]
  format: (value: string) => string
}>()
const emit = defineEmits<{ change: [values: string[]] }>()

const { t } = useI18n()
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
      class="flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs transition-colors data-[state=open]:border-blue-500"
      :class="
        selected.length > 0
          ? 'border-blue-500/50 bg-blue-600/10 text-blue-200'
          : 'border-line bg-canvas text-gray-300 hover:border-gray-600'
      "
    >
      {{ label }}
      <span
        v-if="selected.length > 0"
        class="rounded bg-blue-600/30 px-1 text-[10px] text-blue-100 tabular-nums"
        >{{ selected.length }}</span
      >
      <UiIcon name="chevronDown" :size="12" class="text-gray-500" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="start"
        :side-offset="6"
        :collision-padding="12"
        class="ui-popover z-[60] w-72 rounded-lg border border-line bg-panel p-2 shadow-xl shadow-black/40"
      >
        <label class="relative mb-2 block">
          <span class="sr-only">{{ t('usage.filterSearch') }}</span>
          <UiIcon
            name="search"
            :size="13"
            class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-gray-500"
          />
          <input
            v-model="query"
            type="search"
            :placeholder="t('usage.filterSearch')"
            class="w-full rounded-md border border-line bg-canvas py-1.5 pr-2 pl-7 text-xs text-white focus:border-blue-500 focus:outline-none"
          />
        </label>
        <fieldset class="max-h-64 overflow-y-auto">
          <legend class="sr-only">{{ label }}</legend>
          <p v-if="visible.length === 0" class="px-2 py-3 text-center text-xs text-gray-500">
            {{ t('usage.filterNone') }}
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
              @change="toggle(value, ($event.target as HTMLInputElement).checked)"
            />
            <span class="min-w-0 flex-1 truncate">{{ format(value) }}</span>
          </label>
        </fieldset>
        <div v-if="selected.length > 0" class="mt-2 border-t border-line pt-2">
          <button
            type="button"
            class="w-full rounded px-2 py-1 text-left text-xs text-gray-400 hover:bg-raised hover:text-white"
            @click="emit('change', [])"
          >
            {{ t('usage.filtersClear') }}
          </button>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
