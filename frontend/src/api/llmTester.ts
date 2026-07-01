import { buildApiUrl } from '@/api/client'

export interface LLMTesterProfile {
  id: string
  name: string
  provider: 'openrouter' | 'sub2api' | 'custom'
  baseUrl: string
  apiKey: string
  selectedModel: string
  lastFetchedAt?: string
}

export interface LLMTesterModel {
  id: string
  name: string
  ownedBy?: string
  contextLength?: number
  raw?: Record<string, unknown>
}

export type LLMTesterModelCapability = 'chat' | 'vision' | 'image_generation' | 'video_generation'

export interface LLMTesterAttachment {
  id: string
  name: string
  type: string
  size: number
  kind: 'image' | 'text' | 'media' | 'file'
  dataUrl?: string
  text?: string
}

export interface LLMTesterMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  attachments?: LLMTesterAttachment[]
}

export interface ChatCompletionOptions {
  baseUrl: string
  apiKey: string
  model: string
  messages: LLMTesterMessage[]
  systemInstruction?: string
  temperature?: number
  maxTokens?: number
  signal?: AbortSignal
}

export interface ImageGenerationOptions {
  baseUrl: string
  apiKey: string
  model: string
  messages: LLMTesterMessage[]
  systemInstruction?: string
  signal?: AbortSignal
}

export interface ImageGenerationResult {
  text: string
  attachments: LLMTesterAttachment[]
  raw: unknown
}

export type MediaGenerationResult = ImageGenerationResult

interface OpenAIContentTextPart {
  type: 'text'
  text: string
}

interface OpenAIContentImagePart {
  type: 'image_url'
  image_url: {
    url: string
  }
}

type OpenAIMessageContent = string | Array<OpenAIContentTextPart | OpenAIContentImagePart>

interface OpenAIChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: OpenAIMessageContent
}

export const OPENROUTER_BASE_URL = 'https://openrouter.ai/api/v1'

export function defaultSub2APIBaseUrl(): string {
  return '/v1'
}

export function normalizeBaseUrl(input: string): string {
  const trimmed = input.trim().replace(/\/+$/, '')
  if (!trimmed) return ''
  if (/^https?:\/\//i.test(trimmed) || trimmed.startsWith('/')) return trimmed
  return `https://${trimmed}`
}

export type LLMTesterProxyPath = 'models' | 'chat/completions' | 'images/generations' | 'videos/generations' | 'responses'

export function buildOpenAIEndpoint(baseUrl: string, path: LLMTesterProxyPath): string {
  const normalized = normalizeBaseUrl(baseUrl)
  if (!normalized) return ''
  const resource = path.replace(/^v\d+\//, '')
  if (/\/v\d+$/i.test(normalized)) return `${normalized}/${resource}`
  return `${normalized}/v1/${resource}`
}

function getHeaderSafeSiteTitle(): string {
  if (typeof document === 'undefined') return 'Sub2API LLM Tester'
  return document.title || 'Sub2API LLM Tester'
}

function buildHeaders(apiKey: string): HeadersInit {
  return {
    Authorization: `Bearer ${apiKey}`,
    'Content-Type': 'application/json',
    'X-Title': getHeaderSafeSiteTitle(),
  }
}

function buildJsonHeaders(): HeadersInit {
  return {
    'Content-Type': 'application/json',
  }
}

function getObject(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' ? value as Record<string, unknown> : undefined
}

function getString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined
}

function getNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function getStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value
    .map((item) => typeof item === 'string' ? item.trim().toLowerCase() : '')
    .filter(Boolean)
}

export function isLikelyChatCompletionModelId(modelId: string): boolean {
  const id = modelId.trim().toLowerCase()
  if (!id) return false
  if (/(^|[/:-])(?:text-)?embedding/.test(id) || id.includes('embedding')) return false
  if (/(^|[/:-])(?:gpt-)?image(?:-|$)/.test(id) || id.includes('/image-')) return false
  if (isLikelyImageGenerationModelId(id) || isLikelyVideoGenerationModelId(id)) return false
  if (id.includes('dall-e') || id.includes('whisper') || id.includes('tts')) return false
  if (id.includes('moderation') || id.includes('omni-moderation')) return false
  if (id.includes('transcribe') || id.includes('realtime')) return false
  return true
}

