import { describe, expect, it } from 'vitest'
import { getApiDocs, resolveDocsOrigin, resolveDocsPage } from '../apiDocs'

describe('public API documentation', () => {
  it('keeps the current HTTP scheme and port throughout executable examples', () => {
    const origin = 'http://127.0.0.1:17777'
    const pages = getApiDocs('en', origin)
    const quick = resolveDocsPage(pages, 'quick-start')
    expect(quick.sections[0].fields?.find(field => field.name === 'base_url')?.description).toBe(`${origin}/v1`)
    const request = quick.sections.find(section => section.id === 'first-request')!
    for (const snippet of Object.values(request.examples!)) {
      expect(snippet).toContain(`${origin}/v1/chat/completions`)
      expect(snippet).not.toContain('https://127.0.0.1')
      expect(snippet).not.toMatch(/\n\+\s+-H/)
      expect(snippet).not.toContain('/v1/v1')
    }
    expect(resolveDocsOrigin('http://127.0.0.1:17777/some/path')).toBe(origin)
    expect(resolveDocsOrigin('javascript:alert(1)')).toBe('http://localhost')
    expect(resolveDocsOrigin('https://user:secret@example.test')).toBe('http://localhost')
  })

  it('provides unique pages and anchors, and falls back for malformed page queries', () => {
    const pages = getApiDocs('zh', 'https://example.test')
    expect(new Set(pages.map(page => page.id)).size).toBe(pages.length)
    for (const page of pages) expect(new Set(page.sections.map(section => section.id)).size).toBe(page.sections.length)
    expect(resolveDocsPage(pages, 'does-not-exist').id).toBe('quick-start')
    expect(resolveDocsPage(pages, ['responses', 'messages']).id).toBe('quick-start')
    expect(resolveDocsPage(pages, 'responses').description).toBe('POST /v1/responses')
  })

  it('documents independent official tool payloads without model scaffolding or hardcoded fees', () => {
    const pages = getApiDocs('en', 'https://example.test')
    const search = resolveDocsPage(pages, 'deepseek-search')
    const fields = search.sections.find(section => section.id === 'search-parameters')!.fields!
    expect(fields.map(field => field.name)).toEqual(['query', 'count', 'ori_question', 'localization', 'need_content'])
    expect(fields[0].description).toContain('128')
    expect(fields[1].description).toContain('1–50')
    const code = search.sections[0].examples!.curl!
    expect(code).toContain('/v1/tools/search')
    expect(code).not.toContain('"model"')
    expect(code).not.toContain('"messages"')
    expect(search.sections[0].note).toContain('DeepSeek entry group')
    const fetch = resolveDocsPage(pages, 'deepseek-fetch')
    expect(fetch.sections[1].fields![0].description).toContain('8192')
    const example = JSON.parse(fetch.sections[2].examples!.json!)
    expect(Object.keys(example)).toEqual(['title', 'content'])
    expect(JSON.stringify(pages)).not.toContain('$0.005')
  })

  it('documents failure terminals and metered errors instead of assuming HTTP 200 means success', () => {
    const page = resolveDocsPage(getApiDocs('en', 'https://example.test'), 'streaming')
    const content = JSON.stringify(page)
    expect(content).toContain('response.failed')
    expect(content).toContain('HTTP status may remain 200')
    expect(content).toContain('Observed usage remains billable')
    expect(page.sections[0].examples!.curl).toContain('curl -N')
    expect(page.sections[0].examples!.python).toBeUndefined()
  })
})
