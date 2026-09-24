import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'

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

export interface StreamAccountModelTestOptions {
  accountId: number
  body: Record<string, unknown>
  signal?: AbortSignal
  onEvent: (event: AccountModelTestEvent) => void
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

export function parseSSEDataLine(line: string): AccountModelTestEvent | null {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return null
  const jsonStr = trimmed.slice(5).trim()
  if (!jsonStr) return null
  try {
    return JSON.parse(jsonStr) as AccountModelTestEvent
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
  const url = buildApiUrl(`/admin/accounts/${opts.accountId}/test`)
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
      'Content-Type': 'application/json',
      [ADMIN_UI_REQUEST_HEADER]: '1'
    },
    body: JSON.stringify(opts.body),
    signal: opts.signal
  })

  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`)
  }

  const reader = response.body?.getReader()
  if (!reader) {
    throw new Error('no response body')
  }

  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer = consumeSSEBuffer(buffer, decoder.decode(value, { stream: true }), opts.onEvent)
  }

  if (buffer.trim()) {
    const event = parseSSEDataLine(buffer)
    if (event) opts.onEvent(event)
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
