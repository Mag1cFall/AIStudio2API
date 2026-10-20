<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import ModelEmptyState from './media/ModelEmptyState.vue'
import { usePlayground } from './session'
import { zeroConfigFor, type ZeroStarter } from './zero-configs'

const emit = defineEmits<{ starter: [item: ZeroStarter] }>()
const { t } = useI18n()
const { currentModel } = usePlayground()

// config 是所选模型系列的官网空态配置
const config = computed(() => zeroConfigFor(currentModel.value))
// configTitle 是官网空态标题，模型名缺失时用系列名
const configTitle = computed(() =>
  (config.value?.title ?? '').replace(
    '{model}',
    currentModel.value?.name || config.value?.family || '',
  ),
)
// configStarters 是带参考图缩略图的示例提示词
const configStarters = computed(() =>
  (config.value?.starters ?? []).map((item) =>
    item.images?.[0] ? { ...item, thumb: item.images[0] } : item,
  ),
)
// starters 是没有系列配置时的示例提示
const starters = computed(() => [t('pg.starter1'), t('pg.starter2'), t('pg.starter3')])
// heading 是没有系列配置时的空态标题
const heading = computed(() =>
  currentModel.value
    ? t('pg.startBuilding').replace('{model}', currentModel.value.name)
    : t('nav.playground'),
)
</script>

<template>
  <div class="zero-state">
    <ModelEmptyState
      v-if="config"
      :key="config.id"
      :kind="config.kind"
      :title="configTitle"
      :description="config.subtitle"
      :starters="configStarters"
      :examples="config.examples"
      :docs="config.docs"
      @starter="emit('starter', $event as ZeroStarter)"
      @example="emit('starter', $event as ZeroStarter)"
    />
    <section v-else class="empty-state">
      <div class="empty-state__heading">
        <h3>{{ heading }}</h3>
        <p v-if="currentModel?.details?.summary" class="empty-state__description">
          {{ currentModel.details.summary }}
        </p>
        <ul :aria-label="t('pg.startersLabel')" class="starters">
          <li v-for="(starter, index) in starters" :key="index" :style="{ '--i': index }">
            <button
              type="button"
              class="starter"
              @click="emit('starter', { title: starter, prompt: starter })"
            >
              <span>{{ starter }}</span>
            </button>
          </li>
        </ul>
      </div>
    </section>
  </div>
</template>

<style scoped>
.zero-state {
  container-type: inline-size;
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  box-sizing: border-box;
  width: 100%;
  min-height: 0;
  overflow: auto;
  padding: 20px 20px 0;
  animation: host-enter 0.3s cubic-bezier(0.23, 1, 0.32, 1) both;
}

.ms-model-empty-state {
  margin-block: auto;
}

.empty-state {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.15fr);
  align-items: center;
  gap: 56px;
  width: 100%;
  max-width: 976px;
  margin: auto;
  padding: 40px;
  color: var(--color-v3-text);
}

@container (max-width: 720px) {
  .empty-state {
    grid-template-columns: minmax(0, 1fr);
  }
}

h3 {
  margin: 0;
  font-size: 28px;
  font-weight: 500;
  letter-spacing: -0.6px;
  line-height: 1.2;
  text-wrap: balance;
}

.empty-state__description {
  max-width: 38ch;
  margin: 12px 0 0;
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
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
  color: inherit;
  cursor: pointer;
  font-size: 14px;
  line-height: 1.4;
  text-align: left;
  transition: background 0.2s;
}

.starter:hover {
  background: var(--color-v3-hover);
}

.starter:focus-visible {
  outline: 2px solid var(--color-v3-outline-accent);
  outline-offset: 2px;
}

h3,
.empty-state__description,
.starters li {
  animation: empty-arrive 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
}

.empty-state__description {
  animation-delay: 60ms;
}

.starters li {
  animation-delay: calc(0.14s + var(--i) * 50ms);
}

@keyframes empty-arrive {
  from {
    filter: blur(6px);
    opacity: 0;
    transform: translateY(10px);
  }
}

@keyframes host-enter {
  from {
    opacity: 0;
    transform: translateY(20px);
  }
}
</style>
