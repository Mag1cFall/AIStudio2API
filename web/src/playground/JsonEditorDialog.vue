<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import SchemaEditor from './SchemaEditor.vue'
import {
  functionsText,
  orderedFunctionsText,
  parseFunctions,
  parseSchema,
  schemaIssue,
  type FunctionDeclaration,
} from './schema'

const open = defineModel<boolean>('open', { required: true })
const text = defineModel<string>('text', { required: true })
const props = defineProps<{ kind: 'schema' | 'functions' }>()
const { t } = useI18n()

// links 是说明文字中的链接
const links = {
  schema: 'https://spec.openapis.org/oas/v3.0.3#schema',
  schemaDocs: 'https://ai.google.dev/gemini-api/docs/structured-output',
  functionDocs: 'https://ai.google.dev/gemini-api/docs/function-calling',
}

const mode = ref<'code' | 'visual'>('visual')
const draft = ref('')
const functions = ref<FunctionDeclaration[]>([])
const visualError = ref('')
const saveError = ref('')
const title = computed(() =>
  props.kind === 'schema' ? t('pg.structuredOutput') : t('pg.functionDeclarations'),
)
// description 把说明拆成文字与链接片段
const description = computed(() => {
  const source = props.kind === 'schema' ? t('pg.schemaDescription') : t('pg.functionsDescription')
  return source.split(/(\{schema\}|\{docs\})/).map((part) => {
    if (part === '{schema}') return { text: t('pg.openapiSchema'), href: links.schema }
    if (part === '{docs}')
      return {
        text: t('pg.apiDocumentation'),
        href: props.kind === 'schema' ? links.schemaDocs : links.functionDocs,
      }
    return { text: part }
  })
})

// parseError 返回代码编辑器中的草稿不是有效 schema 或函数声明数组时的说明
const parseError = computed(() => {
  if (mode.value === 'visual') return visualError.value
  try {
    if (props.kind === 'schema') parseSchema(draft.value)
    else parseFunctions(draft.value)
    return ''
  } catch (error) {
    return error instanceof Error ? error.message : String(error)
  }
})

// visualText 把可视化编辑结果写为文本，函数声明缺少名称或描述时抛出说明
function visualText(): string {
  return props.kind === 'schema' ? draft.value : functionsText(functions.value)
}

// enterVisual 切到可视化编辑器，草稿无法解析时留在代码编辑器
function enterVisual(): void {
  try {
    if (props.kind === 'schema') parseSchema(draft.value)
    else functions.value = parseFunctions(draft.value)
    visualError.value = ''
    mode.value = 'visual'
  } catch {
    mode.value = 'code'
  }
}

// switchMode 在代码与可视化编辑器之间切换并同步内容
function switchMode(next: 'code' | 'visual'): void {
  if (next === mode.value) return
  saveError.value = ''
  if (next === 'visual') {
    enterVisual()
    return
  }
  try {
    draft.value = visualText()
    mode.value = 'code'
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : String(error)
  }
}

// save 校验并保存编辑结果
function save(): void {
  saveError.value = ''
  try {
    if (mode.value === 'visual') draft.value = visualText()
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : String(error)
    return
  }
  if (parseError.value !== '') return
  const schemas =
    props.kind === 'schema'
      ? [draft.value]
      : parseFunctions(draft.value).flatMap((item) => item.parameters ?? [])
  const issue = schemas.map((schema) => schemaIssue(parseSchema(schema).children)).find(Boolean)
  if (issue) {
    saveError.value = issue
    return
  }
  text.value = props.kind === 'schema' ? draft.value : orderedFunctionsText(draft.value)
  open.value = false
}

// reset 清空编辑内容
function reset(): void {
  draft.value = ''
  functions.value = []
  visualError.value = ''
  saveError.value = ''
}

// addFunction 添加一个空函数声明
function addFunction(): void {
  functions.value.push({ name: '', description: '' })
}

