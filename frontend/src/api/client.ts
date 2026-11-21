/**
 * Axios HTTP Client Configuration
 * Base client with interceptors for authentication, token refresh, and error handling
 */

import axios, { AxiosInstance, AxiosError, InternalAxiosRequestConfig, AxiosResponse } from 'axios'
import type { ApiResponse } from '@/types'
import { getLocale } from '@/i18n'
import {
  STEP_UP_CANCELLED,
  STEP_UP_REQUIRED,
  isStepUpCancelled,
  localizeStepUpErrorCode,
  stepUpMarkerOf,
} from '@/utils/stepUpErrors'
import { isStepUpHostAvailable, retryWithGlobalStepUp } from '@/composables/useStepUpGate'
import { recordStepUpSkip } from '@/composables/useStepUpSkip'
import {
  ADMIN_UI_REQUEST_HEADER,
  USER_UI_REQUEST_HEADER,
  shouldMarkAdminUIRequest,
  shouldMarkUserUIRequest,
} from './adminUIRequest'
import { refreshAuthTokens } from './tokenRefresh'
import { getAPIBaseURL } from './url'
import { KEY_ROTATION_REQUIRED_EVENT, isKeyRotationRequiredPayload } from './keyRotationGateCodes'
import { getAppRuntimeContext, type AppRuntimeContext } from '@runtime/context'
export { buildApiUrl, buildGatewayUrl } from './url'

declare module 'axios' {
  interface AxiosRequestConfig {
    /**
     * Marks a page-load / background read. A STEP_UP_REQUIRED 403 on such a
     * request never opens the TOTP dialog; it rejects so the caller can render
     * a degraded placeholder instead (see src/api/silentStepUp.ts).
     */
    silentStepUp?: boolean
  }
}

// ==================== Axios Instance Configuration ====================

// Heavy usage/report endpoints can legitimately scan a large usage history.
// Keep the normal API budget at 30s, but give report requests the same 180s
// budget as the corresponding backend handlers.  Centralising this rule is
// intentional: a new admin report must not silently regress to 30s merely
// because its caller forgot an endpoint-local Axios option.
export const LONG_RUNNING_REPORT_TIMEOUT_MS = 180000

const longRunningReportPath = (url: string): boolean => {
  const path = url
    .split('?')[0]
    .replace(/^https?:\/\/[^/]+/i, '')
    .replace(/^\/api\/v\d+\//i, '/')
  return (
    /^\/?admin\/accounts\/[^/]+\/stats(?:\/|$)/i.test(path) ||
    /^\/?admin\/(?:usage\/stats|dashboard\/|ops\/(?:dashboard\/|errors(?:\/|$)|request-errors(?:\/|$)|upstream-errors(?:\/|$)))/i.test(path) ||
    /^\/?usage\/(?:stats|dashboard\/)/i.test(path)
  )
}

// ==================== Step-up (sudo) 2FA ====================

/**
 * Endpoints that consume the TOTP code itself; a gate prompt there would deadlock.
 */
const stepUpVerificationPath = (url: string): boolean =>
  /\/user\/totp\/step-up(?:\?|$)/i.test(url)

/**
 * Backend step-up markers are localized through the shared i18n translator
 * (registered in src/i18n/index.ts). Falling back to the backend message keeps
 * SSR/pre-bootstrap requests readable; the UI never sees that English text in
 * the browser because the app shell always registers the translator first.
 */
function localizeStepUpMessage(marker: string, fallback?: string): string {
  return localizeStepUpErrorCode(marker) ?? fallback ?? marker
}

// ==================== Request Interceptor ====================

function createApiClient(context?: AppRuntimeContext): AxiosInstance {
const apiClient = axios.create({
  baseURL: getAPIBaseURL(), withCredentials: true, timeout: 30000,
  headers: { 'Content-Type': 'application/json' },
})

// Get user's timezone
const getUserTimezone = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return 'UTC'
  }
}

apiClient.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const runtime = context ?? getAppRuntimeContext()
    if (runtime?.apiAdapter) config.adapter = runtime.apiAdapter
    const requestURL = String(config.url || '')
    if (longRunningReportPath(requestURL)) {
      // Respect an explicitly larger caller timeout, while ensuring report
      // calls never inherit the shared 30s default.
      config.timeout = Math.max(config.timeout ?? 0, LONG_RUNNING_REPORT_TIMEOUT_MS)
    }

    // Attach token from localStorage
    const token = !runtime?.cookieSession && typeof localStorage !== 'undefined'
      ? localStorage.getItem('auth_token') : null
    if (token && config.headers) {
      config.headers.Authorization = `Bearer ${token}`
    }

    // Attach locale for backend translations
    if (config.headers) {
      config.headers['Accept-Language'] = getLocale()
    }

    // Attach timezone for all GET requests (backend may use it for default date ranges)
    if (config.method === 'get') {
      if (!config.params) {
        config.params = {}
      }
      config.params.timezone = getUserTimezone()
    }

    if (config.headers) {
      if (shouldMarkAdminUIRequest(requestURL)) {
        config.headers[ADMIN_UI_REQUEST_HEADER] = '1'
      }
      if (shouldMarkUserUIRequest(requestURL)) {
        config.headers[USER_UI_REQUEST_HEADER] = '1'
      }
    }

    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

