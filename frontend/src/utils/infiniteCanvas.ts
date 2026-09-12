import type { ApiKey, Group } from '@/types'

export const INFINITE_CANVAS_KEY_NAME = 'Infinite Canvas'
export const INFINITE_CANVAS_PATH = '/canvas/'

export function selectSmartRoutingGroupIds(groups: Array<Pick<Group, 'id'>>): number[] {
  const seen = new Set<number>()
  const ids: number[] = []
  for (const group of groups) {
    if (!Number.isInteger(group.id) || group.id <= 0 || seen.has(group.id)) continue
    seen.add(group.id)
    ids.push(group.id)
  }
  return ids
}

export function groupIdsEqual(left: number[] | undefined, right: number[]): boolean {
  const a = left ?? []
  if (a.length !== right.length) return false
  return a.every((id, index) => id === right[index])
}

export function resolveHttpBaseUrl(pageOrigin: string): string {
  return String(pageOrigin || '').trim().replace(/\/+$/, '')
}

export function resolveInfiniteCanvasBaseUrl(pageOrigin: string): string {
  const origin = resolveHttpBaseUrl(pageOrigin)
  if (!origin) return INFINITE_CANVAS_PATH
  return `${origin}${INFINITE_CANVAS_PATH}`
}

export function buildInfiniteCanvasImportUrl(options: {
  canvasBaseUrl: string
  apiKey: string
  openaiBaseUrl: string
  pageOrigin: string
  theme?: 'light' | 'dark'
  lang?: string
}): string {
  const url = new URL(options.canvasBaseUrl, `${options.pageOrigin || 'http://localhost'}/`)
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
