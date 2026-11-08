import { BILLING_MODE_IMAGE, BILLING_MODE_VIDEO } from './billingMode'
import type { UsageLog } from '@/types'

/**
 * 请求延迟健康度分档（用于用量明细"延迟"列的纵向健康扫视）。
 *
 * 首 Token（TTFT）：10s 内正常，10-30s 偏慢，30-60s 缓慢，60s 及以上严重。
 * 总耗时：流式请求整体时长天然更长，阈值放宽为 1min / 3min / 5min。
 */
export type LatencySeverity = 'good' | 'warn' | 'slow' | 'critical'

export const FIRST_TOKEN_THRESHOLDS_MS = {
  warn: 10_000,
  slow: 30_000,
  critical: 60_000,
} as const

export const DURATION_THRESHOLDS_MS = {
  warn: 60_000,
  slow: 180_000,
  critical: 300_000,
} as const

interface Thresholds {
  warn: number
  slow: number
  critical: number
}

const classify = (ms: number, thresholds: Thresholds): LatencySeverity => {
  if (ms >= thresholds.critical) return 'critical'
  if (ms >= thresholds.slow) return 'slow'
  if (ms >= thresholds.warn) return 'warn'
  return 'good'
}

export const firstTokenSeverity = (ms: number): LatencySeverity =>
  classify(ms, FIRST_TOKEN_THRESHOLDS_MS)

export const durationSeverity = (ms: number): LatencySeverity =>
  classify(ms, DURATION_THRESHOLDS_MS)

export const LATENCY_TEXT_CLASSES: Record<LatencySeverity, string> = {
  good: 'text-emerald-600 dark:text-emerald-400',
  warn: 'text-amber-600 dark:text-amber-400',
  slow: 'text-orange-600 dark:text-orange-400',
  critical: 'text-red-600 dark:text-red-400',
}

/** 无首字数据时的纯色色条（仅按总耗时档着色）。 */
export const LATENCY_BAR_CLASSES: Record<LatencySeverity, string> = {
  good: 'bg-emerald-500',
  warn: 'bg-amber-400',
  slow: 'bg-orange-500',
  critical: 'bg-red-500',
}

/** 渐变色条上端（首字档）；与 LATENCY_BAR_TO_CLASSES 组合成上下渐变，避免两段硬切割裂感。 */
export const LATENCY_BAR_FROM_CLASSES: Record<LatencySeverity, string> = {
  good: 'from-emerald-500',
  warn: 'from-amber-400',
  slow: 'from-orange-500',
  critical: 'from-red-500',
}

/** 渐变色条下端（总耗时档）。 */
export const LATENCY_BAR_TO_CLASSES: Record<LatencySeverity, string> = {
  good: 'to-emerald-500',
  warn: 'to-amber-400',
  slow: 'to-orange-500',
  critical: 'to-red-500',
}

/** 有文本输出可言的请求类型；live 等实时/媒体请求不计算输出速率。 */
const TEXT_RATE_REQUEST_TYPES = new Set(['sync', 'stream', 'ws_v2', 'cyber', 'unknown'])

export type OutputRateRow = Pick<
  UsageLog,
  'output_tokens' | 'duration_ms' | 'image_count' | 'image_output_tokens' | 'billing_mode' | 'request_type'
>

/**
 * 单次请求的输出速率：输出 Token ÷ 记录的总耗时，单位 t/s。
 *
 * 口径与运维「平均 Tokens/秒」统计一致：
 * - 使用完整耗时，包含首字等待，不按首字时间切出"生成窗口"；
 * - 输出 Token 可能包含推理 Token，因此这是平均吞吐而不是模型实际生成速度；
 * - 图片 / 视频等媒体请求没有文本输出速率，直接不显示。
 */
export const formatUsageOutputRate = (row: OutputRateRow): string => {
  if (
    row.image_count > 0
    || row.image_output_tokens > 0
    || row.billing_mode === BILLING_MODE_IMAGE
    || row.billing_mode === BILLING_MODE_VIDEO
    || (row.request_type != null && !TEXT_RATE_REQUEST_TYPES.has(row.request_type))
  ) return '-'
  const tokens = row.output_tokens
  const duration = row.duration_ms
  if (!Number.isFinite(tokens) || tokens <= 0) return '-'
  if (duration == null || !Number.isFinite(duration) || duration <= 0) return '-'
  return `${(tokens * 1000 / duration).toFixed(1)} t/s`
}
