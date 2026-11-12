import {
  computed,
  effectScope,
  hasInjectionContext,
  inject,
  markRaw,
  provide,
  reactive,
  ref,
  shallowReactive,
  toRaw,
  watch,
  watchEffect,
  type InjectionKey,
} from 'vue'
import { api, ApiError } from '@/api'
import { useConsole } from '@/console'
import { keepPage } from '@/desktop'
import { useI18n } from '@/i18n'
import type { Model, SavedPrompt, SystemInstructionPreset } from '@/types'
import { attachmentBlob, copyAttachmentBlob } from './attachment'
import {
  agentTools,
  Capability,
  defaultChatModel,
  defaultOn,
  hasCode,
  isAgent,
  toolCodes,
  toolCompatible,
  type Tool,
} from './catalog'
import { countVideoFrames, driveFileExists, uploadDriveFile } from './drive'
import { readImage } from './media/api'
import { generate, protocolsFor } from './protocols'
import {
  agentFields,
  apiKey,
  apiPart,
  appendParts,
  buildRequest,
  continuation,
  countTokens,
  countTokensDetailed,
  failureText,
} from './request'
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
import { requestTitle } from './title'
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

// outputLimit 返回模型输出长度上限，自动续写模型为输入上限
export function outputLimit(model: Model | undefined): number | undefined {
  return model?.capabilities?.output_continuation
    ? model.input_token_limit
    : model?.output_token_limit
}

// researchDefaults 是 Deep Research 的思考摘要、可视化与协作规划默认值，与官网一样取模型目录 field 82 默认开启的能力码
function researchDefaults(model: Model | undefined) {
  return {
    thinkingSummaries: defaultOn(model, Capability.ThinkingSummaries),
    visualization: defaultOn(model, Capability.Visualization),
    collaborativePlanning: defaultOn(model, Capability.CollaborativePlanning),
  }
}

// modelDefaults 返回随模型变化的运行设置默认值，官网面板隐藏的采样参数默认不发送，agent 打开官网默认的工具
function modelDefaults(model: Model | undefined): Partial<RunSettings> {
  return {
    ...(isAgent(model) && { tools: agentTools(model!) }),
    model: model?.id ?? '',
    temperature: hasCode(model, Capability.Temperature) ? (model?.temperature ?? null) : null,
    topP: hasCode(model, Capability.TopP) ? (model?.top_p ?? null) : null,
    topK: hasCode(model, Capability.TopK) ? (model?.top_k ?? null) : null,
    maxOutputTokens: model?.output_token_limit ?? null,
    thinkingLevel: model?.default_thinking_level ?? null,
    thinkingBudget: -1,
    aspectRatio: '',
    imageSize: defaultImageSize(model),
    videoAspectRatio: '',
    videoDuration: null,
    videoResolution: model?.capability_options?.video_output_resolutions?.[0] ?? '',
    videoTask: '',
    ...researchDefaults(model),
  }
}

// defaultImageSize 与官网一致：模型可选分辨率中有 1K 时默认 1K，否则为第一项
function defaultImageSize(model: Model | undefined): string {
  const sizes = model?.capability_options?.image_output_resolutions ?? []
  return sizes.includes('1K') ? '1K' : (sizes[0] ?? '')
}

// defaultSettings 返回新对话的运行设置：模型默认参数、安全设置全部关闭，agent 之外的模型工具全部关闭
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
    tools: [],
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
    agentModel: '',
    maxTotalTokens: null,
    environment: { type: 'new', id: '', sources: [], rules: [] },
    ...researchDefaults(undefined),
    ...modelDefaults(model),
  }
}

// newTurnID 生成 32 位十六进制的轮次与对话 ID
export function newTurnID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  )
    .join('')
    .toUpperCase()
}

// SaveState 是对话保存状态
type SaveState = 'idle' | 'saving' | 'saved' | 'error'

// uploadedVideo 表示片段是附件加入时已上传 Drive 的视频，与官网一样以抽帧统计 token 数
function uploadedVideo(part: Part): boolean {
  return part.fileData !== undefined && part.inlineData?.mimeType.startsWith('video/') === true
}

// renewUploads 在发送前把 Files API 上传文件已到期或已不存在的视频以对话中保留的原视频重新上传，片段改为引用新文件；signal 中止时检查与上传随之中止
async function renewUploads(
  turns: readonly Turn[],
  signal: AbortSignal | null = null,
): Promise<void> {
  const videos = turns.flatMap((turn) => turn.parts).filter(uploadedVideo)
  for (const part of videos) {
    if (Date.now() < part.fileExpires! && (await driveFileExists(part.fileData!.fileUri, signal)))
      continue
    const uploaded = await uploadDriveFile(attachmentBlob(part), part.fileName!, signal)
    part.fileData = { mimeType: uploaded.mimeType, fileUri: uploaded.uri }
    part.fileExpires = Date.parse(uploaded.expirationTime)
  }
}

