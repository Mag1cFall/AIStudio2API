<script setup lang="ts">
import { computed, ref, useTemplateRef } from 'vue'
import { useI18n } from '@/i18n'
import type { Model } from '@/types'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { IconName } from '@/playground/ui/icons'
import { priceLines } from './catalog'
import {
  elementOfID,
  elements,
  lanthanides,
  releasedElements,
  type Element,
  type ElementSymbol,
} from './periodic'

const props = defineProps<{
  models: Model[]
  // selected 是当前页面已选模型的 ID
  selected: string
  // query 是面板搜索框中的文字
  query: string
}>()
const emit = defineEmits<{ choose: [model: Model] }>()
const { t, locale } = useI18n()
const grid = useTemplateRef<HTMLElement>('grid')

// released 是实时目录中各元素对应的模型，已选模型所在的元素取已选模型
const released = computed(() => releasedElements(props.models, props.selected))
// selectedSymbol 是已选模型的元素
const selectedSymbol = computed(() => elementOfID(props.selected))
// latestSymbol 是发布日期最新的已发布元素
const latestSymbol = computed(() => {
  let latest: ElementSymbol | undefined
  let date = 0
  for (const [symbol, model] of released.value) {
    const value = model.details?.release_date ?? 0
    if (value > date) [latest, date] = [symbol, value]
  }
  return latest
})
// hovered 与 focused 是指针悬停与键盘聚焦的元素
const hovered = ref<ElementSymbol>()
const focused = ref<ElementSymbol>()
// restingSymbol 是没有悬停与聚焦时元素卡片展示的元素：已选模型的元素，其次为最新发布的元素与第一个已发布的元素
const restingSymbol = computed(() =>
  selectedSymbol.value !== undefined && released.value.has(selectedSymbol.value)
    ? selectedSymbol.value
    : (latestSymbol.value ?? released.value.keys().next().value),
)
// active 是元素卡片展示的元素
const active = computed(() => {
  const symbol = hovered.value ?? focused.value ?? restingSymbol.value
  return elements.find((element) => element.symbol === symbol)!
})
// activeModel 是元素卡片展示的元素对应的模型
const activeModel = computed(() => released.value.get(active.value.symbol))

// matches 返回元素是否符合搜索文字：元素符号、本地化名称、代号、原子序数与对应模型的名称和 ID
function matches(element: Element, query: string): boolean {
  const model = released.value.get(element.symbol)
  return (
    String(element.number) === query ||
    [
      element.symbol,
      element.codename,
      t(`pg.element.${element.symbol}`),
      model?.name ?? '',
      model?.id ?? '',
    ].some((text) => text.toLowerCase().includes(query))
  )
}

// tiles 是各元素格的展示状态，enterDelay 让元素格从没有悬停与聚焦时卡片展示的元素向外依次出现
const tiles = computed(() => {
  const query = props.query.trim().toLowerCase()
  const origin = elements.find((element) => element.symbol === restingSymbol.value)!
  return elements.map((element) => {
    const matched = query !== '' && matches(element, query)
    return {
      element,
      model: released.value.get(element.symbol),
      matched,
      dimmed: query !== '' && !matched,
      enterDelay: Math.round(
        30 + Math.hypot(element.period - origin.period, element.group - origin.group) * 18,
      ),
    }
  })
})

// legend 是“已发布 N 个”中数字前后的文字
const legend = computed(() => t('pg.periodicReleased').split('{count}'))

const dateFormat = computed(
  () =>
    new Intl.DateTimeFormat(locale.value, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      timeZone: 'UTC',
    }),
)
const monthFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', timeZone: 'UTC' }),
)
const numberFormat = computed(() => new Intl.NumberFormat(locale.value))

