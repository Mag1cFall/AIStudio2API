import {
  computed,
  effectScope,
  hasInjectionContext,
  inject,
  provide,
  reactive,
  ref,
  shallowReactive,
  watch,
  type InjectionKey,
} from 'vue'
import { api, ApiError } from '@/api'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import type { Model, SavedPrompt } from '@/types'
import { defaultChatModel, hasCode, toolCodes, toolCompatible, type Tool } from './catalog'
import { readImage } from './media/api'
import { generate, protocolsFor } from './protocols'
import { apiKey, appendParts, buildRequest, countTokens, StreamError } from './request'
import type {
  CompareData,
  PaneData,
  Part,
  PromptData,
  RunSettings,
  SafetyCategory,
  SafetyThreshold,
  Turn,
} from './types'
import type { ZeroStarter } from './zero-configs'

// safetyCategories 是官网安全设置对话框的类别顺序
export const safetyCategories: SafetyCategory[] = [
  'HARM_CATEGORY_HARASSMENT',
  'HARM_CATEGORY_HATE_SPEECH',
  'HARM_CATEGORY_SEXUALLY_EXPLICIT',
  'HARM_CATEGORY_DANGEROUS_CONTENT',
]

// safetyThresholds 是安全设置滑块从关闭到最严格的五档
export const safetyThresholds: SafetyThreshold[] = [
  'OFF',
  'BLOCK_NONE',
  'BLOCK_ONLY_HIGH',
  'BLOCK_MEDIUM_AND_ABOVE',
  'BLOCK_LOW_AND_ABOVE',
]

// lastModelKey 是记住上次所选模型的本机存储键
const lastModelKey = 'playground_model'

// outputLimit 返回模型输出长度上限，自动续写模型为输入上限
export function outputLimit(model: Model | undefined): number | undefined {
  return model?.capabilities?.output_continuation
    ? model.input_token_limit
    : model?.output_token_limit
}

// modelDefaults 返回随模型变化的运行设置默认值
function modelDefaults(model: Model | undefined): Partial<RunSettings> {
  return {
    model: model?.id ?? '',
    temperature: model?.temperature ?? null,
    topP: model?.top_p ?? null,
    topK: model?.top_k ?? null,
    maxOutputTokens: model?.output_token_limit ?? null,
    thinkingLevel: model?.default_thinking_level ?? null,
    thinkingBudget: -1,
    aspectRatio: '',
    imageSize: defaultImageSize(model),
    videoAspectRatio: '',
    videoDuration: null,
    videoResolution: model?.capability_options?.video_output_resolutions?.[0] ?? '',
    videoTask: '',
  }
}

// defaultImageSize 与官网一致：模型可选分辨率中有 1K 时默认 1K，否则为第一项
function defaultImageSize(model: Model | undefined): string {
  const sizes = model?.capability_options?.image_output_resolutions ?? []
  return sizes.includes('1K') ? '1K' : (sizes[0] ?? '')
}

// defaultSettings 返回新对话的运行设置：模型默认参数、安全设置全部关闭、支持时开启 Google 搜索
export function defaultSettings(model: Model | undefined): RunSettings {
  return {
    model: '',
    protocol: 'gemini',
    stream: true,
    systemInstruction: '',
    temperature: null,
    topP: null,
    topK: null,
    maxOutputTokens: null,
    stopSequences: [],
    thinkingLevel: null,
    thinkingBudget: -1,
    mediaResolution: '',
    safety: Object.fromEntries(
      safetyCategories.map((category) => [category, 'OFF']),
    ) as RunSettings['safety'],
    tools: hasCode(model, toolCodes.google_search) ? ['google_search'] : [],
    responseSchema: '',
    functionDeclarations: '',
    imageOutput: 'IMAGE_TEXT',
    aspectRatio: '',
    imageSize: '',
    videoAspectRatio: '',
    videoDuration: null,
    videoResolution: '',
    videoTask: '',
    imageCount: 1,
    openAISize: 'auto',
    imageQuality: 'auto',
    ...modelDefaults(model),
  }
}

// newTurnID 生成 32 位十六进制的轮次与对话 ID，非 HTTPS 的远程地址中同样可用
export function newTurnID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  )
    .join('')
    .toUpperCase()
}

