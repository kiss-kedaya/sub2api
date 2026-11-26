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

/** 费用明细首块里按 token 计费的四个分项金额（USD）。 */
export interface UsageCostParts {
  input_cost: number
  output_cost: number
  cache_creation_cost: number
  cache_read_cost: number
}

// usage_logs 的金额列是 DECIMAL(20,10)：每项最多差 5e-11，分项之和和 total_cost 的舍入误差远小于 1e-9；
// web search 这类按次加价远大于舍入误差，不会被当成误差放过。
const COST_SUM_TOLERANCE = 1e-9

const finiteOrZero = (value: number | null | undefined): number =>
  typeof value === 'number' && Number.isFinite(value) ? value : 0

/**
 * Fast / UltraFast 行的分项金额按标准价口径还原。
 *
 * 后端存的 input_cost / output_cost / cache_*_cost 和 total_cost 一样，是上游按档位实际收的价
 * （例如 UltraFast 官方档位价是标准价的 6 倍），用户实付 = 标准价 × 档位倍率 × 分组倍率。
 * 「免费 UltraFast」（档位倍率 1×）的行如果直接显示这些分项，输入 / 输出单价会是标准价的 6 倍，
 * 和下面的 1× 档位倍率、用户扣费对不上。所以按 标准价 ÷ total_cost 等比缩放：
 * 缩放后分项之和 = 标准价，标准价 × 综合倍率 = 用户扣费。
 *
 * 返回 null = 不缩放（调用方照旧显示后端存的金额）：
 * - 不是带档位倍率的 Fast / UltraFast 行（getUsageTierPricing 为 null）；
 * - total_cost 不是正数，或 标准价 ÷ total_cost 不是正的有限数（例如扣费为 0）；
 * - 有图片分项：Fast 档的图片价有显式配置时不随档位缩放，从行里证明不了就整块不动；
 * - 分项之和对不上 total_cost：说明里面还有按次 / 工具 / 搜索之类的固定加价，不是纯 token 费用。
 *
 * 已知限制：标准价是从 actual_cost（落库只保留 8 位小数）反推的，几百 token 以内的小请求，单价最后一两位
 * 可能有量化误差；分项之和仍然等于气泡里的标准价。Fast 档如果模型只配了部分 priority 价（各分项档位比例不一致），
 * 单个分项也只是按总比例折算，合计仍是标准价。
 */
export function getUsageStandardCostParts(row: {
  service_tier?: string | null
  service_tier_multiplier?: number | null
  rate_multiplier?: number | null
  actual_cost?: number | null
  total_cost?: number | null
  input_cost?: number | null
  output_cost?: number | null
  cache_creation_cost?: number | null
  cache_read_cost?: number | null
  image_input_cost?: number | null
  image_output_cost?: number | null
}): UsageCostParts | null {
  const pricing = getUsageTierPricing(row)
  if (!pricing) return null
  const total = row.total_cost
  if (typeof total !== 'number' || !Number.isFinite(total) || total <= 0) return null
  const ratio = pricing.standardPrice / total
  if (!Number.isFinite(ratio) || ratio <= 0) return null
  if (finiteOrZero(row.image_input_cost) > 0 || finiteOrZero(row.image_output_cost) > 0) return null
  const parts: UsageCostParts = {
    input_cost: finiteOrZero(row.input_cost),
    output_cost: finiteOrZero(row.output_cost),
    cache_creation_cost: finiteOrZero(row.cache_creation_cost),
    cache_read_cost: finiteOrZero(row.cache_read_cost),
  }
  const sum = parts.input_cost + parts.output_cost + parts.cache_creation_cost + parts.cache_read_cost
  if (Math.abs(sum - total) > COST_SUM_TOLERANCE) return null
  return {
    input_cost: parts.input_cost * ratio,
    output_cost: parts.output_cost * ratio,
    cache_creation_cost: parts.cache_creation_cost * ratio,
    cache_read_cost: parts.cache_read_cost * ratio,
  }
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
