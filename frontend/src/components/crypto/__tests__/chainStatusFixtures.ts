import zh from '@/i18n/locales/zh/admin/crypto'
import type { ChainNodeStatus, ChainStatus, ChainStatusReport } from '@/api/admin/crypto'

// Specs render the panel with the REAL zh messages, so assertions read what an
// operator actually sees. An unknown key renders as MISSING:<key> and fails loudly.
const common: Record<string, string> = { 'common.refresh': '刷新', 'common.loading': '加载中...' }

function lookup(key: string): string | undefined {
  if (key in common) return common[key]
  if (!key.startsWith('admin.crypto.')) return undefined
  let current: unknown = (zh as { crypto: unknown }).crypto
  for (const part of key.slice('admin.crypto.'.length).split('.')) {
    if (current === null || typeof current !== 'object') return undefined
    current = (current as Record<string, unknown>)[part]
  }
  return typeof current === 'string' ? current : undefined
}

export function translate(key: string, params?: Record<string, unknown>): string {
  const message = lookup(key)
  if (message === undefined) return `MISSING:${key}`
  return message.replace(/\{(\w+)\}/g, (_, name: string) => String(params?.[name] ?? `{${name}}`))
}

export const i18nMock = { useI18n: () => ({ t: translate }) }

export const node = (over: Partial<ChainNodeStatus> = {}): ChainNodeStatus => ({
  label: 'ankr#0', host: 'rpc.ankr.com', status: 'ok', latency_ms: 120, head: 1000, behind: 0, error_kind: null, ...over
})

export const chain = (over: Partial<ChainStatus> = {}): ChainStatus => ({
  chain: 'bsc', enabled: true, state: 'synced', confirmations: 3, scan_range: 2000,
  head: 40_000_000, confirmed_head: 39_999_997, cursor: 39_999_990, unscanned: 7,
  progress_pct: 100, scan_speed_bpm: 4000, head_speed_bpm: 80, eta_seconds: 0,
  cursor_updated_at: '2023-11-14T22:13:15Z', stalled_seconds: 5, pending_deposits: 0,
  last_error: null, nodes: [node()], ...over
})

export const report = (chains: ChainStatus[] = [chain()], over: Partial<ChainStatusReport> = {}): ChainStatusReport => ({
  generated_at: '2023-11-14T22:13:20Z',
  summary: { enabled: chains.filter(c => c.enabled).length, lagging: 0, stalled: 0, error: 0 },
  chains, ...over
})

// What the failing rows look like: no head, no progress, nothing computed.
export const deadChain = (over: Partial<ChainStatus> = {}): ChainStatus => chain({
  state: 'error', head: null, confirmed_head: null, unscanned: null, progress_pct: null,
  scan_speed_bpm: null, head_speed_bpm: null, eta_seconds: null,
  last_error: { kind: 'http_5xx', stage: 'head' },
  nodes: [node({ status: 'error', latency_ms: null, head: null, behind: null, error_kind: 'http_5xx' })], ...over
})
