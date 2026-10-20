<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import ChatSession from './ChatSession.vue'
import ComparePane from './ComparePane.vue'
import PlaygroundToolbar from './PlaygroundToolbar.vue'
import PromptBox from './PromptBox.vue'
import RunSettings from './RunSettings.vue'
import ZeroState from './ZeroState.vue'
import { defaultImageModel, isChatModel } from './catalog'
import type { ZeroStarter } from './zero-configs'
import { useCompare, usePlayground } from './session'
import type { PromptData } from './types'
import type { SavedPrompt } from '@/types'

defineOptions({ name: 'PlaygroundPage' })

const props = defineProps<{ prompt?: SavedPrompt<PromptData> | undefined }>()

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useConsole()
const playground = usePlayground()
const { session } = playground
const { panes, comparing, toggleRaw } = useCompare()

// narrowQuery 与官网一致，960 px 以下运行设置为右侧滑出面板
const narrowQuery = window.matchMedia('(max-width: 960px)')
const narrow = ref(narrowQuery.matches)
const settingsOpen = ref(!narrow.value)
const onNarrowChange = (event: MediaQueryListEvent): void => {
  narrow.value = event.matches
  settingsOpen.value = !event.matches
}

const promptBox = ref<InstanceType<typeof PromptBox>>()

// useStarter 用空态示例填写输入框并聚焦
function useStarter(item: ZeroStarter): void {
  promptBox.value?.focus()
  void playground.applyStarter(item)
}
watch(
  () => route.params.id,
  async (id) => {
    if (typeof id === 'string' && id !== '') await playground.open(id, props.prompt)
    else if (session.id !== null || session.turns.length > 0) playground.newChat()
  },
  { immediate: true },
)
watch(
  () => store.models.value,
  () => playground.ensureModel(),
  { immediate: true },
)
watch(
  () => [route.query.model, store.models.value] as const,
  ([id, models]) => {
    if (
      typeof id === 'string' &&
      route.params.id === undefined &&
      session.turns.length === 0 &&
      session.settings.model !== id &&
      models.some((model) => model.id === id && isChatModel(model))
    )
      playground.selectModel(id)
  },
  { immediate: true },
)
// 图片页没有指定模型时与官网一样使用目录中的图片模型，不改变对话页记住的模型；从空白图片页回到对话页时恢复对话模型
watch(
  () => [route.name, route.query.model, store.models.value] as const,
  ([name, id, models], previous) => {
    if (id !== undefined || session.turns.length > 0) return
    if (name !== 'image') {
      if (previous?.[0] === 'image') playground.newChat()
      return
    }
    const model = defaultImageModel(models)
    if (model !== undefined && session.settings.model !== model.id)
      playground.selectModel(model.id, false)
  },
  { immediate: true },
)
watch(
  () => session.id,
  (id) => {
    if (id !== null && route.params.id !== id) void router.replace(`/prompts/${id}`)
  },
)

// editable 返回事件目标是否为可输入元素
function editable(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null
  return (
    element !== null &&
    (element.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(element.tagName))
  )
}

// onKeydown 处理官网快捷键：/ 聚焦输入框，Ctrl+Shift+S 切换运行设置，Shift+R 切换原始模式
function onKeydown(event: KeyboardEvent): void {
  if (event.defaultPrevented || document.querySelector('[role="dialog"]')) return
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === 's') {
    event.preventDefault()
    settingsOpen.value = !settingsOpen.value
    return
  }
  if (editable(event.target) || event.ctrlKey || event.metaKey || event.altKey) return
  if (event.key === '/') {
    event.preventDefault()
    promptBox.value?.focus()
  } else if (event.shiftKey && event.key === 'R') {
    event.preventDefault()
    toggleRaw()
  }
}

// newChat 与官网一致打开保留当前系统指令的新对话
function newChat(): void {
  playground.newChat(session.settings.systemInstruction)
  void router.push('/prompts/new_chat')
}

onMounted(() => {
  narrowQuery.addEventListener('change', onNarrowChange)
  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  narrowQuery.removeEventListener('change', onNarrowChange)
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="playground">
    <div class="chat-column">
      <PlaygroundToolbar
        :settings-open="settingsOpen"
        :narrow="narrow"
        @new-chat="newChat"
        @toggle-settings="settingsOpen = !settingsOpen"
      />
      <div v-if="comparing" class="chat-container">
        <ComparePane v-for="(pane, index) in panes" :key="index" :pane="pane" :index="index" />
      </div>
      <div v-else class="chat-area">
        <div v-if="session.loadError === 'not_found'" class="load-error">
          <h2>{{ t('pg.notFoundTitle') }}</h2>
          <p>{{ t('pg.notFoundBody') }}</p>
          <button type="button" ms-button class="ms-button-primary" @click="newChat">
            {{ t('pg.newChat') }}
          </button>
        </div>
        <ZeroState v-else-if="session.turns.length === 0" @starter="useStarter" />
        <ChatSession v-else />
      </div>
      <PromptBox ref="promptBox" />
    </div>
    <template v-if="!comparing">
      <div
        v-if="narrow && settingsOpen"
        class="settings-overlay"
        @click="settingsOpen = false"
      ></div>
      <RunSettings :open="settingsOpen" :narrow="narrow" @close="settingsOpen = false" />
    </template>
  </div>
</template>

<style scoped>
.playground {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  background-color: var(--color-v3-surface);
}

.chat-column {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.chat-area {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.chat-container {
  display: flex;
  flex: 1;
  min-height: 0;
}

.settings-overlay {
  position: fixed;
  z-index: 4;
  inset: 0;
  background-color: var(--color-overlay-background);
}

.load-error {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 24px;
  text-align: center;
}

.load-error h2 {
  margin: 0;
  font-family: var(--font-heading);
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
}

.load-error p {
  margin: 0;
  color: var(--color-v3-text-var);
}
</style>
