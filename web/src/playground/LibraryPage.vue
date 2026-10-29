<script lang="ts">
import { ref } from 'vue'
import type { PromptSummary } from '@/types'

// library 是历史页已读取的会话、下一页令牌、当前页、滚动位置与整表读取的序号，与官网一样离开历史页后保留，回到历史页时先显示
const library = {
  prompts: ref<PromptSummary[]>([]),
  nextPageToken: ref(''),
  loaded: ref(false),
  page: ref(0),
  scrollTop: 0,
  version: 0,
}
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiGradientSpinner from '@/playground/ui/UiGradientSpinner.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import LibraryPaginator from './LibraryPaginator.vue'
import LibraryRowMenu from './LibraryRowMenu.vue'
import SavePromptDialog from './SavePromptDialog.vue'
import { useVideo } from './media/video'
import { useMediaQuery } from './media-query'
import { newTurnID, usePlayground } from './session'

// SortColumn 是排序所依据的列，空字符串为默认的更新时间倒序
type SortColumn = '' | 'name' | 'updated'

const { t, locale, errorText } = useI18n()
const store = useConsole()
const router = useRouter()
const playground = usePlayground()
const video = useVideo()
const handset = useMediaQuery('(max-width: 768px)')
const { prompts, nextPageToken, loaded, page } = library
const loadingMore = ref(false)
const scroller = ref<HTMLElement>()
const query = ref('')
const pending = ref<PromptSummary | null>(null)
const renaming = ref<PromptSummary | null>(null)
const sort = ref<{ column: SortColumn; descending: boolean }>({ column: '', descending: false })

// shown 与官网一样是已读取的会话中标题、描述、所有者或更新时间文字包含搜索词的会话，按官网表头的规则排序：名称比较小写后的码位，更新时间比较时间戳
const shown = computed(() => {
  const text = query.value.trim().toLowerCase()
  const items = prompts.value.filter((item) =>
    [title(item), item.description, t('pg.createdByYou'), relativeTime(item.updated_at)].some(
      (field) => field.toLowerCase().includes(text),
    ),
  )
  const { column, descending } = sort.value
  if (column === '') return items
  const key = (item: PromptSummary) =>
    column === 'name' ? title(item).toLowerCase() : updatedTime(item)
  return items.sort((a, b) => {
    const left = key(a)
    const right = key(b)
    return left === right ? 0 : (left < right ? -1 : 1) * (descending ? -1 : 1)
  })
})

// title 返回会话标题，未命名时为占位文字
function title(item: PromptSummary): string {
  return item.title || t('pg.untitled')
}

// updatedTime 返回会话更新时间的毫秒时间戳
function updatedTime(item: PromptSummary): number {
  return Date.parse(item.updated_at)
}

// now 是相对时间的当前时刻，每分钟更新
const now = ref(Date.now())
const clock = window.setInterval(() => (now.value = Date.now()), 60_000)

// relativeTime 按界面语言把更新时间写成相对当前的时间
function relativeTime(value: string): string {
  const seconds = (new Date(value).getTime() - now.value) / 1000
  const format = new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' })
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['year', 31536000],
    ['month', 2592000],
    ['week', 604800],
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
  ]
  for (const [unit, size] of units)
    if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit)
  return format.format(Math.round(seconds), 'second')
}

// toggleSort 按官网表头切换排序：升序、降序、恢复默认
function toggleSort(column: Exclude<SortColumn, ''>): void {
  const current = sort.value
  if (current.column !== column) sort.value = { column, descending: false }
  else if (!current.descending) sort.value = { column, descending: true }
  else sort.value = { column: '', descending: false }
}

// ariaSort 返回列表头的 aria-sort 值
function ariaSort(column: SortColumn): 'ascending' | 'descending' | 'none' {
  if (sort.value.column !== column) return 'none'
  return sort.value.descending ? 'descending' : 'ascending'
}

// openPrompt 打开会话
function openPrompt(item: PromptSummary): void {
  void router.push(`/prompts/${encodeURIComponent(item.id)}`)
}

// copy 与官网一样复制会话后打开副本
async function copy(item: PromptSummary): Promise<void> {
  try {
    const id = newTurnID()
    await api.copyPrompt(item.id, id, t('pg.copyOf').replace('{title}', title(item)))
    void router.push(`/prompts/${encodeURIComponent(id)}`)
  } catch (error) {
    store.showNotice(errorText(error), 'error')
  }
}

