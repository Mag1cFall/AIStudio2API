import type { Directive } from 'vue'

let tip: HTMLDivElement | null = null
let timer = 0

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
  const text = target.dataset.tooltip
  if (!text) return
  window.clearTimeout(timer)
  timer = window.setTimeout(() => {
    const element = tooltipElement()
    element.textContent = text
    element.classList.add('visible')
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
}

// vTooltip 以站内样式显示悬停与键盘聚焦提示，值为空时不显示
export const vTooltip: Directive<HTMLElement, string | undefined> = {
  mounted(element, binding) {
    element.dataset.tooltip = binding.value ?? ''
    element.addEventListener('pointerenter', () => show(element))
    element.addEventListener('pointerleave', hide)
    element.addEventListener('focus', () => show(element))
    element.addEventListener('blur', hide)
    element.addEventListener('pointerdown', hide)
  },
  updated(element, binding) {
    element.dataset.tooltip = binding.value ?? ''
  },
  beforeUnmount: hide,
}
