<script setup lang="ts">
import { computed, ref, useTemplateRef, watch } from 'vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { splitReason } from '@/cooldowns'
import { useI18n, type TranslationKey } from '@/i18n'
import {
  capabilityNames,
  categoryLabels,
  categoryOrder,
  modelBadges,
  modelFamily,
  outputLimit,
  tokenLimit,
} from '@/playground/catalog'
import type { Account, Cooldown, Model } from '@/types'
import CopyButton from './CopyButton.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  // account 是要查看模型的账户，为空时对话框关闭
  account: Account | undefined
  // catalog 是服务的实时模型目录，提供账户各模型的名称、上限与能力
  catalog: Model[]
  cooldowns: Cooldown[]
  // trigger 是打开对话框的按钮，关闭后焦点回到这里
  trigger: HTMLElement | null
}>()
const emit = defineEmits<{ close: [] }>()
const { t, locale, logText } = useI18n()
const query = ref('')
const search = useTemplateRef<HTMLInputElement>('search')
// selectedID 是右侧显示详情的模型
const selectedID = ref('')
// detailOpen 是窄屏下是否显示详情，宽屏下列表与详情并排
const detailOpen = ref(false)

// AccountModel 是账户的一个模型：目录中的模型信息、所属产品分类与该账户在这个模型上最晚结束的冷却
interface AccountModel {
  id: string
  model: Model | undefined
  family: number | undefined
  cooldown: Cooldown | undefined
}

// badgeLabels 是官网模型标记的文案
const badgeLabels: Record<'Confidential' | 'New' | 'Paid', TranslationKey> = {
  Confidential: 'pg.badgeConfidential',
  New: 'pg.badgeNew',
  Paid: 'pg.badgePaid',
}

// entries 是账户的全部模型，顺序与账户目录相同
const entries = computed<AccountModel[]>(() => {
  const account = props.account
  if (account === undefined) return []
  const models = new Map(props.catalog.map((model) => [model.id, model]))
  const latest = new Map<string, Cooldown>()
  for (const cooldown of props.cooldowns) {
    if (cooldown.account_id !== account.id) continue
    const current = latest.get(cooldown.model_id)
    if (current === undefined || Date.parse(cooldown.until) > Date.parse(current.until))
      latest.set(cooldown.model_id, cooldown)
  }
  return account.models.map((id) => {
    const model = models.get(id)
    return {
      id,
      model,
      family: model && modelFamily(model),
      cooldown: latest.get(id),
    }
  })
})

// familyName 返回产品分类的名称，不属于任何产品分类的模型归入其他
function familyName(family: number | undefined): string {
  return family === undefined ? t('accounts.modelsOther') : t(categoryLabels[family]!)
}

// groups 是按产品分类分组的搜索结果，模型 ID、名称或分类名包含搜索词即命中，不区分大小写
const groups = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const families = new Map<number | undefined, AccountModel[]>()
  for (const entry of entries.value) {
    const text = [entry.id, entry.model?.name ?? '', familyName(entry.family)].join('\n')
    if (!text.toLowerCase().includes(needle)) continue
    const items = families.get(entry.family)
    if (items) items.push(entry)
    else families.set(entry.family, [entry])
  }
  // rank 是分类在模型选择面板中的位置，其他排在最后
  const rank = (family: number | undefined) =>
    family === undefined ? Infinity : categoryOrder.indexOf(family)
  return [...families.entries()]
    .sort(([left], [right]) => rank(left) - rank(right))
    .map(([family, items]) => ({ name: familyName(family), items }))
})

// cooling 是账户上正在冷却的模型数
const cooling = computed(() => entries.value.filter((entry) => entry.cooldown).length)

// selected 是显示详情的模型，所选模型被搜索滤掉时为结果中的第一个
const selected = computed(() => {
  const visible = groups.value.flatMap((group) => group.items)
  return visible.find((entry) => entry.id === selectedID.value) ?? visible[0]
})

watch(
  () => props.account?.id,
  () => {
    query.value = ''
    selectedID.value = ''
    detailOpen.value = false
  },
)

// choose 显示一个模型的详情
function choose(entry: AccountModel): void {
  selectedID.value = entry.id
  detailOpen.value = true
}

// summary 是对话框标题下的一行：模型数、账户权益与冷却中的模型数
const summary = computed(() => {
  const account = props.account
  if (account === undefined) return ''
  const count = account.models.length
  return [
    count === 1 ? t('models.countOne') : t('models.count').replace('{count}', String(count)),
    account.benefit_tier === 'Unknown' ? t('accounts.tierUnknown') : account.benefit_tier,
    ...(cooling.value
      ? [t('accounts.modelsCooling').replace('{count}', String(cooling.value))]
      : []),
  ].join(' · ')
})