// facts 是元素卡片中模型的规格：token 上限、价格、知识截止与发布日期
const facts = computed(() => {
  const model = activeModel.value
  if (model === undefined) return []
  const list: { icon: IconName; text: string }[] = []
  if (model.input_token_limit && model.output_token_limit) {
    list.push({
      icon: 'tokenLimits',
      text: t('pg.tokenLimits')
        .replace('{input}', numberFormat.value.format(model.input_token_limit))
        .replace('{output}', numberFormat.value.format(model.output_token_limit)),
    })
  }
  for (const line of priceLines(model, t)) list.push({ icon: 'price', text: line.text })
  if (model.details?.knowledge_cutoff) {
    list.push({
      icon: 'cutoff',
      text: `${t('pg.knowledgeCutoff')} ${monthFormat.value.format(model.details.knowledge_cutoff * 1000)}`,
    })
  }
  if (model.details?.release_date) {
    list.push({
      icon: 'release',
      text: `${t('pg.releaseDate')} ${dateFormat.value.format(model.details.release_date * 1000)}`,
    })
  }
  return list
})

// tileLabel 返回已发布元素格的无障碍名称
function tileLabel(element: Element, model: Model): string {
  const parts = [
    t(`pg.element.${element.symbol}`),
    t('pg.periodicAtomicNumber').replace('{number}', String(element.number)),
    model.name,
  ]
  if (element.symbol === latestSymbol.value) parts.push(t('pg.periodicLatest'))
  return parts.join(', ')
}

// move 以方向键、Home 与 End 在已发布的元素格之间移动焦点
function move(event: KeyboardEvent): void {
  const buttons = [...grid.value!.querySelectorAll<HTMLButtonElement>('button.tile')]
  const index = buttons.indexOf(event.currentTarget as HTMLButtonElement)
  const steps: Record<string, number> = {
    ArrowRight: index + 1,
    ArrowDown: index + 1,
    ArrowLeft: index - 1,
    ArrowUp: index - 1,
    Home: 0,
    End: buttons.length - 1,
  }
  const next = steps[event.key]
  if (next === undefined) return
  event.preventDefault()
  hovered.value = undefined
  buttons[(next + buttons.length) % buttons.length]!.focus()
}
</script>

