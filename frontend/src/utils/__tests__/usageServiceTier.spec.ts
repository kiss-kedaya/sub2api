import { describe, expect, it } from 'vitest'

import { calculateTokenPricePerMillion } from '@/utils/usagePricing'
import {
  formatUsageServiceTier,
  getUsageServiceTierLabel,
  getUsageServiceTierMultiplier,
  getUsageStandardCostParts,
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

  // gpt-6.1-sol 标准价：输入 2 / 输出 10 美元每百万 token；UltraFast 官方是标准价 6 倍（12 / 60），Fast 官方 2 倍（4 / 20）。
  // 线上核对过的一行：1313 输入 + 615 输出 token，分组倍率 0.16，分组把 UltraFast 档位倍率设成 1×（免费 UltraFast）：
  // 标准价 0.008776 × 1 × 0.16 = 0.00140416 = actual_cost；total_cost 和分项存的都是上游 6 倍档位价。
  describe('getUsageStandardCostParts', () => {
    const freeUltrafast = {
      service_tier: 'ultrafast',
      service_tier_multiplier: 1,
      rate_multiplier: 0.16,
      input_cost: 0.015756,
      output_cost: 0.0369,
      cache_creation_cost: 0,
      cache_read_cost: 0,
      total_cost: 0.052656,
      actual_cost: 0.00140416,
    }
    const fastTier = { input_cost: 0.005252, output_cost: 0.0123, cache_creation_cost: 0, cache_read_cost: 0, total_cost: 0.017552 }

    it.each([
      { name: 'free UltraFast (override 1x, group 0.16): the production row', row: freeUltrafast },
      { name: 'UltraFast official 6x, no override, group 0.16', row: { ...freeUltrafast, service_tier_multiplier: 6, actual_cost: 0.00842496 } },
      { name: 'UltraFast official 6x, no override, group 1', row: { ...freeUltrafast, service_tier_multiplier: 6, rate_multiplier: 1, actual_cost: 0.052656 } },
      { name: 'UltraFast override 3x, group 0.16', row: { ...freeUltrafast, service_tier_multiplier: 3, actual_cost: 0.00421248 } },
      { name: 'Fast official 2x, no override, group 1', row: { ...fastTier, service_tier: 'priority', service_tier_multiplier: 2, rate_multiplier: 1, actual_cost: 0.017552 } },
      { name: 'free Fast (override 1x, group 0.16)', row: { ...fastTier, service_tier: 'fast', service_tier_multiplier: 1, rate_multiplier: 0.16, actual_cost: 0.00140416 } },
    ])('$name: input / output come back at the Standard price', ({ row }) => {
      const parts = getUsageStandardCostParts(row)!
      expect(parts.input_cost).toBeCloseTo(0.002626, 12)
      expect(parts.output_cost).toBeCloseTo(0.00615, 12)
      expect(parts.cache_creation_cost).toBe(0)
      expect(parts.cache_read_cost).toBe(0)
      // 单价 = 缩放后的分项 ÷ 同一份 token 数：标准价 2 / 10 美元每百万 token
      expect(calculateTokenPricePerMillion(parts.input_cost, 1313)).toBeCloseTo(2, 9)
      expect(calculateTokenPricePerMillion(parts.output_cost, 615)).toBeCloseTo(10, 9)
      // 分项之和 = 标准价，标准价 × 综合倍率 = 用户扣费
      const pricing = getUsageTierPricing(row)!
      const sum = parts.input_cost + parts.output_cost + parts.cache_creation_cost + parts.cache_read_cost
      expect(sum).toBeCloseTo(0.008776, 12)
      expect(sum).toBeCloseTo(pricing.standardPrice, 12)
      expect(sum * pricing.combinedRate).toBeCloseTo(row.actual_cost, 12)
    })

    it('scales the cache creation and cache read components with the same ratio', () => {
      // Fast 官方 2x、分组倍率 0.5。标准价：输入 1000 × 2e-6、输出 200 × 1e-5、缓存写 500 × 2.5e-6、缓存读 8000 × 1e-7
      const row = {
        service_tier: 'priority',
        service_tier_multiplier: 2,
        rate_multiplier: 0.5,
        input_cost: 0.004,
        output_cost: 0.004,
        cache_creation_cost: 0.0025,
        cache_read_cost: 0.0016,
        total_cost: 0.0121,
        actual_cost: 0.00605,
      }
      const parts = getUsageStandardCostParts(row)!
      expect(parts.input_cost).toBeCloseTo(0.002, 12)
      expect(parts.output_cost).toBeCloseTo(0.002, 12)
      expect(parts.cache_creation_cost).toBeCloseTo(0.00125, 12)
      expect(parts.cache_read_cost).toBeCloseTo(0.0008, 12)
    })

    it('treats missing component fields as 0', () => {
      const { cache_creation_cost: _a, cache_read_cost: _b, ...row } = freeUltrafast
      expect(getUsageStandardCostParts(row)).toMatchObject({ cache_creation_cost: 0, cache_read_cost: 0 })
    })

    it('does not mutate the row', () => {
      const row = { ...freeUltrafast }
      getUsageStandardCostParts(row)
      expect(row).toEqual(freeUltrafast)
    })

    it('leaves a tier row that already sits at the Standard price as it is (ratio 1)', () => {
      const row = { ...freeUltrafast, service_tier_multiplier: 1, rate_multiplier: 1, input_cost: 0.002626, output_cost: 0.00615, total_cost: 0.008776, actual_cost: 0.008776 }
      const parts = getUsageStandardCostParts(row)!
      expect(parts.input_cost).toBeCloseTo(0.002626, 12)
      expect(parts.output_cost).toBeCloseTo(0.00615, 12)
    })

    // 金额列是 DECIMAL(20,10)，分项之和和 total_cost 可以差几个 1e-10 的舍入误差，但不能多
    it('tolerates database rounding noise in total_cost, not a real extra charge', () => {
      expect(getUsageStandardCostParts({ ...freeUltrafast, total_cost: 0.052656 + 4e-10 })).not.toBeNull()
      expect(getUsageStandardCostParts({ ...freeUltrafast, total_cost: 0.052656 - 4e-10 })).not.toBeNull()
      expect(getUsageStandardCostParts({ ...freeUltrafast, total_cost: 0.052656 + 2e-9 })).toBeNull()
    })

    it.each([
      { name: 'total_cost is 0', patch: { total_cost: 0 } },
      { name: 'total_cost is negative', patch: { total_cost: -0.052656 } },
      // 全是负数时 标准价 ÷ total_cost 反而是正数，只有 total_cost > 0 这条守卫能拦住
      { name: 'a refund-like row where every amount is negative', patch: { total_cost: -0.052656, input_cost: -0.015756, output_cost: -0.0369, actual_cost: -0.00140416 } },
      { name: 'total_cost is missing', patch: { total_cost: undefined } },
      { name: 'total_cost is null', patch: { total_cost: null } },
      { name: 'total_cost is NaN', patch: { total_cost: NaN } },
      { name: 'nothing was charged (ratio 0)', patch: { actual_cost: 0 } },
      { name: 'ratio overflows to Infinity', patch: { total_cost: Number.MIN_VALUE, input_cost: Number.MIN_VALUE, output_cost: 0 } },
      { name: 'a per-call add-on sits on top of the token components', patch: { total_cost: 0.062656 } },
      { name: 'the components add up to more than total_cost', patch: { total_cost: 0.04 } },
      { name: 'a fixed-price call without token components', patch: { input_cost: 0, output_cost: 0 } },
      { name: 'image input cost (not provably tier-scaled)', patch: { image_input_cost: 0.001 } },
      { name: 'image output cost (not provably tier-scaled)', patch: { image_output_cost: 0.001 } },
      // 真实的图片行：图片分项算在 total_cost 里（文本输入 + 图片输入 + 输出 = total），文本分项之和对不上
      { name: 'image input priced separately inside total_cost', patch: { image_input_cost: 0.0012, input_cost: 0.014556 } },
      { name: 'tier multiplier not recorded (old row)', patch: { service_tier_multiplier: null } },
      { name: 'tier multiplier 0', patch: { service_tier_multiplier: 0 } },
      { name: 'group rate missing', patch: { rate_multiplier: undefined } },
      { name: 'standard tier', patch: { service_tier: 'standard' } },
      { name: 'flex tier', patch: { service_tier: 'flex' } },
      { name: 'no tier', patch: { service_tier: null } },
    ])('returns null so the caller keeps the stored amounts: $name', ({ patch }) => {
      expect(getUsageStandardCostParts({ ...freeUltrafast, ...patch })).toBeNull()
    })
  })
})
