import claudeCodeIcon from '@lobehub/icons-static-svg/icons/claudecode-color.svg?raw'
import codexIcon from '@lobehub/icons-static-svg/icons/codex-color.svg?raw'
import geminiCliIcon from '@lobehub/icons-static-svg/icons/geminicli-color.svg?raw'
import openCodeIcon from '@lobehub/icons-static-svg/icons/opencode.svg?raw'
import openClawIcon from '@lobehub/icons-static-svg/icons/openclaw-color.svg?raw'
import hermesIcon from '@lobehub/icons-static-svg/icons/hermesagent.svg?raw'
import deepSeekIcon from '@lobehub/icons-static-svg/icons/deepseek-color.svg?raw'
import cherryIcon from '@lobehub/icons-static-svg/icons/cherrystudio-color.svg?raw'
import sillyTavernIcon from '@lobehub/icons-static-svg/icons/sillytavern-color.svg?raw'
import openWebuiIcon from '@lobehub/icons-static-svg/icons/openwebui.svg?raw'
import lobeHubIcon from '@lobehub/icons-static-svg/icons/lobehub-color.svg?raw'
import newApiIcon from '@lobehub/icons-static-svg/icons/newapi-color.svg?raw'
import type { TranslationKey } from '@/i18n'
import type { Model } from '@/types'
import ompIcon from './icons/omp.svg?raw'
import piIcon from './icons/pi.svg?raw'

// Shell 是写环境变量的终端
export type Shell = 'unix' | 'windows'

// CherryType 是 Cherry Studio 提供商的接口类型
export type CherryType = 'openai' | 'gemini' | 'anthropic'

// ClientInput 是生成客户端配置所需的服务地址、API key、所选模型、Claude Code 三档模型、全部对话模型、实时目录、终端、Cherry Studio 接口类型与界面文案
export interface ClientInput {
  origin: string
  key: string
  model: Model | undefined
  modelID: string
  tiers: { opus: string; sonnet: string; haiku: string }
  models: Model[]
  catalog: Model[]
  shell: Shell
  cherry: CherryType
  t: (key: TranslationKey) => string
}

// ClientField 是在客户端界面中填写的一个字段：客户端当前语言下的字段名与要填的值，choice 表示从选项中选择
export interface ClientField {
  label: string
  value: string
  choice?: boolean
}

// ClientStep 是客户端配置的一步：code 为要写入文件或在终端运行的内容，fields 为在客户端界面中填写的字段
export type ClientStep =
  | {
      kind: 'code'
      text: TranslationKey
      file?: string
      language: string
      code: string
    }
  | { kind: 'fields'; text: TranslationKey; fields: ClientField[] }

// ClientLink 是一键导入客户端的链接
export interface ClientLink {
  label: TranslationKey
  href: string
}

// Client 是接入页的一个客户端：分组、图标、接入协议、说明、主页与生成配置的方法，noModel 表示配置不使用所选模型
export interface Client {
  id: string
  name: string
  icon: string
  group: 'agent' | 'chat' | 'gateway'
  protocol: string
  summary: TranslationKey
  homepage: string
  noModel?: boolean
  tiers?: boolean
  shell?: boolean
  cherry?: boolean
  docker?: boolean
  steps: (input: ClientInput) => ClientStep[]
  links?: (input: ClientInput) => ClientLink[]
}

// keyVariable 是客户端从环境变量读取 key 时使用的变量名
const keyVariable = 'AISTUDIO2API_KEY'

// providerID 是写入客户端配置的提供商 ID
const providerID = 'aistudio2api'

// envBlock 按终端写出设置环境变量的命令，最后一行是随后运行的命令
function envBlock(shell: Shell, variables: [string, string][], command: string): string {
  const lines = variables.map(([name, value]) =>
    shell === 'windows' ? `$env:${name} = "${value}"` : `export ${name}="${value}"`,
  )
  return [...lines, command].join('\n')
}

// envStep 是在终端设置环境变量后运行命令的一步
function envStep(
  input: ClientInput,
  text: TranslationKey,
  variables: [string, string][],
  command: string,
): ClientStep {
  return {
    kind: 'code',
    text,
    language: 'bash',
    code: envBlock(input.shell, variables, command),
  }
}

