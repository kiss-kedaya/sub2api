// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { executeCacheRun, newCacheRound, parseCacheUsage, probeCacheRound, reconcileCacheBills } from '../groupCacheTest'
import { DEFAULT_CACHE_SETTINGS, aggregateCacheTokens, billFromUsage, buildCachePrefix, buildCacheRequest, cacheHitRate, exportCacheRun, loadCacheHistory, saveCacheHistory, type CacheProtocol, type CacheTestRun } from '@/utils/groupCacheTest'
import type { AdminUsageLog } from '@/types'

const { listUsage } = vi.hoisted(() => ({ listUsage: vi.fn() }))
vi.mock('@/api/admin/usage', () => ({ list: listUsage }))
const run = (protocol: CacheProtocol = 'responses'): CacheTestRun => ({
  version: 1, id: 'synthetic-run', startedAt: '2026-10-12T00:00:00Z', finishedAt: null,
  groupId: 7, groupName: 'Demo', keyId: 10, keyName: 'Demo key', status: 'running',
  settings: { ...DEFAULT_CACHE_SETTINGS, model: 'test-model', rounds: 2, intervalMs: 0, protocol }, rounds: []
})
const usageLog = (changes: Partial<AdminUsageLog> = {}): AdminUsageLog => ({
  id: 80, request_id: 'client:demo-1', user_id: 1, api_key_id: 10, group_id: 7, account_id: 4,
  model: 'test-model', input_tokens: 100, output_tokens: 2, cache_read_tokens: 800, cache_creation_tokens: 100,
  input_cost: 0.002, output_cost: 0.001, cache_read_cost: 0.001, cache_creation_cost: 0.002,
  total_cost: 0.006, actual_cost: 0.012, rate_multiplier: 2, first_token_ms: 120, duration_ms: 140,
  ...changes
} as AdminUsageLog)
const event = (value: unknown) => 'data: ' + JSON.stringify(value) + '\r\n\r\n'
const completed = (usage: unknown = { input_tokens: 1000, output_tokens: 2, input_tokens_details: { cached_tokens: 800, cache_write_tokens: 100 } }) =>
  event({ type: 'response.output_text.delta', delta: '好' }) + event({ type: 'response.completed', response: { status: 'completed', model: 'test-model', service_tier: 'default', usage } })
function sse(body: string, id: string | null = 'demo-1', chunkSize = 11) {
  const bytes = new TextEncoder().encode(body)
  const stream = new ReadableStream({
    start(controller) {
      for (let i = 0; i < bytes.length; i += chunkSize) controller.enqueue(bytes.slice(i, i + chunkSize))
      controller.close()
    }
  })
  return new Response(stream, { headers: { 'Content-Type': 'text/event-stream', ...(id ? { 'X-Client-Request-ID': id } : {}) } })
}
const signal = () => new AbortController().signal
let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => { fetchMock = vi.fn(); vi.stubGlobal('fetch', fetchMock); listUsage.mockReset(); localStorage.clear() })
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

describe('group cache request contract', () => {
  it.each<CacheProtocol>(['responses', 'chat', 'messages'])('keeps the %s prefix identical within a run and isolates new runs', protocol => {
    const test = run(protocol), prefix = buildCachePrefix(test.id, test.settings.prefixBlocks)
    const first = buildCacheRequest(test.settings, prefix, 1), repeat = buildCacheRequest(test.settings, prefix, 2)
    expect(JSON.stringify(first)).not.toEqual(JSON.stringify(repeat))
    expect(JSON.stringify(first)).toContain(JSON.stringify(prefix).slice(1, -1))
    expect(JSON.stringify(repeat)).toContain(JSON.stringify(prefix).slice(1, -1))
    expect(prefix).not.toEqual(buildCachePrefix('another-run', test.settings.prefixBlocks))
    if (protocol === 'messages') expect(JSON.stringify(repeat)).toContain('"cache_control":{"type":"ephemeral"}')
    expect(first.model).toBe(repeat.model)
  })
  it('normalizes input buckets without double-counting cached tokens', () => {
    const raw = parseCacheUsage('responses', { input_tokens: 1000, output_tokens: 2, input_tokens_details: { cached_tokens: 800, cache_write_tokens: 100 } })
    const bill = billFromUsage(usageLog())
    expect(raw).toEqual({ input: 1000, output: 2, read: 800, write: 100 })
    expect(bill.tokens).toEqual(raw)
    expect(cacheHitRate(raw)).toBe(0.8)
    const start = parseCacheUsage('messages', { input_tokens: 100, cache_read_input_tokens: 800, cache_creation_input_tokens: 100, output_tokens: 1 })
    expect(parseCacheUsage('messages', { output_tokens: 2 }, start)).toEqual(raw)
    expect(parseCacheUsage('chat', { prompt_tokens: 1000, completion_tokens: 2, prompt_tokens_details: { cached_tokens: 800, cache_write_tokens: 100 } })).toEqual(raw)
  })
  it('does not infer zero for absent cache telemetry; aggregates by token weight', () => {
    expect(cacheHitRate(parseCacheUsage('responses', { input_tokens: 1000, output_tokens: 2 }))).toBeNull()
    expect(cacheHitRate({ input: 0, output: 0, read: 0, write: 0 })).toBeNull()
    expect(cacheHitRate({ input: 10, output: 0, read: 11, write: 0 })).toBeNull()
    const first = { ...newCacheRound(1), status: 'completed' as const, tokens: { input: 100, output: 1, read: 100, write: 0 } }
    const second = { ...newCacheRound(2), status: 'completed' as const, tokens: { input: 900, output: 1, read: 0, write: 0 } }
    expect(cacheHitRate(aggregateCacheTokens([first, second]))).toBe(0.1)
    expect(aggregateCacheTokens([{ ...first, status: 'failed' }]).read).toBeNull()
  })
})

