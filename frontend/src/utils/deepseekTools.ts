/**
 * DeepSeek standalone tool endpoints metadata and effective-price helpers.
 * Shared across Model Plaza, Model Comparison, and Admin Group pricing UI so
 * displayed units, official defaults, and multiplier calculations match backend
 * settlement (backend/internal/service/deepseek_tool_pricing.go).
 */

export const DEEPSEEK_TOOL_MODELS = [
  'deepseek-tools-search',
  'deepseek-tools-fetch',
  'deepseek-tools-ocr',
  'deepseek-tools-tts',
  'deepseek-tools-asr',
] as const

export type DeepSeekToolModel = (typeof DEEPSEEK_TOOL_MODELS)[number]

export interface DeepSeekToolSpec {
  model: DeepSeekToolModel
  unitKey: 'perUnitRequest' | 'perUnitPage' | 'perUnitMillionChars' | 'perUnitHour'
  unitZh: string
  unitEn: string
  /** Official built-in base price in the canonical billing unit (call / page / 1M chars / hour). */
  officialUnitPrice: number
  /** Official built-in base price in the admin input unit (per 1k calls / per 1k pages / per 1M chars / per hour). */
  officialInputPrice: number
  /** Divisor from admin input unit to canonical billing unit (1000 for search/fetch/ocr, 1 for tts/asr). */
  inputScale: number
}

export const DEEPSEEK_TOOL_SPECS: Record<DeepSeekToolModel, DeepSeekToolSpec> = {
  'deepseek-tools-search': {
    model: 'deepseek-tools-search',
    unitKey: 'perUnitRequest',
    unitZh: '次',
    unitEn: 'call',
    officialUnitPrice: 0.005,
    officialInputPrice: 5,
    inputScale: 1000,
  },
  'deepseek-tools-fetch': {
    model: 'deepseek-tools-fetch',
    unitKey: 'perUnitRequest',
    unitZh: '次',
    unitEn: 'call',
    officialUnitPrice: 0.005,
    officialInputPrice: 5,
    inputScale: 1000,
  },
  'deepseek-tools-ocr': {
    model: 'deepseek-tools-ocr',
    unitKey: 'perUnitPage',
    unitZh: '页',
    unitEn: 'page',
    officialUnitPrice: 0.0015,
    officialInputPrice: 1.5,
    inputScale: 1000,
  },
  'deepseek-tools-tts': {
    model: 'deepseek-tools-tts',
    unitKey: 'perUnitMillionChars',
    unitZh: '百万字符',
    unitEn: '1M chars',
    officialUnitPrice: 300,
    officialInputPrice: 300,
    inputScale: 1,
  },
  'deepseek-tools-asr': {
    model: 'deepseek-tools-asr',
    unitKey: 'perUnitHour',
    unitZh: '小时',
    unitEn: 'hr',
    officialUnitPrice: 0.78,
    officialInputPrice: 0.78,
    inputScale: 1,
  },
}

export function getDeepSeekToolSpec(model: string): DeepSeekToolSpec | null {
  const key = model.trim().toLowerCase() as DeepSeekToolModel
  return DEEPSEEK_TOOL_SPECS[key] ?? null
}

export function isDeepSeekToolModel(model: string): boolean {
  return getDeepSeekToolSpec(model) !== null
}

export function deepSeekToolSortOrder(model: string): number {
  const idx = DEEPSEEK_TOOL_MODELS.indexOf(model.trim().toLowerCase() as DeepSeekToolModel)
  return idx === -1 ? 999 : idx
}

export interface DeepSeekToolPreviewInput {
  rate_multiplier: number | string | null | undefined
  tool_rate_independent: boolean
  tool_rate_multiplier: number | string | null | undefined
  search_price_per_1k: number | string | null | undefined
  fetch_price_per_1k: number | string | null | undefined
  ocr_price_per_1k_pages: number | string | null | undefined
  audio_tts_price_per_million_chars: number | string | null | undefined
  audio_stt_price_per_hour: number | string | null | undefined
}