// SaveState 是对话保存状态
export type SaveState = 'idle' | 'saving' | 'saved' | 'error'

// cloneData 以 JSON 往返复制对话数据，嵌套的响应式对象一并复制为普通对象
function cloneData<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

// models 返回实时模型目录
function models(): Model[] {
  return useConsole().models.value
}

// startingModel 返回新对话的模型：上次所选且仍在目录中，否则为默认对话模型
function startingModel(): Model | undefined {
  let remembered = ''
  try {
    remembered = localStorage.getItem(lastModelKey) ?? ''
  } catch {
    remembered = ''
  }
  return models().find((model) => model.id === remembered) ?? defaultChatModel(models())
}

// SessionLinks 是对话实例与页面其他窗格的关联：compare 返回随对话一起保存的右侧窗格，save 是右侧窗格变化时保存页面对话的函数
interface SessionLinks {
  compare?: () => CompareData | undefined
  save?: () => Promise<void>
}

// createSession 创建一个对话实例，temporary 为真时不单独写入对话库
function createSession(temporary = false, links: SessionLinks = {}) {
  const session = reactive({
    id: null as string | null,
    title: '',
    settings: defaultSettings(undefined),
    turns: [] as Turn[],
    draft: '',
    attachments: [] as Part[],
    running: false,
    started: 0,
    temporary,
    rawMode: false,
    promptTokens: null as number | null,
    saveState: 'idle' as SaveState,
    loadError: '',
    loading: false,
  })

  let controller: AbortController | undefined
  let saveTimer = 0
  let tokenController: AbortController | undefined
  const scope = effectScope(true)

  // currentModel 返回当前所选模型
  const currentModel = scope.run(() =>
    computed(() => models().find((model) => model.id === session.settings.model)),
  )!

  // newChat 丢弃尚未写入的保存与生成，开始一个空白对话，systemInstruction 为新对话的系统指令
  function newChat(systemInstruction = ''): void {
    discard()
    Object.assign(session, {
      id: null,
      title: '',
      settings: { ...defaultSettings(startingModel()), systemInstruction },
      turns: [],
      draft: '',
      attachments: [],
      running: false,
      promptTokens: null,
      saveState: 'idle',
      loadError: '',
      rawMode: false,
    })
  }

  // ensureModel 在目录到达后为没有模型的对话补上默认模型
  function ensureModel(): void {
    if (session.settings.model === '' && models().length > 0) {
      const model = startingModel()
      session.settings = {
        ...defaultSettings(model),
        systemInstruction: session.settings.systemInstruction,
      }
    }
  }

  // selectModel 切换模型：参数换为新模型默认值，去掉新模型不支持的工具；remember 为真时记为下次新对话的模型
  function selectModel(id: string, remember = true): void {
    const model = models().find((item) => item.id === id)
    Object.assign(session.settings, modelDefaults(model))
    session.settings.tools = session.settings.tools.filter((tool) =>
      hasCode(model, toolCodes[tool]),
    )
    if (!protocolsFor(model).some((item) => item.value === session.settings.protocol))
      session.settings.protocol = 'gemini'
    if (!remember) return
    try {
      localStorage.setItem(lastModelKey, id)
    } catch {
      // 本机存储不可用时只影响下次默认模型
    }
  }

  // open 读取对话库中的对话并返回其内容，loaded 为已读取的内容，读取失败时返回 undefined
  async function open(
    id: string,
    loaded?: SavedPrompt<PromptData>,
  ): Promise<SavedPrompt<PromptData> | undefined> {
    if (session.id === id) return undefined
    discard()
    session.loading = true
    session.loadError = ''
    try {
      const prompt = loaded ?? (await api.prompt<PromptData>(id))
      Object.assign(session, {
        id,
        title: prompt.title,
        settings: { ...defaultSettings(undefined), ...prompt.data.settings },
        turns: prompt.data.turns,
        draft: '',
        attachments: [],
        running: false,
        temporary: false,
        promptTokens: null,
        saveState: 'saved',
      })
      void refreshTokens()
      return prompt
    } catch (error) {
      session.loadError =
        error instanceof ApiError && error.status === 404 ? 'not_found' : String(error)
      return undefined
    } finally {
      session.loading = false
    }
  }

  // promptTitle 以首条用户输入的第一行作为对话标题
  function promptTitle(): string {
    const first = session.turns.find((turn) => turn.role === 'user')
    const text =
      first?.parts
        .find((part) => part.text)
        ?.text?.trim()
        .split('\n')[0] ?? ''
    return text.length > 60 ? `${text.slice(0, 60)}…` : text
  }

  // save 把调用时的对话连同对比模式的右侧窗格写入对话库，右侧窗格保存所在的页面对话，其他临时对话不保存；保存状态只回写到仍在显示的同一对话
  async function save(): Promise<void> {
    if (links.save) return links.save()
    if (session.temporary || session.turns.length === 0) return
    const compare = links.compare?.()
    session.id ??= newTurnID()
    if (session.title === '') session.title = promptTitle()
    const id = session.id
    session.saveState = 'saving'
    try {
      await api.savePrompt<PromptData>(id, {
        title: session.title,
        model: session.settings.model,
        description: promptTitle(),
        data: { settings: session.settings, turns: session.turns, ...(compare && { compare }) },
      })
      if (session.id === id) session.saveState = 'saved'
    } catch {
      if (session.id === id) session.saveState = 'error'
    }
  }

  // scheduleSave 在对话变化后合并保存，生成中到时的保存由生成结束时的保存代替
  function scheduleSave(): void {
    window.clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => {
      saveTimer = 0
      if (!session.running) void save()
    }, 600)
  }

  // pending 表示对话有尚未到时的合并保存或正在生成
  function pending(): boolean {
    return saveTimer !== 0 || session.running
  }

  // discard 撤销尚未到时的合并保存并中止生成
  function discard(): void {
    window.clearTimeout(saveTimer)
    saveTimer = 0
    controller?.abort()
  }

  // refreshTokens 用 countTokens 统计当前对话的输入 token
  async function refreshTokens(): Promise<void> {
    tokenController?.abort()
    const model = currentModel.value
    if (model === undefined || session.turns.length === 0) {
      session.promptTokens = null
      return
    }
    const counter = new AbortController()
    tokenController = counter
    try {
      const count = await countTokens(
        model.id,
        apiKey(),
        buildRequest(session.settings, model, session.turns),
        counter.signal,
      )
      if (!counter.signal.aborted) session.promptTokens = count
    } catch {
      // 统计失败时保留上一次的数字
    }
  }

  // append 把输入框内容作为用户轮次加入对话但不运行并安排保存，输入为空时返回 false
  function append(): boolean {
    const text = session.draft.trim()
    if (text === '' && session.attachments.length === 0) return false
    const parts: Part[] = [...session.attachments]
    if (text !== '') parts.push({ text: session.draft })
    session.turns.push({ id: newTurnID(), role: 'user', parts, created: Date.now() })
    session.draft = ''
    session.attachments = []
    scheduleSave()
    return true
  }

  // submit 发送输入框内容；输入为空且最后一轮为用户轮次时直接运行
  async function submit(): Promise<void> {
    if (session.running) return
    if (!append() && session.turns.at(-1)?.role !== 'user') return
    await run()
  }

  // run 以当前对话生成一个模型轮次
  async function run(): Promise<void> {
    const model = currentModel.value
    if (model === undefined || session.running) return
    const request = buildRequest(session.settings, model, session.turns)
    void refreshTokens()
    const turn = reactive<Turn>({ id: newTurnID(), role: 'model', parts: [], created: Date.now() })
    session.turns.push(turn)
    session.running = true
    session.started = performance.now()
    controller = new AbortController()
    const signal = controller.signal
    if (session.id === null && !session.temporary) await save()
    try {
      await generate({
        protocol: session.settings.protocol,
        stream: session.settings.stream,
        model,
        modelID: model.id,
        apiKey: apiKey(),
        request,
        images: {
          count: session.settings.imageCount,
          size: session.settings.openAISize,
          quality: session.settings.imageQuality,
        },
        signal,
        onUpdate: (update) => {
          const elapsed = Math.round(performance.now() - session.started)
          if (turn.latency === undefined && update.parts.length > 0) turn.latency = elapsed
          if (
            turn.thinking === undefined &&
            turn.parts.some((part) => part.thought) &&
            update.parts.some(
              (part) => !part.thought && !(part.text === '' && part.thoughtSignature),
            )
          )
            turn.thinking = elapsed
          appendParts(turn.parts, update.parts)
          if (update.finishReason) turn.finishReason = update.finishReason
          if (update.tokens !== undefined) turn.tokens = update.tokens
          if (update.thoughtTokens !== undefined) turn.thoughtTokens = update.thoughtTokens
          if (update.promptTokens !== undefined) {
            tokenController?.abort()
            session.promptTokens = update.promptTokens + (turn.tokens ?? 0)
          }
          if (update.sources) turn.sources = update.sources
          if (update.supports) turn.supports = update.supports
          if (update.searchQueries) turn.searchQueries = update.searchQueries
        },
      })
    } catch (error) {
      if (!signal.aborted) {
        turn.error =
          error instanceof StreamError || error instanceof Error ? error.message : String(error)
        useConsole().showNotice(useI18n().t('pg.generateFailed'), 'error')
      }
    } finally {
      turn.duration = Math.round(performance.now() - session.started)
      if (turn.thinking === undefined && turn.parts.some((part) => part.thought))
        turn.thinking = turn.duration
      session.running = false
      controller = undefined
      const index = session.turns.indexOf(turn)
      if (
        index !== -1 &&
        turn.parts.length === 0 &&
        turn.error === undefined &&
        turn.finishReason === undefined
      )
        session.turns.splice(index, 1)
      scheduleSave()
    }
  }

  // stop 中止当前生成
  function stop(): void {
    controller?.abort()
  }

  // rerun 从某一轮重新生成：模型轮次替换自身，用户轮次替换其后的回复
  function rerun(index: number): void {
    if (session.running) return
    const turn = session.turns[index]
    if (turn === undefined) return
    session.turns.splice(turn.role === 'model' ? index : index + 1)
    void run()
  }

  // respond 以用户填写的函数响应继续生成：响应为 JSON 对象时原样发送，否则作为 result 字段发送
  function respond(call: NonNullable<Part['functionCall']>, text: string): void {
    if (session.running) return
    let response: Record<string, unknown>
    try {
      const value: unknown = JSON.parse(text)
      response =
        typeof value === 'object' && value !== null && !Array.isArray(value)
          ? (value as Record<string, unknown>)
          : { result: value }
    } catch {
      response = { result: text }
    }
    session.turns.push({
      id: newTurnID(),
      role: 'user',
      parts: [
        { functionResponse: { name: call.name, response, ...(call.id ? { id: call.id } : {}) } },
      ],
      created: Date.now(),
    })
    void run()
  }

  // editTurn 用新文本替换某一轮的文本片段
  function editTurn(index: number, text: string): void {
    const turn = session.turns[index]
    if (turn === undefined) return
    const others = turn.parts.filter((part) => part.text === undefined || part.thought)
    turn.parts = text === '' ? others : [...others, { text }]
    scheduleSave()
  }

  // deleteTurn 删除一轮
  function deleteTurn(index: number): void {
    session.turns.splice(index, 1)
    scheduleSave()
  }

  // branch 以前 index+1 轮创建新对话并返回其 ID
  async function branch(
    index: number,
    titleKey: 'pg.branchOf' | 'pg.copyOf' = 'pg.branchOf',
  ): Promise<string> {
    const id = newTurnID()
    await api.savePrompt<PromptData>(id, {
      title: useI18n()
        .t(titleKey)
        .replace('{title}', session.title || promptTitle()),
      model: session.settings.model,
      description: promptTitle(),
      data: cloneData({ settings: session.settings, turns: session.turns.slice(0, index + 1) }),
    })
    return id
  }

  // copyPrompt 复制当前对话为新对话并返回其 ID
  async function copyPrompt(): Promise<string> {
    return branch(session.turns.length - 1, 'pg.copyOf')
  }

  // applyStarter 按官网空态示例填写输入框：提示词、参考图片、思考档位与搜索、代码执行开关
  async function applyStarter(item: ZeroStarter): Promise<void> {
    const model = currentModel.value
    if (item.thinkingLevel && model?.thinking_levels?.includes(item.thinkingLevel))
      session.settings.thinkingLevel = item.thinkingLevel
    const enable = (tool: Tool) => {
      if (hasCode(model, toolCodes[tool]) && toolCompatible(model, tool, session.settings.tools))
        session.settings.tools = [...new Set([...session.settings.tools, tool])]
    }
    if (item.search) enable('google_search')
    if (item.codeExecution) enable('code_execution')
    session.draft = item.prompt
    session.attachments = []
    try {
      for (const url of item.images ?? []) {
        const response = await fetch(url)
        if (!response.ok) throw new Error(`${response.status} ${url}`)
        session.attachments.push({ inlineData: await readImage(await response.blob()) })
      }
    } catch (error) {
      useConsole().showNotice(String(error), 'error')
    }
  }

  // rename 修改对话标题
  function rename(title: string): void {
    session.title = title.trim()
    scheduleSave()
  }

  // dispose 中止生成与保存并停止监听
  function dispose(): void {
    discard()
    tokenController?.abort()
    scope.stop()
  }

  scope.run(() => {
    watch(
      () => [session.settings, session.temporary] as const,
      () => {
        if (session.id !== null && !session.running) scheduleSave()
      },
      { deep: true },
    )
    watch(
      () => currentModel.value?.id,
      () => void refreshTokens(),
    )
  })

  return {
    session,
    currentModel,
    newChat,
    ensureModel,
    selectModel,
    open,
    append,
    submit,
    run,
    stop,
    rerun,
    respond,
    editTurn,
    deleteTurn,
    branch,
    copyPrompt,
    applyStarter,
    rename,
    save,
    pending,
    refreshTokens,
    dispose,
  }
}

