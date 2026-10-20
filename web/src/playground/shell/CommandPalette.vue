<script setup lang="ts">
import arrowForward from '@material-symbols/svg-300/outlined/arrow_forward.svg?raw'
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api'
import { useConsole } from '@/console'
import { useI18n, type TranslationKey } from '@/i18n'
import type { Model, PromptSummary } from '@/types'
import { hasCategory, modelIcon } from '@/playground/catalog'
import { pageKindOf, pageRoutes } from '@/playground/media/kinds'
import { usePlayground } from '@/playground/session'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'

// PaletteMode 是命令面板当前的列表：根列表、/chat 模型列表、/chat 选定模型后的提示词输入或 /shortcuts 快捷键列表
export type PaletteMode = 'root' | 'chat' | 'prompt' | 'shortcuts'

// PaletteItem 是命令面板中的一行
interface PaletteItem {
  id: string
  icon?: IconName
  label: string
  description: string
  keys?: string[]
  submenu?: boolean
  run: () => void
}

const open = defineModel<boolean>('open', { required: true })
const mode = defineModel<PaletteMode>('mode', { required: true })
const { t } = useI18n()
const router = useRouter()
const store = useConsole()
const playground = usePlayground()
const query = ref('')
const selected = ref(-1)
// firstSelection 是列表刷新后的选中行：/chat 列表默认选中第一行，其余列表不选中
const firstSelection = (value: PaletteMode = mode.value) => (value === 'chat' ? 0 : -1)
const recent = ref<PromptSummary[]>([])
const input = ref<HTMLInputElement>()
const list = ref<HTMLElement>()
// chosen 是 /chat 中选定的模型
const chosen = ref<Model>()

// shortcuts 是快捷键列表中的按键与说明，与官网快捷键注册一致
const shortcuts: { label: TranslationKey; description: TranslationKey; keys: string[] }[] = [
  { label: 'pg.shortcutFocus', description: 'pg.shortcutFocusHint', keys: ['/'] },
  { label: 'pg.shortcutPalette', description: 'pg.shortcutPaletteHint', keys: ['Ctrl', '/'] },
  { label: 'pg.shortcutRaw', description: 'pg.shortcutRawHint', keys: ['Shift', 'R'] },
  {
    label: 'pg.shortcutSettings',
    description: 'pg.shortcutSettingsHint',
    keys: ['Ctrl', 'Shift', 'S'],
  },
  {
    label: 'pg.paletteShortcuts',
    description: 'pg.shortcutListHint',
    keys: ['Ctrl', 'Shift', '/'],
  },
]

// close 关闭命令面板
function close(): void {
  open.value = false
}

// press 关闭面板后向页面派发快捷键，交给页面已有的快捷键处理
function press(init: KeyboardEventInit): void {
  close()
  void nextTick(() => window.dispatchEvent(new KeyboardEvent('keydown', init)))
}

// openPage 关闭面板并打开地址
function openPage(path: string): void {
  close()
  void router.push(path)
}

// startChat 以输入的文字开始新对话，文字填入输入框
function startChat(text: string): void {
  close()
  playground.newChat()
  playground.session.draft = text
  void router.push('/prompts/new_chat')
}

// sendPrompt 以选定模型开始新对话，输入文字时直接发送
async function sendPrompt(model: Model, text: string): Promise<void> {
  close()
  playground.newChat()
  await router.push(`/prompts/new_chat?model=${encodeURIComponent(model.id)}`)
  playground.session.draft = text
  if (text.trim() !== '') await playground.submit()
}

// modelItem 返回 /chat 列表中的一个模型
function modelItem(model: Model): PaletteItem {
  return {
    id: `model-${model.id}`,
    icon: modelIcon(model),
    label: model.name,
    description: t('pg.startChatWith').replace('{model}', model.name),
    submenu: pageKindOf(model) === 'chat',
    run: () => {
      if (pageKindOf(model) !== 'chat') {
        openPage(`${pageRoutes[pageKindOf(model)]}?model=${encodeURIComponent(model.id)}`)
        return
      }
      chosen.value = model
      setMode('prompt')
    },
  }
}

// sessionLabel 返回最近记录的会话类型
function sessionLabel(summary: PromptSummary): string {
  const model = store.models.value.find((item) => item.id === summary.model)
  return pageKindOf(model) === 'video' ? t('pg.videoSession') : t('pg.chatSession')
}

