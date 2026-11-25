/**
 * 格式化缓存 token 数量（1K/1M 缩写）
 */
export function formatCacheTokens(tokens: number): string {
  if (tokens >= 1000000) return `${(tokens / 1000000).toFixed(1)}M`
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(1)}K`
  return tokens.toLocaleString()
}

/**
 * 自适应精度格式化倍率：保留至多 4 位小数并去掉末尾多余的 0，
 * 但至少保留 2 位小数（0.035 -> "0.035"，0.3 -> "0.30"，1 -> "1.00"）
 */
export function formatMultiplier(val: number): string {
  if (val < 0.0001) return val.toPrecision(2)
  return val.toFixed(4).replace(/(\.\d{2}\d*?)0+$/, '$1')
}

/**
 * 倍率的紧凑写法：至多 4 位小数、去掉多余的 0、不补零（1 -> "1"，0.145 -> "0.145"，2.5 -> "2.5"），
 * 调用方自己拼「×」。0.1 × 3 这类浮点噪声（0.30000000000000004）也会被抹掉。
 */
export function formatMultiplierCompact(val: number): string {
  const rounded = Number(val.toFixed(4))
  // 极小的正数四舍五入会变成 0，显示 0 会误导，改用 2 位有效数字
  if (rounded === 0 && val !== 0) return String(Number(val.toPrecision(2)))
  return String(rounded)
}
