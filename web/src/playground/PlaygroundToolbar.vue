<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useRouter } from 'vue-router'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { isOmniModel } from './catalog'
import { useMediaQuery } from './media-query'
import { useCompare, usePlayground } from './session'

defineProps<{ settingsOpen: boolean; narrow: boolean }>()
const emit = defineEmits<{ newChat: []; toggleSettings: [] }>()
const { t, locale } = useI18n()
const router = useRouter()
const playground = usePlayground()
const { session } = playground
const { comparing, enterCompare, toggleRaw } = useCompare()
// handset 与官网一致，768 px 及以下没有对比模式
const handset = useMediaQuery('(max-width: 768px)')

const editing = ref(false)
const titleDraft = ref('')
const confirmDelete = ref(false)

// hasPrompt 表示对话已有内容，标题可编辑
const hasPrompt = computed(() => session.turns.length > 0 || session.id !== null)
// temporaryLabel 与官网一样在临时对话不可用时显示原因
const temporaryLabel = computed(() =>
  isOmniModel(playground.currentModel.value)
    ? t('pg.temporaryOmni')
    : session.id !== null
      ? t('pg.temporarySaved')
      : t('pg.temporaryChat'),
)
// title 是工具栏标题：空白新对话为 Playground，已有内容未命名时为 Untitled prompt
const title = computed(() =>
  hasPrompt.value ? session.title || t('pg.untitled') : t('nav.playground'),
)
// tokens 是当前对话的 token 数
const tokens = computed(() =>
  session.promptTokens === null
    ? ''
    : t('pg.tokens').replace(
        '{count}',
        new Intl.NumberFormat(locale.value).format(session.promptTokens),
      ),
)

// openRename 打开标题编辑对话框
function openRename(): void {
  titleDraft.value = session.title
  editing.value = true
}

// saveTitle 保存编辑后的标题
function saveTitle(): void {
  playground.rename(titleDraft.value)
  editing.value = false
}

// makeCopy 复制当前对话并打开副本
async function makeCopy(): Promise<void> {
  const id = await playground.copyPrompt()
  void router.push(`/prompts/${id}`)
}

// removePrompt 删除当前对话并回到新对话
async function removePrompt(): Promise<void> {
  confirmDelete.value = false
  await playground.deletePrompt()
  void router.push('/prompts/new_chat')
}
</script>

