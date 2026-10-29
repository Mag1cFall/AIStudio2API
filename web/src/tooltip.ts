import type { Directive } from 'vue'

// TooltipModifier 是提示指令的修饰符：overflow 只在文字被截断时显示，其余为显示位置，默认在上方
type TooltipModifier = 'overflow' | 'below' | 'left' | 'right'

let tip: HTMLDivElement | null = null
let timer = 0
// shown 是当前显示提示的元素
let shown: HTMLElement | null = null
// pending 是等待显示提示的元素
let pending: HTMLElement | null = null

// tooltipElement 返回全局共用的提示浮层
function tooltipElement(): HTMLDivElement {
  if (!tip) {
    tip = document.createElement('div')
    tip.setAttribute('role', 'tooltip')
    document.body.appendChild(tip)
  }
  return tip
}

// place 按指定位置与间距放置提示，超出视口时换到相反一侧并限制在视口内
function place(element: HTMLDivElement, rect: DOMRect, position: string, gap: number): void {
  const width = element.offsetWidth
  const height = element.offsetHeight
  const clampX = (value: number) => Math.min(Math.max(8, value), window.innerWidth - width - 8)
  const clampY = (value: number) => Math.min(Math.max(8, value), window.innerHeight - height - 8)
  let left: number
  let top: number
  if (position === 'left' || position === 'right') {
    const before = rect.left - width - gap
    const after = rect.right + gap
    left =
      position === 'left'
        ? before >= 8
          ? before
          : after
        : after + width <= window.innerWidth - 8
          ? after
          : before
    top = clampY(rect.top + rect.height / 2 - height / 2)
  } else {
    const above = rect.top - height - gap
    const below = rect.bottom + gap
    top =
      position === 'below'
        ? below + height <= window.innerHeight - 8
          ? below
          : above
        : above >= 8
          ? above
          : below
    left = clampX(rect.left + rect.width / 2 - width / 2)
  }
  element.style.left = `${left}px`
  element.style.top = `${top}px`
}

// showTooltip 在目标元素旁显示其 data-tooltip 文字，API 试用页用官网样式，管理端用站内样式
export function showTooltip(target: HTMLElement): void {
  if (!target.dataset.tooltip) return
  if (target.dataset.tooltipOverflow !== undefined && target.scrollWidth <= target.clientWidth)
    return
  window.clearTimeout(timer)
  pending = target
  timer = window.setTimeout(() => {
    pending = null
    const text = target.dataset.tooltip
    if (!text) return
    const studio = document.documentElement.classList.contains('studio')
    const element = tooltipElement()
    element.className = studio ? 'ms-tooltip visible' : 'ui-tooltip visible'
    element.textContent = text
    shown = target
    place(
      element,
      target.getBoundingClientRect(),
      target.dataset.tooltipPosition ?? '',
      studio ? 8 : 6,
    )
  }, 300)
}

// hideTooltip 隐藏提示
function hideTooltip(): void {
  window.clearTimeout(timer)
  tip?.classList.remove('visible')
  shown = null
  pending = null
}

// vTooltip 显示悬停与键盘聚焦提示，值为空时不显示
export const vTooltip: Directive<HTMLElement, string | undefined, TooltipModifier> = {
  mounted(element, binding) {
    element.dataset.tooltip = binding.value ?? ''
    if (binding.modifiers.overflow) element.dataset.tooltipOverflow = ''
    const position = (['below', 'left', 'right'] as const).find((name) => binding.modifiers[name])
    if (position) element.dataset.tooltipPosition = position
    element.addEventListener('pointerenter', () => showTooltip(element))
    element.addEventListener('pointerleave', hideTooltip)
    element.addEventListener('focus', () => showTooltip(element))
    element.addEventListener('blur', hideTooltip)
    element.addEventListener('pointerdown', hideTooltip)
  },
  updated(element, binding) {
    element.dataset.tooltip = binding.value ?? ''
    if (shown !== element || !tip) return
    if (element.dataset.tooltip) tip.textContent = element.dataset.tooltip
    else hideTooltip()
  },
  beforeUnmount(element) {
    if (element === shown || element === pending) hideTooltip()
  },
}