// matches 判断文字是否包含查询
const matches = (text: string) => text.toLowerCase().includes(query.value.trim().toLowerCase())

// fuzzy 判断查询中的字符是否按顺序出现在文字中
function fuzzy(text: string): boolean {
  const target = text.toLowerCase()
  let index = 0
  for (const char of query.value.replace(/\s/g, '').toLowerCase()) {
    index = target.indexOf(char, index) + 1
    if (index === 0) return false
  }
  return true
}

// sections 是当前模式下按分组排列的行，相邻分组之间显示分隔线
const sections = computed<{ title: string; items: PaletteItem[] }[]>(() => {
  const text = query.value.trim()
  if (mode.value === 'prompt') return []
  if (mode.value === 'shortcuts')
    return [
      {
        title: '',
        items: shortcuts
          .filter((item) => matches(t(item.label)) || matches(t(item.description)))
          .map((item) => ({
            id: item.label,
            label: t(item.label),
            description: t(item.description),
            keys: item.keys,
            run: () => runShortcut(item.label),
          })),
      },
    ]
  if (mode.value === 'chat') {
    const models = store.models.value
    const featured = models.filter((model) => hasCategory(model, 10))
    const explore: PaletteItem = {
      id: 'explore',
      icon: 'newChat',
      label: t('pg.exploreModels'),
      description: t('pg.seeAllModels'),
      run: () => openPage('/prompts/new_chat'),
    }
    if (text === '')
      return [{ title: t('pg.featured'), items: [explore, ...featured.map(modelItem)] }]
    return [
      {
        title: '',
        items: models.filter((model) => matches(model.name) || matches(model.id)).map(modelItem),
      },
    ]
  }
  const commands: PaletteItem[] = [
    {
      id: 'chat',
      icon: 'chat',
      label: '/chat',
      description: t('pg.paletteChat'),
      submenu: true,
      run: () => setMode('chat'),
    },
    {
      id: 'key',
      icon: 'key',
      label: '/key',
      description: t('pg.paletteKey'),
      run: () => openPage('/?tab=settings'),
    },
    {
      id: 'shortcuts',
      icon: 'keyboardReturn',
      label: '/shortcuts',
      description: t('pg.paletteShortcuts'),
      submenu: true,
      run: () => setMode('shortcuts'),
    },
  ]
  const sessions = recent.value
    .filter((summary) => text === '' || fuzzy(summary.title))
    .map<PaletteItem>((summary) => ({
      id: summary.id,
      icon: 'chatBubble',
      label: summary.title || t('pg.untitled'),
      description: sessionLabel(summary),
      run: () => openPage(`/prompts/${encodeURIComponent(summary.id)}`),
    }))
  if (text === '')
    return [
      { title: '', items: commands },
      { title: t('pg.recentActivity'), items: sessions },
    ]
  const found = commands.filter((item) => item.label.startsWith(text.split(' ')[0] ?? ''))
  if (text.startsWith('/')) return [{ title: '', items: found }]
  return [
    { title: '', items: sessions },
    {
      title: '',
      items: [
        {
          id: 'start',
          icon: 'chat',
          label: t('pg.startChatText').replace('{text}', text),
          description: t('pg.startChatHint'),
          run: () => startChat(text),
        },
      ],
    },
  ]
})

// items 是全部可选的行，按显示顺序
const items = computed(() => sections.value.flatMap((section) => section.items))

// runShortcut 执行快捷键列表中的一项
function runShortcut(label: TranslationKey): void {
  if (label === 'pg.shortcutFocus') press({ key: '/' })
  else if (label === 'pg.shortcutPalette') setMode('root')
  else if (label === 'pg.shortcutRaw') press({ key: 'R', shiftKey: true })
  else if (label === 'pg.shortcutSettings') press({ key: 'S', ctrlKey: true, shiftKey: true })
  else setMode('shortcuts')
}

// setMode 切换面板列表并清空输入
function setMode(next: PaletteMode): void {
  mode.value = next
  query.value = ''
  selected.value = firstSelection(next)
  void nextTick(() => input.value?.focus())
}

