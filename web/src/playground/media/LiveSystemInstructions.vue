<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlidingPanel from '@/playground/ui/UiSlidingPanel.vue'
import { newTurnID } from '@/playground/session'

// Preset 是一条保存在本机的系统指令，与对话页共用同一份列表
interface Preset {
  id: string
  title: string
  text: string
}

const open = defineModel<boolean>('open', { required: true })
const text = defineModel<string>('text', { required: true })
const { t } = useI18n()

// presetsKey 是系统指令预设的本机存储键，与对话页相同
const presetsKey = 'playground_system_instructions'

// loadPresets 读取本机保存的系统指令
function loadPresets(): Preset[] {
  try {
    return JSON.parse(localStorage.getItem(presetsKey) ?? '[]') as Preset[]
  } catch {
    return []
  }
}

const presets = ref<Preset[]>(loadPresets())
const selected = ref('')
const title = ref('')
const saved = ref(true)
let saveTimer = 0

// currentPreset 是当前编辑的预设
const currentPreset = computed(() => presets.value.find((preset) => preset.id === selected.value))

// persist 把预设写入本机存储
function persist(): void {
  try {
    localStorage.setItem(presetsKey, JSON.stringify(presets.value))
  } catch {
    // 本机存储不可用时预设只在本次打开期间有效
  }
  saved.value = true
}

// remember 在编辑后合并保存当前指令到预设
function remember(): void {
  saved.value = false
  window.clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    if (text.value.trim() === '' && title.value.trim() === '') {
      saved.value = true
      return
    }
    let preset = currentPreset.value
    if (preset === undefined) {
      preset = { id: newTurnID(), title: '', text: '' }
      presets.value.push(preset)
      selected.value = preset.id
    }
    preset.title = title.value
    preset.text = text.value
    persist()
  }, 500)
}

// choose 切换到另一条预设或新建空白指令
function choose(id: string | number | undefined): void {
  selected.value = String(id ?? '')
  const preset = currentPreset.value
  title.value = preset?.title ?? ''
  text.value = preset?.text ?? ''
}

// remove 删除当前预设并清空指令
function remove(): void {
  presets.value = presets.value.filter((preset) => preset.id !== selected.value)
  persist()
  choose('')
}

watch(open, (value) => {
  if (!value) return
  presets.value = loadPresets()
  const match = presets.value.find((preset) => preset.text === text.value)
  selected.value = match?.id ?? ''
  title.value = match?.title ?? ''
})
</script>

<template>
  <UiSlidingPanel v-model:open="open" :title="t('pg.systemInstructions')">
    <template #status>
      <span class="saving-status" :class="{ visible: text !== '' }">
        {{ saved ? t('pg.saved') : t('pg.savingShort') }}
      </span>
    </template>
    <div class="panel-content-wrapper">
      <UiSelect
        :model-value="selected"
        class="w-full"
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
          :placeholder="t('pg.title')"
          :aria-label="t('pg.title')"
          @input="remember"
        />
        <button
          v-tooltip.below="t('common.delete')"
          type="button"
          ms-button
          class="ms-button-icon"
          :aria-label="t('common.delete')"
          :aria-disabled="currentPreset === undefined"
          @click="currentPreset && remove()"
        >
          <UiIcon name="delete" :size="18" />
        </button>
      </div>
      <textarea
        v-model="text"
        ms-input
        class="instructions"
        :placeholder="t('pg.systemPlaceholder')"
        :aria-label="t('pg.systemInstructions')"
        @input="remember"
      ></textarea>
    </div>
    <template #footer>
      <p class="local-storage-notice">{{ t('pg.systemLocal') }}</p>
    </template>
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

.local-storage-notice {
  margin: 8px 0 0;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}
</style>
