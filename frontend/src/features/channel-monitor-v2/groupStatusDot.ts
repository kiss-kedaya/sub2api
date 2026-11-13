import type { MonitorMatrixBucket, MonitorMatrixRow, MonitorRange } from '@/api/channelMonitorV2'
import { getMatrix } from '@/api/channelMonitorV2'
import { availabilityBarClass } from './monitorFormat'

/**
 * Colour of one timeline bar in /monitor.
 *
 * This mirrors ChannelMonitorV3Timeline exactly: an unknown bucket is grey, and
 * a known one is coloured by availability. Keeping the rule here means the dot
 * on a group row can never drift from the bar it is meant to summarise.
 */
export function bucketStatusClass(bucket: MonitorMatrixBucket | null | undefined): string {
  if (!bucket) return 'bg-gray-300 dark:bg-dark-600'
  const overall = bucket.health?.overall
  if (overall !== 'healthy' && overall !== 'warning' && overall !== 'critical') {
    return 'bg-gray-300 dark:bg-dark-600'
  }
  const errorRate = bucket.metrics?.error_rate
  const availability = typeof errorRate === 'number' && Number.isFinite(errorRate) ? (1 - errorRate) * 100 : null
  return availabilityBarClass(availability)
}

/** The newest bucket of a row, i.e. the last bar drawn before "NOW". */
export function latestBucket(row: MonitorMatrixRow | null | undefined): MonitorMatrixBucket | null {
  const buckets = row?.buckets
  if (!buckets || buckets.length === 0) return null
  return [...buckets].sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start)).at(-1) ?? null
}

/** Colour of the group's last status bar, or null when the monitor knows nothing yet. */
export function groupStatusClass(row: MonitorMatrixRow | null | undefined): string | null {
  const bucket = latestBucket(row)
  if (!bucket) return null
  return bucketStatusClass(bucket)
}

const CACHE_TTL_MS = 60_000
const DEFAULT_RANGE: MonitorRange = '90m'

let cachedAt = 0
let cachedClasses: Record<number, string> = {}
let inFlight: Promise<Record<number, string>> | null = null

/**
 * Load every group's latest status-bar colour once and reuse it for a minute.
 *
 * The group pickers on the key page open far more often than the monitor data
 * changes, and this call is optional decoration: any failure resolves to an
 * empty map so the picker still renders.
 */
export async function loadGroupStatusClasses(range: MonitorRange = DEFAULT_RANGE): Promise<Record<number, string>> {
  const fresh = Date.now() - cachedAt < CACHE_TTL_MS
  if (fresh && Object.keys(cachedClasses).length > 0) return cachedClasses
  if (inFlight) return inFlight

  inFlight = (async () => {
    try {
      const matrix = await getMatrix({ range, platforms: [], groupIds: [], models: [] }, 'platform_group', false)
      const next: Record<number, string> = {}
      for (const row of matrix.items ?? []) {
        if (typeof row.group_id !== 'number') continue
        const cls = groupStatusClass(row)
        if (cls) next[row.group_id] = cls
      }
      cachedClasses = next
      cachedAt = Date.now()
      return next
    } catch {
      // Monitoring is decoration here; never let it break the picker.
      cachedAt = Date.now()
      return cachedClasses
    } finally {
      inFlight = null
    }
  })()

  return inFlight
}
