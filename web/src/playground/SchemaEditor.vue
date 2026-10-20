<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import SchemaEntries from './SchemaEntries.vue'
import { newEntry, parseSchema, schemaText, type SchemaTree } from './schema'

const text = defineModel<string>('text', { required: true })
const props = defineProps<{ path?: string }>()
const emit = defineEmits<{ error: [message: string] }>()
const { t } = useI18n()

const tree = ref<SchemaTree>({ children: [] })
// written 是编辑器最近一次写出的文本，外部文本与它相同时不重新解析
let written: string | undefined

watch(
  text,
  (value) => {
    if (value === written) return
    try {
      tree.value = parseSchema(value)
      emit('error', '')
    } catch (error) {
      emit('error', error instanceof Error ? error.message : String(error))
    }
  },
  { immediate: true },
)

// changed 把编辑结果写回 schema 文本
function changed(): void {
  written = schemaText(tree.value)
  text.value = written
}

// add 在根对象末尾添加属性
function add(): void {
  tree.value.children.push(newEntry())
  changed()
}
</script>

<template>
  <div class="schema-editor">
    <SchemaEntries v-model:entries="tree.children" :path="props.path ?? 'root'" @change="changed" />
    <button type="button" ms-button class="ms-button-borderless" @click="add">
      {{ t('pg.addProperty') }}
    </button>
  </div>
</template>

<style scoped>
.schema-editor {
  width: 100%;
}
</style>
