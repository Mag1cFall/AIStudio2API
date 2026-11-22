<script setup lang="ts">
import { CalendarDate, Time } from '@internationalized/date'
import { computed, ref, shallowRef } from 'vue'
import {
  PopoverContent,
  PopoverPortal,
  PopoverRoot,
  PopoverTrigger,
  RangeCalendarCell,
  RangeCalendarCellTrigger,
  RangeCalendarGrid,
  RangeCalendarGridBody,
  RangeCalendarGridHead,
  RangeCalendarGridRow,
  RangeCalendarHeadCell,
  RangeCalendarHeader,
  RangeCalendarHeading,
  RangeCalendarNext,
  RangeCalendarPrev,
  RangeCalendarRoot,
  TimeFieldInput,
  TimeFieldRoot,
  type DateRange,
  type DateValue,
  type TimeValue,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from '../UiIcon.vue'
import { useUsageFormat } from './format'
import {
  calendarPresets,
  liveRange,
  quotaDayStart,
  relativePresets,
  retention,
  validRange,
  type RangePreset,
} from './model'

interface DaySlotState {
  today: boolean
  selected: boolean
  selectionStart: boolean
  selectionEnd: boolean
  highlighted: boolean
  highlightedStart: boolean
  highlightedEnd: boolean
  outsideView: boolean
}

const props = defineProps<{
  range: RangePreset
  resolved: { from: Date; to: Date } | null
}>()
const emit = defineEmits<{ apply: [range: RangePreset, from: string, to: string] }>()

const { t, locale } = useI18n()
const { dateTime } = useUsageFormat()
const open = ref(false)
const now = ref(new Date())
const timeEdited = ref(false)

// toCalendarDate 把浏览器本地 Date 转为日历日期对象
function toCalendarDate(date: Date): CalendarDate {
  return new CalendarDate(date.getFullYear(), date.getMonth() + 1, date.getDate())
}

// toTime 把浏览器本地 Date 转为精确到分钟的时间对象
function toTime(date: Date): Time {
  return new Time(date.getHours(), date.getMinutes())
}

// combineLocal 把日历日期与时间组合为浏览器本地 Date
function combineLocal(date: DateValue, time: TimeValue): Date {
  return new Date(date.year, date.month - 1, date.day, time.hour, time.minute)
}

// formatIsoDate 把日历日期格式化为 YYYY-MM-DD
function formatIsoDate(date: DateValue): string {
  const pad = (value: number): string => String(value).padStart(2, '0')
  return `${date.year}-${pad(date.month)}-${pad(date.day)}`
}

const selectedDates = shallowRef<DateRange>({ start: undefined, end: undefined })
const calendarMonth = shallowRef<DateValue>(toCalendarDate(now.value))
const startTime = shallowRef<TimeValue | null>(null)
const endTime = shallowRef<TimeValue | null>(null)

const summary = computed(() =>
  props.resolved === null
    ? ''
    : `${dateTime(props.resolved.from)} – ${dateTime(props.resolved.to)}`,
)

// localClock 把时刻格式化为浏览器本地的 24 小时制“时:分”
function localClock(date: Date): string {
  const pad = (value: number): string => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// quotaStart 是当前配额日（最近一次太平洋时间零点）对应的本地起始时刻
const quotaStart = computed(() =>
  localClock(
    props.range === 'quotaDay' && props.resolved !== null
      ? props.resolved.from
      : quotaDayStart(
          liveRange(props.range) && props.resolved !== null ? props.resolved.to : now.value,
        ),
  ),
)

// presetLabel 返回时间范围选项的显示文字，配额日带上本地起始时间
function presetLabel(preset: RangePreset): string {
  if (preset === 'quotaDay') return t('usage.range.quotaDay').replace('{time}', quotaStart.value)
  return t(`usage.range.${preset}`)
}

// weekdayFormat 为中文提供单字星期表头，其余语言使用简写以区分重名星期
const weekdayFormat = computed<'narrow' | 'short'>(() =>
  locale.value.startsWith('zh') ? 'narrow' : 'short',
)

// retentionFloor 是按分钟对齐的 90 天保留起点，与近 90 天预设保持一致
const retentionFloor = computed(() => Math.floor(now.value.getTime() / 60_000) * 60_000 - retention)
const retentionDay = computed(() => toCalendarDate(new Date(retentionFloor.value)))

// isBeforeRetentionDay 判断日历日期是否早于 90 天记录保留期所在日期
function isBeforeRetentionDay(day: DateValue): boolean {
  return day.compare(retentionDay.value) < 0
}

const selectingEnd = computed(
  () => selectedDates.value.start !== undefined && selectedDates.value.end === undefined,
)

const draftRange = computed<{ from: Date; to: Date } | null>(() => {
  const { start, end } = selectedDates.value
  if (start === undefined || end === undefined) return null
  if (startTime.value === null || endTime.value === null) return null
  return {
    from: combineLocal(start, startTime.value),
    to: combineLocal(end, endTime.value),
  }
})

const customError = computed(() => {
  const draft = draftRange.value
  if (draft === null) return ''
  const error = validRange(draft.from, draft.to)
  if (error === 'usage.rangeInvalid') return t(error)
  if (draft.from.getTime() < retentionFloor.value) return t('usage.rangeRetention')
  return error === '' ? '' : t(error)
})

const canApply = computed(() => draftRange.value !== null && customError.value === '')

// prepare 打开浮层时用当前范围初始化日历与起止时间
function prepare(value: boolean): void {
  open.value = value
  if (!value) return
  now.value = new Date()
  const range = props.resolved ?? {
    from: new Date(now.value.getTime() - 86_400_000),
    to: now.value,
  }
  selectedDates.value = {
    start: toCalendarDate(range.from),
    end: toCalendarDate(range.to),
  }
  calendarMonth.value = toCalendarDate(range.to)
  startTime.value = toTime(range.from)
  endTime.value = toTime(range.to)
  timeEdited.value = false
}

// updateDates 同步日历选区；未手动修改时间且起止时刻相同时，单日选区默认覆盖全天
function updateDates(value: DateRange): void {
  selectedDates.value = value
  if (
    !timeEdited.value &&
    value.start !== undefined &&
    value.end !== undefined &&
    value.start.compare(value.end) === 0 &&
    startTime.value !== null &&
    endTime.value !== null &&
    startTime.value.hour === endTime.value.hour &&
    startTime.value.minute === endTime.value.minute
  ) {
    startTime.value = new Time(0, 0)
    endTime.value = new Time(23, 59)
  }
}

// updateStartTime 更新开始时间并标记时间已手动编辑
function updateStartTime(value: TimeValue | undefined): void {
  timeEdited.value = true
  startTime.value = value ?? null
}

// updateEndTime 更新结束时间并标记时间已手动编辑
function updateEndTime(value: TimeValue | undefined): void {
  timeEdited.value = true
  endTime.value = value ?? null
}

// focusMonth 把日历视图跳转到指定日期所在月份
function focusMonth(date: DateValue): void {
  calendarMonth.value = date.copy()
}

// dayClasses 返回日历单日按钮在选区、悬停预览、今日与保留期外的样式类
function dayClasses(day: DateValue, state: DaySlotState): string[] {
  const classes: string[] = []
  const endpoint = state.selectionStart || state.selectionEnd
  const previewEdge =
    !endpoint && state.highlighted && (state.highlightedStart || state.highlightedEnd)
  const inBand = !endpoint && !previewEdge && (state.selected || state.highlighted)

  if (endpoint) {
    classes.push('bg-blue-600', 'font-medium', 'text-white')
    if (state.selectionStart && state.selectionEnd) {
      classes.push('rounded-md')
    } else if (state.selectionStart) {
      if (
        selectingEnd.value &&
        (!state.highlighted || (state.highlightedStart && state.highlightedEnd))
      )
        classes.push('rounded-md')
      else if (selectingEnd.value && state.highlightedEnd) classes.push('rounded-r-md')
      else classes.push('rounded-l-md')
    } else {
      classes.push('rounded-r-md')
    }
  } else if (previewEdge) {
    classes.push('bg-blue-600/25', 'text-blue-300', 'ring-1', 'ring-inset', 'ring-blue-500/60')
    classes.push(state.highlightedStart ? 'rounded-l-md' : 'rounded-r-md')
  } else if (inBand) {
    classes.push('rounded-none', 'bg-blue-600/20', 'text-blue-300', 'hover:bg-blue-600/30')
  } else {
    classes.push('rounded-md', 'text-gray-200', 'hover:bg-raised', 'hover:text-white')
  }

  if (state.outsideView) classes.push('opacity-40')
  else if (!endpoint && !inBand && !previewEdge && isBeforeRetentionDay(day))
    classes.push('opacity-45')

  if (state.today) {
    classes.push(
      'relative',
      'after:pointer-events-none',
      'after:absolute',
      'after:bottom-[2px]',
      'after:left-1/2',
      'after:h-[3px]',
      'after:w-[3px]',
      'after:-translate-x-1/2',
      'after:rounded-full',
      endpoint ? 'after:bg-current' : 'after:bg-blue-400',
    )
  }

  return classes
}

// choose 应用快捷范围并关闭浮层
function choose(range: RangePreset): void {
  emit('apply', range, '', '')
  open.value = false
}

// applyCustom 应用校验通过的自定义范围
function applyCustom(): void {
  const draft = draftRange.value
  if (draft === null || customError.value !== '') return
  emit('apply', 'custom', draft.from.toISOString(), draft.to.toISOString())
  open.value = false
}
</script>

<template>
  <PopoverRoot :open="open" @update:open="prepare">
    <PopoverTrigger
      class="flex min-w-0 items-center gap-2 rounded-md border border-line bg-canvas px-3 py-1 text-left text-sm text-gray-200 transition-colors hover:border-gray-600 data-[state=open]:border-blue-500"
      :aria-label="`${t('usage.range')}: ${presetLabel(range)} ${summary}`"
    >
      <UiIcon name="calendar" :size="14" class="text-gray-400" />
      <span class="font-medium whitespace-nowrap">{{ presetLabel(range) }}</span>
      <span
        v-tooltip.overflow="summary"
        class="hidden truncate text-xs text-gray-500 tabular-nums sm:inline"
        >{{ summary }}</span
      >
      <UiIcon name="chevronDown" :size="12" class="text-gray-500" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="start"
        :side-offset="6"
        :collision-padding="12"
        class="ui-popover z-[60] max-h-(--reka-popover-content-available-height) w-[min(36rem,calc(100vw-24px))] overflow-y-auto rounded-lg border border-line bg-panel p-3 text-sm shadow-xl shadow-black/40"
      >
        <div class="grid gap-4 sm:grid-cols-[1fr_1.1fr]">
          <div class="space-y-3">
            <section
              v-for="group in [
                { label: 'usage.rangeRecent' as const, items: relativePresets },
                { label: 'usage.rangeCalendar' as const, items: calendarPresets },
              ]"
              :key="group.label"
            >
              <h4 class="mb-1.5 text-xs font-medium tracking-wide text-gray-500 uppercase">
                {{ t(group.label) }}
              </h4>
              <div class="grid grid-cols-2 gap-1">
                <button
                  v-for="item in group.items"
                  :key="item"
                  v-tooltip="item === 'quotaDay' ? t('usage.range.quotaDayHelp') : undefined"
                  type="button"
                  :aria-pressed="range === item"
                  class="rounded px-2 py-1.5 text-left text-xs transition-colors"
                  :class="
                    range === item
                      ? 'bg-blue-600/20 text-blue-300'
                      : 'text-gray-300 hover:bg-raised hover:text-white'
                  "
                  @click="choose(item)"
                >
                  {{ presetLabel(item) }}
                </button>
              </div>
            </section>
          </div>
          <form class="space-y-2.5 border-line sm:border-l sm:pl-4" @submit.prevent="applyCustom">
            <h4 class="text-xs font-medium tracking-wide text-gray-500 uppercase">
              {{ t('usage.rangeCustom') }}
            </h4>
            <RangeCalendarRoot
              v-slot="{ grid, weekDays }"
              :model-value="selectedDates"
              :placeholder="calendarMonth"
              :locale="locale"
              :weekday-format="weekdayFormat"
              :calendar-label="t('usage.rangeCustom')"
              :fixed-weeks="true"
              :prevent-deselect="true"
              class="select-none"
              @update:model-value="updateDates"
              @update:placeholder="(value) => (calendarMonth = value)"
            >
              <RangeCalendarHeader class="mb-1.5 flex items-center justify-between">
                <RangeCalendarPrev
                  v-tooltip="t('usage.rangePrevMonth')"
                  :aria-label="t('usage.rangePrevMonth')"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-raised hover:text-white disabled:opacity-40"
                >
                  <UiIcon name="chevronLeft" :size="14" />
                </RangeCalendarPrev>
                <RangeCalendarHeading class="text-xs font-medium text-gray-200 tabular-nums" />
                <RangeCalendarNext
                  v-tooltip="t('usage.rangeNextMonth')"
                  :aria-label="t('usage.rangeNextMonth')"
                  class="inline-flex h-7 w-7 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-raised hover:text-white disabled:opacity-40"
                >
                  <UiIcon name="chevronRight" :size="14" />
                </RangeCalendarNext>
              </RangeCalendarHeader>
              <RangeCalendarGrid
                v-for="month in grid"
                :key="month.value.toString()"
                class="w-full table-fixed border-collapse"
              >
                <RangeCalendarGridHead>
                  <RangeCalendarGridRow>
                    <RangeCalendarHeadCell
                      v-for="day in weekDays"
                      :key="day"
                      class="pb-1 text-center text-xs font-normal text-gray-500"
                    >
                      {{ day }}
                    </RangeCalendarHeadCell>
                  </RangeCalendarGridRow>
                </RangeCalendarGridHead>
                <RangeCalendarGridBody>
                  <RangeCalendarGridRow v-for="(weekDates, rowIndex) in month.rows" :key="rowIndex">
                    <RangeCalendarCell
                      v-for="day in weekDates"
                      :key="day.toString()"
                      :date="day"
                      class="p-0 py-0.5 text-center"
                    >
                      <RangeCalendarCellTrigger
                        v-slot="slotProps"
                        :day="day"
                        :month="month.value"
                        class="block w-full cursor-pointer rounded-md"
                      >
                        <span
                          class="flex h-7 w-full items-center justify-center text-xs tabular-nums transition-colors"
                          :class="dayClasses(day, slotProps)"
                        >
                          {{ slotProps.dayValue }}
                        </span>
                      </RangeCalendarCellTrigger>
                    </RangeCalendarCell>
                  </RangeCalendarGridRow>
                </RangeCalendarGridBody>
              </RangeCalendarGrid>
            </RangeCalendarRoot>
            <div
              class="grid grid-cols-[auto_1fr_auto] items-center gap-x-2 gap-y-1.5 border-t border-line pt-2.5"
            >
              <label for="usage-range-start-time" class="text-xs text-gray-400">
                {{ t('usage.rangeStart') }}
              </label>
              <button
                v-if="selectedDates.start"
                type="button"
                class="justify-self-start rounded px-1.5 py-0.5 text-xs text-gray-200 tabular-nums transition-colors hover:bg-raised hover:text-white"
                @click="focusMonth(selectedDates.start)"
              >
                {{ formatIsoDate(selectedDates.start) }}
              </button>
              <span v-else class="px-1.5 py-0.5 text-xs text-gray-500">—</span>
              <TimeFieldRoot
                id="usage-range-start-time"
                v-slot="{ segments }"
                :model-value="startTime"
                :hour-cycle="24"
                granularity="minute"
                :locale="locale"
                :aria-label="t('usage.rangeStart')"
                class="inline-flex items-center rounded-md border bg-canvas px-1.5 py-0.5 text-xs text-gray-200 tabular-nums transition-colors focus-within:border-blue-500"
                :class="customError !== '' ? 'border-red-500/60' : 'border-line'"
                @update:model-value="updateStartTime"
              >
                <template v-for="(item, index) in segments" :key="`${item.part}-${index}`">
                  <TimeFieldInput
                    v-if="item.part === 'literal'"
                    :part="item.part"
                    class="px-0.5 text-gray-500 select-none"
                  >
                    {{ item.value }}
                  </TimeFieldInput>
                  <TimeFieldInput
                    v-else
                    :part="item.part"
                    class="min-w-[1.5rem] rounded px-1 py-0.5 text-center outline-none focus:bg-blue-600/20 focus:text-blue-300 data-[placeholder]:text-gray-500"
                  >
                    {{ item.value }}
                  </TimeFieldInput>
                </template>
              </TimeFieldRoot>

              <label for="usage-range-end-time" class="text-xs text-gray-400">
                {{ t('usage.rangeEnd') }}
              </label>
              <button
                v-if="selectedDates.end"
                type="button"
                class="justify-self-start rounded px-1.5 py-0.5 text-xs text-gray-200 tabular-nums transition-colors hover:bg-raised hover:text-white"
                @click="focusMonth(selectedDates.end)"
              >
                {{ formatIsoDate(selectedDates.end) }}
              </button>
              <span v-else class="px-1.5 py-0.5 text-xs text-gray-500">—</span>
              <TimeFieldRoot
                id="usage-range-end-time"
                v-slot="{ segments }"
                :model-value="endTime"
                :hour-cycle="24"
                granularity="minute"
                :locale="locale"
                :aria-label="t('usage.rangeEnd')"
                class="inline-flex items-center rounded-md border bg-canvas px-1.5 py-0.5 text-xs text-gray-200 tabular-nums transition-colors focus-within:border-blue-500"
                :class="customError !== '' ? 'border-red-500/60' : 'border-line'"
                @update:model-value="updateEndTime"
              >
                <template v-for="(item, index) in segments" :key="`${item.part}-${index}`">
                  <TimeFieldInput
                    v-if="item.part === 'literal'"
                    :part="item.part"
                    class="px-0.5 text-gray-500 select-none"
                  >
                    {{ item.value }}
                  </TimeFieldInput>
                  <TimeFieldInput
                    v-else
                    :part="item.part"
                    class="min-w-[1.5rem] rounded px-1 py-0.5 text-center outline-none focus:bg-blue-600/20 focus:text-blue-300 data-[placeholder]:text-gray-500"
                  >
                    {{ item.value }}
                  </TimeFieldInput>
                </template>
              </TimeFieldRoot>
            </div>
            <p v-if="customError !== ''" class="text-xs text-red-400" role="alert">
              {{ customError }}
            </p>
            <p v-else-if="selectingEnd" class="text-xs text-gray-400">
              {{ t('usage.rangeSelectEnd') }}
            </p>
            <button
              type="submit"
              :disabled="!canApply"
              class="w-full rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
            >
              {{ t('usage.rangeApply') }}
            </button>
          </form>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
