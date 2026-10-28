<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import CodeDialog from '../CodeDialog.vue'
import ModelCard from '../ModelCard.vue'
import RunSettingsFrame from '../RunSettingsFrame.vue'
import MediaLayout from './MediaLayout.vue'
import MediaPromptBox from './MediaPromptBox.vue'
import PaidUsageZeroState, { type Suggestion } from './PaidUsageZeroState.vue'
import { imagenCode, imagenCodeOptions, useImagen, type ImagenLanguage } from './imagen'
import { useMediaQuery } from '../media-query'
import { failureText } from '../request'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useConsole()
const imagen = useImagen()
const { state, currentModel, aspectRatios } = imagen
const codeOpen = ref(false)
// handset 与官网一致，768 px 及以下 Tools 只显示图标
const handset = useMediaQuery('(max-width: 768px)')

// short 与官网示例卡片一致截取提示词开头，引号按界面语言
function short(prompt: string): string {
  return t('pg.quotedText').replace(
    '{text}',
    prompt.length > 100 ? `${prompt.slice(0, 100)}...` : prompt,
  )
}

// suggestions 是当前界面语言的官网 Imagen 页空态四个示例
const suggestions = computed<Suggestion[]>(() =>
  [
    {
      prompt: t('zero.imagen.example1Prompt'),
      image: 'https://www.gstatic.com/aistudio/zero-state/bunnies_opt_162x162.png',
    },
    {
      prompt: t('zero.imagen.example2Prompt'),
      image: 'https://www.gstatic.com/aistudio/zero-state/sushi_opt_162x162.png',
    },
    {
      prompt: t('zero.imagen.example3Prompt'),
      image: 'https://www.gstatic.com/aistudio/zero-state/house_in_forest_opt_288x162.png',
    },
    {
      prompt: t('zero.imagen.example4Prompt'),
      image: 'https://www.gstatic.com/aistudio/zero-state/futuristic_city_opt_162x162.png',
    },
  ].map((item) => ({ ...item, label: short(item.prompt) })),
)

const canRun = computed(() => currentModel.value !== undefined && state.draft.trim() !== '')

// now 在生成期间每 100 ms 刷新，用于显示已用时间
const now = ref(performance.now())
let ticker = 0
watch(
  () => state.running,
  (running) => {
    window.clearInterval(ticker)
    if (running) ticker = window.setInterval(() => (now.value = performance.now()), 100)
  },
  { immediate: true },
)
onUnmounted(() => window.clearInterval(ticker))

// elapsed 返回模型轮次的耗时文字
function elapsed(index: number, duration: number | undefined): string {
  const running = state.running && index === state.turns.length - 1
  return `${((running ? now.value - state.started : (duration ?? 0)) / 1000).toFixed(1)}s`
}

// save 下载一张生成的图片
function save(source: string, index: number): void {
  const link = document.createElement('a')
  link.href = source
  link.download = `image-${index + 1}.${source.slice(11, source.indexOf(';')).replace('jpeg', 'jpg')}`
  link.click()
}

// code 按语言生成当前提示词与设置的示例代码
function code(language: ImagenLanguage): string {
  return imagenCode(language, window.location.origin)
}

// generate 运行一次生成，失败时与官网一样以提示条显示上游说明
function generate(): void {
  imagen.run().catch((error: unknown) => {
    store.showNotice(failureText(error, t('pg.imageUnreachable'), t('pg.imageFailed')), 'error')
  })
}

// runSuggestion 与官网一样点选示例后填入提示词并立即运行
function runSuggestion(item: Suggestion): void {
  state.draft = item.prompt
  generate()
}

// newChat 清空当前会话
function newChat(): void {
  imagen.newSession()
  void router.replace({ query: { ...route.query } })
}

watch(
  () => store.models.value,
  () => imagen.ensureModel(typeof route.query.model === 'string' ? route.query.model : undefined),
  { immediate: true },
)
</script>

