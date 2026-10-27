// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import type { AdminUsageLog } from '@/types'

export type CacheProtocol = 'responses' | 'chat' | 'messages'
export type CacheTestError = 'http' | 'network' | 'invalid_stream' | 'incomplete' | 'timeout' | 'aborted'
export interface CacheTestSettings {
  protocol: CacheProtocol
  model: string
  rounds: number
  prefixBlocks: number
  intervalMs: number
  timeoutMs: number
  maxOutputTokens: number
  reasoningEffort: string
  serviceTier: '' | 'default' | 'priority'
}
export interface CacheTokens {
  // Total input includes cache reads and writes for every supported protocol.
  input: number | null
  output: number | null
  read: number | null
  write: number | null
}
export interface CacheBill {
  id: number
  requestId: string
  groupId: number | null
  accountId: number | null
  model: string
  upstreamModel: string | null
  serviceTier: string | null
  reasoningEffort: string | null
  tokens: CacheTokens
  inputCost: number | null
  outputCost: number | null
  readCost: number | null
  writeCost: number | null
  totalCost: number | null
  actualCost: number | null
  multiplier: number | null
  firstTokenMs: number | null
  durationMs: number | null
}
export interface CacheRound {
  index: number
  status: 'running' | 'completed' | 'failed' | 'stopped'
  startedAt: string
  requestId: string | null
  tokens: CacheTokens | null
  responseModel: string | null
  serviceTier: string | null
  firstTextMs: number | null
  durationMs: number | null
  error: CacheTestError | null
  httpStatus: number | null
  bill: CacheBill | null
  billingState: 'pending' | 'matched' | 'unavailable' | 'error'
}
export interface CacheTestRun {
  version: 1
  id: string
  startedAt: string
  finishedAt: string | null
  groupId: number
  groupName: string
  keyId: number
  keyName: string
  settings: CacheTestSettings
  status: 'running' | 'completed' | 'failed' | 'stopped'
  rounds: CacheRound[]
}
export const DEFAULT_CACHE_SETTINGS: CacheTestSettings = {
  protocol: 'responses', model: '', rounds: 3, prefixBlocks: 48, intervalMs: 1000,
  timeoutMs: 120000, maxOutputTokens: 256, reasoningEffort: '', serviceTier: ''
}
export const CACHE_ENDPOINTS: Record<CacheProtocol, string> = {
  responses: '/v1/responses', chat: '/v1/chat/completions', messages: '/v1/messages'
}
const efforts = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']
const boundedInteger = (n: number, min: number, max: number) => Number.isInteger(n) && n >= min && n <= max
export function createCacheTestId(): string {
  // getRandomValues also works on self-hosted HTTP origins where randomUUID is unavailable.
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
}
export function validCacheSettings(s: CacheTestSettings): boolean {
  return ['responses', 'chat', 'messages'].includes(s.protocol) && typeof s.model === 'string' &&
    s.model.trim().length > 0 && s.model.length <= 200 && boundedInteger(s.rounds, 2, 6) &&
    boundedInteger(s.prefixBlocks, 16, 128) && boundedInteger(s.intervalMs, 0, 10000) &&
    boundedInteger(s.timeoutMs, 10000, 300000) && boundedInteger(s.maxOutputTokens, 64, 4096) &&
    efforts.includes(s.reasoningEffort) && ['', 'default', 'priority'].includes(s.serviceTier) &&
    (s.protocol !== 'messages' || (!s.reasoningEffort && !s.serviceTier))
}