// Playground 是一个对话实例与页面级操作
export type Playground = ReturnType<typeof createSession> & {
  deletePrompt: () => Promise<void>
}

// main 是页面对话，对比模式下为左侧窗格，保存时带上右侧窗格
const main = createSession(false, { compare: comparePane })

// comparePane 返回对比模式右侧窗格的运行设置、对话与系统指令同步状态，单对话时为 undefined
function comparePane(): CompareData | undefined {
  const right = panes[1]
  return (
    right && {
      settings: right.session.settings,
      turns: right.session.turns,
      sync: syncInstructions.value,
    }
  )
}

// flush 在切换对话前立即保存页面对话与对比模式右侧窗格尚未写入的内容，生成中保存已收到的内容
function flush(): void {
  if (panes.some((pane) => pane.pending())) void main.save()
}

// newChat 保存当前对话后开始一个空白对话并退出对比模式，systemInstruction 为新对话的系统指令
function newChat(systemInstruction = ''): void {
  flush()
  leaveCompare()
  main.newChat(systemInstruction)
}

// open 读取对话库中的对话，切换到其他对话时退出对比模式，对话带右侧窗格时恢复对比模式
async function open(
  id: string,
  loaded?: SavedPrompt<PromptData>,
): Promise<SavedPrompt<PromptData> | undefined> {
  if (main.session.id === id) return undefined
  flush()
  leaveCompare()
  const prompt = await main.open(id, loaded)
  const compare = prompt?.data.compare
  if (compare) {
    const pane = addPane({
      settings: { ...defaultSettings(undefined), ...compare.settings },
      turns: compare.turns,
    })
    pane.session.rawMode = main.session.rawMode
    syncInstructions.value = compare.sync
    void pane.refreshTokens()
  }
  return prompt
}

