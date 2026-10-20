<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import CodeDialog from '../CodeDialog.vue'
import { submits } from '../run-keys'
import AudioFooter from './AudioFooter.vue'
import CopyPromptButton from './CopyPromptButton.vue'
import MediaLayout from './MediaLayout.vue'
import SpeechBlockCard from './SpeechBlockCard.vue'
import SpeechSettings from './SpeechSettings.vue'
import VoicePanel from './VoicePanel.vue'
import { mediaCodeOptions, type MediaCodeLanguage } from './code'
import { useSamplePlayer } from './sample'
import { useSpeech } from './speech'
import { speechCode } from './speech-code'
import { speechTemplates, type SpeechTemplate } from './speech-templates'

const route = useRoute()
const { t } = useI18n()
const store = useConsole()
const speech = useSpeech()
const { state, currentModel, daikon, multi, openAIMulti, canRun } = speech
const { playingSample, loadingSample, toggleSample, stopSample } = useSamplePlayer()
const codeOpen = ref(false)
const voiceOpen = ref(false)
const voiceSpeaker = ref(0)
const blockCards = ref<InstanceType<typeof SpeechBlockCard>[]>([])
const carousel = ref<HTMLElement>()
const fadeLeft = ref(false)
const fadeRight = ref(false)
const zero = computed(() => state.blocks.length === 0)

// updateFades 按轮播滚动位置显示两侧渐隐与翻页按钮状态
function updateFades(): void {
  const element = carousel.value
  if (!element) return
  fadeLeft.value = element.scrollLeft > 1
  fadeRight.value = element.scrollLeft + element.clientWidth < element.scrollWidth - 1
}

// scrollCarousel 按一屏宽度翻页
function scrollCarousel(direction: -1 | 1): void {
  const element = carousel.value
  if (!element) return
  element.scrollBy({ left: direction * element.clientWidth * 0.8, behavior: 'smooth' })
}

// openVoice 打开说话人的音色库
function openVoice(speaker: number): void {
  voiceSpeaker.value = speaker
  voiceOpen.value = true
}

// newDialog 打开空白对话并聚焦第一段台词
function newDialog(): void {
  stopSample()
  speech.newDialog()
  void nextTick(() => blockCards.value[0]?.focus())
}

// useTemplate 载入官网示例对话
function useTemplate(template: SpeechTemplate): void {
  stopSample()
  speech.useTemplate(template)
}

// addBlock 加入一段台词并聚焦
function addBlock(): void {
  speech.addBlock()
  void nextTick(() => blockCards.value.at(-1)?.focus())
}

// onBlockKeydown 按提交键运行
function onBlockKeydown(event: KeyboardEvent): void {
  if (submits(event)) {
    event.preventDefault()
    void speech.run()
  }
}

// code 按语言生成当前台词与设置的语音示例代码
function code(language: MediaCodeLanguage): string {
  const model = currentModel.value
  const openAI = speech.openAISpeechBody(state.settings.model)
  return speechCode(language, {
    baseURL: window.location.origin,
    model: state.settings.model,
    prompt: speech.codePrompt(),
    temperature: state.settings.temperature ?? model?.temperature ?? 1,
    speakers: multi.value ? state.speakers : [state.speakers[0]!],
    openAI: {
      input: String(openAI.input ?? ''),
      voice: String(openAI.voice ?? ''),
      instructions: typeof openAI.instructions === 'string' ? openAI.instructions : undefined,
    },
  })
}

watch(
  () => store.models.value,
  () => speech.ensureModel(typeof route.query.model === 'string' ? route.query.model : undefined),
  { immediate: true },
)
watch(zero, (value) => {
  if (value) void nextTick(updateFades)
})
onMounted(() => {
  updateFades()
  window.addEventListener('resize', updateFades)
})
onUnmounted(() => {
  stopSample()
  speech.stopPreview()
  window.removeEventListener('resize', updateFades)
})
</script>

