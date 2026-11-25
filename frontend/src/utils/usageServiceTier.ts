export function normalizeUsageServiceTier(serviceTier?: string | null): string | null {
  const value = serviceTier?.trim().toLowerCase()
  if (!value) return null
  if (value === 'fast') return 'priority'
  if (value === 'default' || value === 'standard') return 'standard'
  if (value === 'priority' || value === 'flex' || value === 'ultrafast') return value
  return value
}

export function formatUsageServiceTier(serviceTier?: string | null): string {
  const normalized = normalizeUsageServiceTier(serviceTier)
  if (!normalized) return 'standard'
  return normalized
}

/**
 * 该行实际叠加的服务档位倍率（叠在分组倍率上）。只有 Fast / UltraFast 且后端有记录时才有值；
 * Standard / Flex、没有记录的老数据、不是正数的值都返回 null，调用方据此决定要不要多显示一行。
 */
export function getUsageServiceTierMultiplier(row: {
  service_tier?: string | null
  service_tier_multiplier?: number | null
}): number | null {
  const tier = normalizeUsageServiceTier(row.service_tier)
  if (tier !== 'priority' && tier !== 'ultrafast') return null
  const value = row.service_tier_multiplier
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null
}

export interface UsageTierPricing {
  /** 该请求实际叠加的档位倍率（有分组覆盖时是覆盖值，没有覆盖时是官方档位比值） */
  tierMultiplier: number
  /** 分组倍率（不含档位倍率） */
  groupRate: number
  /** 综合倍率 = 分组倍率 × 档位倍率 */
  combinedRate: number
  /** 标准价 = 用户扣费 / 综合倍率，所以「标准价 × 综合倍率 = 用户扣费」恒成立 */
  standardPrice: number
}

/**
 * Fast / UltraFast 且后端记录了档位倍率的行：把费用明细里「标准价 × 综合倍率 = 用户扣费」要用的数一次算好。
 *
 * 后端口径：rate_multiplier 是分组倍率（不含档位倍率）；total_cost 是上游按档位实际收的价（已含官方档位比值，
 * Fast 就是标准价 × 2）；actual_cost（用户扣费）= 标准价 × 档位倍率 × 分组倍率。
 * 所以不能把 total_cost 当「原始」去乘综合倍率：无覆盖行 total 已含官方比值会重复乘，免费 Fast 这类覆盖行实付更低。
 *
 * 返回 null = 老数据 / 不是 Fast·UltraFast / 分组倍率或乘积不是正数（反推会除零）：调用方整行保持老样子。
 */
export function getUsageTierPricing(row: {
  service_tier?: string | null
  service_tier_multiplier?: number | null
  rate_multiplier?: number | null
  actual_cost?: number | null
}): UsageTierPricing | null {
  const tierMultiplier = getUsageServiceTierMultiplier(row)
  if (tierMultiplier === null) return null
  const groupRate = row.rate_multiplier
  if (typeof groupRate !== 'number' || !Number.isFinite(groupRate) || groupRate <= 0) return null
  const combinedRate = groupRate * tierMultiplier
  if (!Number.isFinite(combinedRate) || combinedRate <= 0) return null
  // 缺失的扣费和气泡里其它金额一样按 0 显示
  const actual = typeof row.actual_cost === 'number' && Number.isFinite(row.actual_cost) ? row.actual_cost : 0
  const standardPrice = actual / combinedRate
  if (!Number.isFinite(standardPrice)) return null
  return { tierMultiplier, groupRate, combinedRate, standardPrice }
}

export function getUsageServiceTierLabel(
  serviceTier: string | null | undefined,
  translate: (key: string) => string,
): string {
  const tier = formatUsageServiceTier(serviceTier)
  if (tier === 'priority') return translate('usage.serviceTierPriority')
  if (tier === 'ultrafast') return translate('usage.serviceTierUltrafast')
  if (tier === 'flex') return translate('usage.serviceTierFlex')
  if (tier === 'standard') return translate('usage.serviceTierStandard')
  return tier
}
