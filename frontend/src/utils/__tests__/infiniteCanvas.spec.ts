import { describe, expect, it } from 'vitest'

import type { ApiKey } from '@/types'
import {
  INFINITE_CANVAS_KEY_NAME,
  MAX_SMART_ROUTE_GROUPS,
  buildInfiniteCanvasImportUrl,
  findReusableCanvasKey,
  groupIdsEqual,
  resolveHttpBaseUrl,
  resolveInfiniteCanvasBaseUrl,
  selectSmartRoutingGroupIds,
} from '../infiniteCanvas'

describe('selectSmartRoutingGroupIds', () => {
  it('keeps available-group order, drops invalid ids, and caps at the smart-routing limit', () => {
    const groups = [
      { id: 3 },
      { id: 3 },
      { id: 0 },
      { id: 8 },
      ...Array.from({ length: 12 }, (_, index) => ({ id: 10 + index })),
    ]
    const ids = selectSmartRoutingGroupIds(groups)
    expect(ids[0]).toBe(3)
    expect(ids[1]).toBe(8)
    expect(ids).toHaveLength(MAX_SMART_ROUTE_GROUPS)
    expect(new Set(ids).size).toBe(ids.length)
  })
})

describe('groupIdsEqual', () => {
  it('compares ordered group lists', () => {
    expect(groupIdsEqual([1, 2], [1, 2])).toBe(true)
    expect(groupIdsEqual([1, 2], [2, 1])).toBe(false)
    expect(groupIdsEqual(undefined, [])).toBe(true)
  })
})

describe('resolveHttpBaseUrl', () => {
  it('prefers an absolute API base URL and falls back to the page origin', () => {
    expect(resolveHttpBaseUrl('https://api.example.com/v1/', 'http://ui.example.com')).toBe('https://api.example.com/v1')
    expect(resolveHttpBaseUrl('', 'http://ui.example.com')).toBe('http://ui.example.com')
    expect(resolveHttpBaseUrl('/gateway', 'https://ui.example.com')).toBe('https://ui.example.com/gateway')
  })
})

describe('resolveInfiniteCanvasBaseUrl', () => {
  it('uses the same-origin /canvas/ path unless an absolute override is provided', () => {
    expect(resolveInfiniteCanvasBaseUrl('', 'http://51.222.42.218:17777')).toBe('http://51.222.42.218:17777/canvas/')
    expect(resolveInfiniteCanvasBaseUrl('https://canvas.example.com', 'http://ui.example.com')).toBe('https://canvas.example.com/')
  })
})

describe('buildInfiniteCanvasImportUrl', () => {
  it('imports OpenAI-compatible credentials the way Infinite Canvas reads query params', () => {
    const href = buildInfiniteCanvasImportUrl({
      canvasBaseUrl: 'http://51.222.42.218:17777/canvas/',
      apiKey: 'sk-test',
      openaiBaseUrl: 'http://51.222.42.218:17777',
      theme: 'dark',
      lang: 'zh-CN',
    })
    const url = new URL(href)
    expect(url.origin).toBe('http://51.222.42.218:17777')
    expect(url.pathname).toBe('/canvas/')
    expect(url.searchParams.get('apiKey')).toBe('sk-test')
    expect(url.searchParams.get('baseUrl')).toBe('http://51.222.42.218:17777')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
  })
})

describe('findReusableCanvasKey', () => {
  it('prefers an active key with the reserved canvas name', () => {
    const keys = [
      { id: 1, name: INFINITE_CANVAS_KEY_NAME, status: 'inactive', key: 'old' },
      { id: 2, name: INFINITE_CANVAS_KEY_NAME, status: 'active', key: 'live' },
      { id: 3, name: 'other', status: 'active', key: 'nope' },
    ] as ApiKey[]
    expect(findReusableCanvasKey(keys)?.id).toBe(2)
  })
})
