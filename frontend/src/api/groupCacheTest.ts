// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { buildGatewayUrl } from '@/api/url'
import { list as listUsage } from '@/api/admin/usage'
import {
  CACHE_ENDPOINTS, billFromUsage, buildCachePrefix, buildCacheRequest, nonnegative, validCacheSettings,
  type CacheProtocol, type CacheRound, type CacheTestError, type CacheTestRun, type CacheTokens
} from '@/utils/groupCacheTest'

class ProbeError extends Error {
  constructor(public code: CacheTestError) { super(code) }
}
type JSONRecord = Record<string, unknown>
const object = (value: unknown): JSONRecord => value && typeof value === 'object' && !Array.isArray(value) ? value as JSONRecord : {}
const text = (value: unknown): string | null => typeof value === 'string' && value.length <= 200 ? value : null

export function parseCacheUsage(protocol: CacheProtocol, value: unknown, previous: CacheTokens | null = null): CacheTokens | null {
  const usage = object(value)
  if (!Object.keys(usage).length) return previous
  if (protocol === 'messages') {
    const input = nonnegative(usage.input_tokens)
    const read = nonnegative(usage.cache_read_input_tokens) ?? previous?.read ?? null
    const write = nonnegative(usage.cache_creation_input_tokens) ?? previous?.write ?? null
    return {
      input: input === null ? previous?.input ?? null : input + (read ?? 0) + (write ?? 0),
      output: nonnegative(usage.output_tokens) ?? previous?.output ?? null, read, write
    }
  }
  const chat = protocol === 'chat'
  return {
    input: nonnegative(usage[chat ? 'prompt_tokens' : 'input_tokens']),
    output: nonnegative(usage[chat ? 'completion_tokens' : 'output_tokens']),
    read: nonnegative(object(usage[chat ? 'prompt_tokens_details' : 'input_tokens_details']).cached_tokens),
    write: nonnegative(object(usage[chat ? 'prompt_tokens_details' : 'input_tokens_details']).cache_write_tokens)
  }
}

