<script setup lang="ts">
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { EnvironmentSource } from './types'

const open = defineModel<boolean>('open', { required: true })
const sources = defineModel<EnvironmentSource[]>('sources', { required: true })
defineProps<{ readonly?: boolean }>()
const { t } = useI18n()

// draft 是对话框中尚未保存的源
const draft = ref<EnvironmentSource[]>([])
// selected 是右侧正在编辑的源的序号
const selected = ref(0)
watch(open, (value) => {
  if (!value) return
  draft.value = sources.value.map((source) => ({ ...source }))
  selected.value = 0
})
const current = computed(() => draft.value[selected.value])
// kinds 与官网 Add source 菜单的三项相同，baseName 是新源的默认挂载路径
const kinds = [
  { type: 'inline', label: 'pg.inlineFile', icon: 'document', baseName: 'new_file' },
  { type: 'gcs', label: 'pg.gcsBucket', icon: 'folder', baseName: 'new_gcs_bucket' },
  { type: 'repository', label: 'pg.repository', icon: 'branch', baseName: 'new_repository' },
] as const

// add 与官网一样以不与已有源重复的默认挂载路径加入一个源并选中它
function add(kind: (typeof kinds)[number]): void {
  const taken = new Set(draft.value.map((source) => source.target))
  let target = `/${kind.baseName}`
  for (let index = 1; taken.has(target); index++) target = `/${kind.baseName}_${index}`
  draft.value.push({ type: kind.type, source: '', target, content: '' })
  selected.value = draft.value.length - 1
}

// remove 删除一个源并选中第一个源
function remove(index: number): void {
  draft.value.splice(index, 1)
  selected.value = 0
}

// setTarget 与官网一样把挂载路径补成以 / 开头
function setTarget(value: string): void {
  if (current.value) current.value.target = value.startsWith('/') ? value : `/${value}`
}

// save 保存源并关闭对话框
function save(): void {
  sources.value = draft.value
  open.value = false
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.environmentSources')" width="1500px" headerless>
    <div class="sources-layout">
      <div v-if="draft.length > 0" class="file-list">
        <button
          v-for="(source, index) in draft"
          :key="index"
          type="button"
          class="file-node"
          :class="{ selected: index === selected }"
          @click="selected = index"
        >
          <UiIcon :name="kinds.find((kind) => kind.type === source.type)!.icon" :size="16" />
          <span class="file-name">{{ source.target.replace(/^\//, '') }}</span>
        </button>
        <div v-if="!readonly" class="add-source-cell">
          <DropdownMenuRoot :modal="false">
            <DropdownMenuTrigger
              v-tooltip.above="t('pg.addNewSource')"
              ms-button
              class="ms-button-secondary add-source-button"
            >
              <UiIcon name="add" :size="18" /> {{ t('pg.addSource') }}
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent :side-offset="4" class="ms-menu-panel">
                <DropdownMenuItem
                  v-for="kind in kinds"
                  :key="kind.type"
                  class="ms-menu-item"
                  @select="add(kind)"
                >
                  <UiIcon :name="kind.icon" :size="18" />
                  <span class="ms-menu-item-text">{{ t(kind.label) }}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
        </div>
      </div>
      <div v-if="current" class="source-panel">
        <div class="mount-row">
          <p class="field-label">{{ t('pg.mountingPath') }}</p>
          <div class="mount-input">
            <input
              ms-input
              :value="current.target"
              :disabled="readonly"
              :aria-label="t('pg.mountingPath')"
              @change="setTarget(($event.target as HTMLInputElement).value)"
            />
            <button
              v-if="!readonly"
              v-tooltip.left="t('pg.deleteFile')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('pg.deleteFile')"
              @click="remove(selected)"
            >
              <UiIcon name="delete" :size="20" />
            </button>
          </div>
        </div>
        <textarea
          v-if="current.type === 'inline'"
          v-model="current.content"
          :readonly="readonly"
          class="content-editor"
          spellcheck="false"
          :aria-label="t('pg.fileContent')"
        ></textarea>
        <div v-else class="uri-row">
          <p class="field-label">
            {{ current.type === 'gcs' ? t('pg.cloudStorageUri') : t('pg.repoUri') }}
          </p>
          <input
            v-model="current.source"
            ms-input
            :disabled="readonly"
            :aria-label="current.type === 'gcs' ? t('pg.cloudStorageUri') : t('pg.repoUri')"
            :placeholder="
              t('pg.sourceUrlPlaceholder').replace(
                '{example}',
                current.type === 'gcs'
                  ? 'gcs://my-bucket/path/to/file.txt'
                  : 'github://my-repo/path',
              )
            "
          />
        </div>
      </div>
      <div v-else class="empty-state">
        <UiIcon name="folder" :size="32" />
        <p>{{ t('pg.noSourceYet') }}</p>
        <DropdownMenuRoot v-if="!readonly" :modal="false">
          <DropdownMenuTrigger
            v-tooltip.below="t('pg.addNewSource')"
            ms-button
            class="ms-button-secondary"
          >
            <UiIcon name="add" :size="18" /> {{ t('pg.addSource') }}
          </DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent :side-offset="4" class="ms-menu-panel">
              <DropdownMenuItem
                v-for="kind in kinds"
                :key="kind.type"
                class="ms-menu-item"
                @select="add(kind)"
              >
                <UiIcon :name="kind.icon" :size="18" />
                <span class="ms-menu-item-text">{{ t(kind.label) }}</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
    </div>
    <template #actions>
      <button
        v-if="readonly"
        type="button"
        ms-button
        class="ms-button-primary"
        @click="open = false"
      >
        {{ t('common.close') }}
      </button>
      <template v-else>
        <button type="button" ms-button class="ms-button-borderless" @click="open = false">
          {{ t('common.cancel') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="save">
          {{ t('common.save') }}
        </button>
      </template>
    </template>
  </UiDialog>
</template>

<style scoped>
.sources-layout {
  display: flex;
  height: calc(100dvh - 230px);
  margin: 0 -16px 0;
  border-bottom: 1px solid var(--color-v3-outline-var);
}

.file-list {
  display: flex;
  flex-direction: column;
  flex: 0 0 180px;
  padding: 8px;
  border-right: 1px solid var(--color-v3-outline-var);
}

.file-node {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 8px;
  border: 0;
  border-radius: 6px;
  background: none;
  color: var(--color-v3-text);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  text-align: start;
}

.file-node.selected {
  background: var(--color-v3-surface-container-high);
}

.file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.add-source-cell {
  margin-top: auto;
}

.add-source-button {
  width: 100%;
}

.source-panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.mount-row,
.uri-row {
  padding: 12px 12px 12px 12px;
}

.field-label {
  margin: 0 0 8px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.mount-input {
  display: flex;
  align-items: center;
  gap: 8px;
}

.mount-input input {
  flex: 1;
}

.content-editor {
  flex: 1;
  padding: 12px;
  border: 0;
  border-top: 1px solid var(--color-v3-outline-var);
  outline: none;
  background: none;
  color: var(--color-v3-text);
  font-family: var(--font-family-code);
  font-size: 13px;
  line-height: 20px;
  resize: none;
}

.empty-state {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--color-v3-text-var);
}

.empty-state p {
  margin: 0;
  font-size: 14px;
}
</style>
