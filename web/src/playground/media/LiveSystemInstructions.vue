<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlidingPanel from '@/playground/ui/UiSlidingPanel.vue'
import { useInstructionPresets } from '@/playground/session'

const open = defineModel<boolean>('open', { required: true })
const text = defineModel<string>('text', { required: true })
// locked 表示会话进行中，指令只读
const props = defineProps<{ locked: boolean }>()
const { t } = useI18n()
// lockedTip 是会话进行中编辑区的官网提示
const lockedTip = computed(() => (props.locked ? t('pg.liveSystemLocked') : ''))
const { presets, selected, title, saved, saveError, currentPreset, remember, choose, remove } =
  useInstructionPresets(
    () => open.value,
    () => text.value,
    (value) => {
      text.value = value
    },
  )
</script>

<template>
  <UiSlidingPanel v-model:open="open" :title="t('pg.systemInstructions')">
    <template #status>
      <span class="saving-status" :class="{ visible: text !== '' }">
        {{ saveError ? t('pg.saveFailed') : saved ? t('pg.saved') : t('pg.savingShort') }}
      </span>
    </template>
    <div class="panel-content-wrapper">
      <UiSelect
        :model-value="selected"
        class="w-full"
        :disabled="locked"
        :aria-label="t('pg.systemPresets')"
        @update:model-value="choose"
      >
        <option value="">{{ t('pg.untitledInstruction') }}</option>
        <option v-for="preset in presets" :key="preset.id" :value="preset.id">
          {{ preset.title || t('pg.untitledInstruction') }}
        </option>
      </UiSelect>
      <div class="title-row">
        <input
          v-model="title"
          ms-input
          :disabled="locked"
          :placeholder="t('pg.title')"
          :aria-label="t('pg.title')"
          @input="remember"
        />
        <button
          v-tooltip.below="lockedTip || t('common.delete')"
          type="button"
          ms-button
          class="ms-button-icon"
          :aria-label="t('common.delete')"
          :aria-disabled="currentPreset === undefined || locked"
          @click="currentPreset && !locked && remove()"
        >
          <UiIcon name="delete" :size="18" />
        </button>
      </div>
      <textarea
        v-model="text"
        v-tooltip="lockedTip"
        ms-input
        class="instructions"
        :disabled="locked"
        :placeholder="t('pg.systemPlaceholder')"
        :aria-label="t('pg.systemInstructions')"
        @input="remember"
      ></textarea>
    </div>
  </UiSlidingPanel>
</template>

<style scoped>
.panel-content-wrapper {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
}

.title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.title-row input {
  flex: 1;
}

.title-row [ms-button] {
  border: 1px solid var(--color-v3-outline-var);
}

.instructions {
  flex: 1;
  min-height: 200px;
}

.saving-status {
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
  opacity: 0;
  transition: opacity 0.2s ease-in-out;
}

.saving-status.visible {
  opacity: 1;
}
</style>