/** Parse SSE incrementally, including CRLF, split UTF-8, comments, and multi-line data. */
async function consumeSSE(response: Response, onData: (value: unknown) => boolean, signal: AbortSignal): Promise<void> {
  if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new ProbeError('invalid_stream')
  const reader = response.body.getReader(), decoder = new TextDecoder()
  let buffer = '', data: string[] = [], done = false
  const dispatch = () => {
    if (!data.length) return
    const payload = data.join('\n')
    data = []
    if (payload === '[DONE]') { done = onData('[DONE]'); return }
    let parsed: unknown
    try { parsed = JSON.parse(payload) } catch { throw new ProbeError('invalid_stream') }
    done = onData(parsed)
  }
  const line = (raw: string) => {
    const value = raw.endsWith('\r') ? raw.slice(0, -1) : raw
    if (!value) dispatch()
    else if (value.startsWith('data:')) data.push(value.slice(5).replace(/^ /, ''))
  }
  const cancel = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', cancel, { once: true })
  try {
    while (!done) {
      if (signal.aborted) throw new ProbeError('aborted')
      const chunk = await reader.read()
      buffer += decoder.decode(chunk.value, { stream: !chunk.done })
      if (buffer.length + data.reduce((n, d) => n + d.length, 0) > 2_000_000) throw new ProbeError('invalid_stream')
      let end: number
      while (!done && (end = buffer.indexOf('\n')) >= 0) {
        line(buffer.slice(0, end)); buffer = buffer.slice(end + 1)
      }
      if (chunk.done) {
        if (buffer && !done) line(buffer)
        if (!done) dispatch()
        break
      }
    }
    if (signal.aborted) throw new ProbeError('aborted')
    if (!done) throw new ProbeError('incomplete')
  } finally {
    signal.removeEventListener('abort', cancel)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export function newCacheRound(index: number): CacheRound {
  return {
    index, startedAt: new Date().toISOString(), status: 'running', requestId: null, tokens: null,
    responseModel: null, serviceTier: null, firstTextMs: null, durationMs: null, error: null,
    httpStatus: null, bill: null, billingState: 'pending'
  }
}

export async function probeCacheRound(run: CacheTestRun, round: CacheRound, key: string, prefix: string, signal: AbortSignal): Promise<void> {
  const controller = new AbortController()
  const abort = () => controller.abort()
  signal.addEventListener('abort', abort, { once: true })
  if (signal.aborted) controller.abort()
  let timedOut = false, finishedChoice = false, messageStarted = false, truncated = false
  const timer = setTimeout(() => { timedOut = true; controller.abort() }, run.settings.timeoutMs)
  const started = performance.now()
  const markText = (value: unknown) => {
    if (typeof value === 'string' && value.length > 0 && round.firstTextMs === null) round.firstTextMs = Math.round(performance.now() - started)
  }
  try {
    const protocol = run.settings.protocol
    const headers: Record<string, string> = { 'Content-Type': 'application/json', Accept: 'text/event-stream' }
    if (protocol === 'messages') { headers['x-api-key'] = key; headers['anthropic-version'] = '2023-06-01' }
    else headers.Authorization = 'Bearer ' + key
    const response = await fetch(buildGatewayUrl(CACHE_ENDPOINTS[protocol]), {
      method: 'POST', headers, body: JSON.stringify(buildCacheRequest(run.settings, prefix, round.index)),
      signal: controller.signal, credentials: 'omit', cache: 'no-store', redirect: 'error'
    })
    round.httpStatus = response.status
    const correlation = response.headers.get('X-Client-Request-ID')
    if (correlation && /^[a-zA-Z0-9._:-]{1,200}$/.test(correlation)) round.requestId = 'client:' + correlation
    if (!response.ok) { await response.body?.cancel(); throw new ProbeError('http') }
    await consumeSSE(response, value => {
      if (value === '[DONE]') {
        if (protocol !== 'chat' || !finishedChoice || truncated) throw new ProbeError('incomplete')
        return true
      }
      const event = object(value)
      if (event.error || event.type === 'error') throw new ProbeError('http')
      if (protocol === 'responses') {
        const responseData = object(event.response)
        round.responseModel = text(responseData.model) ?? round.responseModel
        round.serviceTier = text(responseData.service_tier) ?? round.serviceTier
        if (event.type === 'response.output_text.delta') markText(event.delta)
        if (responseData.usage) round.tokens = parseCacheUsage(protocol, responseData.usage, round.tokens)
        if (event.type === 'response.failed') throw new ProbeError('http')
        if (event.type === 'response.incomplete') throw new ProbeError('incomplete')
        return event.type === 'response.completed' && responseData.status === 'completed'
      }
      if (protocol === 'messages') {
        const message = object(event.message)
        if (event.type === 'message_start') {
          messageStarted = true
          round.responseModel = text(message.model)
          round.tokens = parseCacheUsage(protocol, message.usage, round.tokens)
        }
        if (event.type === 'content_block_delta') markText(object(event.delta).text)
        if (event.type === 'message_delta') {
          round.tokens = parseCacheUsage(protocol, event.usage, round.tokens)
          const reason = object(event.delta).stop_reason
          if (reason === 'max_tokens') throw new ProbeError('incomplete')
          finishedChoice = typeof reason === 'string' && reason.length > 0
        }
        return event.type === 'message_stop' && messageStarted && finishedChoice
      }
      round.responseModel = text(event.model) ?? round.responseModel
      round.serviceTier = text(event.service_tier) ?? round.serviceTier
      if (event.usage) round.tokens = parseCacheUsage(protocol, event.usage, round.tokens)
      if (Array.isArray(event.choices)) for (const choice of event.choices) {
        const item = object(choice)
        markText(object(item.delta).content)
        if (item.finish_reason === 'length' || item.finish_reason === 'content_filter') truncated = true
        if (typeof item.finish_reason === 'string' && item.finish_reason) finishedChoice = true
      }
      return false
    }, controller.signal)
    round.status = 'completed'
  } catch (error) {
    round.error = timedOut ? 'timeout' : signal.aborted ? 'aborted' : error instanceof ProbeError ? error.code : 'network'
    round.status = signal.aborted ? 'stopped' : 'failed'
  } finally {
    round.durationMs = Math.round(performance.now() - started)
    controller.abort()
    if (!round.requestId) round.billingState = 'unavailable'
    clearTimeout(timer)
    signal.removeEventListener('abort', abort)
  }
}

export async function abortableDelay(ms: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) throw new ProbeError('aborted')
  if (!ms) return
  await new Promise<void>((resolve, reject) => {
    const abort = () => { clearTimeout(timer); signal.removeEventListener('abort', abort); reject(new ProbeError('aborted')) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, ms)
    signal.addEventListener('abort', abort, { once: true })
  })
}

/** Exactly one paid request per round. Never retry a failed or ambiguous probe. */
export async function executeCacheRun(run: CacheTestRun, key: string, signal: AbortSignal, changed: () => void): Promise<void> {
  if (!validCacheSettings(run.settings) || run.rounds.length || !key) throw new Error('Invalid cache test')
  const prefix = buildCachePrefix(run.id, run.settings.prefixBlocks)
  try {
    for (let index = 1; index <= run.settings.rounds; index++) {
      if (signal.aborted) break
      if (index > 1) await abortableDelay(run.settings.intervalMs, signal)
      if (signal.aborted) break
      run.rounds.push(newCacheRound(index))
      changed()
      const round = run.rounds[run.rounds.length - 1]
      await probeCacheRound(run, round, key, prefix, signal)
      changed()
      if (round.status !== 'completed') break
    }
  } catch (error) {
    if (!signal.aborted) throw error
  } finally {
    run.status = signal.aborted ? 'stopped' : run.rounds.length === run.settings.rounds && run.rounds.every(r => r.status === 'completed') ? 'completed' : 'failed'
    run.finishedAt = new Date().toISOString()
    changed()
  }
}

/** Read-only reconciliation: a missing bill never causes a repeated model request. */
export async function reconcileCacheBills(run: CacheTestRun, userId: number, signal: AbortSignal, changed: () => void): Promise<void> {
  for (const delay of [0, 1000, 2000, 4000]) {
    if (signal.aborted) return
    try { await abortableDelay(delay, signal) } catch { return }
    if (signal.aborted) return
    const pending = run.rounds.filter(r => r.requestId && !r.bill)
    if (!pending.length) return
    await Promise.all(pending.map(async round => {
      const controller = new AbortController(), abort = () => controller.abort()
      signal.addEventListener('abort', abort, { once: true })
      const timer = setTimeout(abort, 10000)
      try {
        const result = await listUsage({ page: 1, page_size: 10, request_id: round.requestId!, api_key_id: run.keyId, user_id: userId }, { signal: controller.signal })
        if (signal.aborted) return
        const log = result.items.find(item => item.request_id === round.requestId && item.api_key_id === run.keyId && item.user_id === userId)
        if (log) { round.bill = billFromUsage(log); round.billingState = 'matched' }
        else round.billingState = 'pending'
      } catch { if (!signal.aborted) round.billingState = 'error' }
      finally { clearTimeout(timer); signal.removeEventListener('abort', abort) }
    }))
    changed()
  }
}
