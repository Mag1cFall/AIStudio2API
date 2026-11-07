<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

// Starter 是标题下方的示例提示词
export interface Starter {
  title: string
  prompt: string
}

// Example 是右侧的示例卡片
export interface Example {
  title: string
  prompt: string
  preview: string
  video?: string
  detection?: { label: string; inset?: string }
}

defineProps<{
  kind: 'music' | 'chat' | 'images' | 'robotics' | 'live' | 'video'
  title: string
  description: string
  starters: readonly Starter[]
  examples: readonly Example[]
  clickableTitle?: boolean
  docs?: string | undefined
  caption?: string | undefined
}>()
const emit = defineEmits<{ starter: [item: Starter]; example: [item: Example]; title: [] }>()
const { t } = useI18n()
// played 是指针或焦点到过的示例卡下标，卡片第一次被指向时才载入它的示例视频
const played = ref(new Set<number>())
</script>

<template>
  <div class="ms-model-empty-state">
    <section class="empty-state" :data-kind="kind">
      <div class="empty-state__heading">
        <h3 v-if="clickableTitle" id="empty-state-heading">
          <button type="button" class="empty-state__title" @click="emit('title')">
            {{ title }}
          </button>
        </h3>
        <h3 v-else id="empty-state-heading">{{ title }}</h3>
        <p class="empty-state__description">{{ description }}</p>
        <ul v-if="starters.length" class="starters" :aria-label="t('pg.promptsToTry')">
          <li v-for="(item, index) in starters" :key="item.title" :style="{ '--i': index }">
            <button type="button" class="starter" @click="emit('starter', item)">
              <span>{{ item.title }}</span>
            </button>
          </li>
        </ul>
        <a
          v-if="docs"
          class="empty-state__docs-link"
          :href="docs"
          target="_blank"
          rel="noopener noreferrer"
          aria-describedby="empty-state-heading"
        >
          {{ t('pg.learnMore') }}
          <UiIcon name="external" :size="14" class="empty-state__external-icon" />
        </a>
      </div>
      <div class="empty-state__showcase">
        <p v-if="caption" class="empty-state__examples-caption">
          <svg class="empty-state__examples-caption-icon" viewBox="0 0 24 24" aria-hidden="true">
            <defs>
              <linearGradient id="empty-state-caption-gradient" x1="0" y1="1" x2="1" y2="0">
                <stop offset="33%" stop-color="#346bf1" />
                <stop offset="49%" stop-color="#3186ff" />
                <stop offset="66%" stop-color="#4fa0ff" />
              </linearGradient>
            </defs>
            <path
              fill="url(#empty-state-caption-gradient)"
              d="M12 2c0 5.52 4.48 10 10 10-5.52 0-10 4.48-10 10 0-5.52-4.48-10-10-10 5.52 0 10-4.48 10-10z"
            />
          </svg>
          {{ caption }}
        </p>
        <div class="empty-state__examples" :data-count="examples.length">
          <div
            v-for="(item, index) in examples"
            :key="item.title"
            class="tile"
            :style="{ '--i': index }"
            @pointerenter="played.add(index)"
            @focusin="played.add(index)"
          >
            <button type="button" class="tile__frame" @click="emit('example', item)">
              <span class="tile__media">
                <span class="tile__halo" aria-hidden="true"></span>
                <span class="tile__window">
                  <img :src="item.preview" alt="" />
                  <video
                    v-if="item.video"
                    class="tile__video"
                    :src="played.has(index) ? item.video : undefined"
                    autoplay
                    muted
                    loop
                    playsinline
                    preload="none"
                    aria-hidden="true"
                  ></video>
                  <span
                    v-if="item.detection"
                    class="detection"
                    :style="item.detection.inset ? { inset: item.detection.inset } : undefined"
                    aria-hidden="true"
                  >
                    <b>{{ item.detection.label }}</b>
                  </span>
                </span>
              </span>
              <span class="tile__label">
                <UiIcon v-if="item.video" name="playFill" :size="14" class="tile__play-icon" />
                {{ item.title }}
              </span>
            </button>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ms-model-empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  width: 100%;
  min-width: 0;
  container-type: inline-size;
}

