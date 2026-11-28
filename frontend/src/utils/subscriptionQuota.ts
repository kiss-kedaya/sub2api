import type { UserSubscription } from '@/types'

const ONE_DAY_MS = 24 * 60 * 60 * 1000

export type ExpirationDateRelation = 'expired' | 'today' | 'tomorrow' | 'later'

export type RemainingExpiryDuration =
  | { unit: 'days'; days: number }
  | { unit: 'hoursMinutes'; hours: number; minutes: number }

export interface RemainingDurationParts {
  days: number
  hours: number
  minutes: number
}

export type DailyQuotaWindowDisplay =
  | { kind: 'reset'; targetAt: Date }
  | { kind: 'quotaEnds'; targetAt: Date }

export function isOneTimeDailyQuota(
  subscription: Pick<UserSubscription, 'starts_at' | 'expires_at'>
): boolean {
  if (!subscription.starts_at || !subscription.expires_at) return false

  const startsAt = new Date(subscription.starts_at).getTime()
  const expiresAt = new Date(subscription.expires_at).getTime()

  if (!Number.isFinite(startsAt) || !Number.isFinite(expiresAt)) return false

  return expiresAt <= startsAt + ONE_DAY_MS
}

export function resolveDailyQuotaWindowDisplay(
  subscription: Pick<
    UserSubscription,
    'starts_at' | 'expires_at' | 'daily_window_start' | 'next_daily_reset_at' | 'group'
  >
): DailyQuotaWindowDisplay | null {
  if (!subscription.daily_window_start) return null

  const expiresMs = subscription.expires_at ? new Date(subscription.expires_at).getTime() : NaN
  const hasValidExpiry = Number.isFinite(expiresMs)

  // Prefer server-computed next_daily_reset_at when present (handles server timezone & anchored 24h mode).
  if (subscription.next_daily_reset_at) {
    const nextResetMs = new Date(subscription.next_daily_reset_at).getTime()
    if (Number.isFinite(nextResetMs)) {
      if (hasValidExpiry && nextResetMs >= expiresMs) {
        return { kind: 'quotaEnds', targetAt: new Date(expiresMs) }
      }
      return { kind: 'reset', targetAt: new Date(nextResetMs) }
    }
  }

  if (isOneTimeDailyQuota(subscription) && hasValidExpiry) {
    return { kind: 'quotaEnds', targetAt: new Date(expiresMs) }
  }

  const windowStartMs = new Date(subscription.daily_window_start).getTime()
  if (!Number.isFinite(windowStartMs)) return null

  let nextResetMs: number
  if (subscription.group?.daily_reset_mode === 'anchored_24h' && subscription.starts_at) {
    const startsMs = new Date(subscription.starts_at).getTime()
    if (!Number.isFinite(startsMs)) return null
    const windowIndex = windowStartMs < startsMs
      ? 0
      : Math.floor((windowStartMs - startsMs) / ONE_DAY_MS)
    nextResetMs = startsMs + (windowIndex + 1) * ONE_DAY_MS
  } else {
    nextResetMs = windowStartMs + ONE_DAY_MS
  }

  if (hasValidExpiry && nextResetMs >= expiresMs) {
    return { kind: 'quotaEnds', targetAt: new Date(expiresMs) }
  }

  return { kind: 'reset', targetAt: new Date(nextResetMs) }
}

export function getRemainingDurationParts(
  targetAt: Date | string,
  now: Date = new Date()
): RemainingDurationParts | null {
  const targetTime = targetAt instanceof Date ? targetAt.getTime() : new Date(targetAt).getTime()
  const nowTime = now.getTime()

  if (!Number.isFinite(targetTime) || !Number.isFinite(nowTime)) return null

  const diffMs = targetTime - nowTime
  if (diffMs <= 0) return null

  const totalMinutes = Math.floor(diffMs / (1000 * 60))
  const days = Math.floor(totalMinutes / (24 * 60))
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60)
  const minutes = totalMinutes % 60

  return { days, hours, minutes }
}

export function getExpirationDateRelation(
  targetAt: Date | string,
  now: Date = new Date()
): ExpirationDateRelation | null {
  const target = targetAt instanceof Date ? targetAt : new Date(targetAt)
  const targetTime = target.getTime()
  const nowTime = now.getTime()

  if (!Number.isFinite(targetTime) || !Number.isFinite(nowTime)) return null
  if (targetTime <= nowTime) return 'expired'

  const targetDay = Date.UTC(target.getFullYear(), target.getMonth(), target.getDate())
  const currentDay = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate())
  const calendarDays = Math.round((targetDay - currentDay) / ONE_DAY_MS)

  if (calendarDays === 0) return 'today'
  if (calendarDays === 1) return 'tomorrow'
  return 'later'
}

export function getRemainingExpiryDuration(
  targetAt: Date | string,
  now: Date = new Date()
): RemainingExpiryDuration | null {
  const targetTime = targetAt instanceof Date ? targetAt.getTime() : new Date(targetAt).getTime()
  const nowTime = now.getTime()

  if (!Number.isFinite(targetTime) || !Number.isFinite(nowTime)) return null

  const diffMs = targetTime - nowTime
  if (diffMs <= 0) return null
  if (diffMs >= ONE_DAY_MS) {
    return { unit: 'days', days: Math.ceil(diffMs / ONE_DAY_MS) }
  }

  const totalMinutes = Math.ceil(diffMs / (60 * 1000))
  return {
    unit: 'hoursMinutes',
    hours: Math.floor(totalMinutes / 60),
    minutes: totalMinutes % 60
  }
}
