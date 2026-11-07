import { computed } from 'vue'
import type { TranslationKey } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import { hasCode, isAgent, toolCodes, toolCompatible, type Tool } from './catalog'
import { usePlayground } from './session'

// ToolDefinition 是运行设置中一个工具开关的名称、图标与说明
interface ToolDefinition {
  id: Tool
  label: TranslationKey
  tooltip: TranslationKey
  icon: IconName
  editable?: boolean
}

// toolDefinitions 是官网工具分组的开关顺序
const toolDefinitions: ToolDefinition[] = [
  {
    id: 'structured_output',
    label: 'pg.structuredOutput',
    tooltip: 'pg.structuredOutputTip',
    icon: 'structuredOutput',
    editable: true,
  },
  { id: 'code_execution', label: 'pg.agentMode', tooltip: 'pg.agentModeTip', icon: 'code' },
  {
    id: 'function_declarations',
    label: 'pg.functionCalling',
    tooltip: 'pg.functionCallingTip',
    icon: 'functionCalling',
    editable: true,
  },
  { id: 'google_search', label: 'pg.googleSearch', tooltip: 'pg.googleSearchTip', icon: 'google' },
  { id: 'google_maps', label: 'pg.googleMaps', tooltip: 'pg.googleMapsTip', icon: 'maps' },
  { id: 'image_search', label: 'pg.imageSearch', tooltip: 'pg.imageSearchTip', icon: 'modeImage' },
  { id: 'url_context', label: 'pg.urlContext', tooltip: 'pg.urlContextTip', icon: 'link' },
]

// agentCodeExecution 是 agent 的代码执行开关：与官网 agent 页面一样称为代码执行
const agentCodeExecution = { label: 'pg.codeExecution', tooltip: 'pg.codeExecutionTip' } as const

// useTools 返回当前模型支持的工具及其开启与可用状态
export function useTools() {
  const { session, currentModel } = usePlayground()
  const tools = computed(() =>
    toolDefinitions
      .filter((tool) => hasCode(currentModel.value, toolCodes[tool.id]))
      .map((tool) => ({
        ...tool,
        ...(tool.id === 'code_execution' && isAgent(currentModel.value) && agentCodeExecution),
        enabled: session.settings.tools.includes(tool.id),
        disabled: !toolCompatible(currentModel.value, tool.id, session.settings.tools),
      })),
  )

  // setTool 开启或关闭一个工具
  function setTool(id: Tool, enabled: boolean): void {
    const current = session.settings.tools.filter((tool) => tool !== id)
    session.settings.tools = enabled ? [...current, id] : current
  }

  return { tools, setTool }
}
