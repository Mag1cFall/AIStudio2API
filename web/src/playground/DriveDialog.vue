<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { listDriveFiles, type DriveFile } from './drive'

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ select: [file: DriveFile] }>()
const { t } = useI18n()
const files = ref<DriveFile[]>([])
const loading = ref(false)
const error = ref('')

watch(open, async (visible) => {
  if (!visible) return
  loading.value = true
  error.value = ''
  try {
    files.value = await listDriveFiles()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    loading.value = false
  }
})

// size 把字节数写为 KB 或 MB
function size(file: DriveFile): string {
  const bytes = Number(file.sizeBytes ?? 0)
  return bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MB` : `${Math.ceil(bytes / 1024)} KB`
}

// choose 选中文件并关闭对话框
function choose(file: DriveFile): void {
  emit('select', file)
  open.value = false
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.drive')" width="560px">
    <p v-if="loading" class="drive-status">{{ t('common.loading') }}</p>
    <p v-else-if="error" class="drive-status error">{{ error }}</p>
    <p v-else-if="files.length === 0" class="drive-status">{{ t('pg.driveEmpty') }}</p>
    <ul v-else class="drive-files">
      <li v-for="file in files" :key="file.name">
        <button type="button" class="drive-file" @click="choose(file)">
          <UiIcon name="addToDrive" :size="20" />
          <span class="drive-file-name">{{ file.displayName || file.name }}</span>
          <span class="drive-file-meta">{{ file.mimeType }} · {{ size(file) }}</span>
        </button>
      </li>
    </ul>
  </UiDialog>
</template>

<style scoped>
.drive-status {
  margin: 0;
  color: var(--color-v3-text-var);
}

.drive-status.error {
  color: var(--color-v3-error-text);
}

.drive-files {
  max-height: 50vh;
  margin: 0;
  overflow-y: auto;
  padding: 0;
  list-style: none;
}

.drive-file {
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 10px 12px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--color-v3-text);
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.drive-file:hover,
.drive-file:focus-visible {
  background-color: var(--color-v3-surface-container-high);
  outline: none;
}

.drive-file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.drive-file-meta {
  color: var(--color-v3-text-var);
  font-size: 12px;
}
</style>
