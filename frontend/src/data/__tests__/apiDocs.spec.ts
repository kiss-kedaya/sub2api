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

  it('publishes image, Nano Banana, and video pages in the media category for both locales', () => {
    const mediaPages = ['image-generation', 'nano-banana', 'video-generation']
    const zhPages = getApiDocs('zh', 'https://example.test')
    const enPages = getApiDocs('en', 'https://example.test')
    const pageIds = new Set(enPages.map(page => page.id))
    for (const id of mediaPages) {
      const zhPage = zhPages.find(page => page.id === id)
      const enPage = enPages.find(page => page.id === id)
      expect(zhPage, `missing zh page ${id}`).toBeDefined()
      expect(enPage, `missing en page ${id}`).toBeDefined()
      expect(zhPage!.category).toBe('media')
      expect(enPage!.category).toBe('media')
      expect(zhPage!.title).not.toBe(enPage!.title)
      expect(new Set(enPage!.sections.map(section => section.id)).size).toBe(enPage!.sections.length)
      for (const section of enPage!.sections) {
        for (const link of section.links || []) {
          if (link.page) expect(pageIds.has(link.page), `${id}:${section.id} links to ${link.page}`).toBe(true)
          else expect(link.href).toBeTruthy()
        }
      }
    }
    const quick = resolveDocsPage(enPages, 'quick-start')
    const quickLinks = quick.sections.flatMap(section => section.links || []).map(link => link.page)
    expect(quickLinks).toEqual(expect.arrayContaining(mediaPages))
  })

  it('documents both image endpoints, platform gating, and b64_json/url response shapes', () => {
    const origin = 'http://127.0.0.1:18888'
    const page = resolveDocsPage(getApiDocs('en', origin), 'image-generation')
    const content = JSON.stringify(page)
    expect(content).toContain('/v1/images/generations')
    expect(content).toContain('/v1/images/edits')
    expect(content).toContain('b64_json')
    expect(content).toContain('data[].url')
    expect(content).toContain('Images API is not supported for this platform')
    expect(content).toContain('Image generation is not enabled for this group')
    expect(content).toContain('grok-imagine-edit')
    expect(content).toContain('async image tasks are not enabled')
    expect(content).toContain('3 source images')
    expect(content).not.toContain('more returns a 400')
    const fields = page.sections.find(section => section.id === 'image-request')!.fields!.map(field => field.name)
    expect(fields).toEqual(expect.arrayContaining(['model', 'prompt', 'n', 'size', 'response_format', 'stream']))
    const generate = page.sections.find(section => section.id === 'image-request')!.examples!
    for (const snippet of Object.values(generate)) {
      expect(snippet).toContain(`${origin}/v1/images/generations`)
      expect(snippet).not.toContain('sk-')
    }
    const lastLinks = page.sections[page.sections.length - 1].links!.map(link => link.page)
    expect(lastLinks).toEqual(expect.arrayContaining(['nano-banana', 'video-generation']))
  })

  it('documents native Gemini image calls with responseModalities and inlineData', () => {
    const origin = 'http://127.0.0.1:18888'
    const page = resolveDocsPage(getApiDocs('en', origin), 'nano-banana')
    const content = JSON.stringify(page)
    expect(content).toContain('/v1beta/models/gemini-2.5-flash-image:generateContent')
    expect(content).toContain('responseModalities')
    expect(content).toContain('inlineData')
    expect(content).toContain('gemini-3-pro-image')
    expect(content).toContain('x-goog-api-key')
    expect(content).toContain('not available through /v1/images/generations')
    expect(content).toContain('API key group platform is not gemini')
    const request = page.sections.find(section => section.id === 'nano-request')!.examples!
    expect(request.curl).toContain(`${origin}/v1beta/models/gemini-2.5-flash-image:generateContent`)
    expect(request.curl).toContain('responseModalities')
    expect(request.curl).toContain('x-goog-api-key')
    expect(request.curl).not.toContain('sk-')
    expect(page.sections.find(section => section.id === 'nano-edit')!.examples!.json).toContain('inlineData')
  })

  it('documents asynchronous video create, poll, and content routes across providers', () => {
    const origin = 'http://127.0.0.1:18888'
    const page = resolveDocsPage(getApiDocs('en', origin), 'video-generation')
    const content = JSON.stringify(page)
    for (const route of ['/v1/videos', '/v1/videos/generations', '/v1/videos/{request_id}', '/v1/videos/{request_id}/content', '/v1/videos/edits', '/v1/videos/extensions']) {
      expect(content).toContain(route)
    }
    expect(content).toContain('Videos API is not supported for this platform')
    expect(content).toContain('grok-imagine-video')
    expect(content).toContain('MiniMax-H3')
    expect(content).toContain('seconds')
    expect(content).toContain('request_id')
    expect(content).toContain('completed')
    expect(content).toContain('allow_image_generation')
    expect(content).toContain('grok_media_no_eligible_account')
    expect(content).toContain('task_id')
    expect(content).toContain('/api/v3/contents/generations/tasks')
    const createResponse = page.sections.find(section => section.id === 'video-create-response')!
    expect(createResponse.fields!.map(field => field.name)).toEqual(['id', 'request_id'])
    expect(Object.keys(JSON.parse(createResponse.examples!.json!))).toEqual(['id', 'request_id'])
    const create = page.sections.find(section => section.id === 'video-create')!.examples!
    for (const snippet of Object.values(create)) {
      expect(snippet).toContain(`${origin}/v1/videos`)
      expect(snippet).not.toContain('sk-')
    }
    expect(page.sections[page.sections.length - 1].links!.map(link => link.page)).toEqual(expect.arrayContaining(['image-generation', 'nano-banana']))
  })

  it('keeps media docs free of prices, real credentials, and private hosts', () => {
    const origin = 'http://127.0.0.1:18888'
    for (const locale of ['zh', 'en']) {
      const media = getApiDocs(locale, origin).filter(page => page.category === 'media')
      expect(media).toHaveLength(3)
      const serialized = JSON.stringify(media)
      expect(serialized).not.toMatch(/\$\s?\d/)
      expect(serialized).not.toMatch(/sk-[A-Za-z0-9]{8,}/)
      expect(serialized).not.toContain('api.example.com')
      const examples = media.flatMap(page => page.sections.flatMap(section => Object.values(section.examples || {}))).filter((snippet): snippet is string => typeof snippet === 'string')
      expect(examples.some(snippet => snippet.includes(origin))).toBe(true)
      for (const snippet of examples) expect(snippet).not.toMatch(/sk-[A-Za-z0-9]/)
    }
  })
})
