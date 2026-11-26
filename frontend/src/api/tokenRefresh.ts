import axios from 'axios'
import type { ApiResponse } from '@/types'
import { getAPIBaseURL } from './url'

const AUTH_TOKEN_KEY = 'auth_token'
const AUTH_USER_KEY = 'auth_user'
const REFRESH_TOKEN_KEY = 'refresh_token'
const TOKEN_EXPIRES_AT_KEY = 'token_expires_at'
const TOKEN_REFRESH_LOCK_NAME = 'sub2api-auth-token-refresh'
// 10 秒：服务端对「响应丢失后的重试」只给很短的宽限窗口，请求挂 30 秒再重试就落在窗口之外了。
const TOKEN_REFRESH_TIMEOUT_MS = 10_000
// 网络错误 / 5xx 之后，用同一枚 refresh token 重试一次的等待时间（2–4 秒的抖动）：落在服务端的宽限窗口内，
// 又给网络和网关留出恢复的时间。不靠放大服务端宽限来兜底。
const REFRESH_RETRY_MIN_DELAY_MS = 2_000
const REFRESH_RETRY_JITTER_MS = 2_000
const PEER_REFRESH_WAIT_MS = 1_000
const PEER_REFRESH_GRACE_MS = 1_000
const PEER_REFRESH_POLL_MS = 25
// 200 响应但内容不是预期的 JSON（被网关/WAF 换成了 HTML 页面等）：当作暂时性失败，不当作令牌被拒。
const BAD_REFRESH_RESPONSE_CODE = 'ERR_BAD_REFRESH_RESPONSE'

/**
 * 服务端明确拒绝这枚 refresh token 时的业务码（按 HTTP 状态分组）。只有命中这里才允许清登录态；
 * 其他一切失败（超时、网络错误、5xx、429、Cloudflare/WAF 的 403/468/408、没有业务码的 4xx、
 * 200 返回 HTML）都不是令牌的问题，保留会话让下一次请求再试。
 */
const REFRESH_REJECTION_REASONS: Record<number, ReadonlySet<string>> = {
  401: new Set([
    'REFRESH_TOKEN_INVALID',
    'REFRESH_TOKEN_EXPIRED',
    'REFRESH_TOKEN_REUSE',
    'REFRESH_TOKEN_REUSED',
    'REFRESH_TOKEN_SESSION_EXPIRED',
    'SESSION_BINDING_MISMATCH',
    'TOKEN_REVOKED'
  ]),
  // 账号已被停用：服务端已经撤销了会话，重试没有意义。
  // 后端模式（只允许管理员）下非管理员的会话再也刷新不了：服务端在消费令牌之前拒绝，业务码明确，清登录态回到登录页。
  403: new Set(['USER_NOT_ACTIVE', 'BACKEND_MODE_ADMIN_ONLY'])
}

export interface RefreshTokenResponse {
  access_token: string
  refresh_token: string
  expires_in: number
  token_type: string
}

export interface RefreshAuthTokensOptions {
  /** Access token attached to the request that received a 401 response. */
  failedAccessToken?: string | null
}

interface AuthSnapshot {
  accessToken: string | null
  refreshToken: string
  expiresAt: number
  userID: number | null
}

let inFlightRefresh: Promise<RefreshTokenResponse> | null = null

function responseOf(error: unknown): { status?: unknown; data?: unknown } | undefined {
  return (error as { response?: { status?: unknown; data?: unknown } } | null | undefined)?.response
}

/** 服务端在刷新失败响应里给出的业务码（`reason`，兼容 `code` 是字符串的响应形状）。 */
export function refreshRejectionReason(error: unknown): string {
  const data = responseOf(error)?.data as { reason?: unknown; code?: unknown } | null | undefined
  const reason = data?.reason ?? data?.code
  return typeof reason === 'string' ? reason : ''
}

/**
 * 服务端是否明确拒绝了这枚 refresh token（401 + 令牌被拒的业务码，或 403 账号停用）。
 * 只有这种情况才该清除本地登录态。
 */
export function isRefreshTokenRejection(error: unknown): boolean {
  const status = responseOf(error)?.status
  if (typeof status !== 'number') {
    return false
  }
  return REFRESH_REJECTION_REASONS[status]?.has(refreshRejectionReason(error)) ?? false
}