// rawInlineMarks 把对话轮次中的内联媒体标记为非响应式对象
function rawInlineMarks(turns: Turn[]): Turn[] {
  for (const turn of turns) {
    for (const part of turn.parts) {
      if (part.inlineData) part.inlineData = markRaw(toRaw(part.inlineData))
    }
  }
  return turns
}

// framedVideo 表示片段是已按抽帧统计过 token 数的视频
function framedVideo(part: Part): boolean {
  return uploadedVideo(part) && part.tokenCount !== undefined
}

// attachmentCounts 记录每个附件最近一次 token 计数请求，只接收最近一次请求的结果
const attachmentCounts = new WeakMap<Part, object>()

// cloneData 复制对话数据为普通对象，内联附件复用原 Base64 字符串与缓存的 Blob
function cloneData<T>(value: T): T {
  const raw = toRaw(value)
  if (raw === null || typeof raw !== 'object') return raw
  if (Array.isArray(raw)) return raw.map((item) => cloneData(item)) as T
  const copy: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(raw as Record<string, unknown>)) {
    if (key === 'inlineData' && item !== null && typeof item === 'object') {
      const source = toRaw(item) as NonNullable<Part['inlineData']>
      const target = markRaw({ ...source })
      copyAttachmentBlob(source, target)
      copy[key] = target
    } else {
      copy[key] = cloneData(item)
    }
  }
  return copy as T
}

// models 返回实时模型目录
function models(): Model[] {
  return useConsole().models.value
}

// SessionLinks 是对话实例与页面其他窗格的关联：compare 返回随对话一起保存的右侧窗格，save 是右侧窗格变化时保存页面对话的函数，draft 返回共用的输入框文本与附件
interface SessionLinks {
  compare?: () => CompareData | undefined
  save?: () => Promise<void>
  draft?: () => { text: string; attachments: Part[] }
}

