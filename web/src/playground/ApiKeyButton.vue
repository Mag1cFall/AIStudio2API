<script setup lang="ts">
import { nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

const router = useRouter()
const { t } = useI18n()

// openApiKey 打开管理端设置页中的 API 密钥设置
async function openApiKey(): Promise<void> {
  await router.push({ path: '/', query: { tab: 'settings' } })
  await nextTick()
  document.getElementById('setting-api-key')?.scrollIntoView({ block: 'center' })
}
</script>

<template>
  <button
    v-tooltip.below="t('pg.apiKey')"
    type="button"
    ms-button
    class="ms-button-filter-chip"
    :aria-label="t('pg.apiKey')"
    @click="openApiKey"
  >
    <UiIcon name="keyOff" :size="18" />
  </button>
</template>