// deletePrompt 从对话库删除当前对话并开始空白对话，被删对话尚未写入的内容与生成一并丢弃
async function deletePrompt(): Promise<void> {
  const id = main.session.id
  leaveCompare()
  main.newChat()
  if (id !== null) await api.deletePrompt(id)
}

// page 为对话实例加上页面级操作
function page(instance: ReturnType<typeof createSession>): Playground {
  return { ...instance, newChat, open, deletePrompt }
}

// primary 是页面对话及其操作
const primary = page(main)

// panes 是并排显示的对话，单对话时只有 primary
const panes = shallowReactive<Playground[]>([primary])

// comparing 表示处于对比模式
const comparing = computed(() => panes.length > 1)

// syncInstructions 与官网一致表示对比模式两侧的系统指令同步，进入对比模式时开启
const syncInstructions = ref(false)

// 同步系统指令时，一侧的系统指令变化后写入其他窗格
watch(
  () => panes.map((pane) => pane.session.settings.systemInstruction),
  (values, previous) => {
    if (!comparing.value || !syncInstructions.value) return
    const changed = values.findIndex((value, index) => value !== previous[index])
    const text = values[changed]
    if (text === undefined) return
    for (const pane of panes) pane.session.settings.systemInstruction = text
  },
)

// setSync 与官网一致切换系统指令同步，开启时把 index 窗格的系统指令写入右侧窗格
function setSync(index: number, value: boolean): void {
  syncInstructions.value = value
  const text = panes[index]?.session.settings.systemInstruction
  if (!value || text === undefined) return
  for (const pane of panes.slice(1)) pane.session.settings.systemInstruction = text
}

