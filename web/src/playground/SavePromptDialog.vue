<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'

const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ title: string; description: string }>()
const emit = defineEmits<{ save: [title: string, description: string] }>()
const { t } = useI18n()
const titleDraft = ref('')
const descriptionDraft = ref('')

watch(open, (value) => {
  if (!value) return
  titleDraft.value = props.title
  descriptionDraft.value = props.description
})

// save 提交编辑后的标题与描述并关闭对话框
function save(): void {
  emit('save', titleDraft.value.trim(), descriptionDraft.value.trim())
  open.value = false
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="t('pg.savePrompt')"
    width="min(500px, 90vw)"
    :closable="false"
  >
    <label class="save-dialog-label" for="save-prompt-name">{{ t('pg.promptName') }}</label>
    <input
      id="save-prompt-name"
      v-model="titleDraft"
      ms-input
      maxlength="120"
      @keydown.enter.prevent="save"
    />
    <label class="save-dialog-label description-label" for="save-prompt-description">{{
      t('pg.promptDescription')
    }}</label>
    <textarea
      id="save-prompt-description"
      v-model="descriptionDraft"
      ms-input
      class="description-input"
      rows="3"
      :placeholder="t('pg.optional')"
    ></textarea>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="open = false">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary save-button" @click="save">
        {{ t('common.save') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.save-dialog-label {
  font-weight: 500;
}

.description-label {
  margin-top: 8px;
}

.description-input {
  display: block;
  padding: 8px;
}

.save-button {
  margin-left: 4px;
}
</style>