const GROK_IMAGE_MODEL_IDS = new Set([
  'grok-imagine',
  'grok-imagine-image',
  'grok-imagine-image-quality',
  'grok-imagine-edit',
])

const GROK_VIDEO_MODEL_IDS = new Set([
  'grok-imagine-video',
  'grok-imagine-video-1.5',
])

export function isLikelyImageGenerationModelId(modelId: string): boolean {
  const id = modelId.trim().toLowerCase()
  if (!id) return false
  return (
    GROK_IMAGE_MODEL_IDS.has(id) ||
    /(^|[/:-])(?:gpt-)?image(?:-|$)/.test(id) ||
    id.includes('/image-') ||
    id.includes('dall-e') ||
    id.includes('imagen')
  )
}

export function isLikelyVideoGenerationModelId(modelId: string): boolean {
  const id = modelId.trim().toLowerCase()
  if (!id) return false
  return GROK_VIDEO_MODEL_IDS.has(id) || id.includes('video-generation') || /(^|[/:-])video(?:-|$)/.test(id)
}

function splitModalities(value: string): string[] {
  return value
    .split(/[+,]/)
    .map((part) => part.trim().toLowerCase())
    .filter(Boolean)
}

function getModelModalities(model: LLMTesterModel): { input: string[]; output: string[] } {
  const architecture = getObject(model.raw?.architecture)
  const input = new Set(getStringArray(architecture?.input_modalities))
  const output = new Set(getStringArray(architecture?.output_modalities))

  const modality = getString(architecture?.modality)?.toLowerCase()
  if (modality?.includes('->')) {
    const [inputSide, outputSide] = modality.split('->')
    splitModalities(inputSide || '').forEach((item) => input.add(item))
    splitModalities(outputSide || '').forEach((item) => output.add(item))
  }

  return {
    input: Array.from(input),
    output: Array.from(output),
  }
}

function isKnownUnsupportedModelId(modelId: string): boolean {
  const id = modelId.trim().toLowerCase()
  return (
    /(^|[/:-])(?:text-)?embedding/.test(id) ||
    id.includes('embedding') ||
    id.includes('moderation') ||
    id.includes('omni-moderation') ||
    id.includes('whisper') ||
    id.includes('tts') ||
    id.includes('transcribe') ||
    id.includes('realtime')
  )
}

export function getLLMTesterModelCapabilities(model: LLMTesterModel): LLMTesterModelCapability[] {
  const capabilities = new Set<LLMTesterModelCapability>()
  const modalities = getModelModalities(model)
  const hasOutputMetadata = modalities.output.length > 0
  const outputsText = modalities.output.includes('text')
  const outputsImage = modalities.output.includes('image') || isLikelyImageGenerationModelId(model.id)
  const outputsVideo = modalities.output.includes('video') || isLikelyVideoGenerationModelId(model.id)
  const unsupportedByTester = isKnownUnsupportedModelId(model.id)

  if (outputsImage) {
    capabilities.add('image_generation')
  }

  if (outputsVideo) {
    capabilities.add('video_generation')
  }

  if (!unsupportedByTester && !outputsImage && !outputsVideo && (!hasOutputMetadata || outputsText)) {
    capabilities.add('chat')
  }

  if (capabilities.has('chat') && modalities.input.includes('image')) {
    capabilities.add('vision')
  }

  return Array.from(capabilities)
}

export function isChatCompletionModel(model: LLMTesterModel): boolean {
  return getLLMTesterModelCapabilities(model).includes('chat')
}

export function isImageGenerationModel(model: LLMTesterModel): boolean {
  return getLLMTesterModelCapabilities(model).includes('image_generation')
}

export function isVideoGenerationModel(model: LLMTesterModel): boolean {
  return getLLMTesterModelCapabilities(model).includes('video_generation')
}

export function isLLMTesterSupportedModel(model: LLMTesterModel): boolean {
  const capabilities = getLLMTesterModelCapabilities(model)
  return capabilities.includes('chat') || capabilities.includes('image_generation') || capabilities.includes('video_generation')
}

