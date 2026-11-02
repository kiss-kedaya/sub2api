/** Reads OAuth fragment parameters once, then immediately removes secrets from the URL. */
export function consumeOAuthFragment(): URLSearchParams {
  if (typeof window === 'undefined') return new URLSearchParams()
  const raw = window.location.hash
  const params = new URLSearchParams(raw.startsWith('#') ? raw.slice(1) : raw)
  if (raw) {
    const current = new URL(window.location.href)
    const pathname = window.location.pathname || current.pathname
    const search = window.location.search || current.search
    window.history.replaceState(window.history.state, '', `${pathname}${search}`)
  }
  return params
}
