/**
 * Shared URL builder for iframe-embedded pages.
 * Used by PurchaseSubscriptionView and CustomPageView to build consistent URLs
 * with user_id, token, theme, lang, ui_mode, src_host, and src parameters.
 */

const EMBEDDED_USER_ID_QUERY_KEY = 'user_id'
const EMBEDDED_AUTH_TOKEN_QUERY_KEY = 'token'
const EMBEDDED_THEME_QUERY_KEY = 'theme'
const EMBEDDED_LANG_QUERY_KEY = 'lang'
const EMBEDDED_UI_MODE_QUERY_KEY = 'ui_mode'
const EMBEDDED_UI_MODE_VALUE = 'embedded'
const EMBEDDED_SRC_HOST_QUERY_KEY = 'src_host'
const EMBEDDED_SRC_QUERY_KEY = 'src_url'

/**
 * 只有这些来源才允许把登录凭据带进 URL：
 *   - 同源（管理者自己的页面）
 *   - 显式配置的可信嵌入域（VITE_EMBEDDED_TRUSTED_ORIGINS，逗号分隔）
 *
 * 第三方域（例如外部充值中心）只允许拿到 user_id 之类的非凭据标识。
 * 把 access JWT 交给第三方等于把 24 小时会话交给对方保管：它会进
 * 对方访问日志 / CDN 日志 / 浏览器历史，无法撤回。
 */
function trustedCredentialOrigins(): string[] {
  const raw = (import.meta.env.VITE_EMBEDDED_TRUSTED_ORIGINS as string | undefined) ?? ''
  return raw
    .split(',')
    .map(entry => entry.trim())
    .filter(Boolean)
}

function isTrustedCredentialOrigin(target: URL): boolean {
  if (typeof window === 'undefined') return false
  if (target.origin === window.location.origin) return true
  return trustedCredentialOrigins().includes(target.origin)
}

export function buildEmbeddedUrl(
  baseUrl: string,
  userId?: number,
  authToken?: string | null,
  theme: 'light' | 'dark' = 'light',
  lang?: string,
): string {
  if (!baseUrl) return baseUrl
  try {
    const url = new URL(baseUrl)
    if (userId) {
      url.searchParams.set(EMBEDDED_USER_ID_QUERY_KEY, String(userId))
    }
    // 凭据只发往可信来源；第三方域永远不带 token。
    if (authToken && isTrustedCredentialOrigin(url)) {
      url.searchParams.set(EMBEDDED_AUTH_TOKEN_QUERY_KEY, authToken)
    } else {
      url.searchParams.delete(EMBEDDED_AUTH_TOKEN_QUERY_KEY)
    }
    url.searchParams.set(EMBEDDED_THEME_QUERY_KEY, theme)
    if (lang) {
      url.searchParams.set(EMBEDDED_LANG_QUERY_KEY, lang)
    }
    url.searchParams.set(EMBEDDED_UI_MODE_QUERY_KEY, EMBEDDED_UI_MODE_VALUE)
    // Source tracking: let the embedded page know where it's being loaded from
    if (typeof window !== 'undefined') {
      url.searchParams.set(EMBEDDED_SRC_HOST_QUERY_KEY, window.location.origin)
      url.searchParams.set(EMBEDDED_SRC_QUERY_KEY, window.location.href)
    }
    return url.toString()
  } catch {
    return baseUrl
  }
}

export function detectTheme(): 'light' | 'dark' {
  if (typeof document === 'undefined') return 'light'
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}