<template>
  <MediaLayout :title="state.title">
    <template #actions>
      <button
        v-tooltip.below="state.turns.length === 0 ? t('pg.alreadyNewChat') : t('pg.newChat')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newChat')"
        :aria-disabled="state.turns.length === 0 || state.running"
        @click="state.turns.length > 0 && !state.running && newChat()"
      >
        <UiIcon name="add" :size="20" />
      </button>
    </template>

    <PaidUsageZeroState
      v-if="state.turns.length === 0"
      :title="t('pg.imagenEmptyTitle')"
      layout="mosaic"
      icon="modelImage"
      :suggestions="suggestions"
      @select="runSuggestion"
    />
    <div v-else class="image-panel">
      <div class="scrolling-content">
        <template v-for="(turn, index) in state.turns" :key="turn.id">
          <template v-if="turn.role === 'user'">
            <div class="turn-role">{{ t('pg.user') }}</div>
            <div class="turn-prompt">{{ turn.prompt }}</div>
          </template>
          <template v-else>
            <div class="turn-role">{{ turn.model }} • {{ elapsed(index, turn.duration) }}</div>
            <div class="image-gallery">
              <template v-if="turn.images">
                <div
                  v-for="(image, imageIndex) in turn.images"
                  :key="imageIndex"
                  class="image-container"
                >
                  <img :src="image" alt="" />
                  <div class="actions-container">
                    <button
                      v-tooltip="t('pg.download')"
                      type="button"
                      ms-button
                      class="ms-button-borderless ms-button-icon ms-button-small"
                      :aria-label="t('pg.download')"
                      @click="save(image, imageIndex)"
                    >
                      <UiIcon name="download" :size="20" />
                    </button>
                  </div>
                </div>
              </template>
              <div
                v-for="placeholder in turn.count"
                v-else
                :key="placeholder"
                class="image-container loading"
                :style="{ aspectRatio: state.aspectRatio.replace(':', ' / ') }"
              ></div>
            </div>
            <div v-if="index < state.turns.length - 1" class="turn-separator"></div>
          </template>
        </template>
      </div>
    </div>

    <div class="prompt-input-container">
      <MediaPromptBox
        v-model="state.draft"
        :placeholder="t('pg.describeImage')"
        :label="t('pg.imagePromptLabel')"
        :running="state.running"
        :can-run="canRun"
        @run="generate()"
        @stop="imagen.stop()"
      >
        <template #left>
          <button
            type="button"
            ms-button
            class="ms-button-primary"
            :class="{ 'ms-button-icon': handset }"
            :aria-label="t('pg.openTools')"
            aria-disabled="true"
          >
            <UiIcon name="tools" :size="18" />
            <template v-if="!handset">{{ t('pg.tools') }}</template>
          </button>
        </template>
      </MediaPromptBox>
    </div>

    <template #settings="{ open, narrow, close }">
      <RunSettingsFrame
        :open="open"
        :narrow="narrow"
        @close="close"
        @code="codeOpen = true"
        @reset="imagen.reset()"
      >
        <div class="field-group">
          <ModelCard
            :model="currentModel"
            kind="image"
            :has-content="state.turns.length > 0"
            @select="imagen.ensureModel($event.id)"
          />
        </div>
        <div class="field-group">
          <div class="settings-item-column">
            <h3 class="item-description-title">{{ t('pg.numberOfResults') }}</h3>
            <div class="ms-slider">
              <UiSlider
                v-model="state.count"
                :min="1"
                :max="4"
                :step="1"
                :label="t('pg.numberOfResults')"
              />
              <input
                v-model.number="state.count"
                ms-input
                class="small slider-number-input"
                type="number"
                min="1"
                max="4"
                :aria-label="t('pg.numberOfResults')"
              />
            </div>
          </div>
          <div class="settings-item-column">
            <h3 class="item-description-title">{{ t('pg.aspectRatio') }}</h3>
            <UiSelect v-model="state.aspectRatio" class="w-full" :aria-label="t('pg.aspectRatio')">
              <option v-for="value in aspectRatios" :key="value" :value="value">{{ value }}</option>
            </UiSelect>
          </div>
        </div>
      </RunSettingsFrame>
    </template>
  </MediaLayout>
  <CodeDialog v-model:open="codeOpen" :options="imagenCodeOptions" :code="code" />
</template>

<style scoped>
.image-panel {
  display: flex;
  flex: 1;
  justify-content: center;
  min-height: 0;
  overflow-y: auto;
}

.scrolling-content {
  width: 100%;
  max-width: 1000px;
  margin: 0 auto;
  padding: 20px;
}

.turn-role {
  margin-top: 20px;
  margin-bottom: 12px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.turn-prompt {
  margin: 8px 0;
  color: var(--color-on-surface);
  font-size: 14px;
  line-height: 21px;
  white-space: pre-wrap;
}

.turn-separator {
  margin-top: 12px;
  border-top: 1px solid var(--turn-separator-color, var(--color-v3-outline));
}

.image-gallery {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 8px;
  width: 100%;
}

.image-container {
  position: relative;
  margin-top: 8px;
  overflow: clip;
  border-radius: 12px;
}

.image-container img {
  display: block;
  width: 100%;
  transition: transform 0.2s ease-in-out;
}

.image-container:is(:hover, :focus-within) img {
  transform: scale(1.05);
}

.actions-container {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: end;
  justify-content: end;
  gap: 4px;
  padding: 8px;
  background: rgb(0 0 0 / 33.3%);
  opacity: 0;
  transition: opacity 0.2s ease-in-out;
}

.image-container:is(:hover, :focus-within) .actions-container {
  opacity: 1;
}

.actions-container button {
  background: transparent;
  color: #fff;
}

.image-container.loading {
  animation: moving-gradient 1.7s linear infinite forwards;
  background: linear-gradient(
    290deg,
    var(--color-loading-background) 40%,
    var(--color-loading-background-contrast) 50%,
    var(--color-loading-background) 60%
  );
  background-size: 800px;
}

@keyframes moving-gradient {
  0% {
    background-position: -400px 0;
  }

  to {
    background-position: 400px 0;
  }
}

.prompt-input-container {
  position: relative;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  align-items: center;
  padding-inline: 20px;
  padding-bottom: 6px;
}

@media screen and (max-width: 600px) {
  .prompt-input-container {
    padding-bottom: 8px;
  }
}
</style>
