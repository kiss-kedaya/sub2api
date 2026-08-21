/**
 * Shared display formatters for channel-monitor-v2.
 * Kept pure so unit tests can lock metric presentation accuracy.
 *
 * Privacy: prefer rates (error_rate, RPM, TPM, success %) over absolute
 * request/error/token counts in user-facing surfaces.
 */

import type { HealthScoreBand, MonitorHealth } from '@/api/channelMonitorV2'
import { formatCompactNumber } from '@/utils/format'

export function monitorIntlLocale(): string {
  if (typeof document !== 'undefined') {
    const htmlLang = document.documentElement.getAttribute('lang')?.trim()
    if (htmlLang) return htmlLang
  }
  if (typeof navigator !== 'undefined' && navigator.language) return navigator.language
  return 'zh-CN'
}

export function formatMonitorNumber(value: number, compactAt = 10000, locale = monitorIntlLocale()): string {
  return Intl.NumberFormat(locale, {
    notation: value >= compactAt ? 'compact' : 'standard',
    maximumFractionDigits: 1,
  }).format(value || 0)
}

export function formatMonitorRate(value: number, locale = monitorIntlLocale()): string {
  return Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value || 0)
}

/**
 * RPM/TPM throughput display: always K/M compact (1.0K, 1.5M) for ops-readable density.
 * Prefer this over formatMonitorRate for rate KPIs and table cells.
 */
export function formatMonitorThroughput(value: number | null | undefined): string {
  if (value == null || Number.isNaN(Number(value))) return '0'
  const n = Number(value)
  if (Math.abs(n) < 1000) {
    return Intl.NumberFormat(monitorIntlLocale(), { maximumFractionDigits: 1 }).format(n)
  }
  return formatCompactNumber(n)
}

/**
 * Backend stores tokens-per-minute as `tpm`. Convert to tokens-per-second for display.
 */
export function tokensPerSecondFromTpm(tpm: number | null | undefined): number {
  if (tpm == null || Number.isNaN(Number(tpm))) return 0
  return Number(tpm) / 60
}

/** Tokens/sec from backend TPM (per-minute), compact ops formatting. */
export function formatMonitorTokensPerSecond(tpm: number | null | undefined): string {
  return formatMonitorThroughput(tokensPerSecondFromTpm(tpm))
}


export function formatMonitorPercent(value: number, locale = monitorIntlLocale()): string {
  return `${new Intl.NumberFormat(locale, {
    minimumFractionDigits: value < 0.01 ? 2 : 1,
    maximumFractionDigits: value < 0.01 ? 2 : 1,
  }).format((value || 0) * 100)}%`
}

/**
 * Color the user-facing availability status by the newest availability value.
 * Boundaries are intentionally ordered from worst to best so that the bands
 * do not overlap: <30 black, <50 red, <60 yellow, <80 pale yellow,
 * <90 light green, and >=90 deep green.
 */
export function availabilityBadgeClass(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) {
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
  if (value < 30) return 'bg-gray-950 text-white dark:bg-black dark:text-white'
  if (value < 50) return 'bg-red-600 text-white dark:bg-red-500 dark:text-white'
  if (value < 60) return 'bg-amber-200 text-amber-950 dark:bg-amber-500/80 dark:text-white'
  if (value < 80) return 'bg-yellow-100 text-yellow-800 dark:bg-yellow-500/20 dark:text-yellow-200'
  if (value < 90) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-200'
  return 'bg-emerald-700 text-white dark:bg-emerald-600 dark:text-white'
}

export function availabilityBarClass(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return 'bg-gray-300 dark:bg-dark-600'
  if (value < 30) return 'bg-gray-950 dark:bg-black'
  if (value < 50) return 'bg-red-500 dark:bg-red-400'
  if (value < 60) return 'bg-amber-400 dark:bg-amber-300'
  if (value < 80) return 'bg-yellow-300 dark:bg-yellow-200'
  if (value < 90) return 'bg-emerald-400 dark:bg-emerald-300'
  return 'bg-emerald-600 dark:bg-emerald-400'
}

export function availabilityTextClass(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return 'text-gray-900 dark:text-gray-100'
  if (value < 30) return 'text-gray-950 dark:text-white'
  if (value < 50) return 'text-red-600 dark:text-red-400'
  if (value < 60) return 'text-amber-700 dark:text-amber-300'
  if (value < 80) return 'text-yellow-700 dark:text-yellow-300'
  if (value < 90) return 'text-emerald-700 dark:text-emerald-300'
  return 'text-emerald-800 dark:text-emerald-300'
}