// running 表示有窗格正在生成
const running = computed(() => panes.some((pane) => pane.session.running))

// addPane 以 data 的运行设置与对话加入右侧窗格并返回它，右侧窗格变化时保存页面对话
function addPane(data: PaneData): Playground {
  const pane = page(createSession(true, { save: main.save }))
  pane.session.settings = cloneData(data.settings)
  pane.session.turns = cloneData(data.turns)
  panes.push(pane)
  return pane
}

// enterCompare 与官网一致：复制当前对话与运行设置作为右侧窗格
function enterCompare(): void {
  if (comparing.value) return
  const pane = addPane({
    settings: main.session.settings,
    turns: main.session.turns.map((turn) => ({ ...turn, id: newTurnID() })),
  })
  pane.session.promptTokens = main.session.promptTokens
  pane.session.rawMode = main.session.rawMode
  syncInstructions.value = true
}

// leaveCompare 关闭右侧窗格回到单对话
function leaveCompare(): void {
  for (const pane of panes.splice(1)) pane.dispose()
}

// closePane 关闭一个窗格：关闭左侧时右侧的对话与运行设置成为页面对话
function closePane(index: number): void {
  const right = panes[1]
  if (right === undefined) return
  if (index === 0) {
    main.stop()
    right.stop()
    Object.assign(main.session, {
      settings: right.session.settings,
      turns: right.session.turns,
      promptTokens: right.session.promptTokens,
    })
  }
  leaveCompare()
  if (main.session.id !== null) void main.save()
}

