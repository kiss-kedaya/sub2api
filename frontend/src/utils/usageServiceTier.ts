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
