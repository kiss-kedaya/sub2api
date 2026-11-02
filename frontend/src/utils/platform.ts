export type PlatformKind = 'ios' | 'android' | 'macos' | 'windows' | 'linux' | 'unknown'

export function detectPlatform(userAgent = typeof navigator !== 'undefined' ? navigator.userAgent : ''): PlatformKind {
  const value = userAgent.toLowerCase()
  if (/ipad|iphone|ipod/.test(value) || (value.includes('macintosh') && typeof navigator !== 'undefined' && navigator.maxTouchPoints > 1)) return 'ios'
  if (/android/.test(value)) return 'android'
  if (/macintosh|mac os x/.test(value)) return 'macos'
  if (/windows/.test(value)) return 'windows'
  if (/linux/.test(value)) return 'linux'
  return 'unknown'
}

export function applyPlatformProfile(): PlatformKind {
  const platform = detectPlatform()
  if (typeof document !== 'undefined') document.documentElement.dataset.platform = platform
  return platform
}
