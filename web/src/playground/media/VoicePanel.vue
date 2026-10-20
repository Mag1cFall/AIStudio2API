<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import type { TranslationKey } from '@/i18n'
import type { VoiceDetail } from '@/types'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSlidingPanel from '@/playground/ui/UiSlidingPanel.vue'
import StyleDropdown from './StyleDropdown.vue'
import { useSamplePlayer } from './sample'
import { accentOptions, paceOptions, useSpeech, type StylePreset } from './speech'

const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ speaker: number }>()
const { t } = useI18n()
const speech = useSpeech()
const { state, voices, daikon, styles } = speech
const { playingSample, loadingSample, toggleSample, stopSample } = useSamplePlayer()
const current = computed(() => state.speakers[props.speaker]!)
const currentDetail = computed(() => speech.voiceDetail(current.value.voice))

// FilterKey 是音色筛选项
type FilterKey = 'locale' | 'gender' | 'pitch' | 'trait' | 'use_case'
const filterKeys: { key: FilterKey; label: TranslationKey; all: TranslationKey }[] = [
  { key: 'locale', label: 'pg.voiceLanguage', all: 'pg.allLanguages' },
  { key: 'gender', label: 'pg.voiceGender', all: 'pg.allGenders' },
  { key: 'pitch', label: 'pg.voicePitch', all: 'pg.allPitches' },
  { key: 'trait', label: 'pg.voicePersona', all: 'pg.allStyles' },
  { key: 'use_case', label: 'pg.voiceUseCase', all: 'pg.allUseCases' },
]
const search = ref('')
const filters = ref<Record<FilterKey, string>>({
  locale: '',
  gender: '',
  pitch: '',
  trait: '',
  use_case: '',
})
// drill 是展开查看全部的分组
const drill = ref('')
const favorites = ref(new Set<string>(loadFavorites()))

// loadFavorites 读取本浏览器收藏的音色
function loadFavorites(): string[] {
  try {
    return JSON.parse(localStorage.getItem('aistudio2api.voice-favorites') ?? '[]') as string[]
  } catch {
    return []
  }
}

