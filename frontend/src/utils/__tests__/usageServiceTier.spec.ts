import { describe, expect, it } from 'vitest'

import {
  formatUsageServiceTier,
  getUsageServiceTierLabel,
  getUsageServiceTierMultiplier,
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
})
