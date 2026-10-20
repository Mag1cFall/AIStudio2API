import { defineComponent, h, type PropType, type VNode } from 'vue'
import CodeBlock from './CodeBlock.vue'
import MermaidBlock from './MermaidBlock.vue'
import type { MarkdownNode } from './markdown'

// renderNode 把一个正文节点渲染为 VNode；块元素只含一段行内内容时直接写入该元素
function renderNode(node: MarkdownNode, key: number): VNode {
  switch (node.kind) {
    case 'code':
      return node.language === 'mermaid'
        ? h(MermaidBlock, { key, code: node.code })
        : h(CodeBlock, { key, code: node.code, language: node.language })
    case 'inline':
      return h('span', { key, innerHTML: node.html })
    case 'html':
      return h('div', { key, class: 'md-html', innerHTML: node.html })
    case 'element': {
      const only = node.children.length === 1 ? node.children[0] : undefined
      const table = node.tag === 'table'
      const attrs = table ? node.attrs : { ...node.attrs, key }
      const element =
        only?.kind === 'inline'
          ? h(node.tag, { ...attrs, innerHTML: only.html })
          : h(node.tag, attrs, node.children.map(renderNode))
      return table ? h('div', { key, class: 'table-container' }, [element]) : element
    }
  }
}

// MarkdownBlock 渲染正文的一个顶层块；nodes 为同一对象时不重新渲染
export default defineComponent({
  name: 'MarkdownBlock',
  props: { nodes: { type: Array as PropType<MarkdownNode[]>, required: true } },
  setup(props) {
    return () => props.nodes.map(renderNode)
  },
})
