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
    expect(content).toContain('GIF output is not enabled for this request')
    expect(content).toContain('check the actual format of the returned image')
    expect(content).not.toContain('more returns a 400')
    const zhContent = JSON.stringify(resolveDocsPage(getApiDocs('zh', origin), 'image-generation'))
    expect(zhContent).toContain('GIF output is not enabled for this request')
    expect(zhContent).toContain('请检查返回图片的实际格式')
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

  it.each(['zh', 'en'])('publishes DeepSeek ASR, TTS and OCR reference pages in %s with safe examples', (locale) => {
    const origin = 'http://127.0.0.1:18888'
    const pages = getApiDocs(locale, origin)
    const ids = ['deepseek-asr', 'deepseek-tts', 'deepseek-ocr']
    expect(pages.slice(pages.findIndex(page => page.id === 'deepseek-fetch') + 1).map(page => page.id)).toEqual(ids)
    const titles = locale === 'zh'
      ? ['语音识别 ASR', '语音合成 TTS', '文档识别 OCR']
      : ['Speech recognition ASR', 'Speech synthesis TTS', 'Document recognition OCR']
    for (const [index, id] of ids.entries()) {
      const page = resolveDocsPage(pages, id)
      expect(page.title).toBe(titles[index])
      expect(page.category).toBe('reference')
      expect(page.sections[0].note).toContain('DeepSeek')
      const request = page.sections.find(section => section.id.endsWith('-parameters'))!
      expect(Object.keys(request.examples!)).toEqual(['curl', 'python', 'node'])
      for (const [language, snippet] of Object.entries(request.examples!)) {
        expect(snippet).toContain(`${origin}/v1/tools/${id.slice('deepseek-'.length)}`)
        expect(snippet).toContain(language === 'curl' ? '$API_KEY' : language === 'python' ? 'os.environ["API_KEY"]' : 'process.env.API_KEY')
        expect(snippet).not.toMatch(/kedaya\.(ai|xyz)|api\.deepseek\.com|sk-[A-Za-z0-9]/)
        expect(snippet).not.toContain('"model"')
      }
      expect(JSON.stringify(page)).not.toMatch(/\$\s?\d|0\.225|0\.116|0\.45/)
      expect(page.sections.some(section => section.id.endsWith('-errors') && section.fields?.some(field => field.name.includes('429')))).toBe(true)
    }
  })

  it('documents audio limits, streamed binary output and unsupported WebSocket synthesis', () => {
    const pages = getApiDocs('en', 'https://example.test')
    const asr = resolveDocsPage(pages, 'deepseek-asr')
    const asrContent = JSON.stringify(asr)
    for (const text of ['16-bit PCM WAV', 'Ogg Opus', '8 MiB', '120 seconds', 'left channel', '4 seconds']) expect(asrContent).toContain(text)
    expect(asr.sections.find(section => section.id === 'asr-parameters')!.fields!.map(field => field.name)).toEqual(['file'])
    const tts = resolveDocsPage(pages, 'deepseek-tts')
    const ttsContent = JSON.stringify(tts)
    for (const text of ['10,000', '64 KiB', '30 minutes', 'stella', 'tide', 'audio/wav', '24 kHz', 'Chinese character counts as 2', 'does not support WebSocket', 'interrupted response as a failure']) expect(ttsContent).toContain(text)
    const request = tts.sections.find(section => section.id === 'tts-parameters')!.examples!
    expect(request.curl).toContain('--output speech.wav')
    expect(request.python).toContain('response.iter_content(')
    expect(request.node).toContain('await pipeline(')
    expect(JSON.stringify(resolveDocsPage(getApiDocs('zh', 'https://example.test'), 'deepseek-tts'))).toContain('不支持 WebSocket')
  })

  it('documents OCR keepalives, body errors, coordinates, billing and exact multipart fields', () => {
    const page = resolveDocsPage(getApiDocs('en', 'https://example.test'), 'deepseek-ocr')
    const content = JSON.stringify(page)
    for (const text of ['100 MiB', '1,000 pages', '180 seconds', 'leading JSON whitespace', 'HTTP status is already 200', '<｜ref｜>', '<｜box｜>', '0–1000', 'page_count', 'disconnect does not cancel', 'not charged']) expect(content).toContain(text)
    const request = page.sections.find(section => section.id === 'ocr-parameters')!
    expect(request.fields!.map(field => [field.name, field.required])).toEqual([['file', true], ['purpose', true]])
    expect(request.examples!.curl).toContain('-F "purpose=text_recognition"')
    expect(request.examples!.python).toContain('if result.get("error")')
    expect(request.examples!.node).toContain('if (result.error)')
    const error = page.sections.find(section => section.id === 'ocr-errors')!.examples!.json!
    expect(JSON.parse(error)).toMatchObject({ error: { param: null, code: 'UPSTREAM_ERROR_CODE' } })
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

  describe('key pages', () => {
    const origin = 'https://example.test'
    const pageOf = (locale: string, id: string) => resolveDocsPage(getApiDocs(locale, origin), id)

    it('publishes the self-service and console key pages in both languages under their own category', () => {
      for (const locale of ['zh', 'en']) {
        for (const id of ['key-self-service', 'manage-keys']) {
          const page = pageOf(locale, id)
          expect(page.id, `${locale}:${id}`).toBe(id)
          expect(page.category).toBe('account')
          expect(page.sections.length).toBeGreaterThan(3)
        }
      }
      expect(pageOf('zh', 'key-self-service').title).not.toBe(pageOf('en', 'key-self-service').title)
    })

    it('keeps the key pages ahead of the API reference and behind the client setup pages', () => {
      const ids = getApiDocs('en', origin).map(page => page.id)
      expect(ids.indexOf('claude-code')).toBeLessThan(ids.indexOf('key-self-service'))
      expect(ids.indexOf('manage-keys')).toBeLessThan(ids.indexOf('chat-completions'))
    })

    it('documents the four key endpoints with runnable examples that use the site origin and send only the key', () => {
      const page = pageOf('en', 'key-self-service')
      const endpoints = page.sections.filter(section => section.endpoint).map(section => `${section.endpoint!.method} ${section.endpoint!.path}`)
      expect(endpoints).toEqual(['GET /v1/sub2api/key', 'GET /v1/sub2api/groups', 'GET /v1/sub2api/usage', 'GET /v1/sub2api/account'])
      for (const section of page.sections.filter(entry => entry.endpoint)) expect(section.endpoint!.auth).toBe('apiKey')

      const overview = page.sections[0].examples!
      expect(Object.keys(overview)).toEqual(['curl', 'python', 'node'])
      expect(overview.curl!.split('\n')).toEqual([`curl ${origin}/v1/sub2api/key \\`, '  -H "Authorization: Bearer $API_KEY"'])
      expect(overview.python).toContain(`requests.get(\n    "${origin}/v1/sub2api/key"`)
      expect(overview.node).toContain(`fetch("${origin}/v1/sub2api/key"`)
      for (const snippet of Object.values(overview)) {
        expect(snippet).not.toContain('/v1/v1')
        expect(snippet).not.toMatch(/sk-[A-Za-z0-9]{8,}/)
      }
      const usage = page.sections.find(section => section.id === 'key-info-usage')!.examples!
      expect(usage.curl).toContain('/v1/sub2api/usage?start_date=2026-11-01&end_date=2026-11-07&daily=1')

      // The account endpoint has the same three runnable requests, plus the answer it gives.
      const account = page.sections.find(section => section.id === 'key-info-account')!.examples!
      expect(Object.keys(account)).toEqual(['curl', 'python', 'node', 'json'])
      expect(account.curl!.split('\n')).toEqual([`curl ${origin}/v1/sub2api/account \\`, '  -H "Authorization: Bearer $API_KEY"'])
      expect(account.python).toContain(`requests.get(\n    "${origin}/v1/sub2api/account"`)
      expect(account.node).toContain(`fetch("${origin}/v1/sub2api/account"`)
      for (const snippet of [account.curl!, account.python!, account.node!, account.json!]) {
        expect(snippet).not.toContain('/v1/v1')
        expect(snippet).not.toMatch(/sk-[A-Za-z0-9]{8,}/)
      }
    })

    it('documents the account endpoint field by field in both languages, and keeps the example in step with it', () => {
      for (const locale of ['zh', 'en']) {
        const section = pageOf(locale, 'key-self-service').sections.find(entry => entry.id === 'key-info-account')!
        expect(section.fields!.map(field => field.name), locale).toEqual(['balance.wallet', 'balance.frozen', 'subscriptions', 'today', 'timezone', 'partial'])
        for (const field of section.fields!) expect(field.description.length, `${locale}:${field.name}`).toBeGreaterThan(10)

        // The example is what the endpoint answers with: the same keys, in the order the handler writes them.
        const example = JSON.parse(section.examples!.json!)
        expect(Object.keys(example), locale).toEqual(['object', 'schema_version', 'timezone', 'balance', 'subscriptions', 'today', 'observed_at'])
        expect(example.object).toBe('sub2api.account')
        expect(Object.keys(example.balance)).toEqual(['wallet', 'frozen', 'unit'])
        expect(Object.keys(example.subscriptions[0])).toEqual(['group_id', 'group_name', 'expires_at', 'remaining', 'unlimited'])
        expect(Object.keys(example.today)).toEqual(['label', 'start', 'end', 'requests', 'input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens', 'total_tokens', 'cost', 'actual_cost'])
        const tokens = example.today
        expect(tokens.total_tokens).toBe(tokens.input_tokens + tokens.output_tokens + tokens.cache_read_tokens + tokens.cache_write_tokens)
      }
      expect(pageOf('en', 'key-self-service').description).toContain('account balance')
      expect(pageOf('zh', 'key-self-service').description).toContain('账号余额')
    })

    it('tells the reader that holding the key is enough to read the account, in both languages', () => {
      const warn = (locale: string) => pageOf(locale, 'key-self-service').sections.find(section => section.id === 'key-info-account')!.paragraphs!.join(' ')
      expect(warn('en')).toContain('Anyone holding this key can read them')
      expect(warn('zh')).toContain('持有这把密钥的人就能读到这些数字')
    })

    it('names the credential each console endpoint needs, and it is the login token', () => {
      const page = pageOf('en', 'manage-keys')
      const withEndpoint = page.sections.filter(section => section.endpoint)
      expect(withEndpoint.length).toBeGreaterThanOrEqual(4)
      for (const section of withEndpoint) {
        expect(section.endpoint!.auth).toBe('session')
        expect(section.endpoint!.path.startsWith('/api/v1/')).toBe(true)
      }
      const auth = JSON.stringify(page.sections[0])
      expect(auth).toContain('access_token')
      expect(auth).toContain('refresh_token')
      expect(auth).toContain('cannot call them')
      expect(auth).toContain('/api/v1/auth/refresh')
    })

    it('says plainly that an API key cannot call the console API, in both languages', () => {
      expect(JSON.stringify(pageOf('zh', 'manage-keys').sections[0])).toContain('API 密钥不能调用它们')
      expect(JSON.stringify(pageOf('en', 'manage-keys').sections[0])).toContain('An API key cannot call them')
    })

    it('gives anchors that are unique inside each page and links that stay inside the docs', () => {
      const all = getApiDocs('en', origin)
      const ids = new Set(all.map(page => page.id))
      for (const id of ['key-self-service', 'manage-keys']) {
        const page = resolveDocsPage(all, id)
        expect(new Set(page.sections.map(section => section.id)).size).toBe(page.sections.length)
        for (const section of page.sections) for (const link of section.links || []) if (link.page) expect(ids.has(link.page)).toBe(true)
      }
    })

    it('documents the balance on the account endpoint only, and nothing the endpoints are built to hold back', () => {
      const text = JSON.stringify([pageOf('en', 'key-self-service'), pageOf('zh', 'key-self-service')])
      // It may say these are NOT returned; it must not present them as response fields.
      for (const field of ['"name":"account_cost"', '"name":"last_used_ip"', '"name":"ip_whitelist"', '"name":"key"', '"name":"email"', '"name":"username"', '"name":"user_id"', '"name":"balance"']) {
        expect(text).not.toContain(field)
      }
      for (const locale of ['zh', 'en']) {
        // The wallet is a field of exactly one section; the three key-scoped endpoints still carry none.
        const withBalance = pageOf(locale, 'key-self-service').sections.filter(section => (section.fields || []).some(field => /balance|wallet/.test(field.name)))
        expect(withBalance.map(section => section.id), locale).toEqual(['key-info-account'])
      }
      for (const id of ['key-info-key', 'key-info-groups', 'key-info-usage']) {
        const examples = pageOf('en', 'key-self-service').sections.find(section => section.id === id)!.examples!
        expect(JSON.stringify(examples), id).not.toMatch(/balance|wallet/)
      }
      const example = pageOf('en', 'key-self-service').sections.find(section => section.id === 'key-info-key')!.examples!.json!
      expect(Object.keys(JSON.parse(example))).not.toEqual(expect.arrayContaining(['key', 'balance', 'ip_whitelist', 'last_used_ip']))
    })

    it('no longer says the balance is held back, and still names what is', () => {
      const overview = (locale: string) => JSON.stringify(pageOf(locale, 'key-self-service').sections[0].paragraphs)
      expect(overview('en')).not.toContain('wallet or subscription balance')
      expect(overview('zh')).not.toContain('钱包或订阅余额')
      for (const text of ['key plaintext', 'IP lists', 'email or username', 'upstream accounts', 'upstream cost']) expect(overview('en')).toContain(text)
      for (const text of ['密钥明文', 'IP 名单', '邮箱与用户名', '上游账号', '上游成本']) expect(overview('zh')).toContain(text)
      expect(overview('en')).toContain('Four read-only endpoints')
      expect(overview('zh')).toContain('四个只读接口')
    })

    it('states the real per-minute budget so the docs and the limiter cannot drift apart silently', () => {
      expect(JSON.stringify(pageOf('en', 'key-self-service'))).toContain('30 times a minute')
      expect(JSON.stringify(pageOf('zh', 'key-self-service'))).toContain('每分钟最多 30 次')
      expect(JSON.stringify(pageOf('en', 'key-self-service'))).toContain('separate counters for the four')
      expect(JSON.stringify(pageOf('zh', 'key-self-service'))).toContain('四个接口各有独立计数')
    })
  })
})