// json 把配置写为两空格缩进的 JSON
function json(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

// limits 返回模型的上下文与输出 token 上限，目录没有时取 Gemini 对话模型的常见值
function limits(model: Model | undefined): { context: number; output: number } {
  return {
    context: model?.input_token_limit ?? 1048576,
    output: model?.output_token_limit ?? 65536,
  }
}

// gatewayModels 是 new-api Gemini 渠道能转发的模型：渠道按模型名把请求发到 generateContent、embedContent 或 Imagen 的 predict
function gatewayModels(catalog: Model[]): string[] {
  return catalog
    .filter((model) =>
      model.methods.some((method) =>
        ['generateContent', 'embedContent', 'predict'].includes(method),
      ),
    )
    .map((model) => model.id)
}

// ccSwitch 返回 CC Switch 导入提供商的深度链接，参数按 CC Switch 深度链接协议 v1 以 URLSearchParams 编码
function ccSwitch(app: string, params: Record<string, string>): ClientLink {
  const query = new URLSearchParams({ resource: 'provider', app, name: 'AIStudio2API', ...params })
  return { label: 'access.importCCSwitch', href: `ccswitch://v1/import?${query.toString()}` }
}

// base64 把 UTF-8 文本编码为标准 Base64
function base64(text: string): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(text)))
}

// cherryHost 是 Cherry Studio 各接口类型的 API 地址：OpenAI 带 /v1，Gemini 与 Anthropic 为服务地址
function cherryHost(input: ClientInput): string {
  return input.cherry === 'openai' ? `${input.origin}/v1` : input.origin
}

// claudeVariables 是 Claude Code 使用的环境变量：Anthropic 地址、key、主模型、opus、sonnet、haiku 三档模型与主模型的上下文窗口
function claudeVariables(input: ClientInput): [string, string][] {
  return [
    ['ANTHROPIC_BASE_URL', input.origin],
    ['ANTHROPIC_AUTH_TOKEN', input.key],
    ['ANTHROPIC_MODEL', input.modelID],
    ['ANTHROPIC_DEFAULT_OPUS_MODEL', input.tiers.opus],
    ['ANTHROPIC_DEFAULT_SONNET_MODEL', input.tiers.sonnet],
    ['ANTHROPIC_DEFAULT_HAIKU_MODEL', input.tiers.haiku],
    ['CLAUDE_CODE_MAX_CONTEXT_TOKENS', String(limits(input.model).context)],
  ]
}