<template>
  <div class="periodic-board">
    <div class="periodic-stage">
      <div
        ref="grid"
        class="periodic-grid"
        role="group"
        :aria-label="t('pg.periodicLabel')"
        @mouseleave="hovered = undefined"
      >
        <div class="saddle">
          <span class="eyebrow">{{ t('pg.periodicEyebrow') }}</span>
          <h2 class="title">Gemini 4</h2>
          <p class="legend">
            <span v-if="legend[0]">{{ legend[0] }}</span>
            <span class="legend-count">{{ released.size }}</span>
            <span v-if="legend[1]">{{ legend[1] }}</span>
          </p>
        </div>
        <template v-for="tile in tiles" :key="tile.element.symbol">
          <button
            v-if="tile.model"
            type="button"
            class="tile released"
            :class="[
              `family-${tile.element.family}`,
              {
                selected: tile.element.symbol === selectedSymbol,
                latest: tile.element.symbol === latestSymbol,
                matched: tile.matched,
                dimmed: tile.dimmed,
              },
            ]"
            :style="{
              gridColumn: tile.element.group,
              gridRow: tile.element.period,
              '--enter-delay': `${tile.enterDelay}ms`,
            }"
            :aria-pressed="tile.element.symbol === selectedSymbol"
            :aria-label="tileLabel(tile.element, tile.model)"
            @click="emit('choose', tile.model)"
            @mouseenter="hovered = tile.element.symbol"
            @focus="focused = tile.element.symbol"
            @blur="focused = undefined"
            @keydown="move"
          >
            <span class="tile-number">{{ tile.element.number }}</span>
            <span class="tile-symbol">{{ tile.element.symbol }}</span>
            <span v-if="tile.element.symbol === latestSymbol" class="tile-pip"></span>
          </button>
          <div
            v-else
            class="tile"
            :class="{
              faded: tile.element.period === lanthanides.period && !tile.matched,
              matched: tile.matched,
              dimmed: tile.dimmed,
            }"
            :style="{
              gridColumn: tile.element.group,
              gridRow: tile.element.period,
              '--enter-delay': `${tile.enterDelay}ms`,
            }"
            aria-hidden="true"
            @mouseenter="hovered = tile.element.symbol"
          >
            <span class="tile-number">{{ tile.element.number }}</span>
            <span class="tile-symbol">{{ tile.element.symbol }}</span>
          </div>
        </template>
        <div
          v-tooltip.below="t('pg.periodicLanthanides')"
          class="tile faded range"
          :class="{ dimmed: query.trim() !== '' }"
          :style="{ gridColumn: lanthanides.group, gridRow: lanthanides.period }"
          aria-hidden="true"
        >
          {{ lanthanides.first }}–{{ lanthanides.last }}
        </div>
      </div>
    </div>
    <div class="specimen" :class="{ unreleased: !activeModel }">
      <div class="specimen-row">
        <div
          class="tile swatch"
          :class="activeModel ? ['released', `family-${active.family}`] : []"
          aria-hidden="true"
        >
          <span class="tile-number">{{ active.number }}</span>
          <span class="tile-symbol">{{ active.symbol }}</span>
        </div>
        <div class="specimen-text">
          <div class="specimen-heading">
            <span class="specimen-name">{{ t(`pg.element.${active.symbol}`) }}</span>
            <span class="specimen-meta">
              {{ t(`pg.family.${active.family}`) }} ·
              {{ t('pg.periodicAtomicNumber').replace('{number}', String(active.number)) }}
            </span>
            <span v-if="active.symbol === latestSymbol" class="specimen-latest">
              {{ t('pg.periodicLatest') }}
            </span>
          </div>
          <template v-if="activeModel">
            <div class="specimen-model">
              <span class="specimen-model-name">{{ activeModel.name }}</span>
              <span class="specimen-model-id">{{ activeModel.id }}</span>
            </div>
            <p
              v-if="activeModel.details?.summary || activeModel.description"
              class="specimen-description"
            >
              {{ activeModel.details?.summary || activeModel.description }}
            </p>
          </template>
          <p v-else class="specimen-description">{{ t('pg.periodicUnreleased') }}</p>
        </div>
      </div>
      <ul v-if="facts.length > 0" class="specimen-facts">
        <li v-for="(fact, index) in facts" :key="`${fact.icon}-${index}`">
          <UiIcon :name="fact.icon" :size="14" />
          <span>{{ fact.text }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.periodic-board {
  --tile-ink: var(--color-v3-text-disable);
  --tile-surface: transparent;
  --tile-edge: var(--color-v3-outline);
  --tile-shade: rgb(0 0 0 / 0.08);

  display: flex;
  flex-direction: column;
  gap: 12px;
  container-type: inline-size;
}

:root[data-theme='dark'] .periodic-board {
  --tile-ink: var(--color-v3-text-var);
  --tile-surface: var(--color-v3-surface-container-high);
  --tile-edge: transparent;
  --tile-shade: rgb(0 0 0 / 0.4);
}

.periodic-stage {
  padding: 16px 14px 10px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container-high);
}

:root[data-theme='dark'] .periodic-stage {
  background-color: var(--color-v3-surface);
}

.periodic-grid {
  display: grid;
  grid-template-columns: repeat(18, minmax(0, 1fr));
  gap: 5px 4px;
}

/* saddle 占据周期表第 1 至第 3 周期中间的空位 */
.saddle {
  display: flex;
  grid-row: 1 / 4;
  grid-column: 3 / 13;
  flex-direction: column;
  justify-content: center;
  min-width: 0;
  padding: 4px 20px 6px 22px;
  pointer-events: none;
}

