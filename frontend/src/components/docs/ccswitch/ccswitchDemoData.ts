import {
  CCS_LOCAL_ASSETS,
  SVG_CLAUDE,
  SVG_COPILOT,
  SVG_DEEPSEEK,
  SVG_GEMINI,
  SVG_GROK,
  SVG_KIMI_APP,
  SVG_KIMI_PROVIDER,
  SVG_MINIMAX,
  SVG_OMP,
  SVG_OPENCLAW,
  SVG_OPENCODE,
  SVG_OPENAI,
  SVG_OPENROUTER,
  SVG_PI,
  SVG_SITE_GATEWAY,
  SVG_WORKBUDDY,
  SVG_ZHIPU,
} from './ccswitchIcons'
import type {
  CcsAppRuntimeState,
  CcsAppSpec,
  CcsAuthAccount,
  CcsDiscoverSkill,
  CcsInstalledAppRow,
  CcsInstalledSkill,
  CcsLocale,
  CcsMcpServer,
  CcsPromptItem,
  CcsProviderItem,
  CcsSessionItem,
  CcsUsageMetric,
  CcsUsageRange,
} from './ccswitchTypes'

function svgDataUrl(svg: string): string {
  return `data:image/svg+xml,${encodeURIComponent(svg)}`
}

const DATA_CLAUDE = svgDataUrl(SVG_CLAUDE)
const DATA_GEMINI = svgDataUrl(SVG_GEMINI)
const DATA_MINIMAX = svgDataUrl(SVG_MINIMAX)
const DATA_DEEPSEEK = svgDataUrl(SVG_DEEPSEEK)
const DATA_WORKBUDDY = svgDataUrl(SVG_WORKBUDDY)
const DATA_OPENCODE = svgDataUrl(SVG_OPENCODE)
const DATA_OPENCLAW = svgDataUrl(SVG_OPENCLAW)
const DATA_OMP = svgDataUrl(SVG_OMP)

export const SITE_PROVIDER_PLACEHOLDER = '__SITE__'

const KIMI_HREF = {
  zh: 'https://www.kimi.com/code',
  en: 'https://www.kimi.ai/code',
}