.empty-state {
  --count: 3;
  --empty-duration: 10s;
  --fill: var(--color-v3-hover);
  --halo: light-dark(0.9, 1);
  --ink: var(--color-v3-text);
  --line: var(--color-v3-outline);
  --muted: var(--color-v3-text-var);
  --surface: var(--color-v3-surface);
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.15fr);
  align-items: center;
  gap: 56px;
  box-sizing: border-box;
  width: 100%;
  max-width: 976px;
  margin: auto;
  padding: 40px;
  color: var(--ink);
}

.empty-state__title {
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font: inherit;
  text-align: inherit;
  cursor: pointer;
}

.empty-state h3 {
  margin: 0;
  font-size: 28px;
  font-weight: 500;
  letter-spacing: -0.6px;
  line-height: 1.2;
  text-wrap: balance;
}

.empty-state button {
  color: inherit;
  cursor: pointer;
  font: inherit;
  letter-spacing: inherit;
}

.empty-state button:focus-visible {
  outline: 2px solid var(--color-v3-outline-accent);
  outline-offset: 2px;
}

.empty-state__description {
  max-width: 38ch;
  margin: 12px 0 0;
  color: var(--muted);
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
}

.empty-state h3,
.empty-state__description,
.starters li,
.tile {
  animation: empty-arrive 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
}

.empty-state__description {
  animation-delay: 60ms;
}

.starters li {
  animation-delay: calc(0.14s + var(--i) * 50ms);
}

.tile {
  animation-delay: calc(0.12s + var(--i) * 90ms);
}

@keyframes empty-arrive {
  0% {
    filter: blur(6px);
    opacity: 0;
    transform: translateY(10px);
  }

  to {
    filter: none;
    opacity: 1;
    transform: none;
  }
}

.starters {
  display: grid;
  justify-items: start;
  gap: 2px;
  margin: 18px 0 0 -12px;
  padding: 0;
  list-style: none;
}

.starter {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border: 0;
  border-radius: 10px;
  background: none;
  font-size: 14px;
  line-height: 1.4;
  text-align: left;
  transition: background 0.2s;
}

.starter:hover {
  background: var(--fill);
}