export interface DeepSeekToolPreviewItem {
  model: DeepSeekToolModel
  baseInputPrice: number
  effectiveInputPrice: number
  effectiveUnitPrice: number
  inheritedFromSearch: boolean
  isDefault: boolean
  isFree: boolean
}

function parseOptionalPrice(value: number | string | null | undefined): number | null {
  if (value === null || value === undefined || value === '') return null
  const num = Number(value)
  if (!Number.isFinite(num) || num < 0) return null
  return num
}

export function resolveEffectiveDeepSeekToolMultiplier(input: Pick<DeepSeekToolPreviewInput, 'rate_multiplier' | 'tool_rate_independent' | 'tool_rate_multiplier'>): number {
  const raw = input.tool_rate_independent ? input.tool_rate_multiplier : input.rate_multiplier
  if (raw === null || raw === undefined || raw === '') return 1
  const num = Number(raw)
  return Number.isFinite(num) && num >= 0 ? num : 1
}

export function computeDeepSeekToolPreviews(input: DeepSeekToolPreviewInput): {
  multiplier: number
  items: DeepSeekToolPreviewItem[]
} {
  const multiplier = resolveEffectiveDeepSeekToolMultiplier(input)
  const searchRaw = parseOptionalPrice(input.search_price_per_1k)
  const fetchRaw = parseOptionalPrice(input.fetch_price_per_1k)
  const ocrRaw = parseOptionalPrice(input.ocr_price_per_1k_pages)
  const ttsRaw = parseOptionalPrice(input.audio_tts_price_per_million_chars)
  const asrRaw = parseOptionalPrice(input.audio_stt_price_per_hour)

  const build = (
    model: DeepSeekToolModel,
    resolvedBaseInput: number,
    isDefault: boolean,
    inheritedFromSearch = false,
  ): DeepSeekToolPreviewItem => {
    const spec = DEEPSEEK_TOOL_SPECS[model]
    const effectiveInputPrice = resolvedBaseInput * multiplier
    const effectiveUnitPrice = (resolvedBaseInput / spec.inputScale) * multiplier
    return {
      model,
      baseInputPrice: resolvedBaseInput,
      effectiveInputPrice,
      effectiveUnitPrice,
      inheritedFromSearch,
      isDefault,
      isFree: effectiveUnitPrice === 0,
    }
  }

  const searchBase = searchRaw ?? DEEPSEEK_TOOL_SPECS['deepseek-tools-search'].officialInputPrice
  const fetchBase = fetchRaw ?? searchRaw ?? DEEPSEEK_TOOL_SPECS['deepseek-tools-fetch'].officialInputPrice
  const ocrBase = ocrRaw ?? DEEPSEEK_TOOL_SPECS['deepseek-tools-ocr'].officialInputPrice
  const ttsBase = ttsRaw ?? DEEPSEEK_TOOL_SPECS['deepseek-tools-tts'].officialInputPrice
  const asrBase = asrRaw ?? DEEPSEEK_TOOL_SPECS['deepseek-tools-asr'].officialInputPrice

  return {
    multiplier,
    items: [
      build('deepseek-tools-search', searchBase, searchRaw === null),
      build('deepseek-tools-fetch', fetchBase, fetchRaw === null && searchRaw === null, fetchRaw === null && searchRaw !== null),
      build('deepseek-tools-ocr', ocrBase, ocrRaw === null),
      build('deepseek-tools-tts', ttsBase, ttsRaw === null),
      build('deepseek-tools-asr', asrBase, asrRaw === null),
    ],
  }
}

export function formatDeepSeekToolAmount(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '0'
  const abs = Math.abs(value)
  const decimals = abs >= 1000 ? 4 : abs >= 1 ? 6 : 8
  const fixed = value.toFixed(decimals)
  return fixed.includes('.') ? fixed.replace(/\.?0+$/, '') : fixed
}
