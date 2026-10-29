<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import ThinkingOrb from '@/playground/ui/ThinkingOrb.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import CodeDialog from '../CodeDialog.vue'
import { submits } from '../run-keys'
import AudioFooter from './AudioFooter.vue'
import CopyPromptButton from './CopyPromptButton.vue'
import MediaLayout from './MediaLayout.vue'
import SpeechBlockCard from './SpeechBlockCard.vue'
import SpeechCarouselSection from './SpeechCarouselSection.vue'
import SpeechSettings from './SpeechSettings.vue'
import VoicePanel from './VoicePanel.vue'
import { mediaCodeOptions, type MediaCodeLanguage } from './code'
import { useSamplePlayer } from './sample'
import { useSpeech } from './speech'
import { speechCode } from './speech-code'
import { speechTemplates, type SpeechTemplate } from './speech-templates'
import { speechVoices } from './speech-voices'

const route = useRoute()
const { t, logText } = useI18n()
const store = useConsole()
const speech = useSpeech()
const { state, daikon, multi, openAIMulti, canRun } = speech
const { playingSample, loadingSample, toggleSample, stopSample } = useSamplePlayer()
const codeOpen = ref(false)
const voiceOpen = ref(false)
const voiceSpeaker = ref(0)
const blockCards = ref<InstanceType<typeof SpeechBlockCard>[]>([])
const zero = computed(() => state.blocks.length === 0)
// templates 与 voices 是当前界面语言的示例对话与示例声音
const templates = computed(speechTemplates)
const voices = computed(speechVoices)

// sampleIcon 返回试听按钮在加载、播放与停止时的图标
function sampleIcon(url: string): 'spinner' | 'stop' | 'play' {
  if (loadingSample.value === url) return 'spinner'
  return playingSample.value === url ? 'stop' : 'play'
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
  void nextTick(() => blockCards.value[0]!.focus())
}

// useTemplate 载入官网示例对话
function useTemplate(template: SpeechTemplate): void {
  stopSample()
  speech.useTemplate(template)
}

// addBlock 加入一段台词并聚焦
function addBlock(): void {
  speech.addBlock()
  void nextTick(() => blockCards.value.at(-1)!.focus())
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
  return speechCode(language, {
    baseURL: window.location.origin,
    model: state.settings.model,
    prompt: speech.codePrompt(),
    temperature: speech.temperature.value,
    speakers: multi.value ? state.speakers : [state.speakers[0]!],
    openAI: speech.openAISpeechBody(state.settings.model),
  })
}

watch(
  () => store.models.value,
  () => speech.ensureModel(typeof route.query.model === 'string' ? route.query.model : undefined),
  { immediate: true },
)
onUnmounted(() => {
  stopSample()
  speech.stopPreview()
})
</script>

<template>
  <MediaLayout>
    <template #actions>
      <CopyPromptButton :text="speech.codePrompt" :disabled="zero" />
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
        <SpeechCarouselSection
          :title="t('pg.exploreSpeech')"
          icon="comment"
          :region-label="t('pg.speechRegion')"
          :previous-label="t('pg.previous')"
          :next-label="t('pg.next')"
          :action-label="t('pg.createDialog')"
          @action="newDialog"
        >
          <div
            v-for="template in templates"
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
                <UiIcon :name="sampleIcon(template.audio)" :size="16" />
              </button>
            </div>
            <div class="card-text-content">
              <h3 class="card-title">{{ template.name }}</h3>
              <p class="card-description">{{ template.description }}</p>
            </div>
          </div>
        </SpeechCarouselSection>
        <SpeechCarouselSection
          :title="t('pg.exploreVoices')"
          icon="voiceSelection"
          :region-label="t('pg.exploreVoices')"
          :previous-label="t('pg.previousVoices')"
          :next-label="t('pg.nextVoices')"
          :action-label="t('pg.createVoice')"
          :action-unavailable="t('pg.voiceDesignPaid')"
        >
          <div
            v-for="voice in voices"
            :key="voice.id"
            v-tooltip.below="t('pg.voiceDesignPaid')"
            class="voice-card"
            aria-disabled="true"
            :aria-label="`${voice.title} - ${voice.description}`"
          >
            <div class="top-row">
              <div class="voice-sphere-visual" aria-hidden="true">
                <ThinkingOrb :state="voice.orb" />
              </div>
              <button
                v-tooltip.below="playingSample === voice.sample ? t('pg.pause') : t('pg.play')"
                type="button"
                class="play-audio-button"
                :aria-label="playingSample === voice.sample ? t('pg.pause') : t('pg.play')"
                @click.stop="toggleSample(voice.sample)"
              >
                <UiIcon :name="sampleIcon(voice.sample)" :size="16" />
              </button>
            </div>
            <div class="text-content">
              <div class="voice-title">{{ voice.title }}</div>
              <div class="voice-description">{{ voice.description }}</div>
            </div>
          </div>
        </SpeechCarouselSection>
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

    <p v-if="state.error" role="alert" class="speech-error">
      {{ logText(state.error) }}
    </p>
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
  .speech-prompt-main:not(.zero-state) {
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

.dialog-example-card {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  width: 100%;
  height: 148px;
  padding: 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  outline: none;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.23, 1, 0.32, 1);
}

.dialog-example-card:focus-visible {
  border-color: var(--color-v3-outline-active);
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

.voice-card {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  width: 100%;
  height: 148px;
  padding: 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container-highest);
}

.top-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.voice-sphere-visual {
  display: flex;
  align-items: center;
  height: 36px;
  margin-bottom: 12px;
  overflow: visible;
  color: var(--color-v3-text);
}

.voice-sphere-visual > * {
  transform: scale(0.65);
  transform-origin: left center;
}

.top-row .play-audio-button {
  flex: none;
  margin-bottom: 12px;
}

.text-content {
  display: flex;
  flex-direction: column;
}

.voice-title {
  margin: 0 0 4px;
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
}

.voice-description,
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
