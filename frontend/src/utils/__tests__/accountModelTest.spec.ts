import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  applyAccountModelTestEvent,
  buildAccountModelTestBody,
  consumeSSEBuffer,
  createAccountModelTestOutput,
  formatAccountModelTestDuration,
  groupJobsByAccount,
  inferGrokTestMode,
  isMediaHeavyModel,
  parseSSEDataLine,
  resolveBatchTestAccounts,
  runWithConcurrency,
  streamAccountModelTest
} from '../accountModelTest'

describe('accountModelTest helpers', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('flags media-heavy model ids', () => {
    expect(isMediaHeavyModel('gemini-2.5-flash-image')).toBe(true)
    expect(isMediaHeavyModel('grok-imagine-video')).toBe(true)
    expect(isMediaHeavyModel('whisper-large-v3')).toBe(true)
    expect(isMediaHeavyModel('gpt-5.4')).toBe(false)
    expect(isMediaHeavyModel('claude-sonnet-4')).toBe(false)
  })

  it('infers grok test mode from model id', () => {
    expect(inferGrokTestMode('grok-4.3')).toBe('text')
    expect(inferGrokTestMode('grok-imagine-image')).toBe('image')
    expect(inferGrokTestMode('grok-imagine-video')).toBe('video')
  })

  it('builds openai and grok request bodies', () => {
    expect(buildAccountModelTestBody({ modelId: 'gpt-5.4', platform: 'openai' })).toEqual({
      model_id: 'gpt-5.4',
      prompt: '',
      mode: 'default'
    })
    expect(buildAccountModelTestBody({ modelId: 'grok-imagine-video' })).toEqual({
      model_id: 'grok-imagine-video',
      prompt: '',
      mode: 'video'
    })
  })

  it('falls back to #id when the selected row is off the current page', () => {
    expect(
      resolveBatchTestAccounts([12, 34], [{ id: 12, name: 'On Page', platform: 'openai', type: 'apikey' }])
    ).toEqual([
      { id: 12, name: 'On Page', platform: 'openai', type: 'apikey' },
      { id: 34, name: '#34', platform: '', type: '' }
    ])
  })

  it('parses SSE data lines and leftover buffers', () => {
    const events: Array<{ type: string }> = []
    const rest = consumeSSEBuffer('', 'data: {"type":"test_start"}\ndata: {"type":"content","text":"hi"}', events.push.bind(events))
    expect(events).toEqual([{ type: 'test_start' }])
    expect(parseSSEDataLine(rest)).toEqual({ type: 'content', text: 'hi' })
  })

  it('groups jobs by first-seen account order', () => {
    expect(
      groupJobsByAccount([
        { accountId: 2, modelId: 'a' },
        { accountId: 1, modelId: 'b' },
        { accountId: 2, modelId: 'c' }
      ])
    ).toEqual([
      [
        { accountId: 2, modelId: 'a' },
        { accountId: 2, modelId: 'c' }
      ],
      [{ accountId: 1, modelId: 'b' }]
    ])
  })

  it('runs workers with a concurrency cap', async () => {
    let current = 0
    let peak = 0
    await runWithConcurrency([1, 2, 3, 4], 2, async () => {
      current += 1
      peak = Math.max(peak, current)
      await Promise.resolve()
      current -= 1
    })
    expect(peak).toBe(2)
  })

  it('streams account model tests through fetch SSE', async () => {
    const encoder = new TextEncoder()
    const chunks = [
      encoder.encode('data: {"type":"test_start","model":"gpt-5.4"}\n'),
      encoder.encode('data: {"type":"test_complete","success":true}\n')
    ]
    let index = 0
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        body: {
          getReader: () => ({
            cancel: vi.fn().mockResolvedValue(undefined),
            releaseLock: vi.fn(),
            read: vi.fn().mockImplementation(async () => {
              if (index < chunks.length) {
                return { done: false, value: chunks[index++] }
              }
              return { done: true, value: undefined }
            })
          })
        }
      })
    )
    Object.defineProperty(globalThis, 'localStorage', {
      value: { getItem: () => 'test-token' },
      configurable: true
    })

    const events: Array<{ type: string }> = []
    await streamAccountModelTest({
      accountId: 42,
      body: { model_id: 'gpt-5.4', prompt: '', mode: 'default' },
      onEvent: (event) => events.push(event)
    })

    expect(events.map((event) => event.type)).toEqual(['test_start', 'test_complete'])
    expect(fetch).toHaveBeenCalledTimes(1)
    const [, request] = (fetch as unknown as { mock: { calls: [string, { body: string }][] } }).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gpt-5.4',
      prompt: '',
      mode: 'default'
    })
  })

  it('collects streamed text, media, and duration labels', () => {
    const state = createAccountModelTestOutput()
    applyAccountModelTestEvent(state, { type: 'content', text: 'hello ' })
    applyAccountModelTestEvent(state, { type: 'content', text: 'world' })
    applyAccountModelTestEvent(state, { type: 'image', image_url: 'data:image/png;base64,QQ==', mime_type: 'image/png' })
    applyAccountModelTestEvent(state, { type: 'test_complete', success: true })
    expect(state.output).toBe('hello world')
    expect(state.success).toBe(true)
    expect(state.images).toEqual([{ url: 'data:image/png;base64,QQ==', mimeType: 'image/png' }])
    expect(formatAccountModelTestDuration(12)).toBe('12ms')
    expect(formatAccountModelTestDuration(1500)).toBe('1.50s')
  })

  it.each(['null', '1', '[]', '{}', '{"type":"content","text":{}}', '{"type":"test_complete","success":"false"}', '[DONE]'])('ignores invalid SSE payload %s', (payload) => {
    expect(parseSSEDataLine('data: ' + payload)).toBeNull()
  })

  it('keeps failure when a completion event follows an error', () => {
    const state = createAccountModelTestOutput()
    applyAccountModelTestEvent(state, { type: 'error', error: 'denied' })
    applyAccountModelTestEvent(state, { type: 'test_complete', success: true })
    expect(state.success).toBe(false)
    expect(state.error).toBe('denied')
  })

  it('decodes split UTF-8, CRLF and a final unterminated event', async () => {
    const bytes = new TextEncoder().encode('data: {"type":"content","text":"你好"}\r\ndata: {"type":"test_complete","success":true}')
    const read = vi.fn()
    for (const byte of bytes) read.mockResolvedValueOnce({ done: false, value: new Uint8Array([byte]) })
    read.mockResolvedValue({ done: true })
    const releaseLock = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, body: { getReader: () => ({ read, releaseLock }) } }))
    const onEvent = vi.fn()
    await streamAccountModelTest({ accountId: 1, body: {}, onEvent })
    expect(onEvent.mock.calls.map(([event]) => event)).toEqual([
      { type: 'content', text: '你好' }, { type: 'test_complete', success: true }
    ])
    expect(releaseLock).toHaveBeenCalledOnce()
  })

  it('rejects truncated streams and releases the reader', async () => {
    const releaseLock = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, body: { getReader: () => ({
      read: vi.fn().mockResolvedValue({ done: true }), releaseLock
    }) } }))
    await expect(streamAccountModelTest({ accountId: 1, body: {}, onEvent: vi.fn() })).rejects.toThrow('before completion')
    expect(releaseLock).toHaveBeenCalledOnce()
  })

  it('preserves structured HTTP error details', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 403, json: async () => ({ message: 'account disabled' }) }))
    await expect(streamAccountModelTest({ accountId: 1, body: {}, onEvent: vi.fn() })).rejects.toThrow('HTTP 403: account disabled')
  })

  it('cancels a pending reader without dispatching late data', async () => {
    let finishRead!: (result: { done: boolean }) => void
    const cancel = vi.fn().mockImplementation(async () => finishRead({ done: true }))
    const releaseLock = vi.fn()
    const read = vi.fn().mockImplementation(() => new Promise((resolve) => { finishRead = resolve }))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, body: { getReader: () => ({ read, cancel, releaseLock }) } }))
    const controller = new AbortController()
    const onEvent = vi.fn()
    const result = streamAccountModelTest({ accountId: 1, body: {}, signal: controller.signal, onEvent })
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' })
    await vi.waitFor(() => expect(read).toHaveBeenCalledOnce())
    controller.abort()
    await rejected
    expect(cancel).toHaveBeenCalledOnce()
    expect(releaseLock).toHaveBeenCalledOnce()
    expect(onEvent).not.toHaveBeenCalled()
  })
})