.eyebrow {
  color: var(--color-v3-text-var);
  font-size: clamp(9px, 1.35cqi, 12px);
  font-weight: 700;
  line-height: 1.15;
  letter-spacing: 0.16em;
  text-transform: uppercase;
}

.title {
  margin: 4px 0 0;
  color: var(--color-v3-text);
  font-family: var(--font-heading);
  font-size: clamp(22px, 5.9cqi, 50px);
  font-weight: 700;
  line-height: 0.96;
  letter-spacing: -0.04em;
  white-space: nowrap;
}

.legend {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: clamp(6px, 1.3cqi, 12px) 0 0;
  color: var(--color-v3-text-var);
  font-size: clamp(10px, 1.5cqi, 13px);
  font-weight: 500;
  line-height: 1.2;
}

.legend-count {
  min-width: 20px;
  padding: 0 5px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 3px;
  background-color: var(--color-v3-surface);
  box-shadow: inset 0 -1px 0 var(--color-v3-outline);
  color: var(--color-v3-text);
  font-family: var(--font-heading);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
  text-align: center;
}

:root[data-theme='dark'] .legend-count {
  background-color: var(--color-v3-surface-container-high);
}

.tile {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  aspect-ratio: 1 / 1.12;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 3px;
  background-color: var(--tile-surface);
  box-shadow:
    inset 0 0 0 1px var(--tile-edge),
    inset 0 -2px 0 var(--tile-shade),
    0 1px 1px var(--tile-shade);
  color: var(--tile-ink);
  font: inherit;
  user-select: none;
  transition:
    opacity 0.15s ease-out,
    transform 0.15s cubic-bezier(0.23, 1, 0.32, 1),
    box-shadow 0.15s ease-out;
}

.tile-number {
  position: absolute;
  top: 3px;
  left: 4px;
  font-size: 9px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  line-height: 1;
  opacity: 0.7;
}

.tile-symbol {
  padding-top: 6px;
  font-family: var(--font-heading);
  font-size: clamp(9px, 1.95cqi, 18px);
  font-weight: 700;
  line-height: 1;
  letter-spacing: -0.025em;
}

.tile.dimmed {
  opacity: 0.28;
}

.tile.matched {
  outline: 1.5px solid var(--color-v3-outline-accent);
  outline-offset: 1px;
}

/* faded 为第 6 周期未发布的元素格，向下渐隐 */
.tile.faded {
  mask-image: linear-gradient(180deg, rgb(0 0 0 / 0.56) 0, rgb(0 0 0 / 0.18) 68%, transparent);
  pointer-events: none;
}

.tile.range {
  font-size: clamp(6px, 1cqi, 9px);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  pointer-events: auto;
}

/* family-* 为各化学分类的色相，已发布的元素格以它着色 */
.family-alkali {
  --hue: 25;
}

.family-alkaline {
  --hue: 60;
}

.family-transition {
  --hue: 300;
}

.family-postTransition {
  --hue: 100;
}

.family-metalloid {
  --hue: 345;
}

.family-nonmetal {
  --hue: 255;
}

.family-halogen {
  --hue: 200;
}

.family-noble {
  --hue: 155;
}

.tile.released {
  --tint: oklch(0.56 0.17 var(--hue));
  --tile-ink: color-mix(in oklab, var(--tint) 78%, var(--color-v3-text));

  z-index: 1;
  background-color: color-mix(in oklab, var(--tint) 16%, var(--color-v3-surface));
  box-shadow:
    inset 0 0 0 1px color-mix(in oklab, var(--tint) 34%, transparent),
    inset 0 -2px 0 color-mix(in oklab, var(--tint) 26%, transparent),
    0 2px 3px var(--tile-shade);
}

:root[data-theme='dark'] .tile.released {
  --tint: oklch(0.82 0.13 var(--hue));
  --tile-ink: color-mix(in oklab, var(--tint) 82%, var(--color-v3-text));

  background-color: color-mix(in oklab, var(--tint) 17%, var(--color-v3-surface-container-high));
}