// createSession 创建一个对话实例，temporary 为真时不单独写入对话库
function createSession(temporary = false, links: SessionLinks = {}) {
  const session = reactive({
    id: null as string | null,
    title: '',
    description: '',
    settings: defaultSettings(undefined),
    turns: [] as Turn[],
    draft: '',
    attachments: [] as Part[],
    running: false,
    started: 0,
    temporary,
    rawMode: false,
    promptTokens: null as number | null,
    inputTokens: null as number | null,
    outputTokens: 0,
    cachedTokens: 0,
    systemTokens: null as number | null,
    saveState: 'idle' as SaveState,
    loadError: '',
    loading: false,
  })

  let controller: AbortController | undefined
  const saveTimer = ref(0)
  // opened 是 open 装入的运行设置，换入它引起的设置变化不触发保存
  let opened: RunSettings | undefined
  let tokenController: AbortController | undefined
  let tokenTimer = 0
  let lastTokenKey = ''
  let pendingTokenKey = ''
  let lastSystemKey = ''
  // countedTotalTokens 是最近一次 countTokens 统计到的对话与草稿文本总 token 数（含末尾模型轮次的可见正文與已抽帧视频）
  const countedTotalTokens = ref<number | null>(null)
  // turnCounts 记录每一轮在 countTokens 中占用的可见 token 数，删除轮次或切换末尾模型轮次时同步扣除
  const turnCounts = reactive(new Map<string, number>())
  const scope = effectScope(true)

  // draftSource 返回本窗格使用的输入框文本与附件，对比模式的右侧窗格共用主输入框
  const draftSource = () =>
    links.draft ? links.draft() : { text: session.draft, attachments: session.attachments }

  // currentModel 返回当前所选模型
  const currentModel = scope.run(() =>
    computed(() => models().find((model) => model.id === session.settings.model)),
  )!

  // draftAttachmentTokens 是输入框中待发送附件的 token 之和，附件计数完成或移除时立即生效
  const draftAttachmentTokens = scope.run(() =>
    computed(() =>
      draftSource().attachments.reduce((sum, part) => sum + (part.tokenCount ?? 0), 0),
    ),
  )!

  // activeModelTurn 返回当前作为本轮输出的末尾模型轮次；输入框有待发送内容时末尾模型轮次并入下一轮输入历史
  const activeModelTurn = scope.run(() =>
    computed(() => {
      const source = draftSource()
      if (source.text.trim() !== '' || source.attachments.length > 0) return undefined
      const last = session.turns.at(-1)
      if (last?.role !== 'model') return undefined
      return last.tokens !== undefined || turnCounts.has(last.id) ? last : undefined
    }),
  )!

  // visibleTurnTokens 返回模型轮次在上下文中占用的非思考可见 token 数
  function visibleTurnTokens(turn: Turn): number {
    const counted = turnCounts.get(turn.id)
    if (counted !== undefined) return counted
    return Math.max(0, (turn.tokens ?? 0) - (turn.thoughtTokens ?? 0))
  }

  // outputTokens 是当前末尾模型轮次的输出 token 数
  const outputTokens = scope.run(() =>
    computed(() => {
      const tail = activeModelTurn.value
      if (!tail) return 0
      return tail.tokens ?? turnCounts.get(tail.id) ?? 0
    }),
  )!

  // cachedTokens 是当前末尾模型轮次的缓存命中 token 数
  const cachedTokens = scope.run(() => computed(() => activeModelTurn.value?.cachedTokens ?? 0))!

  // newChat 丢弃尚未写入的保存与生成，开始一个空白对话，systemInstruction 为新对话的系统指令
  function newChat(systemInstruction = ''): void {
    discard()
    lastTokenKey = ''
    lastSystemKey = ''
    countedTotalTokens.value = null
    turnCounts.clear()
    Object.assign(session, {
      id: null,
      title: '',
      description: '',
      settings: { ...defaultSettings(defaultChatModel(models())), systemInstruction },
      turns: [],
      draft: '',
      attachments: [],
      running: false,
      promptTokens: null,
      inputTokens: null,
      outputTokens: 0,
      cachedTokens: 0,
      systemTokens: null,
      saveState: 'idle',
      loadError: '',
      rawMode: false,
    })
    if (systemInstruction.trim() !== '') scheduleTokens()
  }

  // ensureModel 在目录到达后为没有模型的对话补上默认模型
  function ensureModel(): void {
    if (session.settings.model === '' && models().length > 0) {
      const model = defaultChatModel(models())
      session.settings = {
        ...defaultSettings(model),
        systemInstruction: session.settings.systemInstruction,
      }
    }
  }

  // selectModel 切换模型：参数换为新模型默认值，去掉新模型不支持的工具
  function selectModel(id: string): void {
    const model = models().find((item) => item.id === id)
    Object.assign(session.settings, modelDefaults(model))
    session.settings.tools = session.settings.tools.filter((tool) =>
      hasCode(model, toolCodes[tool]),
    )
    const protocols = protocolsFor(model)
    if (!protocols.some((item) => item.value === session.settings.protocol))
      session.settings.protocol = protocols[0]!.value
  }

  // open 读取对话库中的对话并返回其内容，loaded 为已读取的内容，读取失败时换成空白对话并记录原因，返回 undefined
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
      lastTokenKey = ''
      lastSystemKey = ''
      countedTotalTokens.value = null
      turnCounts.clear()
      Object.assign(session, {
        id,
        title: prompt.title,
        description: prompt.description,
        settings: { ...defaultSettings(undefined), ...prompt.data.settings },
        turns: rawInlineMarks(prompt.data.turns),
        draft: '',
        attachments: [],
        running: false,
        temporary: false,
        promptTokens: null,
        inputTokens: null,
        outputTokens: 0,
        cachedTokens: 0,
        systemTokens: null,
        saveState: 'saved',
      })
      opened = session.settings
      // 打开对话后在下一个任务统计 token，构造整段对话的请求不并入渲染对话的任务
      setTimeout(() => void refreshTokens())
      return prompt
    } catch (error) {
      newChat()
      session.loadError =
        error instanceof ApiError && error.status === 404 ? 'not_found' : useI18n().errorText(error)
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
    if (
      session.temporary ||
      (session.id === null &&
        session.turns.length === 0 &&
        session.settings.systemInstruction.trim() === '')
    )
      return
    const compare = links.compare?.()
    session.id ??= newTurnID()
    const id = session.id
    session.saveState = 'saving'
    try {
      await api.savePrompt<PromptData>(id, {
        title: session.title,
        model: session.settings.model,
        description: session.description,
        data: { settings: session.settings, turns: session.turns, ...(compare && { compare }) },
      })
      if (session.id === id) session.saveState = 'saved'
    } catch {
      if (session.id === id) session.saveState = 'error'
    }
  }

  // scheduleSave 在对话变化后合并保存，生成中到时的保存由生成结束时的保存代替
  function scheduleSave(): void {
    if (session.saveState === 'saved') session.saveState = 'idle'
    window.clearTimeout(saveTimer.value)
    saveTimer.value = window.setTimeout(() => {
      saveTimer.value = 0
      if (!session.running) void save()
    }, 600)
  }

  // pending 表示对话有尚未到时的合并保存或正在生成
  function pending(): boolean {
    return saveTimer.value !== 0 || session.running
  }

  // discard 撤销尚未到时的合并保存与 token 统计并中止生成
  function discard(): void {
    window.clearTimeout(saveTimer.value)
    saveTimer.value = 0
    window.clearTimeout(tokenTimer)
    tokenTimer = 0
    pendingTokenKey = ''
    tokenController?.abort()
    controller?.abort()
  }

  // requestTurns 返回需要发往 countTokens 统计的轮次与已抽帧计数的视频片段
  function requestTurns(): { turns: Turn[]; sources: Turn[]; framed: Part[] } {
    const source = draftSource()
    const text = source.text.trim()
    const rawTurns =
      text === ''
        ? session.turns
        : [
            ...session.turns,
            { id: 'draft', role: 'user' as const, parts: [{ text: source.text }], created: 0 },
          ]
    const framed = session.turns.flatMap((turn) => turn.parts.filter(framedVideo))
    const turns: Turn[] = []
    const sources: Turn[] = []
    for (const turn of rawTurns) {
      const parts = turn.parts.filter(
        (part) =>
          !framedVideo(part) &&
          !(part.thought && part.text !== undefined && !part.thoughtSignature),
      )
      if (parts.length === 0) {
        const turnFramed = turn.parts
          .filter(framedVideo)
          .reduce((sum, part) => sum + part.tokenCount!, 0)
        turnCounts.set(turn.id, turnFramed)
        continue
      }
      turns.push({ ...turn, parts })
      sources.push(turn)
    }
    return { turns, sources, framed }
  }

  // hasSystemInput 表示当前有可统计 token 的系统指令
  function hasSystemInput(): boolean {
    if (session.settings.systemInstruction.trim() === '') return false
    return hasCode(currentModel.value, Capability.SystemInstruction)
  }

  // scheduleTokens 在文本或设置变化后合并统计 token，输入清空时立即清零
  function scheduleTokens(): void {
    window.clearTimeout(tokenTimer)
    tokenTimer = 0
    const hasSystem = hasSystemInput()
    if (!hasSystem) {
      lastSystemKey = ''
      session.systemTokens = null
    }
    const { turns, framed } = requestTurns()
    if (
      currentModel.value === undefined ||
      (turns.length === 0 && framed.length === 0 && !hasSystem)
    ) {
      pendingTokenKey = ''
      lastTokenKey = ''
      tokenController?.abort()
      countedTotalTokens.value = null
      return
    }
    tokenTimer = window.setTimeout(() => {
      tokenTimer = 0
      if (!session.running) void refreshTokens()
    }, 250)
  }

  // refreshTokens 与官网一致统计当前对话与输入框的 token：与 open 保持同一请求口径，相同请求不重复发送
  async function refreshTokens(): Promise<void> {
    window.clearTimeout(tokenTimer)
    tokenTimer = 0
    const model = currentModel.value
    const hasSystem = hasSystemInput()
    if (!hasSystem) {
      lastSystemKey = ''
      session.systemTokens = null
    }
    if (model === undefined) {
      pendingTokenKey = ''
      lastTokenKey = ''
      tokenController?.abort()
      countedTotalTokens.value = null
      return
    }
    const { turns, sources, framed } = requestTurns()
    if (turns.length === 0 && framed.length === 0 && !hasSystem) {
      pendingTokenKey = ''
      lastTokenKey = ''
      tokenController?.abort()
      countedTotalTokens.value = null
      return
    }
    const framedTokens = framed.reduce((sum, part) => sum + part.tokenCount!, 0)
    if (turns.length === 0 && !hasSystem) {
      pendingTokenKey = ''
      lastTokenKey = ''
      tokenController?.abort()
      countedTotalTokens.value = framedTokens
      return
    }
    const request = buildRequest(session.settings, model, turns)
    const ids = sources.map((turn) => turn.id).join(',')
    const key = `${model.id}:${framedTokens}:${ids}:${JSON.stringify(request)}`
    const systemReady = !hasSystem || session.systemTokens !== null
    if (key === pendingTokenKey) return
    if (key === lastTokenKey && countedTotalTokens.value !== null && systemReady) return
    tokenController?.abort()
    const counter = new AbortController()
    tokenController = counter
    pendingTokenKey = key
    try {
      const result = await countTokensDetailed(model.id, apiKey(), request, counter.signal)
      if (counter.signal.aborted) return
      if (result.contentTokens) {
        sources.forEach((turn, index) => {
          const count = result.contentTokens?.[index]
          if (count === undefined) return
          const turnFramed = turn.parts
            .filter(framedVideo)
            .reduce((sum, part) => sum + part.tokenCount!, 0)
          turnCounts.set(turn.id, count + turnFramed)
          if (turn.role === 'model' && turn.tokens === undefined) turn.tokens = count
        })
      }
      countedTotalTokens.value = result.totalTokens + framedTokens
      if (hasSystem) {
        const sysKey = `${model.id}:${session.settings.systemInstruction}`
        const direct =
          result.systemTokens ??
          (turns.length === 0
            ? (result.contentTokens?.[0] ?? (!request.tools ? result.totalTokens : undefined))
            : undefined)
        if (direct !== undefined) {
          session.systemTokens = direct
          lastSystemKey = sysKey
        } else if (sysKey !== lastSystemKey || session.systemTokens === null) {
          const sysCount = await countTokens(
            model.id,
            apiKey(),
            {
              contents: [{ role: 'user', parts: [{ text: session.settings.systemInstruction }] }],
              generationConfig: {},
            },
            counter.signal,
          )
          if (counter.signal.aborted) return
          session.systemTokens = sysCount
          lastSystemKey = sysKey
        }
      }
      lastTokenKey = key
    } catch {
      // 统计失败时保留上一次的数字
    } finally {
      if (pendingTokenKey === key) pendingTokenKey = ''
    }
  }

  // countAttachment 与官网一致：附件加入时单独统计它的 token 数，写入片段的 tokenCount；已上传 Drive 的视频按视频设置抽帧计数
  async function countAttachment(part: Part): Promise<void> {
    const model = currentModel.value
    if (model === undefined) return
    const request = {}
    attachmentCounts.set(toRaw(part), request)
    try {
      if (uploadedVideo(part)) {
        await renewUploads([{ id: '', role: 'user', parts: [part], created: 0 }])
      }
      const count = uploadedVideo(part)
        ? await countVideoFrames(model.id, part.fileData!.fileUri, part.videoMetadata)
        : await countTokens(
            model.id,
            apiKey(),
            { contents: [{ role: 'user', parts: [apiPart(part)] }], generationConfig: {} },
            new AbortController().signal,
          )
      if (attachmentCounts.get(toRaw(part)) === request) part.tokenCount = count
    } catch {
      // 统计失败时附件不显示 token 数
    }
  }

  // append 把输入框内容作为用户轮次加入对话但不运行并安排保存，输入为空时返回 false
  function append(): boolean {
    const text = session.draft.trim()
    if (text === '' && session.attachments.length === 0) return false
    const attachSum = session.attachments.reduce((sum, part) => sum + (part.tokenCount ?? 0), 0)
    const parts: Part[] = [...session.attachments]
    if (text !== '') parts.push({ text: session.draft })
    const turn: Turn = { id: newTurnID(), role: 'user', parts, created: Date.now() }
    if (attachSum > 0) {
      turnCounts.set(turn.id, attachSum)
      countedTotalTokens.value = (countedTotalTokens.value ?? 0) + attachSum
    }
    session.turns.push(turn)
    session.draft = ''
    session.attachments = []
    lastTokenKey = ''
    scheduleSave()
    void refreshTokens()
    return true
  }

  // submit 发送输入框内容；输入为空且最后一轮为用户轮次时直接运行
  async function submit(): Promise<void> {
    if (!append() && session.turns.at(-1)?.role !== 'user') return
    await run()
  }

  // run 以当前对话生成一个模型轮次
  async function run(): Promise<void> {
    const model = currentModel.value
    if (model === undefined || session.running) return
    const input = continuation(session.settings.protocol, session.turns)
    // sent 是本次发送的轮次，不含随后加入的模型轮次
    const sent = [...input.turns]
    const turn = reactive<Turn>({
      id: newTurnID(),
      role: 'model',
      parts: [],
      created: Date.now(),
      ...(session.settings.tools.includes('code_execution') && { agent: true }),
    })
    // owner 是本次生成所属对话的轮次，打开其他对话或开始新对话时被整体替换
    const owner = session.turns
    owner.push(turn)
    session.running = true
    session.started = performance.now()
    controller = new AbortController()
    const signal = controller.signal
    if (session.id === null && !session.temporary) await save()
    try {
      await renewUploads(sent, signal)
      void refreshTokens()
      await generate({
        protocol: session.settings.protocol,
        stream: session.settings.stream,
        model,
        modelID: model.id,
        apiKey: apiKey(),
        request: buildRequest(session.settings, model, sent),
        images: {
          count: session.settings.imageCount,
          size: session.settings.openAISize,
          quality: session.settings.imageQuality,
        },
        agent: agentFields(session.settings, model),
        previous: input.previous,
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
          if (update.interactionId) turn.interactionId = update.interactionId
          if (update.environmentId)
            Object.assign(session.settings.environment, {
              type: 'existing',
              id: update.environmentId,
              produced: update.environmentId,
            })
          if (update.finishReason) turn.finishReason = update.finishReason
          if (update.tokens !== undefined) turn.tokens = update.tokens
          if (update.thoughtTokens !== undefined) turn.thoughtTokens = update.thoughtTokens
          if (update.cachedTokens !== undefined) turn.cachedTokens = update.cachedTokens
          if (update.promptTokens !== undefined) {
            tokenController?.abort()
            pendingTokenKey = ''
            lastTokenKey = ''
            const visibleOut = Math.max(0, (turn.tokens ?? 0) - (turn.thoughtTokens ?? 0))
            turnCounts.set(turn.id, visibleOut)
            countedTotalTokens.value =
              Math.max(0, update.promptTokens - draftAttachmentTokens.value) + visibleOut
          }
          if (update.sources) turn.sources = update.sources
          if (update.supports) turn.supports = update.supports
          if (update.searchQueries) turn.searchQueries = update.searchQueries
        },
      })
    } catch (error) {
      if (!signal.aborted) {
        turn.error = failureText(error, useI18n().t('pg.chatUnreachable'))
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
      ) {
        turnCounts.delete(turn.id)
        session.turns.splice(index, 1)
      }
      if (!links.save && session.title === '' && session.turns.includes(turn))
        void generateTitle(model.id)
      if (session.turns === owner) {
        lastTokenKey = ''
        scheduleSave()
        void refreshTokens()
      }
    }
  }

  // generateTitle 与官网一样在对话未命名时以最后一轮用户文本与模型回答生成标题，失败时保持未命名
  async function generateTitle(model: string): Promise<void> {
    const turns = session.turns
    const text = (role: Turn['role']) =>
      turns
        .filter((turn) => turn.role === role)
        .at(-1)
        ?.parts.filter((part) => part.text && !part.thought)
        .map((part) => part.text)
        .join('') ?? ''
    const title = await requestTitle(model, text('user'), text('model'), () => ({
      owner: session.turns,
      title: session.title,
    }))
    if (title === '') return
    session.title = title
    scheduleSave()
  }

  // stop 中止当前生成
  function stop(): void {
    controller?.abort()
  }

  // removeTurns 从 start 起移除轮次并立即从已统计的总 token 中扣除它们占用的部分
  function removeTurns(start: number, count = session.turns.length - start): void {
    const removed = session.turns.splice(start, count)
    let deducted = 0
    for (const item of removed) {
      const tokens =
        turnCounts.get(item.id) ?? Math.max(0, (item.tokens ?? 0) - (item.thoughtTokens ?? 0))
      turnCounts.delete(item.id)
      deducted += tokens
    }
    if (countedTotalTokens.value !== null && deducted > 0)
      countedTotalTokens.value = Math.max(0, countedTotalTokens.value - deducted)
    if (session.turns.length === 0 && !hasSystemInput() && draftSource().text.trim() === '')
      countedTotalTokens.value = null
    lastTokenKey = ''
  }

  // rerun 从某一轮重新生成：模型轮次替换自身，用户轮次替换其后的回复
  function rerun(index: number): void {
    if (session.running) return
    removeTurns(session.turns[index]!.role === 'model' ? index : index + 1)
    void run()
  }

  // respond 以用户填写的函数响应继续生成：响应为 JSON 对象时原样发送，否则作为 result 字段发送
  function respond(call: NonNullable<Part['functionCall']>, text: string): void {
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
    lastTokenKey = ''
    void run()
  }

  // forgetInteractions 去掉从 index 起各模型轮的交互 ID 与缓存统计：这些轮次的上游上下文已变化
  function forgetInteractions(index: number): void {
    for (const turn of session.turns.slice(index)) {
      delete turn.interactionId
      delete turn.cachedTokens
    }
  }

  // editTurn 用新文本替换某一轮的文本片段，保留文本与附件的相对位置，文本有变化时其后的交互不再用于续接
  function editTurn(index: number, text: string): void {
    const turn = session.turns[index]!
    const current = turn.parts
      .filter((part) => part.text !== undefined && !part.thought)
      .map((part) => part.text)
      .join('')
    if (text === current) return
    forgetInteractions(index)
    if (turn.role === 'model') {
      delete turn.tokens
      delete turn.thoughtTokens
    }
    const firstText = turn.parts.findIndex((part) => part.text !== undefined && !part.thought)
    const others = turn.parts.filter((part) => part.text === undefined || part.thought)
    if (text === '') {
      turn.parts = others
    } else if (firstText === -1) {
      turn.parts = [...others, { text }]
    } else {
      const before = turn.parts
        .slice(0, firstText)
        .filter((part) => part.text === undefined || part.thought)
      const after = turn.parts
        .slice(firstText + 1)
        .filter((part) => part.text === undefined || part.thought)
      turn.parts = [...before, { text }, ...after]
    }
    lastTokenKey = ''
    scheduleSave()
    void refreshTokens()
  }

  // deleteTurn 删除一轮，其后的交互不再用于续接
  function deleteTurn(index: number): void {
    removeTurns(index, 1)
    forgetInteractions(index)
    scheduleSave()
    void refreshTokens()
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
      description: session.description,
      data: cloneData({ settings: session.settings, turns: session.turns.slice(0, index + 1) }),
    })
    return id
  }

  // copyPrompt 复制当前对话为新对话并返回其 ID
  async function copyPrompt(): Promise<string> {
    return branch(session.turns.length - 1, 'pg.copyOf')
  }

  // applyStarter 按官网空态示例填写输入框：提示词、参考图片与搜索、代码执行开关，模型支持时思考档位设为 HIGH
  async function applyStarter(item: ZeroStarter): Promise<void> {
    const model = currentModel.value
    if (model?.thinking_levels?.includes('HIGH')) session.settings.thinkingLevel = 'HIGH'
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
        const part: Part = { inlineData: await readImage(await response.blob()) }
        session.attachments.push(part)
        void countAttachment(part)
      }
    } catch (error) {
      const failed = useI18n().t('pg.sampleImageFailed')
      useConsole().showNotice(failureText(error, failed, failed), 'error')
    }
  }

  // rename 修改对话标题与描述
  function rename(title: string, description: string): void {
    session.title = title.trim()
    session.description = description.trim()
    scheduleSave()
  }

  // dispose 中止生成与保存并停止监听
  function dispose(): void {
    discard()
    scope.stop()
  }

  scope.run(() => {
    watchEffect(
      () => {
        const attach = draftAttachmentTokens.value
        const tail = activeModelTurn.value
        const out = outputTokens.value
        const cached = cachedTokens.value
        const tailVisible = tail ? visibleTurnTokens(tail) : 0
        const baseInput =
          countedTotalTokens.value !== null
            ? Math.max(0, countedTotalTokens.value - tailVisible)
            : null
        const input = baseInput !== null || attach > 0 ? (baseInput ?? 0) + attach : null
        session.inputTokens = input
        session.outputTokens = out
        session.cachedTokens = cached
        session.promptTokens = input !== null || out > 0 ? (input ?? 0) + out : null
      },
      { flush: 'sync' },
    )
    watch(
      () => [session.settings, session.temporary] as const,
      ([settings], [previous]) => {
        if (settings !== previous && settings === opened) return
        if ((session.id !== null || session.turns.length > 0) && !session.running) scheduleSave()
      },
      { deep: true },
    )
    watch(
      () => [
        draftSource().text,
        session.settings.systemInstruction,
        session.settings.functionDeclarations,
        session.settings.mediaResolution,
        ...session.settings.tools,
      ],
      () => scheduleTokens(),
    )
    watch(
      () => currentModel.value?.id,
      (id, previous) => {
        lastTokenKey = ''
        lastSystemKey = ''
        if (previous !== undefined && id !== previous) {
          for (const turn of session.turns) {
            delete turn.cachedTokens
            for (const part of turn.parts) {
              if (uploadedVideo(part)) void countAttachment(part)
            }
          }
        }
        for (const part of session.attachments) void countAttachment(part)
        void refreshTokens()
      },
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
    countAttachment,
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

// 关闭桌面窗口时，生成、待写入、写入中或写入失败的对话与未发送的输入保留页面
keepPage(() =>
  panes.some(
    ({ session, pending }) =>
      pending() ||
      session.saveState === 'saving' ||
      session.saveState === 'error' ||
      session.draft.trim() !== '' ||
      session.attachments.length > 0,
  ),
)

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
  if (!value) return
  const text = panes[index]!.session.settings.systemInstruction
  for (const pane of panes.slice(1)) pane.session.settings.systemInstruction = text
}

// running 表示有窗格正在生成
const running = computed(() => panes.some((pane) => pane.session.running))

// addPane 以 data 的运行设置与对话加入右侧窗格并返回它，右侧窗格变化时保存页面对话
function addPane(data: PaneData): Playground {
  const pane = page(
    createSession(true, {
      save: main.save,
      draft: () => ({ text: main.session.draft, attachments: main.session.attachments }),
    }),
  )
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
  pane.session.rawMode = main.session.rawMode
  syncInstructions.value = true
  void pane.refreshTokens()
}

// leaveCompare 关闭右侧窗格回到单对话
function leaveCompare(): void {
  for (const pane of panes.splice(1)) pane.dispose()
}

// closePane 关闭一个窗格：关闭左侧时右侧的对话与运行设置成为页面对话
function closePane(index: number): void {
  const right = panes[1]!
  if (index === 0) {
    main.stop()
    right.stop()
    Object.assign(main.session, {
      settings: right.session.settings,
      turns: right.session.turns,
    })
    void main.refreshTokens()
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

// localPresetsKey 是浏览器本机保存系统指令预设的键，读取时并入对话库后删除
const localPresetsKey = 'playground_system_instructions'

// useInstructionPresets 管理保存在对话库中的系统指令预设
export function useInstructionPresets(
  open: () => boolean,
  getText: () => string,
  setText: (value: string) => void,
) {
  const presets = ref<SystemInstructionPreset[]>([])
  const selected = ref('')
  const title = ref('')
  const saved = ref(true)
  const saveError = ref(false)
  const { errorText } = useI18n()
  const { showNotice } = useConsole()
  let saveTimer = 0

  // currentPreset 是当前选中的系统指令预设
  const currentPreset = computed(() => presets.value.find((preset) => preset.id === selected.value))

  // syncSelection 按当前系统指令匹配预设选中态
  function syncSelection(): void {
    const match = presets.value.find((preset) => preset.text === getText())
    selected.value = match?.id ?? ''
    title.value = match?.title ?? ''
  }

  // persist 等待一次预设写入并更新保存状态
  async function persist(write: Promise<void>): Promise<void> {
    saved.value = false
    saveError.value = false
    try {
      await write
      saved.value = saveTimer === 0
    } catch (cause) {
      saveError.value = true
      showNotice(errorText(cause), 'error')
    }
  }

  // commit 把当前标题与系统指令写入预设列表并保存
  function commit(): void {
    const text = getText()
    if (text.trim() === '' && title.value.trim() === '') {
      saved.value = true
      return
    }
    let preset = currentPreset.value
    if (preset === undefined) {
      preset = { id: newTurnID(), title: '', text: '' }
      presets.value.push(preset)
      selected.value = preset.id
    }
    preset.title = title.value
    preset.text = text
    void persist(api.saveSystemInstruction(preset))
  }

  // flush 立即提交待保存的系统指令预设
  function flush(): void {
    if (saveTimer === 0) return
    window.clearTimeout(saveTimer)
    saveTimer = 0
    commit()
  }

  // remember 在编辑后合并保存当前指令到预设
  function remember(): void {
    saved.value = false
    saveError.value = false
    window.clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => {
      saveTimer = 0
      commit()
    }, 500)
  }

  // choose 切换到另一条预设或新建空白指令
  function choose(id: string | number | undefined): void {
    flush()
    selected.value = String(id ?? '')
    const preset = currentPreset.value
    title.value = preset?.title ?? ''
    setText(preset?.text ?? '')
  }

  // remove 删除当前预设并清空指令
  function remove(): void {
    window.clearTimeout(saveTimer)
    saveTimer = 0
    const id = selected.value
    presets.value = presets.value.filter((preset) => preset.id !== id)
    void persist(api.deleteSystemInstruction(id))
    choose('')
  }

  // refresh 从服务端读取预设，并入浏览器本机保存的预设后匹配当前指令
  async function refresh(): Promise<void> {
    try {
      presets.value = await api.systemInstructions()
      const local = localStorage.getItem(localPresetsKey)
      if (local !== null) {
        const remote = presets.value
        const missing = (JSON.parse(local) as SystemInstructionPreset[]).filter(
          (item) =>
            !remote.some(
              (preset) =>
                preset.id === item.id || (preset.title === item.title && preset.text === item.text),
            ),
        )
        for (const preset of missing) await api.saveSystemInstruction(preset)
        presets.value = [...remote, ...missing]
        localStorage.removeItem(localPresetsKey)
      }
      syncSelection()
    } catch (cause) {
      showNotice(errorText(cause), 'error')
    }
  }

  watch(open, (value) => {
    if (value) void refresh()
    else flush()
  })

  return { presets, selected, title, saved, saveError, currentPreset, remember, choose, remove }
}