// rename 保存对话框中的标题与描述，同一会话已在试用页打开时同步其标题与描述
async function rename(newTitle: string, description: string): Promise<void> {
  const item = renaming.value
  if (item === null) return
  try {
    const summary = await api.renamePrompt(item.id, newTitle, description)
    prompts.value = [summary, ...prompts.value.filter((entry) => entry.id !== item.id)]
    if (playground.session.id === item.id)
      Object.assign(playground.session, { title: newTitle, description })
    if (video.state.id === item.id) Object.assign(video.state, { title: newTitle, description })
  } catch (error) {
    store.showNotice(errorText(error), 'error')
  }
}

// remove 删除确认框中的会话并从列表移除，同一会话已在试用页打开时换成新会话
async function remove(): Promise<void> {
  const item = pending.value
  pending.value = null
  if (item === null) return
  try {
    if (playground.session.id === item.id) await playground.deletePrompt()
    else if (video.state.id === item.id) await video.remove()
    else await api.deletePrompt(item.id)
    prompts.value = prompts.value.filter((entry) => entry.id !== item.id)
  } catch (error) {
    store.showNotice(errorText(error), 'error')
  }
}

// pageSize 与官网 ListPrompts 每页条数相同
const pageSize = 100

// filtering 表示有搜索词或按表头排序，此时搜索与排序作用于全部会话
const filtering = computed(() => query.value.trim() !== '' || sort.value.column !== '')

// pageRows 是当前页的会话
const pageRows = computed(() =>
  shown.value.slice(page.value * pageSize, (page.value + 1) * pageSize),
)

// total 是会话总数，还有未读取的页时为 undefined
const total = computed(() => (nextPageToken.value === '' ? shown.value.length : undefined))

// hasNext 表示当前页之后还有会话
const hasNext = computed(
  () => (page.value + 1) * pageSize < shown.value.length || nextPageToken.value !== '',
)

// refresh 从头重新读取与已读取条数相同（至少一页）的会话，替换列表与下一页令牌，返回前有更新的整表读取时丢弃结果
async function refresh(): Promise<void> {
  const version = ++library.version
  const result = await api.promptPage(Math.max(pageSize, prompts.value.length), '')
  if (version !== library.version) return
  prompts.value = result.prompts
  nextPageToken.value = result.next_page_token ?? ''
}

// loadPage 读取 token 之后的一页会话并追加到列表
async function loadPage(token: string): Promise<void> {
  const result = await api.promptPage(pageSize, token)
  const known = new Set(prompts.value.map((item) => item.id))
  prompts.value = prompts.value.concat(result.prompts.filter((item) => !known.has(item.id)))
  nextPageToken.value = result.next_page_token ?? ''
}

// fill 按下一页令牌读取，直到第 index 页读满或没有下一页
async function fill(index: number): Promise<void> {
  while (nextPageToken.value !== '' && prompts.value.length < (index + 1) * pageSize)
    await loadPage(nextPageToken.value)
}

// turn 翻到前一页或后一页，后一页未读取时先读取，翻页后回到列表顶部
async function turn(step: number): Promise<void> {
  loadingMore.value = true
  try {
    await fill(page.value + step)
  } catch (error) {
    store.showNotice(errorText(error), 'error')
    return
  } finally {
    loadingMore.value = false
  }
  page.value += step
  if (scroller.value) scroller.value.scrollTop = 0
}

// readAll 在搜索或排序前读取全部会话，返回前有更新的整表读取时丢弃结果
async function readAll(): Promise<void> {
  if (nextPageToken.value === '') return
  const version = ++library.version
  loadingMore.value = true
  try {
    const all = await api.prompts()
    if (version !== library.version) return
    prompts.value = all
    nextPageToken.value = ''
  } catch (error) {
    store.showNotice(errorText(error), 'error')
  } finally {
    loadingMore.value = false
  }
}

watch(filtering, (value) => {
  if (value) void readAll()
})

watch([query, sort], () => {
  page.value = 0
})

watch(
  () => shown.value.length,
  (length) => {
    page.value = Math.min(page.value, Math.max(0, Math.ceil(length / pageSize) - 1))
  },
)