const CLAUDE_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'claude-official',
    iconSvg: SVG_CLAUDE,
    iconBg: 'bg-blue-500/20',
    iconColor: '#D4915D',
    name: 'Claude Official',
    official: true,
    subtitle: 'https://www.anthropic.com/claude-code · Max 20x',
    href: 'https://www.anthropic.com/claude-code',
    time: '1',
    quota: {
      updatedMinutes: 1,
      tiers: [
        { label: '5h', utilization: 36, resetsIn: '2h10m' },
        { label: '7d', utilization: 64, resetsIn: '3d8h' },
        { label: 'fable', utilization: 48, resetsIn: '3d8h' },
      ],
    },
  },
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    href: KIMI_HREF,
    time: '3',
    quota: {
      updatedMinutes: 3,
      tiers: [
        { label: '5h', utilization: 42, resetsIn: '3h35m' },
        { label: '7d', utilization: 18, resetsIn: '5d20h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const CLAUDE_DESKTOP_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'claude-desktop-official',
    iconSvg: SVG_CLAUDE,
    iconBg: 'bg-amber-500/20',
    iconColor: '#D4915D',
    name: 'Claude Desktop Official',
    official: true,
    subtitle: 'https://claude.ai/download',
  },
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    href: KIMI_HREF,
    time: '4',
    quota: {
      updatedMinutes: 4,
      tiers: [
        { label: '5h', utilization: 25, resetsIn: '4h10m' },
        { label: '7d', utilization: 33, resetsIn: '4d2h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const CODEX_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'openai-official',
    iconSvg: SVG_OPENAI,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'OpenAI Official',
    official: true,
    subtitle: 'https://chatgpt.com/codex',
    time: '2',
    quota: {
      updatedMinutes: 2,
      tiers: [
        { label: '5h', utilization: 51, resetsIn: '1h50m' },
        { label: '7d', utilization: 23, resetsIn: '5d12h' },
      ],
    },
  },
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    href: KIMI_HREF,
    time: '5',
    quota: {
      updatedMinutes: 5,
      tiers: [
        { label: '5h', utilization: 19, resetsIn: '4h25m' },
        { label: '7d', utilization: 46, resetsIn: '2d9h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const GEMINI_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'google-official',
    iconSvg: SVG_GEMINI,
    iconBg: 'bg-blue-500/20',
    name: 'Google Official',
    official: true,
    subtitle: 'https://ai.google.dev/',
    time: '4',
    quota: {
      updatedMinutes: 4,
      tiers: [
        { label: 'Pro', utilization: 22, resetsIn: '14h' },
        { label: 'Flash', utilization: 8, resetsIn: '14h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const OPENCODE_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    href: KIMI_HREF,
    time: '2',
    quota: {
      updatedMinutes: 2,
      tiers: [
        { label: '5h', utilization: 33, resetsIn: '2h45m' },
        { label: '7d', utilization: 21, resetsIn: '5d6h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const OPENCLAW_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    href: KIMI_HREF,
    time: '8',
    quota: {
      updatedMinutes: 8,
      tiers: [
        { label: '5h', utilization: 72, resetsIn: '45m' },
        { label: '7d', utilization: 27, resetsIn: '4d6h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const HERMES_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'nous-research',
    icon: CCS_LOCAL_ASSETS.hermes,
    iconBg: 'bg-violet-500/20',
    name: 'Nous Research',
    subtitle: 'https://nousresearch.com',
  },
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    time: '3',
    quota: {
      updatedMinutes: 3,
      tiers: [
        { label: '5h', utilization: 14, resetsIn: '4h40m' },
        { label: '7d', utilization: 37, resetsIn: '3d15h' },
      ],
    },
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const GROK_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'grok-official',
    iconSvg: SVG_GROK,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Grok Official',
    official: true,
    subtitle: 'https://x.ai/grok',
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const PI_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'kimi-for-coding',
    iconSvg: SVG_KIMI_PROVIDER,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: 'Kimi For Coding',
    subtitle: 'https://www.kimi.com/code',
    time: '3',
    quota: {
      updatedMinutes: 3,
      tiers: [
        { label: '5h', utilization: 27, resetsIn: '4h05m' },
        { label: '7d', utilization: 41, resetsIn: '2d18h' },
      ],
    },
  },
  {
    id: 'zhipu-glm',
    iconSvg: SVG_ZHIPU,
    iconBg: 'bg-blue-500/20',
    iconColor: '#0F62FE',
    name: 'Zhipu GLM',
    subtitle: 'https://open.bigmodel.cn',
  },
  {
    id: 'openrouter',
    iconSvg: SVG_OPENROUTER,
    iconBg: 'bg-orange-500/20',
    iconColor: '#6566F1',
    name: 'OpenRouter',
    subtitle: 'https://openrouter.ai',
  },
]

const MCODE_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'minimax',
    iconSvg: SVG_MINIMAX,
    iconBg: 'bg-rose-500/20',
    name: 'MiniMax',
    subtitle: 'https://platform.minimax.cn',
    time: '2',
    quota: {
      updatedMinutes: 2,
      tiers: [
        { label: '5h', utilization: 38, resetsIn: '3h20m' },
        { label: '7d', utilization: 15, resetsIn: '5d9h' },
      ],
    },
  },
  ...PI_PROVIDERS.filter((p) => p.name !== 'Zhipu GLM'),
  {
    id: 'deepseek',
    iconSvg: SVG_DEEPSEEK,
    iconBg: 'bg-blue-500/15',
    name: 'DeepSeek',
    subtitle: 'https://platform.deepseek.com',
  },
]

const KIMI_CODE_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'kimi-official',
    iconSvg: SVG_KIMI_APP,
    iconBg: 'bg-sky-500/15',
    iconColor: 'currentColor',
    name: 'Kimi Official',
    official: true,
    subtitle: 'https://www.kimi.com/code',
    time: '2',
    quota: {
      updatedMinutes: 2,
      tiers: [
        { label: '5h', utilization: 31, resetsIn: '3h40m' },
        { label: '7d', utilization: 46, resetsIn: '4d6h' },
      ],
    },
  },
  ...PI_PROVIDERS.filter((p) => p.name !== 'Kimi For Coding' && p.name !== 'OpenRouter'),
  {
    id: 'deepseek',
    iconSvg: SVG_DEEPSEEK,
    iconBg: 'bg-blue-500/15',
    name: 'DeepSeek',
    subtitle: 'https://platform.deepseek.com',
  },
  ...PI_PROVIDERS.filter((p) => p.name === 'OpenRouter'),
]

const DSH_ACCOUNT = 'dev@example.com'
const DSH_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'dsh-official',
    iconSvg: SVG_DEEPSEEK,
    iconBg: 'bg-white',
    logoOnWhite: true,
    name: DSH_ACCOUNT,
    official: true,
    subtitle: 'https://platform.deepseek.com',
    caption: { zh: '官方账号 · Web 和 Desktop 共用', en: 'Official account · shared by Web and Desktop' },
    aside: { zh: '余额 ¥86.40', en: 'Balance ¥86.40' },
  },
  ...PI_PROVIDERS,
]

const COPILOT_OFFICIAL = 'GitHub Copilot'
const COPILOT_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'copilot-official',
    iconSvg: SVG_COPILOT,
    iconBg: 'bg-slate-500/20',
    iconColor: 'currentColor',
    name: COPILOT_OFFICIAL,
    official: true,
    subtitle: 'https://github.com/features/copilot/cli',
  },
  ...PI_PROVIDERS,
]

const WORKBUDDY_NAMES = ['WorkBuddy', 'WorkBuddy · Acme']
const WORKBUDDY_PROVIDERS: CcsProviderItem[] = [
  {
    id: 'workbuddy-personal',
    iconSvg: SVG_WORKBUDDY,
    iconBg: 'bg-teal-500/15',
    name: WORKBUDDY_NAMES[0],
    official: true,
    subtitle: '',
    caption: { zh: 'wb-dev · 专业版', en: 'wb-dev · Pro' },
    aside: { zh: '积分 剩余 1,240 / 2,000', en: 'Credits 1,240 / 2,000 left' },
  },
  {
    id: 'workbuddy-team',
    iconSvg: SVG_WORKBUDDY,
    iconBg: 'bg-teal-500/15',
    name: WORKBUDDY_NAMES[1],
    official: true,
    subtitle: '',
    caption: { zh: 'acme-team · 团队版', en: 'acme-team · Team' },
    aside: { zh: '积分 剩余 8,600 / 10,000', en: 'Credits 8,600 / 10,000 left' },
  },
  ...PI_PROVIDERS,
]

export const CC_SWITCH_DEMO_APPS: CcsAppSpec[] = [
  {
    id: 'claude',
    label: 'Claude Code',
    icon: DATA_CLAUDE,
    badge: 'terminal',
    kind: 'switch',
    modes: ['direct', 'route', 'stack'],
    providers: CLAUDE_PROVIDERS,
    stack: { default: 'Kimi For Coding', members: { 'Kimi For Coding': 2, OpenRouter: 3 } },
  },
  {
    id: 'claude-desktop',
    label: 'Claude Desktop',
    icon: DATA_CLAUDE,
    badge: 'monitor',
    kind: 'switch',
    modes: ['direct', 'route', 'stack'],
    providers: CLAUDE_DESKTOP_PROVIDERS,
    initialMode: 'route',
    stack: { default: 'Kimi For Coding', members: { 'Kimi For Coding': 2, OpenRouter: 3 } },
    needsRoute: ['OpenRouter'],
  },
  {
    id: 'codex',
    label: 'Codex',
    icon: '',
    iconSvg: SVG_OPENAI,
    kind: 'switch',
    modes: ['direct', 'route', 'stack'],
    providers: CODEX_PROVIDERS,
    initialMode: 'stack',
    stack: { default: 'OpenAI Official', members: { 'OpenAI Official': 6, 'Kimi For Coding': 2, OpenRouter: 3 } },
    needsRoute: ['OpenRouter'],
  },
  {
    id: 'gemini',
    label: 'Gemini CLI',
    icon: DATA_GEMINI,
    kind: 'switch',
    modes: ['direct', 'route'],
    providers: GEMINI_PROVIDERS,
  },
  {
    id: 'grokbuild',
    label: 'Grok Build',
    icon: '',
    iconSvg: SVG_GROK,
    kind: 'switch',
    modes: ['direct', 'route'],
    providers: GROK_PROVIDERS,
  },
  {
    id: 'opencode',
    label: 'OpenCode',
    icon: DATA_OPENCODE,
    kind: 'additive',
    providers: OPENCODE_PROVIDERS,
    added: ['Kimi For Coding'],
  },
  {
    id: 'openclaw',
    label: 'OpenClaw',
    icon: DATA_OPENCLAW,
    kind: 'additive',
    providers: OPENCLAW_PROVIDERS,
    added: ['Kimi For Coding'],
  },
  {
    id: 'hermes',
    label: 'Hermes',
    icon: CCS_LOCAL_ASSETS.hermes,
    kind: 'additive',
    providers: HERMES_PROVIDERS,
    added: ['Nous Research', 'Kimi For Coding'],
  },
  {
    id: 'pi',
    label: 'Pi',
    icon: '',
    iconSvg: SVG_PI,
    kind: 'additive',
    providers: PI_PROVIDERS,
    added: ['Kimi For Coding', 'Zhipu GLM'],
    enableLabel: true,
  },
  {
    id: 'mcode',
    label: 'MiniMax Code',
    icon: DATA_MINIMAX,
    kind: 'additive',
    providers: MCODE_PROVIDERS,
    added: ['MiniMax', 'Kimi For Coding'],
  },
  {
    id: 'kimi',
    label: 'Kimi Code',
    icon: '',
    iconSvg: SVG_KIMI_APP,
    kind: 'additive',
    providers: KIMI_CODE_PROVIDERS,
    added: ['Kimi Official', 'Zhipu GLM'],
  },
  {
    id: 'dsh',
    label: 'DeepSeek Harness',
    icon: DATA_DEEPSEEK,
    kind: 'additive',
    providers: DSH_PROVIDERS,
    added: [DSH_ACCOUNT, 'Kimi For Coding'],
    pinned: { [DSH_ACCOUNT]: 'none' },
    sharedList: true,
  },
  {
    id: 'agy',
    label: 'Antigravity',
    icon: CCS_LOCAL_ASSETS.antigravity,
    kind: 'launch',
    providers: GEMINI_PROVIDERS,
  },
  {
    id: 'omp',
    label: 'Oh My Pi',
    icon: DATA_OMP,
    kind: 'additive',
    providers: PI_PROVIDERS,
    added: ['Kimi For Coding'],
    enableLabel: true,
  },
  {
    id: 'copilot',
    label: 'Copilot CLI',
    icon: '',
    iconSvg: SVG_COPILOT,
    kind: 'additive',
    providers: COPILOT_PROVIDERS,
    added: [COPILOT_OFFICIAL, 'Kimi For Coding'],
    pinned: { [COPILOT_OFFICIAL]: 'openTerminal' },
  },
  {
    id: 'workbuddy',
    label: 'WorkBuddy',
    icon: DATA_WORKBUDDY,
    kind: 'additive',
    providers: WORKBUDDY_PROVIDERS,
    added: [WORKBUDDY_NAMES[0], 'Kimi For Coding'],
    exclusive: WORKBUDDY_NAMES,
  },
]

export const CC_SWITCH_APP_MAP: Record<string, CcsAppSpec> = Object.fromEntries(
  CC_SWITCH_DEMO_APPS.map((app) => [app.id, app]),
)

export const AGENT_TO_DEMO_APP: Record<string, string> = {
  'claude-code': 'claude',
  'claude-desktop': 'claude-desktop',
  codex: 'codex',
  'gemini-cli': 'gemini',
  'grok-build': 'grokbuild',
  opencode: 'opencode',
  openclaw: 'openclaw',
  'ccswitch-ecosystem': 'hermes',
}

export function isRouteOrStackBlocked(appId: string, provider: CcsProviderItem): boolean {
  return Boolean(provider.official) && appId !== 'codex'
}

export function providerNeedsRoute(app: CcsAppSpec, provider: CcsProviderItem): boolean {
  return app.needsRoute?.includes(provider.name) ?? false
}

export function createInitialAppState(app: CcsAppSpec): CcsAppRuntimeState {
  const first = app.providers[0]?.name ?? ''
  const firstNonBlocked = app.providers.find((p) => !isRouteOrStackBlocked(app.id, p))?.name ?? first
  const stackDefault = app.stack?.default ?? firstNonBlocked
  const stackMembers = { ...(app.stack?.members ?? {}) }
  delete stackMembers[stackDefault]
  const initialMode = app.initialMode ?? 'direct'
  return {
    active: initialMode,
    view: initialMode,
    direct: first,
    route: initialMode === 'stack' ? stackDefault : firstNonBlocked,
    stackDefault,
    stack: stackMembers,
    added: [...(app.added ?? [])],
  }
}

export function createInitialAppStates(): Record<string, CcsAppRuntimeState> {
  return Object.fromEntries(CC_SWITCH_DEMO_APPS.map((app) => [app.id, createInitialAppState(app)]))
}

export function getAppDefaultEndpoint(appId: string, origin: string): string {
  const clean = origin.replace(/\/+$/, '')
  if (appId === 'grokbuild') return `${clean}/v1`
  return clean
}

export function getAppDefaultModel(appId: string): string {
  switch (appId) {
    case 'codex':
      return 'gpt-5.6-sol'
    case 'gemini':
    case 'agy':
      return 'gemini-2.5-pro'
    case 'grokbuild':
      return 'grok-4.6'
    case 'dsh':
      return 'deepseek-v4-pro'
    case 'kimi':
      return 'kimi-k3'
    case 'mcode':
      return 'MiniMax-M3'
    default:
      return 'claude-sonnet-4-6'
  }
}

export function createSiteProvider(
  origin: string,
  siteName: string,
  appId: string,
  maskedKeyPreview?: string,
  customEndpoint?: string,
  customModel?: string,
): CcsProviderItem {
  const cleanName = siteName.trim() || 'kedaya.ai'
  return {
    id: `site-provider-${cleanName.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
    name: cleanName,
    iconSvg: SVG_SITE_GATEWAY,
    iconBg: 'bg-blue-500/15',
    isSiteProvider: true,
    modelBadge: customModel?.trim() || getAppDefaultModel(appId),
    maskedKeyPreview: maskedKeyPreview?.trim() || '',
    subtitle: customEndpoint?.trim() || getAppDefaultEndpoint(appId, origin),
    remaining: '128.64',
  }
}

export const INITIAL_APPS_INVENTORY: CcsInstalledAppRow[] = [
  {
    app: 'claude',
    source: 'Homebrew',
    path: '/opt/homebrew/bin/claude',
    version: '2.4.1',
    latest: '2.4.3',
    conflict: { path: '~/.npm-global/bin/claude', version: '2.3.9', source: 'npm' },
  },
  { app: 'claude-desktop' },
  { app: 'codex', source: 'npm', path: '~/.npm-global/bin/codex', version: '0.142.0' },
  { app: 'gemini', source: 'npm', path: '~/.npm-global/bin/gemini', version: '0.31.2', latest: '0.32.0' },
  { app: 'grokbuild', source: 'npm', path: '~/.npm-global/bin/grok', version: '0.1.6' },
  { app: 'opencode', source: 'Homebrew', path: '/opt/homebrew/bin/opencode', version: '1.4.7' },
  { app: 'openclaw', installHint: 'npm' },
  { app: 'hermes', path: '~/.local/bin/hermes', version: '0.9.2' },
  { app: 'pi', source: 'npm', path: '~/.npm-global/bin/pi', version: '1.0.4' },
  { app: 'mcode', installHint: 'script' },
  { app: 'kimi', path: '~/.local/bin/kimi', version: '1.6.0' },
  { app: 'agy', path: '~/.local/bin/agy', version: '0.4.2' },
  { app: 'dsh', source: 'npm', path: '~/.npm-global/bin/dsh', version: '0.8.4' },
  { app: 'omp', source: 'npm', path: '~/.npm-global/bin/omp', version: '0.12.3' },
  { app: 'copilot', installHint: 'npm' },
  { app: 'workbuddy' },
]

export const MATRIX_APPS: string[] = [
  'claude',
  'codex',
  'gemini',
  'grokbuild',
  'opencode',
  'hermes',
  'pi',
  'mcode',
  'kimi',
  'dsh',
  'agy',
  'omp',
  'copilot',
  'workbuddy',
]

export const INITIAL_MCP_SERVERS: CcsMcpServer[] = [
  { id: 'context7', transport: 'http', summary: 'mcp.context7.com/mcp', apps: ['claude', 'codex', 'grokbuild', 'opencode', 'kimi', 'dsh', 'omp', 'copilot'] },
  { id: 'fetch', transport: 'stdio', summary: 'uvx mcp-server-fetch', apps: ['claude', 'codex', 'gemini', 'hermes'] },
  { id: 'figma', transport: 'http', summary: '127.0.0.1:3845/mcp', apps: ['claude'] },
  { id: 'github', transport: 'http', summary: 'api.githubcopilot.com/mcp/', apps: ['claude', 'codex', 'opencode', 'mcode', 'agy', 'workbuddy'] },
  { id: 'memory', transport: 'stdio', summary: 'npx -y @modelcontextprotocol/server-memory', apps: [] },
  { id: 'playwright', transport: 'stdio', summary: 'npx -y @playwright/mcp@latest', apps: ['claude', 'codex'] },
  { id: 'sequential-thinking', transport: 'stdio', summary: 'npx -y @modelcontextprotocol/server-sequential-thinking', apps: ['claude', 'codex', 'gemini'] },
  { id: 'serena', transport: 'stdio', summary: 'uvx --from git+https://github.com/oraios/serena serena start-mcp-server', apps: ['claude', 'codex', 'hermes', 'pi', 'omp'] },
]

export const INITIAL_INSTALLED_SKILLS: CcsInstalledSkill[] = [
  { name: 'baoyu-cover-image', description: { zh: '为文章生成封面图', en: 'Generate cover images for articles' }, repo: 'JimLiu/baoyu-skills', apps: ['claude', 'codex', 'gemini'] },
  { name: 'brainstorming', description: { zh: '写代码前先把需求和方案聊清楚', en: 'Talk through requirements and the plan before coding' }, repo: 'obra/superpowers', apps: ['claude', 'codex', 'opencode', 'mcode', 'kimi'] },
  { name: 'frontend-design', description: { zh: '设计并实现有辨识度的前端界面', en: 'Design and build distinctive front-end interfaces' }, repo: 'anthropics/skills', apps: ['claude', 'codex', 'opencode'], update: true },
  { name: 'mcp-builder', description: { zh: '编写高质量的 MCP 服务器', en: 'Build high-quality MCP servers' }, repo: 'anthropics/skills', apps: [] },
  { name: 'pdf', description: { zh: '提取、合并和填写 PDF', en: 'Extract, merge and fill in PDFs' }, repo: 'anthropics/skills', apps: ['claude', 'codex', 'gemini', 'hermes'], update: true },
  { name: 'systematic-debugging', description: { zh: '先定位根因再动手修', en: 'Find the root cause before fixing' }, repo: 'obra/superpowers', apps: ['claude', 'codex', 'grokbuild', 'pi', 'agy', 'omp'], update: true },
  { name: 'team-conventions', description: { zh: '团队的提交与代码规范', en: "Your team's commit and code conventions" }, repo: null, apps: ['claude', 'codex', 'opencode', 'hermes', 'pi', 'mcode', 'kimi', 'dsh', 'omp', 'copilot', 'workbuddy'] },
]

export const DISCOVER_SKILLS: CcsDiscoverSkill[] = [
  { name: 'canvas-design', repo: 'anthropics/skills', installs: '18.4K', description: { zh: '用设计原则做海报和视觉稿', en: 'Make posters and visuals with design principles' } },
  { name: 'docx', repo: 'anthropics/skills', installs: '31.2K', description: { zh: '读写 Word 文档，保留修订和批注', en: 'Read and write Word documents, keeping tracked changes' } },
  { name: 'pptx', repo: 'anthropics/skills', installs: '27.9K', description: { zh: '生成和编辑演示文稿', en: 'Create and edit presentations' } },
  { name: 'skill-creator', repo: 'anthropics/skills', installs: '22.6K', description: { zh: '帮你写一个新的 Skill', en: 'Helps you write a new Skill' } },
  { name: 'webapp-testing', repo: 'anthropics/skills', installs: '15.1K', description: { zh: '用 Playwright 测试本地网页应用', en: 'Test local web apps with Playwright' } },
  { name: 'xlsx', repo: 'anthropics/skills', installs: '24.3K', description: { zh: '读写表格，保留公式', en: 'Read and write spreadsheets, keeping formulas' } },
  { name: 'brainstorming', repo: 'obra/superpowers', installs: '19.8K', description: { zh: '写代码前先把需求和方案聊清楚', en: 'Talk through requirements and the plan before coding' } },
  { name: 'writing-plans', repo: 'obra/superpowers', installs: '12.7K', description: { zh: '把需求拆成可执行的实现计划', en: 'Turn requirements into an implementation plan' } },
]

export const PROMPT_APP_ORDER = ['claude', 'codex', 'gemini', 'grokbuild', 'opencode', 'hermes', 'pi', 'mcode', 'kimi', 'dsh', 'agy', 'omp', 'copilot', 'workbuddy']

export const PROMPT_TARGET_FILES: Record<string, { file: string; path: string }> = {
  claude: { file: 'CLAUDE.md', path: '~/.claude/CLAUDE.md' },
  codex: { file: 'AGENTS.md', path: '~/.codex/AGENTS.md' },
  gemini: { file: 'GEMINI.md', path: '~/.gemini/GEMINI.md' },
  grokbuild: { file: 'AGENTS.md', path: '~/.grok/AGENTS.md' },
  opencode: { file: 'AGENTS.md', path: '~/.config/opencode/AGENTS.md' },
  hermes: { file: 'SOUL.md', path: '~/.hermes/SOUL.md' },
  pi: { file: 'AGENTS.md', path: '~/.pi/agent/AGENTS.md' },
  mcode: { file: 'AGENTS.md', path: '~/.minimax/AGENTS.md' },
  kimi: { file: 'AGENTS.md', path: '~/.kimi-code/AGENTS.md' },
  agy: { file: 'AGENTS.md', path: '~/.gemini/config/AGENTS.md' },
  omp: { file: 'AGENTS.md', path: '~/.omp/agent/AGENTS.md' },
  dsh: { file: 'AGENTS.md', path: '~/.dsh/AGENTS.md' },
  copilot: { file: 'copilot-instructions.md', path: '~/.copilot/copilot-instructions.md' },
  workbuddy: { file: 'MEMORY.md', path: '~/.workbuddy/user-1024-personal/MEMORY.md' },
}

const P_PRESETS = {
  team: { name: { zh: '团队编码规范', en: 'Team coding rules' }, description: { zh: '所有仓库通用：提交、测试、依赖', en: 'For every repo: commits, tests, dependencies' } },
  review: { name: { zh: '只读审查模式', en: 'Read-only review' }, description: { zh: '只审查，不改文件', en: 'Review only, never edit files' } },
  chinese: { name: { zh: '中文回复', en: 'Reply in Chinese' }, description: { zh: '回复语言', en: 'Reply language' } },
  frontend: { name: { zh: '前端项目约定', en: 'Front-end project rules' }, description: { zh: 'Vue + Tailwind 的仓库用', en: 'For Vue + Tailwind repos' } },
  rust: { name: { zh: 'Rust 后端约定', en: 'Rust back-end rules' }, description: { zh: 'cargo fmt、clippy 零警告', en: 'cargo fmt, zero clippy warnings' } },
  careful: { name: { zh: '严谨的助手', en: 'Careful assistant' }, description: { zh: '先问清楚再动手', en: 'Asks before acting' } },
  casual: { name: { zh: '轻松闲聊', en: 'Casual chat' }, description: { zh: '语气随意，回答简短', en: 'Relaxed tone, short answers' } },
}

const upd = (m: number, d: number, mon: string) => ({ zh: `${m} 月 ${d} 日更新`, en: `Updated ${mon} ${d}` })
const updToday = { zh: '今天 18:40 更新', en: 'Updated today 18:40' }

export function createInitialPromptsState(): Record<string, { enabled: string | null; items: CcsPromptItem[] }> {
  return {
    claude: {
      enabled: 'team',
      items: [
        { id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(9, 28, 'Sep') },
        { id: 'review', ...P_PRESETS.review, size: '312 B', updated: upd(9, 20, 'Sep') },
        { id: 'chinese', ...P_PRESETS.chinese, size: '113 B', updated: upd(8, 30, 'Aug') },
        { id: 'frontend', ...P_PRESETS.frontend, size: '250 B', updated: upd(9, 12, 'Sep') },
      ],
    },
    codex: {
      enabled: 'team',
      items: [
        { id: 'team', ...P_PRESETS.team, size: '702 B', updated: updToday },
        { id: 'review', ...P_PRESETS.review, size: '312 B', updated: upd(9, 20, 'Sep') },
        { id: 'rust', ...P_PRESETS.rust, size: '1.2 KB', updated: upd(10, 2, 'Oct') },
      ],
    },
    gemini: {
      enabled: 'team',
      items: [
        { id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(9, 28, 'Sep') },
        { id: 'chinese', ...P_PRESETS.chinese, size: '113 B', updated: upd(8, 30, 'Aug') },
      ],
    },
    grokbuild: { enabled: null, items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(9, 28, 'Sep') }] },
    opencode: {
      enabled: 'frontend',
      items: [
        { id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(9, 28, 'Sep') },
        { id: 'frontend', ...P_PRESETS.frontend, size: '250 B', updated: upd(9, 12, 'Sep') },
      ],
    },
    hermes: {
      enabled: 'careful',
      items: [
        { id: 'careful', ...P_PRESETS.careful, size: '1.6 KB', updated: upd(9, 24, 'Sep') },
        { id: 'casual', ...P_PRESETS.casual, size: '420 B', updated: upd(9, 3, 'Sep') },
      ],
    },
    pi: { enabled: 'team', items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(9, 28, 'Sep') }] },
    mcode: { enabled: null, items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(9, 28, 'Sep') }] },
    kimi: { enabled: 'team', items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(10, 24, 'Oct') }] },
    agy: { enabled: null, items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(10, 21, 'Oct') }] },
    omp: { enabled: 'team', items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(10, 24, 'Oct') }] },
    dsh: { enabled: 'team', items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(11, 2, 'Nov') }] },
    copilot: { enabled: null, items: [{ id: 'team', ...P_PRESETS.team, size: '718 B', updated: upd(11, 9, 'Nov') }] },
    workbuddy: { enabled: 'chinese', items: [{ id: 'chinese', ...P_PRESETS.chinese, size: '113 B', updated: upd(11, 11, 'Nov') }] },
  }
}

export const SESSION_APP_ORDER = ['claude', 'codex', 'opencode', 'hermes', 'gemini', 'pi', 'omp', 'grokbuild', 'openclaw', 'mcode', 'kimi', 'dsh', 'agy', 'copilot', 'workbuddy']
export const SESSION_MODEL_BY_APP: Record<string, string> = {
  claude: 'claude-sonnet-5-5',
  codex: 'gpt-5.6-sol',
  gemini: 'gemini-3.6-flash',
  opencode: 'kimi-k3',
  grokbuild: 'grok-4.5',
  pi: 'deepseek-v4-pro',
  hermes: 'kimi-k3',
}

export const INITIAL_SESSIONS: CcsSessionItem[] = [
  {
    id: '3f2a9c1e-7b40-4d2e-9a61-5c8f0e7d2b14',
    app: 'claude',
    project: '~/Code/cc-switch',
    title: { zh: '重构会话管理页的布局', en: 'Rework the session manager layout' },
    last: { zh: '已把列表改成单栏，下一步是阅读页。', en: 'The list is a single column now; the reader is next.' },
    ago: { unit: 'minutes', count: 3 },
    bucket: 'today',
    turns: [
      {
        question: {
          zh: '会话管理页现在是左右两栏，窄窗口里很挤。改成单栏列表，点开再进阅读页。',
          en: 'The session manager is two columns and cramped in narrow windows. Make it a single list that opens a reader.',
        },
        steps: [
          { kind: 'read', target: 'src/components/sessions/SessionManagerPage.tsx' },
          { kind: 'read', target: 'src/components/sessions/SessionItem.tsx' },
          { kind: 'edit', target: 'src/components/sessions/SessionManagerPage.tsx', added: 86, removed: 141 },
          { kind: 'edit', target: 'src/components/sessions/SessionItem.tsx', added: 24, removed: 9 },
          { kind: 'run', target: 'pnpm typecheck' },
          { kind: 'run', target: 'pnpm test:unit sessions', failed: true },
          { kind: 'edit', target: 'tests/components/SessionManagerPage.test.tsx', added: 12, removed: 4 },
          { kind: 'run', target: 'pnpm test:unit sessions' },
        ],
        reply: {
          zh: '列表已经改成单栏：按项目分组，每行显示标题、最后一条消息和时间，悬停时出现恢复和复制按钮。测试里有一条还在找旧的右栏，已经改掉，现在全部通过。',
          en: 'The list is a single column now, grouped by project. Each row shows the title, the last message and the time, with resume and copy buttons on hover. One test still looked for the old right pane; I fixed it and everything passes.',
        },
        time: '15:25',
        duration: '4m12s',
      },
      {
        question: {
          zh: '好。阅读页先做个骨架：顶部返回和恢复按钮，右边一个对话目录。',
          en: 'Good. Sketch the reader next: back and resume at the top, a conversation outline on the right.',
        },
        steps: [
          { kind: 'read', target: 'src/components/shell/AppPageHeader.tsx' },
          { kind: 'edit', target: 'src/components/sessions/reader/SessionReader.tsx', added: 132, removed: 0 },
          { kind: 'edit', target: 'src/components/sessions/reader/SessionOutline.tsx', added: 58, removed: 0 },
          { kind: 'run', target: 'pnpm typecheck' },
        ],
        reply: {
          zh: '阅读页的骨架已经搭好：页头有返回、上一个 / 下一个和「在 Terminal 中恢复」，右侧是对话目录，点目录会跳到对应的那一轮。',
          en: 'The reader skeleton is in: the header has back, previous / next and "Resume in Terminal", and the outline on the right jumps to the matching turn.',
        },
        time: '15:41',
        duration: '2m03s',
      },
    ],
  },
  {
    id: '9b17e2d4-1c3a-4f58-8e20-7a6d5f4c3b21',
    app: 'grokbuild',
    project: '~/Code/cc-switch',
    title: { zh: '给 Grok 会话加用量导入', en: 'Import usage from Grok sessions' },
    last: { zh: '导入间隔改为 60 秒。', en: 'The import interval is 60 seconds now.' },
    ago: { unit: 'hours', count: 2 },
    bucket: 'today',
    turns: [
      {
        question: { zh: 'Grok Build 的用量要等十分钟才进统计，能不能快一点？', en: 'Grok Build usage takes ten minutes to show up in stats. Can it be faster?' },
        steps: [
          { kind: 'read', target: 'src-tauri/src/services/session_usage_grok.rs' },
          { kind: 'edit', target: 'src-tauri/src/services/session_usage_grok.rs', added: 3, removed: 3 },
          { kind: 'run', target: 'cargo test session_usage_grok' },
        ],
        reply: {
          zh: '导入间隔改为 60 秒，游标照旧按行号续读，不会重复计数。',
          en: 'The import interval is 60 seconds now; the cursor still resumes by line, so nothing is counted twice.',
        },
        time: '12:08',
        duration: '1m40s',
      },
    ],
  },
  {
    id: 'c4d81a3f-5e62-4b97-a0d3-2f1e8c7b6a59',
    app: 'codex',
    project: '~/Code/cc-switch',
    title: { zh: '修复 Hermes 消息查询的列名', en: 'Fix the column name in the Hermes message query' },
    last: { zh: 'messages 表的时间列叫 timestamp。', en: 'The time column in messages is called timestamp.' },
    ago: { unit: 'hours', count: 5 },
    bucket: 'today',
    turns: [
      {
        question: { zh: 'Hermes 会话打不开，报 no such column: created_at。', en: "Hermes sessions won't open: no such column: created_at." },
        steps: [
          { kind: 'run', target: 'sqlite3 ~/.hermes/state.db ".schema messages"' },
          { kind: 'edit', target: 'src-tauri/src/session_manager/providers/hermes.rs', added: 2, removed: 2 },
          { kind: 'run', target: 'cargo test hermes' },
        ],
        reply: { zh: 'messages 表的时间列叫 timestamp，查询已改过来，测试通过。', en: 'The time column in messages is called timestamp. The query uses it now and the tests pass.' },
        time: '09:52',
        duration: '58s',
      },
    ],
  },
  {
    id: '71e3b9a2-8d4c-4e15-b6f0-9c2a1d3e4f57',
    app: 'claude',
    project: '~/Code/cc-switch',
    title: { zh: '给 MCP 面板加写入失败提示', en: 'Show write failures on the MCP page' },
    last: { zh: '失败时在面板顶部写明哪个应用没写进去。', en: "When a write fails, the top of the page names the app it didn't reach." },
    ago: { unit: 'days', count: 1 },
    bucket: 'yesterday',
    turns: [
      {
        question: { zh: '某个应用的配置写不进去时，MCP 页上看不出来。', en: "When one app's config can't be written, the MCP page doesn't show it." },
        steps: [
          { kind: 'read', target: 'src/components/mcp/UnifiedMcpPanel.tsx' },
          { kind: 'edit', target: 'src/components/mcp/UnifiedMcpPanel.tsx', added: 41, removed: 6 },
          { kind: 'run', target: 'pnpm test:unit mcp' },
        ],
        reply: { zh: '失败时在面板顶部写明哪个应用没写进去，对应的格子变成黄色警告，点一下就重试。', en: "When a write fails, the top of the page names the app it didn't reach, and the cell turns into a yellow warning you can click to retry." },
        time: '18:30',
        duration: '3m21s',
      },
    ],
  },
  {
    id: '5d6e7f80-9a1b-4c2d-be3f-4a5b6c7d8e90',
    app: 'codex',
    project: '~/Code/opencode-go-probe',
    title: { zh: '抓 OpenCode Go 网关的请求头', en: 'Capture OpenCode Go gateway headers' },
    last: { zh: '网关只认 x-api-key。', en: 'The gateway only accepts x-api-key.' },
    ago: { unit: 'days', count: 4 },
    bucket: 'earlier',
    archived: true,
    turns: [
      {
        question: { zh: '看看 OpenCode Go 网关认哪个鉴权头。', en: 'Find out which auth header the OpenCode Go gateway accepts.' },
        steps: [
          { kind: 'run', target: 'curl -sI -H "Authorization: Bearer $KEY" $GATEWAY/v1/messages', failed: true },
          { kind: 'run', target: 'curl -sI -H "x-api-key: $KEY" $GATEWAY/v1/messages' },
        ],
        reply: { zh: '网关只认 x-api-key，Bearer 会返回 401。', en: 'The gateway only accepts x-api-key; Bearer returns 401.' },
        time: '11:37',
        duration: '46s',
      },
    ],
  },
  {
    id: '0a9b8c7d-6e5f-4a3b-9c2d-1e0f9a8b7c6d',
    app: 'hermes',
    project: null,
    title: { zh: '周报草稿', en: 'Weekly report draft' },
    last: { zh: '已按项目列出本周完成的事。', en: "Listed this week's work by project." },
    ago: { unit: 'days', count: 12 },
    bucket: 'earlier',
    turns: [
      {
        question: { zh: '帮我把这周的提交整理成周报。', en: "Turn this week's commits into a weekly report." },
        steps: [{ kind: 'run', target: 'git log --since="1 week ago" --oneline' }],
        reply: { zh: '已按项目列出本周完成的事，每项一句话。', en: "Listed this week's work by project, one line each." },
        time: '19:48',
        duration: '38s',
      },
    ],
  },
]

export interface CcsAuthServiceSpec {
  id: string
  name: string
  iconSrc?: string
  iconSvg?: string
  login: 'browser' | 'device' | 'import'
  chooser?: 'kimi' | 'copilot'
}

export const AUTH_SERVICES: CcsAuthServiceSpec[] = [
  { id: 'claude', name: 'Claude', iconSrc: DATA_CLAUDE, login: 'browser' },
  { id: 'copilot', name: 'GitHub Copilot', iconSvg: SVG_COPILOT, login: 'device', chooser: 'copilot' },
  { id: 'chatgpt', name: 'ChatGPT', iconSvg: SVG_OPENAI, login: 'device' },
  { id: 'xai', name: 'xAI', iconSvg: SVG_GROK, login: 'device' },
  { id: 'kimi', name: 'Kimi Code', iconSvg: SVG_KIMI_APP, login: 'device', chooser: 'kimi' },
  { id: 'dsh', name: 'DeepSeek Harness', iconSrc: DATA_DEEPSEEK, login: 'browser' },
  { id: 'agy', name: 'Antigravity', iconSrc: CCS_LOCAL_ASSETS.antigravity, login: 'browser' },
  { id: 'workbuddy', name: 'WorkBuddy', iconSrc: DATA_WORKBUDDY, login: 'import' },
]

export const AUTH_DEFAULT_SUPPORTED = ['copilot', 'chatgpt', 'xai', 'kimi', 'agy', 'workbuddy']

export const INITIAL_AUTH_ACCOUNTS: Record<string, CcsAuthAccount[]> = {
  claude: [
    { login: 'jason@example.com', plan: 'Max 20x', signedIn: { zh: '9月18日', en: 'Sep 18' }, quota: [{ tier: 'fiveHour', left: 46 }, { tier: 'weekly', left: 71 }], checkedMinutesAgo: 2 },
    { login: 'dev@acme.dev', plan: 'Team', signedIn: { zh: '10月20日', en: 'Oct 20' }, quota: [{ tier: 'fiveHour', left: 88 }, { tier: 'weekly', left: 15 }], checkedMinutesAgo: 5 },
  ],
  copilot: [{ login: 'octocat', host: 'github.com', signedIn: { zh: '9月12日', en: 'Sep 12' }, usedBy: 2, isDefault: true, quota: [{ tier: 'premium', left: 72 }], checkedMinutesAgo: 3 }],
  chatgpt: [
    {
      login: 'dev@example.com',
      signedIn: { zh: '9月30日', en: 'Sep 30' },
      usedBy: 3,
      isDefault: true,
      quota: [{ tier: 'fiveHour', left: 64 }, { tier: 'weekly', left: 81 }],
      resetCredits: [
        { date: { zh: '10月12日', en: 'Oct 12' }, inTime: '4d6h', count: 2 },
        { date: { zh: '11月2日', en: 'Nov 2' }, inTime: '25d', count: 1 },
      ],
      credits: 62500,
      checkedMinutesAgo: 1,
    },
    {
      login: 'team@example.com',
      signedIn: { zh: '10月3日', en: 'Oct 3' },
      usedBy: 1,
      quota: [{ tier: 'fiveHour', left: 8 }, { tier: 'weekly', left: 40 }],
      resetCredits: [{ date: { zh: '10月9日', en: 'Oct 9' }, inTime: '1d20h', count: 1, expiringSoon: true }],
      checkedMinutesAgo: 6,
    },
  ],
  xai: [{ login: 'grok-builder', signedIn: { zh: '8月21日', en: 'Aug 21' }, usedBy: 1, isDefault: true, needsReauth: true, quota: [{ tier: 'weekly', left: 55 }], checkedMinutesAgo: 0 }],
  kimi: [
    { login: 'Moonshot Dev', region: 'mainland-cn', primary: true, signedIn: { zh: '10月2日', en: 'Oct 2' }, isDefault: true, quota: [{ tier: 'fiveHour', left: 62 }, { tier: 'weekly', left: 90 }], checkedMinutesAgo: 1 },
    { login: 'kimi-global', region: 'global', signedIn: { zh: '10月15日', en: 'Oct 15' }, noSubscription: true, quota: [], checkedMinutesAgo: 4 },
  ],
  dsh: [{ login: 'DeepSeek Dev', contact: 'dev@example.com', signedIn: { zh: '11月2日', en: 'Nov 2' }, wallet: { balance: '¥86.40', bonus: '¥10.00' }, quota: [], checkedMinutesAgo: 2 }],
  agy: [{ login: 'dev@gmail.com', signedIn: { zh: '10月8日', en: 'Oct 8' }, isDefault: true, quota: [{ tier: 'geminiFiveHour', left: 80 }, { tier: 'geminiWeekly', left: 66 }, { tier: 'claudeGptWeekly', left: 12 }], checkedMinutesAgo: 3 }],
  workbuddy: [
    { login: 'wb-dev', planText: { zh: '专业版', en: 'Pro' }, signedIn: { zh: '11月11日', en: 'Nov 11' }, isDefault: true, points: { remaining: 1240, total: 2000 }, quota: [], checkedMinutesAgo: 1 },
    { login: 'acme-team', planText: { zh: '团队版', en: 'Team' }, signedIn: { zh: '11月12日', en: 'Nov 12' }, points: { remaining: 8600, total: 10000 }, quota: [], checkedMinutesAgo: 9 },
  ],
}

export const AUTH_NEW_ACCOUNT_TEMPLATES: Record<string, Omit<CcsAuthAccount, 'signedIn' | 'checkedMinutesAgo'>> = {
  claude: { login: 'me@example.com', plan: 'Pro', quota: [{ tier: 'fiveHour', left: 100 }, { tier: 'weekly', left: 100 }] },
  copilot: { login: 'mona', host: 'github.com', usedBy: 0, quota: [{ tier: 'premium', left: 100 }] },
  chatgpt: { login: 'me@example.com', usedBy: 0, quota: [{ tier: 'fiveHour', left: 100 }, { tier: 'weekly', left: 100 }] },
  xai: { login: 'grok-dev', usedBy: 0, quota: [{ tier: 'weekly', left: 100 }] },
  kimi: { login: 'kimi-new', region: 'mainland-cn', quota: [{ tier: 'fiveHour', left: 100 }, { tier: 'weekly', left: 100 }] },
  dsh: { login: 'DeepSeek Dev', contact: 'me@example.com', wallet: { balance: '¥0.00' }, quota: [] },
  workbuddy: { login: 'wb-new', planText: { zh: '免费版', en: 'Free' }, points: { remaining: 500, total: 500 }, quota: [] },
  agy: { login: 'me@gmail.com', quota: [{ tier: 'geminiFiveHour', left: 100 }, { tier: 'claudeGptWeekly', left: 100 }] },
}

// Deterministic Usage Statistics Math (exact replica of ccswitch.io/zh/)
export const USAGE_REFERENCE_NOW = new Date(2026, 9, 7, 14, 58)
const DAY_MS = 1440 * 60 * 1000

export function seededRand(seed: number): number {
  const x = Math.sin(seed * 9301 + 49297) * 233280
  return x - Math.floor(x)
}

export const USAGE_APP_BASELINES: Record<string, { cost: number; requests: number; tokens: number; hit: number }> = {
  claude: { cost: 16821.04, requests: 142060, tokens: 201e8, hit: 96.2 },
  codex: { cost: 8023.69, requests: 91140, tokens: 97e8, hit: 93.1 },
  gemini: { cost: 962.47, requests: 18400, tokens: 11e8, hit: 88.4 },
  grokbuild: { cost: 840.63, requests: 12620, tokens: 9e8, hit: 90.7 },
  opencode: { cost: 411.82, requests: 6880, tokens: 5e8, hit: 91.5 },
  pi: { cost: 93.14, requests: 1460, tokens: 1e8, hit: 84.9 },
  mcode: { cost: 37.69, requests: 680, tokens: 36e6, hit: 82.3 },
  kimi: { cost: 124.05, requests: 2140, tokens: 1e8, hit: 86 },
  agy: { cost: 58.31, requests: 940, tokens: 52e6, hit: 83.6 },
}

export const USAGE_APP_KEYS = Object.keys(USAGE_APP_BASELINES)
const HOURLY_WEIGHTS = [0.1, 0.05, 0.03, 0.02, 0.02, 0.03, 0.1, 0.3, 0.7, 1, 1, 0.95, 0.6, 0.85, 1, 0.95, 0.9, 0.8, 0.55, 0.4, 0.45, 0.5, 0.35, 0.2]
const HOURLY_SUM = HOURLY_WEIGHTS.reduce((a, b) => a + b, 0)
const YEAR_SCALE = 1.6 / 365

export interface UsageBucket {
  key: string
  label: string
  heading: string
  hourly: boolean
  date: Date
}

export interface UsageStatsPoint {
  cost: number
  requests: number
  tokens: number
  hit: number
}

export function startOfDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

export function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
}

export function resolveRangeWindow(range: CcsUsageRange): { start: Date; end: Date } {
  if (range.preset === 'custom') {
    return { start: range.start, end: range.liveEnd ? USAGE_REFERENCE_NOW : range.end }
  }
  const now = USAGE_REFERENCE_NOW
  switch (range.preset) {
    case 'today':
      return { start: startOfDay(now), end: now }
    case '1d':
      return { start: new Date(now.getTime() - DAY_MS), end: now }
    case '7d':
    case '14d':
    case '30d':
      return { start: new Date(now.getTime() - parseInt(range.preset, 10) * DAY_MS), end: now }
    case 'all':
      return { start: new Date(now.getTime() - 30 * DAY_MS), end: now }
  }
}

const pad2 = (n: number) => String(n).padStart(2, '0')

export function buildUsageBuckets(range: CcsUsageRange, lang: CcsLocale): UsageBucket[] {
  const { start, end } = resolveRangeWindow(range)
  const intlLoc = lang === 'zh' ? 'zh-CN' : 'en-US'
  const buckets: UsageBucket[] = []
  if (end.getTime() - start.getTime() <= DAY_MS) {
    const cur = new Date(start.getFullYear(), start.getMonth(), start.getDate(), start.getHours())
    while (cur <= end) {
      const d = new Date(cur)
      buckets.push({
        key: d.toISOString(),
        label: `${pad2(d.getHours())}:00`,
        heading: d.toLocaleString(intlLoc, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }),
        hourly: true,
        date: d,
      })
      cur.setHours(cur.getHours() + 1)
    }
    return buckets
  }
  const cur = startOfDay(start)
  while (cur <= end && buckets.length < 62) {
    const d = new Date(cur)
    buckets.push({
      key: d.toISOString(),
      label: `${pad2(d.getMonth() + 1)}/${pad2(d.getDate())}`,
      heading: d.toLocaleDateString(intlLoc, { year: 'numeric', month: '2-digit', day: '2-digit' }),
      hourly: false,
      date: d,
    })
    cur.setDate(cur.getDate() + 1)
  }
  return buckets
}

export function calcBucketPoint(appId: string, bucket: UsageBucket): UsageStatsPoint {
  const base = USAGE_APP_BASELINES[appId]
  const seedBase = USAGE_APP_KEYS.indexOf(appId) * 101
  const dayIdx = Math.floor(bucket.date.getTime() / DAY_MS)
  let scale = YEAR_SCALE * (bucket.date.getDay() === 0 || bucket.date.getDay() === 6 ? 0.55 : 1) * (0.55 + seededRand(dayIdx * 13 + seedBase) * 0.9)
  if (bucket.hourly) {
    const hr = bucket.date.getHours()
    scale *= (HOURLY_WEIGHTS[hr] / HOURLY_SUM) * (0.6 + seededRand(dayIdx * 29 + hr * 7 + seedBase) * 0.8)
  }
  return {
    tokens: base.tokens * scale,
    requests: base.requests * scale * (0.85 + seededRand(dayIdx * 31 + seedBase + 5) * 0.3),
    cost: base.cost * scale * (0.85 + seededRand(dayIdx * 37 + seedBase + 9) * 0.3),
    hit: Math.min(99.5, base.hit + (seededRand(dayIdx * 41 + (bucket.hourly ? bucket.date.getHours() : 0) + seedBase) - 0.5) * 6),
  }
}

export function aggregatePoints(points: UsageStatsPoint[]): UsageStatsPoint {
  const sum = points.reduce(
    (acc, item) => ({
      cost: acc.cost + item.cost,
      requests: acc.requests + item.requests,
      tokens: acc.tokens + item.tokens,
      hit: acc.hit + item.hit * item.tokens,
    }),
    { cost: 0, requests: 0, tokens: 0, hit: 0 },
  )
  return {
    ...sum,
    hit: sum.tokens > 0 ? sum.hit / sum.tokens : 0,
  }
}

export function splitTokenBreakdown(totals: UsageStatsPoint) {
  const output = totals.tokens * 0.012
  const cacheRead = (totals.tokens - output) * (totals.hit / 100)
  const rest = Math.max(0, totals.tokens - output - cacheRead)
  return {
    freshInput: rest * 0.35,
    output,
    cacheWrite: rest * 0.65,
    cacheRead,
  }
}

export function formatCompactTokens(value: number, lang: CcsLocale): string {
  const intlLoc = lang === 'zh' ? 'zh-CN' : 'en-US'
  if (Math.abs(value) < 1e4) return new Intl.NumberFormat(intlLoc).format(Math.round(value))
  for (const [unit, suffix] of [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ] as const) {
    if (Math.abs(value) >= unit) return `${Number((value / unit).toPrecision(3))}${suffix}`
  }
  return String(value)
}

export function formatUsd(value: number, decimals = 2): string {
  return `$${value.toLocaleString('en-US', { minimumFractionDigits: decimals, maximumFractionDigits: decimals })}`
}

export function niceCeiling(maxVal: number): number {
  if (maxVal <= 0) return 1
  const exp = 10 ** Math.floor(Math.log10(maxVal))
  const mult = [1, 2, 5, 10].find((m) => m * exp >= maxVal) ?? 10
  return mult * exp
}

export function calcTodayTotalCost(): number {
  return buildUsageBuckets({ preset: 'today' }, 'en').reduce(
    (sum, bucket) => sum + USAGE_APP_KEYS.reduce((s, appId) => s + calcBucketPoint(appId, bucket).cost, 0),
    0,
  )
}

export function buildHeatmapCells(metric: CcsUsageMetric, selectedApp: string): number[] {
  const metricIndex = { tokens: 1, requests: 2, cost: 3, hitRate: 4 }[metric]
  const appIdx = selectedApp === 'all' ? 0 : CC_SWITCH_DEMO_APPS.findIndex((a) => a.id === selectedApp) + 1
  return Array.from({ length: 53 * 7 }, (_, i) => {
    const trend = 0.25 + (Math.floor(i / 7) / 53) * 0.75
    const wknd = i % 7 >= 5 ? 0.6 : 1
    const r = seededRand(i + metricIndex * 1000 + appIdx * 77)
    const val = trend * wknd * (0.35 + r * 0.9)
    if (val < 0.18) return 0
    if (val < 0.38) return 1
    if (val < 0.58) return 2
    if (val < 0.8) return 3
    return 4
  })
}

export interface UsageLogRow {
  time: string
  app: string
  provider: string
  model: string
  cost: string
  input: number
  output: number
  cacheRead: string
  speed: string
  firstToken: string
}

export const INITIAL_USAGE_LOGS: UsageLogRow[] = [
  { time: '14:58:06', app: 'claude', provider: 'Kimi For Coding', model: 'kimi-k3', cost: '$0.0240', input: 54, output: 424, cacheRead: '143K', speed: '≈49', firstToken: '≈2.4s' },
  { time: '14:57:54', app: 'codex', provider: 'OpenAI Official', model: 'gpt-5.6-sol', cost: '$0.0950', input: 812, output: 1184, cacheRead: '86K', speed: '87', firstToken: '1.1s' },
  { time: '14:57:40', app: 'claude', provider: 'Claude Official', model: 'claude-opus-5-5', cost: '$0.0707', input: 55, output: 101, cacheRead: '342K', speed: '—', firstToken: '—' },
  { time: '14:54:11', app: 'codex', provider: SITE_PROVIDER_PLACEHOLDER, model: 'deepseek-v4-pro', cost: '$0.0054', input: 268, output: 605, cacheRead: '64K', speed: '53', firstToken: '1.6s' },
  { time: '14:53:31', app: 'grokbuild', provider: 'Grok Official', model: 'grok-4.5', cost: '$0.0107', input: 92, output: 323, cacheRead: '51K', speed: '48', firstToken: '0.9s' },
  { time: '14:53:14', app: 'gemini', provider: 'Google Official', model: 'gemini-3.6-flash', cost: '$0.0312', input: 1460, output: 904, cacheRead: '38K', speed: '80', firstToken: '0.7s' },
  { time: '14:52:47', app: 'claude', provider: 'Claude Official', model: 'claude-sonnet-5-5', cost: '$0.0566', input: 61, output: 2210, cacheRead: '201K', speed: '≈92', firstToken: '≈1.8s' },
  { time: '14:51:09', app: 'opencode', provider: 'Kimi For Coding', model: 'kimi-k3', cost: '$0.0081', input: 340, output: 512, cacheRead: '72K', speed: '61', firstToken: '1.3s' },
  { time: '14:49:30', app: 'kimi', provider: 'Kimi Official', model: 'kimi-k3', cost: '$0.0035', input: 210, output: 388, cacheRead: '40K', speed: '≈58', firstToken: '≈1.5s' },
  { time: '14:47:02', app: 'agy', provider: 'Antigravity', model: 'gemini-3.6-pro', cost: '$0.0412', input: 980, output: 1320, cacheRead: '57K', speed: '≈71', firstToken: '≈2.0s' },
]

const USAGE_SHARES = {
  providers: [
    { name: 'Claude Official', app: 'claude', share: 0.62 },
    { name: 'Kimi For Coding', app: 'claude', share: 0.38 },
    { name: 'OpenAI Official', app: 'codex', share: 0.8 },
    { name: SITE_PROVIDER_PLACEHOLDER, app: 'codex', share: 0.2 },
    { name: 'Google Official', app: 'gemini', share: 1 },
    { name: 'Grok Official', app: 'grokbuild', share: 1 },
    { name: 'Kimi For Coding', app: 'opencode', share: 1 },
    { name: 'Kimi For Coding', app: 'pi', share: 1 },
    { name: 'MiniMax', app: 'mcode', share: 1 },
    { name: 'Kimi Official', app: 'kimi', share: 1 },
    { name: 'Antigravity', app: 'agy', share: 1 },
  ],
  models: [
    { name: 'claude-opus-5-5', app: 'claude', share: 0.45 },
    { name: 'claude-sonnet-5-5', app: 'claude', share: 0.17 },
    { name: 'kimi-k3', app: 'claude', share: 0.38 },
    { name: 'gpt-5.6-sol', app: 'codex', share: 0.8 },
    { name: 'deepseek-v4-pro', app: 'codex', share: 0.2 },
    { name: 'gemini-3.6-flash', app: 'gemini', share: 0.6 },
    { name: 'gemini-3.6-pro', app: 'gemini', share: 0.4 },
    { name: 'grok-4.5', app: 'grokbuild', share: 1 },
    { name: 'kimi-k3', app: 'opencode', share: 1 },
    { name: 'kimi-k3', app: 'pi', share: 1 },
    { name: 'MiniMax-M3', app: 'mcode', share: 1 },
    { name: 'kimi-k3', app: 'kimi', share: 1 },
    { name: 'gemini-3.6-pro', app: 'agy', share: 1 },
  ],
}

export interface UsageAggregateRow {
  name: string
  requests: number
  cost: number
  tokens: number
  success: number
  speed: number
  firstToken: number
}

export function buildUsageAggregates(
  kind: 'providers' | 'models',
  appTotals: Record<string, UsageStatsPoint>,
): UsageAggregateRow[] {
  const map = new Map<string, UsageAggregateRow>()
  for (const item of USAGE_SHARES[kind]) {
    const appStat = appTotals[item.app]
    if (!appStat) continue
    const prev = map.get(item.name) ?? { name: item.name, requests: 0, cost: 0, tokens: 0, success: 0, speed: 0, firstToken: 0 }
    prev.requests += appStat.requests * item.share
    prev.cost += appStat.cost * item.share
    prev.tokens += appStat.tokens * item.share
    map.set(item.name, prev)
  }
  return [...map.values()]
    .map((row, idx) => {
      const r = seededRand(row.name.length * 17 + idx)
      return {
        ...row,
        success: 98.4 + r * 1.6,
        speed: 45 + r * 50,
        firstToken: 0.7 + r * 1.6,
      }
    })
    .sort((a, b) => b.requests - a.requests)
}

export function quotaTone(left: number): 'normal' | 'warning' | 'danger' {
  if (left <= 0) return 'danger'
  if (left < 20) return 'warning'
  return 'normal'
}
