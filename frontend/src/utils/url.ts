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

export function sanitizeUrl(value: string, options: SanitizeOptions = {}): string {
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }

  if (options.allowRelative && trimmed.startsWith('/') && !trimmed.startsWith('//')) {
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

const hasControlChar = (value: string): boolean =>
  Array.from(value).some((ch) => {
    const code = ch.charCodeAt(0)
    return code <= 0x1f || code === 0x7f
  })

/**
 * 校验并规范化站内重定向路径，防止开放重定向（protocol-relative、外部 URL、控制字符注入）。
 */
export function sanitizeRedirectPath(
  path: unknown,
  fallback = '/dashboard',
): string {
  const raw = Array.isArray(path) ? path[0] : path
  if (typeof raw !== 'string') return fallback
  if (hasControlChar(raw)) return fallback
  const trimmed = raw.trim()
  if (!trimmed) return fallback
  if (!trimmed.startsWith('/') || trimmed.startsWith('//') || trimmed.startsWith('/\\')) {
    return fallback
  }
  if (trimmed.includes('://')) {
    return fallback
  }
  try {
    const parsed = new URL(trimmed, 'http://localhost')
    if (parsed.origin !== 'http://localhost') {
      return fallback
    }
    return trimmed
  } catch {
    return fallback
  }
}