// toggleFavorite 收藏或取消收藏音色
function toggleFavorite(name: string): void {
  const next = new Set(favorites.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  favorites.value = next
  try {
    localStorage.setItem('aistudio2api.voice-favorites', JSON.stringify([...next]))
  } catch {
    // 浏览器禁用存储时收藏只在本页有效
  }
}

// languageName 返回语言区域的显示名称
function languageName(locale: string): string {
  try {
    return new Intl.DisplayNames([navigator.language], { type: 'language' }).of(locale) ?? locale
  } catch {
    return locale
  }
}

// optionLabel 返回筛选值的显示文字
function optionLabel(key: FilterKey, value: string): string {
  return key === 'locale' ? languageName(value) : value
}

// filterOptions 返回音色中出现过的筛选值
function filterOptions(key: FilterKey): string[] {
  return [...new Set(voices.value.map((voice) => voice[key] ?? '').filter(Boolean))]
}

// subtitle 与官网一致把描述与音高合为一行
function subtitle(voice: VoiceDetail): string {
  const items = [...(voice.descriptions ?? [])]
  if (voice.pitch) items.push(`${voice.pitch} pitch`)
  return items.join(' · ')
}

const filtering = computed(
  () => search.value.trim() !== '' || Object.values(filters.value).some(Boolean),
)
const matched = computed(() => {
  const query = search.value.trim().toLowerCase()
  return voices.value.filter((voice) => {
    if (
      query &&
      ![voice.name, ...(voice.descriptions ?? []), voice.accent, voice.trait, voice.use_case].some(
        (value) => value?.toLowerCase().includes(query),
      )
    )
      return false
    return filterKeys.every(({ key }) => !filters.value[key] || voice[key] === filters.value[key])
  })
})
// groups 与官网音色库一致：收藏在前，其余按用途分组，每组先显示 3 个
const groups = computed(() => {
  if (filtering.value) return [{ id: 'search', title: '', voices: matched.value, total: 0 }]
  const result: { id: string; title: string; voices: VoiceDetail[]; total: number }[] = []
  const favored = voices.value.filter((voice) => favorites.value.has(voice.name))
  if (favored.length > 0)
    result.push({
      id: 'favorites',
      title: t('pg.favorites'),
      voices: favored,
      total: favored.length,
    })
  const byUse = new Map<string, VoiceDetail[]>()
  for (const voice of voices.value) {
    const key = voice.use_case || ''
    byUse.set(key, [...(byUse.get(key) ?? []), voice])
  }
  for (const [key, list] of byUse) {
    const id = key || 'other'
    result.push({
      id,
      title: key || t('pg.otherVoices'),
      voices: drill.value === id || id === 'favorites' ? list : list.slice(0, 3),
      total: list.length,
    })
  }
  return drill.value ? result.filter((group) => group.id === drill.value) : result
})
const drillGroup = computed(() => groups.value.find((group) => group.id === drill.value))

// choose 为当前说话人选择音色
function choose(name: string): void {
  current.value.voice = name
}

// chooseOption 设置旧版模型说话人的语速或口音，再次选择已选项时清除
function chooseOption(field: 'pace' | 'accent', option: StylePreset): void {
  current.value[field] = current.value[field] === option.value ? '' : option.value
}

watch(open, (value) => {
  if (!value) {
    stopSample()
    drill.value = ''
  }
})
</script>

<template>
  <UiSlidingPanel v-model:open="open" :title="t('pg.speakerSettings')" width="400px">
    <div class="speaker-panel">
      <section v-if="!daikon" class="panel-section voice-direction-section">
        <div class="section-label">{{ t('pg.voiceDirection') }}</div>
        <textarea
          v-model="current.persona"
          class="persona-textarea"
          rows="2"
          :placeholder="t('pg.personaPlaceholder')"
          :aria-label="t('pg.voiceDirection')"
        ></textarea>
        <div class="direction-chips-row">
          <StyleDropdown
            v-model="current.style"
            class="direction-style"
            :styles="styles"
            :custom="false"
          />
          <DropdownMenuRoot
            v-for="field in ['pace', 'accent'] as const"
            :key="field"
            :modal="false"
          >
            <DropdownMenuTrigger as-child>
              <button
                type="button"
                ms-button
                class="ms-button-filter-chip attribute-chip"
                :aria-label="field === 'pace' ? t('pg.pace') : t('pg.accent')"
              >
                <span class="chip-label">
                  {{
                    (field === 'pace' ? paceOptions : accentOptions).find(
                      (option) => option.value === current[field],
                    )?.label ?? (field === 'pace' ? t('pg.pace') : t('pg.accent'))
                  }}
                </span>
                <UiIcon name="arrowDropDown" :size="18" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent
                align="start"
                :side-offset="4"
                class="ms-menu-panel voice-style-menu"
              >
                <DropdownMenuItem
                  v-for="option in field === 'pace' ? paceOptions : accentOptions"
                  :key="option.label"
                  class="ms-menu-item attribute-menu-item"
                  :class="{ selected: current[field] === option.value }"
                  @select="chooseOption(field, option)"
                >
                  <span v-if="field === 'pace'" class="preset-option">
                    <span class="preset-label">{{ option.label }}</span>
                    <span class="preset-description">{{ option.value }}</span>
                  </span>
                  <template v-else>{{ option.label }}</template>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
        </div>
        <hr class="panel-divider" />
      </section>

      <section class="panel-section voice-section">
        <div class="section-label">{{ t('pg.voice') }}</div>
        <div class="current-voice-card">
          <div class="current-voice-info">
            <span class="current-voice-caption">{{ t('pg.currentVoice') }}</span>
            <span class="current-voice-name">{{ current.voice || t('pg.none') }}</span>
          </div>
          <button
            v-if="currentDetail?.sample_url"
            v-tooltip.left="
              playingSample === currentDetail.sample_url ? t('pg.pause') : t('pg.play')
            "
            type="button"
            class="play-pill"
            :aria-label="t('pg.playVoiceSample')"
            @click="toggleSample(currentDetail.sample_url)"
          >
            <UiIcon
              :name="
                loadingSample === currentDetail.sample_url
                  ? 'spinner'
                  : playingSample === currentDetail.sample_url
                    ? 'stopFill'
                    : 'playFill'
              "
              :size="16"
            />
          </button>
        </div>
        <div class="search-action-row">
          <label class="voice-search">
            <UiIcon name="search" :size="18" />
            <input
              v-model="search"
              type="search"
              :placeholder="t('pg.searchVoices')"
              :aria-label="t('pg.searchVoices')"
            />
          </label>
        </div>
        <div class="filter-chips-row">
          <DropdownMenuRoot v-for="filter in filterKeys" :key="filter.key" :modal="false">
            <DropdownMenuTrigger as-child>
              <button
                type="button"
                ms-button
                class="ms-button-filter-chip filter-dropdown-chip"
                :class="{ active: filters[filter.key] }"
                :disabled="filterOptions(filter.key).length === 0"
              >
                <span class="chip-label">
                  {{
                    filters[filter.key]
                      ? optionLabel(filter.key, filters[filter.key])
                      : t(filter.label)
                  }}
                </span>
                <UiIcon name="arrowDropDown" :size="18" class="dropdown-icon" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent
                align="start"
                :side-offset="4"
                class="ms-menu-panel voice-filter-menu"
              >
                <DropdownMenuItem
                  class="ms-menu-item"
                  :class="{ selected: !filters[filter.key] }"
                  @select="filters[filter.key] = ''"
                >
                  {{ t(filter.all) }}
                </DropdownMenuItem>
                <DropdownMenuItem
                  v-for="value in filterOptions(filter.key)"
                  :key="value"
                  class="ms-menu-item"
                  :class="{ selected: filters[filter.key] === value }"
                  @select="filters[filter.key] = value"
                >
                  {{ optionLabel(filter.key, value) }}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
        </div>

        <section class="voice-list" :aria-label="t('pg.availableVoices')">
          <div v-if="drill && drillGroup" class="section-drilldown-header">
            <button
              v-tooltip.below="t('pg.backToLibrary')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon back-to-lib-btn"
              :aria-label="t('pg.backToLibrary')"
              @click="drill = ''"
            >
              <UiIcon name="arrowBack" :size="20" />
            </button>
            <div class="drilldown-title-group">
              <span class="group-title">{{ drillGroup.title }}</span>
              <span class="group-count">{{ drillGroup.total }}</span>
            </div>
          </div>
          <div v-for="group in groups" :key="group.id" class="voice-group">
            <div v-if="!drill && group.title" class="voice-group-header">
              <span class="group-title">{{ group.title }}</span>
              <button
                v-if="group.total > group.voices.length"
                type="button"
                class="group-view-all-btn"
                @click="drill = group.id"
              >
                {{ t('pg.viewAll') }}
              </button>
            </div>
            <div class="voice-group-items">
              <div
                v-for="voice in group.voices"
                :key="voice.name"
                class="voice-card"
                :class="{ selected: voice.name === current.voice }"
                :data-voice-name="voice.name"
              >
                <UiIcon
                  v-if="voice.name === current.voice"
                  name="check"
                  :size="18"
                  class="voice-lead-check"
                />
                <button
                  type="button"
                  class="voice-card-content"
                  :aria-label="
                    voice.name === current.voice ? `${voice.name} (${t('pg.current')})` : voice.name
                  "
                  @click="choose(voice.name)"
                >
                  <span class="voice-row-primary">
                    <span class="voice-name">{{ voice.name }}</span>
                  </span>
                  <span v-tooltip.below="subtitle(voice)" class="voice-subtitle">{{
                    subtitle(voice)
                  }}</span>
                </button>
                <div class="voice-row-actions">
                  <button
                    v-tooltip.below="
                      favorites.has(voice.name) ? t('pg.removeFavorite') : t('pg.addFavorite')
                    "
                    type="button"
                    ms-button
                    class="ms-button-borderless ms-button-icon voice-action-btn"
                    :class="{ hearted: favorites.has(voice.name) }"
                    :aria-label="
                      favorites.has(voice.name) ? t('pg.removeFavorite') : t('pg.addFavorite')
                    "
                    @click="toggleFavorite(voice.name)"
                  >
                    <UiIcon
                      :name="favorites.has(voice.name) ? 'favoriteFill' : 'favorite'"
                      :size="18"
                    />
                  </button>
                  <button
                    v-if="voice.sample_url"
                    v-tooltip.below="
                      playingSample === voice.sample_url ? t('pg.pause') : t('pg.play')
                    "
                    type="button"
                    class="play-pill"
                    :aria-label="t('pg.playVoiceSample')"
                    @click="toggleSample(voice.sample_url)"
                  >
                    <UiIcon
                      :name="
                        loadingSample === voice.sample_url
                          ? 'spinner'
                          : playingSample === voice.sample_url
                            ? 'stopFill'
                            : 'playFill'
                      "
                      :size="16"
                    />
                  </button>
                </div>
              </div>
            </div>
          </div>
          <p v-if="filtering && matched.length === 0" role="status" class="empty-state">
            {{ t('pg.noVoicesMatch') }}
          </p>
        </section>
      </section>
    </div>
  </UiSlidingPanel>
</template>

<style scoped>
.speaker-panel {
  display: flex;
  flex-direction: column;
  padding: 0 12px 0 6px;
}

.section-label {
  margin-bottom: 4px;
  color: var(--color-v3-text-var);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.8px;
  line-height: 18px;
  text-transform: uppercase;
}

.voice-direction-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.persona-textarea {
  box-sizing: border-box;
  width: 100%;
  max-height: 4lh;
  padding: 8px 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  outline: none;
  background: transparent;
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
  line-height: 21px;
  resize: none;
}

.persona-textarea:focus {
  border-color: var(--color-v3-outline-accent);
}

.direction-chips-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.direction-style {
  max-width: 165px;
}

.attribute-chip {
  height: 32px;
  border-radius: 12px;
}

.panel-divider {
  width: 100%;
  margin: 16px 0;
  border: none;
  border-top: 1px solid var(--color-v3-outline-var);
}

.voice-section {
  display: flex;
  flex-direction: column;
  padding-top: 4px;
}

.current-voice-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 4px;
  margin-bottom: 12px;
  padding: 12px 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
}

