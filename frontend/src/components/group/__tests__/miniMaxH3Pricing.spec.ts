import { describe, expect, it } from 'vitest'
import { readMiniMaxH3Price, writeMiniMaxH3Price } from '../miniMaxH3Pricing'

describe('MiniMax-H3 pricing', () => {
  it('preserves other provider prices and permits only one H3 tariff', () => {
    const prices = { 'grok-imagine-video': { '720p': 0.2 }, 'minimax-h3': { per_second: 0.02 } }
    const settings = readMiniMaxH3Price(prices)
    expect(settings).toMatchObject({ enabled: true, mode: 'per_second', price: 0.02 })
    expect(writeMiniMaxH3Price(prices, { ...settings, mode: 'per_request', seconds: 5, price: 0.08 })).toEqual({
      'grok-imagine-video': { '720p': 0.2 }, 'minimax-h3': { request_5s: 0.08 }
    })
    expect(writeMiniMaxH3Price(prices, { ...settings, enabled: false })).toEqual({ 'grok-imagine-video': { '720p': 0.2 } })
  })
  it('does not silently turn an empty price into a free tariff', () => {
    const settings = readMiniMaxH3Price({})
    expect(settings.enabled).toBe(false)
    expect(settings.price).toBe(null)
    expect(writeMiniMaxH3Price({}, { ...settings, enabled: true })['minimax-h3']).toEqual({ per_second: null })
  })
})
