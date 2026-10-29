import { BILLING_MODE_IMAGE, BILLING_MODE_TOKEN } from '@/constants/channel'
import type { ModelPlazaGroup } from '@/api/modelPlaza'

export interface ModelOffer {
  key: string
  modelKey: string
  model: string
  platform: string
  groupId: number
  groupName: string
  groupRate: number
  subscriptionType: string
  isExclusive: boolean
  qualityStatus?: 'healthy' | 'suspect'
  billingMode: string
  inputPerMillion: number | null
  outputPerMillion: number | null
  cacheWritePerMillion: number | null
  cacheReadPerMillion: number | null
  requestPrice: number | null
  blendedPerMillion: number | null
  multiTier: boolean
}

export interface ModelComparison {
  key: string
  name: string
  platform: string
  offers: ModelOffer[]
  cheapestInput: ModelOffer | null
  cheapestOutput: ModelOffer | null
  cheapestBlended: ModelOffer | null
  bestStable: ModelOffer | null
  mostExpensiveInput: ModelOffer | null
  cheapestRequest: ModelOffer | null
  mostExpensiveRequest: ModelOffer | null
  minInput: number | null
  maxInput: number | null
  spread: number | null
  requestSpread: number | null
  hasToken: boolean
  hasRequest: boolean
  multiTier: boolean
}

export interface ComparisonFilters {
  search?: string
  platform?: string
  groupId?: number | null
  billingMode?: string
  onlyComparable?: boolean
}

export type ComparisonSort = 'spread' | 'input' | 'output' | 'name'

function effectiveRate(group: ModelPlazaGroup): number {
  const rate = group.user_rate_multiplier ?? group.rate_multiplier
  return Number.isFinite(rate) && rate > 0 ? rate : 1
}

function scaled(value: number | null | undefined, rate: number, perMillion: boolean): number | null {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null
  return value * rate * (perMillion ? 1_000_000 : 1)
}

function blended(input: number | null, output: number | null): number | null {
  if (input != null && output != null) return input * 0.75 + output * 0.25
  return input ?? output
}

/**
 * 把模型广场的分组数据摊平成「分组 × 模型」报价。
 * 实付价 = 渠道/目录单价 × 分组生效倍率；图片独立倍率按计费口径单独处理。
 */
export function buildModelOffers(groups: ModelPlazaGroup[]): ModelOffer[] {
  const offers: ModelOffer[] = []
  for (const group of groups) {
    const rate = effectiveRate(group)
    for (const model of group.models) {
      const pricing = model.pricing
      if (!pricing) continue
      const mode = pricing.billing_mode || BILLING_MODE_TOKEN
      const imageRate = mode === BILLING_MODE_IMAGE && group.image_rate_independent
        ? (Number.isFinite(group.image_rate_multiplier) && group.image_rate_multiplier > 0 ? group.image_rate_multiplier : 1)
        : rate
      const isToken = mode === BILLING_MODE_TOKEN
      const inputPerMillion = isToken ? scaled(pricing.input_price, rate, true) : null
      const outputPerMillion = isToken ? scaled(pricing.output_price, rate, true) : null
      const requestPrice = isToken ? null : scaled(pricing.per_request_price, imageRate, false)
      offers.push({
        key: `${group.id}:${model.platform}:${model.name}`,
        modelKey: `${model.platform}:${model.name}`,
        model: model.name,
        platform: model.platform,
        groupId: group.id,
        groupName: group.name,
        groupRate: rate,
        subscriptionType: group.subscription_type,
        isExclusive: group.is_exclusive,
        qualityStatus: group.quality_status,
        billingMode: mode,
        inputPerMillion,
        outputPerMillion,
        cacheWritePerMillion: isToken ? scaled(pricing.cache_write_price, rate, true) : null,
        cacheReadPerMillion: isToken ? scaled(pricing.cache_read_price, rate, true) : null,
        requestPrice,
        blendedPerMillion: isToken ? blended(inputPerMillion, outputPerMillion) : null,
        multiTier: (pricing.intervals?.length ?? 0) > 1,
      })
    }
  }
  return offers
}

function minBy<T>(items: T[], pick: (item: T) => number): T | null {
  let best: T | null = null
  let bestValue = Number.POSITIVE_INFINITY
  for (const item of items) {
    const value = pick(item)
    if (!Number.isFinite(value)) continue
    if (best == null || value < bestValue) {
      best = item
      bestValue = value
    }
  }
  return best
}

