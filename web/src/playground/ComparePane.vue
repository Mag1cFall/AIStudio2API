<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import ChatSession from './ChatSession.vue'
import GetCodeDialog from './GetCodeDialog.vue'
import ModelCard from './ModelCard.vue'
import RunSettings from './RunSettings.vue'
import { providePlayground, useCompare, type Playground } from './session'

const props = defineProps<{ pane: Playground; index: number }>()
const { t, locale } = useI18n()
const { closePane } = useCompare()
providePlayground(props.pane)
const { session, currentModel } = props.pane

const settingsOpen = ref(false)
const codeOpen = ref(false)
const confirmClose = ref(false)

// tokens 是窗格对话的 token 数
const tokens = computed(() =>
  session.promptTokens === null
    ? ''
    : t('pg.tokens').replace(
        '{count}',
        new Intl.NumberFormat(locale.value).format(session.promptTokens),
      ),
)

// close 确认后关闭窗格
function close(): void {
  confirmClose.value = false
  closePane(props.index)
}
</script>

<template>
  <div class="chat-view-container side-by-side">
    <div class="chat-settings-header-container">
      <ModelCard
        :model="currentModel"
        kind="chat"
        side-by-side
        :has-content="session.turns.length > 0"
        @select="pane.selectModel($event.id)"
      />
      <span v-if="tokens" class="token-count">{{ tokens }}</span>
      <button
        v-tooltip.below="t('pg.paneGetCodeTip')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.paneGetCode')"
        @click="codeOpen = true"
      >
        <UiIcon name="code" :size="20" />
      </button>
      <button
        v-tooltip.below="t('pg.showRunSettings')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.showRunSettings')"
        @click="settingsOpen = true"
      >
        <UiIcon name="options" :size="20" />
      </button>
      <button
        v-tooltip.below="t('pg.closeChat')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.closeChat')"
        @click="confirmClose = true"
      >
        <UiIcon name="close" :size="20" />
      </button>
    </div>
    <ChatSession side-by-side />
    <div v-if="settingsOpen" class="settings-overlay" @click="settingsOpen = false"></div>
    <RunSettings
      :open="settingsOpen"
      narrow
      :title="index === 0 ? t('pg.runSettingsLeft') : t('pg.runSettingsRight')"
      @close="settingsOpen = false"
    />
    <GetCodeDialog v-model:open="codeOpen" />
    <UiDialog v-model:open="confirmClose" :title="t('pg.closeChatTitle')" width="420px">
      <p class="confirm-text">{{ t('pg.closeChatBody') }}</p>
      <template #actions>
        <button type="button" ms-button class="ms-button-borderless" @click="confirmClose = false">
          {{ t('common.cancel') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="close">
          {{ t('pg.continue') }}
        </button>
      </template>
    </UiDialog>
  </div>
</template>

<style scoped>
.chat-view-container {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.chat-view-container:not(:first-child) {
  border-left: 1px solid var(--color-inverse-on-surface);
}

@media screen and (max-width: 768px) {
  .chat-view-container:not(:first-child) {
    display: none;
  }
}

.chat-settings-header-container {
  display: flex;
  align-items: center;
  justify-content: end;
  gap: 8px;
  margin: 12px 20px;
}

.token-count {
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
}

.settings-overlay {
  position: fixed;
  z-index: 4;
  inset: 0;
  background-color: var(--color-overlay-background);
}

.confirm-text {
  margin: 0;
  color: var(--color-v3-text-var);
}
</style>
