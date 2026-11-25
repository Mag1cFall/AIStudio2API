<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import type { Account } from '@/types'
import FilterMenu from './FilterMenu.vue'
import { tierBadgeClass } from './ImportFilterBar.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  accounts: Account[]
  selected: string[]
  disabled?: boolean
  // field 表示触发器使用表单下拉框的样式
  field?: boolean
}>()
const emit = defineEmits<{ change: [ids: string[]] }>()

const { t } = useI18n()

// byID 是账户 ID 到账户的映射
const byID = computed(() => new Map(props.accounts.map((account) => [account.id, account])))

// label 返回账户 ID 对应的显示名称
function label(id: string): string {
  return byID.value.get(id)?.label ?? id
}

// tier 返回账户已知的权益等级，未知时为空串
function tier(id: string): string {
  const value = byID.value.get(id)?.benefit_tier ?? ''
  return value === 'Unknown' ? '' : value
}

// note 返回账户备注
function note(id: string): string {
  return byID.value.get(id)?.note ?? ''
}

// keywords 是搜索时匹配的账户名称、权益与备注
function keywords(id: string): string {
  return [label(id), tier(id), note(id)].join(' ')
}

// triggerLabel 是按钮文字：未选时为全部账户，选中时为第一个账户
const triggerLabel = computed(() =>
  props.selected.length === 0 ? t('settings.keyAccountsAll') : label(props.selected[0]!),
)

// triggerBadge 是按钮上其余已选账户的数目，只选一个时不显示
const triggerBadge = computed(() =>
  props.selected.length > 1 ? `+${props.selected.length - 1}` : '',
)
</script>

<!-- AccountFilterMenu 是多选账户的下拉，选项显示账户名称、权益与备注，可按三者搜索 -->
<template>
  <FilterMenu
    v-bind="$attrs"
    :field="field"
    :label="triggerLabel"
    :badge="triggerBadge"
    :options="accounts.map((account) => account.id)"
    :selected="selected"
    :format="keywords"
    :search-label="t('settings.accountSearch')"
    :empty-label="t('settings.accountNone')"
    :clear-label="t('settings.keyAccountsClear')"
    :disabled="disabled"
    @change="emit('change', $event)"
  >
    <template #option="{ value }">
      <span class="min-w-0 flex-1">
        <span class="flex min-w-0 items-center gap-1.5">
          <span v-tooltip.overflow="label(value)" class="min-w-0 truncate">{{ label(value) }}</span>
          <span
            v-if="tier(value)"
            class="shrink-0 rounded border px-1.5 text-xs font-medium"
            :class="tierBadgeClass(tier(value))"
            >{{ tier(value) }}</span
          >
        </span>
        <span
          v-if="note(value)"
          v-tooltip.overflow="note(value)"
          class="mt-0.5 block truncate text-gray-500"
          >{{ note(value) }}</span
        >
      </span>
    </template>
  </FilterMenu>
</template>
