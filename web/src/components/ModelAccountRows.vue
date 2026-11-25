<script setup lang="ts">
import { ref, watch } from 'vue'
import { parseAccountBindings } from '@/accountBindings'
import { useI18n } from '@/i18n'
import { useAppendList } from '@/rows'
import type { Account } from '@/types'
import AccountFilterMenu from './AccountFilterMenu.vue'
import UiIcon from './UiIcon.vue'
import UiSelect from './UiSelect.vue'

// Row 是一行模型与它可用的账户 ID
interface Row {
  model: string
  ids: string[]
}

const props = defineProps<{
  value: string
  models: string[]
  accounts: Account[]
  modelPlaceholder: string
  addLabel: string
}>()
const emit = defineEmits<{ change: [value: string] }>()

const { t } = useI18n()
const fieldClass =
  'w-full min-w-0 rounded-md border border-line bg-canvas px-2.5 py-1.5 text-xs text-white transition focus:border-blue-500 focus:outline-none'

// parse 与服务端一致解析 MODEL_ACCOUNTS，模型 ID 去掉 models/ 前缀
function parse(value: string): Row[] {
  return [...parseAccountBindings(value)].map(([model, ids]) => ({
    model: model.replace(/^models\//, ''),
    ids,
  }))
}

// serialize 只写回已选模型且已选账户的行
function serialize(rows: Row[]): string {
  return rows
    .filter((row) => row.model !== '' && row.ids.length > 0)
    .map((row) => `${row.model}:${row.ids.join('|')}`)
    .join(',')
}

const rows = ref<Row[]>(parse(props.value))
watch(
  () => props.value,
  (value) => {
    if (value !== serialize(rows.value)) rows.value = parse(value)
  },
)

// modelChoices 是一行可选的模型：去掉其他行已选的模型，保留本行不在目录中的取值
function modelChoices(index: number): string[] {
  const current = rows.value[index]!.model
  const taken = new Set(rows.value.map((row) => row.model).filter((_, other) => other !== index))
  const choices = props.models.filter((model) => !taken.has(model))
  return current === '' || choices.includes(current) ? choices : [current, ...choices]
}

// update 修改一行的模型或账户并写回
function update(index: number, change: Partial<Row>): void {
  Object.assign(rows.value[index]!, change)
  emit('change', serialize(rows.value))
}

const reveal = useAppendList('list')

// add 追加一个空行并把它滚入列表、聚焦
function add(): void {
  rows.value.push({ model: '', ids: [] })
  void reveal(true)
}

// remove 删除一行并写回
function remove(index: number): void {
  rows.value.splice(index, 1)
  emit('change', serialize(rows.value))
}
</script>

<template>
  <div class="space-y-2">
    <div ref="list" class="append-list space-y-2">
      <div
        v-for="(row, index) in rows"
        :key="index"
        class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto] items-center gap-1.5"
      >
        <UiSelect
          :model-value="row.model"
          :class="fieldClass"
          :aria-label="modelPlaceholder"
          @update:model-value="update(index, { model: String($event) })"
        >
          <option value="" disabled>{{ modelPlaceholder }}</option>
          <option v-for="model in modelChoices(index)" :key="model" :value="model">
            {{ model }}
          </option>
        </UiSelect>
        <UiIcon name="chevronRight" :size="14" class="text-gray-500" />
        <AccountFilterMenu
          field
          :class="fieldClass"
          :accounts="accounts"
          :selected="row.ids"
          @change="update(index, { ids: $event })"
        />
        <button
          v-tooltip="t('common.delete')"
          type="button"
          class="rounded-md p-1.5 text-gray-500 transition hover:bg-raised hover:text-gray-200"
          :aria-label="t('common.delete')"
          @click="remove(index)"
        >
          <UiIcon name="trash" :size="14" />
        </button>
      </div>
    </div>
    <button
      type="button"
      class="flex items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-xs text-gray-400 transition hover:border-gray-600 hover:text-gray-200"
      @click="add"
    >
      <UiIcon name="add" :size="13" />
      {{ addLabel }}
    </button>
  </div>
</template>