/** 暂时性失败：网络错误 / 超时 / 5xx / 200 但内容不对。这类失败值得用同一枚令牌重试一次。 */
function isTransientRefreshFailure(error: unknown): boolean {
  const response = responseOf(error)
  if (response) {
    return typeof response.status === 'number' && response.status >= 500
  }
  const code = (error as { code?: unknown } | null | undefined)?.code
  return (
    code === 'ERR_NETWORK' ||
    code === 'ECONNABORTED' ||
    code === 'ETIMEDOUT' ||
    code === BAD_REFRESH_RESPONSE_CODE
  )
}

function badRefreshResponse(message: string): Error {
  return Object.assign(new Error(message), { code: BAD_REFRESH_RESPONSE_CODE })
}

function getStoredUserID(): number | null {
  const rawUser = localStorage.getItem(AUTH_USER_KEY)
  if (!rawUser) {
    return null
  }

  try {
    const id = Number((JSON.parse(rawUser) as { id?: unknown }).id)
    return Number.isFinite(id) && id > 0 ? id : null
  } catch {
    return null
  }
}

function readAuthSnapshot(): AuthSnapshot {
  const refreshToken = localStorage.getItem(REFRESH_TOKEN_KEY)
  if (!refreshToken) {
    throw new Error('No refresh token available')
  }

  return {
    accessToken: localStorage.getItem(AUTH_TOKEN_KEY),
    refreshToken,
    expiresAt: Number(localStorage.getItem(TOKEN_EXPIRES_AT_KEY)),
    userID: getStoredUserID()
  }
}

function readStoredTokenPair(snapshot: AuthSnapshot): RefreshTokenResponse | null {
  const accessToken = localStorage.getItem(AUTH_TOKEN_KEY)
  const refreshToken = localStorage.getItem(REFRESH_TOKEN_KEY)
  const expiresAt = Number(localStorage.getItem(TOKEN_EXPIRES_AT_KEY))

  if (
    !accessToken ||
    !refreshToken ||
    !Number.isFinite(expiresAt) ||
    expiresAt <= Date.now() ||
    getStoredUserID() !== snapshot.userID
  ) {
    return null
  }

  return {
    access_token: accessToken,
    refresh_token: refreshToken,
    expires_in: Math.max(1, Math.ceil((expiresAt - Date.now()) / 1000)),
    token_type: 'Bearer'
  }
}

function readPeerRefreshResult(
  snapshot: AuthSnapshot,
  failedAccessToken?: string | null
): RefreshTokenResponse | null {
  const storedPair = readStoredTokenPair(snapshot)
  if (!storedPair) {
    return null
  }

  if (storedPair.refresh_token !== snapshot.refreshToken) {
    return storedPair
  }

  if (
    failedAccessToken &&
    snapshot.accessToken !== failedAccessToken &&
    storedPair.access_token === snapshot.accessToken
  ) {
    return storedPair
  }

  return null
}

async function waitForPeerRefresh(
  snapshot: AuthSnapshot,
  failedAccessToken?: string | null,
  deadline = Date.now() + PEER_REFRESH_WAIT_MS
): Promise<RefreshTokenResponse | null> {
  while (Date.now() < deadline) {
    const peerResult = readPeerRefreshResult(snapshot, failedAccessToken)
    if (peerResult) {
      return peerResult
    }
    await new Promise((resolve) => window.setTimeout(resolve, PEER_REFRESH_POLL_MS))
  }

  return readPeerRefreshResult(snapshot, failedAccessToken)
}

function persistTokenPair(tokens: RefreshTokenResponse): void {
  localStorage.setItem(AUTH_TOKEN_KEY, tokens.access_token)
  localStorage.setItem(TOKEN_EXPIRES_AT_KEY, String(Date.now() + tokens.expires_in * 1000))
  // The rotating refresh token is written last so other tabs can treat its change as a commit marker.
  localStorage.setItem(REFRESH_TOKEN_KEY, tokens.refresh_token)
}

/** 这次刷新开始之后，本地会话是否还是同一个（没有被登出、换号，也没有被别的标签页换发新令牌）。 */
function sessionUnchanged(snapshot: AuthSnapshot): boolean {
  return (
    localStorage.getItem(REFRESH_TOKEN_KEY) === snapshot.refreshToken &&
    getStoredUserID() === snapshot.userID
  )
}

