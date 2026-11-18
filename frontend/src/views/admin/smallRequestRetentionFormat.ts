import { formatCostFixed, formatCurrency, formatDateTime } from '@/utils/format'

// Presentation only: retained wire text and copied bodies always keep their bytes.
export function retentionTime(value?: string | null): string {
  return formatDateTime(value) || '—'
}

export function retentionCost(value: string): string {
  const amount = Number(value)
  if (!value || !Number.isFinite(amount)) return '—'
  // The settlement API has eight decimal places; the shared currency formatter has six.
  return amount !== 0 && Math.abs(amount) < 0.01 ? '$' + formatCostFixed(amount, 8) : formatCurrency(amount)
}

export function retentionHours(value: string, now = Date.now()): number | null {
  const remaining = Date.parse(value) - now
  return Number.isFinite(remaining) ? Math.max(0, Math.ceil(remaining / 3_600_000)) : null
}

export function retentionDistribution(distribution: Record<string, number>) {
  const entries = ['1', '2-9', '10-99', '100+'].map(bucket => ({ bucket, count: distribution[bucket] || 0 }))
  const maximum = Math.max(1, ...entries.map(entry => entry.count))
  return entries.map(entry => ({ ...entry, width: entry.count / maximum * 100 }))
}

const replayReasons = new Set([
  'external_context:previous_response_id', 'external_context:file_id', 'external_context:cachedcontent',
  'external_context:cached_content', 'external_context:encrypted_content', 'client_delivery_incomplete', 'stream_terminal_missing',
  'request_credentials_redacted',
])
const settlements = new Set(['settled', 'already_settled', 'failed', 'not_billed', 'not_billed_no_usage_row'])
export function retentionReasonKey(reason: string): string | null {
  return replayReasons.has(reason.toLowerCase()) ? 'admin.requestCapture.small.replayReasons.' + reason.toLowerCase() : null
}
export function retentionSettlementKey(value: string): string | null {
  return settlements.has(value) ? 'admin.requestCapture.small.settlements.' + value : null
}

// HTML is escaped before adding spans. Formatting never changes the retained bytes.
export function highlightRetentionJSON(text: string): string {
  let pretty: string
  try { pretty = JSON.stringify(JSON.parse(text), null, 2) } catch { return '' }
  const escaped = pretty.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  return escaped.replace(/"(?:\\.|[^"\\])*"(?=\s*:)|"(?:\\.|[^"\\])*"|\b(?:true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g, token => {
    const kind = token.startsWith('"') ? 'text-default' : 'text-muted'
    return `<span class="${kind}">${token}</span>`
  })
}