// onKeydown 处理面板内的方向键、回车、退格与 Escape
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    const count = items.value.length
    if (count === 0) return
    if (event.key === 'ArrowDown') selected.value = (selected.value + 1) % count
    else selected.value = selected.value <= 0 ? count - 1 : selected.value - 1
    void nextTick(() =>
      list.value?.querySelector('.result-item.selected')?.scrollIntoView({ block: 'nearest' }),
    )
  } else if (event.key === 'Enter') {
    event.preventDefault()
    if (mode.value === 'prompt' && chosen.value) void sendPrompt(chosen.value, query.value)
    else items.value[Math.max(selected.value, 0)]?.run()
  } else if (event.key === 'Escape') {
    event.preventDefault()
    if (mode.value === 'root') close()
    else setMode('root')
  } else if (event.key === 'Backspace' && query.value === '' && mode.value !== 'root') {
    event.preventDefault()
    setMode(mode.value === 'prompt' ? 'chat' : 'root')
  }
}

watch(query, () => (selected.value = firstSelection()))
watch(open, async (value) => {
  if (!value) return
  query.value = ''
  selected.value = firstSelection()
  void nextTick(() => input.value?.focus())
  if (mode.value === 'root')
    recent.value = (await api.prompts().catch(() => [] as PromptSummary[]))
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
      .slice(0, 10)
})
</script>

<template>
  <Transition name="palette">
    <div v-if="open" class="overlay-backdrop" @mousedown.self="close">
      <div
        role="dialog"
        aria-modal="true"
        :aria-label="t('pg.commandPalette')"
        class="overlay-content"
        @keydown="onKeydown"
      >
        <label class="input-wrapper">
          <span v-if="mode !== 'root'" class="chips-wrapper">
            <span class="chip">
              <UiIcon :name="mode === 'shortcuts' ? 'keyboardReturn' : 'chat'" :size="18" />
              /{{ mode === 'shortcuts' ? 'shortcuts' : 'chat' }}
            </span>
            <span v-if="mode === 'prompt' && chosen" class="chip">
              <UiIcon :name="modelIcon(chosen)" :size="18" />
              {{ chosen.name }}
            </span>
          </span>
          <span class="input-content">
            <UiIcon v-if="mode === 'root'" name="search" :size="20" class="input-icon" />
            <input
              ref="input"
              v-model="query"
              type="text"
              role="combobox"
              aria-haspopup="listbox"
              aria-controls="palette-results"
              aria-expanded="true"
              :placeholder="
                mode === 'prompt'
                  ? t('pg.typePrompt')
                  : mode === 'chat'
                    ? t('pg.selectModelHint')
                    : mode === 'shortcuts'
                      ? t('pg.searchShortcuts')
                      : t('pg.palettePlaceholder')
              "
              :aria-label="t('pg.palettePlaceholder')"
            />
            <span
              v-if="mode === 'root' && query === ''"
              class="shortcut input-shortcut-hint"
              aria-hidden="true"
            >
              <kbd>Ctrl</kbd><kbd>/</kbd>
            </span>
            <span v-else class="action-icon" aria-hidden="true" v-html="arrowForward"></span>
          </span>
        </label>
        <div class="menu-overlay">
          <ul id="palette-results" ref="list" role="listbox" class="results-list">
            <template v-for="(section, index) in sections" :key="index">
              <li
                v-if="
                  !section.title &&
                  index > 0 &&
                  section.items.length &&
                  sections[index - 1]?.items.length
                "
                role="presentation"
              >
                <div role="separator" class="divider"></div>
              </li>
              <li v-if="section.title && section.items.length" role="presentation">
                <div role="presentation" class="category-header">{{ section.title }}</div>
              </li>
              <li
                v-for="item in section.items"
                :key="item.id"
                role="option"
                class="result-item"
                :class="{ selected: items.indexOf(item) === selected }"
                :aria-selected="items.indexOf(item) === selected"
                @click="item.run()"
              >
                <span v-if="item.icon" class="icon-container">
                  <UiIcon :name="item.icon" :size="20" class="item-icon" />
                </span>
                <span class="item-content">
                  <span class="item-label">{{ item.label }}</span>
                  <span class="item-description">{{ item.description }}</span>
                </span>
                <span v-if="item.keys" class="shortcut">
                  <kbd v-for="key in item.keys" :key="key">{{ key }}</kbd>
                </span>
                <UiIcon v-if="item.submenu" name="chevronRight" :size="18" class="menu-chevron" />
              </li>
            </template>
            <li v-if="items.length === 0" class="empty-state">
              {{ mode === 'prompt' ? t('pg.pressEnterToSend') : t('pg.noResults') }}
            </li>
          </ul>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.overlay-backdrop {
  position: fixed;
  z-index: 1100;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  backdrop-filter: blur(8px);
  background-color: var(--color-v3-overlay-background);
}

