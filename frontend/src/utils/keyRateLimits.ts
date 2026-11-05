import type { ApiKey } from '@/types'

export type UsageTone = 'ok' | 'warn' | 'danger'

export interface RateLimitWindow {
  label: '5h' | '1d' | '7d'
  limit: number
  used: number
  /** 0-100, capped so an over-limit key still draws a full bar. */
  percent: number
  tone: UsageTone
  resetAt: string | null
}

export const RATE_LIMIT_TEXT_CLASS: Record<UsageTone, string> = {
  ok: 'text-gray-700 dark:text-gray-300',
  warn: 'text-yellow-500',
  danger: 'text-red-500',
}

export const RATE_LIMIT_BAR_CLASS: Record<UsageTone, string> = {
  ok: 'bg-emerald-500',
  warn: 'bg-yellow-500',
  danger: 'bg-red-500',
}

/** Spend against a limit: danger once it is reached, warn from 80%. */
export function usageTone(used: number, limit: number): UsageTone {
  if (used >= limit) return 'danger'
  if (used >= limit * 0.8) return 'warn'
  return 'ok'
}

const WINDOWS = ['5h', '1d', '7d'] as const

/** The rolling windows a key really has a limit on, in 5h → 1d → 7d order. */
export function rateLimitWindows(key: ApiKey): RateLimitWindow[] {
  return WINDOWS.flatMap((label) => {
    const limit = key[`rate_limit_${label}`]
    if (!(limit > 0)) return []
    const used = key[`usage_${label}`] ?? 0
    return [{
      label,
      limit,
      used,
      percent: Math.min((used / limit) * 100, 100),
      tone: usageTone(used, limit),
      resetAt: key[`reset_${label}_at`] ?? null,
    }]
  })
}