// ==================== Step-up skip notice ====================

/**
 * The backend gate marks every sensitive request that skipped the TOTP prompt
 * because the 5-minute step-up window was still valid:
 * `X-StepUp-Skipped: 1` + `X-StepUp-Remaining: <seconds>`. Feed the shared
 * notice store so the operator always sees "2FA skipped · time left" instead of
 * a silent bypass. Header lookup tolerates both AxiosHeaders#get and plain
 * objects (adapter mocks in tests use plain objects).
 */
function syncStepUpSkipFromHeaders(headers: unknown): void {
  if (!headers || typeof headers !== 'object') return
  const read = (name: string): unknown => {
    const container = headers as { get?: (key: string) => unknown } & Record<string, unknown>
    if (typeof container.get === 'function') {
      const viaGet = container.get(name)
      if (viaGet !== undefined && viaGet !== null) return viaGet
    }
    return container[name] ?? container[name.toLowerCase()]
  }
  const skipped = read('x-stepup-skipped')
  if (String(skipped) !== '1') return
  const remaining = Number(read('x-stepup-remaining'))
  if (Number.isFinite(remaining) && remaining > 0) {
    recordStepUpSkip(remaining)
  }
}

// ==================== Response Interceptor ====================

apiClient.interceptors.response.use(
  (response: AxiosResponse) => {
    // Sensitive request executed without a TOTP prompt: surface the remaining
    // grace window in the global bottom-right notice.
    syncStepUpSkipFromHeaders(response.headers)
    // Unwrap standard API response format { code, message, data }
    const apiResponse = response.data as ApiResponse<unknown>
    if (apiResponse && typeof apiResponse === 'object' && 'code' in apiResponse) {
      if (apiResponse.code === 0) {
        // Success - return the data portion
        response.data = apiResponse.data
      } else {
        // API error
        const resp = apiResponse as unknown as Record<string, unknown>
        return Promise.reject({
          status: response.status,
          code: apiResponse.code,
          message: apiResponse.message || 'Unknown error',
          reason: resp.reason,
          metadata: resp.metadata,
        })
      }
    }
    return response
  },
  async (error: AxiosError<ApiResponse<unknown>>) => {
    // Request cancellation: keep the original axios cancellation error so callers can ignore it.
    // Otherwise we'd misclassify it as a generic "network error".
    if (error.code === 'ERR_CANCELED' || axios.isCancel(error)) {
      return Promise.reject(error)
    }

    const originalRequest = error.config as InternalAxiosRequestConfig & {
      _retry?: boolean
      _stepUpRetried?: boolean
    }

    // Handle common errors
    if (error.response) {
      const { status, data } = error.response
      // Even a 4xx can carry the skip marker (gate passed, handler rejected the
      // body): keep the notice in sync with what the backend actually reported.
      syncStepUpSkipFromHeaders(error.response.headers)
      const url = String(error.config?.url || '')

      // Validate `data` shape to avoid HTML error pages breaking our error handling.
      const apiData = (typeof data === 'object' && data !== null ? data : {}) as Record<string, any>

      // Step-up (sudo) 2FA: the backend gates sensitive admin operations behind a
      // fresh TOTP grant and answers 403 STEP_UP_REQUIRED. Every admin API call goes
      // through this client, so handling it here covers pages that do not (and new
      // pages that will not) wrap calls in useStepUp. The prompt is app-wide and
      // single-flight: concurrent 403s share one dialog, then each request retries
      // once with the newly minted grant.
      //
      // Page-load/background reads are explicitly marked `silentStepUp` (see
      // src/api/silentStepUp.ts): a background read must never interrupt the
      // operator with a TOTP dialog. It rejects instead so the caller degrades.
      const stepUpMarker = stepUpMarkerOf({ code: apiData.code, reason: apiData.reason })
      if (stepUpMarker) {
        if (originalRequest.silentStepUp === true) {
          return Promise.reject({
            status,
            code: stepUpMarker,
            reason: stepUpMarker,
            message: localizeStepUpMessage(stepUpMarker, apiData.message),
            silentStepUp: true,
            metadata: apiData.metadata,
          })
        }
        const retryRequest =
          stepUpMarker === STEP_UP_REQUIRED &&
          !originalRequest._stepUpRetried &&
          !stepUpVerificationPath(url) &&
          typeof window !== 'undefined' &&
          isStepUpHostAvailable()
        if (retryRequest) {
          originalRequest._stepUpRetried = true
          try {
            return await retryWithGlobalStepUp(() => apiClient(originalRequest))
          } catch (cancelled) {
            if (!isStepUpCancelled(cancelled)) throw cancelled
            // Operator dismissed the dialog: degrade quietly. Callers get a plain
            // object (no StepUpCancelledError stack in the console) carrying the
            // localized Chinese copy; page-level handlers decide whether/how to
            // surface it (most already catch step-up cancellation).
            const message = localizeStepUpMessage(STEP_UP_CANCELLED) ?? 'Step-up verification cancelled'
            return Promise.reject({
              status,
              code: STEP_UP_CANCELLED,
              reason: STEP_UP_CANCELLED,
              message,
              cancelled: true,
            })
          }
        }
        // Non-retryable step-up state (or no dialog host / failed retry):
        // reject with localized copy instead of the backend's English message.
        return Promise.reject({
          status,
          code: stepUpMarker,
          reason: stepUpMarker,
          message: localizeStepUpMessage(stepUpMarker, apiData.message),
          metadata: apiData.metadata,
        })
      }

      // 密钥轮换门禁：兑换/充值/USDT 相关接口会返回 403 KEY_ROTATION_REQUIRED。
      // 在这里统一广播，由 useKeyRotationGate 标记状态并展示整改卡片，
      // 页面自己的 catch 只需避免弹出误导性错误提示。
      if (status === 403 && isKeyRotationRequiredPayload(apiData) && typeof window !== 'undefined') {
        try {
          window.dispatchEvent(new CustomEvent(KEY_ROTATION_REQUIRED_EVENT))
        } catch {
          // ignore event failures
        }
      }

      // Ops monitoring disabled: treat as feature-flagged 404, and proactively redirect away
      // from ops pages to avoid broken UI states.
      if (status === 404 && apiData.message === 'Ops monitoring is disabled') {
        try {
          localStorage.setItem('ops_monitoring_enabled_cached', 'false')
        } catch {
          // ignore localStorage failures
        }
        try {
          window.dispatchEvent(new CustomEvent('ops-monitoring-disabled'))
        } catch {
          // ignore event failures
        }

        if (window.location.pathname.startsWith('/admin/ops')) {
          window.location.href = '/admin/settings'
        }

        return Promise.reject({
          status,
          code: 'OPS_DISABLED',
          message: apiData.message || error.message,
          url
        })
      }

      if (status === 423 && apiData.code === 'ADMIN_COMPLIANCE_ACK_REQUIRED') {
        try {
          window.dispatchEvent(new CustomEvent('admin-compliance-required', {
            detail: apiData.metadata || {}
          }))
        } catch {
          // ignore event failures
        }

        return Promise.reject({
          status,
          code: apiData.code,
          message: apiData.message || error.message,
          metadata: apiData.metadata,
        })
      }

      const cookieSession = Boolean(context?.cookieSession || getAppRuntimeContext()?.cookieSession)
      const headers = error.response.headers
      const rawRetryAfter = status === 429
        ? (headers instanceof axios.AxiosHeaders
          ? headers.get('Retry-After')
          : Object.entries(headers ?? {}).find(([name]) => name.toLowerCase() === 'retry-after')?.[1])
        : undefined
      const retryAfter = typeof rawRetryAfter === 'string' || typeof rawRetryAfter === 'number' ? String(rawRetryAfter) : undefined
      if (cookieSession && status === 401 && typeof window !== 'undefined') {
        window.dispatchEvent(new CustomEvent('auth-session-expired'))
        if (!window.location.pathname.includes('/login')) window.location.href = '/login'
      }

      // Server rendering must never touch browser storage or redirect a global window.
      // The BFF owns HttpOnly session rotation; browser cookie expiry is handled above.
      if (typeof window === 'undefined' || cookieSession) {
        return Promise.reject({ status, ...(retryAfter ? { retryAfter } : {}), code: apiData.code, reason: apiData.reason,
          error: apiData.error, message: apiData.message || apiData.detail || error.message,
          metadata: apiData.metadata })
      }

      // 401: Try to refresh the token if we have a refresh token
      // This handles TOKEN_EXPIRED, INVALID_TOKEN, TOKEN_REVOKED, etc.
      if (status === 401 && !originalRequest._retry) {
        const refreshToken = localStorage.getItem('refresh_token')
        const isAuthEndpoint =
          url.includes('/auth/login') || url.includes('/auth/register') || url.includes('/auth/refresh')

        // If we have a refresh token and this is not an auth endpoint, try to refresh
        if (refreshToken && !isAuthEndpoint) {
          const refreshSessionUser = localStorage.getItem('auth_user')
          originalRequest._retry = true

          try {
            const headers = originalRequest.headers as Record<string, unknown> | undefined
            const authHeader = headers?.Authorization ?? headers?.authorization
            const failedAccessToken =
              typeof authHeader === 'string' && authHeader.startsWith('Bearer ')
                ? authHeader.slice('Bearer '.length)
                : null
            const tokens = await refreshAuthTokens({ failedAccessToken })

            // Retry the original request with the refreshed token
            if (originalRequest.headers) {
              originalRequest.headers.Authorization = `Bearer ${tokens.access_token}`
            }
            return apiClient(originalRequest)
          } catch (refreshError) {
            // A stale request must never destroy a session that was logged out or replaced while
            // its refresh was in flight (for example, when another tab signs in as another user).
            const sessionChanged =
              localStorage.getItem('refresh_token') !== refreshToken ||
              localStorage.getItem('auth_user') !== refreshSessionUser
            if (sessionChanged) {
              return Promise.reject({
                status: 401,
                code: 'AUTH_SESSION_CHANGED',
                message: 'Authentication session changed while refreshing.'
              })
            }

            if (axios.isAxiosError(refreshError)) {
              const refreshStatus = refreshError.response?.status ?? 0
              if (refreshStatus === 0 || refreshStatus === 429 || refreshStatus >= 500) {
                return Promise.reject({
                  status: refreshStatus,
                  code: 'TOKEN_REFRESH_UNAVAILABLE',
                  message: refreshError.response?.data?.message || refreshError.message
                })
              }
            }

            // Clear tokens and redirect to login
            localStorage.removeItem('auth_token')
            localStorage.removeItem('refresh_token')
            localStorage.removeItem('auth_user')
            localStorage.removeItem('token_expires_at')
            sessionStorage.setItem('auth_expired', '1')

            if (!window.location.pathname.includes('/login')) {
              window.location.href = '/login'
            }

            return Promise.reject({
              status: 401,
              code: 'TOKEN_REFRESH_FAILED',
              message: 'Session expired. Please log in again.'
            })
          }
        }

        // No refresh token or is auth endpoint - clear auth and redirect
        const hasToken = !!localStorage.getItem('auth_token')
        const headers = error.config?.headers as Record<string, unknown> | undefined
        const authHeader = headers?.Authorization ?? headers?.authorization
        const sentAuth =
          typeof authHeader === 'string'
            ? authHeader.trim() !== ''
            : Array.isArray(authHeader)
              ? authHeader.length > 0
              : !!authHeader

        localStorage.removeItem('auth_token')
        localStorage.removeItem('refresh_token')
        localStorage.removeItem('auth_user')
        localStorage.removeItem('token_expires_at')
        if ((hasToken || sentAuth) && !isAuthEndpoint) {
          sessionStorage.setItem('auth_expired', '1')
        }
        // Only redirect if not already on login page
        if (!window.location.pathname.includes('/login')) {
          window.location.href = '/login'
        }
      }

      return Promise.reject({
        status,
        ...(retryAfter ? { retryAfter } : {}),
        code: apiData.code,
        reason: apiData.reason,
        error: apiData.error,
        message: apiData.message || apiData.detail || error.message,
        metadata: apiData.metadata,
      })
    }

    // Network error
    return Promise.reject({
      status: 0,
      message: 'Network error. Please check your connection.'
    })
  }
)

return apiClient
}

const standaloneClient = createApiClient()
function currentClient(): AxiosInstance {
  const runtime = getAppRuntimeContext()
  if (!runtime) return standaloneClient
  return runtime.apiClient ??= createApiClient(runtime)
}

// Capture the app at invocation, not in Axios's deferred interceptor microtask.
// Each SSR request gets its own instance; no cookie/token can cross requests.
export const apiClient = new Proxy(standaloneClient, {
  get(_target, key) {
    const client = currentClient()
    const value = Reflect.get(client, key, client)
    return typeof value === 'function' ? value.bind(client) : value
  },
  apply(_target, thisArg, args) { return Reflect.apply(currentClient(), thisArg, args) },
})
export default apiClient