// clients 是接入页列出的客户端，配置写法取自各客户端文档与本服务 agent-clients 的实测
export const clients: Client[] = [
  {
    id: 'claude-code',
    name: 'Claude Code',
    icon: claudeCodeIcon,
    group: 'agent',
    protocol: 'Anthropic Messages',
    summary: 'access.client.claude',
    homepage: 'https://github.com/anthropics/claude-code',
    tiers: true,
    shell: true,
    steps: (input) => [
      envStep(input, 'access.step.envRun', claudeVariables(input), 'claude'),
      {
        kind: 'code',
        text: 'access.step.settingsFile',
        file: '~/.claude/settings.json',
        language: 'json',
        code: json({ env: Object.fromEntries(claudeVariables(input)) }),
      },
    ],
    links: (input) => [
      ccSwitch('claude', {
        endpoint: input.origin,
        apiKey: input.key,
        apiFormat: 'anthropic',
        model: input.modelID,
        opusModel: input.tiers.opus,
        sonnetModel: input.tiers.sonnet,
        haikuModel: input.tiers.haiku,
      }),
    ],
  },
  {
    id: 'codex',
    name: 'Codex',
    icon: codexIcon,
    group: 'agent',
    protocol: 'OpenAI Responses',
    summary: 'access.client.codex',
    homepage: 'https://github.com/openai/codex',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.configFile',
        file: '~/.codex/config.toml',
        language: 'ini',
        code: [
          `model = "${input.modelID}"`,
          `model_provider = "${providerID}"`,
          `model_context_window = ${limits(input.model).context}`,
          '',
          `[model_providers.${providerID}]`,
          'name = "AIStudio2API"',
          `base_url = "${input.origin}/v1"`,
          `env_key = "${keyVariable}"`,
          'wire_api = "responses"',
        ].join('\n'),
      },
      envStep(input, 'access.step.keyRun', [[keyVariable, input.key]], 'codex'),
    ],
    links: (input) => [
      ccSwitch('codex', {
        endpoint: `${input.origin}/v1`,
        apiKey: input.key,
        apiFormat: 'openai_responses',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'gemini-cli',
    name: 'Gemini CLI',
    icon: geminiCliIcon,
    group: 'agent',
    protocol: 'Gemini API',
    summary: 'access.client.geminiCli',
    homepage: 'https://github.com/google-gemini/gemini-cli',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.geminiAuth',
        file: '~/.gemini/settings.json',
        language: 'json',
        code: json({ security: { auth: { selectedType: 'gemini-api-key' } } }),
      },
      envStep(
        input,
        'access.step.envRun',
        [
          ['GOOGLE_GEMINI_BASE_URL', input.origin],
          ['GEMINI_API_KEY', input.key],
          ['GEMINI_MODEL', input.modelID],
        ],
        'gemini',
      ),
      {
        kind: 'code',
        text: 'access.step.envFile',
        file: '~/.gemini/.env',
        language: 'bash',
        code: [
          `GOOGLE_GEMINI_BASE_URL=${input.origin}`,
          `GEMINI_API_KEY=${input.key}`,
          `GEMINI_MODEL=${input.modelID}`,
        ].join('\n'),
      },
    ],
    links: (input) => [
      ccSwitch('gemini', {
        endpoint: input.origin,
        apiKey: input.key,
        apiFormat: 'gemini_native',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'opencode',
    name: 'OpenCode',
    icon: openCodeIcon,
    group: 'agent',
    protocol: 'OpenAI Chat Completions',
    summary: 'access.client.opencode',
    homepage: 'https://opencode.ai',
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.configFile',
        file: '~/.config/opencode/opencode.json',
        language: 'json',
        code: json({
          $schema: 'https://opencode.ai/config.json',
          provider: {
            [providerID]: {
              npm: '@ai-sdk/openai-compatible',
              name: 'AIStudio2API',
              options: { baseURL: `${input.origin}/v1`, apiKey: input.key },
              models: Object.fromEntries(
                input.models.map((model) => [
                  model.id,
                  {
                    name: model.name,
                    limit: { context: limits(model).context, output: limits(model).output },
                  },
                ]),
              ),
            },
          },
          model: `${providerID}/${input.modelID}`,
        }),
      },
    ],
    links: (input) => [
      ccSwitch('opencode', {
        endpoint: `${input.origin}/v1`,
        apiKey: input.key,
        apiFormat: 'openai_chat',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'openclaw',
    name: 'OpenClaw',
    icon: openClawIcon,
    group: 'agent',
    protocol: 'OpenAI Chat Completions',
    summary: 'access.client.openclaw',
    homepage: 'https://github.com/openclaw/openclaw',
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.configFile',
        file: '~/.openclaw/openclaw.json',
        language: 'json',
        code: json({
          agents: { defaults: { model: { primary: `${providerID}/${input.modelID}` } } },
          models: {
            providers: {
              [providerID]: {
                baseUrl: `${input.origin}/v1`,
                apiKey: input.key,
                api: 'openai-completions',
                models: [
                  {
                    id: input.modelID,
                    name: input.model?.name ?? input.modelID,
                    reasoning: true,
                    contextWindow: limits(input.model).context,
                    maxTokens: limits(input.model).output,
                  },
                ],
              },
            },
          },
        }),
      },
    ],
    links: (input) => [
      ccSwitch('openclaw', {
        endpoint: `${input.origin}/v1`,
        apiKey: input.key,
        apiFormat: 'openai_chat',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'hermes',
    name: 'Hermes',
    icon: hermesIcon,
    group: 'agent',
    protocol: 'OpenAI Chat Completions',
    summary: 'access.client.hermes',
    homepage: 'https://github.com/NousResearch/hermes-agent',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.configFile',
        file: '~/.hermes/config.yaml',
        language: 'yaml',
        code: [
          'model:',
          `  default: ${input.modelID}`,
          `  provider: ${providerID}`,
          'providers:',
          `  ${providerID}:`,
          `    base_url: ${input.origin}/v1`,
          `    key_env: ${keyVariable}`,
          '    api_mode: chat_completions',
        ].join('\n'),
      },
      envStep(input, 'access.step.keyRun', [[keyVariable, input.key]], 'hermes'),
    ],
    links: (input) => [
      ccSwitch('hermes', {
        endpoint: `${input.origin}/v1`,
        apiKey: input.key,
        apiFormat: 'openai_chat',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'pi',
    name: 'pi',
    icon: piIcon,
    group: 'agent',
    protocol: 'OpenAI Chat Completions',
    summary: 'access.client.pi',
    homepage: 'https://github.com/badlogic/pi-mono',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.configFile',
        file: '~/.pi/agent/models.json',
        language: 'json',
        code: json({
          providers: {
            [providerID]: {
              baseUrl: `${input.origin}/v1`,
              api: 'openai-completions',
              apiKey: keyVariable,
              models: [
                {
                  id: input.modelID,
                  name: input.model?.name ?? input.modelID,
                  reasoning: true,
                  input: ['text', 'image'],
                  contextWindow: limits(input.model).context,
                  maxTokens: limits(input.model).output,
                },
              ],
            },
          },
        }),
      },
      envStep(
        input,
        'access.step.keyRun',
        [[keyVariable, input.key]],
        `pi --provider ${providerID} --model ${input.modelID}`,
      ),
    ],
    links: (input) => [
      ccSwitch('pi', {
        endpoint: `${input.origin}/v1`,
        apiKey: input.key,
        apiFormat: 'openai_chat',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'omp',
    name: 'omp',
    icon: ompIcon,
    group: 'agent',
    protocol: 'OpenAI Chat Completions · Gemini API',
    summary: 'access.client.omp',
    homepage: 'https://github.com/can1357/oh-my-pi',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.configFile',
        file: '~/.omp/agent/models.yml',
        language: 'yaml',
        code: [
          'providers:',
          `  ${providerID}:`,
          `    baseUrl: ${input.origin}/v1`,
          '    api: openai-completions',
          `    apiKey: ${keyVariable}`,
          '    models:',
          `      - id: ${input.modelID}`,
          `  ${providerID}-gemini:`,
          `    baseUrl: ${input.origin}/v1beta`,
          '    api: google-generative-ai',
          `    apiKey: ${keyVariable}`,
          '    models:',
          `      - id: ${input.modelID}`,
        ].join('\n'),
      },
      {
        kind: 'code',
        text: 'access.step.ompSearch',
        file: '~/.omp/agent/config.yml',
        language: 'yaml',
        code: ['modelRoles:', `  web: ${providerID}-gemini/${input.modelID}`].join('\n'),
      },
      envStep(
        input,
        'access.step.keyRun',
        [[keyVariable, input.key]],
        `omp --provider ${providerID} --model ${input.modelID}`,
      ),
    ],
  },
  {
    id: 'dsh',
    name: 'dsh',
    icon: deepSeekIcon,
    group: 'agent',
    protocol: 'OpenAI Chat Completions',
    summary: 'access.client.dsh',
    homepage: 'https://www.npmjs.com/package/@deepseek-ai/dsh',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'access.step.patchFile',
        file: 'aistudio2api.patch.yml',
        language: 'yaml',
        code: [
          '- id: llm-pi-ai',
          '  config:',
          '    providers:',
          `      ${providerID}:`,
          '        api: openai-completions',
          `        baseURL: ${input.origin}/v1`,
          `        apiKeyEnv: ${keyVariable}`,
          '        models:',
          `          - id: ${input.modelID}`,
          `            contextWindow: ${limits(input.model).context}`,
          '- id: agent-default-model',
          '  config:',
          `    provider: ${providerID}`,
          `    model: ${input.modelID}`,
        ].join('\n'),
      },
      envStep(
        input,
        'access.step.keyRun',
        [[keyVariable, input.key]],
        'dsh --patch aistudio2api.patch.yml',
      ),
    ],
  },
  {
    id: 'cherry-studio',
    name: 'Cherry Studio',
    icon: cherryIcon,
    group: 'chat',
    protocol: 'OpenAI · Gemini · Anthropic',
    summary: 'access.client.cherry',
    homepage: 'https://www.cherry-ai.com',
    noModel: true,
    cherry: true,
    steps: (input) => [
      {
        kind: 'fields',
        text: 'access.step.cherry',
        fields: [
          {
            label: input.t('access.field.cherryType'),
            value: { openai: 'OpenAI', gemini: 'Gemini', anthropic: 'Anthropic' }[input.cherry],
            choice: true,
          },
          { label: input.t('access.field.cherryKey'), value: input.key },
          { label: input.t('access.field.cherryHost'), value: cherryHost(input) },
        ],
      },
    ],
    links: (input) => [
      {
        label: 'access.importCherry',
        href: `cherrystudio://providers/api-keys?v=1&data=${encodeURIComponent(
          base64(
            JSON.stringify({
              id: providerID,
              baseUrl: cherryHost(input),
              apiKey: input.key,
              name: 'AIStudio2API',
              type: input.cherry,
            }),
          ),
        )}`,
      },
    ],
  },
  {
    id: 'sillytavern',
    name: 'SillyTavern',
    icon: sillyTavernIcon,
    group: 'chat',
    protocol: 'Gemini API',
    summary: 'access.client.sillytavern',
    homepage: 'https://github.com/SillyTavern/SillyTavern',
    docker: true,
    steps: (input) => [
      {
        kind: 'fields',
        text: 'access.step.sillytavern',
        fields: [
          { label: 'API', value: input.t('access.field.stChat'), choice: true },
          { label: input.t('access.field.stSource'), value: 'Google AI Studio', choice: true },
          { label: input.t('access.field.stUrl'), value: input.origin },
          { label: input.t('access.field.stPassword'), value: input.key },
          { label: input.t('access.field.stModel'), value: input.modelID },
        ],
      },
    ],
  },
  {
    id: 'open-webui',
    name: 'Open WebUI',
    icon: openWebuiIcon,
    group: 'chat',
    protocol: 'OpenAI Chat Completions',
    summary: 'access.client.openWebui',
    homepage: 'https://github.com/open-webui/open-webui',
    noModel: true,
    docker: true,
    steps: (input) => [
      {
        kind: 'fields',
        text: 'access.step.openWebui',
        fields: [
          { label: 'URL', value: `${input.origin}/v1` },
          { label: input.t('access.field.webuiKey'), value: input.key },
        ],
      },
      {
        kind: 'code',
        text: 'access.step.dockerEnv',
        file: 'Docker',
        language: 'bash',
        code: [`OPENAI_API_BASE_URL=${input.origin}/v1`, `OPENAI_API_KEY=${input.key}`].join('\n'),
      },
    ],
  },
  {
    id: 'lobechat',
    name: 'LobeChat',
    icon: lobeHubIcon,
    group: 'chat',
    protocol: 'Gemini API',
    summary: 'access.client.lobechat',
    homepage: 'https://github.com/lobehub/lobe-chat',
    noModel: true,
    docker: true,
    steps: (input) => [
      {
        kind: 'fields',
        text: 'access.step.lobechat',
        fields: [
          { label: input.t('access.field.lobeKey'), value: input.key },
          { label: input.t('access.field.lobeUrl'), value: input.origin },
        ],
      },
      {
        kind: 'code',
        text: 'access.step.dockerEnv',
        file: 'Docker',
        language: 'bash',
        code: [`GOOGLE_API_KEY=${input.key}`, `GOOGLE_PROXY_URL=${input.origin}`].join('\n'),
      },
    ],
  },
  {
    id: 'new-api',
    name: 'New API',
    icon: newApiIcon,
    group: 'gateway',
    protocol: 'Gemini API',
    summary: 'access.client.newApi',
    homepage: 'https://github.com/QuantumNous/new-api',
    noModel: true,
    docker: true,
    steps: (input) => [
      {
        kind: 'fields',
        text: 'access.step.newApi',
        fields: [
          { label: input.t('access.field.newapiProvider'), value: 'Gemini', choice: true },
          { label: input.t('access.field.newapiUrl'), value: input.origin },
          { label: input.t('access.field.newapiKey'), value: input.key },
          {
            label: input.t('access.field.newapiModels'),
            value: gatewayModels(input.catalog).join(','),
          },
        ],
      },
    ],
  },
]
