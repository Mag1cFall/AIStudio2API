<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { useI18n } from '@/i18n'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import ModelCard from '../ModelCard.vue'
import RunSettingsFrame from '../RunSettingsFrame.vue'
import { useVideo, videoProtocols } from './video'

defineProps<{ open: boolean; narrow: boolean }>()
const emit = defineEmits<{ close: []; code: [] }>()
const { t } = useI18n()
const video = useVideo()
const { state, currentModel, openAIFrames, aspectRatios, durations, resolutions } = video
</script>

<template>
  <RunSettingsFrame
    :open="open"
    :narrow="narrow"
    @close="emit('close')"
    @code="emit('code')"
    @reset="video.reset()"
  >
    <div class="field-group">
      <ModelCard
        :model="currentModel"
        kind="video"
        :has-content="state.turns.length > 0"
        @select="video.setModel($event.id)"
      />
      <div
        v-tooltip.left="openAIFrames ? t('pg.openAIVideoStartOnly') : ''"
        class="settings-item-column protocol-setting"
      >
        <h3 class="item-description-title">{{ t('playground.protocol') }}</h3>
        <UiSelect
          v-model="state.settings.protocol"
          class="w-full"
          :aria-label="t('playground.protocol')"
        >
          <option v-for="item in videoProtocols" :key="item.value" :value="item.value">
            {{ item.label }}
          </option>
        </UiSelect>
      </div>
    </div>
    <div class="field-group">
      <div v-tooltip.left="t('pg.videoCountTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.numberOfResults') }}</h3>
        <div v-tooltip.left="t('pg.videoCountFixed')" class="ms-slider">
          <UiSlider
            :model-value="1"
            :min="0"
            :max="1"
            :step="1"
            disabled
            :label="t('pg.numberOfResults')"
          />
          <input ms-input class="small slider-number-input" type="text" value="1" disabled />
        </div>
      </div>
      <div v-tooltip.left="t('pg.videoAspectTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.aspectRatio') }}</h3>
        <UiSelect
          v-model="state.settings.aspectRatio"
          class="w-full"
          :aria-label="t('pg.aspectRatio')"
          :disabled="aspectRatios.length <= 1"
        >
          <option v-for="value in aspectRatios" :key="value" :value="value">{{ value }}</option>
        </UiSelect>
      </div>
      <div v-tooltip.left="t('pg.videoDurationTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.videoDuration') }}</h3>
        <UiSelect
          v-model="state.settings.durationSeconds"
          class="w-full"
          :aria-label="t('pg.videoDuration')"
          :disabled="durations.length <= 1"
        >
          <option
            v-for="option in durations"
            :key="option.value"
            :value="option.value"
            :disabled="option.disabled"
            :title="
              option.disabled
                ? state.last
                  ? t('pg.videoInterpolationOnly8s')
                  : t('pg.videoOnly720p')
                : ''
            "
          >
            {{ option.value }}s
          </option>
        </UiSelect>
      </div>
      <div v-tooltip.left="t('pg.comingSoon')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.frameRate') }}</h3>
        <UiSelect :model-value="24" class="w-full" :aria-label="t('pg.frameRate')" disabled>
          <option :value="24">24 fps</option>
        </UiSelect>
      </div>
      <div v-tooltip.left="t('pg.videoResolutionTip')" class="settings-item-column">
        <h3 class="item-description-title">{{ t('pg.outputResolution') }}</h3>
        <UiSelect
          v-model="state.settings.resolution"
          class="w-full"
          :aria-label="t('pg.outputResolution')"
          :disabled="resolutions.length <= 1"
        >
          <option
            v-for="option in resolutions"
            :key="option.value"
            :value="option.value"
            :disabled="option.disabled"
          >
            {{ option.value }}
          </option>
        </UiSelect>
      </div>
      <p class="settings-note">{{ t('pg.videoNoHistory') }}</p>
    </div>
  </RunSettingsFrame>
</template>