function maxBy<T>(items: T[], pick: (item: T) => number): T | null {
  let best: T | null = null
  let bestValue = Number.NEGATIVE_INFINITY
  for (const item of items) {
    const value = pick(item)
    if (!Number.isFinite(value)) continue
    if (best == null || value > bestValue) {
      best = item
      bestValue = value
    }
  }
  return best
}

/** 按模型聚合报价，给出最低价、最高价、价差和最稳/最划算的渠道。 */
export function compareModels(offers: ModelOffer[]): ModelComparison[] {
  const byModel = new Map<string, ModelOffer[]>()
  for (const offer of offers) {
    const list = byModel.get(offer.modelKey)
    if (list) list.push(offer)
    else byModel.set(offer.modelKey, [offer])
  }

  const out: ModelComparison[] = []
  for (const [key, list] of byModel) {
    const inputOffers = list.filter(offer => offer.inputPerMillion != null)
    const outputOffers = list.filter(offer => offer.outputPerMillion != null)
    const blendedOffers = list.filter(offer => offer.blendedPerMillion != null)
    const stableOffers = blendedOffers.filter(offer => offer.qualityStatus === 'healthy')
    const requestOffers = list.filter(offer => offer.requestPrice != null)
    const cheapestInput = minBy(inputOffers, offer => offer.inputPerMillion as number)
    const mostExpensiveInput = maxBy(inputOffers, offer => offer.inputPerMillion as number)
    const cheapestRequest = minBy(requestOffers, offer => offer.requestPrice as number)
    const mostExpensiveRequest = maxBy(requestOffers, offer => offer.requestPrice as number)
    const minInput = cheapestInput?.inputPerMillion ?? null
    const maxInput = mostExpensiveInput?.inputPerMillion ?? null
    const minRequest = cheapestRequest?.requestPrice ?? null
    const maxRequest = mostExpensiveRequest?.requestPrice ?? null
    out.push({
      key,
      name: list[0].model,
      platform: list[0].platform,
      offers: list,
      cheapestInput,
      cheapestOutput: minBy(outputOffers, offer => offer.outputPerMillion as number),
      cheapestBlended: minBy(blendedOffers, offer => offer.blendedPerMillion as number),
      bestStable: minBy(stableOffers, offer => offer.blendedPerMillion as number),
      mostExpensiveInput,
      cheapestRequest,
      mostExpensiveRequest,
      minInput,
      maxInput,
      spread: minInput != null && maxInput != null && minInput > 0 ? maxInput / minInput - 1 : null,
      requestSpread: minRequest != null && maxRequest != null && minRequest > 0 ? maxRequest / minRequest - 1 : null,
      hasToken: list.some(offer => offer.billingMode === BILLING_MODE_TOKEN),
      hasRequest: list.some(offer => offer.billingMode !== BILLING_MODE_TOKEN),
      multiTier: list.some(offer => offer.multiTier),
    })
  }
  return out
}

export function filterComparisons(models: ModelComparison[], filters: ComparisonFilters): ModelComparison[] {
  const query = (filters.search ?? '').trim().toLowerCase()
  return models.filter(model => {
    if (filters.onlyComparable && model.offers.length < 2) return false
    if (filters.platform && model.platform !== filters.platform) return false
    if (filters.groupId && !model.offers.some(offer => offer.groupId === filters.groupId)) return false
    if (filters.billingMode && !model.offers.some(offer => offer.billingMode === filters.billingMode)) return false
    if (query) {
      const haystack = `${model.name} ${model.platform}`.toLowerCase()
      if (!haystack.includes(query)) return false
    }
    return true
  })
}

export function sortComparisons(models: ModelComparison[], sort: ComparisonSort): ModelComparison[] {
  const rows = [...models]
  if (sort === 'name') {
    rows.sort((a, b) => a.name.localeCompare(b.name) || a.platform.localeCompare(b.platform))
    return rows
  }
  const valueOf = (model: ModelComparison): number | null => {
    if (sort === 'input') return model.minInput
    if (sort === 'output') return model.cheapestOutput?.outputPerMillion ?? null
    return model.spread ?? model.requestSpread
  }
  rows.sort((a, b) => {
    const av = valueOf(a)
    const bv = valueOf(b)
    if (av == null && bv == null) return a.name.localeCompare(b.name)
    if (av == null) return 1
    if (bv == null) return -1
    if (av !== bv) return sort === 'spread' ? bv - av : av - bv
    return a.name.localeCompare(b.name)
  })
  return rows
}
