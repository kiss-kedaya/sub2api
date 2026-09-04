const RELOAD_KEY = 'chunk_reload_attempted'
const RELOAD_COOLDOWN_MS = 10_000

export function isChunkLoadError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const err = error as { message?: string; name?: string }
  const message = err.message ?? ''
  return (
    err.name === 'ChunkLoadError' ||
    message.includes('Failed to fetch dynamically imported module') ||
    message.includes('error loading dynamically imported module') ||
    message.includes('Loading chunk') ||
    message.includes('Loading CSS chunk')
  )
}

export function recoverFromChunkLoadError(targetPath?: string): boolean {
  if (typeof window === 'undefined') return false
  const now = Date.now()
  const lastReload = window.sessionStorage.getItem(RELOAD_KEY)
  if (lastReload && now - Number.parseInt(lastReload, 10) < RELOAD_COOLDOWN_MS) {
    return false
  }
  window.sessionStorage.setItem(RELOAD_KEY, String(now))
  const next = targetPath && targetPath.startsWith('/')
    ? targetPath
    : `${window.location.pathname}${window.location.search}${window.location.hash}`
  window.location.replace(next)
  return true
}

export async function pushOrReload(
  router: { push: (path: string) => Promise<unknown> },
  path: string,
): Promise<void> {
  try {
    await router.push(path)
  } catch (error) {
    if (isChunkLoadError(error)) {
      recoverFromChunkLoadError(path)
      return
    }
    throw error
  }
}