// facts 是详情中的上限与日期
const facts = computed(() => {
  const model = selected.value?.model
  const details = model?.details
  return [
    { label: t('models.context'), value: tokenLimit(model?.input_token_limit, locale.value) },
    { label: t('models.output'), value: tokenLimit(outputLimit(model), locale.value) },
    ...(details?.release_date
      ? [
          {
            label: t('models.releaseDate'),
            value: new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium' }).format(
              details.release_date * 1000,
            ),
          },
        ]
      : []),
    ...(details?.knowledge_cutoff
      ? [
          {
            label: t('models.knowledgeCutoff'),
            value: new Intl.DateTimeFormat(locale.value, {
              year: 'numeric',
              month: 'short',
            }).format(details.knowledge_cutoff * 1000),
          },
        ]
      : []),
  ]
})

// description 是官网模型卡片的简介，没有简介时为与名称不同的目录描述
const description = computed(() => {
  const model = selected.value?.model
  if (model === undefined) return ''
  if (model.details?.summary) return model.details.summary
  return model.description !== model.name ? (model.description ?? '') : ''
})

// badges 与 capabilities 是所选模型的官网标记与能力名称
const badges = computed(() =>
  selected.value?.model ? modelBadges(selected.value.model, 'All') : [],
)
const capabilities = computed(() =>
  selected.value?.model ? capabilityNames(selected.value.model, t) : [],
)

// reason 是所选模型当前冷却的上游错误原文
const reason = computed(() => splitReason(selected.value?.cooldown?.reason).text)

// cooldownCategory 返回冷却类型的界面文字
function cooldownCategory(cooldown: Cooldown): string {
  const category = splitReason(cooldown.reason).category
  return category ? logText(category) : t('cooldowns.other')
}

// untilText 返回冷却结束的本地时刻与距今的相对时间
function untilText(cooldown: Cooldown): string {
  const until = Date.parse(cooldown.until)
  const seconds = Math.max(1, Math.round((until - Date.now()) / 1000))
  const [value, unit] =
    seconds < 3600
      ? [Math.ceil(seconds / 60), 'minute' as const]
      : seconds < 86_400
        ? [Math.round(seconds / 3600), 'hour' as const]
        : [Math.round(seconds / 86_400), 'day' as const]
  const relative = new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' }).format(
    value,
    unit,
  )
  const time = new Intl.DateTimeFormat(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(until)
  return `${time} · ${relative}`
}

// restoreFocus 在对话框关闭后把焦点放回打开它的按钮
function restoreFocus(event: Event): void {
  event.preventDefault()
  props.trigger?.focus()
}
</script>

