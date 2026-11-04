import { computed, ref, watch, type Ref } from 'vue'

// usePaged 把列表按 size 条分页，page 从 1 开始；列表变短使当前页越界时回到最后一页
export function usePaged<T>(list: Readonly<Ref<readonly T[]>>, size: number) {
  const page = ref(1)
  const pages = computed(() => Math.max(1, Math.ceil(list.value.length / size)))
  watch(pages, (count) => {
    if (page.value > count) page.value = count
  })
  const paged = computed(() => list.value.slice((page.value - 1) * size, page.value * size))
  return { page, pages, paged }
}