export function formatMonitorMs(value: number | null | undefined): string {
  if (value == null) return '-'
  return value >= 1000 ? `${(value / 1000).toFixed(1)}s` : `${Math.round(value)}ms`
}

export function formatMonitorSuccessRate(successRequests: number, requestCount: number): string {
  if (!requestCount) return '-'
  return formatMonitorPercent(successRequests / requestCount)
}

export function formatMonitorSuccessRateFromError(errorRate: number): string {
  return formatMonitorPercent(1 - (errorRate || 0))
}

/**
 * Map continuous 0–100 score to 11 fine bands for multi-stop green→yellow→red.
 * score10 = best (green), score0 = worst (red).
 */
export function scoreToBand(score: number | null | undefined): HealthScoreBand {
  if (score == null || Number.isNaN(score)) return 'unknown'
  const clamped = Math.max(0, Math.min(100, score))
  // 0–100 → score0..score10 (11 stops for green→yellow→red gradient)
  const band = Math.round(clamped / 10)
  return `score${Math.max(0, Math.min(10, band))}` as HealthScoreBand
}

export type HealthDisplayMode = 'overall' | 'success' | 'ttft' | 'cache'

/** Resolve the score used for a health mode. */
export function healthModeScore(
  health: MonitorHealth,
  mode: HealthDisplayMode,
): number | null {
  if (mode === 'success') {
    return health.error_rate_score ?? null
  }
  if (mode === 'ttft') {
    return health.ttft_score ?? null
  }
  if (mode === 'cache') {
    return health.cache_score ?? null
  }
  return health.score ?? null
}

export function healthScoreClass(
  health: MonitorHealth,
  mode: HealthDisplayMode,
  requestCount: number,
): string {
  const score = healthModeScore(health, mode)
  if (score == null) {
    if (requestCount <= 0) return 'health-unknown'
    // Fall back to coarse state when score is absent (older payloads).
    const coarse =
      mode === 'success'
        ? health.error_rate
        : mode === 'ttft'
          ? health.ttft
          : mode === 'cache'
            ? health.cache
            : health.overall
    return healthStateClass(coarse)
  }
  return `health-${scoreToBand(score)}`
}

export function healthStateClass(state: string | undefined): string {
  return `health-${state || 'unknown'}`
}

/** Privacy-safe latency summary: avg + p50 + p90 (no absolute sample counts). */
export function formatLatencyPrivacy(
  p50: number | null | undefined,
  p90: number | null | undefined,
  avg?: number | null | undefined,
  p95?: number | null | undefined,
): string {
  const parts: string[] = []
  if (avg != null) parts.push(`AVG ${formatMonitorMs(avg)}`)
  if (p50 != null) parts.push(`P50 ${formatMonitorMs(p50)}`)
  if (p90 != null) parts.push(`P90 ${formatMonitorMs(p90)}`)
  // p95 only as fallback when p90 missing (older payloads)
  if (p90 == null && p95 != null) parts.push(`P95 ${formatMonitorMs(p95)}`)
  return parts.length ? parts.join(' · ') : '-'
}

/**
 * KPI secondary line for latency: AVG + P90 only (P50 is the primary value).
 * Falls back to P95 when P90 is absent. Delimiter is " · " so MetricCell can
 * split into non-truncated chips.
 */
export function formatLatencyKpiSecondary(
  avg?: number | null | undefined,
  p90?: number | null | undefined,
  p95?: number | null | undefined,
): string {
  const parts: string[] = []
  if (avg != null && Number.isFinite(avg)) parts.push(`AVG ${formatMonitorMs(avg)}`)
  if (p90 != null && Number.isFinite(p90)) parts.push(`P90 ${formatMonitorMs(p90)}`)
  else if (p95 != null && Number.isFinite(p95)) parts.push(`P95 ${formatMonitorMs(p95)}`)
  return parts.length ? parts.join(' · ') : '-'
}

/** Fallback when i18n key is missing; prefer `channelMonitorV2.errorCategories.*`. */
export function monitorErrorCategoryLabel(category: string): string {
  return category
}
