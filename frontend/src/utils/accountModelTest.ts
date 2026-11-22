import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'
import { managementFetchHeaders } from '@/api/managementFetch'

export interface AccountModelTestEvent {
  type: string
  text?: string
  model?: string
  success?: boolean
  error?: string
  image_url?: string
  audio_url?: string
  video_url?: string
  mime_type?: string
}

export interface AccountModelTestMedia {
  url: string
  mimeType?: string
}

export interface AccountModelTestOutput {
  output: string
  success?: boolean
  error?: string
  images: AccountModelTestMedia[]
  audios: AccountModelTestMedia[]
  videos: AccountModelTestMedia[]
}

export function createAccountModelTestOutput(): AccountModelTestOutput {
  return { output: '', images: [], audios: [], videos: [] }
}

export interface StreamAccountModelTestOptions {
  accountId: number
  body: Record<string, unknown>
  signal?: AbortSignal
  onEvent: (event: AccountModelTestEvent) => void
}

/** 模型测试默认并发上限：一次最多同时打 3 个模型，避免点「测试全部」把上游和面板一起打爆。 */
export const DEFAULT_ACCOUNT_MODEL_TEST_CONCURRENCY = 3

/** 工具栏可选并发档位。默认 3，最大 10，避免误选过大值把上游打满。 */
export const ACCOUNT_MODEL_TEST_CONCURRENCY_OPTIONS = [1, 2, 3, 4, 5, 8, 10] as const

export type AccountModelTestFailureKind = 'auth' | 'rate_limit' | 'network' | 'other'

/** HTTP 层失败带上游状态码，供失败原因归类使用（SSE 失败只有错误文本）。 */
export class AccountModelTestHTTPError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'AccountModelTestHTTPError'
    this.status = status
  }
}

/**
 * 把一次模型测试失败归到 4 类里。判定顺序很重要：
 * 429 → 网络/超时 → 其余 4xx（Key/权限）→ 其他（5xx、解析、未知）。
 * 文本判定兜底 SSE 错误事件：那里只有上游返回的错误串，没有独立状态码字段。
 */
export function classifyAccountModelTestFailure(input: {
  error?: string
  status?: number
}): AccountModelTestFailureKind {
  const status = input.status
  const text = (input.error || '').toLowerCase()

  if (status === 429 || /\b429\b|rate.?limit|too many requests|resource_exhausted/.test(text)) {
    return 'rate_limit'
  }
  if (
    status === 408 ||
    /timeout|timed out|etimedout|deadline exceeded|econnreset|econnrefused|connection reset|connection refused|broken pipe|no route to host|network error|fetch failed|socket hang up|tls handshake|no response body|stream ended before completion|eai_again|dns lookup/.test(
      text
    )
  ) {
    return 'network'
  }
  if (
    (status !== undefined && status >= 400 && status < 500) ||
    /\b40[1-9]\b|\b4[0-9]{2}\b|unauthorized|forbidden|invalid api key|invalid_api_key|api key|permission|credential|insufficient_quota|quota exceeded|authentication/.test(
      text
    )
  ) {
    return 'auth'
  }
  return 'other'
}

export interface AccountModelTestEditLocation {
  path: string
  query: Record<string, string>
}

/** 账号编辑深链：AccountsView 监听 ?account= 并直接打开编辑弹窗。 */
export function buildAccountEditLocation(accountId: number): AccountModelTestEditLocation {
  return { path: '/admin/accounts', query: { account: String(accountId) } }
}

/** 渠道编辑深链：ChannelsView 监听 ?channel= 并直接打开编辑弹窗（渠道维度复用）。 */
export function buildChannelEditLocation(channelId: number): AccountModelTestEditLocation {
  return { path: '/admin/channels', query: { channel: String(channelId) } }
}

export interface BatchTestAccount {
  id: number
  name: string
  platform?: string
  type?: string
}

const MEDIA_MODEL_RE = /image|video|whisper|tts|flux|diffusion|audio|realtime/i