onMounted(async () => {
  document.title = `${t('pg.history')} | AIStudio2API`
  scroller.value!.scrollTop = library.scrollTop
  try {
    await refresh()
  } catch (error) {
    store.showNotice(errorText(error), 'error')
  }
  loaded.value = true
  if (filtering.value) await readAll()
})

onBeforeUnmount(() => {
  library.scrollTop = filtering.value ? 0 : scroller.value!.scrollTop
  if (filtering.value) page.value = 0
  window.clearInterval(clock)
})
</script>

<template>
  <div ref="scroller" class="library-page">
    <div class="lib-view">
      <div class="header-container">
        <h2 class="header">{{ t('pg.history') }}</h2>
        <div class="header-actions">
          <label class="search-bar">
            <UiIcon name="search" :size="20" class="prefix-icon" />
            <input
              v-model="query"
              ms-input
              class="has-icon"
              type="text"
              :placeholder="t('pg.historySearch')"
              :aria-label="t('pg.historySearch')"
            />
            <button
              v-if="query"
              type="button"
              ms-button
              class="clear-button ms-button-borderless ms-button-icon"
              :aria-label="t('pg.clearSearch')"
              @click="query = ''"
            >
              <UiIcon name="close" :size="20" />
            </button>
          </label>
        </div>
      </div>
      <div v-if="!loaded" class="loading-state">
        <UiGradientSpinner />
        <div class="loading-text">{{ t('pg.historyLoading') }}</div>
      </div>
      <div v-else-if="prompts.length === 0" class="empty">
        <div class="empty-text">{{ t('pg.historyEmpty') }}</div>
        <RouterLink
          to="/prompts/new_chat"
          ms-button
          class="ms-button-primary"
          :aria-label="t('pg.newChatPromptLabel')"
        >
          <UiIcon name="add" :size="18" />
          {{ t('pg.newChatPrompt') }}
        </RouterLink>
      </div>
      <div v-else-if="handset" class="prompt-cards-container">
        <div
          v-for="item in pageRows"
          :key="item.id"
          role="button"
          tabindex="0"
          class="prompt-card"
          :aria-label="t('pg.openPrompt').replace('{title}', title(item))"
          @click="openPrompt(item)"
          @keydown.enter.self="openPrompt(item)"
        >
          <div class="card-header">
            <div class="card-name-info">
              <UiIcon name="chatBubble" :size="20" />
              <div class="card-name-column">
                <RouterLink
                  :to="`/prompts/${encodeURIComponent(item.id)}`"
                  class="name-link"
                  @click.stop
                >
                  {{ title(item) }}
                </RouterLink>
                <div class="sub-text subtitle-line">
                  <span>{{ t('pg.createdByYou') }}</span>
                  <template v-if="item.description">
                    <span class="subtitle-separator">•</span>
                    <span class="subtitle-description">{{ item.description }}</span>
                  </template>
                </div>
              </div>
            </div>
            <div class="card-actions" @click.stop @keydown.stop>
              <LibraryRowMenu
                @rename="renaming = item"
                @copy="copy(item)"
                @remove="pending = item"
              />
            </div>
          </div>
          <div class="card-details">
            <div class="card-detail-row">
              <span class="card-detail-label">{{ t('pg.historyUpdated') }}</span>
              <span class="card-detail-value">{{ relativeTime(item.updated_at) }}</span>
            </div>
          </div>
        </div>
        <LibraryPaginator
          v-if="pageRows.length > 0"
          :start="page * pageSize + 1"
          :end="page * pageSize + pageRows.length"
          :total="total"
          :previous="page > 0 && !loadingMore"
          :next="hasNext && !loadingMore"
          @previous="turn(-1)"
          @next="turn(1)"
        />
      </div>
      <div v-else class="lib-table-wrapper">
        <table class="library-table">
          <thead>
            <tr class="header-row">
              <th class="icon-cell"></th>
              <th class="table-header-cell sort-header" :aria-sort="ariaSort('name')">
                <div
                  role="button"
                  tabindex="0"
                  class="sort-header-container"
                  :class="{
                    sorted: sort.column === 'name',
                    descending: sort.column === 'name' && sort.descending,
                  }"
                  @click="toggleSort('name')"
                  @keydown.enter.prevent="toggleSort('name')"
                  @keydown.space.prevent="toggleSort('name')"
                >
                  {{ t('pg.historyName') }}
                  <span class="sort-arrow">
                    <svg viewBox="0 -960 960 960" focusable="false" aria-hidden="true">
                      <path
                        d="M440-240v-368L296-464l-56-56 240-240 240 240-56 56-144-144v368h-80Z"
                      />
                    </svg>
                  </span>
                </div>
              </th>
              <th
                class="table-header-cell sort-header updated-cell"
                :aria-sort="ariaSort('updated')"
              >
                <div
                  role="button"
                  tabindex="0"
                  class="sort-header-container"
                  :class="{
                    sorted: sort.column === 'updated',
                    descending: sort.column === 'updated' && sort.descending,
                  }"
                  @click="toggleSort('updated')"
                  @keydown.enter.prevent="toggleSort('updated')"
                  @keydown.space.prevent="toggleSort('updated')"
                >
                  {{ t('pg.historyUpdated') }}
                  <span class="sort-arrow">
                    <svg viewBox="0 -960 960 960" focusable="false" aria-hidden="true">
                      <path
                        d="M440-240v-368L296-464l-56-56 240-240 240 240-56 56-144-144v368h-80Z"
                      />
                    </svg>
                  </span>
                </div>
              </th>
              <th class="actions-cell"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in pageRows" :key="item.id" class="row">
              <td class="table-body-cell icon-cell" @click="openPrompt(item)">
                <UiIcon name="chatBubble" :size="20" />
              </td>
              <td class="table-body-cell name-cell" @click="openPrompt(item)">
                <RouterLink
                  :to="`/prompts/${encodeURIComponent(item.id)}`"
                  class="name-link"
                  @click.stop
                >
                  {{ title(item) }}
                </RouterLink>
                <div class="sub-text subtitle-line">
                  <span>{{ t('pg.createdByYou') }}</span>
                  <template v-if="item.description">
                    <span class="subtitle-separator">•</span>
                    <span class="subtitle-description">{{ item.description }}</span>
                  </template>
                </div>
              </td>
              <td
                v-tooltip="new Date(item.updated_at).toLocaleString(locale)"
                class="table-body-cell updated-cell sub-text"
                @click="openPrompt(item)"
              >
                {{ relativeTime(item.updated_at) }}
              </td>
              <td class="table-body-cell actions-cell">
                <LibraryRowMenu
                  @rename="renaming = item"
                  @copy="copy(item)"
                  @remove="pending = item"
                />
              </td>
            </tr>
          </tbody>
        </table>
        <LibraryPaginator
          v-if="pageRows.length > 0"
          :start="page * pageSize + 1"
          :end="page * pageSize + pageRows.length"
          :total="total"
          :previous="page > 0 && !loadingMore"
          :next="hasNext && !loadingMore"
          @previous="turn(-1)"
          @next="turn(1)"
        />
      </div>
      <div v-if="loaded && prompts.length > 0 && shown.length === 0 && !loadingMore" class="empty">
        <div class="empty-text">{{ t('pg.historyNoMatch') }}</div>
      </div>
      <div v-if="loadingMore" class="loading-next-batch">
        <UiGradientSpinner />
        <div class="loading-next-batch-text">{{ t('pg.historyLoadingMore') }}</div>
      </div>
    </div>
    <SavePromptDialog
      :open="renaming !== null"
      :title="renaming?.title ?? ''"
      :description="renaming?.description ?? ''"
      @update:open="(open) => !open && (renaming = null)"
      @save="rename"
    />
    <UiDialog
      :open="pending !== null"
      :title="t('pg.deleteTitle')"
      width="500px"
      :closable="false"
      @update:open="(open) => !open && (pending = null)"
    >
      <p class="confirm-text">{{ t('pg.deleteBody') }}</p>
      <template #actions>
        <button type="button" ms-button class="ms-button-borderless" @click="pending = null">
          {{ t('common.cancel') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="remove">
          {{ t('common.delete') }}
        </button>
      </template>
    </UiDialog>
  </div>
</template>

<style scoped>
.library-page {
  height: 100%;
  overflow-y: auto;
  padding: 0 24px;
}

.lib-view {
  display: flex;
  flex-direction: column;
  min-height: 100%;
  max-width: 960px;
  margin: 0 auto;
  padding-top: 60px;
}

.header-container {
  display: flex;
  flex-flow: row wrap;
  gap: 8px;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 32px;
}

.header {
  margin: 3px 0;
  font-family: var(--font-heading);
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
  white-space: nowrap;
}

.header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-left: auto;
}

.search-bar {
  position: relative;
  display: flex;
  align-items: center;
  width: 269px;
}

.prefix-icon {
  position: absolute;
  left: 12px;
  color: var(--color-v3-text-var);
  pointer-events: none;
}

.has-icon {
  padding: 1px 36px 1px 40px;
}

.clear-button {
  position: absolute;
  right: 0;
}

.lib-table-wrapper {
  flex-grow: 1;
  margin: 0 -24px;
}

.library-table {
  table-layout: fixed;
  width: 100%;
  border-collapse: collapse;
  border-spacing: 0;
}

.header-row {
  height: 40px;
  border-bottom: 1px solid var(--color-v3-outline-var);
}

.header-row th:first-child,
.row td:first-child {
  padding-left: 24px;
}

.header-row th:last-child,
.row td:last-child {
  padding-right: 24px;
}

.table-header-cell {
  color: var(--color-v3-text-var);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  text-align: start;
}

.sort-header {
  cursor: pointer;
}

.sort-header-container {
  display: flex;
  align-items: center;
  outline: 0;
}

.sort-arrow {
  position: relative;
  width: 12px;
  height: 12px;
  margin-left: 6px;
  overflow: visible;
  color: var(--mat-sys-on-surface);
  opacity: 0;
  transition:
    transform 225ms cubic-bezier(0.4, 0, 0.2, 1),
    opacity 225ms cubic-bezier(0.4, 0, 0.2, 1);
}

.sort-arrow svg {
  position: absolute;
  top: 50%;
  left: 50%;
  width: 24px;
  height: 24px;
  margin: -12px 0 0 -12px;
  fill: currentColor;
}

.sort-header:hover .sort-arrow,
.sort-header-container:focus-visible .sort-arrow {
  opacity: 0.54;
}

.sort-header .sorted .sort-arrow {
  opacity: 1;
}

.descending .sort-arrow {
  transform: rotate(180deg);
}

.table-body-cell {
  padding-top: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--color-v3-outline-var);
  font-size: 14px;
  line-height: 21px;
}