function extractErrorMessage(payload: unknown, fallback: string): string {
  const obj = getObject(payload)
  const errorObj = getObject(obj?.error)
  return (
    getString(errorObj?.message) ||
    getString(obj?.message) ||
    getString(obj?.detail) ||
    fallback
  )
}

async function parseResponsePayload(response: Response): Promise<unknown> {
  const contentType = response.headers.get('content-type') || ''
  if (contentType.includes('application/json')) return response.json()
  const text = await response.text()
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

function unwrapApiEnvelope(payload: unknown): unknown {
  const obj = getObject(payload)
  if (!obj || !('code' in obj) || !('data' in obj)) return payload
  return obj.data
}

function shouldUseTesterProxy(baseUrl: string): boolean {
  const normalized = normalizeBaseUrl(baseUrl)
  if (!normalized || normalized.startsWith('/')) return false
  if (typeof window === 'undefined') return true
  try {
    return new URL(normalized).origin !== window.location.origin
  } catch {
    return true
  }
}

async function postTesterProxy(path: LLMTesterProxyPath, body: Record<string, unknown>, signal?: AbortSignal): Promise<unknown> {
  const response = await fetch(buildApiUrl(`/llm-tester/${path}`), {
    method: 'POST',
    headers: buildJsonHeaders(),
    body: JSON.stringify(body),
    signal,
  })
  const payload = await parseResponsePayload(response)
  if (!response.ok) {
    const fallback = path === 'models'
      ? `Failed to fetch models (${response.status})`
      : path === 'videos/generations'
        ? `Video generation failed (${response.status})`
        : path === 'images/generations' || path === 'responses'
          ? `Image generation failed (${response.status})`
          : `Chat request failed (${response.status})`
    throw new Error(extractErrorMessage(payload, fallback))
  }
  return unwrapApiEnvelope(payload)
}

export function parseModelList(payload: unknown): LLMTesterModel[] {
  const obj = getObject(payload)
  const data = Array.isArray(obj?.data) ? obj.data : Array.isArray(payload) ? payload : []

  return data
    .map((item): LLMTesterModel | null => {
      const raw = getObject(item)
      if (!raw) return null

      const id = getString(raw.id) || getString(raw.name)
      if (!id) return null

      const topProvider = getObject(raw.top_provider)
      return {
        id,
        name: getString(raw.name) || id,
        ownedBy: getString(raw.owned_by) || getString(raw.ownedBy),
        contextLength: getNumber(raw.context_length) || getNumber(raw.contextLength) || getNumber(topProvider?.context_length),
        raw,
      }
    })
    .filter((model): model is LLMTesterModel => model !== null)
    .filter(isLLMTesterSupportedModel)
    .sort((a, b) => a.id.localeCompare(b.id))
}

export async function fetchLLMModels(baseUrl: string, apiKey: string, signal?: AbortSignal): Promise<LLMTesterModel[]> {
  const endpoint = buildOpenAIEndpoint(baseUrl, 'models')
  if (!endpoint) throw new Error('Base URL is required')

  if (shouldUseTesterProxy(baseUrl)) {
    const payload = await postTesterProxy('models', {
      base_url: normalizeBaseUrl(baseUrl),
      api_key: apiKey,
    }, signal)
    return parseModelList(payload)
  }

  const response = await fetch(endpoint, {
    method: 'GET',
    headers: buildHeaders(apiKey),
    signal,
  })
  const payload = await parseResponsePayload(response)
  if (!response.ok) {
    throw new Error(extractErrorMessage(payload, `Failed to fetch models (${response.status})`))
  }

  return parseModelList(payload)
}

function inferLanguage(filename: string, type: string): string {
  const lower = filename.toLowerCase()
  const ext = lower.includes('.') ? lower.split('.').pop() || '' : ''
  const byExt: Record<string, string> = {
    js: 'javascript',
    jsx: 'jsx',
    ts: 'typescript',
    tsx: 'tsx',
    vue: 'vue',
    py: 'python',
    go: 'go',
    rs: 'rust',
    java: 'java',
    c: 'c',
    cpp: 'cpp',
    cs: 'csharp',
    html: 'html',
    css: 'css',
    json: 'json',
    md: 'markdown',
    sh: 'bash',
    sql: 'sql',
    yml: 'yaml',
    yaml: 'yaml',
    xml: 'xml',
    toml: 'toml',
    csv: 'csv',
  }
  if (byExt[ext]) return byExt[ext]
  if (type.includes('json')) return 'json'
  if (type.includes('markdown')) return 'markdown'
  if (type.includes('html')) return 'html'
  return ''
}

function formatTextAttachment(attachment: LLMTesterAttachment): string {
  const language = inferLanguage(attachment.name, attachment.type)
  return [
    `Attached file: ${attachment.name}`,
    `\`\`\`${language}`,
    attachment.text || '',
    '```',
  ].join('\n')
}

function buildImageGenerationPrompt(messages: LLMTesterMessage[], systemInstruction = ''): string {
  const latestUserMessage = [...messages].reverse().find((message) => message.role === 'user')
  const attachments = latestUserMessage?.attachments || []
  const textAttachments = attachments.filter((attachment) => attachment.kind === 'text' && attachment.text)
  const mediaAttachments = attachments.filter((attachment) => attachment.kind !== 'text')

  const sections = [
    systemInstruction.trim(),
    latestUserMessage?.content.trim() || '',
    ...textAttachments.map(formatTextAttachment),
    ...mediaAttachments.map((attachment) => `Attached reference file: ${attachment.name} (${attachment.type || 'unknown type'}, ${attachment.size} bytes).`),
  ].filter(Boolean)

  return sections.join('\n\n')
}

function buildMediaGenerationPrompt(messages: LLMTesterMessage[], systemInstruction = ''): string {
  return buildImageGenerationPrompt(messages, systemInstruction)
}

function buildUserContent(message: LLMTesterMessage): OpenAIMessageContent {
  const attachments = message.attachments || []
  const imageAttachments = attachments.filter((attachment) => attachment.kind === 'image' && attachment.dataUrl)
  const textAttachments = attachments.filter((attachment) => attachment.kind === 'text' && attachment.text)
  const otherAttachments = attachments.filter((attachment) => attachment.kind !== 'image' && attachment.kind !== 'text')

  const textParts = [
    message.content.trim(),
    ...textAttachments.map(formatTextAttachment),
    ...otherAttachments.map((attachment) => `Attached media: ${attachment.name} (${attachment.type || 'unknown type'}, ${attachment.size} bytes).`),
  ].filter(Boolean)

  if (imageAttachments.length === 0) return textParts.join('\n\n')

  const content: Array<OpenAIContentTextPart | OpenAIContentImagePart> = []
  content.push({
    type: 'text',
    text: textParts.join('\n\n') || 'Please analyze the attached image.',
  })

  for (const attachment of imageAttachments) {
    if (!attachment.dataUrl) continue
    content.push({
      type: 'image_url',
      image_url: { url: attachment.dataUrl },
    })
  }

  return content
}

export function buildChatCompletionMessages(messages: LLMTesterMessage[], systemInstruction = ''): OpenAIChatMessage[] {
  const out: OpenAIChatMessage[] = []
  const system = systemInstruction.trim()
  if (system) {
    out.push({ role: 'system', content: system })
  }

  for (const message of messages) {
    out.push({
      role: message.role,
      content: message.role === 'user' ? buildUserContent(message) : message.content,
    })
  }

  return out
}

export function extractChatCompletionText(payload: unknown): string {
  const obj = getObject(payload)
  const choices = Array.isArray(obj?.choices) ? obj.choices : []
  const firstChoice = getObject(choices[0])
  const message = getObject(firstChoice?.message)
  const content = message?.content

  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((part) => {
        const partObj = getObject(part)
        return getString(partObj?.text) || getString(partObj?.content) || ''
      })
      .filter(Boolean)
      .join('\n')
  }

  const text = getString(firstChoice?.text)
  if (text) return text

  return JSON.stringify(payload, null, 2)
}

