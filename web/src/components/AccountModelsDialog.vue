<script setup lang="ts">
import { computed, ref, useTemplateRef, watch } from 'vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  // account 是要查看模型的账户邮箱，为空时对话框关闭
  account: string
  models: string[]
  // trigger 是打开对话框的按钮，关闭后焦点回到这里
  trigger: HTMLElement | null
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const query = ref('')
const search = useTemplateRef<HTMLInputElement>('search')

watch(
  () => props.account,
  () => (query.value = ''),
)

// matches 是模型 ID 包含搜索词的模型，不区分大小写
const matches = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return props.models.filter((model) => model.toLowerCase().includes(needle))
})

// listHeight 是模型列表的固定高度，按全部模型的行数计算并以 24rem 与 55vh 为上限，搜索时对话框大小不变
const listHeight = computed(() => `min(${props.models.length * 1.75}rem, 24rem, 55vh)`)

// restoreFocus 在对话框关闭后把焦点放回打开它的按钮
function restoreFocus(event: Event): void {
  event.preventDefault()
  props.trigger?.focus()
}
</script>

<template>
  <DialogRoot :open="account !== ''" @update:open="(value: boolean) => !value && emit('close')">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[70] bg-black/60" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[71] flex max-h-[85vh] w-[calc(100vw-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg border border-line bg-panel shadow-2xl focus:outline-none"
        @open-auto-focus.prevent="search?.focus()"
        @close-auto-focus="restoreFocus"
      >
        <header class="flex items-start gap-3 border-b border-line px-5 py-4">
          <div class="min-w-0 flex-1">
            <DialogTitle class="text-base font-semibold [overflow-wrap:anywhere] text-white">
              {{ account }}
            </DialogTitle>
            <DialogDescription class="mt-1 text-xs text-gray-400 tabular-nums">
              {{
                models.length === 1
                  ? t('models.countOne')
                  : t('models.count').replace('{count}', String(models.length))
              }}
            </DialogDescription>
          </div>
          <DialogClose
            v-tooltip="t('common.close')"
            class="shrink-0 rounded p-1 text-gray-500 hover:bg-raised hover:text-white"
            :aria-label="t('common.close')"
          >
            <UiIcon name="close" :size="16" />
          </DialogClose>
        </header>
        <div class="px-5 pt-3 pb-2">
          <div class="relative">
            <UiIcon
              name="search"
              :size="14"
              class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-gray-500"
            />
            <input
              ref="search"
              v-model="query"
              type="text"
              :aria-label="t('accounts.searchModels')"
              :placeholder="t('accounts.searchModels')"
              class="w-full rounded-md border border-line bg-canvas py-1.5 pr-3 pl-8 text-sm text-white focus:border-blue-500 focus:outline-none"
            />
          </div>
        </div>
        <ul
          class="min-h-0 overflow-y-auto px-2 pb-2 [scrollbar-gutter:stable]"
          :style="{ height: listHeight }"
          :aria-label="account"
        >
          <li
            v-for="model in matches"
            :key="model"
            class="rounded px-3 py-1.5 font-mono text-xs leading-4 [overflow-wrap:anywhere] text-gray-200"
          >
            {{ model }}
          </li>
          <li v-if="matches.length === 0" class="px-3 py-6 text-center text-sm text-gray-500">
            {{ t('models.noMatches') }}
          </li>
        </ul>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
