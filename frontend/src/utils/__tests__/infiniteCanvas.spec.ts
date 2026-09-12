import { describe, expect, it } from 'vitest'

import type { ApiKey } from '@/types'
import {
  INFINITE_CANVAS_KEY_NAME,
  buildInfiniteCanvasImportUrl,
  findReusableCanvasKey,
  groupIdsEqual,
  resolveHttpBaseUrl,
  resolveInfiniteCanvasBaseUrl,
  selectSmartRoutingGroupIds,
} from '../infiniteCanvas'

describe('selectSmartRoutingGroupIds', () => {
  it('keeps available-group order and drops invalid ids', () => {
    const extra = Array.from({ length: 12 }, (_, index) => ({ id: 10 + index }))
    const groups = [{ id: 3 }, { id: 3 }, { id: 0 }, { id: 8 }, ...extra]
    const ids = selectSmartRoutingGroupIds(groups)
    expect(ids[0]).toBe(3)
    expect(ids[1]).toBe(8)
    expect(ids).toHaveLength(2 + extra.length)
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
  it('always uses the page origin so canvas API calls stay same-origin', () => {
    expect(resolveHttpBaseUrl('https://kedaya.ai/')).toBe('https://kedaya.ai')
    expect(resolveHttpBaseUrl('http://localhost:5173')).toBe('http://localhost:5173')
  })
})

describe('resolveInfiniteCanvasBaseUrl', () => {
  it('stays on the current origin /canvas/ path', () => {
    expect(resolveInfiniteCanvasBaseUrl('https://kedaya.ai')).toBe('https://kedaya.ai/canvas/')
    expect(resolveInfiniteCanvasBaseUrl('https://kedaya.ai/')).toBe('https://kedaya.ai/canvas/')
  })
})

describe('buildInfiniteCanvasImportUrl', () => {
  it('imports credentials against the same origin the sidebar is on', () => {
    const href = buildInfiniteCanvasImportUrl({
      canvasBaseUrl: '/canvas/',
      apiKey: 'sk-test',
      openaiBaseUrl: 'https://kedaya.ai',
      pageOrigin: 'https://kedaya.ai',
      theme: 'dark',
      lang: 'zh-CN',
    })
    const url = new URL(href)
    expect(url.origin).toBe('https://kedaya.ai')
    expect(url.pathname).toBe('/canvas/')
    expect(url.searchParams.get('apiKey')).toBe('sk-test')
    expect(url.searchParams.get('baseUrl')).toBe('https://kedaya.ai')
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
