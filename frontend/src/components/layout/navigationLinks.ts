const NON_NAV_PROTOCOL = /^(?:mailto|tel|javascript):/i

/**
 * Returns the in-app path for a plain same-origin anchor, or null when the
 * browser should handle it normally. The document-level fallback exists because
 * a stray <a href> in a third-party menu component turns a client navigation
 * into a full document reload.
 */
export function resolveInternalNavigation(anchor: HTMLAnchorElement, location: Location): string | null {
  if (anchor.target && anchor.target !== '_self') return null
  if (anchor.hasAttribute('download')) return null
  if (anchor.hasAttribute('data-native-navigation')) return null

  const raw = anchor.getAttribute('href')
  if (!raw || raw.startsWith('#') || NON_NAV_PROTOCOL.test(raw)) return null

  let url: URL
  try {
    url = new URL(anchor.href, location.href)
  } catch {
    return null
  }
  if (url.origin !== location.origin) return null
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/_nuxt/') || url.pathname.startsWith('/assets/')) return null
  return `${url.pathname}${url.search}${url.hash}`
}