<template>
  <MediaLayout>
    <template #actions>
      <CopyPromptButton
        :text="speech.codePrompt"
        :disabled="zero || speech.state.blocks.length === 0"
      />
      <button
        v-tooltip.below="zero ? t('pg.alreadyNewChat') : t('pg.newChat')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newChat')"
        :aria-disabled="zero"
        @click="!zero && speech.close()"
      >
        <UiIcon name="add" :size="20" />
      </button>
    </template>

    <div class="speech-prompt-main" :class="{ 'zero-state': zero }">
      <div v-if="zero" class="speech-zero-state-container" data-testid="tts-zero-state">
        <div class="hero-header">
          <h1 class="hero-title">{{ t('pg.speechHero') }}</h1>
        </div>
        <section class="zero-state-section">
          <div class="section-header">
            <div class="section-title-group">
              <UiIcon name="speakerNotes" :size="16" class="section-icon" />
              <h2 class="section-title">{{ t('pg.exploreSpeech') }}</h2>
            </div>
            <div class="carousel-controls">
              <button
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon carousel-nav-button"
                :aria-label="t('pg.previous')"
                :aria-disabled="!fadeLeft"
                @click="scrollCarousel(-1)"
              >
                <UiIcon name="chevronLeft" :size="18" />
              </button>
              <button
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon carousel-nav-button"
                :aria-label="t('pg.next')"
                :aria-disabled="!fadeRight"
                @click="scrollCarousel(1)"
              >
                <UiIcon name="chevronRight" :size="18" />
              </button>
            </div>
          </div>
          <div
            ref="carousel"
            class="cards-carousel"
            :class="{ 'fade-left': fadeLeft, 'fade-right': fadeRight }"
            @scroll="updateFades"
          >
            <button type="button" class="action-card" @click="newDialog">
              <span class="action-card-icon"><UiIcon name="add" :size="20" /></span>
              <span class="action-card-label">{{ t('pg.createDialog') }}</span>
            </button>
            <div
              v-for="template in speechTemplates"
              :key="template.id"
              role="button"
              tabindex="0"
              class="dialog-example-card"
              @click="useTemplate(template)"
              @keydown.enter.prevent="useTemplate(template)"
            >
              <div class="card-top-row">
                <span class="dialog-icon-wrapper"><UiIcon :name="template.icon" :size="20" /></span>
                <button
                  v-tooltip.below="playingSample === template.audio ? t('pg.pause') : t('pg.play')"
                  type="button"
                  class="play-audio-button"
                  :aria-label="playingSample === template.audio ? t('pg.pause') : t('pg.play')"
                  @click.stop="toggleSample(template.audio)"
                >
                  <UiIcon
                    :name="
                      loadingSample === template.audio
                        ? 'spinner'
                        : playingSample === template.audio
                          ? 'stop'
                          : 'play'
                    "
                    :size="16"
                  />
                </button>
              </div>
              <div class="card-text-content">
                <h3 class="card-title">{{ template.name }}</h3>
                <p class="card-description">{{ template.description }}</p>
              </div>
            </div>
          </div>
        </section>
      </div>

      <div v-else class="composer">
        <div v-if="!daikon" class="scene-section">
          <label class="scene-field">
            <span v-tooltip.below="t('pg.sceneTip')" class="section-title">{{
              t('pg.scene')
            }}</span>
            <textarea
              v-model="state.scene"
              rows="2"
              class="scene-textarea"
              :placeholder="t('pg.scenePlaceholder')"
              :aria-label="t('pg.scene')"
            ></textarea>
          </label>
          <label class="scene-field">
            <span v-tooltip.below="t('pg.sampleContextTip')" class="section-title">
              {{ t('pg.sampleContext') }}
            </span>
            <textarea
              v-model="state.context"
              rows="2"
              class="scene-textarea"
              :placeholder="t('pg.sampleContextPlaceholder')"
              :aria-label="t('pg.sampleContext')"
            ></textarea>
          </label>
        </div>
        <div class="speech-blocks-container">
          <SpeechBlockCard
            v-for="(block, index) in state.blocks"
            :key="block.id"
            ref="blockCards"
            :index="index"
            @voice="openVoice(block.speaker)"
            @keydown="onBlockKeydown"
          />
          <button
            type="button"
            ms-button
            class="ms-button-borderless add-block-button"
            @click="addBlock"
          >
            <UiIcon name="add" :size="18" />
            {{ t('pg.addSpeechBlock') }}
          </button>
        </div>
      </div>
    </div>

    <p v-if="state.error" role="alert" class="speech-error">{{ state.error }}</p>
    <AudioFooter
      v-if="!zero"
      :audio="state.audio"
      :running="state.running"
      :can-run="canRun"
      :run-tooltip="openAIMulti ? t('pg.openAISpeechSingle') : ''"
      file-name="speech.wav"
      @run="speech.run()"
      @stop="speech.stop()"
    />

    <template #settings="{ open, narrow, close }">
      <SpeechSettings
        :open="open"
        :narrow="narrow"
        @close="close"
        @code="codeOpen = true"
        @voice="openVoice"
      />
    </template>
  </MediaLayout>
  <VoicePanel v-model:open="voiceOpen" :speaker="voiceSpeaker" />
  <CodeDialog v-model:open="codeOpen" :options="mediaCodeOptions" :code="code" />
</template>

<style scoped>
.speech-prompt-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  padding: 0 40px;
  overflow-y: auto;
}

