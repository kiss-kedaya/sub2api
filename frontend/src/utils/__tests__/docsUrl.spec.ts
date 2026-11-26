import { describe, expect, it } from 'vitest'
import { INTERNAL_DOCS_URL, isExternalDocsUrl, resolveDocsUrl } from '../docsUrl'

describe('documentation URL selection', () => {
  it('falls back to local docs without a configured URL', () => {
    expect(resolveDocsUrl('')).toBe(INTERNAL_DOCS_URL)
    expect(resolveDocsUrl(undefined)).toBe(INTERNAL_DOCS_URL)
  })
  it('retains valid configured external and local URLs', () => {
    expect(resolveDocsUrl('https://docs.example.test/guide')).toBe('https://docs.example.test/guide')
    expect(resolveDocsUrl('/guide?page=start')).toBe('/guide?page=start')
  })
  it('distinguishes external http(s) docs URLs from internal SPA docs paths', () => {
    expect(isExternalDocsUrl('https://docs.example.test/guide')).toBe(true)
    expect(isExternalDocsUrl('http://docs.example.test/guide')).toBe(true)
    expect(isExternalDocsUrl('/docs?page=quick-start')).toBe(false)
    expect(isExternalDocsUrl('')).toBe(false)
  })
  it('cannot turn a docs link into executable or protocol-relative content', () => {
    for (const value of ['javascript:alert(1)', 'data:text/html,<script>', '//evil.example.test/docs']) {
      expect(resolveDocsUrl(value)).toBe(INTERNAL_DOCS_URL)
    }
  })
})
