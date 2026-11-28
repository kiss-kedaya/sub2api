import { describe, expect, it } from 'vitest'
import {
  DEEPSEEK_TOOL_MODELS,
  computeDeepSeekToolPreviews,
  deepSeekToolSortOrder,
  formatDeepSeekToolAmount,
  getDeepSeekToolSpec,
  isDeepSeekToolModel,
  resolveEffectiveDeepSeekToolMultiplier,
} from '../deepseekTools'

describe('deepseekTools helpers', () => {
  it('recognizes all 5 DeepSeek tool endpoints with canonical units and order', () => {
    expect(DEEPSEEK_TOOL_MODELS).toEqual([
      'deepseek-tools-search',
      'deepseek-tools-fetch',
      'deepseek-tools-ocr',
      'deepseek-tools-tts',
      'deepseek-tools-asr',
    ])
    for (let i = 0; i < DEEPSEEK_TOOL_MODELS.length; i++) {
      const m = DEEPSEEK_TOOL_MODELS[i]
      expect(isDeepSeekToolModel(m)).toBe(true)
      expect(isDeepSeekToolModel(`  ${m.toUpperCase()}  `)).toBe(true)
      expect(deepSeekToolSortOrder(m)).toBe(i)
    }
    expect(isDeepSeekToolModel('deepseek-chat')).toBe(false)
    expect(deepSeekToolSortOrder('deepseek-chat')).toBe(999)
    expect(getDeepSeekToolSpec('deepseek-tools-ocr')?.unitKey).toBe('perUnitPage')
    expect(getDeepSeekToolSpec('deepseek-tools-tts')?.unitKey).toBe('perUnitMillionChars')
    expect(getDeepSeekToolSpec('deepseek-tools-asr')?.unitKey).toBe('perUnitHour')
  })

  it('computes effective prices following group multiplier when tool_rate_independent is false', () => {
    const result = computeDeepSeekToolPreviews({
      rate_multiplier: 0.05,
      tool_rate_independent: false,
      tool_rate_multiplier: 1,
      search_price_per_1k: null,
      fetch_price_per_1k: null,
      ocr_price_per_1k_pages: null,
      audio_tts_price_per_million_chars: null,
      audio_stt_price_per_hour: null,
    })
    expect(result.multiplier).toBeCloseTo(0.05)
    const byModel = Object.fromEntries(result.items.map(item => [item.model, item]))
    expect(byModel['deepseek-tools-search'].effectiveUnitPrice).toBeCloseTo(0.00025)
    expect(byModel['deepseek-tools-search'].isDefault).toBe(true)
    expect(byModel['deepseek-tools-fetch'].effectiveUnitPrice).toBeCloseTo(0.00025)
    expect(byModel['deepseek-tools-fetch'].inheritedFromSearch).toBe(false)
    expect(byModel['deepseek-tools-ocr'].effectiveUnitPrice).toBeCloseTo(0.000075)
    expect(byModel['deepseek-tools-tts'].effectiveUnitPrice).toBeCloseTo(15)
    expect(byModel['deepseek-tools-asr'].effectiveUnitPrice).toBeCloseTo(0.039)
  })

  it('supports independent tool multiplier, fetch inheritance from search, and explicit 0 free', () => {
    const inherited = computeDeepSeekToolPreviews({
      rate_multiplier: 0.3,
      tool_rate_independent: true,
      tool_rate_multiplier: 0.5,
      search_price_per_1k: 4,
      fetch_price_per_1k: null,
      ocr_price_per_1k_pages: 0,
      audio_tts_price_per_million_chars: 100,
      audio_stt_price_per_hour: 0.5,
    })
    expect(inherited.multiplier).toBeCloseTo(0.5)
    const byModel = Object.fromEntries(inherited.items.map(item => [item.model, item]))
    expect(byModel['deepseek-tools-search'].effectiveUnitPrice).toBeCloseTo(0.002)
    expect(byModel['deepseek-tools-fetch'].effectiveUnitPrice).toBeCloseTo(0.002)
    expect(byModel['deepseek-tools-fetch'].inheritedFromSearch).toBe(true)
    expect(byModel['deepseek-tools-fetch'].isDefault).toBe(false)
    expect(byModel['deepseek-tools-ocr'].effectiveUnitPrice).toBe(0)
    expect(byModel['deepseek-tools-ocr'].isFree).toBe(true)
    expect(byModel['deepseek-tools-tts'].effectiveUnitPrice).toBeCloseTo(50)
    expect(byModel['deepseek-tools-asr'].effectiveUnitPrice).toBeCloseTo(0.25)

    // Explicit 0 on fetch overrides search price and marks fetch free
    const freeFetch = computeDeepSeekToolPreviews({
      rate_multiplier: 1,
      tool_rate_independent: false,
      tool_rate_multiplier: 1,
      search_price_per_1k: 5,
      fetch_price_per_1k: 0,
      ocr_price_per_1k_pages: null,
      audio_tts_price_per_million_chars: null,
      audio_stt_price_per_hour: null,
    })
    const fetchItem = freeFetch.items.find(i => i.model === 'deepseek-tools-fetch')!
    expect(fetchItem.effectiveUnitPrice).toBe(0)
    expect(fetchItem.isFree).toBe(true)
    expect(fetchItem.inheritedFromSearch).toBe(false)
  })

  it('handles zero independent multiplier and formats tiny & large amounts without scientific notation', () => {
    expect(
      resolveEffectiveDeepSeekToolMultiplier({
        rate_multiplier: 0.3,
        tool_rate_independent: true,
        tool_rate_multiplier: 0,
      }),
    ).toBe(0)
    expect(formatDeepSeekToolAmount(0)).toBe('0')
    expect(formatDeepSeekToolAmount(300)).toBe('300')
    expect(formatDeepSeekToolAmount(100000)).toBe('100000')
    expect(formatDeepSeekToolAmount(15)).toBe('15')
    expect(formatDeepSeekToolAmount(0.039)).toBe('0.039')
    expect(formatDeepSeekToolAmount(0.00025)).toBe('0.00025')
    expect(formatDeepSeekToolAmount(0.000075)).toBe('0.000075')
    expect(formatDeepSeekToolAmount(0.78 * 0.3)).toBe('0.234')
  })
})