@media screen and (max-width: 600px) {
  .overlay-backdrop {
    align-items: flex-start;
    padding: 12px;
  }
}

.overlay-content {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 600px;
  min-height: calc(50vh + 68px);
  border-radius: 8px;
}

.input-wrapper {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  box-sizing: border-box;
  min-height: 56px;
  padding: 6px 12px;
  overflow: hidden;
  border: 1px solid var(--color-v3-outline);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-lg);
  cursor: text;
  transition: all 0.2s ease;
}

@media screen and (max-width: 600px) {
  .input-wrapper {
    flex-direction: column;
    align-items: stretch;
    gap: 4px;
  }
}

.chips-wrapper {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.input-wrapper:focus-within {
  border-color: var(--color-v3-outline-accent);
  background: var(--color-v3-surface);
}

.input-content {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 8px;
}

.input-icon {
  flex-shrink: 0;
  margin-right: 4px;
  color: var(--color-v3-text-var);
  opacity: 0.7;
}

.input-wrapper:focus-within .input-icon {
  color: var(--color-v3-text);
  opacity: 1;
}

.action-icon {
  display: inline-flex;
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  margin-left: 4px;
  color: var(--color-v3-text-disable);
}

.action-icon :deep(svg) {
  width: 100%;
  height: 100%;
  fill: currentColor;
}

input {
  flex: 1;
  min-width: 120px;
  height: 44px;
  padding: 0;
  border: none;
  outline: none;
  background: transparent;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
}

.chip {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 4px;
  background: var(--color-v3-surface-container-high);
  color: var(--color-v3-text);
  font-size: 12px;
  line-height: 16px;
  white-space: nowrap;
  pointer-events: none;
}

.shortcut {
  display: flex;
  gap: 4px;
}

.input-shortcut-hint {
  display: none;
}

@media (min-width: 600px) {
  .input-shortcut-hint {
    display: flex;
  }
}

.shortcut kbd {
  min-width: 20px;
  padding: 4px 8px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 4px;
  background-color: var(--color-v3-surface-container-high);
  color: var(--color-v3-text-var);
  font-family: var(--font-sans);
  font-size: 12px;
  line-height: 18px;
  text-align: center;
}

.divider {
  margin: 4px 0;
  border-top: 1px solid var(--color-v3-outline-var);
}

.menu-overlay {
  margin-top: 8px;
  overflow: hidden;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-lg);
  animation: palette-slide-down 0.2s ease-out;
}

.results-list {
  position: relative;
  max-height: 50vh;
  margin: 0;
  padding: 6px;
  overflow-y: auto;
  list-style: none;
}

.category-header {
  margin-top: 4px;
  padding: 12px 12px 4px;
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
  cursor: default;
}

li:first-child .category-header {
  margin-top: 0;
}

.result-item {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 2px;
  padding: 8px 12px;
  border-radius: 8px;
  color: var(--color-v3-text);
  cursor: pointer;
  transition: background-color 0.1s ease;
}

.result-item:hover {
  background: var(--color-v3-hover);
}

.result-item:hover .icon-container {
  border-color: var(--color-v3-outline);
  background: var(--color-v3-button-container-high);
}

.result-item:hover .item-icon {
  color: var(--color-v3-text);
}

.result-item.selected {
  background: var(--color-v3-button-container-high);
}

.icon-container {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  background: var(--color-v3-surface-container-high);
  transition: all 0.2s ease;
}

.result-item.selected .icon-container {
  border-color: var(--color-v3-outline);
  background: var(--color-v3-button-container-highest);
}

.item-icon {
  color: var(--color-v3-text-var);
}

.result-item.selected .item-icon {
  color: var(--color-v3-text);
}

.item-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.item-label {
  overflow: hidden;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-description {
  overflow: hidden;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.menu-chevron {
  flex-shrink: 0;
  color: var(--color-v3-text-var);
}

.empty-state {
  padding: 16px;
  color: var(--color-v3-text-var);
  text-align: center;
}

.palette-enter-active {
  animation: palette-fade-in 0.2s ease-in-out;
}

@keyframes palette-fade-in {
  from {
    opacity: 0;
  }
}

@keyframes palette-slide-down {
  from {
    opacity: 0;
    transform: translateY(-10px);
  }
}
</style>
