<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import { useTheme } from '@/theme'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { vTooltip } from '@/playground/ui/tooltip'
import { renderMermaid } from './mermaid'

const props = defineProps<{ code: string }>()
const { t } = useI18n()
const { showNotice } = useConsole()
const { theme } = useTheme()
// diagram 是渲染完成的图的 data URL，渲染失败或未完成时为空
const diagram = ref('')
const showCode = ref(false)
const expanded = ref(true)
const fullscreen = ref(false)
let timer: number | undefined
let version = 0

// darkColors 是官网深色主题下图的配色
const darkColors = {
  bg: '#141414',
  fg: '#fafafa',
  line: '#8a8a8a',
  accent: '#8cb7fa',
  muted: '#b5b5b5',
  surface: '#1f1f1f',
  border: '#424242',
}

// render 在 Worker 中用 beautiful-mermaid 把源码渲染为与官网相同的 SVG 图片
async function render(): Promise<void> {
  const current = ++version
  const svg = await renderMermaid({
    code: props.code,
    options: { font: 'Google Sans', ...(theme.value === 'dark' ? darkColors : {}) },
  })
  if (current === version)
    diagram.value = svg ? `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}` : ''
}

// 源码停止变化 300 ms 后渲染，生成中不随每次揭示重新布局
watch(
  [() => props.code, theme],
  () => {
    window.clearTimeout(timer)
    timer = window.setTimeout(() => void render(), 300)
  },
  { immediate: true },
)
onScopeDispose(() => window.clearTimeout(timer))

const toggleLabel = computed(() => (showCode.value ? t('pg.showDiagram') : t('pg.showCode')))
const expandLabel = computed(() =>
  expanded.value ? t('pg.collapseDiagram') : t('pg.expandDiagram'),
)
const fullscreenLabel = computed(() =>
  fullscreen.value ? t('pg.exitFullscreen') : t('pg.diagramFullscreen'),
)

// copy 把图的源码复制到剪贴板
async function copy(): Promise<void> {
  try {
    await navigator.clipboard.writeText(props.code)
    showNotice(t('pg.copied'), 'success')
  } catch {
    showNotice(t('pg.copyFailed'), 'error')
  }
}

// onKeydown 全屏时按 Esc 退出
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') fullscreen.value = false
}

watch(fullscreen, (open) => {
  if (open) document.addEventListener('keydown', onKeydown)
  else document.removeEventListener('keydown', onKeydown)
})
onScopeDispose(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="ms-mermaid">
    <button
      v-if="fullscreen"
      type="button"
      class="mermaid-fullscreen-backdrop"
      :aria-label="t('pg.exitFullscreen')"
      @click="fullscreen = false"
    ></button>
    <div class="mermaid-card" :class="{ fullscreen, collapsed: !expanded }">
      <div class="mermaid-header">
        <div class="header-content">
          <UiIcon name="hub" :size="20" class="title-icon" />
          <span class="title-text">{{ t('pg.diagram') }}</span>
        </div>
        <div class="actions-container">
          <button
            v-tooltip="toggleLabel"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            data-test-id="mermaid-toggle-code-button"
            :aria-label="toggleLabel"
            @click="showCode = !showCode"
          >
            <UiIcon :name="showCode ? 'hub' : 'code'" :size="20" />
          </button>
          <button
            v-tooltip="t('pg.copyCode')"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            data-test-id="mermaid-copy-button"
            :aria-label="t('pg.copyCode')"
            @click="copy"
          >
            <UiIcon name="copy" :size="20" />
          </button>
          <button
            v-tooltip="fullscreenLabel"
            type="button"
            ms-button
            class="fullscreen-button ms-button-borderless ms-button-icon"
            data-test-id="mermaid-fullscreen-button"
            :aria-label="fullscreenLabel"
            @click="fullscreen = !fullscreen"
          >
            <UiIcon :name="fullscreen ? 'fullscreenExit' : 'fullscreen'" :size="20" />
          </button>
          <button
            v-if="!fullscreen"
            v-tooltip="expandLabel"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            data-test-id="mermaid-expand-button"
            :aria-label="expandLabel"
            :aria-expanded="expanded"
            @click="expanded = !expanded"
          >
            <UiIcon :name="expanded ? 'expandLess' : 'chevronDown'" :size="20" />
          </button>
        </div>
      </div>
      <div v-if="expanded || fullscreen" class="mermaid-container">
        <img
          v-if="diagram && !showCode"
          class="mermaid-diagram"
          :src="diagram"
          :alt="t('pg.mermaidDiagram')"
        />
        <pre v-else class="mermaid-fallback" :class="{ nowrap: showCode }">{{ code }}</pre>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mermaid-card {
  overflow: hidden;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-xs);
}

.mermaid-card.fullscreen {
  position: fixed;
  z-index: 1000;
  inset: 2.5vh 2.5vw;
  display: flex;
  flex-direction: column;
  border-radius: 20px;
  background-color: var(--color-v3-surface);
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.32);
}

.mermaid-card.fullscreen .mermaid-header {
  background-color: var(--color-v3-surface-container-high, var(--color-v3-surface-container));
}

.mermaid-card.fullscreen .mermaid-container {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 20px;
}

.mermaid-card.fullscreen .mermaid-diagram {
  width: 100%;
  height: 100%;
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.mermaid-card.collapsed .mermaid-header {
  border-bottom: none;
}

.mermaid-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 36px;
  padding-inline: 12px 6px;
  border-bottom: 1px solid var(--color-v3-outline-var, var(--color-v3-outline));
}

.header-content {
  display: flex;
  align-items: center;
  min-width: 0;
  overflow: hidden;
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.title-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.title-icon {
  flex-shrink: 0;
  margin-right: 4px;
  color: var(--color-v3-text-var);
}

.actions-container {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.mermaid-container {
  display: flex;
  align-items: center;
  justify-content: center;
  overflow-x: auto;
  padding: 12px;
}

.mermaid-diagram {
  display: block;
  max-width: 100%;
  height: auto;
}

.mermaid-fallback {
  width: 100%;
  margin: 0;
  color: var(--color-v3-text);
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1.5;
  overflow-wrap: break-word;
  white-space: pre-wrap;
}

.mermaid-fallback.nowrap {
  overflow-x: auto;
  overflow-wrap: normal;
  white-space: pre;
}

.mermaid-fullscreen-backdrop {
  position: fixed;
  z-index: 999;
  inset: 0;
  padding: 0;
  border: 0;
  background: rgba(0, 0, 0, 0.5);
  cursor: pointer;
}
</style>