// shareDraft 把输入框内容复制到其他窗格
function shareDraft(): void {
  for (const pane of panes.slice(1)) {
    pane.session.draft = main.session.draft
    pane.session.attachments = cloneData(main.session.attachments)
  }
}

// submitAll 把输入框内容作为用户轮次同时发往每个窗格
async function submitAll(): Promise<void> {
  shareDraft()
  await Promise.all(panes.map((pane) => pane.submit()))
}

// appendAll 把输入框内容作为用户轮次加入每个窗格但不运行
function appendAll(): void {
  shareDraft()
  for (const pane of panes) pane.append()
}

// stopAll 中止所有窗格的生成
function stopAll(): void {
  for (const pane of panes) pane.stop()
}

// toggleRaw 切换所有窗格的原始模式
function toggleRaw(): void {
  const raw = !main.session.rawMode
  for (const pane of panes) pane.session.rawMode = raw
}

// playgroundKey 是窗格向子组件提供对话实例的注入键
const playgroundKey: InjectionKey<Playground> = Symbol('playground')

// providePlayground 让子组件的 usePlayground 返回该窗格的对话
export function providePlayground(instance: Playground): void {
  provide(playgroundKey, instance)
}

// usePlayground 返回所在窗格的对话与操作，窗格外为页面对话
export function usePlayground(): Playground {
  return hasInjectionContext() ? inject(playgroundKey, primary) : primary
}

// useCompare 返回对比模式的窗格与操作
export function useCompare() {
  return {
    panes,
    comparing,
    running,
    enterCompare,
    closePane,
    syncInstructions,
    setSync,
    submitAll,
    appendAll,
    stopAll,
    toggleRaw,
  }
}
