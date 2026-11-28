export type MiniMaxH3Pricing = Record<string, Record<string, number | string | null>>
export type MiniMaxH3PricingMode = 'per_second' | 'flat_per_request' | 'per_request'

const FIXED_REQUEST_KEY_RE = /^request_([1-9]|[12][0-9]|30)s$/

export function readMiniMaxH3Price(prices: MiniMaxH3Pricing) {
  const tariff = prices['minimax-h3'] ?? {}
  const entries = Object.entries(tariff).filter(
    ([key]) => key === 'per_second' || key === 'per_request' || FIXED_REQUEST_KEY_RE.test(key)
  )
  const [key, value] = entries.length === 1 ? entries[0]! : ['per_second', null]
  const mode: MiniMaxH3PricingMode =
    key === 'per_second' ? 'per_second' : key === 'per_request' ? 'flat_per_request' : 'per_request'
  return {
    enabled: entries.length > 0,
    mode,
    seconds: mode === 'per_request' ? Number(key.slice(8, -1)) : 5,
    price: value
  }
}

export function writeMiniMaxH3Price(
  prices: MiniMaxH3Pricing,
  settings: ReturnType<typeof readMiniMaxH3Price>
): MiniMaxH3Pricing {
  const next = { ...prices }
  delete next['minimax-h3']
  if (!settings.enabled) return next
  const key =
    settings.mode === 'per_second'
      ? 'per_second'
      : settings.mode === 'flat_per_request'
        ? 'per_request'
        : `request_${settings.seconds}s`
  // Preserve the editable empty value for normal form validation. Saving an
  // incomplete price never creates a free tariff silently.
  next['minimax-h3'] = { [key]: settings.price }
  return next
}
