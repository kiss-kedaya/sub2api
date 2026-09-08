export type UpstreamFailureKind =
  | 'cloudflare-waf'
  | 'capacity-cooling'
  | 'credential-forbidden'
  | 'rate-limit'
  | 'model_not_found'
  | ''

export function classifyUpstreamFailureBody(body: string, statusCode?: number | null): UpstreamFailureKind {
  const text = String(body || '').toLowerCase()
  if (
    text.includes('error code: 1010') ||
    text.includes('error 1010') ||
    text.includes('your request was blocked') ||
    text.includes('request was blocked') ||
    text.includes('cf-mitigated') ||
    text.includes('just a moment') ||
    text.includes('challenge-platform') ||
    text.includes('window._cf_chl_opt')
  ) {
    return 'cloudflare-waf'
  }
  if (
    text.includes('cooling') ||
    text.includes('货源均在冷却中') ||
    text.includes('候选供应商均请求失败') ||
    text.includes('all candidates failed')
  ) {
    return 'capacity-cooling'
  }
  if (text.includes('model_not_found') || text.includes('model not found') || text.includes('unknown provider for model')) {
    return 'model_not_found'
  }
  if (statusCode === 429 || text.includes('rate limit') || text.includes('too many requests')) {
    return 'rate-limit'
  }
  if (
    text.includes('invalid api key') ||
    text.includes('incorrect api key') ||
    text.includes('unauthorized') ||
    text.includes('authentication')
  ) {
    return 'credential-forbidden'
  }
  return ''
}
