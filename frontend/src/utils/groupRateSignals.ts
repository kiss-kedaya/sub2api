import type { Group } from '@/types'

/**
 * The display hints the backend attaches to a group on GET /groups/available.
 * They never affect pricing; a missing field simply means "nothing to show".
 */
export type GroupSignalSource = Pick<Group, 'recent_rate_change' | 'is_new' | 'launched_at' | 'launch_rate'>

/** Trim trailing zeros but keep two decimals: 0.5 -> "0.50", 0.0125 -> "0.0125", 1 -> "1.00". */
export function formatRateValue(value: number): string {
  if (!Number.isFinite(value)) return '-'
  const fixed = value.toFixed(4)
  const trimmed = fixed.replace(/0+$/, '')
  const [integer, fraction = ''] = trimmed.split('.')
  return `${integer}.${fraction.padEnd(2, '0')}`
}

/** Short local date and time for a change or launch moment, "-" when unparsable. */
export function formatSignalTime(value: string | null | undefined, locale?: string): string {
  if (!value) return '-'
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return '-'
  return new Intl.DateTimeFormat(locale || undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(parsed)
}