async function requestTokenPair(
  snapshot: AuthSnapshot,
  failedAccessToken?: string | null,
  mayHaveUncoordinatedPeer = false
): Promise<RefreshTokenResponse> {
  // If this request loses a one-time-token race, the winning peer can legitimately take as long
  // as our own HTTP timeout to publish its replacement token. Keep the recovery window tied to
  // that timeout instead of an arbitrary short delay.
  const peerRefreshDeadline = Date.now() + TOKEN_REFRESH_TIMEOUT_MS + PEER_REFRESH_GRACE_MS

  let retried = false
  for (;;) {
    try {
      const response = await axios.post<ApiResponse<RefreshTokenResponse>>(
        `${getAPIBaseURL()}/auth/refresh`,
        { refresh_token: snapshot.refreshToken },
        { headers: { 'Content-Type': 'application/json' }, timeout: TOKEN_REFRESH_TIMEOUT_MS }
      )
      const payload = response.data
      if (!payload || payload.code !== 0 || !payload.data) {
        throw badRefreshResponse(payload?.message || 'Token refresh failed')
      }

      if (
        localStorage.getItem(REFRESH_TOKEN_KEY) !== snapshot.refreshToken ||
        getStoredUserID() !== snapshot.userID
      ) {
        const peerResult = readPeerRefreshResult(snapshot, failedAccessToken)
        if (peerResult) {
          return peerResult
        }
        throw new Error('Session changed during token refresh')
      }

      persistTokenPair(payload.data)
      return payload.data
    } catch (error) {
      // 网络错误 / 超时 / 5xx / 200 返回了不是 JSON 的内容：响应可能是在服务端轮换之后丢的，
      // 等 2–4 秒后用同一枚令牌重试一次（服务端对「消费它的那个客户端」的短时间重试有宽限）。
      // 等待期间别的标签页若已经换发了新令牌，直接采用它，不再发请求；会话被登出/换号则不重试。
      if (!retried && isTransientRefreshFailure(error) && sessionUnchanged(snapshot)) {
        retried = true
        const retryDelay = REFRESH_RETRY_MIN_DELAY_MS + Math.random() * REFRESH_RETRY_JITTER_MS
        const peerResult = await waitForPeerRefresh(snapshot, failedAccessToken, Date.now() + retryDelay)
        if (peerResult) {
          return peerResult
        }
        if (sessionUnchanged(snapshot)) {
          continue
        }
        throw error
      }

      // A peer tab may have rotated the one-time refresh token while this request was in flight.
      // A 4xx response can arrive quickly while the winning peer's response is still in flight, so
      // wait through the shared request deadline before treating the session as expired. Transient
      // non-4xx failures retain the short reconciliation window.
      const responseStatus = responseOf(error)?.status
      const isTokenRejection =
        typeof responseStatus === 'number' && responseStatus >= 400 && responseStatus < 500
      const peerResult = await waitForPeerRefresh(
        snapshot,
        failedAccessToken,
        isTokenRejection && mayHaveUncoordinatedPeer
          ? peerRefreshDeadline
          : Date.now() + PEER_REFRESH_WAIT_MS
      )
      if (peerResult) {
        return peerResult
      }
      throw error
    }
  }
}

async function runRefresh(options: RefreshAuthTokensOptions): Promise<RefreshTokenResponse> {
  const snapshot = readAuthSnapshot()
  const refresh = async (mayHaveUncoordinatedPeer = false): Promise<RefreshTokenResponse> => {
    const peerResult = readPeerRefreshResult(snapshot, options.failedAccessToken)
    if (peerResult) {
      return peerResult
    }
    return requestTokenPair(snapshot, options.failedAccessToken, mayHaveUncoordinatedPeer)
  }

  if (typeof navigator !== 'undefined' && navigator.locks) {
    return navigator.locks.request(TOKEN_REFRESH_LOCK_NAME, () => refresh(false))
  }

  return refresh(true)
}

/**
 * Refresh and persist the browser session.
 *
 * Calls in the same document share one promise. Web Locks serialize refreshes across tabs, while
 * the token snapshot check adopts a peer's newly rotated token instead of logging the user out.
 */
export function refreshAuthTokens(
  options: RefreshAuthTokensOptions = {}
): Promise<RefreshTokenResponse> {
  if (inFlightRefresh) {
    return inFlightRefresh
  }

  const pending = runRefresh(options)
  inFlightRefresh = pending
  const clearPending = (): void => {
    if (inFlightRefresh === pending) {
      inFlightRefresh = null
    }
  }
  void pending.then(clearPending, clearPending)
  return pending
}