.current-voice-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.current-voice-caption {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.current-voice-name {
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.play-pill {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 9999px;
  background: var(--color-v3-surface);
  color: var(--color-v3-text);
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.play-pill:hover {
  background-color: var(--color-v3-surface-container-highest);
}

.search-action-row {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 8px;
}

.voice-search {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 8px;
  min-width: 0;
  height: 40px;
  padding: 0 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  color: var(--color-v3-text-var);
}

.voice-search:focus-within {
  border-color: var(--color-v3-outline-accent);
}

.voice-search input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
}

.filter-chips-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.filter-dropdown-chip {
  flex-shrink: 0;
  height: 32px;
  padding: 0 8px 0 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 10px;
  font-size: 13px;
}

.filter-dropdown-chip.active {
  border-color: var(--color-v3-text);
  background-color: var(--color-v3-text);
  color: var(--color-v3-surface);
}

.filter-dropdown-chip .dropdown-icon {
  margin-left: 2px;
}

.voice-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-right: -12px;
  margin-left: -6px;
  padding-top: 4px;
}

.voice-group {
  display: flex;
  flex-direction: column;
  margin-bottom: 12px;
}

.section-drilldown-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px 12px;
}

.back-to-lib-btn {
  margin-left: -6px;
}

.drilldown-title-group {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.drilldown-title-group .group-title {
  color: var(--color-v3-text);
  font-size: 16px;
  font-weight: 500;
}

.group-count {
  color: var(--color-v3-text-var);
  font-size: 12px;
}

.voice-group-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 4px;
  padding: 8px 12px 4px;
}

