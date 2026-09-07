import type { GroupPlatform } from '@/types'

export const OPENAI_CC_SWITCH_CODEX_MODEL = 'gpt-5.6-sol'
export const GROK_CC_SWITCH_MODEL = 'grok-4.6'
export const CLAUDE_CC_SWITCH_MODEL = 'claude-sonnet-4-6'
export const GEMINI_CC_SWITCH_MODEL = 'gemini-2.5-pro'

export type CcSwitchClientType = 'claude' | 'gemini'

// Mirrors farion1231/cc-switch APP_IDS / AppType.
export type CcSwitchApp =
  | 'claude'
  | 'claude-desktop'
  | 'codex'
  | 'gemini'
  | 'grokbuild'
  | 'opencode'
  | 'openclaw'
  | 'hermes'
  | 'pi'

export const CC_SWITCH_APPS: CcSwitchApp[] = [
  'claude',
  'claude-desktop',
  'codex',
  'gemini',
  'grokbuild',
  'opencode',
  'openclaw',
  'hermes',
  'pi'
]

export const CC_SWITCH_APP_LABEL_KEYS: Record<CcSwitchApp, string> = {
  claude: 'claude',
  'claude-desktop': 'claudeDesktop',
  codex: 'codex',
  gemini: 'gemini',
  grokbuild: 'grok',
  opencode: 'opencode',
  openclaw: 'openclaw',
  hermes: 'hermes',
  pi: 'pi'
}

// src-tauri/src/deeplink/parser.rs provider allowlist. Pi and Claude Desktop
// exist as apps but provider import currently rejects them.
export const CC_SWITCH_PROVIDER_DEEPLINK_APPS: readonly CcSwitchApp[] = [
  'claude',
  'codex',
  'gemini',
  'grokbuild',
  'opencode',
  'openclaw',
  'hermes'
]

export const CC_SWITCH_MODEL_SUGGESTIONS: Record<CcSwitchApp, string[]> = {
  claude: ['claude-sonnet-4-6', 'claude-sonnet-5', 'claude-opus-4-6', 'claude-haiku-4-5-20251001'],
  'claude-desktop': ['claude-sonnet-4-6', 'claude-sonnet-5', 'claude-opus-4-6', 'claude-haiku-4-5-20251001'],
  gemini: ['gemini-2.5-pro', 'gemini-3-pro-preview'],
  codex: ['gpt-5.6-sol', 'gpt-5.6', 'gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini'],
  grokbuild: ['grok-4.6', 'grok-4.5', 'grok-build-0.1'],
  opencode: ['gpt-5.6-sol', 'claude-sonnet-4-6', 'grok-4.6'],
  openclaw: ['gpt-5.6-sol', 'claude-sonnet-4-6', 'grok-4.6'],
  hermes: ['gpt-5.6-sol', 'claude-sonnet-4-6'],
  pi: ['gpt-5.6-sol', 'claude-sonnet-4-6', 'grok-4.6']
}

export interface CcSwitchImportConfig {
  app: CcSwitchApp
  endpoint: string
  model?: string
}

export interface CcSwitchImportDeeplinkInput {
  baseUrl: string
  platform?: GroupPlatform | null
  clientType?: CcSwitchClientType
  providerName: string
  apiKey: string
  usageScript: string
  app?: CcSwitchApp
  model?: string
}

function withV1Endpoint(baseUrl: string): string {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, '')
  return normalizedBaseUrl.endsWith('/v1') ? normalizedBaseUrl : `${normalizedBaseUrl}/v1`
}

export function isCcSwitchApp(value: string): value is CcSwitchApp {
  return (CC_SWITCH_APPS as readonly string[]).includes(value)
}

export function ccSwitchProviderDeeplinkSupported(app: CcSwitchApp): boolean {
  return (CC_SWITCH_PROVIDER_DEEPLINK_APPS as readonly string[]).includes(app)
}

