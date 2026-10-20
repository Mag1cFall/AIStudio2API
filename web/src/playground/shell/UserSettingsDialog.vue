<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import { useTheme, type ThemePreference } from '@/theme'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import { microphone, submitKey, type SubmitKey } from './preferences'

const open = defineModel<boolean>('open', { required: true })
const { t, availableLocales, locale, setLocale } = useI18n()
const { preference, selectTheme } = useTheme()
const themes: { value: ThemePreference; label: 'theme.light' | 'theme.dark' | 'pg.themeSystem' }[] =
  [
    { value: 'light', label: 'theme.light' },
    { value: 'dark', label: 'theme.dark' },
    { value: 'system', label: 'pg.themeSystem' },
  ]
// submitKeys 与官网一致的运行快捷键选项
const submitKeys: { value: SubmitKey; label: 'pg.submitCtrlEnter' | 'pg.submitEnter' }[] = [
  { value: 'ctrl-enter', label: 'pg.submitCtrlEnter' },
  { value: 'enter', label: 'pg.submitEnter' },
]
// microphones 是浏览器列出的音频输入设备
const microphones = ref<{ id: string; label: string }[]>([])

// chooseTheme 与官网一样直接切换到选中的配色选项
function chooseTheme(next: ThemePreference): void {
  if (next !== preference.value) selectTheme(next)
}

// loadMicrophones 列出音频输入设备，未授权时设备名为空并按序号显示
async function loadMicrophones(): Promise<void> {
  const devices = (await navigator.mediaDevices.enumerateDevices()).filter(
    (device) =>
      device.kind === 'audioinput' && device.deviceId !== '' && device.deviceId !== 'default',
  )
  microphones.value = devices.map((device, index) => ({
    id: device.deviceId,
    label: device.label || `${t('pg.microphone')} ${index + 1}`,
  }))
}

watch(open, (value) => {
  if (value) void loadMicrophones()
})
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.userSettingsTitle')" width="500px" :closable="false">
    <div class="settings-content">
      <section class="settings-section">
        <h3 class="section-title">{{ t('theme.label') }}</h3>
        <p class="section-description">{{ t('pg.themeHint') }}</p>
        <div class="chips">
          <button
            v-for="item in themes"
            :key="item.value"
            type="button"
            ms-button
            class="ms-button-filter-chip"
            :class="{ 'ms-button-active': preference === item.value }"
            :aria-pressed="preference === item.value"
            @click="chooseTheme(item.value)"
          >
            {{ t(item.label) }}
          </button>
        </div>
      </section>
      <hr class="divider" />
      <section class="settings-section">
        <h3 class="section-title">{{ t('pg.submitKey') }}</h3>
        <p class="section-description">{{ t('pg.submitKeyHint') }}</p>
        <div class="chips">
          <button
            v-for="item in submitKeys"
            :key="item.value"
            type="button"
            ms-button
            class="ms-button-filter-chip"
            :class="{ 'ms-button-active': submitKey === item.value }"
            :aria-pressed="submitKey === item.value"
            @click="submitKey = item.value"
          >
            {{ t(item.label) }}
          </button>
        </div>
      </section>
      <hr class="divider" />
      <section class="settings-section">
        <h3 class="section-title">{{ t('app.language') }}</h3>
        <p class="section-description">{{ t('pg.languageHint') }}</p>
        <div class="chips">
          <button
            v-for="item in availableLocales"
            :key="item.code"
            type="button"
            ms-button
            class="ms-button-filter-chip"
            :class="{ 'ms-button-active': locale === item.code }"
            :aria-pressed="locale === item.code"
            @click="setLocale(item.code)"
          >
            {{ item.label }}
          </button>
        </div>
      </section>
      <hr class="divider" />
      <section class="settings-section">
        <label class="microphone-label" for="studio-microphone">{{
          t('pg.microphoneSource')
        }}</label>
        <div v-tooltip="t('pg.microphoneTip')">
          <UiSelect
            id="studio-microphone"
            v-model="microphone"
            class="w-full"
            :aria-label="t('pg.microphoneSelect')"
          >
            <option value="">{{ t('pg.default') }}</option>
            <option v-for="item in microphones" :key="item.id" :value="item.id">
              {{ item.label }}
            </option>
          </UiSelect>
        </div>
      </section>
    </div>
    <template #actions>
      <button type="button" ms-button class="ms-button-primary" @click="open = false">
        {{ t('common.close') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.settings-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section-title {
  margin: 0 0 8px;
  color: var(--color-v3-text);
  font-size: 16px;
  font-weight: 500;
  line-height: 24px;
}

.section-description {
  margin: 0 0 12px;
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.divider {
  margin: 0;
  border: none;
  border-top: 1px solid var(--color-v3-outline-var);
}

.microphone-label {
  display: block;
  margin-bottom: 8px;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
}
</style>