// A new marker isolates runs; within a run the early content is byte-identical.
// Synthetic public input only. Character count is deliberately not called tokens.
export function buildCachePrefix(id: string, blocks: number): string {
  const lines = ['Cache experiment ' + id + '. Read the reference notes and follow the instruction at the end.']
  for (let i = 0; i < blocks; i++) {
    lines.push('Reference ' + (i + 1) + ': The observatory keeps a daily ledger of weather, instrument calibration, and maintenance. Each entry records the local date, temperature, wind direction, cloud cover, and the name of the instrument. Reviewers compare measurements against the previous week, mark uncertain observations for another review, and preserve the original values. A missing measurement is recorded as unknown rather than zero. The ledger is a synthetic reference for a repeatable software experiment and contains no personal information.')
  }
  return lines.join('\n')
}
export function buildCacheRequest(s: CacheTestSettings, prefix: string, round: number): Record<string, unknown> {
  const suffix = 'Round ' + round + '. Reply with only CACHE_OK. Do not summarize the reference notes.'
  const base = { model: s.model.trim(), stream: true }
  if (s.protocol === 'messages') return {
    ...base, max_tokens: s.maxOutputTokens, messages: [{ role: 'user', content: [
      { type: 'text', text: prefix, cache_control: { type: 'ephemeral' } },
      { type: 'text', text: suffix }
    ] }]
  }
  const tier = s.serviceTier ? { service_tier: s.serviceTier } : {}
  if (s.protocol === 'responses') return {
    ...base, ...tier, max_output_tokens: s.maxOutputTokens,
    ...(s.reasoningEffort ? { reasoning: { effort: s.reasoningEffort } } : {}),
    input: [{ role: 'user', content: prefix + '\n\n' + suffix }]
  }
  return {
    ...base, ...tier, max_completion_tokens: s.maxOutputTokens,
    ...(s.reasoningEffort ? { reasoning_effort: s.reasoningEffort } : {}),
    stream_options: { include_usage: true }, messages: [{ role: 'user', content: prefix + '\n\n' + suffix }]
  }
}
export function nonnegative(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
}
export function cacheHitRate(tokens: CacheTokens | null): number | null {
  return tokens?.input && tokens.read !== null && tokens.read <= tokens.input ? tokens.read / tokens.input : null
}
export function aggregateCacheTokens(rounds: CacheRound[]): CacheTokens {
  const reports = rounds.filter(r => r.status === 'completed' && cacheHitRate(r.tokens) !== null)
  if (!reports.length) return { input: null, output: null, read: null, write: null }
  return reports.reduce<CacheTokens>((sum, r) => ({
    input: (sum.input ?? 0) + r.tokens!.input!, read: (sum.read ?? 0) + r.tokens!.read!,
    output: (sum.output ?? 0) + (r.tokens!.output ?? 0), write: (sum.write ?? 0) + (r.tokens!.write ?? 0)
  }), { input: 0, output: 0, read: 0, write: 0 })
}
export function billFromUsage(log: AdminUsageLog): CacheBill {
  const input = nonnegative(log.input_tokens), read = nonnegative(log.cache_read_tokens), write = nonnegative(log.cache_creation_tokens)
  return {
    id: log.id, requestId: log.request_id, groupId: log.group_id, accountId: log.account_id,
    model: log.model, upstreamModel: log.upstream_model ?? null, serviceTier: log.service_tier ?? null,
    reasoningEffort: log.reasoning_effort ?? null,
    tokens: { input: input === null || read === null || write === null ? null : input + read + write,
      output: nonnegative(log.output_tokens), read, write },
    inputCost: nonnegative(log.input_cost), outputCost: nonnegative(log.output_cost),
    readCost: nonnegative(log.cache_read_cost), writeCost: nonnegative(log.cache_creation_cost),
    totalCost: nonnegative(log.total_cost), actualCost: nonnegative(log.actual_cost), multiplier: nonnegative(log.rate_multiplier),
    firstTokenMs: nonnegative(log.first_token_ms), durationMs: nonnegative(log.duration_ms)
  }
}
const HISTORY_PREFIX = 'sub2api-group-cache-tests:v1:'
const tokenFields = ['input', 'output', 'read', 'write'] as const
const roundFields = ['index', 'status', 'startedAt', 'requestId', 'responseModel', 'serviceTier', 'firstTextMs', 'durationMs', 'error', 'httpStatus', 'billingState'] as const
const billFields = ['id', 'requestId', 'groupId', 'accountId', 'model', 'upstreamModel', 'serviceTier', 'reasoningEffort', 'inputCost', 'outputCost', 'readCost', 'writeCost', 'totalCost', 'actualCost', 'multiplier', 'firstTokenMs', 'durationMs'] as const
function pick<T extends object, K extends keyof T>(value: T, fields: readonly K[]): Pick<T, K> {
  return Object.fromEntries(fields.map(key => [key, value[key]])) as Pick<T, K>
}
// Allowlists at every level: never persist credentials, response/error bodies,
// user objects, or complete usage records.
export function safeCacheRun(run: CacheTestRun): CacheTestRun {
  return {
    ...pick(run, ['version', 'id', 'startedAt', 'finishedAt', 'groupId', 'groupName', 'keyId', 'keyName', 'status']),
    settings: pick(run.settings, ['protocol', 'model', 'rounds', 'prefixBlocks', 'intervalMs', 'timeoutMs', 'maxOutputTokens', 'reasoningEffort', 'serviceTier']),
    rounds: run.rounds.map(r => ({
      ...pick(r, roundFields), tokens: r.tokens ? pick(r.tokens, tokenFields) : null,
      bill: r.bill ? { ...pick(r.bill, billFields), tokens: pick(r.bill.tokens, tokenFields) } : null
    }))
  }
}
export function exportCacheRun(run: CacheTestRun): string {
  return JSON.stringify(safeCacheRun(run), null, 2)
}
const nullableNumber = (value: unknown) => value === null || nonnegative(value) !== null
const nullableText = (value: unknown) => value === null || (typeof value === 'string' && value.length <= 1000)
function validTokens(value: CacheTokens | null): boolean {
  return value === null || (!!value && tokenFields.every(field => nullableNumber(value[field])))
}
function validStoredRound(round: CacheRound): boolean {
  if (!round || !boundedInteger(round.index, 1, 6) || typeof round.startedAt !== 'string' ||
    !['running', 'completed', 'stopped', 'failed'].includes(round.status) ||
    !['pending', 'matched', 'unavailable', 'error'].includes(round.billingState) ||
    ![null, 'http', 'network', 'invalid_stream', 'incomplete', 'timeout', 'aborted'].includes(round.error) ||
    !validTokens(round.tokens) || ![round.requestId, round.responseModel, round.serviceTier].every(nullableText) ||
    ![round.firstTextMs, round.durationMs, round.httpStatus].every(nullableNumber)) return false
  const bill = round.bill
  return bill === null || (!!bill && Number.isInteger(bill.id) && typeof bill.requestId === 'string' &&
    typeof bill.model === 'string' && !!bill.tokens && validTokens(bill.tokens) &&
    [bill.groupId, bill.accountId, bill.inputCost, bill.outputCost, bill.readCost, bill.writeCost, bill.totalCost,
      bill.actualCost, bill.multiplier, bill.firstTokenMs, bill.durationMs].every(nullableNumber) &&
    [bill.upstreamModel, bill.serviceTier, bill.reasoningEffort].every(nullableText))
}
export function loadCacheHistory(userId: number, storage?: Storage): CacheTestRun[] {
  try {
    const raw = (storage ?? localStorage).getItem(HISTORY_PREFIX + userId)
    if (!raw || raw.length > 1000000) return []
    const items = JSON.parse(raw)
    if (!Array.isArray(items)) return []
    return items.slice(0, 20).filter((r): r is CacheTestRun =>
      r && r.version === 1 && typeof r.id === 'string' && typeof r.startedAt === 'string' &&
      typeof r.groupName === 'string' && typeof r.keyName === 'string' && Number.isInteger(r.groupId) &&
      Number.isInteger(r.keyId) && r.settings && validCacheSettings(r.settings) &&
      ['running', 'completed', 'stopped', 'failed'].includes(r.status) &&
      Array.isArray(r.rounds) && r.rounds.length <= 6 && r.rounds.every(validStoredRound)
    ).map(r => {
      const safe = safeCacheRun(r)
      if (safe.status === 'running') safe.status = 'stopped'
      safe.rounds.forEach(round => { if (round.status === 'running') { round.status = 'stopped'; round.error = 'aborted' } })
      return safe
    })
  } catch { return [] }
}
export function saveCacheHistory(userId: number, runs: CacheTestRun[], storage?: Storage): boolean {
  try {
    (storage ?? localStorage).setItem(HISTORY_PREFIX + userId, JSON.stringify(runs.slice(0, 20).map(safeCacheRun)))
    return true
  } catch { return false }
}