button.tile.released {
  cursor: pointer;
}

/* latest 为最新发布的元素格，以分类色实底显示 */
.tile.released.latest {
  --tile-ink: var(--color-v3-surface);

  background-color: var(--tint);
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 0.3),
    inset 0 -2px 0 rgb(0 0 0 / 0.18),
    0 2px 6px color-mix(in oklab, var(--tint) 45%, transparent);
}

:root[data-theme='dark'] .tile.released.latest {
  --tile-ink: var(--color-v3-surface);

  background-color: var(--tint);
}

.tile-pip {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background-color: currentcolor;
}

button.tile.released:hover {
  z-index: 3;
  transform: translateY(-2px);
}

button.tile.released:active {
  transform: translateY(0.5px) scale(0.97);
}

button.tile.released:focus-visible {
  z-index: 3;
  outline: 2px solid var(--color-v3-outline-accent);
  outline-offset: 2px;
}

button.tile.released.selected {
  z-index: 2;
  outline: 2px solid var(--color-v3-outline-accent);
  outline-offset: 2px;
  transform: translateY(-1px);
}

.specimen {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 68px;
  padding: 12px 14px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  background-color: var(--color-v3-surface-container);
}

.specimen-row {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  min-width: 0;
}

.tile.swatch {
  flex-shrink: 0;
  width: 38px;
  height: 42px;
  aspect-ratio: auto;
}

.tile.swatch .tile-number {
  font-size: 8.5px;
}

.tile.swatch .tile-symbol {
  padding-top: 4px;
  font-size: 16px;
}

.specimen-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.specimen-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 8px;
}

.specimen-name {
  color: var(--color-v3-text);
  font-family: var(--font-heading);
  font-size: 16px;
  font-weight: 600;
  line-height: 1.3;
}

.specimen-meta,
.specimen-model-id {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 1.3;
}

.specimen-latest {
  padding: 0 6px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 8px;
  color: var(--color-v3-text);
  font-size: 11.5px;
  line-height: 16px;
}

.specimen-model {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 8px;
}

.specimen-model-name {
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 1.3;
}

.specimen-model-id {
  font-family: var(--font-mono);
}

.specimen-description {
  margin: 0;
  color: var(--color-v3-text-var);
  font-size: 12.5px;
  line-height: 1.4;
}

.specimen-facts {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.specimen-facts li {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 6px;
  background-color: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text-var);
  font-size: 11.5px;
  line-height: 1.3;
}

:root[data-theme='dark'] .specimen-facts li {
  background-color: var(--color-v3-surface-container-high);
}

@container (max-width: 520px) {
  .periodic-stage {
    padding: 10px 8px 6px;
  }

  .periodic-grid {
    gap: 3px 2px;
  }

  .saddle {
    padding: 0 6px 2px 8px;
  }

  .tile {
    border-radius: 2px;
  }

  .tile-number {
    display: none;
  }

  .tile.range {
    font-size: 0;
  }

  .tile-symbol {
    padding-top: 0;
  }

  .tile.swatch .tile-number {
    display: inline;
  }

  .tile-pip {
    top: 2px;
    right: 2px;
    width: 3px;
    height: 3px;
  }
}

@media (prefers-reduced-motion: no-preference) {
  .periodic-grid .tile {
    animation: tile-enter 0.24s cubic-bezier(0.23, 1, 0.32, 1) var(--enter-delay, 0ms) backwards;
  }

  .tile.released.latest .tile-pip {
    animation: pip-pulse 2.4s ease-in-out 0.8s infinite;
  }
}

@keyframes tile-enter {
  from {
    opacity: 0;
    transform: scale(0.82);
  }
}

@keyframes pip-pulse {
  50% {
    opacity: 0.35;
    transform: scale(1.6);
  }
}
</style>
