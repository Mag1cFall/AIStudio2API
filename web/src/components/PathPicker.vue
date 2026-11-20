<script setup lang="ts">
import { nextTick, ref, useTemplateRef, watch } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { PathListing } from '@/types'
import DetailDialog from './DetailDialog.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ open: boolean; start: string }>()
const emit = defineEmits<{ close: []; choose: [path: string] }>()

const { t, errorText } = useI18n()

// roots 是服务所在机器的文件系统根，listing 是当前位置的内容，typed 是路径栏中的文字
const roots = ref<PathListing['entries']>([])
const listing = ref<PathListing | null>(null)
const typed = ref('')
const loading = ref(false)
const failure = ref('')
const field = useTemplateRef<HTMLInputElement>('field')

// load 读取服务所在机器上的一个位置，把路径栏更新为实际打开的目录并显示路径末尾
async function load(path: string): Promise<void> {
  loading.value = true
  failure.value = ''
  try {
    listing.value = await api.paths(path)
    typed.value = listing.value.path
    await nextTick()
    if (field.value) field.value.scrollLeft = field.value.scrollWidth
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    listing.value = null
    void load(props.start)
    roots.value = (await api.paths('roots')).entries
  },
)
</script>

<!-- PathPicker 在服务所在机器上浏览并选择账户目录或账户状态文件：路径栏可直接输入，点目录进入，点文件选中 -->
<template>
  <DetailDialog narrow :open="open" :title="t('settings.pathPickerTitle')" @close="emit('close')">
    <form class="flex items-center gap-1.5" @submit.prevent="load(typed.trim())">
      <button
        v-tooltip="t('settings.pathPickerUp')"
        type="button"
        class="shrink-0 rounded-md border border-line p-2 text-gray-400 transition hover:bg-raised hover:text-white disabled:opacity-40"
        :aria-label="t('settings.pathPickerUp')"
        :disabled="!listing?.parent"
        @click="load(listing!.parent)"
      >
        <UiIcon name="arrowUp" :size="15" />
      </button>
      <input
        ref="field"
        v-model="typed"
        class="min-w-0 flex-1 rounded-md border border-line bg-canvas px-3 py-2 font-mono text-sm text-white"
        :aria-label="t('settings.pathPickerPath')"
        :placeholder="t('settings.pathPickerPath')"
        autocomplete="off"
        spellcheck="false"
      />
    </form>
    <div class="mt-2 flex flex-wrap items-center gap-1.5 text-xs">
      <span class="text-gray-500">{{ t('settings.pathPickerRoots') }}</span>
      <button
        v-for="root in roots"
        :key="root.path"
        type="button"
        class="rounded border border-line px-2 py-0.5 font-mono text-gray-300 transition hover:bg-raised hover:text-white"
        @click="load(root.path)"
      >
        {{ root.name }}
      </button>
    </div>
    <p v-if="failure" role="alert" class="mt-2 text-xs text-red-300">{{ failure }}</p>
    <ul
      class="mt-2 h-72 overflow-y-auto rounded-md border border-line bg-canvas p-1 [scrollbar-width:thin]"
      :aria-busy="loading"
    >
      <li
        v-if="listing && listing.entries.length === 0"
        class="px-2 py-6 text-center text-xs text-gray-500"
      >
        {{ t('settings.pathPickerEmpty') }}
      </li>
      <li v-for="entry in listing?.entries" :key="entry.path">
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm text-gray-200 hover:bg-raised"
          @click="entry.dir ? load(entry.path) : emit('choose', entry.path)"
        >
          <UiIcon :name="entry.dir ? 'folder' : 'file'" :size="14" class="shrink-0 text-gray-500" />
          <span v-tooltip.overflow="entry.name" class="min-w-0 flex-1 truncate">{{
            entry.name
          }}</span>
          <UiIcon v-if="entry.dir" name="chevronRight" :size="13" class="shrink-0 text-gray-600" />
        </button>
      </li>
    </ul>
    <div class="mt-3 flex justify-end gap-2">
      <button
        type="button"
        class="rounded-md border border-line px-3 py-1.5 text-sm text-gray-300 transition hover:bg-raised hover:text-white"
        @click="emit('close')"
      >
        {{ t('common.cancel') }}
      </button>
      <button
        type="button"
        class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
        :disabled="!listing?.path"
        @click="emit('choose', listing!.path)"
      >
        {{ t('settings.pathPickerSelect') }}
      </button>
    </div>
  </DetailDialog>
</template>
