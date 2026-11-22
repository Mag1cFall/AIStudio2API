import { computed, ref, type Ref } from 'vue'

// ImportFilterKind 是导入对话框候选列表的筛选分段
export type ImportFilterKind = 'all' | 'unimported' | 'pro'

// ImportCandidateOptions 描述一种导入入口如何检索、归类与勾选候选项
export interface ImportCandidateOptions<T> {
  // key 返回候选项的唯一标识
  key: (item: T) => string
  // matches 判断候选项是否匹配已转为小写并去空白的搜索关键词
  matches: (item: T, needle: string) => boolean
  // imported 表示候选项是否已导入账户池
  imported: (item: T) => boolean
  // pro 表示候选项是否属于 Pro+ 权益分段
  pro: (item: T) => boolean
  // selectable 表示候选项当前能否勾选导入
  selectable: (item: T) => boolean
}

// useImportCandidates 维护导入对话框的搜索、分段筛选、分段计数与仅作用于当前可见项的全选逻辑
export function useImportCandidates<T>(
  items: Readonly<Ref<readonly T[]>>,
  selected: Ref<string[]>,
  options: ImportCandidateOptions<T>,
) {
  const search = ref('')
  const filter = ref<ImportFilterKind>('all')

  const searched = computed(() => {
    const needle = search.value.trim().toLowerCase()
    if (needle === '') return items.value
    return items.value.filter((item) => options.matches(item, needle))
  })

  const counts = computed(() => {
    let unimported = 0
    let pro = 0
    for (const item of searched.value) {
      if (!options.imported(item)) unimported++
      if (options.pro(item)) pro++
    }
    return { all: searched.value.length, unimported, pro }
  })

  const filtered = computed(() => {
    if (filter.value === 'unimported') {
      return searched.value.filter((item) => !options.imported(item))
    }
    if (filter.value === 'pro') {
      return searched.value.filter((item) => options.pro(item))
    }
    return searched.value
  })

  const visibleSelectable = computed(() => filtered.value.filter(options.selectable))

  const allSelected = computed(() => {
    if (visibleSelectable.value.length === 0) return false
    const chosen = new Set(selected.value)
    return visibleSelectable.value.every((item) => chosen.has(options.key(item)))
  })

  // toggleAll 只勾选或取消当前筛选结果中可选的候选项，保留已被筛选隐藏的已选项
  function toggleAll(): void {
    const visibleKeys = visibleSelectable.value.map(options.key)
    const visibleSet = new Set(visibleKeys)
    if (allSelected.value) {
      selected.value = selected.value.filter((key) => !visibleSet.has(key))
    } else {
      selected.value = [...new Set([...selected.value, ...visibleKeys])]
    }
  }

  // reset 清空搜索词与分段筛选
  function reset(): void {
    search.value = ''
    filter.value = 'all'
  }

  return { search, filter, counts, filtered, visibleSelectable, allSelected, toggleAll, reset }
}
