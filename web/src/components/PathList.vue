<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { PathListing } from '@/types'
import DetailDialog from './DetailDialog.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ value: string; inputId: string; inputClass: string }>()
const emit = defineEmits<{ change: [value: string] }>()

const { t, errorText } = useI18n()

// filled 返回去掉空白与空项后的路径
function filled(paths: string[]): string[] {
  return paths.map((item) => item.trim()).filter(Boolean)
}

// parse 把逗号分隔的路径拆成行，没有路径时保留一个空行
function parse(value: string): string[] {
  const paths = filled(value.split(','))
  return paths.length > 0 ? paths : ['']
}

const rows = ref(parse(props.value))
watch(
  () => props.value,
  (value) => {
    if (filled(rows.value).join(',') !== value) rows.value = parse(value)
  },
)

// commit 写回已填写的路径
function commit(): void {
  emit('change', filled(rows.value).join(','))
}

// remove 删除一行路径，始终保留一个输入框
function remove(index: number): void {
  rows.value.splice(index, 1)
  if (rows.value.length === 0) rows.value.push('')
  commit()
}

// picking 是打开路径选择器的行号，-1 表示关闭
const picking = ref(-1)
const listing = ref<PathListing | null>(null)
const pickError = ref('')

// load 读取服务所在机器上的一个位置
async function load(path: string): Promise<void> {
  pickError.value = ''
  try {
    listing.value = await api.paths(path)
  } catch (error) {
    pickError.value = errorText(error)
  }
}

// browse 打开一行的路径选择器，从该行已填的路径开始
function browse(index: number): void {
  picking.value = index
  listing.value = null
  void load(rows.value[index]!.trim())
}

// choose 用选中的路径填入正在选择的行
function choose(path: string): void {
  rows.value[picking.value] = path
  picking.value = -1
  commit()
}
</script>

<!-- PathList 是可增删的多行路径输入：每行可手填，也可在服务所在机器上浏览选择 -->
<template>
  <div class="space-y-1.5">
    <div v-for="(row, index) in rows" :key="index" class="flex items-center gap-1.5">
      <input
        :id="index === 0 ? inputId : undefined"
        v-model.trim="rows[index]"
        :class="inputClass"
        :aria-label="
          index === 0 ? undefined : t('settings.authPathRow').replace('{index}', String(index + 1))
        "
        autocomplete="off"
        spellcheck="false"
        @change="commit"
      />
      <button
        v-tooltip="t('settings.authPathBrowse')"
        type="button"
        class="shrink-0 rounded-md p-2 text-gray-500 transition hover:text-gray-200"
        :aria-label="t('settings.authPathBrowse')"
        @click="browse(index)"
      >
        <UiIcon name="folder" :size="15" />
      </button>
      <button
        v-if="rows.length > 1"
        type="button"
        class="shrink-0 rounded-md p-2 text-gray-500 transition hover:text-gray-200"
        :aria-label="t('settings.authPathRemove').replace('{path}', row)"
        @click="remove(index)"
      >
        <UiIcon name="close" :size="15" />
      </button>
    </div>
    <button
      type="button"
      class="flex items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-xs text-gray-400 transition hover:border-gray-600 hover:text-gray-200"
      @click="rows.push('')"
    >
      <UiIcon name="add" :size="13" />
      {{ t('settings.authPathAdd') }}
    </button>

    <DetailDialog
      :open="picking !== -1"
      :title="t('settings.pathPickerTitle')"
      :description="listing?.path || t('settings.pathPickerRoots')"
      @close="picking = -1"
    >
      <p v-if="pickError" class="mb-2 text-xs text-red-300">{{ pickError }}</p>
      <template v-if="listing">
        <button
          v-if="listing.parent"
          type="button"
          class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm text-gray-300 hover:bg-raised"
          @click="load(listing!.parent)"
        >
          <UiIcon name="arrowUp" :size="14" class="text-gray-500" />
          {{ t('settings.pathPickerUp') }}
        </button>
        <p v-if="listing.entries.length === 0" class="px-2 py-3 text-center text-xs text-gray-500">
          {{ t('settings.pathPickerEmpty') }}
        </p>
        <button
          v-for="entry in listing.entries"
          :key="entry.path"
          type="button"
          class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm text-gray-200 hover:bg-raised"
          @click="entry.dir ? load(entry.path) : choose(entry.path)"
        >
          <UiIcon :name="entry.dir ? 'folder' : 'file'" :size="14" class="shrink-0 text-gray-500" />
          <span class="min-w-0 truncate">{{ entry.name }}</span>
        </button>
        <div v-if="listing.path" class="mt-3 flex justify-end border-t border-line pt-3">
          <button
            type="button"
            class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-blue-500"
            @click="choose(listing!.path)"
          >
            {{ t('settings.pathPickerSelect') }}
          </button>
        </div>
      </template>
    </DetailDialog>
  </div>
</template>