export function extractImageGenerationResult(payload: unknown): ImageGenerationResult {
  const attachments: LLMTesterAttachment[] = []
  const lines: string[] = []

  const pushImageAttachment = (rawValue: unknown, index: number) => {
    const value = normalizeGeneratedImageValue(rawValue)
    if (!value) return
    attachments.push({
      id: `generated-image-${Date.now()}-${index}`,
      name: `generated-image-${index + 1}.png`,
      type: 'image/png',
      size: 0,
      kind: 'image',
      dataUrl: value,
    })
  }

  const explicitImageResult = (value: unknown): unknown => {
    const text = getString(value)
    if (!text) return value
    if (/^(?:data:image\/|https?:\/\/)/i.test(text)) return text
    return `data:image/png;base64,${text}`
  }

  const processOutputItem = (item: unknown) => {
    const outputItem = getObject(item)
    if (!outputItem) return
    const type = getString(outputItem.type)

    if (type === 'image_generation_call') {
      const b64 = getString(outputItem.b64_json)
      pushImageAttachment(b64 ? `data:image/png;base64,${b64}` : explicitImageResult(outputItem.result) || outputItem.image_url || outputItem.url, attachments.length)
      const revisedPrompt = getString(outputItem.revised_prompt)
      if (revisedPrompt) {
        lines.push(`Revised prompt: ${revisedPrompt}`)
      }
    }

    const content = Array.isArray(outputItem.content) ? outputItem.content : []
    content.forEach((part) => {
      const partObj = getObject(part)
      if (!partObj) return
      const partType = getString(partObj.type)
      const text = getString(partObj.text)
      if (text && (partType === 'output_text' || partType === 'text')) {
        lines.push(text)
      }
      const b64 = getString(partObj.b64_json)
      pushImageAttachment(b64 ? `data:image/png;base64,${b64}` : explicitImageResult(partObj.result) || partObj.image_url || partObj.url, attachments.length)
    })

    const outputText = getString(outputItem.text)
    if (outputText && type !== 'image_generation_call') {
      lines.push(outputText)
    }
  }

  const processPayload = (rawPayload: unknown) => {
    const obj = getObject(rawPayload)
    if (!obj) return

    if (obj.item) {
      processOutputItem(obj.item)
    }
    if (obj.response) {
      processPayload(obj.response)
    }

    const data = Array.isArray(obj.data) ? obj.data : []
    data.forEach((item, index) => {
      const image = getObject(item)
      if (!image) return

      const revisedPrompt = getString(image.revised_prompt)
      if (revisedPrompt) {
        lines.push(`Revised prompt: ${revisedPrompt}`)
      }

      const b64 = getString(image.b64_json)
      const url = getString(image.url)
      pushImageAttachment(b64 ? `data:image/png;base64,${b64}` : url, index)
    })

    const output = Array.isArray(obj.output) ? obj.output : []
    output.forEach(processOutputItem)
  }

  const payloads = typeof payload === 'string' ? parseEventStreamPayload(payload) : [payload]
  payloads.forEach(processPayload)

  if (attachments.length > 0) {
    lines.unshift(`Generated ${attachments.length} image${attachments.length === 1 ? '' : 's'}.`)
  }

  return {
    text: lines.join('\n\n') || JSON.stringify(payload, null, 2),
    attachments,
    raw: payload,
  }
}