describe('streamed probes', () => {
  it('handles split UTF-8 and CRLF, correlates the returned header, and never sends the panel JWT', async () => {
    localStorage.setItem('auth_token', 'PANEL_JWT_MUST_NOT_LEAK')
    fetchMock.mockResolvedValue(sse(': keepalive\r\n\r\n' + completed(), 'demo-1', 1))
    const test = run(), round = newCacheRound(1)
    await probeCacheRound(test, round, 'TEST_KEY', 'prefix', signal())
    expect(round.status).toBe('completed')
    expect(round.requestId).toBe('client:demo-1')
    expect(round.firstTextMs).not.toBeNull()
    expect(round.tokens?.read).toBe(800)
    expect(round.serviceTier).toBe('default')
    const [url, options] = fetchMock.mock.calls[0]
    expect(url).toMatch(/\/v1\/responses$/)
    expect(options.credentials).toBe('omit')
    expect(options.redirect).toBe('error')
    expect(options.headers.Authorization).toBe('Bearer TEST_KEY')
    expect(JSON.stringify(options)).not.toContain('PANEL_JWT')
    expect(options.headers['X-Client-Request-ID']).toBeUndefined()
  })
  it('reads Chat usage after finish_reason and requires DONE', async () => {
    const body = event({ model: 'chat-model', choices: [{ delta: { content: 'OK' }, finish_reason: null }] }) +
      event({ choices: [{ delta: {}, finish_reason: 'stop' }] }) +
      event({ choices: [], usage: { prompt_tokens: 10, completion_tokens: 1, prompt_tokens_details: { cached_tokens: 8 } } }) +
      'data: [DONE]\n\n'
    fetchMock.mockResolvedValue(sse(body))
    const round = newCacheRound(1)
    await probeCacheRound(run('chat'), round, 'TEST_KEY', 'prefix', signal())
    expect(round.status).toBe('completed')
    expect(round.tokens).toEqual({ input: 10, output: 1, read: 8, write: null })
  })
  it('retains trailing Chat usage even when the output is truncated', async () => {
    const body = event({ choices: [{ delta: {}, finish_reason: 'length' }] }) +
      event({ choices: [], usage: { prompt_tokens: 100, completion_tokens: 256, prompt_tokens_details: { cached_tokens: 80 } } }) +
      'data: [DONE]\n\n'
    fetchMock.mockResolvedValue(sse(body))
    const round = newCacheRound(1)
    await probeCacheRound(run('chat'), round, 'TEST_KEY', 'prefix', signal())
    expect(round.error).toBe('incomplete')
    expect(round.status).toBe('failed')
    expect(round.tokens?.read).toBe(80)
  })
  it('merges cumulative Messages usage and uses key headers', async () => {
    const body = event({ type: 'message_start', message: { model: 'messages-model', usage: { input_tokens: 10, output_tokens: 1, cache_read_input_tokens: 90, cache_creation_input_tokens: 0 } } }) +
      event({ type: 'content_block_delta', delta: { text: 'OK' } }) +
      event({ type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 2 } }) + event({ type: 'message_stop' })
    fetchMock.mockResolvedValue(sse(body))
    const round = newCacheRound(1)
    await probeCacheRound(run('messages'), round, 'TEST_KEY', 'prefix', signal())
    expect(round.status).toBe('completed')
    expect(round.tokens).toEqual({ input: 100, output: 2, read: 90, write: 0 })
    expect(fetchMock.mock.calls[0][1].headers).toMatchObject({ 'x-api-key': 'TEST_KEY', 'anthropic-version': '2023-06-01' })
    expect(fetchMock.mock.calls[0][1].headers.Authorization).toBeUndefined()
  })
  it.each([
    ['data: {bad}\n\n', 'invalid_stream'],
    [event({ type: 'response.output_text.delta', delta: 'partial' }), 'incomplete'],
    [event({ type: 'response.failed', response: { error: { message: 'SECRET' } } }), 'http'],
    [event({ type: 'response.incomplete', response: { usage: { input_tokens: 100, output_tokens: 256 } } }), 'incomplete']
  ])('stops after an invalid, failed, or truncated stream', async (body, error) => {
    fetchMock.mockResolvedValue(sse(body))
    const test = run()
    await executeCacheRun(test, 'TEST_KEY', signal(), vi.fn())
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(test.status).toBe('failed')
    expect(test.rounds[0].error).toBe(error)
    expect(exportCacheRun(test)).not.toContain('SECRET')
  })
  it('rejects HTTP failure and a JSON response to a streaming request', async () => {
    fetchMock.mockResolvedValueOnce(new Response('SECRET', { status: 429 }))
    const first = newCacheRound(1)
    await probeCacheRound(run(), first, 'TEST_KEY', 'prefix', signal())
    expect(first.error).toBe('http')
    expect(first.httpStatus).toBe(429)
    fetchMock.mockResolvedValueOnce(new Response('{}', { headers: { 'content-type': 'application/json' } }))
    const second = newCacheRound(2)
    await probeCacheRound(run(), second, 'TEST_KEY', 'prefix', signal())
    expect(second.error).toBe('invalid_stream')
  })
  it('preserves usage on a failed Responses terminal event', async () => {
    fetchMock.mockResolvedValue(sse(event({ type: 'response.failed', response: {
      model: 'test-model', usage: { input_tokens: 100, output_tokens: 2, input_tokens_details: { cached_tokens: 80 } }
    } })))
    const round = newCacheRound(1)
    await probeCacheRound(run(), round, 'TEST_KEY', 'prefix', signal())
    expect(round.status).toBe('failed')
    expect(round.tokens?.read).toBe(80)
    expect(round.requestId).toBe('client:demo-1')
    expect(fetchMock.mock.calls[0][1].signal.aborted).toBe(true)
  })
  it('shows unknown usage and unavailable correlation truthfully', async () => {
    fetchMock.mockResolvedValue(sse(completed(null), null))
    const round = newCacheRound(1)
    await probeCacheRound(run(), round, 'TEST_KEY', 'prefix', signal())
    expect(round.status).toBe('completed')
    expect(round.tokens).toBeNull()
    expect(round.billingState).toBe('unavailable')
  })
  it('cancels during a stream without launching subsequent rounds', async () => {
    let streamCanceled = false
    fetchMock.mockResolvedValue(new Response(new ReadableStream({ cancel() { streamCanceled = true } }), { headers: { 'Content-Type': 'text/event-stream' } }))
    const test = run(), controller = new AbortController()
    const pending = executeCacheRun(test, 'TEST_KEY', controller.signal, vi.fn())
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    controller.abort()
    await pending
    expect(test.status).toBe('stopped')
    expect(test.rounds[0].error).toBe('aborted')
    expect(streamCanceled).toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('times out an idle stream and stops later requests', async () => {
    vi.useFakeTimers()
    fetchMock.mockResolvedValue(new Response(new ReadableStream(), { headers: { 'Content-Type': 'text/event-stream' } }))
    const test = run()
    const pending = executeCacheRun(test, 'TEST_KEY', signal(), vi.fn())
    await vi.advanceTimersByTimeAsync(test.settings.timeoutMs)
    await pending
    expect(test.rounds[0].error).toBe('timeout')
    expect(test.status).toBe('failed')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('finishes all rounds sequentially without changing the common prefix', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(sse(completed())))
    const test = run()
    await executeCacheRun(test, 'TEST_KEY', signal(), vi.fn())
    expect(test.status).toBe('completed')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const bodies = fetchMock.mock.calls.map(([, options]) => JSON.parse(options.body))
    const prefix = buildCachePrefix(test.id, test.settings.prefixBlocks)
    expect(bodies[0].input[0].content).toContain(prefix)
    expect(bodies[1].input[0].content).toContain(prefix)
    expect(bodies[0].input[0].content).not.toBe(bodies[1].input[0].content)
  })
  it('executes sequential rounds with identical prefixes and stops during the interval', async () => {
    vi.useFakeTimers()
    fetchMock.mockImplementation(() => Promise.resolve(sse(completed())))
    const test = run(); test.settings.intervalMs = 1000
    const controller = new AbortController(), changed = vi.fn()
    const pending = executeCacheRun(test, 'TEST_KEY', controller.signal, changed)
    await vi.advanceTimersByTimeAsync(10)
    expect(test.rounds[0].status).toBe('completed')
    controller.abort()
    await pending
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(test.status).toBe('stopped')
  })
})

describe('billing and history', () => {
  it('matches only the exact request ID and key/user; retries reads but never the probe', async () => {
    vi.useFakeTimers()
    const test = run(); test.rounds = [{ ...newCacheRound(1), requestId: 'client:demo-1', status: 'completed' }]
    listUsage.mockResolvedValueOnce({ items: [usageLog({ request_id: 'client:unrelated' }), usageLog({ api_key_id: 11 })] })
      .mockResolvedValueOnce({ items: [usageLog()] })
    const pending = reconcileCacheBills(test, 1, signal(), vi.fn())
    await vi.runAllTimersAsync(); await pending
    expect(listUsage).toHaveBeenCalledTimes(2)
    expect(listUsage.mock.calls[0][0]).toMatchObject({ request_id: 'client:demo-1', api_key_id: 10, user_id: 1 })
    expect(test.rounds[0].bill?.actualCost).toBe(0.012)
    expect(test.rounds[0].billingState).toBe('matched')
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it('keeps missing bills pending and a lookup error distinct from zero cost', async () => {
    vi.useFakeTimers()
    const test = run(); test.rounds = [{ ...newCacheRound(1), requestId: 'client:demo-1', status: 'completed' }]
    listUsage.mockResolvedValue({ items: [] })
    const pending = reconcileCacheBills(test, 1, signal(), vi.fn())
    await vi.runAllTimersAsync(); await pending
    expect(listUsage).toHaveBeenCalledTimes(4)
    expect(test.rounds[0].billingState).toBe('pending')
    expect(test.rounds[0].bill).toBeNull()
    listUsage.mockRejectedValue(new Error('offline'))
    const retry = reconcileCacheBills(test, 1, signal(), vi.fn())
    await vi.runAllTimersAsync(); await retry
    expect(test.rounds[0].billingState).toBe('error')
    expect(test.rounds[0].bill).toBeNull()
  })
  it('never writes secrets or nested usage objects and scopes history to a user', () => {
    const test = run()
    Object.assign(test, { key: 'SECRET', response: 'SECRET' })
    Object.assign(test.settings, { apiKey: 'SECRET' })
    const round = newCacheRound(1)
    Object.assign(round, { rawResponse: 'SECRET', key: 'SECRET' })
    round.bill = billFromUsage(usageLog())
    Object.assign(round.bill, { api_key: { key: 'SECRET' } })
    test.rounds = [round]
    expect(exportCacheRun(test)).not.toContain('SECRET')
    expect(saveCacheHistory(1, [test])).toBe(true)
    expect(loadCacheHistory(2)).toEqual([])
    expect(loadCacheHistory(1)[0].status).toBe('stopped')
    expect(loadCacheHistory(1)[0].rounds[0].status).toBe('stopped')
    expect(JSON.stringify(loadCacheHistory(1))).not.toContain('SECRET')
  })
  it('rejects malformed nested billing data in otherwise valid history', () => {
    const test = run(); test.rounds = [{ ...newCacheRound(1), bill: billFromUsage(usageLog()) }]
    Object.assign(test.rounds[0].bill!, { actualCost: 'invalid-number' })
    localStorage.setItem('sub2api-group-cache-tests:v1:1', JSON.stringify([test]))
    expect(loadCacheHistory(1)).toEqual([])
  })
  it('tolerates an unavailable browser storage object', () => {
    vi.stubGlobal('localStorage', undefined)
    expect(loadCacheHistory(1)).toEqual([])
    expect(saveCacheHistory(1, [run()])).toBe(false)
  })
  it('handles corrupt history and full storage without breaking results', () => {
    localStorage.setItem('sub2api-group-cache-tests:v1:1', '{bad')
    expect(loadCacheHistory(1)).toEqual([])
    expect(saveCacheHistory(1, [run()], { setItem() { throw new Error('full') } } as unknown as Storage)).toBe(false)
  })
})
