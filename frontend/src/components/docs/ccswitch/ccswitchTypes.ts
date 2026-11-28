export type CcsMode = 'direct' | 'route' | 'stack'
export type CcsAppKind = 'switch' | 'additive' | 'launch'
export type CcsPageKind =
  | 'app'
  | 'usage'
  | 'mcp'
  | 'skills'
  | 'prompts'
  | 'sessions'
  | 'auth'
  | 'apps'
  | 'settings'

export type CcsScene = 'provider' | 'proxy' | 'stats'
export type CcsLocale = 'zh' | 'en'
export type CcsThemePref = 'system' | 'light' | 'dark'

export interface CcsLocalizedText {
  zh: string
  en: string
}

export interface CcsQuotaTier {
  label: string
  utilization: number
  resetsIn: string
}

export interface CcsProviderItem {
  id: string
  name: string
  icon?: string
  iconSvg?: string
  iconBg: string
  iconColor?: string
  logoOnWhite?: boolean
  official?: boolean
  isSiteProvider?: boolean
  modelBadge?: string
  maskedKeyPreview?: string
  subtitle: string
  href?: string | CcsLocalizedText
  time?: string
  used?: string
  remaining?: string
  quota?: {
    updatedMinutes: number
    tiers: CcsQuotaTier[]
  }
  caption?: CcsLocalizedText
  aside?: CcsLocalizedText
}

export interface CcsAppSpec {
  id: string
  label: string
  icon: string
  iconSvg?: string
  badge?: 'terminal' | 'monitor'
  kind: CcsAppKind
  modes?: CcsMode[]
  initialMode?: CcsMode
  providers: CcsProviderItem[]
  stack?: {
    default: string
    members: Record<string, number>
  }
  needsRoute?: string[]
  added?: string[]
  enableLabel?: boolean
  sharedList?: boolean
  pinned?: Record<string, 'none' | 'openTerminal'>
  exclusive?: string[]
}

export interface CcsAppRuntimeState {
  active: CcsMode
  view: CcsMode
  direct: string
  route: string
  stackDefault: string
  stack: Record<string, number>
  added: string[]
}

export interface CcsMenuItem {
  label: string
  hint?: string
  danger?: boolean
  disabled?: boolean
  checked?: boolean
  separatorBefore?: boolean
  icon?: string
  rightText?: string
  onSelect?: () => void
}

export interface CcsToastState {
  id: number
  text: string
  detail?: string
  undo?: () => void
}

export interface CcsInstalledAppRow {
  app: string
  source?: string
  path?: string
  version?: string
  latest?: string
  installHint?: 'npm' | 'script'
  conflict?: {
    path: string
    version: string
    source: string
  }
}

export interface CcsMcpServer {
  id: string
  transport: 'http' | 'stdio'
  summary: string
  apps: string[]
}

export interface CcsInstalledSkill {
  name: string
  description: CcsLocalizedText
  repo: string | null
  apps: string[]
  update?: boolean
}

export interface CcsDiscoverSkill {
  name: string
  repo: string
  installs: string
  description: CcsLocalizedText
}

export interface CcsPromptItem {
  id: string
  name: CcsLocalizedText
  description: CcsLocalizedText
  size: string
  updated: CcsLocalizedText
}

export interface CcsSessionStep {
  kind: 'read' | 'edit' | 'run'
  target: string
  added?: number
  removed?: number
  failed?: boolean
}

export interface CcsSessionTurn {
  question: CcsLocalizedText
  steps: CcsSessionStep[]
  reply: CcsLocalizedText
  time: string
  duration: string
}

export interface CcsSessionItem {
  id: string
  app: string
  project: string | null
  title: CcsLocalizedText
  last: CcsLocalizedText
  ago: { unit: 'minutes' | 'hours' | 'days'; count: number }
  bucket: 'today' | 'yesterday' | 'thisWeek' | 'earlier'
  archived?: boolean
  turns: CcsSessionTurn[]
}

export interface CcsAuthAccount {
  login: string
  plan?: string
  host?: string
  contact?: string
  region?: 'mainland-cn' | 'global'
  primary?: boolean
  planText?: CcsLocalizedText
  signedIn: CcsLocalizedText
  usedBy?: number
  isDefault?: boolean
  needsReauth?: boolean
  noSubscription?: boolean
  quota: Array<{ tier: string; left: number }>
  resetCredits?: Array<{
    date: CcsLocalizedText
    inTime: string
    count: number
    expiringSoon?: boolean
  }>
  credits?: number
  wallet?: { balance: string; bonus?: string }
  points?: { remaining: number; total: number }
  checkedMinutesAgo: number
}

export type CcsUsagePreset = 'today' | '1d' | '7d' | '14d' | '30d' | 'all'

export type CcsUsageRange =
  | { preset: CcsUsagePreset }
  | { preset: 'custom'; start: Date; end: Date; liveEnd: boolean }

export type CcsUsageMetric = 'cost' | 'requests' | 'tokens' | 'hitRate'

export interface CcsEditorDraft {
  mode: 'add' | 'edit'
  appId: string
  originalName?: string
  name: string
  endpoint: string
  apiKey: string
  model: string
}
