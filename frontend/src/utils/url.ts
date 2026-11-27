/**
 * 验证并规范化 URL
 * 默认只接受绝对 URL（以 http:// 或 https:// 开头），可按需允许相对路径
 * @param value 用户输入的 URL
 * @returns 规范化后的 URL，如果无效则返回空字符串
 */
type SanitizeOptions = {
  allowRelative?: boolean
  allowDataUrl?: boolean
}

function hasUnsafeControlOrBackslash(value: string): boolean {
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i)
    if (code < 0x20 || code === 0x7f || code === 0x5c) {
      return true
    }
  }
  return false
}

function extractRedirectPathCandidate(path: unknown): string {
  if (typeof path === 'string') {
    return path.trim()
  }
  if (Array.isArray(path)) {
    for (const item of path) {
      if (typeof item === 'string') {
        const trimmed = item.trim()
        if (trimmed) {
          return trimmed
        }
      }
    }
  }
  return ''
}

/**
 * 规范化站内相对跳转路径，拦截协议相对路径、反斜杠、控制字符（含 \t/\r/\n）与跨源解析绕过。
 * 兼容 Vue Router 重复 query 参数产生的数组与非字符串输入。
 */
export function sanitizeInternalRedirectPath(
  path: unknown,
  fallback = '/dashboard',
): string {
  const value = extractRedirectPathCandidate(path)
  if (!value) {
    return fallback
  }
  if (!value.startsWith('/') || value.startsWith('//') || value.includes('://') || hasUnsafeControlOrBackslash(value)) {
    return fallback
  }
  if (typeof window !== 'undefined' && window.location?.origin) {
    try {
      const resolved = new URL(value, window.location.origin)
      if (resolved.origin !== window.location.origin) {
        return fallback
      }
    } catch {
      return fallback
    }
  }
  return value
}

export function sanitizeUrl(value: string, options: SanitizeOptions = {}): string {
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }

  if (options.allowRelative && trimmed.startsWith('/') && !trimmed.startsWith('//') && !hasUnsafeControlOrBackslash(trimmed)) {
    return trimmed
  }

  // 允许 data:image/ 开头的 data URL（仅限图片类型）
  if (options.allowDataUrl && trimmed.startsWith('data:image/')) {
    return trimmed
  }

  // 只接受绝对 URL，不使用 base URL 来避免相对路径被解析为当前域名
  // 检查是否以 http:// 或 https:// 开头
  if (!trimmed.match(/^https?:\/\//i)) {
    return ''
  }

  try {
    const parsed = new URL(trimmed)
    const protocol = parsed.protocol.toLowerCase()
    if (protocol !== 'http:' && protocol !== 'https:') {
      return ''
    }
    return parsed.toString()
  } catch {
    return ''
  }
}
