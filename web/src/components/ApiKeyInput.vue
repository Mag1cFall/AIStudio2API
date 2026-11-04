<script setup lang="ts">
import { computed, nextTick, reactive, ref, useTemplateRef } from 'vue'
import { maskKey, splitKeys } from '@/apikey'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

// modelValue 是逗号分隔的 API key，revealed 表示全部 key 以原文显示
const props = defineProps<{ modelValue: string; inputId: string; revealed: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:revealed': [value: boolean]
}>()
const { t } = useI18n()

const keys = computed(() => splitKeys(props.modelValue))
// draft 是输入框中尚未加入列表的文字
const draft = ref('')
// shown 是单独显示原文的 key
const shown = reactive(new Set<string>())
// duplicate 是最近一次输入中与列表重复的 key，提示显示期间该标签高亮
const duplicate = ref('')
let duplicateTimer: number | undefined
// list 是标签区，超过约三行时在框内滚动
const list = useTemplateRef<HTMLElement>('list')

// reveal 把标签区滚到第 index 个标签，新加的标签在末尾
async function reveal(index: number): Promise<void> {
  await nextTick()
  list.value?.children[index]?.scrollIntoView({ block: 'nearest' })
}

// visible 判断 key 是否以原文显示
function visible(key: string): boolean {
  return props.revealed || shown.has(key)
}

// add 把逗号分隔的文字拆成 key 加入列表，去掉首尾空白，丢弃空值，已有的 key 标出且不重复加入
function add(text: string): void {
  const next = [...keys.value]
  for (const key of splitKeys(text)) {
    if (next.includes(key)) {
      duplicate.value = key
      window.clearTimeout(duplicateTimer)
      duplicateTimer = window.setTimeout(() => (duplicate.value = ''), 2400)
    } else next.push(key)
  }
  if (next.length !== keys.value.length) {
    emit('update:modelValue', next.join(','))
    void reveal(next.length - 1)
  } else if (duplicate.value) void reveal(next.indexOf(duplicate.value))
}

// remove 从列表中删除一个 key
function remove(key: string): void {
  shown.delete(key)
  emit('update:modelValue', keys.value.filter((item) => item !== key).join(','))
}

// toggle 切换一个 key 的原文显示
function toggle(key: string): void {
  if (shown.has(key)) shown.delete(key)
  else shown.add(key)
}

// typed 记下输入框的文字，输入逗号时把逗号前的文字加入列表，逗号后的文字留在输入框
function typed(event: Event): void {
  const input = event.target as HTMLInputElement
  const index = input.value.lastIndexOf(',')
  if (index >= 0) {
    add(input.value.slice(0, index))
    input.value = input.value.slice(index + 1)
  }
  draft.value = input.value
}

// commit 把输入框中的文字加入列表并清空输入框
function commit(): void {
  add(draft.value)
  draft.value = ''
}

// pasted 把粘贴的逗号分隔内容整体拆成 key 加入列表
function pasted(event: ClipboardEvent): void {
  const text = event.clipboardData?.getData('text') ?? ''
  if (!text.includes(',')) return
  event.preventDefault()
  add(draft.value + text)
  draft.value = ''
}
</script>

<template>
  <div>
    <div
      class="overflow-hidden rounded-md border border-line bg-canvas transition focus-within:border-blue-500"
    >
      <div
        v-if="keys.length > 0"
        ref="list"
        class="flex max-h-[5.75rem] flex-wrap gap-1 overflow-y-auto border-b border-line/60 p-1.5 [scrollbar-width:thin]"
      >
        <span
          v-for="key in keys"
          :key="key"
          data-api-key
          class="group flex max-w-full min-w-0 items-center rounded border font-mono text-xs transition-colors"
          :class="
            duplicate === key
              ? 'border-amber-400 bg-amber-500/15 text-amber-200'
              : 'border-line bg-raised text-gray-200'
          "
        >
          <button
            type="button"
            class="relative flex min-h-6 min-w-6 items-center rounded-l py-0.5 pr-1 pl-1.5 text-left break-all transition hover:bg-line focus-visible:bg-line focus-visible:outline-none disabled:hover:bg-transparent"
            :disabled="revealed"
            :aria-label="
              (shown.has(key) ? t('settings.keyHide') : t('settings.keyShow')).replace(
                '{key}',
                maskKey(key),
              )
            "
            :aria-pressed="visible(key)"
            @click="toggle(key)"
          >
            <template v-if="visible(key)">{{ key }}</template>
            <span v-else
              ><span class="relative"
                ><span class="group-focus-within:invisible group-hover:invisible">••••</span
                ><UiIcon
                  name="eye"
                  :size="13"
                  class="invisible absolute top-1/2 left-1/2 -translate-1/2 group-focus-within:visible group-hover:visible" /></span
              >{{ key.slice(-4) }}</span
            >
          </button>
          <button
            type="button"
            class="flex h-6 w-6 shrink-0 items-center justify-center rounded-r text-gray-500 transition hover:bg-line hover:text-gray-200 focus-visible:bg-line focus-visible:outline-none"
            :aria-label="t('settings.keyRemove').replace('{key}', maskKey(key))"
            @click="remove(key)"
          >
            <UiIcon name="close" :size="13" />
          </button>
        </span>
      </div>
      <div class="flex items-center gap-1 pr-1">
        <input
          :id="inputId"
          :value="draft"
          :type="revealed ? 'text' : 'password'"
          class="h-9 min-w-0 flex-1 bg-transparent px-3 text-sm text-white placeholder:text-gray-500 focus:outline-none"
          :placeholder="
            keys.length === 0 ? t('settings.apiKeyPlaceholder') : t('settings.apiKeyMore')
          "
          autocomplete="new-password"
          spellcheck="false"
          @input="typed"
          @keydown.enter.prevent="commit"
          @paste="pasted"
          @blur="commit"
        />
        <button
          type="button"
          class="shrink-0 rounded px-2 py-1.5 text-xs text-gray-400 transition hover:bg-raised hover:text-gray-200"
          :aria-pressed="revealed"
          @click="emit('update:revealed', !revealed)"
        >
          {{ revealed ? t('settings.hide') : t('settings.reveal') }}
        </button>
      </div>
    </div>
    <p v-if="duplicate" class="mt-1 text-xs text-amber-300" role="status">
      {{ t('settings.keyDuplicate').replace('{key}', maskKey(duplicate)) }}
    </p>
  </div>
</template>
