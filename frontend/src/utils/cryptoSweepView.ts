import type { AddressOwner, SweepRecord } from '@/api/admin/crypto'

// Pure helpers behind the sweep history panel, kept apart so they can be tested
// without mounting anything.

/** Nuxt UI's Select cannot hold an empty-string item; this stands for "no filter". */
export const SWEEP_FILTER_ALL = '__all__'

export type SweepStatusFilter = typeof SWEEP_FILTER_ALL | 'swept' | 'running' | 'failed' | 'pending'

/** Which raw statuses each filter entry covers. */
export const SWEEP_STATUS_GROUPS: Readonly<Record<Exclude<SweepStatusFilter, typeof SWEEP_FILTER_ALL>, readonly string[]>> = {
  swept: ['swept'],
  running: ['gas_pending', 'gas_confirmed', 'sweep_pending', 'token_pending'],
  failed: ['failed'],
  pending: ['planned', 'manual_review']
}

export function sweepStatusMatches(filter: string, status: string): boolean {
  if (filter === SWEEP_FILTER_ALL || !Object.prototype.hasOwnProperty.call(SWEEP_STATUS_GROUPS, filter)) return true
  return SWEEP_STATUS_GROUPS[filter as keyof typeof SWEEP_STATUS_GROUPS].includes(status)
}

// Same wording and colours as the other tables on the page.
const STATUS_LABEL_KEYS: Readonly<Record<string, string>> = {
  swept: 'admin.crypto.statusSwept',
  failed: 'admin.crypto.failed',
  planned: 'admin.crypto.planned',
  gas_pending: 'admin.crypto.statusGasPending',
  gas_confirmed: 'admin.crypto.statusGasConfirmed',
  sweep_pending: 'admin.crypto.statusTokenPending',
  token_pending: 'admin.crypto.statusTokenPending',
  manual_review: 'admin.crypto.statusManualReview'
}

/** i18n key for a status, or '' when unknown (the caller shows the raw value). */
export function sweepStatusLabelKey(status: string): string {
  return Object.prototype.hasOwnProperty.call(STATUS_LABEL_KEYS, status) ? STATUS_LABEL_KEYS[status] : ''
}

export function sweepStatusColor(status: string): 'success' | 'warning' | 'error' | 'neutral' {
  if (status === 'swept') return 'success'
  if (status === 'failed') return 'error'
  if (['planned', 'gas_pending', 'gas_confirmed', 'sweep_pending', 'token_pending', 'manual_review'].includes(status)) return 'warning'
  return 'neutral'
}

export const SWEEP_STATUS_LABEL_KEYS: readonly string[] = [...new Set(Object.values(STATUS_LABEL_KEYS))]

const ERROR_LINE_MAX = 300

/**
 * The server already stores only vetted messages. This is the second line of
 * defence for what the page prints: URLs, key/token pairs and 64-digit hex
 * runs (which could be a private key) never reach the DOM.
 */
export function redactSweepError(message: string | null | undefined): string {
  const text = String(message ?? '')
    .replace(/[a-z][a-z0-9+.-]*:\/\/\S+/gi, '[url]')
    .replace(/\b(api[_-]?key|access[_-]?token|token|secret|password|passwd|authorization|key)\b\s*[=:]\s*\S+/gi, '$1=[hidden]')
    .replace(/\b(?:0x)?[0-9a-fA-F]{64}\b/g, '[hex]')
    .trim()
  return text.length > ERROR_LINE_MAX ? `${text.slice(0, ERROR_LINE_MAX)}…` : text
}

const YEAR_MS = 365 * 24 * 60 * 60 * 1000

/** "11/05 08:00" for anything under a year old, with the year after that. */
export function formatSweepTime(value: string | null | undefined, locale?: string, now: number = Date.now()): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const recent = Math.abs(now - date.getTime()) < YEAR_MS
  try {
    return new Intl.DateTimeFormat(locale || undefined, {
      ...(recent ? {} : { year: 'numeric' }),
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23'
    }).format(date)
  } catch {
    return date.toLocaleString()
  }
}

/** Full local timestamp for tooltips. */
export function formatSweepFullTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

/**
 * Client-side search over what is already loaded: user name, email, deposit
 * address, either transaction hash, or the record id. The id only matches
 * whole (a leading # is allowed) so "12" does not light up every record 120-129.
 */
export function sweepRecordMatches(record: SweepRecord, owner: AddressOwner | null | undefined, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  const idQuery = q.replace(/^#/, '')
  if (/^\d+$/.test(idQuery) && String(record.id) === idQuery) return true
  return [owner?.username, owner?.email, record.from_address, record.sweep_tx_hash, record.gas_tx_hash]
    .some(value => typeof value === 'string' && value !== '' && value.toLowerCase().includes(q))
}

/** Shortens long values the way the rest of the page does. */
export function shortValue(value: string | null | undefined): string {
  if (!value) return '—'
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}