<template>
  <DialogRoot
    :open="account !== undefined"
    @update:open="(value: boolean) => !value && emit('close')"
  >
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[70] bg-black/60" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-[71] flex h-[min(40rem,85vh)] w-[calc(100vw-2rem)] max-w-3xl -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg border border-line bg-panel shadow-2xl focus:outline-none"
        @open-auto-focus.prevent="search?.focus()"
        @close-auto-focus="restoreFocus"
      >
        <header class="flex items-start gap-3 border-b border-line px-5 py-4">
          <div class="min-w-0 flex-1">
            <DialogTitle class="text-base font-semibold [overflow-wrap:anywhere] text-white">
              {{ account?.label }}
            </DialogTitle>
            <DialogDescription class="mt-1 text-xs text-gray-400 tabular-nums">
              {{ summary }}
            </DialogDescription>
          </div>
          <DialogClose
            v-tooltip="t('common.close')"
            class="shrink-0 rounded p-1 text-gray-500 hover:bg-raised hover:text-white"
            :aria-label="t('common.close')"
          >
            <UiIcon name="close" :size="16" />
          </DialogClose>
        </header>
        <div class="flex min-h-0 flex-1">
          <div
            class="flex min-w-0 flex-1 flex-col border-line sm:max-w-[19rem] sm:border-r"
            :class="{ 'max-sm:hidden': detailOpen }"
          >
            <div class="px-3 pt-3 pb-2">
              <div class="relative">
                <UiIcon
                  name="search"
                  :size="14"
                  class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-gray-500"
                />
                <input
                  ref="search"
                  v-model="query"
                  type="text"
                  :aria-label="t('accounts.searchModels')"
                  :placeholder="t('accounts.searchModels')"
                  class="w-full rounded-md border border-line bg-canvas py-1.5 pr-3 pl-8 text-sm text-white focus:border-blue-500 focus:outline-none"
                />
              </div>
            </div>
            <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-2 [scrollbar-gutter:stable]">
              <section v-for="group in groups" :key="group.name" class="mb-2">
                <h3
                  class="sticky top-0 z-10 flex items-center justify-between bg-panel px-2 py-1.5 text-xs font-medium text-gray-500"
                >
                  <span>{{ group.name }}</span>
                  <span class="tabular-nums">{{ group.items.length }}</span>
                </h3>
                <ul :aria-label="group.name">
                  <li v-for="entry in group.items" :key="entry.id">
                    <button
                      type="button"
                      class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left transition hover:bg-raised"
                      :class="{ 'bg-raised': selected?.id === entry.id }"
                      :aria-current="selected?.id === entry.id"
                      @click="choose(entry)"
                    >
                      <span class="min-w-0 flex-1">
                        <span
                          v-tooltip.overflow="entry.model?.name ?? entry.id"
                          class="block truncate text-sm text-gray-200"
                          >{{ entry.model?.name ?? entry.id }}</span
                        >
                        <code
                          v-if="entry.model"
                          v-tooltip.overflow="entry.id"
                          class="block truncate text-xs text-gray-500"
                          >{{ entry.id }}</code
                        >
                      </span>
                      <span
                        v-if="entry.cooldown"
                        class="shrink-0 rounded bg-yellow-500/10 px-1.5 py-0.5 text-xs text-yellow-300"
                        >{{ cooldownCategory(entry.cooldown) }}</span
                      >
                      <span
                        v-if="entry.model?.paid"
                        class="shrink-0 rounded border border-line px-1.5 py-0.5 text-xs text-gray-400"
                        >{{ t('pg.badgePaid') }}</span
                      >
                    </button>
                  </li>
                </ul>
              </section>
              <p v-if="groups.length === 0" class="px-3 py-6 text-center text-sm text-gray-500">
                {{ t('models.noMatches') }}
              </p>
            </div>
          </div>
          <div
            v-if="selected"
            class="min-w-0 flex-1 overflow-y-auto px-5 py-4 [scrollbar-gutter:stable]"
            :class="{ 'max-sm:hidden': !detailOpen }"
          >
            <button
              type="button"
              class="mb-3 flex items-center gap-1 text-xs text-gray-400 hover:text-gray-200 sm:hidden"
              @click="detailOpen = false"
            >
              <UiIcon name="chevronLeft" :size="14" />{{ t('accounts.modelsBack') }}
            </button>
            <div class="flex items-start gap-2">
              <div class="min-w-0 flex-1">
                <h3 class="text-sm font-semibold [overflow-wrap:anywhere] text-white">
                  {{ selected.model?.name ?? selected.id }}
                </h3>
                <code class="mt-0.5 block text-xs [overflow-wrap:anywhere] text-blue-400">{{
                  selected.id
                }}</code>
              </div>
              <CopyButton :text="selected.id" :label="t('accounts.modelsCopyId')" />
            </div>
            <div v-if="badges.length" class="mt-2 flex flex-wrap gap-1.5">
              <span
                v-for="badge in badges"
                :key="badge"
                class="rounded border border-line px-1.5 py-0.5 text-xs text-gray-300"
                >{{ t(badgeLabels[badge]) }}</span
              >
            </div>
            <p
              v-if="description"
              class="mt-3 text-xs leading-5 [overflow-wrap:anywhere] text-gray-400"
            >
              {{ description }}
            </p>
            <dl class="mt-4 grid grid-cols-2 gap-3 text-xs">
              <div v-for="fact in facts" :key="fact.label">
                <dt class="text-gray-500">{{ fact.label }}</dt>
                <dd class="mt-0.5 text-sm text-gray-200 tabular-nums">{{ fact.value }}</dd>
              </div>
            </dl>
            <section class="mt-4">
              <h4 class="mb-2 text-xs text-gray-500">{{ t('models.capabilities') }}</h4>
              <div class="flex flex-wrap gap-1.5">
                <span
                  v-for="capability in capabilities"
                  :key="capability"
                  class="rounded border border-line bg-canvas px-2 py-0.5 text-xs text-gray-300"
                  >{{ capability }}</span
                >
                <span v-if="capabilities.length === 0" class="text-xs text-gray-600">—</span>
              </div>
            </section>
            <section class="mt-4">
              <h4 class="mb-2 text-xs text-gray-500">{{ t('accounts.modelsCooldown') }}</h4>
              <div
                v-if="selected.cooldown"
                class="rounded-md border border-yellow-500/30 bg-yellow-500/5 p-3 text-xs"
              >
                <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span class="font-medium text-yellow-300">{{
                    cooldownCategory(selected.cooldown)
                  }}</span>
                  <span class="text-gray-300 tabular-nums">{{ untilText(selected.cooldown) }}</span>
                </div>
                <p
                  v-if="reason"
                  class="mt-2 leading-5 whitespace-pre-wrap [overflow-wrap:anywhere] text-gray-400"
                >
                  {{ logText(reason) }}
                </p>
              </div>
              <p v-else class="text-xs text-gray-500">{{ t('accounts.modelsNoCooldown') }}</p>
            </section>
          </div>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
