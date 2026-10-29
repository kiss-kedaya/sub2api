import { describe, expect, it } from 'vitest'
import type { ModelPlazaGroup } from '@/api/modelPlaza'
import { buildModelOffers, compareModels, filterComparisons, sortComparisons } from '../modelComparison'

function group(overrides: Partial<ModelPlazaGroup>): ModelPlazaGroup {
  return {
    id: 1,
    name: 'group',
    description: '',
    platform: 'openai',
    subscription_type: 'standard',
    rate_multiplier: 1,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    long_context_pricing_enabled: false,
    models: [],
    ...overrides,
  }
}

function tokenPricing(input: number, output: number) {
  return {
    billing_mode: 'token' as const,
    input_price: input,
    output_price: output,
    cache_write_price: input / 2,
    cache_read_price: input / 10,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
  }
}

describe('buildModelOffers', () => {
  it('applies the group rate to token prices and converts to per million', () => {
    const offers = buildModelOffers([
      group({
        id: 2,
        name: 'cheap',
        rate_multiplier: 0.5,
        models: [{ name: 'gpt-6-sol', platform: 'openai', pricing: tokenPricing(0.000001, 0.000002), official_pricing: null }],
      }),
    ])
    expect(offers).toHaveLength(1)
    expect(offers[0].inputPerMillion).toBeCloseTo(0.5)
    expect(offers[0].outputPerMillion).toBeCloseTo(1)
    expect(offers[0].blendedPerMillion).toBeCloseTo(0.625)
  })

  it('uses the independent image rate for image billing', () => {
    const offers = buildModelOffers([
      group({
        rate_multiplier: 2,
        image_rate_independent: true,
        image_rate_multiplier: 0.1,
        models: [{
          name: 'gpt-image-2',
          platform: 'openai',
          pricing: {
            ...tokenPricing(0, 0),
            billing_mode: 'image',
            per_request_price: 0.02,
          },
          official_pricing: null,
        }],
      }),
    ])
    expect(offers[0].requestPrice).toBeCloseTo(0.002)
  })
})

describe('compareModels', () => {
  it('finds the cheapest offer and input spread', () => {
    const offers = buildModelOffers([
      group({ id: 1, name: 'a', rate_multiplier: 1, models: [{ name: 'm', platform: 'openai', pricing: tokenPricing(0.000001, 0.000001), official_pricing: null }] }),
      group({ id: 2, name: 'b', rate_multiplier: 2, models: [{ name: 'm', platform: 'openai', pricing: tokenPricing(0.000001, 0.000001), official_pricing: null }] }),
    ])
    const [model] = compareModels(offers)
    expect(model.offers).toHaveLength(2)
    expect(model.cheapestInput?.groupName).toBe('a')
    expect(model.mostExpensiveInput?.groupName).toBe('b')
    expect(model.spread).toBeCloseTo(1)
  })

  it('picks the cheapest healthy group as best stable', () => {
    const offers = buildModelOffers([
      group({ id: 1, name: 'cheap-suspect', rate_multiplier: 1, quality_status: 'suspect', models: [{ name: 'm', platform: 'openai', pricing: tokenPricing(0.000001, 0.000001), official_pricing: null }] }),
      group({ id: 2, name: 'stable', rate_multiplier: 2, quality_status: 'healthy', models: [{ name: 'm', platform: 'openai', pricing: tokenPricing(0.000001, 0.000001), official_pricing: null }] }),
    ])
    const [model] = compareModels(offers)
    expect(model.cheapestInput?.groupName).toBe('cheap-suspect')
    expect(model.bestStable?.groupName).toBe('stable')
  })
})

describe('filter and sort', () => {
  const offers = buildModelOffers([
    group({ id: 1, name: 'a', platform: 'openai', models: [{ name: 'gpt-6-sol', platform: 'openai', pricing: tokenPricing(0.000001, 0.000001), official_pricing: null }] }),
    group({ id: 2, name: 'b', platform: 'openai', models: [{ name: 'gpt-6-sol', platform: 'openai', pricing: tokenPricing(0.000002, 0.000002), official_pricing: null }] }),
    group({ id: 3, name: 'c', platform: 'gemini', models: [{ name: 'gemini-3-pro', platform: 'gemini', pricing: tokenPricing(0.000003, 0.000003), official_pricing: null }] }),
  ])
  const models = compareModels(offers)

  it('filters by platform, group and comparable state', () => {
    expect(filterComparisons(models, { platform: 'openai' })).toHaveLength(1)
    expect(filterComparisons(models, { groupId: 3 })).toHaveLength(1)
    expect(filterComparisons(models, { onlyComparable: true })).toHaveLength(1)
    expect(filterComparisons(models, { search: 'gemini' })).toHaveLength(1)
  })

  it('sorts by spread desc and name', () => {
    expect(sortComparisons(models, 'spread')[0].name).toBe('gpt-6-sol')
    expect(sortComparisons(models, 'name')[0].name).toBe('gemini-3-pro')
  })
})