.sub-text {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
}

.row {
  cursor: pointer;
  transition: background-color 0.15s ease-in-out;
}

.row:hover {
  background: var(--color-v3-surface-container-highest);
}

.row:hover td:first-child {
  border-radius: 12px 0 0 12px;
}

.row:hover td:last-child {
  border-radius: 0 12px 12px 0;
}

.icon-cell {
  width: 58px;
  vertical-align: middle;
}

.name-cell {
  overflow: hidden;
  vertical-align: top;
}

.name-link {
  display: block;
  overflow: hidden;
  color: var(--color-v3-text);
  text-decoration: none;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.name-link:hover {
  text-decoration: underline;
}

.subtitle-line {
  display: flex;
  gap: 4px;
  align-items: baseline;
  margin-top: 2px;
  overflow: hidden;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subtitle-separator {
  flex-shrink: 0;
  opacity: 0.5;
}

.subtitle-description {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.updated-cell {
  width: 120px;
  white-space: nowrap;
}

.actions-cell {
  width: 58px;
  vertical-align: middle;
  cursor: default;
}

.empty {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  align-items: center;
  justify-content: center;
  padding: 24px 0;
}

.empty-text {
  font-size: 14px;
  line-height: 21px;
}

.loading-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding-top: 60px;
}

.loading-next-batch {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 12px 0;
}

.loading-text,
.loading-next-batch-text {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
}

.prompt-cards-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-bottom: 24px;
}

.prompt-card {
  padding: 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container-high);
  cursor: pointer;
}

.card-header {
  display: flex;
  gap: 8px;
  justify-content: space-between;
  align-items: center;
}

.card-name-info {
  display: flex;
  flex: 1;
  gap: 8px;
  align-items: center;
  min-width: 0;
}

.card-name-column {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.card-details {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--color-v3-outline-var);
}

.card-detail-row {
  display: flex;
  gap: 8px;
  justify-content: space-between;
  align-items: center;
}

.card-detail-label {
  flex-shrink: 0;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
}

.card-detail-value {
  font-size: 14px;
  line-height: 21px;
}

.confirm-text {
  margin: 0;
  line-height: 21px;
}

@media screen and (max-width: 600px) {
  .header-actions,
  .search-bar {
    flex: 1 0 100%;
    width: auto;
  }
}
</style>
