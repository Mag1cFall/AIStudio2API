import type { Directive } from 'vue'

let tip: HTMLDivElement | null = null
let timer = 0
// shown 是当前显示提示的元素
let shown: HTMLElement | null = null

// tooltipElement 返回全局共用的提示浮层
function tooltipElement(): HTMLDivElement {
  if (!tip) {
    tip = document.createElement('div')
    tip.className = 'ui-tooltip'
    tip.setAttribute('role', 'tooltip')
    document.body.appendChild(tip)
  }
  return tip
}

function show(target: HTMLElement): void {
  if (!target.dataset.tooltip) return
  if (target.dataset.tooltipOverflow !== undefined && target.scrollWidth <= target.clientWidth)
    return
  window.clearTimeout(timer)
  timer = window.setTimeout(() => {
    const text = target.dataset.tooltip
    if (!text) return
    const element = tooltipElement()
    element.textContent = text
    element.classList.add('visible')
    shown = target
    const rect = target.getBoundingClientRect()
    const width = element.offsetWidth
    const left = Math.min(
      Math.max(8, rect.left + rect.width / 2 - width / 2),
      window.innerWidth - width - 8,
    )
    const above = rect.top - element.offsetHeight - 6
    element.style.left = `${left}px`
    element.style.top = `${above > 8 ? above : rect.bottom + 6}px`
  }, 300)
}

function hide(): void {
  window.clearTimeout(timer)
  tip?.classList.remove('visible')
  shown = null
}

// vTooltip 以站内样式显示悬停与键盘聚焦提示，值为空时不显示；overflow 修饰符只在文字被截断时显示
export const vTooltip: Directive<HTMLElement, string | undefined, 'overflow'> = {
  mounted(element, binding) {
    element.dataset.tooltip = binding.value ?? ''
    if (binding.modifiers.overflow) element.dataset.tooltipOverflow = ''
    element.addEventListener('pointerenter', () => show(element))
    element.addEventListener('pointerleave', hide)
    element.addEventListener('focus', () => show(element))
    element.addEventListener('blur', hide)
    element.addEventListener('pointerdown', hide)
  },
  updated(element, binding) {
    element.dataset.tooltip = binding.value ?? ''
    if (shown !== element || !tip) return
    if (element.dataset.tooltip) tip.textContent = element.dataset.tooltip
    else hide()
  },
  beforeUnmount: hide,
}
