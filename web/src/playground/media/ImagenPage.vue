<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
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
import { imagenCode, imagenCodeOptions, useImagen, type ImagenLanguage } from './imagen'
import { useMediaQuery } from '../media-query'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useConsole()
const imagen = useImagen()
const { state, currentModel, aspectRatios } = imagen
const codeOpen = ref(false)
// handset 与官网一致，768 px 及以下 Tools 只显示图标
const handset = useMediaQuery('(max-width: 768px)')

// suggestions 是官网 Imagen 页空态的四个示例
const suggestions = [
  {
    prompt: 'Create an image of fuzzy bunnies next to a glass of wine in my kitchen.',
    image: 'https://www.gstatic.com/aistudio/zero-state/bunnies_opt_162x162.png',
  },
  {
    prompt: 'Create an image of sushi on a table, oil painting style.',
    image: 'https://www.gstatic.com/aistudio/zero-state/sushi_opt_162x162.png',
  },
  {
    prompt:
      'Create an image of a fancy house by a pond in the forest in front of grassland with wildflowers, close up view.',
    image: 'https://www.gstatic.com/aistudio/zero-state/house_in_forest_opt_288x162.png',
  },
  {
    prompt:
      'Create an image of a futuristic cityscape with tall holographic billboards, sleek flying cars, and neon lights.',
    image: 'https://www.gstatic.com/aistudio/zero-state/futuristic_city_opt_162x162.png',
  },
]

// gallery 为空态示例卡片的容器，窄屏时与官网一样排成一行并带左右滚动按钮
const gallery = ref<HTMLElement>()
const scroll = reactive({ left: false, right: false })
let galleryObserver: ResizeObserver | undefined

// updateScroll 按滚动位置决定是否显示左右滚动按钮
function updateScroll(): void {
  const node = gallery.value
  if (node === undefined) return
  scroll.left = node.scrollLeft > 0
  scroll.right = node.scrollLeft + node.clientWidth < node.scrollWidth - 1
}

// scrollGallery 向左或向右滚动一张卡片
function scrollGallery(direction: 1 | -1): void {
  gallery.value?.scrollBy({ left: direction * 178, behavior: 'smooth' })
}

watch(gallery, (node) => {
  galleryObserver?.disconnect()
  if (node === undefined) return
  galleryObserver = new ResizeObserver(updateScroll)
  galleryObserver.observe(node)
})
onUnmounted(() => galleryObserver?.disconnect())

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

// short 与官网示例卡片一致截取提示词开头
function short(prompt: string): string {
  return `“${prompt.length > 100 ? `${prompt.slice(0, 100)}...` : prompt}”`
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
    const message = error instanceof Error ? error.message : String(error)
    store.showNotice(t('pg.imageFailed').replace('{error}', message), 'error')
  })
}

// runSuggestion 与官网一样点选示例后填入提示词并立即运行
function runSuggestion(prompt: string): void {
  state.draft = prompt
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

    <div v-if="state.turns.length === 0" class="zero-state-content">
      <div class="paid-usage-zero-state">
        <div class="left-container">
          <h1 class="hero-text">{{ t('pg.imagenEmptyTitle') }}</h1>
        </div>
        <div class="suggestion-gallery-host">
          <div ref="gallery" class="suggestion-gallery" @scroll="updateScroll">
            <button
              v-for="item in suggestions"
              :key="item.image"
              v-tooltip="short(item.prompt)"
              type="button"
              class="suggestion-card"
              :aria-label="short(item.prompt)"
              @click="runSuggestion(item.prompt)"
            >
              <img class="display-media" :src="item.image" alt="" @load="updateScroll" />
              <span class="media-type-icon"><UiIcon name="modelImage" :size="18" /></span>
            </button>
          </div>
          <div class="gallery-overlay">
            <div v-if="scroll.left" class="left-controls-container">
              <button
                v-tooltip="t('pg.scrollLeft')"
                type="button"
                ms-button
                class="ms-button-icon ms-button-primary"
                :aria-label="t('pg.scrollLeft')"
                @click="scrollGallery(-1)"
              >
                <UiIcon name="chevronLeft" :size="20" />
              </button>
            </div>
            <div v-if="scroll.right" class="right-controls-container">
              <button
                v-tooltip="t('pg.scrollRight')"
                type="button"
                ms-button
                class="ms-button-icon ms-button-primary"
                :aria-label="t('pg.scrollRight')"
                @click="scrollGallery(1)"
              >
                <UiIcon name="chevronRight" :size="20" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
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
.zero-state-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 0;
  overflow: auto;
  padding-inline: 20px;
}

.paid-usage-zero-state {
  display: grid;
  grid-template-columns: 1fr 1fr;
  column-gap: 24px;
  align-items: center;
  width: 100%;
  max-width: 1000px;
  margin: 0 auto;
  padding: 24px 20px 0;
}

.left-container {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  max-width: 500px;
}

/* hero-text 的颜色为官网 --color-logo-lockup */
.hero-text {
  margin: 0 0 52px;
  padding-top: 12px;
  color: #191919;
  font-family: var(--font-heading);
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
}

:root[data-theme='dark'] .hero-text {
  color: #fcfcfc;
}

.suggestion-gallery-host {
  position: relative;
  width: 100%;
  margin: 32px 0 16px;
}

.gallery-overlay {
  display: none;
}

.suggestion-gallery {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 8px;
  width: 100%;
}

.suggestion-card {
  position: relative;
  grid-column: span 3;
  align-self: start;
  margin: 8px;
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
  transition: transform 0.1s ease-in;
}

.suggestion-card:hover {
  transform: translateY(-4px);
}

.suggestion-card:nth-child(1),
.suggestion-card:nth-child(3) {
  justify-self: end;
}

.suggestion-card:nth-child(1),
.suggestion-card:nth-child(2) {
  align-self: end;
}

.suggestion-card:nth-child(2),
.suggestion-card:nth-child(4) {
  justify-self: start;
}

.display-media {
  display: block;
  max-width: 100%;
  max-height: 162px;
  border-radius: 8px;
  object-fit: cover;
}

.media-type-icon {
  position: absolute;
  top: 8px;
  left: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 24px;
  background: var(--color-surface-brighter);
  color: var(--color-on-surface);
}

@media screen and (max-width: 600px) {
  .zero-state-content {
    justify-content: flex-start;
    padding: 16px 16px 0;
  }

  .paid-usage-zero-state {
    grid-template-columns: 1fr;
    row-gap: 24px;
    padding-inline: 16px;
  }

  .gallery-overlay {
    position: absolute;
    top: 0;
    display: flex;
    width: 100%;
    height: 100%;
    pointer-events: none;
  }

  .gallery-overlay button {
    pointer-events: auto;
  }

  .left-controls-container,
  .right-controls-container {
    position: absolute;
    top: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 56px;
    height: 162px;
  }

  .left-controls-container {
    left: 0;
  }

  .right-controls-container {
    right: 0;
  }

  .suggestion-gallery {
    display: flex;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .suggestion-card {
    flex: 0 0 auto;
  }
}

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