watch(open, (value) => {
  if (!value) return
  draft.value = text.value
  saveError.value = ''
  enterVisual()
})
</script>

<template>
  <UiDialog v-model:open="open" :title="title" width="740px">
    <p class="description">
      <template v-for="(part, index) in description" :key="index">
        <a v-if="part.href" :href="part.href" target="_blank" rel="noopener">{{ part.text }}</a>
        <template v-else>{{ part.text }}</template>
      </template>
    </p>
    <div class="mode-switch">
      <button
        type="button"
        ms-button
        class="ms-button-filter-chip"
        :class="{ 'ms-button-active': mode === 'code' }"
        @click="switchMode('code')"
      >
        {{ t('pg.codeEditor') }}
      </button>
      <button
        type="button"
        ms-button
        class="ms-button-filter-chip"
        :class="{ 'ms-button-active': mode === 'visual' }"
        @click="switchMode('visual')"
      >
        {{ t('pg.visualEditor') }}
      </button>
    </div>
    <div class="editor">
      <textarea
        v-if="mode === 'code'"
        v-model="draft"
        ms-input
        class="code-input"
        spellcheck="false"
        :aria-label="title"
      ></textarea>
      <SchemaEditor
        v-else-if="kind === 'schema'"
        v-model:text="draft"
        @error="visualError = $event"
      />
      <div v-else class="functions">
        <div v-for="(declaration, index) in functions" :key="index" class="function">
          <div class="function-row">
            <div class="field">
              <label :for="`function-name-${index}`">{{ t('pg.name') }}</label>
              <input :id="`function-name-${index}`" v-model="declaration.name" ms-input />
            </div>
            <div class="field">
              <label :for="`function-description-${index}`">{{
                t('pg.functionDescription')
              }}</label>
              <input
                :id="`function-description-${index}`"
                v-model="declaration.description"
                ms-input
              />
            </div>
            <button
              v-tooltip.below="t('pg.deleteFunction')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('pg.deleteFunction')"
              @click="functions.splice(index, 1)"
            >
              <UiIcon name="delete" :size="20" />
            </button>
          </div>
          <div class="parameters">
            <template v-if="declaration.parameters !== undefined">
              <SchemaEditor
                v-model:text="declaration.parameters"
                :path="`function-${index}`"
                @error="visualError = $event"
              />
              <button
                type="button"
                ms-button
                class="ms-button-borderless"
                @click="delete declaration.parameters"
              >
                {{ t('pg.removeParameters') }}
              </button>
            </template>
            <button
              v-else
              type="button"
              ms-button
              class="ms-button-borderless"
              @click="declaration.parameters = ''"
            >
              {{ t('pg.addParameters') }}
            </button>
          </div>
        </div>
        <button type="button" ms-button class="ms-button-borderless" @click="addFunction">
          {{ t('pg.addFunction') }}
        </button>
      </div>
      <p v-if="saveError || parseError" class="parse-error">{{ saveError || parseError }}</p>
    </div>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="reset">
        {{ t('pg.reset') }}
      </button>
      <button
        type="button"
        ms-button
        class="ms-button-primary"
        :aria-disabled="parseError !== ''"
        @click="save"
      >
        {{ t('common.save') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.description {
  margin: 0 0 12px;
}

.description a {
  color: var(--color-v3-text-link);
  text-decoration: none;
}

.mode-switch {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}

.editor {
  display: flex;
  flex-direction: column;
  min-height: 400px;
}

.code-input {
  flex: 1;
  min-height: 400px;
  font-family: monospace;
  font-size: 13px;
  line-height: 20px;
}

.parse-error {
  margin: 8px 0 0;
  color: var(--color-error);
  font-size: 12px;
  line-height: 18px;
}

.functions,
.function {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}

.function {
  width: 100%;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--color-v3-outline-var);
}

.function-row {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  width: 100%;
}

.field {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.field label {
  font-size: 12px;
  line-height: 18px;
}

.parameters {
  width: 100%;
  padding-left: 20px;
}
</style>
