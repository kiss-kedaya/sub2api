import { computed, getCurrentScope, onScopeDispose, ref, watch } from 'vue'

/** 单账号滚动窗口内允许的 2FA 校验提交次数（与服务端 TwoFactorRateLimitMaxAttempts 对齐）。 */
export const TOTP_ATTEMPT_LIMIT = 5
/** 滚动窗口时长：60 秒（与服务端 TwoFactorRateLimitWindow 对齐）。 */
export const TOTP_ATTEMPT_WINDOW_MS = 60_000

const STORAGE_PREFIX = 'totp_attempts:'

export interface TotpAttemptLimitOptions {
  /** 账号维度 key（规范化邮箱 / user id）。为空时退化为组件内计数。 */
  accountKey: () => string | undefined
  /** 便于测试注入时钟。 */
  now?: () => number
}

/**
 * 2FA 提交的前端账号维度限速：
 * - 本地记录 60 秒滚动窗口内的提交时间，达到 5 次后禁用输入并倒计时；
 * - 服务端返回 429 Retry-After 时按服务端时间继续禁用（不替代服务端限速）；
 * - 验证成功后 reset()，成功即清；
 * - 计数按账号 key 存 sessionStorage，同一账号在同一标签页内共享。
 */
export function useTotpAttemptLimit(options: TotpAttemptLimitOptions) {
  const now = options.now ?? (() => Date.now())
  const timestamps = ref<number[]>([])
  const serverBlockedUntil = ref(0)
  const remainingSeconds = ref(0)
  let timer: ReturnType<typeof setInterval> | null = null

  function storageKey(): string {
    const key = options.accountKey()
    return key ? `${STORAGE_PREFIX}${key}` : ''
  }

  function load(): void {
    const key = storageKey()
    if (!key) {
      timestamps.value = []
      return
    }
    try {
      const raw = sessionStorage.getItem(key)
      const parsed = raw ? JSON.parse(raw) : []
      timestamps.value = Array.isArray(parsed) ? parsed.filter((t: unknown) => typeof t === 'number') : []
    } catch {
      timestamps.value = []
    }
  }

  function persist(): void {
    const key = storageKey()
    if (!key) return
    try {
      sessionStorage.setItem(key, JSON.stringify(timestamps.value))
    } catch {
      // ignore storage failures (private mode / quota)
    }
  }

  function localBlockedMillis(current: number): number {
    if (timestamps.value.length < TOTP_ATTEMPT_LIMIT) return 0
    const oldest = timestamps.value[0]
    return Math.max(0, oldest + TOTP_ATTEMPT_WINDOW_MS - current)
  }

  function stopTimer(): void {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }

  function ensureTimer(): void {
    if (remainingSeconds.value <= 0) {
      stopTimer()
      return
    }
    if (timer === null) {
      timer = setInterval(refresh, 250)
    }
  }

  function refresh(): number {
    const current = now()
    const kept = timestamps.value.filter((t) => current - t < TOTP_ATTEMPT_WINDOW_MS)
    if (kept.length !== timestamps.value.length) {
      timestamps.value = kept
      persist()
    }
    const blockedUntil = Math.max(
      serverBlockedUntil.value,
      timestamps.value.length >= TOTP_ATTEMPT_LIMIT ? current + localBlockedMillis(current) : 0
    )
    remainingSeconds.value = Math.max(0, Math.ceil((blockedUntil - current) / 1000))
    ensureTimer()
    return remainingSeconds.value
  }

  /** 是否允许提交（服务端 429 或本地窗口内的第 6 次都会被拦下）。 */
  const canSubmit = computed(() => remainingSeconds.value <= 0)

  /** 记录一次提交；被限速时返回 false，调用方不要发请求。 */
  function recordAttempt(): boolean {
    refresh()
    if (!canSubmit.value) return false
    timestamps.value = [...timestamps.value, now()]
    persist()
    refresh()
    return true
  }

  /** 应用服务端 429 的 Retry-After（秒），取本地/服务端更大者。 */
  function applyServerRetryAfter(seconds: number): void {
    if (!Number.isFinite(seconds) || seconds <= 0) return
    serverBlockedUntil.value = Math.max(serverBlockedUntil.value, now() + seconds * 1000)
    refresh()
  }

  /** 验证成功后清空（成功即清）。 */
  function reset(): void {
    timestamps.value = []
    serverBlockedUntil.value = 0
    persist()
    refresh()
  }

  watch(
    () => options.accountKey(),
    () => {
      load()
      refresh()
    }
  )

  load()
  refresh()
  if (getCurrentScope()) {
    onScopeDispose(stopTimer)
  }

  return {
    canSubmit,
    remainingSeconds,
    recordAttempt,
    applyServerRetryAfter,
    reset,
    refresh,
    stopTimer
  }
}
