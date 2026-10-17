// 与后端 ValidateUsername 同口径：各国文字、数字、空格与 _-. ，最长 50 个字符。
export const USERNAME_MAX_RUNES = 50

const USERNAME_ALLOWED_RE = /^[\p{L}\p{N} _.-]+$/u

export function isUsernameValid(raw: string): boolean {
  const value = String(raw || '').trim()
  if (!value) {
    // 空值表示未设置/清空，由提交方决定是否允许。
    return true
  }
  return Array.from(value).length <= USERNAME_MAX_RUNES && USERNAME_ALLOWED_RE.test(value)
}