<template>
  <header class="ms-header">
    <div class="toolbar-left">
      <div class="page-title">
        <h1 class="mode-title">{{ title }}</h1>
        <button
          v-if="hasPrompt && !session.temporary"
          v-tooltip.below="t('pg.editTitle')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon ms-button-small"
          :aria-label="t('pg.editTitle')"
          @click="openRename"
        >
          <UiIcon name="edit" :size="18" />
        </button>
      </div>
      <span v-if="tokens && !comparing" class="token-count">{{ tokens }}</span>
    </div>
    <div class="toolbar-right">
      <span
        v-if="session.saveState === 'saving'"
        v-tooltip.below="t('pg.saving')"
        role="status"
        class="saving-indicator"
        :aria-label="t('pg.saving')"
      >
        <UiIcon name="save" :size="18" />
      </span>
      <span
        v-else-if="session.saveState === 'error'"
        v-tooltip.below="t('pg.saveFailed')"
        role="status"
        class="saving-indicator error"
        :aria-label="t('pg.saveFailed')"
      >
        <UiIcon name="warning" :size="18" />
      </span>
      <span v-if="session.temporary" class="temporary-badge">
        <UiIcon name="incognito" :size="18" />
        {{ t('pg.temporaryChat') }}
      </span>
      <button
        v-if="!comparing && !handset"
        v-tooltip.below="t('pg.compareMode')"
        type="button"
        ms-button
        class="compare-button ms-button-borderless ms-button-icon"
        :aria-label="t('pg.compareMode')"
        @click="enterCompare"
      >
        <UiIcon name="compare" :size="20" />
      </button>
      <button
        v-tooltip.below="hasPrompt ? t('pg.newChat') : t('pg.alreadyNew')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newChat')"
        :aria-disabled="!hasPrompt"
        @click="hasPrompt && emit('newChat')"
      >
        <UiIcon name="add" :size="20" />
      </button>
      <DropdownMenuRoot :modal="false">
        <DropdownMenuTrigger
          v-tooltip.below="t('pg.moreActions')"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :aria-label="t('pg.moreActions')"
        >
          <UiIcon name="more" :size="20" />
        </DropdownMenuTrigger>
        <DropdownMenuPortal>
          <DropdownMenuContent align="end" :side-offset="4" class="ms-menu-panel">
            <DropdownMenuItem
              class="ms-menu-item"
              :disabled="session.id !== null || isOmniModel(playground.currentModel.value)"
              @select="session.temporary = !session.temporary"
            >
              <UiIcon name="incognito" :size="18" />
              <span class="ms-menu-item-text">{{ temporaryLabel }}</span>
              <span class="ms-menu-radio" :class="{ selected: session.temporary }"></span>
            </DropdownMenuItem>
            <DropdownMenuItem
              class="ms-menu-item"
              :disabled="
                session.temporary || session.turns.length === 0 || session.saveState === 'saved'
              "
              @select="playground.save()"
            >
              <UiIcon name="save" :size="18" />
              <span class="ms-menu-item-text">{{
                session.saveState === 'saved' ? t('pg.noChanges') : t('pg.savePrompt')
              }}</span>
            </DropdownMenuItem>
            <DropdownMenuItem
              class="ms-menu-item"
              :disabled="session.id === null"
              @select="makeCopy"
            >
              <UiIcon name="copy" :size="18" />
              <span class="ms-menu-item-text">{{ t('pg.makeCopy') }}</span>
            </DropdownMenuItem>
            <DropdownMenuItem
              class="ms-menu-item"
              :disabled="session.id === null"
              @select="confirmDelete = true"
            >
              <UiIcon name="delete" :size="18" />
              <span class="ms-menu-item-text">{{ t('common.delete') }}</span>
            </DropdownMenuItem>
            <DropdownMenuItem class="ms-menu-item" @select="toggleRaw">
              <UiIcon name="rawMode" :size="18" />
              <span class="ms-menu-item-text">{{ t('pg.rawMode') }}</span>
              <span class="ms-menu-radio" :class="{ selected: session.rawMode }"></span>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenuPortal>
      </DropdownMenuRoot>
      <Transition name="runsettings-toggle">
        <button
          v-if="!comparing && (narrow || !settingsOpen)"
          v-tooltip.below="t('pg.toggleSettings')"
          type="button"
          ms-button
          class="ms-button-primary ms-button-icon"
          :aria-label="t('pg.toggleSettings')"
          @click="emit('toggleSettings')"
        >
          <UiIcon name="options" :size="20" />
        </button>
      </Transition>
    </div>
    <UiDialog v-model:open="editing" :title="t('pg.editTitle')" width="480px">
      <label class="field-label" for="prompt-title">{{ t('pg.title') }}</label>
      <input
        id="prompt-title"
        v-model="titleDraft"
        ms-input
        class="title-input"
        maxlength="120"
        @keydown.enter.prevent="saveTitle"
      />
      <template #actions>
        <button type="button" ms-button class="ms-button-borderless" @click="editing = false">
          {{ t('common.cancel') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="saveTitle">
          {{ t('common.save') }}
        </button>
      </template>
    </UiDialog>
    <UiDialog v-model:open="confirmDelete" :title="t('pg.deleteTitle')" width="420px">
      <p class="confirm-text">{{ t('pg.deleteBody') }}</p>
      <template #actions>
        <button type="button" ms-button class="ms-button-borderless" @click="confirmDelete = false">
          {{ t('common.cancel') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="removePrompt">
          {{ t('common.delete') }}
        </button>
      </template>
    </UiDialog>
  </header>
</template>

<style scoped>
.ms-header {
  position: relative;
  z-index: 2;
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 16px 20px 8px var(--ms-header-padding-left, 20px);
  backdrop-filter: blur(5px);
  background-color: var(--color-v3-overlay-background);
}

.ms-header::after {
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  height: 32px;
  background: linear-gradient(to bottom, var(--color-v3-surface) 0, transparent 100%);
  content: '';
  pointer-events: none;
  transform: translateY(100%);
}

@media screen and (max-width: 480px) {
  .ms-header {
    padding: 16px 8px 8px var(--ms-header-padding-left, 8px);
  }
}

.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 4px;
}

.toolbar-left {
  min-width: 0;
}

.toolbar-right {
  grid-column: 3;
}

.page-title {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 24px;
  margin-inline: 12px;
  overflow: hidden;
}

.mode-title {
  margin: 0;
  overflow: hidden;
  font-size: 16px;
  font-weight: 500;
  line-height: 24px;
  text-overflow: ellipsis;
  white-space: nowrap;
  animation: fade-in 225ms cubic-bezier(0, 0, 0.2, 1);
}

.token-count {
  padding: 0 8px;
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
}

.saving-indicator {
  display: flex;
  padding: 4px;
  color: var(--color-v3-text-var);
}

.saving-indicator.error {
  color: var(--color-v3-error-text);
}

.temporary-badge {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.field-label {
  display: block;
  margin-bottom: 8px;
  font-weight: 500;
}

.title-input {
  width: 100%;
}

.confirm-text {
  margin: 0;
  color: var(--color-v3-text-var);
}

.runsettings-toggle-enter-active {
  overflow: hidden;
  transition:
    width 225ms cubic-bezier(0, 0, 0.2, 1),
    opacity 225ms cubic-bezier(0, 0, 0.2, 1);
}

.runsettings-toggle-enter-from {
  width: 0;
  opacity: 0;
}

.runsettings-toggle-enter-to {
  width: 32px;
}

@keyframes fade-in {
  from {
    opacity: 0;
  }
}
</style>
