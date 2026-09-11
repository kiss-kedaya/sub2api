import type { ApiKey, Group } from '@/types'

export const INFINITE_CANVAS_KEY_NAME = 'Infinite Canvas'
export const INFINITE_CANVAS_PATH = '/canvas/'
export const MAX_SMART_ROUTE_GROUPS = 10

export function selectSmartRoutingGroupIds(groups: Array<Pick<Group, 'id'>>): number[] {
  const seen = new Set<number>()
  const ids: number[] = []
  for (const group of groups) {
    if (!Number.isInteger(group.id) || group.id <= 0 || seen.has(group.id)) continue
    seen.add(group.id)
    ids.push(group.id)
    if (ids.length >= MAX_SMART_ROUTE_GROUPS) break
  }
  return ids
}

export function groupIdsEqual(left: number[] | undefined, right: number[]): boolean {
  const a = left ?? []
  if (a.length !== right.length) return false
  return a.every((id, index) => id === right[index])
}

export function resolveHttpBaseUrl(value: string | undefined | null, pageOrigin: string): string {
  const origin = String(pageOrigin || '').trim().replace(/\/+$/, '')
  const raw = String(value || '').trim()
  const candidate = raw || origin
  try {
    const url = new URL(candidate, origin || undefined)
    if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.hostname) {
      return origin
    }
    url.hash = ''
    url.search = ''
    return url.toString().replace(/\/+$/, '')
  } catch {
    return origin
  }
}

export function resolveInfiniteCanvasBaseUrl(envUrl: string | undefined | null, pageOrigin: string): string {
  const origin = String(pageOrigin || '').trim().replace(/\/+$/, '')
  const fallback = origin ? `${origin}${INFINITE_CANVAS_PATH}` : INFINITE_CANVAS_PATH
  const raw = String(envUrl || '').trim()
  if (!raw) return fallback.endsWith('/') ? fallback : `${fallback}/`
  try {
    const url = new URL(raw, origin || undefined)
    if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.hostname) {
      return fallback.endsWith('/') ? fallback : `${fallback}/`
    }
    url.hash = ''
    url.search = ''
    const href = url.toString()
    return href.endsWith('/') ? href : `${href}/`
  } catch {
    return fallback.endsWith('/') ? fallback : `${fallback}/`
  }
}

export function buildInfiniteCanvasImportUrl(options: {
  canvasBaseUrl: string
  apiKey: string
  openaiBaseUrl: string
  theme?: 'light' | 'dark'
  lang?: string
}): string {
  const url = new URL(options.canvasBaseUrl)
  url.searchParams.set('apiKey', options.apiKey)
  url.searchParams.set('baseUrl', options.openaiBaseUrl)
  if (options.theme) {
    url.searchParams.set('theme', options.theme)
  }
  if (options.lang) {
    url.searchParams.set('lang', options.lang)
  }
  return url.toString()
}

export function findReusableCanvasKey(keys: ApiKey[], name = INFINITE_CANVAS_KEY_NAME): ApiKey | undefined {
  const matches = keys.filter((key) => key.name === name)
  return matches.find((key) => key.status === 'active') ?? matches[0]
}
