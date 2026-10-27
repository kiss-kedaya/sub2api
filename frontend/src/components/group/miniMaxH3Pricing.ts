export type MiniMaxH3Pricing = Record<string, Record<string, number | string | null>>

export function readMiniMaxH3Price(prices: MiniMaxH3Pricing) {
  const tariff = prices['minimax-h3'] ?? {}
  const entries = Object.entries(tariff).filter(([key]) => key === 'per_second' || /^request_([1-9]|1[0-5])s$/.test(key))
  const [key, value] = entries.length === 1 ? entries[0]! : ['per_second', null]
  return {
    enabled: entries.length > 0,
    mode: key === 'per_second' ? 'per_second' : 'per_request',
    seconds: key === 'per_second' ? 5 : Number(key.slice(8, -1)),
    price: value
  }
}

export function writeMiniMaxH3Price(prices: MiniMaxH3Pricing, settings: ReturnType<typeof readMiniMaxH3Price>): MiniMaxH3Pricing {
  const next = { ...prices }
  delete next['minimax-h3']
  if (!settings.enabled) return next
  const key = settings.mode === 'per_second' ? 'per_second' : `request_${settings.seconds}s`
  // Preserve the editable empty value for normal form validation. Saving an
  // incomplete price never creates a free tariff silently.
  next['minimax-h3'] = { [key]: settings.price }
  return next
}