export function defaultCcSwitchApp(
  platform: GroupPlatform | undefined | null,
  clientType: CcSwitchClientType = 'claude'
): CcSwitchApp {
  switch (platform || 'anthropic') {
    case 'antigravity':
      return clientType === 'gemini' ? 'gemini' : 'claude'
    case 'openai':
      return 'codex'
    case 'gemini':
      return 'gemini'
    case 'grok':
      return 'grokbuild'
    default:
      return 'claude'
  }
}

export function defaultCcSwitchModel(app: CcSwitchApp): string {
  switch (app) {
    case 'claude':
    case 'claude-desktop':
      return CLAUDE_CC_SWITCH_MODEL
    case 'codex':
      return OPENAI_CC_SWITCH_CODEX_MODEL
    case 'gemini':
      return GEMINI_CC_SWITCH_MODEL
    case 'grokbuild':
      return GROK_CC_SWITCH_MODEL
    default:
      return CLAUDE_CC_SWITCH_MODEL
  }
}

export function parseCcSwitchModelsList(payload: unknown): string[] {
  if (!payload || typeof payload !== 'object') return []
  const data = (payload as { data?: unknown }).data
  if (!Array.isArray(data)) return []
  const seen = new Set<string>()
  const ids: string[] = []
  for (const item of data) {
    let id = ''
    if (typeof item === 'string') {
      id = item.trim()
    } else if (item && typeof item === 'object') {
      const row = item as { id?: unknown; name?: unknown }
      if (typeof row.id === 'string') id = row.id.trim()
      else if (typeof row.name === 'string') id = row.name.trim()
    }
    if (!id || seen.has(id)) continue
    seen.add(id)
    ids.push(id)
  }
  return ids
}

export function pickCcSwitchModel(preferred: string, available: string[]): string {
  if (preferred && available.includes(preferred)) return preferred
  if (available.length > 0) return available[0]
  return preferred
}

export async function fetchCcSwitchModels(
  baseUrl: string,
  apiKey: string,
  signal?: AbortSignal
): Promise<string[]> {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, '')
  const response = await fetch(`${normalizedBaseUrl}/v1/models`, {
    headers: { Authorization: `Bearer ${apiKey}` },
    signal
  })
  if (!response.ok) {
    throw new Error(`models ${response.status}`)
  }
  return parseCcSwitchModelsList(await response.json())
}

export function resolveCcSwitchEndpoint(
  platform: GroupPlatform | undefined | null,
  app: CcSwitchApp,
  baseUrl: string
): string {
  if ((platform || 'anthropic') === 'antigravity') {
    return `${baseUrl}/antigravity`
  }
  if (app === 'grokbuild') {
    return withV1Endpoint(baseUrl)
  }
  return baseUrl
}

export function resolveCcSwitchImportConfig(
  platform: GroupPlatform | undefined | null,
  clientType: CcSwitchClientType,
  baseUrl: string,
  selectedApp?: CcSwitchApp
): CcSwitchImportConfig {
  const app = selectedApp ?? defaultCcSwitchApp(platform, clientType)
  const config: CcSwitchImportConfig = {
    app,
    endpoint: resolveCcSwitchEndpoint(platform, app, baseUrl)
  }
  const model = defaultCcSwitchModel(app)
  if (model) {
    config.model = model
  }
  return config
}

export function buildCcSwitchImportDeeplink(input: CcSwitchImportDeeplinkInput): string {
  const clientType = input.clientType ?? 'claude'
  const app = input.app ?? defaultCcSwitchApp(input.platform, clientType)
  const model =
    input.model !== undefined ? input.model.trim() : defaultCcSwitchModel(app)
  const endpoint = resolveCcSwitchEndpoint(input.platform, app, input.baseUrl)
  const entries: [string, string][] = [
    ['resource', 'provider'],
    ['app', app],
    ['name', input.providerName],
    ['homepage', input.baseUrl],
    ['endpoint', endpoint],
    ['apiKey', input.apiKey],
    ['configFormat', 'json'],
    ['usageEnabled', 'true'],
    ['usageScript', btoa(input.usageScript)],
    ['usageAutoInterval', '30']
  ]

  if (model) {
    entries.splice(2, 0, ['model', model])
  }

  return `ccswitch://v1/import?${new URLSearchParams(entries).toString()}`
}
