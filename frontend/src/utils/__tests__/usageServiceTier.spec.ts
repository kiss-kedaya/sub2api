import { describe, expect, it } from 'vitest'

import {
  formatUsageServiceTier,
  getUsageServiceTierLabel,
  getUsageServiceTierMultiplier,
  getUsageTierPricing,
  normalizeUsageServiceTier,
} from '@/utils/usageServiceTier'

describe('usageServiceTier utils', () => {
  it('normalizes fast/default aliases', () => {
    expect(normalizeUsageServiceTier('fast')).toBe('priority')
    expect(normalizeUsageServiceTier(' default ')).toBe('standard')
    expect(normalizeUsageServiceTier('STANDARD')).toBe('standard')
  })

  it('preserves supported tiers', () => {
    expect(normalizeUsageServiceTier('priority')).toBe('priority')
    expect(normalizeUsageServiceTier('flex')).toBe('flex')
    expect(normalizeUsageServiceTier('ultrafast')).toBe('ultrafast')
  })

  it('formats empty values as standard', () => {
    expect(formatUsageServiceTier()).toBe('standard')
    expect(formatUsageServiceTier('')).toBe('standard')
  })

  it('passes through unknown non-empty tiers for display fallback', () => {
    expect(normalizeUsageServiceTier('custom-tier')).toBe('custom-tier')
    expect(formatUsageServiceTier('custom-tier')).toBe('custom-tier')
  })

  it('maps tiers to translated labels', () => {
    const translate = (key: string) => ({
      'usage.serviceTierPriority': 'Fast',
      'usage.serviceTierUltrafast': 'Ultrafast',
      'usage.serviceTierFlex': 'Flex',
      'usage.serviceTierStandard': 'Standard',
    })[key] ?? key

    expect(getUsageServiceTierLabel('fast', translate)).toBe('Fast')
    expect(getUsageServiceTierLabel('ultrafast', translate)).toBe('Ultrafast')
    expect(getUsageServiceTierLabel('flex', translate)).toBe('Flex')
    expect(getUsageServiceTierLabel(undefined, translate)).toBe('Standard')
    expect(getUsageServiceTierLabel('custom-tier', translate)).toBe('custom-tier')
  })

  describe('getUsageServiceTierMultiplier', () => {
    it.each(['priority', 'fast', 'FAST', ' ultrafast '])('returns the recorded multiplier on a %s row', (tier) => {
      expect(getUsageServiceTierMultiplier({ service_tier: tier, service_tier_multiplier: 2.5 })).toBe(2.5)
      expect(getUsageServiceTierMultiplier({ service_tier: tier, service_tier_multiplier: 1 })).toBe(1)
    })

    it.each([undefined, null, 'standard', 'default', 'flex', 'custom-tier', ''])(
      'ignores the multiplier on a %j row (not a Fast / UltraFast request)',
      (tier) => {
        expect(getUsageServiceTierMultiplier({ service_tier: tier, service_tier_multiplier: 2 })).toBeNull()
      },
    )

    it.each([undefined, null, 0, -1, NaN, Infinity])('returns null when the multiplier is %j (old rows have no record)', (multiplier) => {
      expect(getUsageServiceTierMultiplier({ service_tier: 'priority', service_tier_multiplier: multiplier })).toBeNull()
    })
  })

  // 后端口径：total_cost = 上游按档位实际收的价（已含官方档位比值）；
  // actual_cost（用户扣费）= 标准价 × 档位倍率 × 分组倍率。标准价 0.01136，分组倍率 0.16。
  describe('getUsageTierPricing', () => {
    it('Fast without an override: official 2x is already inside total_cost, standard price comes from actual_cost', () => {
      const pricing = getUsageTierPricing({
        service_tier: 'priority', service_tier_multiplier: 2, rate_multiplier: 0.16, actual_cost: 0.0036352,
      })
      expect(pricing).toMatchObject({ tierMultiplier: 2, groupRate: 0.16, combinedRate: 0.32 })
      expect(pricing!.standardPrice).toBeCloseTo(0.01136, 12)
      // 标准价 × 综合倍率 = 用户扣费 恒成立
      expect(pricing!.standardPrice * pricing!.combinedRate).toBeCloseTo(0.0036352, 12)
    })

    it('free Fast (override 1x): the user pays the standard price x group rate', () => {
      const pricing = getUsageTierPricing({
        service_tier: 'fast', service_tier_multiplier: 1, rate_multiplier: 0.16, actual_cost: 0.0018176,
      })
      expect(pricing).toMatchObject({ tierMultiplier: 1, groupRate: 0.16, combinedRate: 0.16 })
      expect(pricing!.standardPrice).toBeCloseTo(0.01136, 12)
    })

    it('UltraFast with an override', () => {
      const pricing = getUsageTierPricing({
        service_tier: 'ultrafast', service_tier_multiplier: 3, rate_multiplier: 0.16, actual_cost: 0.0054528,
      })
      expect(pricing).toMatchObject({ tierMultiplier: 3, groupRate: 0.16, combinedRate: 0.48 })
      expect(pricing!.standardPrice).toBeCloseTo(0.01136, 12)
    })

    it('returns null for old rows, other tiers, and a missing tier multiplier', () => {
      const base = { rate_multiplier: 0.16, actual_cost: 0.0036352 }
      expect(getUsageTierPricing({ ...base, service_tier: 'priority' })).toBeNull()
      expect(getUsageTierPricing({ ...base, service_tier: 'priority', service_tier_multiplier: null })).toBeNull()
      expect(getUsageTierPricing({ ...base, service_tier: 'flex', service_tier_multiplier: 2 })).toBeNull()
      expect(getUsageTierPricing({ ...base, service_tier: null, service_tier_multiplier: 2 })).toBeNull()
    })

    it.each([
      { name: 'group rate 0', rate_multiplier: 0, service_tier_multiplier: 2 },
      { name: 'negative group rate', rate_multiplier: -1, service_tier_multiplier: 2 },
      { name: 'missing group rate', rate_multiplier: undefined, service_tier_multiplier: 2 },
      { name: 'NaN group rate', rate_multiplier: NaN, service_tier_multiplier: 2 },
      { name: 'tier multiplier 0', rate_multiplier: 0.16, service_tier_multiplier: 0 },
      { name: 'product underflows to 0', rate_multiplier: 1e-200, service_tier_multiplier: 1e-200 },
      { name: 'quotient overflows', rate_multiplier: 1e-300, service_tier_multiplier: 1e-10 },
    ])('returns null instead of dividing by zero: $name', ({ rate_multiplier, service_tier_multiplier }) => {
      expect(getUsageTierPricing({
        service_tier: 'priority', service_tier_multiplier, rate_multiplier, actual_cost: 0.5,
      })).toBeNull()
    })

    it('shows a missing charge as a zero standard price, like the other amounts in the bubble', () => {
      for (const actual_cost of [undefined, null, NaN]) {
        expect(getUsageTierPricing({
          service_tier: 'priority', service_tier_multiplier: 2, rate_multiplier: 0.16, actual_cost,
        })).toMatchObject({ standardPrice: 0 })
      }
    })
  })
})
