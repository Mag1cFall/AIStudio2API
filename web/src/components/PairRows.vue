<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'

interface Option {
  value: string
  label: string
}

const props = defineProps<{
  value: string
  leftOptions?: Option[]
  leftPlaceholder: string
  rightOptions: Option[]
  rightPlaceholder: string
  addLabel: string
}>()
const emit = defineEmits<{ change: [value: string] }>()

const { t } = useI18n()
const fieldClass =
  'w-full min-w-0 rounded-md border border-line bg-canvas px-2.5 py-1.5 text-xs text-white transition focus:border-blue-500 focus:outline-none'

// parse 把逗号分隔的 左:右 拆成行，按最后一个冒号分隔
function parse(value: string): [string, string][] {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => {
      const index = item.lastIndexOf(':')
      return [item.slice(0, Math.max(index, 0)).trim(), item.slice(index + 1).trim()]
    })
}

// serialize 只写回左右都已填写的行
function serialize(rows: [string, string][]): string {
  return rows
    .filter(([left, right]) => left !== '' && right !== '')
    .map(([left, right]) => `${left}:${right}`)
    .join(',')
}

const rows = ref<[string, string][]>(parse(props.value))
watch(
  () => props.value,
  (value) => {
    if (value !== serialize(rows.value)) rows.value = parse(value)
  },
)

// withCurrent 在选项中补上行内已有但不在列表中的取值，例如带思考后缀的模型
function withCurrent(options: Option[], value: string): Option[] {
  return value === '' || options.some((option) => option.value === value)
    ? options
    : [{ value, label: value }, ...options]
}

// update 修改一行的左值或右值并写回
function update(index: number, side: 0 | 1, value: string): void {
  rows.value[index]![side] = value
  emit('change', serialize(rows.value))
}

// remove 删除一行并写回
function remove(index: number): void {
  rows.value.splice(index, 1)
  emit('change', serialize(rows.value))
}
</script>

<template>
  <div class="space-y-2">
    <div
      v-for="(row, index) in rows"
      :key="index"
      class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto] items-center gap-1.5"
    >
      <UiSelect
        v-if="leftOptions"
        :model-value="row[0]"
        :class="fieldClass"
        :aria-label="leftPlaceholder"
        @update:model-value="update(index, 0, String($event))"
      >
        <option value="" disabled>{{ leftPlaceholder }}</option>
        <option
          v-for="option in withCurrent(leftOptions, row[0])"
          :key="option.value"
          :value="option.value"
        >
          {{ option.label }}
        </option>
      </UiSelect>
      <input
        v-else
        :value="row[0]"
        :class="fieldClass"
        :placeholder="leftPlaceholder"
        :aria-label="leftPlaceholder"
        autocomplete="off"
        spellcheck="false"
        @change="update(index, 0, ($event.target as HTMLInputElement).value.trim())"
      />
      <UiIcon name="chevronRight" :size="14" class="text-gray-500" />
      <UiSelect
        :model-value="row[1]"
        :class="fieldClass"
        :aria-label="rightPlaceholder"
        @update:model-value="update(index, 1, String($event))"
      >
        <option value="" disabled>{{ rightPlaceholder }}</option>
        <option
          v-for="option in withCurrent(rightOptions, row[1])"
          :key="option.value"
          :value="option.value"
        >
          {{ option.label }}
        </option>
      </UiSelect>
      <button
        type="button"
        class="rounded-md p-1.5 text-gray-500 transition hover:bg-raised hover:text-gray-200"
        :aria-label="t('common.delete')"
        @click="remove(index)"
      >
        <UiIcon name="trash" :size="14" />
      </button>
    </div>
    <button
      type="button"
      class="flex items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-xs text-gray-400 transition hover:border-gray-600 hover:text-gray-200"
      @click="rows.push(['', ''])"
    >
      <UiIcon name="add" :size="13" />
      {{ addLabel }}
    </button>
  </div>
</template>