export function isMediaHeavyModel(id: string): boolean {
  return MEDIA_MODEL_RE.test(id)
}

export function inferGrokTestMode(modelId: string): 'text' | 'image' | 'video' {
  const id = modelId.toLowerCase()
  if (id.startsWith('grok-imagine-video') || id.startsWith('grok-video')) return 'video'
  if (id === 'grok-imagine' || id === 'grok-imagine-edit' || id.startsWith('grok-imagine-image')) {
    return 'image'
  }
  return 'text'
}

export function inferPlatformFromModelId(modelId: string): string {
  const id = modelId.toLowerCase()
  if (id.startsWith('grok')) return 'grok'
  if (
    id.startsWith('gpt-') ||
    id.startsWith('o1') ||
    id.startsWith('o3') ||
    id.startsWith('o4') ||
    id.startsWith('chatgpt') ||
    id.includes('codex')
  ) {
    return 'openai'
  }
  if (id.startsWith('gemini') || id.startsWith('imagen')) return 'gemini'
  if (id.startsWith('claude')) return 'anthropic'
  if (id.startsWith('kimi')) return 'kimi'
  if (id.startsWith('glm') || id.startsWith('zai-')) return 'zhipu'
  if (id.startsWith('deepseek')) return 'deepseek'
  if (id.startsWith('minimax')) return 'minimax'
  return ''
}

export function buildAccountModelTestBody(opts: {
  modelId: string
  platform?: string
  prompt?: string
  openaiMode?: 'default' | 'compact'
}): Record<string, unknown> {
  const platform = opts.platform || inferPlatformFromModelId(opts.modelId)
  const body: Record<string, unknown> = {
    model_id: opts.modelId,
    prompt: opts.prompt ?? ''
  }
  if (platform === 'openai') body.mode = opts.openaiMode ?? 'default'
  if (platform === 'grok') body.mode = inferGrokTestMode(opts.modelId)
  return body
}

export function resolveBatchTestAccounts(
  selectedIds: number[],
  pageAccounts: Array<{ id: number; name?: string; platform?: string; type?: string }>
): BatchTestAccount[] {
  const pageMap = new Map(pageAccounts.map((account) => [account.id, account]))
  return selectedIds.map((id) => {
    const row = pageMap.get(id)
    const name = row?.name?.trim()
    return {
      id,
      name: name || `#${id}`,
      platform: row?.platform || '',
      type: row?.type || ''
    }
  })
}

export function formatAccountModelTestDuration(ms?: number): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return '-'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

export function applyAccountModelTestEvent(
  state: AccountModelTestOutput,
  event: AccountModelTestEvent
): void {
  switch (event.type) {
    case 'content':
    case 'status':
    case 'text':
      if (event.text) state.output += event.text
      break
    case 'image':
      if (event.image_url) {
        state.images.push({ url: event.image_url, mimeType: event.mime_type })
      }
      break
    case 'audio':
      if (event.audio_url) {
        state.audios.push({ url: event.audio_url, mimeType: event.mime_type })
      }
      break
    case 'video':
      if (event.video_url) {
        state.videos.push({ url: event.video_url, mimeType: event.mime_type })
      }
      break
    case 'test_complete':
      if (state.success !== false) state.success = event.success === true
      if (!event.success) state.error = event.error || state.error || ''
      break
    case 'error':
      state.success = false
      state.error = event.error || ''
      break
    default:
      break
  }
}

export function summarizeAccountModelTestOutput(state: AccountModelTestOutput): string {
  if (state.error?.trim()) return state.error.trim()
  const text = state.output.replace(/\s+/g, ' ').trim()
  if (text) return text
  return state.error?.trim() || ''
}