@media screen and (max-width: 600px) {
  .speech-prompt-main {
    padding: 0 16px;
  }
}

.speech-zero-state-container {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  justify-content: center;
  width: 100%;
  max-width: 100%;
  min-height: 100%;
  margin: auto;
  padding: 32px 0;
}

.hero-header {
  margin-bottom: 28px;
  text-align: left;
}

.hero-title {
  margin: 0;
  color: var(--color-v3-text);
  font-family: var(--font-heading);
  font-size: clamp(26px, 3.2vw, 36px);
  font-weight: 300;
  line-height: 1.2;
}

.zero-state-section {
  display: flex;
  flex-direction: column;
  width: 100%;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 28px;
  margin-bottom: 12px;
}

.section-title-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.section-icon {
  color: var(--color-v3-text-var);
}

.section-title {
  margin: 0;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.carousel-controls {
  display: flex;
  align-items: center;
  gap: 4px;
}

.carousel-nav-button {
  width: 28px;
  height: 28px;
  color: var(--color-v3-text-var);
}

.carousel-nav-button[aria-disabled='true'] {
  background: transparent;
  color: var(--color-v3-text-disable);
  cursor: default;
}

.cards-carousel {
  display: flex;
  align-items: stretch;
  gap: 12px;
  width: 100%;
  margin: -4px 0 0;
  padding: 4px 2px 8px;
  overflow-x: auto;
  scroll-behavior: smooth;
  scroll-padding-inline: 2px;
  scroll-snap-type: x mandatory;
  scrollbar-width: none;
}

.cards-carousel::-webkit-scrollbar {
  display: none;
}

.cards-carousel.fade-left {
  mask-image: linear-gradient(90deg, transparent, #000 3%);
}

.cards-carousel.fade-right {
  mask-image: linear-gradient(90deg, #000 97%, transparent);
}

.cards-carousel.fade-left.fade-right {
  mask-image: linear-gradient(90deg, transparent, #000 3%, #000 97%, transparent);
}

.cards-carousel > * {
  flex: 0 0 180px;
  height: 148px;
  scroll-snap-align: start;
}

.action-card,
.dialog-example-card {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 148px;
  padding: 16px;
  border-radius: 12px;
  outline: none;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.23, 1, 0.32, 1);
}

.action-card {
  align-items: center;
  justify-content: center;
  border: 1.5px dashed var(--color-v3-outline);
  text-align: center;
}

.action-card:hover {
  border-color: var(--color-v3-outline-active);
  background: var(--color-v3-surface-container-high);
  transform: translateY(-2px);
}

.action-card:focus-visible,
.dialog-example-card:focus-visible {
  border-color: var(--color-v3-outline-active);
}

.action-card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  margin-bottom: 10px;
  border-radius: 50%;
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text-var);
}

.action-card-label {
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.dialog-example-card {
  justify-content: space-between;
  border: 1px solid var(--color-v3-outline-var);
  text-align: left;
}

.dialog-example-card:hover {
  border-color: var(--color-v3-outline);
  background: var(--color-v3-surface-container-high);
  transform: translateY(-2px);
}

.card-top-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  margin-bottom: 8px;
}

.dialog-icon-wrapper {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 10px;
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text);
}

.play-audio-button {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 50%;
  background: transparent;
  color: var(--color-v3-text-var);
  cursor: pointer;
}

.play-audio-button:hover {
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-outline-accent);
}

.card-text-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: flex-end;
}

.card-title {
  margin: 0 0 4px;
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.card-description {
  display: -webkit-box;
  margin: 0;
  overflow: hidden;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
  text-overflow: ellipsis;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.composer {
  width: 100%;
  max-width: 1000px;
  margin: 0 auto;
  padding: 28px 0 24px;
  animation: composer-fade-in 0.3s ease-out;
}

@keyframes composer-fade-in {
  from {
    opacity: 0;
  }

  to {
    opacity: 1;
  }
}

.scene-section {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 16px;
}

@media screen and (max-width: 600px) {
  .scene-section {
    grid-template-columns: 1fr;
  }
}

.scene-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.scene-textarea {
  box-sizing: border-box;
  width: 100%;
  max-height: 10lh;
  padding: 8px 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  outline: none;
  background: transparent;
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
  line-height: 21px;
  resize: vertical;
}

.scene-textarea:focus {
  border-color: var(--color-v3-outline-accent);
}

.speech-blocks-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.add-block-button {
  align-self: center;
  gap: 4px;
  margin-top: 4px;
  font-weight: 500;
}

.speech-error {
  max-width: 1000px;
  margin: 8px auto;
  padding: 8px 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  color: var(--color-error);
  font-size: 14px;
  line-height: 21px;
}
</style>
