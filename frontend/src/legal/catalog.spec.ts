import { describe, expect, it } from 'vitest'
import {
  bundledLegalMarkdown,
  mergeLegalDocuments,
  resolveLegalDocumentId,
} from './catalog'

describe('legal catalog', () => {
  it('resolves canonical ids and OpenLux aliases used by existing admin docs', () => {
    expect(resolveLegalDocumentId('Privacy')).toBe('privacy')
    expect(resolveLegalDocumentId('usage-policy')).toBe('aup')
    expect(resolveLegalDocumentId('service-specific-terms')).toBe('disclaimer')
    expect(resolveLegalDocumentId('supported-countries')).toBe('supported-regions')
    expect(resolveLegalDocumentId('unknown-doc')).toBeNull()
  })

  it('falls back to bundled markdown when admin content is empty', () => {
    const merged = mergeLegalDocuments('zh', [
      { id: 'privacy', title: '自定义标题', content_md: '   ' },
      { id: 'usage-policy', title: '使用政策', content_md: '' },
    ])
    const privacy = merged.find((doc) => doc.id === 'privacy')
    const aup = merged.find((doc) => doc.id === 'aup')
    expect(privacy?.title).toBe('自定义标题')
    expect(privacy?.content_md).toContain('隐私政策')
    expect(aup?.title).toBe('使用政策')
    expect(aup?.content_md).toContain('可接受使用政策')
  })

  it('overrides bundled markdown when admin content exists', () => {
    const merged = mergeLegalDocuments('en', [
      { id: 'privacy-policy', title: 'Custom Privacy', content_md: 'Admin privacy body' },
    ])
    expect(merged.find((doc) => doc.id === 'privacy')).toEqual({
      id: 'privacy',
      title: 'Custom Privacy',
      content_md: 'Admin privacy body',
    })
  })

  it('keeps extra admin documents after bundled ones', () => {
    const merged = mergeLegalDocuments('en', [
      { id: 'custom-sla', title: 'SLA', content_md: 'sla' },
    ])
    expect(merged.at(-1)).toEqual({ id: 'custom-sla', title: 'SLA', content_md: 'sla' })
  })

  it('ships both locales for the OpenLux document set', () => {
    expect(bundledLegalMarkdown('disclaimer', 'zh')).toContain('免责条款')
    expect(bundledLegalMarkdown('disclaimer', 'en')).toContain('Disclaimer')
    expect(bundledLegalMarkdown('supported-regions', 'zh')).toContain('支持的国家和地区')
    expect(bundledLegalMarkdown('refund', 'zh')).toContain('原则上不予退款')
  })
})