.voice-group-header .group-title {
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 18px;
}

.group-view-all-btn {
  min-width: 0;
  padding: 2px 8px;
  border: none;
  border-radius: 8px;
  background: none;
  color: var(--color-v3-text-var);
  font: inherit;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
  cursor: pointer;
}

.group-view-all-btn:hover {
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text);
}

.voice-group-items {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.voice-card {
  display: flex;
  align-items: center;
  width: 100%;
  height: 56px;
  padding: 0 14px;
  border-radius: 12px;
  transition: background-color 75ms ease-in-out;
}

.voice-card.selected {
  background: var(--color-v3-surface-container);
}

.voice-card:hover {
  background: var(--color-v3-hover);
}

.voice-card:hover .voice-action-btn {
  opacity: 1;
}

.voice-lead-check {
  flex-shrink: 0;
  margin-right: 12px;
  color: var(--color-v3-text);
  animation: check-reveal 0.15s cubic-bezier(0, 0, 0.2, 1);
}

@keyframes check-reveal {
  from {
    width: 0;
    margin-right: -12px;
    opacity: 0;
  }

  to {
    width: 18px;
    margin-right: 12px;
    opacity: 1;
  }
}

.voice-card-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.voice-row-primary {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.voice-name {
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.voice-subtitle {
  overflow: hidden;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.voice-row-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
}

.voice-action-btn {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  color: var(--color-v3-text-var);
  opacity: 0;
  transition: opacity 0.12s ease-in-out;
}

.voice-action-btn:hover,
.voice-action-btn:focus-visible {
  background: var(--color-v3-surface-container-highest);
  color: var(--color-v3-text);
  opacity: 1;
}

.voice-action-btn.hearted {
  color: var(--color-v3-accent-3);
  opacity: 1;
}

.empty-state {
  margin: 0;
  padding: 16px;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  text-align: center;
}
</style>

<style>
.voice-filter-menu {
  max-height: 320px;
  overflow-y: auto;
}

.voice-filter-menu .ms-menu-item.selected,
.voice-style-menu .ms-menu-item.selected {
  background-color: var(--color-v3-surface-container-highest);
}
</style>