function parseEventStreamPayload(payload: string): unknown[] {
  const events: unknown[] = []
  const dataLines: string[] = []

  const flush = () => {
    const data = dataLines.join('\n').trim()
    dataLines.length = 0
    if (!data || data === '[DONE]') return
    try {
      events.push(JSON.parse(data))
    } catch {
      events.push(data)
    }
  }

  for (const line of payload.split(/\r?\n/)) {
    if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trimStart())
      continue
    }
    if (!line.trim()) {
      flush()
    }
  }
  flush()

  if (events.length > 0) return events
  try {
    return [JSON.parse(payload)]
  } catch {
    return []
  }
}

function normalizeGeneratedImageValue(value: unknown): string {
  if (typeof value === 'object' && value !== null) {
    const obj = getObject(value)
    return normalizeGeneratedImageValue(obj?.url || obj?.b64_json || obj?.result)
  }
  const text = getString(value)
  if (!text) return ''
  if (/^data:image\//i.test(text)) return text
  if (/^https?:\/\//i.test(text)) return text
  const compact = text.replace(/\s+/g, '')
  if (compact.length > 100 && /^[A-Za-z0-9+/=]+$/.test(compact)) {
    return `data:image/png;base64,${compact}`
  }
  return ''
}

function normalizeGeneratedMediaValue(value: unknown): string {
  if (typeof value === 'object' && value !== null) {
    const obj = getObject(value)
    return normalizeGeneratedMediaValue(
      obj?.url ||
      obj?.video_url ||
      obj?.download_url ||
      obj?.b64_json ||
      obj?.base64 ||
      obj?.result
    )
  }
  const text = getString(value)
  if (!text) return ''
  if (/^data:video\//i.test(text)) return text
  if (/^https?:\/\//i.test(text)) return text
  const compact = text.replace(/\s+/g, '')
  if (compact.length > 100 && /^[A-Za-z0-9+/=]+$/.test(compact)) {
    return `data:video/mp4;base64,${compact}`
  }
  return ''
}

export function extractVideoGenerationResult(payload: unknown): MediaGenerationResult {
  const attachments: LLMTesterAttachment[] = []
  const lines: string[] = []

  const pushVideoAttachment = (rawValue: unknown, index: number) => {
    const value = normalizeGeneratedMediaValue(rawValue)
    if (!value) return
    attachments.push({
      id: `generated-video-${Date.now()}-${index}`,
      name: `generated-video-${index + 1}.mp4`,
      type: 'video/mp4',
      size: 0,
      kind: 'media',
      dataUrl: value,
    })
  }

  const processObject = (value: unknown) => {
    const obj = getObject(value)
    if (!obj) return

    const status = getString(obj.status)
    if (status) lines.push(`Status: ${status}`)
    const id = getString(obj.id) || getString(obj.request_id)
    if (id) lines.push(`Request ID: ${id}`)
    const revisedPrompt = getString(obj.revised_prompt)
    if (revisedPrompt) lines.push(`Revised prompt: ${revisedPrompt}`)

    pushVideoAttachment(obj, attachments.length)

    const data = Array.isArray(obj.data) ? obj.data : []
    data.forEach((item) => {
      processObject(item)
    })

    const output = Array.isArray(obj.output) ? obj.output : []
    output.forEach((item) => {
      processObject(item)
    })

    const content = Array.isArray(obj.content) ? obj.content : []
    content.forEach((item) => {
      const itemObj = getObject(item)
      const text = getString(itemObj?.text)
      if (text) lines.push(text)
      processObject(item)
    })
  }

  const payloads = typeof payload === 'string' ? parseEventStreamPayload(payload) : [payload]
  payloads.forEach(processObject)

  const uniqueLines = Array.from(new Set(lines))
  if (attachments.length > 0) {
    uniqueLines.unshift(`Generated ${attachments.length} video${attachments.length === 1 ? '' : 's'}.`)
  }

  return {
    text: uniqueLines.join('\n\n') || JSON.stringify(payload, null, 2),
    attachments,
    raw: payload,
  }
}

function imageToolModelId(model: string): string {
  const trimmed = model.trim()
  if (!trimmed) return 'gpt-image-2'
  const parts = trimmed.split('/').filter(Boolean)
  return parts[parts.length - 1] || trimmed
}

function imageResponsesDriverModel(model: string): string {
  return isLikelyImageGenerationModelId(model) ? 'gpt-5.4' : model
}

function buildResponsesImageGenerationBody(model: string, prompt: string): Record<string, unknown> {
  return {
    model: imageResponsesDriverModel(model),
    stream: true,
    tools: [
      {
        type: 'image_generation',
        model: imageToolModelId(model),
      },
    ],
    input: [
      {
        role: 'user',
        content: [
          {
            type: 'input_text',
            text: prompt,
          },
        ],
      },
    ],
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

async function postOpenAIResource(
  baseUrl: string,
  apiKey: string,
  path: LLMTesterProxyPath,
  body: Record<string, unknown>,
  signal?: AbortSignal
): Promise<unknown> {
  const endpoint = buildOpenAIEndpoint(baseUrl, path)
  if (!endpoint) throw new Error('Base URL is required')

  if (shouldUseTesterProxy(baseUrl)) {
    return postTesterProxy(path, {
      base_url: normalizeBaseUrl(baseUrl),
      api_key: apiKey,
      payload: body,
    }, signal)
  }

  const response = await fetch(endpoint, {
    method: 'POST',
    headers: buildHeaders(apiKey),
    body: JSON.stringify(body),
    signal,
  })
  const payload = await parseResponsePayload(response)
  if (!response.ok) {
    const fallback = path === 'chat/completions'
      ? `Chat request failed (${response.status})`
      : path === 'videos/generations'
        ? `Video generation failed (${response.status})`
        : `Image generation failed (${response.status})`
    throw new Error(extractErrorMessage(payload, fallback))
  }

  return payload
}

export async function sendLLMChatCompletion(options: ChatCompletionOptions): Promise<{ text: string; raw: unknown }> {
  const endpoint = buildOpenAIEndpoint(options.baseUrl, 'chat/completions')
  if (!endpoint) throw new Error('Base URL is required')

  const body: Record<string, unknown> = {
    model: options.model,
    messages: buildChatCompletionMessages(options.messages, options.systemInstruction),
    stream: false,
  }

  if (typeof options.temperature === 'number' && Number.isFinite(options.temperature)) {
    body.temperature = options.temperature
  }
  if (typeof options.maxTokens === 'number' && Number.isFinite(options.maxTokens) && options.maxTokens > 0) {
    body.max_tokens = Math.floor(options.maxTokens)
  }

  if (shouldUseTesterProxy(options.baseUrl)) {
    const payload = await postTesterProxy('chat/completions', {
      base_url: normalizeBaseUrl(options.baseUrl),
      api_key: options.apiKey,
      payload: body,
    }, options.signal)
    return {
      text: extractChatCompletionText(payload),
      raw: payload,
    }
  }

  const response = await fetch(endpoint, {
    method: 'POST',
    headers: buildHeaders(options.apiKey),
    body: JSON.stringify(body),
    signal: options.signal,
  })
  const payload = await parseResponsePayload(response)
  if (!response.ok) {
    throw new Error(extractErrorMessage(payload, `Chat request failed (${response.status})`))
  }

  return {
    text: extractChatCompletionText(payload),
    raw: payload,
  }
}

export async function sendLLMImageGeneration(options: ImageGenerationOptions): Promise<ImageGenerationResult> {
  const prompt = buildImageGenerationPrompt(options.messages, options.systemInstruction)
  if (!prompt) throw new Error('Prompt is required for image generation')

  const body: Record<string, unknown> = {
    model: options.model,
    prompt,
    n: 1,
  }
  if (/^gpt-image-/i.test(imageToolModelId(options.model))) {
    body.stream = true
  }

  try {
    const payload = await postOpenAIResource(options.baseUrl, options.apiKey, 'images/generations', body, options.signal)
    return extractImageGenerationResult(payload)
  } catch (primaryError) {
    if (isAbortError(primaryError)) throw primaryError

    try {
      const fallbackPayload = await postOpenAIResource(
        options.baseUrl,
        options.apiKey,
        'responses',
        buildResponsesImageGenerationBody(options.model, prompt),
        options.signal
      )
      const fallbackResult = extractImageGenerationResult(fallbackPayload)
      if (fallbackResult.attachments.length > 0) return fallbackResult
      throw new Error('Responses image tool returned no image output')
    } catch (fallbackError) {
      if (isAbortError(fallbackError)) throw fallbackError
      const primaryMessage = primaryError instanceof Error ? primaryError.message : 'Image endpoint failed'
      const fallbackMessage = fallbackError instanceof Error ? fallbackError.message : 'Responses fallback failed'
      throw new Error(`${primaryMessage}; responses fallback failed: ${fallbackMessage}`)
    }
  }
}

export async function sendLLMVideoGeneration(options: ImageGenerationOptions): Promise<MediaGenerationResult> {
  const prompt = buildMediaGenerationPrompt(options.messages, options.systemInstruction)
  if (!prompt) throw new Error('Prompt is required for video generation')

  const body: Record<string, unknown> = {
    model: options.model,
    prompt,
  }

  const payload = await postOpenAIResource(options.baseUrl, options.apiKey, 'videos/generations', body, options.signal)
  return extractVideoGenerationResult(payload)
}
