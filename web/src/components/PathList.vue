<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api'
import { useConsole } from '@/console'
import { desktopPlatform } from '@/desktop'
import { useI18n } from '@/i18n'
import { useAppendList } from '@/rows'
import PathPicker from './PathPicker.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ value: string; inputId: string; inputClass: string }>()
const emit = defineEmits<{ change: [value: string] }>()

const { t, errorText } = useI18n()
const { showNotice } = useConsole()

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

// picking 是打开页内路径选择器的行号，-1 表示关闭
const picking = ref(-1)
const reveal = useAppendList('list')

// add 追加一个空行并把它滚入列表、聚焦
function add(): void {
  rows.value.push('')
  void reveal(true)
}

// choose 用选中的路径填入一行
function choose(index: number, path: string): void {
  rows.value[index] = path
  picking.value = -1
  commit()
}

// browse 为一行选择路径：桌面窗口中调用系统对话框，其余情况打开页内选择器；系统对话框出错时显示原因
async function browse(index: number): Promise<void> {
  if (desktopPlatform === '') {
    picking.value = index
    return
  }
  try {
    const { path } = await api.pickPath(rows.value[index]!.trim())
    if (path) choose(index, path)
  } catch (error) {
    showNotice(errorText(error), 'error')
  }
}
</script>

<!-- PathList 是可增删的多行路径输入：每行可手填，也可在服务所在机器上浏览选择；行数多时在列表内滚动 -->
<template>
  <div class="space-y-1.5">
    <div ref="list" class="append-list space-y-1.5">
      <div v-for="(row, index) in rows" :key="index" class="flex items-center gap-1.5">
        <input
          :id="index === 0 ? inputId : undefined"
          v-model.trim="rows[index]"
          v-tooltip.overflow="row"
          :class="inputClass"
          :aria-label="
            index === 0
              ? undefined
              : t('settings.authPathRow').replace('{index}', String(index + 1))
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
          v-tooltip="t('settings.authPathRemove').replace('{path}', row)"
          type="button"
          class="shrink-0 rounded-md p-2 text-gray-500 transition hover:text-gray-200"
          :aria-label="t('settings.authPathRemove').replace('{path}', row)"
          @click="remove(index)"
        >
          <UiIcon name="close" :size="15" />
        </button>
      </div>
    </div>
    <button
      type="button"
      class="flex items-center gap-1.5 rounded-md border border-dashed border-line px-2.5 py-1.5 text-xs text-gray-400 transition hover:border-gray-600 hover:text-gray-200"
      @click="add"
    >
      <UiIcon name="add" :size="13" />
      {{ t('settings.authPathAdd') }}
    </button>

    <PathPicker
      :open="picking !== -1"
      :start="picking === -1 ? '' : rows[picking]!.trim()"
      @close="picking = -1"
      @choose="choose(picking, $event)"
    />
  </div>
</template>