export function parseSSEDataLine(line: string): AccountModelTestEvent | null {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return null
  const jsonStr = trimmed.slice(5).trim()
  if (!jsonStr) return null
  try {
    const event: unknown = JSON.parse(jsonStr)
    if (!event || typeof event !== 'object' || Array.isArray(event)) return null
    const candidate = event as Record<string, unknown>
    if (typeof candidate.type !== 'string') return null
    for (const key of ['text', 'model', 'error', 'image_url', 'audio_url', 'video_url', 'mime_type']) {
      if (candidate[key] !== undefined && typeof candidate[key] !== 'string') return null
    }
    if (candidate.success !== undefined && typeof candidate.success !== 'boolean') return null
    return candidate as unknown as AccountModelTestEvent
  } catch {
    return null
  }
}

export function consumeSSEBuffer(
  buffer: string,
  chunk: string,
  onEvent: (event: AccountModelTestEvent) => void
): string {
  const text = buffer + chunk
  const lines = text.split(/\r?\n/)
  const rest = lines.pop() ?? ''
  for (const line of lines) {
    const event = parseSSEDataLine(line)
    if (event) onEvent(event)
  }
  return rest
}

export async function streamAccountModelTest(opts: StreamAccountModelTestOptions): Promise<void> {
  const checkAborted = () => {
    if (opts.signal?.aborted) throw new DOMException('Aborted', 'AbortError')
  }
  checkAborted()
  const url = buildApiUrl(`/admin/accounts/${opts.accountId}/test`)
  const response = await fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: managementFetchHeaders({ [ADMIN_UI_REQUEST_HEADER]: '1' }),
    body: JSON.stringify(opts.body),
    signal: opts.signal
  })

  if (!response.ok) {
    let message = ''
    try {
      const payload = await response.json()
      const detail = payload?.message || payload?.error?.message || payload?.error
      if (typeof detail === 'string') message = detail
    } catch {
      message = ''
    }
    checkAborted()
    throw new AccountModelTestHTTPError(
      response.status,
      [`HTTP ${response.status}`, message].filter(Boolean).join(': ')
    )
  }

  const reader = response.body?.getReader()
  if (!reader) {
    throw new Error('no response body')
  }

  const decoder = new TextDecoder()
  let buffer = ''
  let terminal = false
  const onEvent = (event: AccountModelTestEvent) => {
    if (terminal) return
    checkAborted()
    if (event.type === 'test_complete' || event.type === 'error') terminal = true
    opts.onEvent(event)
  }
  const cancelReader = () => { void reader.cancel().catch(() => {}) }
  opts.signal?.addEventListener('abort', cancelReader, { once: true })
  try {
    checkAborted()
    while (true) {
      const { done, value } = await reader.read()
      checkAborted()
      if (done) break
      buffer = consumeSSEBuffer(buffer, decoder.decode(value, { stream: true }), onEvent)
      if (terminal) break
    }
    buffer += decoder.decode()
    if (!terminal && buffer.trim()) {
      const event = parseSSEDataLine(buffer)
      if (event) onEvent(event)
    }
    if (!terminal) throw new Error('Account test stream ended before completion')
  } finally {
    opts.signal?.removeEventListener('abort', cancelReader)
    if (terminal) cancelReader()
    reader.releaseLock()
  }
}

export async function runWithConcurrency<T>(
  items: T[],
  concurrency: number,
  worker: (item: T, index: number) => Promise<void>,
  signal?: AbortSignal
): Promise<void> {
  if (items.length === 0) return
  const limit = Math.max(1, Math.min(concurrency, items.length))
  let next = 0

  const runNext = async (): Promise<void> => {
    while (true) {
      if (signal?.aborted) {
        throw new DOMException('Aborted', 'AbortError')
      }
      const index = next++
      if (index >= items.length) return
      await worker(items[index], index)
    }
  }

  await Promise.all(Array.from({ length: limit }, () => runNext()))
}

export function groupJobsByAccount<T extends { accountId: number }>(jobs: T[]): T[][] {
  const groups: T[][] = []
  const indexByAccount = new Map<number, number>()
  for (const job of jobs) {
    const existing = indexByAccount.get(job.accountId)
    if (existing === undefined) {
      indexByAccount.set(job.accountId, groups.length)
      groups.push([job])
    } else {
      groups[existing].push(job)
    }
  }
  return groups
}
