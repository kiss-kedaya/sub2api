import type { Account } from '@/types'
import type { CustomUsageConfig, CustomUsageExtractor, CustomUsageTemplate } from '@/api/admin/customUsage'

export function supportsCustomUsage(account: Pick<Account, 'type' | 'credentials'>): boolean {
  return account.type === 'apikey' && typeof account.credentials?.base_url === 'string' && !!account.credentials.base_url.trim()
}

export function usageTemplate(template: CustomUsageTemplate): Pick<CustomUsageConfig, 'request' | 'extractor'> {
  if (template === 'newapi') return {
    request: { url: '{{baseUrl}}/api/user/self', method: 'GET', headers: { Authorization: 'Bearer {{accessToken}}', 'New-Api-User': '{{userId}}' } },
    extractor: { remaining: { path: 'data.quota', divisor: 500000 }, used: { path: 'data.used_quota', divisor: 500000 }, unit: { value: 'USD' } }
  }
  if (template === 'sub2api') return {
    request: { url: '{{baseUrl}}/v1/usage', method: 'GET', headers: { Authorization: 'Bearer {{apiKey}}' } },
    // GatewayHandler.usageUnrestricted / usageQuotaLimited return an unwrapped object.
    extractor: { remaining: { path: 'remaining' }, unit: { path: 'unit' }, plan_name: { path: 'planName' } }
  }
  return {
    request: { url: template === 'custom' ? '' : '{{baseUrl}}/v1/usage', method: 'GET', headers: { Authorization: 'Bearer {{apiKey}}' } },
    extractor: { remaining: { path: 'remaining' }, unit: { value: 'USD' } }
  }
}
export function createUsageDraft(baseUrl = ''): CustomUsageConfig {
  return { enabled: true, template: 'general', base_url: baseUrl.replace(/\/+$/, ''), timeout_seconds: 10, interval_minutes: 10, ...usageTemplate('general') }
}

export type UsageValidationError = 'invalidJson' | 'invalidHeaders' | 'invalidExtractor' | 'invalidUrl' | 'invalidTimeout' | 'invalidInterval' | 'invalidPlaceholder'
const isObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
const allowedTokens = new Set(['baseUrl', 'apiKey', 'accessToken', 'userId'])
const validTemplate = (value: string) => {
  const stripped = value.replace(/\{\{([A-Za-z]+)\}\}/g, (_, name: string) => allowedTokens.has(name) ? '' : '{invalid}')
  return !/[{}]/.test(stripped)
}
const validURL = (value: string): boolean => {
  try { const url = new URL(value); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && !url.hash } catch { return false }
}
// Only property/index traversal, never expressions, functions, filters or JavaScript.
const validPath = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256 && /^(?:\$\.)?[A-Za-z_][\w-]*(?:(?:\.[A-Za-z_][\w-]*)|(?:\[\d+\])|(?:\.\d+))*$/.test(value) && value.split(/[.\[\]]/).filter(Boolean).length <= 24 && !value.split(/[.\[\]]/).some(part => ['__proto__', 'constructor', 'prototype'].includes(part))

export function parseUsageEditors(headersText: string, extractorText: string): { headers?: Record<string, string>; extractor?: CustomUsageExtractor; error?: UsageValidationError } {
  let headers: unknown, extractor: unknown
  try { headers = JSON.parse(headersText); extractor = JSON.parse(extractorText) } catch { return { error: 'invalidJson' } }
  if (!isObject(headers) || Object.entries(headers).some(([name, value]) => !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) || typeof value !== 'string' || value.length > 8192 || /[\r\n]/.test(value) || value.includes(String.fromCharCode(0)) || ['__proto__', 'constructor', 'prototype'].includes(name))) return { error: 'invalidHeaders' }
  const names = Object.keys(headers).map(name => name.toLowerCase())
  const forbidden = new Set(['host', 'connection', 'content-length', 'transfer-encoding', 'upgrade', 'proxy-authorization', 'proxy-connection', 'te', 'trailer', 'accept-encoding'])
  if (names.length > 24 || new Set(names).size !== names.length || names.some(name => forbidden.has(name))) return { error: 'invalidHeaders' }
  if (Object.values(headers).some(value => !validTemplate(String(value)))) return { error: 'invalidPlaceholder' }
  if (!isObject(extractor) || !Object.keys(extractor).length) return { error: 'invalidExtractor' }
  for (const [name, spec] of Object.entries(extractor)) {
    if (!isObject(spec)) return { error: 'invalidExtractor' }
    if (['remaining', 'used', 'total'].includes(name)) {
      if (Object.keys(spec).some(key => !['path', 'divisor'].includes(key)) || !validPath(spec.path) || (spec.divisor !== undefined && (typeof spec.divisor !== 'number' || !Number.isFinite(spec.divisor) || spec.divisor <= 0))) return { error: 'invalidExtractor' }
    } else if (['unit', 'plan_name'].includes(name)) {
      if (Object.keys(spec).some(key => !['path', 'value'].includes(key)) || (!spec.path && !spec.value) || (Boolean(spec.path) && Boolean(spec.value)) || (spec.path !== undefined && !validPath(spec.path)) || (spec.value !== undefined && (typeof spec.value !== 'string' || spec.value.length > 128 || /[<>{}\\]/.test(spec.value) || [...spec.value].some(character => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)))) return { error: 'invalidExtractor' }
    } else return { error: 'invalidExtractor' }
  }
  if (!['remaining', 'used', 'total'].some(key => extractor[key] !== undefined)) return { error: 'invalidExtractor' }
  return { headers: headers as Record<string, string>, extractor: extractor as CustomUsageExtractor }
}

export function validateUsageDraft(config: CustomUsageConfig): UsageValidationError | null {
  if (!Number.isInteger(config.timeout_seconds) || config.timeout_seconds < 1 || config.timeout_seconds > 30) return 'invalidTimeout'
  if (!Number.isSafeInteger(config.interval_minutes) || (config.interval_minutes !== 0 && config.interval_minutes < 5) || config.interval_minutes > 525600) return 'invalidInterval'
  if (!validTemplate(config.request.url)) return 'invalidPlaceholder'
  const resolved = config.request.url.replace(/\{\{(baseUrl|apiKey|accessToken|userId)\}\}/g, (_, name: string) => name === 'baseUrl' ? config.base_url.trim().replace(/\/+$/, '') : 'placeholder')
  if (!validURL(config.base_url.trim()) || !validURL(resolved)) return 'invalidUrl'
  return parseUsageEditors(JSON.stringify(config.request.headers), JSON.stringify(config.extractor)).error ?? null
}

export function usagePayload(config: CustomUsageConfig): CustomUsageConfig {
  return {
    enabled: config.enabled, template: config.template, base_url: config.base_url.trim().replace(/\/+$/, ''),
    timeout_seconds: config.timeout_seconds, interval_minutes: config.interval_minutes,
    request: { url: config.request.url.trim(), method: 'GET', headers: { ...config.request.headers } },
    extractor: JSON.parse(JSON.stringify(config.extractor)) as CustomUsageExtractor,
    ...(config.user_id?.trim() ? { user_id: config.user_id.trim() } : {}),
    ...(config.clear_api_key ? { clear_api_key: true } : config.api_key?.trim() ? { api_key: config.api_key.trim() } : {}),
    ...(config.clear_access_token ? { clear_access_token: true } : config.access_token?.trim() ? { access_token: config.access_token.trim() } : {})
  }
}
