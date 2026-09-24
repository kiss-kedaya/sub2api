import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  buildAccountModelTestBody,
  consumeSSEBuffer,
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
})