.empty-state__showcase {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

.empty-state__examples-caption {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 0;
  color: var(--muted);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  animation: empty-arrive 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
  animation-delay: 0.1s;
}

.empty-state__examples-caption-icon {
  flex: none;
  width: 14px;
  height: 14px;
}

.empty-state__examples {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.empty-state__examples[data-count='1'] {
  grid-template-columns: 1fr;
}

.tile {
  position: relative;
  min-width: 0;
}

.tile__halo {
  position: absolute;
  inset: 0;
  animation: empty-halo-sway calc(var(--empty-duration) * 1.5) ease-in-out infinite alternate;
  transition: opacity 0.3s;
}

.tile__halo::after,
.tile__halo::before {
  position: absolute;
  inset: -3px;
  border-radius: 20px;
  background: conic-gradient(
    from 220deg,
    #3186ff,
    #3186ff 22%,
    #ff4641 40%,
    #fc0 58%,
    #0ebc5f 76%,
    #3186ff
  );
  content: '';
  filter: blur(16px) saturate(1.3);
  opacity: 0;
}

.tile__halo::before {
  animation: empty-halo-visit calc(var(--empty-duration) * 2) ease-in-out infinite;
  animation-delay: calc(0.5s + var(--i) * var(--empty-duration) * 2 / var(--count));
}

.empty-state__examples[data-count='1'] .tile__halo::before {
  animation-name: empty-halo-breathe;
}

.tile__halo::after {
  transition: opacity 0.35s;
}

.tile:focus-within .tile__halo::after,
.tile:hover .tile__halo::after {
  opacity: var(--halo);
}

.tile:focus-within .tile__halo::before,
.tile:hover .tile__halo::before {
  animation-play-state: paused;
}

.empty-state__examples:has(.tile:focus-within) .tile:not(:focus-within) .tile__halo,
.empty-state__examples:has(.tile:hover) .tile:not(:hover) .tile__halo {
  opacity: 0;
}

@keyframes empty-halo-visit {
  0%,
  36%,
  to {
    opacity: 0;
  }

  8%,
  26% {
    opacity: var(--halo);
  }
}

@keyframes empty-halo-breathe {
  0%,
  to {
    opacity: calc(var(--halo) * 0.35);
  }

  50% {
    opacity: var(--halo);
  }
}

@keyframes empty-halo-sway {
  0% {
    transform: rotate(-4deg);
  }

  to {
    transform: rotate(4deg);
  }
}

.tile__frame {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  padding: 0;
  border: 0;
  border-radius: 16px;
  background: none;
  text-align: left;
  transition: transform 0.35s cubic-bezier(0.22, 1, 0.36, 1);
}

.tile:hover .tile__frame {
  transform: translateY(-2px);
}

.tile__media {
  position: relative;
  display: block;
  height: 100%;
  aspect-ratio: 3 / 2;
}

.tile__window {
  position: absolute;
  inset: 0;
  isolation: isolate;
  overflow: hidden;
  border-radius: 16px;
  background: var(--fill);
  box-shadow: 0 0 0 1px var(--line);
}

.tile__window img {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.tile__label {
  position: absolute;
  bottom: 10px;
  left: 10px;
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: calc(100% - 20px);
  padding: 5px 10px;
  border-radius: 12px;
  backdrop-filter: blur(8px);
  background: rgb(0 0 0 / 55%);
  color: var(--color-v3-text-on-button-reverse);
  font-size: 12px;
  line-height: 1.3;
  hyphens: auto;
  overflow-wrap: anywhere;
}

.empty-state[data-kind='chat'] .tile:first-child {
  grid-column: 1 / -1;
}

.empty-state[data-kind='chat'] .tile:first-child .tile__media {
  aspect-ratio: 16 / 9;
}

.empty-state[data-kind='chat'] img {
  object-position: 45% center;
}

.empty-state[data-kind='images'] .empty-state__examples {
  grid-template-rows: 1fr 1fr;
}

.empty-state[data-kind='images'] .tile:first-child {
  grid-row: 1 / -1;
}

.empty-state[data-kind='images'] .tile:first-child .tile__media {
  aspect-ratio: 3 / 4;
}

.empty-state[data-kind='images'] .tile:not(:first-child) .tile__media {
  aspect-ratio: auto;
}

.empty-state[data-kind='live'] .tile__media {
  aspect-ratio: 16 / 11;
}

.empty-state[data-kind='live'] img {
  object-position: center 47%;
}

.empty-state[data-kind='robotics'] .tile__media {
  aspect-ratio: 1280 / 875;
}

.empty-state__docs-link {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-top: 16px;
  padding: 8px 16px;
  border-radius: 9999px;
  background: var(--surface);
  box-shadow: 0 0 0 1px var(--line);
  color: var(--ink);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  text-decoration: none;
  transition: background 0.2s;
  animation: empty-arrive 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
  animation-delay: 0.29s;
}

.empty-state__docs-link:hover {
  background: var(--fill);
}

.empty-state__docs-link:focus-visible {
  outline: 2px solid var(--color-v3-outline-accent);
  outline-offset: 2px;
}

.empty-state__external-icon {
  color: var(--muted);
  transition:
    color 0.2s,
    transform 0.25s cubic-bezier(0.23, 1, 0.32, 1);
}

.empty-state__docs-link:hover .empty-state__external-icon {
  color: var(--ink);
  transform: translate(2px, -2px);
}

.detection {
  position: absolute;
  inset: 54% 46% 33.5% 45.5%;
  border: 1.5px solid var(--color-v3-text-on-button-reverse);
  border-radius: 8px;
  box-shadow: 0 0 0 1px rgb(0 0 0 / 35%);
  animation: empty-detect 0.6s cubic-bezier(0.22, 1, 0.36, 1) 0.9s both;
}

.detection b {
  position: absolute;
  bottom: calc(100% + 4px);
  left: -1.5px;
  padding: 2px 6px;
  border-radius: 8px;
  background: var(--color-v3-surface);
  color: var(--color-v3-text);
  font-size: 11px;
  font-weight: 500;
  line-height: 1.3;
  white-space: nowrap;
}

@keyframes empty-detect {
  0% {
    opacity: 0;
    transform: scale(1.6);
  }

  to {
    opacity: 1;
    transform: none;
  }
}

.empty-state[data-kind='music'] .tile__media {
  aspect-ratio: 1;
}

.tile__video {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.25s ease;
}

.tile:focus-within .tile__video,
.tile:hover .tile__video {
  opacity: 1;
}

.tile__play-icon {
  flex: none;
}

.empty-state[data-kind='video'] .empty-state__examples {
  grid-template-columns: 1.4fr 1fr;
}

.empty-state[data-kind='video'] .tile__media {
  aspect-ratio: 4 / 3;
}

.empty-state[data-kind='video'] .tile:nth-child(2) .tile__media {
  aspect-ratio: auto;
}

.empty-state[data-kind='video'] .empty-state__examples[data-count='3'] {
  grid-template-columns: 1fr 1.25fr;
  grid-template-rows: 1fr 1fr;
}

.empty-state[data-kind='video'] .empty-state__examples[data-count='3'] .tile:first-child {
  grid-row: 1 / -1;
}

.empty-state[data-kind='video']
  .empty-state__examples[data-count='3']
  .tile:first-child
  .tile__media {
  aspect-ratio: 9 / 16;
}

.empty-state[data-kind='video']
  .empty-state__examples[data-count='3']
  .tile:not(:first-child)
  .tile__media {
  aspect-ratio: auto;
}

.empty-state[data-kind='video'] img {
  animation: empty-push-in calc(var(--empty-duration) * 2.5) ease-in-out infinite alternate;
}

@keyframes empty-push-in {
  0% {
    transform: scale(1.02);
  }

  to {
    transform: scale(1.1) translate(-1.5%, 1%);
  }
}

@container (max-width: 640px) {
  .empty-state {
    grid-template-columns: minmax(0, 1fr);
    gap: 28px;
    padding: 32px 40px;
  }

  .empty-state h3 {
    font-size: 24px;
  }

  .empty-state__examples {
    row-gap: 16px;
  }

  .empty-state__examples[data-count='3'] .tile:first-child {
    grid-column: 1 / -1;
  }

  .empty-state[data-kind='images'] .empty-state__examples {
    grid-template-rows: none;
  }

  .empty-state[data-kind='images'] .tile:first-child {
    grid-row: auto;
  }

  .empty-state[data-kind='images'] .tile:first-child .tile__media {
    aspect-ratio: 4 / 3;
  }

  .empty-state[data-kind='images'] .tile:not(:first-child) .tile__media {
    aspect-ratio: 1;
  }

  .tile__media {
    height: auto;
  }
}

@media (prefers-contrast: more) {
  .tile__label {
    background: var(--color-v3-surface);
    color: var(--color-v3-text);
  }
}

@media (prefers-reduced-motion: reduce) {
  .empty-state h3,
  .empty-state[data-kind='video'] img,
  .empty-state__examples-caption,
  .detection,
  .empty-state__docs-link,
  .empty-state__description,
  .starter,
  .starters li,
  .tile,
  .tile__frame,
  .tile__halo,
  .tile__halo::after,
  .tile__halo::before {
    animation: none;
    transition: none;
  }

  .tile:first-child .tile__halo::before {
    opacity: var(--halo);
  }

  .tile:hover .tile__frame {
    transform: none;
  }
}
</style>
