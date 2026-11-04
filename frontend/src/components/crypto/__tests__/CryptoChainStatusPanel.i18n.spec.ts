import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en/admin/crypto'
import zh from '@/i18n/locales/zh/admin/crypto'

const sources = import.meta.glob('../CryptoChainStatusPanel.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
const source = Object.values(sources)[0] ?? ''

type Tree = Record<string, unknown>
function leafKeys(tree: unknown, prefix = ''): string[] {
  if (tree === null || typeof tree !== 'object') return prefix ? [prefix] : []
  return Object.entries(tree as Tree).flatMap(([k, v]) => leafKeys(v, prefix ? `${prefix}.${k}` : k))
}
function valueAt(tree: unknown, path: string): unknown {
  return path.split('.').reduce<unknown>((cur, part) => (cur && typeof cur === 'object' ? (cur as Tree)[part] : undefined), tree)
}

const usedKeys = [...new Set([...source.matchAll(/\bt\(\s*'(admin\.crypto\.[^']+)'/g)].map(m => m[1].slice('admin.crypto.'.length)))].sort()
const chainStatusKeys = (locale: unknown) => leafKeys(valueAt(locale, 'crypto.chainStatus')).sort()

describe('node status panel messages', () => {
  it('found the panel source and the keys it uses', () => {
    expect(source.length).toBeGreaterThan(1000)
    expect(usedKeys.length).toBeGreaterThan(30)
  })

  it('every key the panel uses exists, with text, in both zh and en', () => {
    for (const [name, locale] of [['zh', zh], ['en', en]] as const) {
      const bad = usedKeys.filter(key => {
        const value = valueAt((locale as { crypto: unknown }).crypto, key)
        return typeof value !== 'string' || value.trim() === ''
      })
      expect(bad, `${name} is missing or has empty text for these panel keys`).toEqual([])
    }
  })

  it('zh and en define exactly the same node status keys', () => {
    expect(chainStatusKeys(en)).toEqual(chainStatusKeys(zh))
    expect(valueAt(zh.crypto, 'tabChains')).toBe('节点状态')
    expect(typeof valueAt(en.crypto, 'tabChains')).toBe('string')
  })

  it('has no dead keys: every chainStatus message is referenced by the panel', () => {
    const referenced = new Set(usedKeys)
    const dead = chainStatusKeys(zh).map(k => `chainStatus.${k}`).filter(k => !referenced.has(k))
    expect(dead).toEqual([])
  })

  it('keeps the placeholders of each message identical in both languages', () => {
    const params = (text: unknown) => [...String(text).matchAll(/\{(\w+)\}/g)].map(m => m[1]).sort()
    for (const key of chainStatusKeys(zh)) {
      expect(params(valueAt(en.crypto, `chainStatus.${key}`)), key).toEqual(params(valueAt(zh.crypto, `chainStatus.${key}`)))
    }
  })

  it('words the unenabled-chain warning exactly as agreed', () => {
    expect(valueAt(zh.crypto, 'chainStatus.disabledWarning')).toBe('已配置 RPC 但未开启，不会被扫描')
  })
})

describe('motion', () => {
  it('turns the flowing stripes and the width transition off for prefers-reduced-motion', () => {
    const block = source.match(/@media \(prefers-reduced-motion: reduce\)\s*\{([\s\S]*?)\n\}/)
    expect(block, 'the panel must respect prefers-reduced-motion').not.toBeNull()
    expect(block![1]).toContain('animation: none')
    expect(block![1]).toContain('transition: none')
    expect(source).toMatch(/animation:\s*chain-bar-flow/)
  })
})
