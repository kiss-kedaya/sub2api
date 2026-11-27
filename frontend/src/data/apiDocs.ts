export type DocsCategory = 'start' | 'guides' | 'media' | 'integrations' | 'account' | 'reference'
export type DocsLanguage = 'curl' | 'python' | 'node' | 'json' | 'toml' | 'shell'

export interface DocsField {
  name: string
  type: string
  required?: boolean
  description: string
}

export interface DocsEndpoint {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE'
  path: string
  // apiKey: sent with an API key like model requests. session: sent with the login token the web console uses.
  auth: 'apiKey' | 'session'
}

export interface DocsQuickAction {
  kind: 'createKey' | 'manageKeys' | 'doc'
  label: string
  unauthenticatedLabel?: string
  page?: string
  variant?: 'primary' | 'secondary' | 'ghost'
}

export interface DocsSummaryCard {
  sectionId: string
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  badge?: string
  badgeTone?: 'blue' | 'emerald' | 'amber' | 'slate'
  title: string
  subtitle: string
  path?: string
}

export interface DocsSectionAction {
  kind: 'createKey' | 'manageKeys' | 'doc'
  title: string
  description: string
  label: string
  unauthenticatedLabel?: string
  page?: string
}

export interface DocsSection {
  id: string
  title: string
  endpoint?: DocsEndpoint
  paragraphs?: string[]
  note?: string
  noteTone?: 'info' | 'warning'
  action?: DocsSectionAction
  fields?: DocsField[]
  examples?: Partial<Record<DocsLanguage, string>>
  links?: { label: string; href?: string; page?: string }[]
}

export interface DocsPage {
  id: string
  category: DocsCategory
  title: string
  description: string
  quickActions?: DocsQuickAction[]
  summaryCards?: DocsSummaryCard[]
  sections: DocsSection[]
}

export const DOCS_DEFAULT_PAGE = 'quick-start'

// Examples follow the site being viewed, including an HTTP IP address and port.
// Never infer HTTPS, borrow a production host, or interpolate stored API keys.
export function resolveDocsOrigin(value: string): string {
  try {
    const url = new URL(value)
    if ((url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password) return url.origin
  } catch { /* The browser always supplies a valid origin; tests may not. */ }
  return 'http://localhost'
}

function requestExamples(origin: string, path: string, payload: Record<string, unknown>, anthropic = false): Partial<Record<DocsLanguage, string>> {
  const body = JSON.stringify(payload, null, 2)
  const headers = anthropic
    ? '-H "x-api-key: $API_KEY" \\\n  -H "anthropic-version: 2023-06-01"'
    : '-H "Authorization: Bearer $API_KEY"'
  return {
    curl: `curl ${origin}${path} \\\n  ${headers} \\\n  -H "Content-Type: application/json" \\\n  -d '${body}'`,
    python: `import os\nimport requests\n\nresponse = requests.post(\n    "${origin}${path}",\n    headers={${anthropic ? '"x-api-key": os.environ["API_KEY"], "anthropic-version": "2023-06-01"' : '"Authorization": "Bearer " + os.environ["API_KEY"]'}},\n    json=${JSON.stringify(payload, null, 4).replace(/\btrue\b/g, 'True').replace(/\bfalse\b/g, 'False').replace(/\bnull\b/g, 'None')},\n    timeout=3600,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    node: `const response = await fetch("${origin}${path}", {\n  method: "POST",\n  headers: {\n    ${anthropic ? '"x-api-key": process.env.API_KEY,\n    "anthropic-version": "2023-06-01",' : 'Authorization: \x60Bearer \x24{process.env.API_KEY}\x60,'}\n    "Content-Type": "application/json",\n  },\n  body: JSON.stringify(${body}),\n});\nif (!response.ok) throw new Error(\x60HTTP \x24{response.status}\x60);\nconsole.log(await response.json());`,
  }
}

function getExamples(origin: string, path: string): Partial<Record<DocsLanguage, string>> {
  return {
    curl: `curl ${origin}${path} \\\n  -H "Authorization: Bearer $API_KEY"`,
    python: `import os\nimport requests\n\nresponse = requests.get(\n    "${origin}${path}",\n    headers={"Authorization": "Bearer " + os.environ["API_KEY"]},\n    timeout=30,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    node: `const response = await fetch("${origin}${path}", {\n  headers: { Authorization: \x60Bearer \x24{process.env.API_KEY}\x60 },\n});\nif (!response.ok) throw new Error(\x60HTTP \x24{response.status}\x60);\nconsole.log(await response.json());`,
  }
}

function sessionCreateExamples(origin: string, path: string, payload: Record<string, unknown>): Partial<Record<DocsLanguage, string>> {
  const body = JSON.stringify(payload, null, 2)
  return {
    curl: `curl -X POST ${origin}${path} \\\n  -H "Authorization: Bearer $ACCESS_TOKEN" \\\n  -H "Idempotency-Key: $(uuidgen)" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(payload)}'`,
    python: `import os\nimport uuid\nimport requests\n\nresponse = requests.post(\n    "${origin}${path}",\n    headers={\n        "Authorization": "Bearer " + os.environ["ACCESS_TOKEN"],\n        "Idempotency-Key": str(uuid.uuid4()),\n    },\n    json=${JSON.stringify(payload, null, 4).replace(/\btrue\b/g, 'True').replace(/\bfalse\b/g, 'False').replace(/\bnull\b/g, 'None')},\n    timeout=30,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    node: `import { randomUUID } from "node:crypto";\n\nconst response = await fetch("${origin}${path}", {\n  method: "POST",\n  headers: {\n    Authorization: \x60Bearer \x24{process.env.ACCESS_TOKEN}\x60,\n    "Idempotency-Key": randomUUID(),\n    "Content-Type": "application/json",\n  },\n  body: JSON.stringify(${body}),\n});\nif (!response.ok) throw new Error(\x60HTTP \x24{response.status}\x60);\nconsole.log(await response.json());`,
  }
}

function multipartToolExamples(origin: string, path: string, fileName: string, mimeType: string, fields: Record<string, string> = {}): Partial<Record<DocsLanguage, string>> {
  return {
    curl: `curl --fail-with-body --no-buffer ${origin}${path} \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -F "file=@${fileName};type=${mimeType}"${Object.entries(fields).map(([name, value]) => ` \\\n  -F "${name}=${value}"`).join('')}`,
    python: `import os\nimport requests\n\nwith open("${fileName}", "rb") as file:\n    response = requests.post(\n        "${origin}${path}",\n        headers={"Authorization": "Bearer " + os.environ["API_KEY"]},\n        files={"file": ("${fileName}", file, "${mimeType}")},${Object.keys(fields).length ? `\n        data=${JSON.stringify(fields)},` : ''}\n        timeout=600,\n    )\nresponse.raise_for_status()\nresult = response.json()\nif result.get("error"):\n    raise RuntimeError(result["error"])\nprint(result)`,
    node: `import { readFile } from "node:fs/promises";\n\nconst form = new FormData();\nform.append("file", new Blob([await readFile("${fileName}")], { type: "${mimeType}" }), "${fileName}");${Object.entries(fields).map(([name, value]) => `\nform.append(${JSON.stringify(name)}, ${JSON.stringify(value)});`).join('')}\nconst response = await fetch("${origin}${path}", {\n  method: "POST",\n  headers: { Authorization: \x60Bearer \x24{process.env.API_KEY}\x60 },\n  body: form,\n});\nif (!response.ok) throw new Error(\x60HTTP \x24{response.status}\x60);\nconst result = await response.json();\nif (result.error) throw new Error(JSON.stringify(result.error));\nconsole.log(result);`,
  }
}

export function getApiDocs(locale: string, originValue: string): DocsPage[] {
  const zh = locale.toLowerCase().startsWith('zh')
  const l = (cn: string, en: string) => zh ? cn : en
  const origin = resolveDocsOrigin(originValue)
  const textRequest = { model: 'YOUR_MODEL_ID', messages: [{ role: 'user', content: 'Hello!' }] }
  const responseRequest = { model: 'YOUR_MODEL_ID', input: 'Hello!' }
  const messageRequest = { ...textRequest, max_tokens: 1024 }
  const imageGenerateRequest = { model: 'YOUR_IMAGE_MODEL', prompt: 'A red panda astronaut floating above a city at sunset', size: '1024x1024', n: 1 }
  const imageGifRequest = { model: 'gpt-image-2.6-sunburst', prompt: 'A small red fox running through a meadow, flat colors, seamless loop', size: '864x864', quality: 'high', output_format: 'gif', frame_count: 8, frame_duration_ms: 200 }
  const grokImageEditRequest = { model: 'grok-imagine-edit', prompt: 'Make it snow', image: { url: 'https://example.test/source.png' }, size: '1024x1024' }
  const videoCreateRequest = { model: 'grok-imagine-video', prompt: 'A slow drone shot over a misty lake at dawn', duration: 8, resolution: '720p' }
  const minimaxVideoRequest = { model: 'MiniMax-H3', prompt: 'A drone shot over a misty lake', seconds: '8', size: '1280x720' }
  const seedanceRequest = { model: 'YOUR_SEEDANCE_MODEL', content: [{ type: 'text', text: 'A wave breaking at sunset' }], duration: 5, resolution: '720p' }
  const nanoGenerateRequest = { contents: [{ role: 'user', parts: [{ text: 'A watercolor fox in a forest' }] }], generationConfig: { responseModalities: ['TEXT', 'IMAGE'], imageConfig: { aspectRatio: '1:1', imageSize: '2K' } } }
  const nanoEditRequest = { contents: [{ role: 'user', parts: [{ text: 'Turn this photo into a watercolor painting' }, { inlineData: { mimeType: 'image/png', data: 'BASE64_SOURCE_IMAGE' } }] }], generationConfig: { responseModalities: ['TEXT', 'IMAGE'] } }
  const commonModel: DocsField = { name: 'model', type: 'string', required: true, description: l('从本站模型列表复制完整模型 ID；可用模型取决于密钥绑定的分组。', 'Copy the complete model ID from this site. Availability depends on the groups attached to your API key.') }
  const streamField: DocsField = { name: 'stream', type: 'boolean', description: l('设为 true 接收 SSE 事件。客户端必须同时处理正常结束与错误终态。', 'Set to true for SSE events. Handle both successful completion and terminal errors.') }
  const deepseekToolAccess = l('当前独立工具入口要求密钥的入口分组为 DeepSeek 平台。仅把 DeepSeek 添加为其他平台入口的后备分组，仍可能得到 404。', 'The current independent tool handler requires a DeepSeek entry group. Merely adding a DeepSeek fallback to another platform’s entry group may still return 404.')
  const ttsRequest = { text: 'Hello, world!', voice_id: 'stella', format: 'wav', filter_markdown: true }
  return [
    {
      id: 'quick-start', category: 'start', title: l('首次调用 API', 'Your first API request'),
      description: l('使用本站创建的 API 密钥，选择可用模型，沿用熟悉的 SDK 或直接发送 HTTP 请求。', 'Use an API key created on this site, select an available model, and connect with your existing SDK or HTTP client.'),
      sections: [
        { id: 'connection', title: l('连接配置', 'Connection settings'), paragraphs: [l('示例地址自动跟随当前站点，包含协议和端口。OpenAI SDK 的 base_url 通常以 /v1 结尾；直接请求时使用完整端点，避免重复添加 /v1。', 'Example URLs follow the current site, including the scheme and port. OpenAI SDK base_url normally ends in /v1; direct HTTP calls use a complete endpoint. Do not append /v1 twice.')], note: l('请使用在本站创建的密钥。模型厂商的账户密码或其他站点的密钥不能代替本站 API 密钥。', 'Use a key created on this site. A model provider account password or a key from another site is not a substitute.'), fields: [
          { name: 'base_url', type: 'URL', description: `${origin}/v1` },
          { name: 'api_key', type: 'secret', description: l('登录控制台 → API 密钥，创建并复制密钥；不要放进公开网页或 Git 仓库。', 'Console → API keys: create and copy a key. Keep it out of public webpages and Git repositories.') }, commonModel,
        ] },
        { id: 'before-you-start', title: l('开始之前', 'Before you start'), paragraphs: [l('确认密钥已启用、余额或订阅可用，并绑定了支持目标模型的分组。先请求 GET /v1/models 核对精确模型 ID。', 'Confirm that your key is enabled, has available balance or subscription access, and is assigned to a group supporting your model. Check the exact model ID with GET /v1/models first.')], examples: { shell: `export API_KEY="YOUR_API_KEY"\ncurl ${origin}/v1/models -H "Authorization: Bearer $API_KEY"` } },
        { id: 'first-request', title: l('发送第一条消息', 'Send a message'), examples: requestExamples(origin, '/v1/chat/completions', textRequest) },
        { id: 'read-response', title: l('读取响应', 'Read the response'), paragraphs: [l('Chat Completions 的文本通常位于 choices[0].message.content。读取 usage 获取上游报告的用量；本站最终扣费以使用记录中的账单为准。', 'Chat Completions text normally appears in choices[0].message.content. usage contains provider-reported consumption; your finalized charge is shown in this site’s usage records.')], examples: { json: JSON.stringify({ id: 'chatcmpl_example', object: 'chat.completion', choices: [{ index: 0, message: { role: 'assistant', content: 'Hello!' }, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } }, null, 2) } },
        { id: 'media-generation', title: l('图像与视频生成', 'Image and video generation'), paragraphs: [l('图像生成、Nano Banana 与视频生成使用与聊天不同的入口，并且是否可用取决于密钥绑定的分组平台。按任务继续阅读：图像生成（OpenAI/Grok）、Nano Banana（Gemini 图像模型）与视频生成（Grok/MiniMax 异步任务）。', 'Image generation, Nano Banana, and video generation use endpoints separate from chat, and availability depends on the platform of the group bound to your key. Continue by task: image generation (OpenAI/Grok), Nano Banana (Gemini image models), and video generation (asynchronous Grok/MiniMax jobs).')], links: [
          { label: l('图像生成', 'Image generation'), page: 'image-generation' },
          { label: l('Nano Banana 图像模型', 'Nano Banana image models'), page: 'nano-banana' },
          { label: l('视频生成', 'Video generation'), page: 'video-generation' },
          { label: l('Agent 配置指南（含 CC Switch）', 'Agent setup guide (with CC Switch)'), page: 'agent-setup' },
        ] },
      ],
    },
    {
      id: 'authentication', category: 'start', title: l('密钥与鉴权', 'Keys and authentication'), description: l('同一个本站密钥用于模型接口及可用的独立工具接口。', 'A site API key authenticates model endpoints and available independent tool endpoints.'),
      quickActions: [
        { kind: 'createKey', variant: 'primary', label: l('快捷创建 API 密钥', 'Quick create API key'), unauthenticatedLabel: l('登录并创建 API 密钥', 'Sign in to create key') },
        { kind: 'doc', variant: 'secondary', page: 'key-self-service', label: l('用密钥查询自身信息', 'Key self-service API') },
        { kind: 'doc', variant: 'ghost', page: 'manage-keys', label: l('管理 API 密钥（控制台接口）', 'Console management API') },
      ],
      sections: [
        { id: 'bearer', title: 'Authorization: Bearer', paragraphs: [l('OpenAI 兼容协议、模型列表与独立 Search/Fetch/ASR/TTS/OCR 接口使用 Bearer 密钥。密钥继承所属用户、绑定分组、额度与访问限制。', 'Use Bearer authentication for OpenAI-compatible endpoints, model listing, and independent Search/Fetch/ASR/TTS/OCR. Your key is subject to its user, assigned groups, quotas, and access restrictions.')], examples: { shell: `curl ${origin}/v1/models \\\n  -H "Authorization: Bearer $API_KEY"` } },
        { id: 'anthropic-auth', title: l('Anthropic 原生鉴权', 'Anthropic authentication'), paragraphs: [l('/v1/messages 支持 x-api-key，搭配 anthropic-version: 2023-06-01。模型与分组还必须允许该协议入口。', '/v1/messages accepts x-api-key with anthropic-version: 2023-06-01. Your model and group must also allow this protocol endpoint.')], examples: { shell: `curl ${origin}/v1/messages \\\n  -H "x-api-key: $API_KEY" \\\n  -H "anthropic-version: 2023-06-01" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(messageRequest)}'` } },
        { id: 'safe-config', title: l('配置建议', 'Configuration notes'), paragraphs: [l('将密钥保存在客户端的私有环境变量或安全配置中。401 通常表示密钥无效；403 需结合错误正文判断分组权限、余额或业务限制。不要把所有错误都当成密钥失效。', 'Store your key in a private environment variable or secure client configuration. 401 generally indicates invalid authentication. For 403, inspect the error body for group permissions, balance, or policy restrictions; not every error means your key is invalid.')], links: [
          { label: l('用密钥查询自身信息（状态、额度、倍率与用量）', 'Key self-service API (status, limits, rates & usage)'), page: 'key-self-service' },
          { label: l('管理 API 密钥（控制台创建、修改与轮换接口）', 'Manage API keys (console create, update & rotate API)'), page: 'manage-keys' },
        ] },
      ],
    },
    {
      id: 'models', category: 'start', title: l('模型与价格', 'Models and pricing'), description: l('查询当前密钥真正可以调用的模型，而不是根据客户端图标猜测。', 'Discover models actually available to your API key rather than relying on client icons.'), sections: [
        { id: 'list-models', title: 'GET /v1/models', examples: { curl: `curl ${origin}/v1/models -H "Authorization: Bearer $API_KEY"`, json: JSON.stringify({ object: 'list', data: [{ id: 'YOUR_MODEL_ID', object: 'model' }] }, null, 2) }, paragraphs: [l('响应 data[].id 是请求 model 字段所需的完整标识。绑定分组与上游可用性会影响实际可调用范围。', 'Use the complete data[].id value as the model field. Assigned groups and provider availability affect what can actually be called.')] },
        { id: 'pricing', title: l('了解计费', 'Understand billing'), paragraphs: [l('模型价格、缓存读写价格、分组倍率和工具价格以本站配置为准。使用记录分别保留标准费用和实际扣费；费用已计量不代表内容一定生成成功。', 'Model prices, cache prices, group multipliers, and tool fees follow this site’s configuration. Usage records retain both base cost and actual charges. Metered consumption is not a guarantee of successful delivery.'), l('Search/Fetch 独立调用按成功次数固定计费，不随模型 Token 倍率变化；模型内搜索与抓取的成功工具费固定，模型 Token 仍按适用倍率计算。失败或空结果不计成功工具调用次数。', 'Independent Search/Fetch calls use a fixed fee per successful call, separate from model token multipliers. Hosted search/fetch tool fees are fixed while model tokens retain the applicable multiplier. Failed or empty tools do not count as successful tool calls.'), l('独立 ASR 按音频时长、TTS 按请求文本的计费字符、OCR 按识别成功的页数计费。分组可以单独定价，这三项固定工具价不乘分组倍率，具体规则见对应接口页。', 'Independent ASR is billed by audio duration, TTS by billable characters in the request text, and OCR by successfully recognized pages. Groups can override each price; these fixed tool prices do not use the group multiplier. See each endpoint for details.')] },
      ],
    },
    {
      id: 'streaming', category: 'guides', title: l('流式输出与错误', 'Streaming and errors'), description: l('流式内容逐段到达；判断成功必须等待协议的最终结果。', 'Streaming delivers content incrementally. Determine success from the protocol’s final outcome.'), sections: [
        { id: 'enable-streaming', title: l('启用 SSE', 'Enable SSE'), paragraphs: [l('请求中设置 stream: true，并持续按 SSE 帧读取。不要等待整个响应下载完再显示，也不要把每条 data 行当成完整 JSON 响应。', 'Set stream: true and consume SSE frames continuously. Do not wait for the entire response before displaying content, and do not treat every data line as a complete HTTP JSON response.')], examples: { curl: `curl -N ${origin}/v1/chat/completions \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ ...textRequest, stream: true })}'` } },
        { id: 'terminal-events', title: l('识别终态', 'Recognize terminal events'), paragraphs: [l('Responses 以 response.completed 表示正常结束，response.failed 或 error 表示失败。Anthropic Messages 以完整 message_stop 表示结束；error 事件本身是错误终态，后续断开不能把它改成成功。Chat Completions 需检查错误帧与正常结束标记。', 'Responses uses response.completed for successful completion and response.failed or error for failure. Anthropic Messages ends with a complete message_stop; an error event is a failure terminal state, even if the connection then closes. For Chat Completions, handle error frames as well as normal completion markers.')] },
        { id: 'stream-errors', title: l('HTTP 200 之后仍可能失败', 'A stream can fail after HTTP 200'), note: l('响应头发送后，HTTP 状态可能已经固定为 200；客户端仍必须检查流内错误码和消息。错误码不应被当成另一条 HTTP 响应状态。', 'After headers are committed, the HTTP status may remain 200. Clients must still inspect in-stream error codes and messages; an error code is not a second HTTP response status.'), examples: { shell: 'event: error\ndata: {"type":"error","error":{"type":"relay_stream_error","message":"stream relay failure: scanner_error: unexpected EOF"}}\n\n' }, paragraphs: [l('unexpected EOF 表示流未完整结束。已发送内容无法撤回；开始输出后不能把另一条重试响应直接拼上去。本站保留已观测的真实用量计费，并在存在可关联错误时提示“关联错误（已计量）”。', 'unexpected EOF means the stream ended incompletely. Already delivered content cannot be recalled, and a new retry response must not be spliced into an existing stream. Observed usage remains billable; usage records can show a related metered error when retained diagnostics can be correlated.')] },
      ],
    },
    {
      id: 'tools', category: 'guides', title: l('工具调用与网页工具', 'Tool calls and web tools'), description: l('区分客户端执行的函数工具与平台执行的网页搜索、抓取。', 'Distinguish client-executed functions from hosted web search and fetch tools.'), sections: [
        { id: 'function-tools', title: l('函数工具', 'Function tools'), paragraphs: [l('模型返回工具调用参数后，由你的应用校验并执行函数，再把 tool 结果连同对应 tool_call_id 发送回模型。工具调用本身不是用户可读的最终答案。', 'Validate and execute a function after the model returns its arguments, then send a tool result with the matching tool_call_id. A tool call itself is not the final user-facing answer.')], examples: { json: JSON.stringify({ model: 'YOUR_MODEL_ID', messages: [{ role: 'user', content: 'What is the weather?' }], tools: [{ type: 'function', function: { name: 'get_weather', description: 'Get the current weather for a city.', parameters: { type: 'object', properties: { city: { type: 'string' } }, required: ['city'] } } }] }, null, 2) } },
        { id: 'hosted-web', title: l('模型内网页工具', 'Hosted web tools'), paragraphs: [l('在本站已配置且支持的 DeepSeek 分组中，平台可向模型提供 web_search 与 web_fetch，并执行网页请求、把结果交还模型。是否启用取决于密钥、分组与上游设置。', 'When configured for a supported DeepSeek group, this site can offer web_search and web_fetch to the model, execute requests, and return results to the model. Availability depends on key, group, and upstream settings.'), l('网页内容属于外部不可信资料。不要让网页指令覆盖应用权限、系统规则或泄露密钥。需要确定性搜索或只获取正文时，可直接调用独立 Search/Fetch API，不必启动模型。', 'Web content is untrusted external data. Do not let page instructions override application permissions, system rules, or disclose credentials. Use the independent Search/Fetch APIs when you need direct results without a model.')] },
        { id: 'independent-tools', title: l('独立工具接口', 'Independent tool endpoints'), paragraphs: [l('DeepSeek 入口分组还可直接调用 ASR 语音识别、TTS 语音合成与 OCR 文档识别。它们和 Search/Fetch 一样使用本站 Bearer 密钥，不需要 model、messages 或模型工具调用。', 'DeepSeek entry groups also offer direct ASR transcription, TTS speech synthesis, and OCR document recognition. Like Search/Fetch, they use a site Bearer key without model, messages, or a model-generated tool call.')], links: [
          { label: l('语音识别 ASR', 'Speech recognition ASR'), page: 'deepseek-asr' },
          { label: l('语音合成 TTS', 'Speech synthesis TTS'), page: 'deepseek-tts' },
          { label: l('文档识别 OCR', 'Document recognition OCR'), page: 'deepseek-ocr' },
        ] },
      ],
    },
    {
      id: 'image-generation', category: 'media', title: l('图像生成', 'Image generation'),
      description: l('通过 OpenAI 兼容的 Images 接口生成与编辑图片，并区分 OpenAI 与 Grok 分组的模型、响应和限制。', 'Generate and edit images through the OpenAI-compatible Images API, including the model, response, and limit differences between OpenAI and Grok groups.'),
      sections: [
        { id: 'image-overview', title: l('接口与可用性', 'Endpoints and availability'), paragraphs: [
          l('图像生成使用 POST /v1/images/generations，图像编辑使用 POST /v1/images/edits。鉴权方式与其他 OpenAI 兼容接口相同：请求头携带 Authorization: Bearer $API_KEY。', 'Use POST /v1/images/generations to generate images and POST /v1/images/edits to edit them. Authentication matches other OpenAI-compatible endpoints: send Authorization: Bearer $API_KEY.'),
          l('接口是否可用由密钥绑定的分组平台决定。OpenAI 分组使用 gpt-image-* 模型，Grok 分组使用 xAI Imagine 图像模型；其他平台（例如 Gemini、MiniMax）会返回 404 "Images API is not supported for this platform"。分组还必须开启图像生成权限，否则返回 403 permission_error "Image generation is not enabled for this group"。', 'Availability depends on the platform of the group bound to your API key. OpenAI groups use gpt-image-* models and Grok groups use xAI Imagine image models. Other platforms, such as Gemini or MiniMax, return 404 "Images API is not supported for this platform". The group must also allow image generation; otherwise the request returns 403 permission_error "Image generation is not enabled for this group".'),
          l('具体模型与能力由站点运营者配置的分组和上游账号决定。请先用 GET /v1/models 获取当前密钥可用的模型 ID，不要把页面中的示例模型当作固定清单。', 'The exact models and capabilities are configured by the site operator through groups and upstream accounts. Use GET /v1/models to discover the model IDs available to your key instead of treating the examples here as a fixed catalog.'),
        ], note: l('404 表示该密钥的分组平台不提供 Images 接口；403 表示接口存在但分组未开启图像生成。不要仅凭状态码判断密钥失效。', 'A 404 means the Images API is not offered for your key’s group platform. A 403 means the endpoint exists but image generation is not enabled for the group. Do not treat either as proof that the key is invalid.') },
        { id: 'image-request', title: l('生成图片', 'Generate an image'), paragraphs: [
          l('JSON 与 multipart/form-data 均可使用。下面的示例发送 JSON：model 从本站模型列表复制，prompt 描述画面，size 与 n 按需调整。', 'Both JSON and multipart/form-data are accepted. The example below sends JSON: copy model from this site’s model list, describe the image in prompt, and adjust size and n as needed.'),
        ], fields: [
          { name: 'model', type: 'string', description: l('图片模型 ID。OpenAI 分组可填 gpt-image-*（例如 gpt-image-2）；Grok 分组可填 grok-imagine-image、grok-imagine-image-quality、grok-imagine-image-2.0 或别名 grok-imagine。OpenAI 路径省略时默认 gpt-image-2，Grok 路径必须显式提供；不受支持的模型返回 400。', 'Image model ID. OpenAI groups accept gpt-image-* (for example gpt-image-2); Grok groups accept grok-imagine-image, grok-imagine-image-quality, grok-imagine-image-2.0, or the grok-imagine alias. The OpenAI path defaults to gpt-image-2 when omitted; Grok paths require it explicitly. Unsupported models return a 400.') },
          { name: 'prompt', type: 'string', required: true, description: l('描述要生成的画面。', 'Describe the image to generate.') },
          { name: 'n', type: 'integer', description: l('生成数量，默认 1。Grok 路径原样转发，建议每次请求一张以简化计费与重试。', 'Number of images, default 1. Grok forwards the value as-is; one image per request keeps billing and retries simple.') },
          { name: 'size', type: 'string', description: l('图片尺寸，例如 1024x1024。Grok 分组会把 size 换算为 resolution（1k/2k）和派生的 aspect_ratio 后转发。', 'Image size, for example 1024x1024. Grok groups convert size to resolution (1k/2k) plus a derived aspect_ratio before forwarding.') },
          { name: 'response_format', type: 'string', description: l('OpenAI 路径默认按 b64_json 返回并回填；设为 url 时跳过回填、保留上游 URL。Grok 路径是否接受该字段由上游决定。', 'The OpenAI path returns and backfills b64_json by default; url skips the backfill and keeps upstream URLs. Whether Grok accepts this field is up to upstream.') },
          { name: 'quality', type: 'string', description: l('质量档位，随请求转发；可选值取决于模型与上游。', 'Quality level, forwarded with the request. Accepted values depend on the model and upstream.') },
          { name: 'background', type: 'string', description: l('背景选项，随请求转发；是否生效由模型与上游决定。', 'Background option, forwarded with the request. Whether it applies depends on the model and upstream.') },
          { name: 'output_format', type: 'string', description: l('输出格式，随请求转发；上游支持范围可能不同。', 'Output format, forwarded with the request. Upstream support can vary.') },
          { name: 'moderation', type: 'string', description: l('审核选项，随请求转发。', 'Moderation option, forwarded with the request.') },
          { name: 'input_fidelity', type: 'string', description: l('输入保真选项，随请求转发。', 'Input fidelity option, forwarded with the request.') },
          { name: 'style', type: 'string', description: l('风格选项，随请求转发。', 'Style option, forwarded with the request.') },
          { name: 'output_compression', type: 'integer', description: l('输出压缩参数，随请求转发。', 'Output compression parameter, forwarded with the request.') },
          { name: 'partial_images', type: 'integer', description: l('配合 stream 使用的部分图片数量；具体范围由上游决定。', 'Number of partial images used with stream. The exact range is defined by upstream.') },
          { name: 'stream', type: 'boolean', description: l('OpenAI 路径在上游返回 text/event-stream 时以 SSE 转发，否则按普通 JSON 返回一次结果；Grok 路径会缓冲上游响应后一次返回 JSON，不提供 SSE。', 'The OpenAI path relays SSE when upstream answers with text/event-stream; otherwise it returns one JSON body. Grok buffers the upstream response and returns one JSON body with no SSE.') },
        ], examples: requestExamples(origin, '/v1/images/generations', imageGenerateRequest) },
        { id: 'image-response', title: l('响应结构', 'Response structure'), paragraphs: [
          l('OpenAI 分组的非流式响应默认返回 data[].b64_json：若上游只返回 data[].url，网关会下载图片并回填 b64_json，同时移除 url，避免暴露上游地址。请求显式设置 response_format: "url" 时会跳过回填，响应保留 data[].url。单项回填失败时该项可能仍保留上游 URL，客户端应同时兼容 b64_json 与 url。', 'For OpenAI groups, non-streaming responses return data[].b64_json by default. When upstream only returns data[].url, the gateway downloads the image, backfills b64_json, and removes url so upstream addresses are not exposed. Setting response_format: "url" skips the backfill and keeps data[].url. If backfilling one item fails, that item may still carry the upstream URL, so clients should accept both b64_json and url.'),
          l('Grok 分组的响应按上游原样返回，通常为 { "data": [ { "url": "..." } ] }，不保证包含 b64_json、revised_prompt 或 usage；这些字段只有上游确实返回时才会出现。若上游没有返回任何图片结果，网关以 502 结束请求。', 'Grok responses are passed through as returned by upstream and normally look like { "data": [ { "url": "..." } ] }. b64_json, revised_prompt, and usage are not guaranteed and appear only when upstream actually returns them. If upstream returns no image result at all, the gateway ends the request with a 502.'),
        ], examples: { json: JSON.stringify({ created: 1700000000, data: [{ b64_json: 'BASE64_IMAGE_DATA' }] }, null, 2) } },
        { id: 'image-edits', title: l('编辑图片（OpenAI 分组）', 'Edit images (OpenAI groups)'), paragraphs: [
          l('OpenAI 分组的 JSON 编辑请求中，images 数组的每一项必须是 { "image_url": "..." }；file_id 不受支持，会返回 400。mask 可选，同样使用 { "image_url": "..." }。也可以发送 multipart 请求，文件部分使用 image（可重复）与 mask，并携带 model、prompt 等文本字段。', 'For JSON edits on OpenAI groups, each images entry must be { "image_url": "..." }; file_id is not supported and returns a 400. mask is optional and also uses { "image_url": "..." }. Multipart edits are also accepted: send the source file part as image (repeatable) and an optional mask, along with text fields such as model and prompt.'),
          l('下面的示例使用 multipart 上传源图。JSON 编辑请求的最小结构为 { "model": "YOUR_IMAGE_MODEL", "prompt": "...", "images": [{ "image_url": "https://example.test/source.png" }] }。', 'The example below uploads a source image with multipart. A minimal JSON edit body is { "model": "YOUR_IMAGE_MODEL", "prompt": "...", "images": [{ "image_url": "https://example.test/source.png" }] }.'),
        ], fields: [
          { name: 'images', type: 'array', required: true, description: l('JSON 编辑的源图数组，元素为 { "image_url": "..." }；不支持 file_id。', 'Source-image array for JSON edits, with entries shaped as { "image_url": "..." }; file_id is not supported.') },
          { name: 'mask', type: 'object', description: l('可选蒙版，{ "image_url": "..." }；不支持 mask.file_id。', 'Optional mask, { "image_url": "..." }; mask.file_id is not supported.') },
          { name: 'image', type: 'file', description: l('multipart 编辑的源图文件部分，可重复发送多张。', 'Source file part for multipart edits; it may be repeated.') },
          { name: 'mask (multipart)', type: 'file', description: l('multipart 编辑的可选蒙版文件部分。', 'Optional mask file part for multipart edits.') },
        ], examples: { curl: `curl ${origin}/v1/images/edits \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -F "model=gpt-image-2" \\\n  -F "prompt=Replace the background with a beach" \\\n  -F "image=@source.png" \\\n  -F "size=1024x1024"` } },
        { id: 'image-grok-edits', title: l('编辑图片（Grok 分组）', 'Edit images (Grok groups)'), paragraphs: [
          l('Grok 分组接受 JSON 编辑请求。参考图可以放在 image、images、reference_images、first_frame 或 last_frame 中，元素可以是 URL 字符串，也可以是 { "url": "..." } 或 { "image_url": ... } 形式；mask 可选。网关会统一改写为 { "url": "...", "type": "image_url" } 后转发。每次编辑最多使用 3 张源图，超过时应拆分成多次请求。', 'Grok groups accept JSON edit requests. Reference images may be passed in image, images, reference_images, first_frame, or last_frame, as a URL string or as { "url": "..." } / { "image_url": ... }. mask is optional. The gateway rewrites references to { "url": "...", "type": "image_url" } before forwarding. Each edit uses at most 3 source images; split larger sets into multiple requests.'),
          l('下列示例使用单张参考图。mask 会被解析并转发，但上游是否按蒙版生效取决于模型与上游。', 'The example below uses a single reference image. mask is parsed and forwarded, but whether upstream honors it depends on the model and provider.'),
        ], examples: requestExamples(origin, '/v1/images/edits', grokImageEditRequest) },
        { id: 'image-models', title: l('模型系列', 'Model families'), paragraphs: [
          l('OpenAI 分组使用 gpt-image-* 系列，model 省略时默认 gpt-image-2，其他前缀返回 400。Grok 分组使用 Grok Imagine 图像模型：grok-imagine-image（快速）、grok-imagine-image-quality（质量）、grok-imagine-image-2.0，别名 grok-imagine 会归一化为质量模型；编辑也可使用 grok-imagine-edit 别名。Grok 分组必须显式提供 model，不会套用默认值。', 'OpenAI groups use the gpt-image-* family; model defaults to gpt-image-2 when omitted, and other prefixes return a 400. Grok groups use Grok Imagine image models: grok-imagine-image (fast), grok-imagine-image-quality (quality), and grok-imagine-image-2.0; the grok-imagine alias is normalized to the quality model, and edits may use the grok-imagine-edit alias. Grok groups must send model explicitly; no default is applied.'),
          l('专用图片模型请使用对应媒体端点调用；例如 gpt-image-* 在 /v1/chat/completions 和 /v1/messages 上会被拒绝。Nano Banana 等 Gemini 图像模型使用独立的 Gemini 原生入口，见下一页。', 'Use the matching media endpoint for dedicated image models; gpt-image-*, for example, is rejected on /v1/chat/completions and /v1/messages. Gemini image models such as Nano Banana use the separate native Gemini endpoint covered on the next page.'),
        ] },
        { id: 'image-gif', title: l('生成 GIF 动图（gpt-image-2.6）', 'Generate a GIF animation (gpt-image-2.6)'), paragraphs: [
          l('gpt-image-2.6-sunburst 与 gpt-image-2.6-flare 可以直接输出 GIF 动图：在 Images 请求里加 output_format: "gif"、frame_count 和 frame_duration_ms，prompt、size、quality 与普通生图相同。frame_count 与 frame_duration_ms 写数字或带引号的数字都可以，"8" 与 8 等价。', 'gpt-image-2.6-sunburst and gpt-image-2.6-flare can return real GIF animations: add output_format: "gif", frame_count, and frame_duration_ms to an Images request; prompt, size, and quality work as usual. Both frame fields accept a number or a quoted number — "8" and 8 are equivalent.'),
          l('上游会先把所有帧拼成一张方形工作表再编码：2~4 帧是 2×2，5~9 帧是 3×3，10~16 帧是 4×4，工作表单边不能超过 3840 像素。换成单帧最长边上限：2~4 帧 1920、5~9 帧 1280、10~16 帧 960。因此 1024×1024 的 16 帧会被拒绝（4×4 = 4096）；16 帧请用 960×960 或 864×864，8 帧可以到 1280×1280。', 'Upstream first packs every frame into one square sheet and then encodes it: 2-4 frames use 2×2, 5-9 frames use 3×3, 10-16 frames use 4×4, and the sheet edge may not exceed 3840 px. The resulting per-frame longest-edge limits are 1920 for 2-4 frames, 1280 for 5-9, and 960 for 10-16. So 16 frames at 1024×1024 is rejected (4×4 = 4096); use 960×960 or 864×864 for 16 frames, and up to 1280×1280 for 8 frames.'),
          l('响应仍是 data[].b64_json，内容是 GIF 字节（文件头 GIF89a）。客户端要 base64 解码后按 .gif 保存；如果解码出来是 PNG 且只有一帧，说明这次被上游当成普通图片处理、GIF 参数没有生效，换模型或减少帧数重试。', 'The response is still data[].b64_json, but the bytes are a GIF (header GIF89a). Decode the base64 and save it as .gif. If you get a single-frame PNG instead, the upstream treated the call as a still image and ignored the animation fields — switch models or reduce the frame count and retry.'),
        ], note: l('部分转接上游会用 HTTP 200 包着纯文本错误返回，例如 No supported sheet fits 16 GIF frames at size "1024x1024"。只看状态码会误判成功：响应体不是 JSON、或解不出图片时请按失败处理，并按上面的尺寸/帧数上限调整。', 'Some relays wrap a plain-text error in an HTTP 200 response, for example: No supported sheet fits 16 GIF frames at size "1024x1024". Checking only the status code will wrongly report success; treat a non-JSON body or an undecodable image as a failure and adjust size/frame count using the limits above.'), fields: [
          { name: 'output_format', type: 'string', description: l('填 gif 才返回动图；其他值按普通图片返回。', 'Set to gif to get an animation; other values return a still image.') },
          { name: 'frame_count', type: 'integer | string', description: l('帧数，常用 2~16；与 size 一起受 3840 像素工作表限制。', 'Frame count, commonly 2-16; together with size it must fit the 3840 px sheet limit.') },
          { name: 'frame_duration_ms', type: 'integer | string', description: l('每帧时长（毫秒），常用 20~2000，决定播放速度。', 'Milliseconds per frame, commonly 20-2000; controls playback speed.') },
        ], examples: requestExamples(origin, '/v1/images/generations', imageGifRequest) },
        { id: 'image-async', title: l('异步任务', 'Asynchronous tasks'), paragraphs: [
          l('同步请求可能超过代理或 CDN 的超时时间时，可以使用异步图片任务：POST /v1/images/generations/async 与 POST /v1/images/edits/async 接受与同步端点相同的 JSON 或 multipart 请求体，成功受理返回 202 Accepted 并提供 poll_url；随后用 GET /v1/images/tasks/{task_id} 查询结果。', 'When a synchronous request may exceed a proxy or CDN timeout, use asynchronous image tasks: POST /v1/images/generations/async and POST /v1/images/edits/async accept the same JSON or multipart bodies as their synchronous counterparts, return 202 Accepted with a poll_url, and are queried with GET /v1/images/tasks/{task_id}.'),
          l('该功能默认关闭，只有运营者配置了对象存储后才可用；关闭时端点返回 404 "async image tasks are not enabled"。流式请求不能提交为异步任务。轮询必须使用提交任务的同一个密钥；状态包括 processing、billing、completed、failed，完成后的 result 中图片地址指向对象存储，b64_json 会被移除。', 'The feature is disabled by default and becomes available only after the operator configures object storage; while disabled the endpoints return 404 "async image tasks are not enabled". Streaming requests cannot be submitted as asynchronous tasks. Poll with the same API key that submitted the task; statuses include processing, billing, completed, and failed. Completed results point image URLs at object storage and have b64_json removed.'),
        ], examples: requestExamples(origin, '/v1/images/generations/async', imageGenerateRequest) },
        { id: 'image-batches', title: l('批量图片', 'Batch images'), paragraphs: [
          l('批量图片接口位于 /v1/images/batches*，仅面向 Gemini 平台分组：需要运营者启用批量图片功能并给分组授予批量权限，否则返回 404 或 403。提交体包含 model、可选 provider（gemini_api 或 vertex）以及 items[]；每个 item 提供 custom_id、prompt，以及可选的 output_count 与 reference_images[]（mime_type 加 base64 data，或内部 file_uri）。任务通过 GET /v1/images/batches/{id}、items 与 download 接口查询和下载。', 'Batch image endpoints live under /v1/images/batches*. They are available only to Gemini-platform groups: the operator must enable batch image processing and grant the group batch permission, otherwise the API returns 404 or 403. A submission contains model, an optional provider (gemini_api or vertex), and items[]; each item provides custom_id, prompt, and optional output_count and reference_images[] (mime_type plus base64 data, or an internal file_uri). Query and download jobs with GET /v1/images/batches/{id}, its items, and download endpoints.'),
          l('参考图数量、输出数量、请求大小等上限属于站点侧保护值，并可能随配置与模型变化；提交前请以本站返回的错误信息和运营者配置为准。批量接口没有不带 /v1 前缀的别名。', 'Reference-image counts, output counts, and request-size limits are site-side guardrails that can vary with configuration and model. Follow the errors returned by this site and the operator’s configuration. Batch routes have no aliases without the /v1 prefix.'),
        ] },
        { id: 'image-caveats', title: l('注意事项', 'Caveats'), paragraphs: [
          l('内容策略拒绝会以错误对象返回，错误信息来自上游；重试不能绕过策略。模型可用性、价格与并发限制由本站配置，本页不列出具体费用。', 'Content-policy refusals are returned as error objects with upstream messages; retries do not bypass policy. Model availability, pricing, and concurrency limits are configured by this site, and this page lists no fee amounts.'),
          l('GIF 等输出选项是否生效取决于上游账号。请求 output_format 为 gif（以及 jpeg、webp、svg）时，上游可能返回 400 "GIF output is not enabled for this request."，也可能不报错、以 HTTP 200 返回一张 PNG：frame_count、frame_duration_ms、n 等参数可能被忽略，请求仍按成功计费。本站只按请求透传这些字段，请检查返回图片的实际格式，不要只凭状态码判断是否得到 GIF。', 'Whether output options such as GIF take effect depends on the upstream account. When output_format is set to gif (or jpeg, webp, svg), upstream may return a 400 "GIF output is not enabled for this request.", or it may report no error and return a PNG with HTTP 200: frame_count, frame_duration_ms, n, and similar parameters may be ignored, and the request is still billed as successful. This site only forwards those fields; check the actual format of the returned image instead of judging GIF success from the status code alone.'),
          l('Grok 返回的上游图片链接可能过期，请在收到响应后尽快保存，或转存到自己的对象存储。', 'Upstream image links returned by Grok can expire; save images promptly after receiving a response, or copy them to your own object storage.'),
        ], links: [
          { label: l('Nano Banana 图像模型', 'Nano Banana image models'), page: 'nano-banana' },
          { label: l('视频生成', 'Video generation'), page: 'video-generation' },
        ] },
      ],
    },
    {
      id: 'nano-banana', category: 'media', title: l('Nano Banana 图像模型', 'Nano Banana image models'),
      description: l('通过 Gemini 原生 generateContent 接口调用 Gemini 图像模型，直接读取 inlineData 中的图像字节。', 'Call Gemini image models through the native Gemini generateContent endpoint and read image bytes from inlineData.'),
      sections: [
        { id: 'nano-overview', title: l('接口与可用性', 'Endpoints and availability'), paragraphs: [
          l('Nano Banana 是社区对 Gemini 图像生成能力的昵称，不是模型 ID。本站通过 Gemini 原生协议提供这些模型：POST /v1beta/models/{model}:generateContent，流式调用使用同一路由的 :streamGenerateContent。', 'Nano Banana is a community nickname for Gemini image-generation capabilities, not a model ID. This site exposes them through the native Gemini protocol: POST /v1beta/models/{model}:generateContent, with :streamGenerateContent on the same route for streaming.'),
          l('分组平台必须是 gemini 或 antigravity，并且模型在分组的模型白名单内；否则返回 400 "API key group platform is not gemini" 或 404 "Model is not available for this group"。这类模型不能通过 /v1/images/generations 调用，该端点只服务 OpenAI 与 Grok 分组。', 'The group platform must be gemini or antigravity, and the model must be allowed by the group’s model allowlist; otherwise the request returns 400 "API key group platform is not gemini" or 404 "Model is not available for this group". These models are not available through /v1/images/generations, which only serves OpenAI and Grok groups.'),
          l('鉴权接受 x-goog-api-key、Authorization: Bearer 或 x-api-key；/v1beta 路径也接受查询参数 key。可用模型以 GET /v1beta/models 返回为准。常见图像模型 ID 包括 gemini-2.5-flash-image 与 gemini-3.1-flash-image，别名 gemini-3-pro-image 会映射到 gemini-3.1-flash-image；具体账号能否使用由运营者与上游决定。', 'Authentication accepts x-goog-api-key, Authorization: Bearer, or x-api-key; /v1beta paths also accept the key query parameter. Use GET /v1beta/models for available models. Common image model IDs include gemini-2.5-flash-image and gemini-3.1-flash-image, and the gemini-3-pro-image alias maps to gemini-3.1-flash-image. Whether a specific account can use them is decided by the operator and upstream.'),
        ], note: l('网关不会替你注入 responseModalities；客户端必须在 generationConfig 中显式请求 ["TEXT","IMAGE"]，否则响应可能只包含文本。', 'The gateway does not inject responseModalities for you; the client must explicitly request ["TEXT","IMAGE"] in generationConfig, otherwise the response may contain text only.') },
        { id: 'nano-request', title: l('发起生成请求', 'Send a generation request'), paragraphs: [
          l('请求体是官方 Gemini JSON：contents[] 承载对话与图片输入，generationConfig.responseModalities 声明需要文本与图片。imageConfig 可按模型支持情况指定宽高比与图片尺寸。', 'The body is official Gemini JSON: contents[] carries the conversation and image input, while generationConfig.responseModalities declares that text and images are wanted. imageConfig can request an aspect ratio and image size when the model supports them.'),
        ], fields: [
          { name: 'contents', type: 'array', required: true, description: l('官方 contents[]，通常包含 role: "user" 与 parts[]；文本放在 parts[].text。', 'Official contents[], normally with role: "user" and parts[]; text goes in parts[].text.') },
          { name: 'generationConfig.responseModalities', type: 'string[]', required: true, description: l('必须包含 "TEXT" 与 "IMAGE"，否则模型可能只返回文本。', 'Must include "TEXT" and "IMAGE"; otherwise the model may return text only.') },
          { name: 'generationConfig.imageConfig.aspectRatio', type: 'string', description: l('可选宽高比，例如 1:1、16:9、9:16、4:3、3:4；上游不支持时可能忽略或报错。', 'Optional aspect ratio such as 1:1, 16:9, 9:16, 4:3, or 3:4. Unsupported values may be ignored or rejected upstream.') },
          { name: 'generationConfig.imageConfig.imageSize', type: 'string', description: l('可选图片尺寸，例如 1K、2K、4K；实际支持范围由模型与上游决定。', 'Optional image size such as 1K, 2K, or 4K. The supported range depends on the model and upstream.') },
          { name: 'model（URL 段）', type: 'string', required: true, description: l('放在 URL 中，例如 /v1beta/models/gemini-2.5-flash-image:generateContent。', 'Placed in the URL, for example /v1beta/models/gemini-2.5-flash-image:generateContent.') },
        ], examples: {
          curl: `curl ${origin}/v1beta/models/gemini-2.5-flash-image:generateContent \\\n  -H "x-goog-api-key: $API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(nanoGenerateRequest, null, 2)}'`,
          python: `import os
import requests

response = requests.post(
    "${origin}/v1beta/models/gemini-2.5-flash-image:generateContent",
    headers={"x-goog-api-key": os.environ["API_KEY"]},
    json=${JSON.stringify(nanoGenerateRequest, null, 4)},
    timeout=300,
)
response.raise_for_status()
print(response.json())`,
        } },
        { id: 'nano-response', title: l('读取图像结果', 'Read the image result'), paragraphs: [
          l('图像位于 candidates[].content.parts[].inlineData，包含 mimeType 与 data；data 是不带 data: 前缀的 base64。parts 也可能同时包含文本，请遍历数组而不是固定取第一个元素。', 'Images arrive in candidates[].content.parts[].inlineData with mimeType and data; data is base64 without a data: prefix. parts may also contain text, so iterate the array instead of assuming the first element.'),
          l('usageMetadata 报告 promptTokenCount、candidatesTokenCount 等用量，图像 Token 计入 candidatesTokensDetails。把 data 按 base64 解码保存即可，例如 echo "$IMAGE_BASE64" | base64 -d > image.png。', 'usageMetadata reports usage such as promptTokenCount and candidatesTokenCount, and image tokens appear in candidatesTokensDetails. Save the image by decoding data as base64, for example echo "$IMAGE_BASE64" | base64 -d > image.png.'),
          l(':streamGenerateContent 返回官方 Gemini 流式响应，按 SSE 数据帧读取；示例不再展开流式解析。', ':streamGenerateContent returns the official Gemini streaming response as SSE data frames; this page does not expand the streaming parser.'),
        ], examples: { json: JSON.stringify({ candidates: [{ content: { role: 'model', parts: [{ text: 'Here is your image:' }, { inlineData: { mimeType: 'image/png', data: 'BASE64_IMAGE_DATA' } }] }, finishReason: 'STOP' }], usageMetadata: { promptTokenCount: 12, candidatesTokenCount: 1290 } }, null, 2) } },
        { id: 'nano-edit', title: l('参考图编辑', 'Edit with reference images'), paragraphs: [
          l('把参考图作为 inlineData 放进输入 contents[].parts[]，与文本提示一起发送即可。data 为不带 data: 前缀的 base64，mimeType 必须与图片内容匹配；参考图张数、大小与可接受格式由模型和上游决定。', 'Put reference images in the input contents[].parts[] as inlineData alongside the text prompt. data is base64 without a data: prefix and mimeType must match the image bytes. The number, size, and accepted formats of reference images depend on the model and upstream.'),
          l('如果需要大量参考图或批量生成，请改用批量图片接口 /v1/images/batches，它要求 Gemini 平台分组并受站点侧上限约束，详见图像生成页。', 'For many reference images or batch generation, use the batch image API /v1/images/batches instead; it requires a Gemini-platform group and is subject to site-side limits, as described on the image generation page.'),
        ], examples: { json: JSON.stringify(nanoEditRequest, null, 2) } },
        { id: 'nano-caveats', title: l('注意事项', 'Caveats'), paragraphs: [
          l('Gemini 图像以 /v1beta 原生响应中的 inlineData 为准；其他兼容入口（例如 /v1/messages）不保证返回图像内容，请不要依赖。', 'Gemini images are delivered as inlineData in the native /v1beta response; other compatibility endpoints such as /v1/messages do not guarantee image content and should not be relied on.'),
          l('模型是否可用取决于分组白名单与上游账号类型，本页不承诺所有 Gemini 分组都能生成图像。计费按站点配置的 Token 规则计算，本页不列出价格。', 'Model availability depends on the group allowlist and upstream account type; this page does not promise that every Gemini group can generate images. Billing follows this site’s configured token rules, and no prices are listed here.'),
        ], links: [
          { label: l('图像生成', 'Image generation'), page: 'image-generation' },
          { label: l('视频生成', 'Video generation'), page: 'video-generation' },
        ] },
      ],
    },
    {
      id: 'video-generation', category: 'media', title: l('视频生成', 'Video generation'),
      description: l('以异步任务方式创建视频、轮询状态并下载内容，覆盖 Grok Imagine、MiniMax-H3 与 Seedance。', 'Create videos as asynchronous jobs, poll their status, and download content, covering Grok Imagine, MiniMax-H3, and Seedance.'),
      sections: [
        { id: 'video-overview', title: l('接口与可用性', 'Endpoints and availability'), paragraphs: [
          l('视频生成是异步任务：先创建任务取得任务 ID，再轮询状态，最后从 content 端点下载视频字节。创建使用 POST /v1/videos 或 POST /v1/videos/generations；查询使用 GET /v1/videos/{request_id} 或 GET /v1/videos/generations/{request_id}；下载使用 GET /v1/videos/{request_id}/content。编辑与扩展分别使用 POST /v1/videos/edits 与 POST /v1/videos/extensions。', 'Video generation is asynchronous: create a job to get a task ID, poll its status, then download the video bytes from the content endpoint. Create with POST /v1/videos or POST /v1/videos/generations; poll with GET /v1/videos/{request_id} or GET /v1/videos/generations/{request_id}; download with GET /v1/videos/{request_id}/content. Edits and extensions use POST /v1/videos/edits and POST /v1/videos/extensions.'),
          l('可用平台由分组决定：Grok 分组使用 Grok Imagine 视频；MiniMax 分组转发到 MiniMax-H3；composite/智能路由密钥在模型为 grok-imagine-video* 时路由到 Grok。OpenAI、Gemini 等其他平台的密钥在所有视频路径（创建、状态、内容、编辑、扩展）上都会返回 404 "Videos API is not supported for this platform"。状态与内容请求不携带模型，composite 密钥会直接按 Grok 处理。', 'Availability depends on the group. Grok groups use Grok Imagine video, MiniMax groups forward to MiniMax-H3, and composite or smart-routed keys route to Grok when the model is grok-imagine-video*. Keys on other platforms, such as OpenAI or Gemini, get 404 "Videos API is not supported for this platform" on every video path (create, status, content, edits, and extensions). Status and content requests carry no model, so composite keys are handled as Grok.'),
          l('Grok 视频创建与 Seedance 任务创建还要求分组开启图像生成权限（allow_image_generation），否则返回 403 permission_error；MiniMax-H3 不受该检查。composite 密钥可以透过平台门禁，但若没有可用的 Grok/composite 账号来服务该模型，会返回 503，错误码为 grok_media_no_eligible_account（"No eligible Grok media accounts"）。', 'Grok video creates and Seedance task creates also require the group to allow image generation (allow_image_generation); otherwise they return 403 permission_error. MiniMax-H3 is exempt from that check. Composite keys pass the platform gate, but when no Grok/composite account can serve the model the response is 503 with error code grok_media_no_eligible_account ("No eligible Grok media accounts").'),
        ], note: l('创建前先确认密钥所在分组为 Grok、MiniMax，或 composite 路由且能匹配到 Grok 账号。返回 404 通常表示该平台不提供视频接口，而不是视频任务失败。', 'Before creating a job, confirm the key’s group is Grok or MiniMax, or a composite route that can match a Grok account. A 404 usually means the platform does not offer the Videos API, not that a job failed.') },
        { id: 'video-create', title: l('创建视频任务（Grok）', 'Create a video job (Grok)'), paragraphs: [
          l('Grok 分组的创建请求是 JSON，并已在上游允许的范围内尽量保持原生字段。请求体应至少包含 model 与 prompt；图生视频模型还需要参考图。与图片接口相同，创建请求要求分组开启图像生成权限（allow_image_generation），否则返回 403 permission_error。', 'Create requests on Grok groups are JSON and keep native fields as far as upstream allows. The body should contain at least model and prompt; image-to-video models also need a reference image. As with the image endpoints, creation requires the group to allow image generation (allow_image_generation); otherwise it returns 403 permission_error.'),
        ], fields: [
          { name: 'model', type: 'string', required: true, description: l('grok-imagine-video（默认视频模型）或 grok-imagine-video-1.5（图生视频，必须提供输入图片）；也接受 grok-imagine-video-1.5-preview、grok-video-1.5 等别名。', 'grok-imagine-video (default video model) or grok-imagine-video-1.5 (image-to-video, requires an input image); aliases such as grok-imagine-video-1.5-preview and grok-video-1.5 are also accepted.') },
          { name: 'prompt', type: 'string', description: l('视频画面描述。文生视频需要描述场景；图生视频按上游要求提供。', 'Description of the video. Text-to-video needs a scene description; image-to-video follows upstream requirements.') },
          { name: 'duration', type: 'number', description: l('视频时长（秒），seconds 是接受并会被规范化的别名。计费解析按 1–15 秒；省略时按上游默认 8 秒处理，超出上游范围可能被拒绝。', 'Video duration in seconds; seconds is an accepted alias that is normalized. Billing resolution uses 1–15 seconds; when omitted it is treated as upstream’s 8-second default, and values outside the upstream range may be rejected.') },
          { name: 'resolution', type: 'string', description: l('480p、720p 或 1080p，也接受 480、720、1080、sd、hd 等别名。1080p 通常只由 1.5 图生视频模型支持，以实际上游为准。', '480p, 720p, or 1080p; aliases such as 480, 720, 1080, sd, and hd are also accepted. 1080p is usually supported only by the 1.5 image-to-video model; confirm with upstream.') },
          { name: 'aspect_ratio', type: 'string', description: l('视频宽高比，例如 16:9、9:16、1:1；未提供时可从 size 推导。', 'Video aspect ratio such as 16:9, 9:16, or 1:1; it can be derived from size when omitted.') },
          { name: 'size', type: 'string', description: l('例如 1280x720。网关会换算为 aspect_ratio 后移除该字段，不直接转发 size。', 'For example 1280x720. The gateway converts it to aspect_ratio and removes size; size itself is not forwarded.') },
          { name: 'image / images / reference_images', type: 'string | array', description: l('输入参考图，接受 URL 字符串或 { "url": "..." } / { "image_url": ... } 形式；图生视频与 1.5 模型需要。', 'Input reference images, as URL strings or { "url": "..." } / { "image_url": ... } objects; needed for image-to-video and the 1.5 model.') },
          { name: 'first_frame / last_frame', type: 'string | object', description: l('首帧与尾帧参考图，格式与参考图相同。', 'First-frame and last-frame reference images, in the same shape as other references.') },
        ], examples: requestExamples(origin, '/v1/videos', videoCreateRequest) },
        { id: 'video-create-response', title: l('创建响应', 'Create response'), paragraphs: [
          l('创建响应是上游 JSON 的透传，网关在缺失时补上 id 与 request_id，便于客户端保存任务标识；上游只返回 task_id 时，网关会把它作为任务 ID 并补齐 id/request_id。若上游在创建响应中返回 status 等字段，会原样透传，值以上游为准。', 'Create responses pass through the upstream JSON, and the gateway adds id and request_id when missing so clients can persist the task identifier. When upstream only returns task_id, the gateway uses it as the task ID and fills in id/request_id. If upstream includes fields such as status in the create response, they are passed through unchanged and their values follow upstream.'),
        ], fields: [
          { name: 'id', type: 'string', description: l('任务 ID，用于后续查询与下载。', 'Task ID used for later polling and downloads.') },
          { name: 'request_id', type: 'string', description: l('与 id 相同的任务标识，兼容不同客户端的字段名。', 'Task identifier matching id, kept for clients that use this field name.') },
        ], examples: { json: JSON.stringify({ id: 'video_task_example', request_id: 'video_task_example' }, null, 2) } },
        { id: 'video-status', title: l('轮询任务状态', 'Poll job status'), paragraphs: [
          l('用创建响应中的任务 ID 轮询 GET /v1/videos/{request_id}（也可使用 /v1/videos/generations/{request_id}）。网关把上游的 done 映射为 completed、expired 映射为 failed，其他状态原样保留，具体值以上游为准。终态后才能下载内容。', 'Poll GET /v1/videos/{request_id} (or /v1/videos/generations/{request_id}) with the task ID from the create response. The gateway maps upstream done to completed and expired to failed, and passes other statuses through unchanged; their values follow upstream. Content can be downloaded only after a terminal status.'),
          l('状态响应中的视频地址可能被改写为本站 content 路径，也可能保留上游临时链接；请始终使用 content 端点下载，不要依赖状态里的直链。轮询应使用退避间隔（例如从数秒开始逐步增加），避免高频请求。', 'Video URLs in status responses may be rewritten to this site’s content path or may keep upstream temporary links. Always download from the content endpoint instead of relying on links in the status body. Poll with a backoff interval (for example, start at a few seconds and increase) rather than at a high frequency.'),
          l('常见错误：400 "request_id is required"、404 "Video request not found"、410 表示任务来源账号已被移除或变更（应停止轮询）、503 表示任务信息暂时不可用（稍后重试）。', 'Common errors: 400 "request_id is required", 404 "Video request not found", 410 when the source account for the task was removed or changed (stop polling), and 503 when task information is temporarily unavailable (retry later).'),
        ], examples: {
          curl: `curl ${origin}/v1/videos/VIDEO_REQUEST_ID \\\n  -H "Authorization: Bearer $API_KEY"`,
          json: JSON.stringify({ id: 'video_task_example', status: 'completed', video: { url: '/v1/videos/video_task_example/content', duration: 8 } }, null, 2),
        } },
        { id: 'video-content', title: l('下载视频内容', 'Download video content'), paragraphs: [
          l('任务到达成功终态后，用 GET /v1/videos/{request_id}/content 下载视频字节。响应转发 Content-Type、Content-Length、Content-Range、Accept-Ranges 与 Content-Disposition，并支持 Range 请求，可用于断点续传或只读取片段。', 'After the job reaches a successful terminal status, download the video bytes with GET /v1/videos/{request_id}/content. The response forwards Content-Type, Content-Length, Content-Range, Accept-Ranges, and Content-Disposition, and supports Range requests for resumable downloads or partial reads.'),
          l('任务尚未完成时，content 端点会返回上游错误或未就绪状态；请先轮询状态，不要直接下载。下载时仍需携带与创建任务相同的密钥。', 'While the job is incomplete, the content endpoint returns an upstream error or not-ready status; poll the status first rather than downloading directly. Downloads still require the same API key used to create the job.'),
        ], examples: { shell: `curl -L ${origin}/v1/videos/VIDEO_REQUEST_ID/content \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -o output.mp4` } },
        { id: 'video-minimax', title: l('MiniMax-H3', 'MiniMax-H3'), paragraphs: [
          l('分组平台为 minimax 时，/v1/videos 会转发到 MiniMax-H3。请求必须使用 application/json（否则 415），且每次只创建一个任务、一段视频；上游地址由账号自定义 base URL 决定。MiniMax-H3 不受 allow_image_generation 检查。创建响应用 id 或 task_id 标识任务；查询到 status 为 completed 且存在 metadata.url 时表示成功，网关会把 metadata.url 改写为本站 /v1/videos/{id}/content。', 'When the group platform is minimax, /v1/videos forwards to MiniMax-H3. Requests must use application/json (otherwise 415), and each request creates a single job and video; the upstream address comes from the account’s custom base URL. MiniMax-H3 is exempt from the allow_image_generation check. The create response identifies the task with id or task_id; a status of completed with metadata.url means success, and the gateway rewrites metadata.url to this site’s /v1/videos/{id}/content.'),
        ], fields: [
          { name: 'model', type: 'string', required: true, description: l('必须映射到 MiniMax-H3（不区分大小写），否则请求被拒绝。', 'Must map to MiniMax-H3 (case-insensitive); otherwise the request is rejected.') },
          { name: 'prompt', type: 'string', required: true, description: l('1–7000 个字符。', '1–7000 characters.') },
          { name: 'seconds', type: 'string', required: true, description: l('必须是整数字符串 "1" 到 "15"，例如 "8"；不能写成数字类型。', 'Must be an integer string from "1" to "15", for example "8"; a numeric value is rejected.') },
          { name: 'n', type: 'integer', description: l('可省略；如果提供必须为 1，否则返回错误。', 'May be omitted; when present it must be 1, otherwise the request fails.') },
          { name: 'size', type: 'string', description: l('可选，例如 1280x720。', 'Optional, for example 1280x720.') },
          { name: 'metadata.content', type: 'array', description: l('可选图像参考，元素为 { "type": "image_url", "image_url": { "url": "..." } }。', 'Optional image references shaped as { "type": "image_url", "image_url": { "url": "..." } }.') },
        ], examples: requestExamples(origin, '/v1/videos', minimaxVideoRequest) },
        { id: 'video-seedance', title: l('Seedance（Ark 原生协议）', 'Seedance (native Ark protocol)'), paragraphs: [
          l('OpenAI 或 composite 分组的密钥还可以调用 Seedance 的 Ark 原生任务协议：POST /api/v3/contents/generations/tasks 创建，GET 或 DELETE 同一路径加 /{task_id} 查询或删除，GET .../{task_id}/content 下载视频。该入口使用原生请求体，不是 /v1/videos 的 OpenAI 视频格式；分组平台不是 OpenAI/composite 时返回 403。与 Grok 视频创建相同，该入口还要求分组开启图像生成权限（allow_image_generation），否则返回 403 permission_error。', 'Keys on OpenAI or composite groups can also use the native Ark task protocol for Seedance: create with POST /api/v3/contents/generations/tasks, query or delete with GET or DELETE on the same path plus /{task_id}, and download with GET .../{task_id}/content. This endpoint uses the native body, not the OpenAI videos format under /v1/videos; groups on other platforms return 403. As with Grok video creates, it also requires the group to allow image generation (allow_image_generation); otherwise it returns 403 permission_error.'),
          l('请求必须是 JSON，并包含 model 与非空 content 数组：元素为 { "type": "text", "text": "..." } 或 { "type": "image_url", "image_url": { "url": "..." } }；可选 duration（整数秒，0–60）与 resolution。创建响应必须包含 id；本站以 succeeded、usage.completion_tokens 与 content.video_url 作为成功判定条件（这是网关侧校验，不代表上游一定返回这些字段），满足后网关会把 video_url 改写为本站 content 路径。', 'The request must be JSON with model and a non-empty content array: entries are { "type": "text", "text": "..." } or { "type": "image_url", "image_url": { "url": "..." } }, with optional duration (integer seconds, 0–60) and resolution. A create response must include id; this site treats succeeded, usage.completion_tokens, and content.video_url as its success criteria (a gateway-side check, not a guarantee that upstream returns these fields), and then rewrites video_url to this site’s content path.'),
        ], examples: requestExamples(origin, '/api/v3/contents/generations/tasks', seedanceRequest) },
        { id: 'video-edits', title: l('视频编辑与扩展', 'Video edits and extensions'), paragraphs: [
          l('POST /v1/videos/edits 与 POST /v1/videos/extensions 用于基于源视频的编辑与延长。请求应包含 model 与源视频引用，其他字段按上游协议透传；仓库中的请求结构为 { "model": "grok-imagine-video", "prompt": "...", "video": { "url": "https://example.test/source.mp4" } }。', 'POST /v1/videos/edits and POST /v1/videos/extensions edit and extend an existing video. Requests should include model and a source-video reference, and other fields pass through according to the upstream protocol; the request shape used in this repository is { "model": "grok-imagine-video", "prompt": "...", "video": { "url": "https://example.test/source.mp4" } }.'),
          l('这两个端点只对 Grok 平台（或 composite 路由）可用，其他平台返回同样的 404。创建成功后，用 /v1/videos/edits/{request_id} 与 /v1/videos/extensions/{request_id} 查询状态，并在对应路径加 /content 下载。', 'These endpoints are available only to Grok-platform (or composite-routed) keys; other platforms return the same 404. After creating a job, poll /v1/videos/edits/{request_id} and /v1/videos/extensions/{request_id}, and append /content to the matching path to download.'),
        ], examples: { curl: `curl ${origin}/v1/videos/edits \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ model: 'grok-imagine-video', prompt: 'Add gentle snowfall', video: { url: 'https://example.test/source.mp4' } }, null, 2)}'` } },
        { id: 'video-caveats', title: l('注意事项', 'Caveats'), paragraphs: [
          l('查询与下载必须使用创建任务的同一个密钥；用其他密钥查询会得到 404。请持久化任务 ID，轮询失败时按退避重试，不要重复创建任务。', 'Polling and downloads must use the same API key that created the job; another key gets a 404. Persist the task ID, retry polling with backoff, and avoid creating duplicate jobs.'),
          l('异步任务一旦受理即可能产生计费，即使视频未下载或最终失败；具体计费以本站使用记录为准，本页不列出价格。模型可用性、时长/分辨率支持、并发与限流由站点和上游配置决定。', 'An accepted asynchronous job may be billed even if the video is never downloaded or ultimately fails; billing is reflected in this site’s usage records, and no prices are listed here. Model availability, duration and resolution support, concurrency, and rate limits are configured by this site and upstream.'),
        ], links: [
          { label: l('图像生成', 'Image generation'), page: 'image-generation' },
          { label: l('Nano Banana 图像模型', 'Nano Banana image models'), page: 'nano-banana' },
        ] },
      ],
    },
    {
      id: 'agent-setup', category: 'integrations', title: l('Agent 新手配置指南', 'Agent setup guide'),
      description: l('装好 CC Switch 后，在应用页一键安装智能体，再从本站控制台「API 密钥」一键「导入到 CCS」，覆盖 Claude Code、Codex、Gemini CLI、Grok Build、OpenCode、Claude Desktop、OpenClaw、Hermes、Pi、DeepSeek Harness 等 14 款应用以及 Cursor / VS Code。', 'Install your agent inside CC Switch → Apps and push config from this site with "Import to CCS" across 14 CC Switch apps plus Cursor and VS Code.'),
      sections: [
        { id: 'site-ccswitch-workflow', title: l('4 步上手：本站 + CC Switch 配置全流程', '4-step workflow: Site + CC Switch'), paragraphs: [l('CC Switch 应用页一键安装智能体，本站 API 密钥页点击导入到 CCS 自动写入端点、密钥与余额脚本。', 'Install agents in CC Switch Apps, then use Import to CCS on this site to write endpoint, key, and balance script.')] },
        { id: 'pick-your-agent', title: l('选择你的 Agent', 'Pick your agent'), paragraphs: [l('覆盖 Claude Code、Codex、Gemini CLI、Grok Build、OpenCode、Claude Desktop、OpenClaw、Hermes、Pi、MiniMax Code、Kimi Code、DeepSeek Harness、Antigravity、Oh My Pi、Cursor 与 VS Code。', 'Covers Claude Code, Codex, Gemini CLI, Grok Build, OpenCode, Claude Desktop, OpenClaw, Hermes, Pi, MiniMax Code, Kimi Code, DeepSeek Harness, Antigravity, Oh My Pi, Cursor, and VS Code.')] },
        { id: 'compare-agents', title: l('各 Agent 分组与接入速查表', 'Agent & group quick reference'), paragraphs: [l('对比每款 Agent 的本站推荐分组、CC Switch 一键安装与导入应用名。', 'Compare recommended site groups, CC Switch app installs, and import app IDs.')] },
        { id: 'understanding-agents', title: l('用好本站与 CC Switch 的 4 个核心要点', '4 essentials for using this site with CC Switch'), paragraphs: [l('单分组与智能路由、明文回执与导入到 CCS、自动余额脚本、Base URL 与 /v1 容错规则。', 'Smart routing, plaintext receipts, built-in balance scripts, and Base URL tolerance.')] },
        { id: 'troubleshooting', title: l('报错排查', 'Troubleshooting'), paragraphs: [l('排查找不到导入到 CCS 按钮、ccswitch:// 未唤起、401、403、模型不可用与环境变量覆盖。', 'Troubleshoot Import to CCS visibility, ccswitch:// handler, 401/403, unavailable models, and shell env overrides.')] },
      ],
    },
    {
      id: 'codex', category: 'integrations', title: l('接入 Codex', 'Connect Codex'), description: l('使用 OpenAI Responses 协议接入，保持模型与密钥权限一致。', 'Connect through OpenAI Responses with a model your key is authorized to use.'), sections: [
        { id: 'codex-settings', title: l('Provider 配置', 'Provider configuration'), paragraphs: [l('将地址填入 Codex 的自定义 provider 配置。model 使用本站模型列表中的完整 ID。示例不包含真实密钥。', 'Add this base URL to your custom Codex provider. Set model to a complete ID from this site’s model list. No real API key is included in the example.')], examples: { toml: `model = "YOUR_MODEL_ID"\nmodel_provider = "site"\n\n[model_providers.site]\nname = "API gateway"\nbase_url = "${origin}/v1"\nwire_api = "responses"\nenv_key = "SITE_API_KEY"`, shell: 'export SITE_API_KEY="YOUR_API_KEY"\ncodex' }, links: [{ label: l('打开交互式 Codex 配置向导（含 CC Switch）', 'Open interactive Codex setup guide (with CC Switch)'), page: 'agent-setup' }] },
        { id: 'codex-tools', title: l('搜索与工具', 'Search and tools'), paragraphs: [l('Codex 的搜索行为取决于客户端是否发出搜索请求，以及本站对应分组是否启用了网页工具。出现 web_search 或 web_fetch 只说明工具可用，最终仍应检查工具结果与引用。', 'Search depends on whether Codex requests it and whether web tools are configured for the selected group. Seeing web_search or web_fetch means a tool is available; still verify its results and citations.'), l('不要把 /v1/responses 手工改成 /v1/chat/completions 来绕过流错误，这会改变协议与工具行为。', 'Do not manually replace /v1/responses with /v1/chat/completions to work around stream errors; that changes protocol and tool behavior.')] },
      ],
    },
    {
      id: 'claude-code', category: 'integrations', title: l('接入 Claude Code', 'Connect Claude Code'), description: l('使用 Anthropic Messages 入口与本站密钥。', 'Use the Anthropic Messages endpoint and a site API key.'), sections: [
        { id: 'claude-environment', title: l('环境变量', 'Environment variables'), paragraphs: [l('ANTHROPIC_BASE_URL 填站点根地址，客户端会追加 /v1/messages。模型必须在该密钥可用范围内，且分组允许 messages 入口。', 'Set ANTHROPIC_BASE_URL to the site origin; the client appends /v1/messages. Select an authorized model, and ensure the group permits the messages endpoint.')], examples: { shell: `export ANTHROPIC_BASE_URL="${origin}"\nexport ANTHROPIC_AUTH_TOKEN="YOUR_API_KEY"\nexport ANTHROPIC_MODEL="YOUR_MODEL_ID"\nclaude` }, links: [{ label: l('打开交互式 Claude Code 配置向导（含 CC Switch）', 'Open interactive Claude Code setup guide (with CC Switch)'), page: 'agent-setup' }] },
        { id: 'claude-cache', title: l('缓存与请求结构', 'Caching and request structure'), paragraphs: [l('客户端发送的 cache_control 标记应保留。不要为了缓存随意更改工具顺序、系统消息或历史内容；缓存命中与上游支持、前缀稳定性及本站现有开关有关。', 'Preserve client-provided cache_control markers. Avoid changing tool ordering, system messages, or history solely to force caching. Cache hits depend on provider support, stable prefixes, and this site’s existing configuration.')] },
      ],
    },
    {
      id: 'key-self-service', category: 'account', title: l('用密钥查询自身信息', 'Key self-service API'),
      description: l('只用 API 密钥就能读：密钥状态与额度、账号余额与今日用量、可用分组与倍率、近期用量。', 'Read with only an API key: key status and limits, account balance and today’s usage, usable groups and their multipliers, and recent usage.'),
      quickActions: [
        { kind: 'createKey', variant: 'primary', label: l('快捷创建 API 密钥', 'Quick create API key'), unauthenticatedLabel: l('登录并创建 API 密钥', 'Sign in to create key') },
        { kind: 'manageKeys', variant: 'secondary', label: l('管理已有密钥', 'Manage API keys'), unauthenticatedLabel: l('前往密钥控制台', 'Open keys console') },
        { kind: 'doc', variant: 'ghost', page: 'manage-keys', label: l('查看控制台管理接口', 'Console management API') },
      ],
      summaryCards: [
        { sectionId: 'key-info-key', method: 'GET', path: '/v1/sub2api/key', badge: l('单把密钥', 'Key scope'), badgeTone: 'emerald', title: l('密钥状态与限额', 'Key status & limits'), subtitle: l('状态、是否可用及原因、到期、总额度、5h / 1d / 7d 限速窗口与路由分组。', 'Status, usability cause, expiry, quota, 5h / 1d / 7d windows and bound groups.') },
        { sectionId: 'key-info-groups', method: 'GET', path: '/v1/sub2api/groups', badge: l('单把密钥', 'Key scope'), badgeTone: 'emerald', title: l('可用分组与实时倍率', 'Groups & live multipliers'), subtitle: l('已绑定与可绑定分组状态、专属倍率与高峰倍率生效值。', 'Bound and available groups, personal rates and active peak multipliers.') },
        { sectionId: 'key-info-usage', method: 'GET', path: '/v1/sub2api/usage', badge: l('单把密钥', 'Key scope'), badgeTone: 'emerald', title: l('单密钥用量统计', 'Per-key usage'), subtitle: l('今日、近 7 天、本月与最长 90 天自定义区间，支持按日明细。', 'Today, last 7 days, this month, custom 90-day range and optional daily series.') },
        { sectionId: 'key-info-account', method: 'GET', path: '/v1/sub2api/account', badge: l('整个账号', 'Account scope'), badgeTone: 'amber', title: l('账号余额与今日合计', 'Account balance & today'), subtitle: l('钱包可用与冻结余额、有效订阅剩余额度及账号下全部密钥今日用量。', 'Wallet and frozen balance, active subscription allowance and today’s total across all keys.') },
      ],
      sections: [
        {
          id: 'key-info-overview', title: l('概览', 'Overview'),
          paragraphs: [
            l('四个只读接口，使用与模型请求相同的 API 密钥（Authorization: Bearer 或 x-api-key）。前三个回答「这把密钥是什么、做过什么」；GET /v1/sub2api/account 回答「这个账号还剩多少钱、今天一共用了多少」。任何一个都不返回密钥明文、IP 名单、邮箱与用户名、上游账号、渠道或上游成本。', 'Four read-only endpoints that use the same API key as model requests (Authorization: Bearer or x-api-key). The first three answer what this key is and what it has done; GET /v1/sub2api/account answers how much the account has left and how much it used today. None of them returns the key plaintext, IP lists, email or username, upstream accounts, channels or upstream cost.'),
            l('已过期、额度用完或账号余额为 0 的密钥也可以查询，/key 会说明原因；已停用的密钥、被停用的账号、不在 IP 白名单内的请求仍会被拒绝。查询不会刷新密钥的「最近使用」时间。', 'A key that is expired, out of quota or whose account has no balance left can still ask, and /key says why. A switched-off key, a disabled account, or a request from an IP outside the key’s allow-list is still refused. Asking does not refresh the key’s last-used time.'),
          ],
          note: l('四个接口响应均携带 Cache-Control: no-store，支持 Authorization: Bearer $API_KEY 或 x-api-key: $API_KEY，可直接用于状态栏、CLI 探针或监控脚本。', 'All four endpoints respond with Cache-Control: no-store and accept Authorization: Bearer $API_KEY or x-api-key: $API_KEY, ready for status bars, CLI probes and monitoring scripts.'),
          fields: [
            { name: 'GET /v1/sub2api/key', type: l('密钥', 'key'), description: l('密钥本身：状态、是否可用及原因、到期、额度、限速窗口、绑定分组。', 'The key itself: status, whether it is usable and why not, expiry, quota, rate-limit windows, bound groups.') },
            { name: 'GET /v1/sub2api/groups', type: l('分组', 'groups'), description: l('这把密钥已路由的分组，以及该用户还能绑定的分组：状态与实际支付的倍率。', 'The groups this key routes to, plus the ones its owner may still bind: state and the multiplier actually paid.') },
            { name: 'GET /v1/sub2api/usage', type: l('用量', 'usage'), description: l('这把密钥的今日、近 7 天、本月与自选区间用量。', 'This key’s usage today, over the last 7 days, this month and a range you choose.') },
            { name: 'GET /v1/sub2api/account', type: l('账号', 'account'), description: l('这个账号的钱包余额、订阅剩余额度，以及今日用量（账号下所有密钥合计，不只这一把）。', 'The account’s wallet balance, what is left of its subscriptions, and today’s usage across all of its keys, not just this one.') },
          ],
          examples: getExamples(origin, '/v1/sub2api/key'),
        },
        {
          id: 'key-info-key', title: l('密钥本身', 'The key'),
          endpoint: { method: 'GET', path: '/v1/sub2api/key', auth: 'apiKey' },
          paragraphs: [
            l('返回当前密钥的状态、可用性诊断、总额度、滚动限速窗口与路由分组。客户端发起正式模型请求前检查 is_usable，能直接区分到期、超额、停用或分组不可用。', 'Returns this key’s status, usability diagnosis, total quota, rolling spend windows and routing groups. Inspect is_usable before sending model requests to distinguish expiry, quota exhaustion, disabled state or unavailable groups.'),
          ],
          fields: [
            { name: 'status', type: 'string', description: l('active、inactive、quota_exhausted 或 expired。', 'active, inactive, quota_exhausted or expired.') },
            { name: 'is_usable', type: 'boolean', description: l('现在用这把密钥发请求，是否会因为密钥自身的原因被拒绝。', 'Whether a request made with this key right now would be refused for a reason about the key itself.') },
            { name: 'unusable_cause', type: 'string', description: l('is_usable 为 false 时出现：expired、quota_exhausted、disabled、no_group 或 group_unavailable。', 'Present when is_usable is false: expired, quota_exhausted, disabled, no_group or group_unavailable.') },
            { name: 'quota', type: 'object', description: l('limited、limit、used、remaining，单位 USD；没有额度上限时不含 limit 与 remaining。', 'limited, limit, used, remaining, in USD. Without a quota cap, limit and remaining are absent.') },
            { name: 'rate_limits', type: 'array', description: l('只列出配置了的 5h、1d、7d 窗口：limit、used、remaining、window_start、reset_at。', 'Only the windows that are configured (5h, 1d, 7d): limit, used, remaining, window_start, reset_at.') },
            { name: 'expires_at / days_until_expiry', type: 'time / number', description: l('不会过期的密钥 expires_at 为 null，且没有 days_until_expiry。', 'A key that never expires has expires_at null and no days_until_expiry.') },
            { name: 'route_mode / groups', type: 'string / array', description: l('none、single 或 smart；groups 按路由顺序列出 id、name、platform、status、primary。', 'none, single or smart; groups lists id, name, platform, status and primary in routing order.') },
            { name: 'key_prefix', type: 'string', description: l('只有展示用前缀，用来认出是哪一把密钥。', 'A display prefix only, enough to recognise which key it is.') },
          ],
          examples: {
            ...getExamples(origin, '/v1/sub2api/key'),
            json: JSON.stringify({ object: 'sub2api.key', schema_version: 1, id: 12, name: 'ci-bot', key_prefix: 'sk-a1b2c', status: 'active', is_usable: true, created_at: '2026-11-01T08:00:00Z', expires_at: null, quota: { limited: true, limit: 20, used: 3.2, remaining: 16.8, unit: 'USD' }, rate_limits: [{ window: '5h', limit: 2, used: 0.4, remaining: 1.6, window_start: '2026-11-24T09:12:00Z', reset_at: '2026-11-24T14:12:00Z' }], route_mode: 'single', groups: [{ id: 3, name: 'Claude', platform: 'anthropic', status: 'active', primary: true }], concurrency_limit: 0, observed_at: '2026-11-24T10:00:00Z' }, null, 2),
          },
        },
        {
          id: 'key-info-groups', title: l('可用分组与倍率', 'Groups and multipliers'),
          endpoint: { method: 'GET', path: '/v1/sub2api/groups', auth: 'apiKey' },
          paragraphs: [
            l('按路由顺序先列出当前密钥已绑定的分组（bound_to_key: true），再列出账号还可以绑定的其他分组。估算此刻实际计费倍率时，请以 effective_rate_multiplier 为准。', 'Lists groups bound to this key first in routing order (bound_to_key: true), followed by other groups the account may bind. Use effective_rate_multiplier for the live billing rate.'),
          ],
          fields: [
            { name: 'bound_to_key / primary', type: 'boolean', description: l('bound_to_key 为 true 的是这把密钥路由到的分组，primary 是其中的第一个；其余是该用户还能绑定的分组。', 'bound_to_key marks the groups this key routes to, primary the first of them; the rest are groups the owner may still bind.') },
            { name: 'status', type: 'string', description: l('分组当前状态；不是 active 的分组不会接收请求。', 'The group’s current state. A group that is not active does not receive requests.') },
            { name: 'rate_multiplier', type: 'number', description: l('分组的基础倍率。', 'The group’s base multiplier.') },
            { name: 'user_rate_multiplier', type: 'number', description: l('账号在该分组上的专属倍率，仅在与基础倍率不同时出现。', 'The account’s personal multiplier for the group; present only when it differs from the base one.') },
            { name: 'resolved_rate_multiplier', type: 'number', description: l('实际按此计费的倍率（已含专属倍率）。', 'The multiplier actually billed with (personal rate included).') },
            { name: 'effective_rate_multiplier', type: 'number', description: l('此刻生效的倍率：已含专属倍率与高峰倍率。', 'The multiplier in force right now: personal rate and the peak factor included.') },
            { name: 'peak_*', type: 'object', description: l('启用高峰倍率时给出 peak_start、peak_end、peak_rate_multiplier，时间按响应里的 timezone。', 'When a peak factor is enabled: peak_start, peak_end and peak_rate_multiplier, in the response timezone.') },
          ],
          examples: {
            ...getExamples(origin, '/v1/sub2api/groups'),
            json: JSON.stringify({ object: 'sub2api.key_groups', schema_version: 1, groups: [{ id: 3, name: 'Claude', description: 'Anthropic Claude', platform: 'anthropic', status: 'active', bound_to_key: true, primary: true, subscription_type: 'standard', rate_multiplier: 1, user_rate_multiplier: 0.8, resolved_rate_multiplier: 0.8, peak_rate_enabled: false, effective_rate_multiplier: 0.8 }], timezone: 'Asia/Shanghai', observed_at: '2026-11-26T04:00:00Z' }, null, 2),
          },
        },
        {
          id: 'key-info-usage', title: l('用量', 'Usage'),
          endpoint: { method: 'GET', path: '/v1/sub2api/usage', auth: 'apiKey' },
          paragraphs: [
            l('仅统计当前这一把密钥的请求、Token 与费用，固定返回 today、last_7_days 与 this_month 三段；传入 start_date / end_date（含首尾两天、最长 90 天）可附加 range，传 daily=1 可返回按日明细。', 'Aggregates requests, tokens and charges for this single key only. Always returns today, last_7_days and this_month; add start_date / end_date (inclusive, up to 90 days) for range and daily=1 for per-day points.'),
          ],
          fields: [
            { name: 'start_date / end_date', type: 'YYYY-MM-DD', description: l('自选区间，含首尾两天，最长 90 天；不传则只返回固定的三段。', 'A range of your own, both days included, at most 90 days. Without them only the three fixed blocks are returned.') },
            { name: 'timezone', type: 'string', description: l('如 Asia/Shanghai，决定「今日」从何时算起；无效时用站点时区。', 'For example Asia/Shanghai; decides when "today" starts. An invalid value falls back to the site timezone.') },
            { name: 'daily', type: '1 | true', description: l('同时返回按日的序列：默认近 7 天，指定了区间则为该区间。', 'Also return a per-day series: the last 7 days by default, or your range when one is given.') },
            { name: 'today / last_7_days / this_month / range', type: 'object', description: l('requests、各类 token、cost（标准价）与 actual_cost（实际扣费），不含账号成本。', 'requests, the token counts, cost (list price) and actual_cost (what was charged); no account cost.') },
            { name: 'partial', type: 'array', description: l('某个统计块没能在时限内算出来时，这里写出它的名字，该块不会被当成 0 返回。', 'When a block could not be computed in time its name is listed here; it is left out instead of being reported as zero.') },
          ],
          examples: {
            ...getExamples(origin, '/v1/sub2api/usage?start_date=2026-11-01&end_date=2026-11-07&daily=1'),
            json: JSON.stringify({ object: 'sub2api.key_usage', schema_version: 1, timezone: 'Asia/Shanghai', today: { label: 'today', start: '2026-11-26', end: '2026-11-26', requests: 24, input_tokens: 18000, output_tokens: 5200, cache_read_tokens: 32000, cache_write_tokens: 800, total_tokens: 56000, cost: 0.28, actual_cost: 0.22 }, last_7_days: { label: 'last_7_days', start: '2026-11-20', end: '2026-11-26', requests: 140, input_tokens: 96000, output_tokens: 28000, cache_read_tokens: 180000, cache_write_tokens: 4000, total_tokens: 308000, cost: 1.52, actual_cost: 1.18 }, this_month: { label: 'this_month', start: '2026-11-01', end: '2026-11-26', requests: 410, input_tokens: 290000, output_tokens: 84000, cache_read_tokens: 520000, cache_write_tokens: 12000, total_tokens: 906000, cost: 4.6, actual_cost: 3.55 }, range: { label: 'range', start: '2026-11-01', end: '2026-11-07', requests: 95, input_tokens: 68000, output_tokens: 19000, cache_read_tokens: 110000, cache_write_tokens: 2500, total_tokens: 199500, cost: 1.05, actual_cost: 0.82 }, daily: [{ date: '2026-11-01', requests: 14, input_tokens: 10200, output_tokens: 2800, cache_read_tokens: 15000, cache_write_tokens: 500, total_tokens: 28500, cost: 0.15, actual_cost: 0.12 }], observed_at: '2026-11-26T04:00:00Z' }, null, 2),
          },
        },
        {
          id: 'key-info-account', title: l('账号余额与今日用量', 'Account balance and today’s usage'),
          endpoint: { method: 'GET', path: '/v1/sub2api/account', auth: 'apiKey' },
          paragraphs: [
            l('这是整个账号的数字，不是这把密钥的：余额是账号的钱包，今日用量是账号下所有密钥的合计。持有这把密钥的人就能读到这些数字，请像保管钱一样保管密钥；只想让脚本看到单把密钥的用量，用 /v1/sub2api/usage。', 'These are the account’s figures, not this key’s: the balance is the account’s wallet, and today’s usage adds up every key of the account. Anyone holding this key can read them, so keep the key like money. To let a script see only one key’s usage, use /v1/sub2api/usage.'),
          ],
          noteTone: 'warning',
          note: l('该接口不返回邮箱、用户名或其他密钥列表，但仍会暴露账号级余额与订阅额度。把密钥交给外部脚本或协作者前，建议在控制台为其设置额度上限、到期时间与 IP 白名单。', 'This endpoint omits email, username and other keys, but it still exposes account-wide balance and subscriptions. Before sharing a key with a script or collaborator, set a quota cap, expiry and IP allow-list in the console.'),
          fields: [
            { name: 'balance.wallet', type: 'number', description: l('钱包可用余额，单位 USD，精度与控制台一致。', 'Spendable wallet balance in USD, with the same precision as the console.') },
            { name: 'balance.frozen', type: 'number', description: l('被仍在运行的异步批量任务冻结、暂时不能花的金额，USD。', 'USD held by asynchronous batch jobs that are still running; it cannot be spent meanwhile.') },
            { name: 'subscriptions', type: 'array', description: l('有效订阅，没有时是空数组。每项含 group_id、group_name、expires_at 与 remaining：日、周、月限额里最紧的那个还剩多少（USD）。套餐没有任何限额时没有 remaining，unlimited 为 true。', 'Active subscriptions; an empty array when there are none. Each has group_id, group_name, expires_at and remaining: what is left of the tightest of its daily, weekly and monthly limits (USD). A plan with no limit has no remaining, and unlimited is true.') },
            { name: 'today', type: 'object', description: l('label、start、end（YYYY-MM-DD，同一天）、requests、各类 token、cost（标准价）与 actual_cost（实际扣费），账号下所有密钥合计。', 'label, start and end (YYYY-MM-DD, the same day), requests, the token counts, cost (list price) and actual_cost (what was charged), for all keys of the account together.') },
            { name: 'timezone', type: 'string', description: l('查询参数，如 Asia/Shanghai，决定「今日」从何时算起，规则与 /v1/sub2api/usage 相同；无效时用站点时区。响应里的 timezone 是实际采用的时区。', 'A query parameter such as Asia/Shanghai that decides when "today" starts, with the same rules as /v1/sub2api/usage; an invalid value falls back to the site timezone. The timezone in the response is the one actually used.') },
            { name: 'partial', type: 'array', description: l('balance、subscriptions、today 里没能在时限内读出来的块会在这里列出，该块不会出现在响应里，也不会被当成 0 或空。', 'A block among balance, subscriptions and today that could not be read in time is listed here and left out of the response; it is never reported as zero or empty.') },
          ],
          examples: {
            ...getExamples(origin, '/v1/sub2api/account'),
            json: JSON.stringify({ object: 'sub2api.account', schema_version: 1, timezone: 'Asia/Shanghai', balance: { wallet: 12.34567891, frozen: 0, unit: 'USD' }, subscriptions: [{ group_id: 5, group_name: 'Claude Pro', expires_at: '2026-12-31T16:00:00Z', remaining: 4.2, unlimited: false }], today: { label: 'today', start: '2026-11-26', end: '2026-11-26', requests: 120, input_tokens: 80000, output_tokens: 24000, cache_read_tokens: 150000, cache_write_tokens: 3000, total_tokens: 257000, cost: 1.25, actual_cost: 0.9 }, observed_at: '2026-11-26T04:00:00Z' }, null, 2),
          },
        },
        {
          id: 'key-info-limits', title: l('限流与错误', 'Limits and errors'),
          paragraphs: [
            l('每把密钥在每个接口上每分钟最多 30 次，四个接口各有独立计数；状态栏每几秒轮询一次不会触顶。超出时返回 429，带 Retry-After（秒）与 code: key_info_rate_limited，等待后重试即可。', 'Each key may call each endpoint 30 times a minute, with separate counters for the four. A status bar polling every few seconds stays far below that. Beyond it the response is 429 with Retry-After (seconds) and code key_info_rate_limited; wait and retry.'),
            l('常见状态：401 密钥缺失、无效或已停用；403 来源 IP 不在该密钥的白名单内，或所属分组已停用；404 站点以简易模式运行，不提供此信息。', 'Common statuses: 401 for a missing, invalid or switched-off key; 403 when the source IP is outside the key’s allow-list or its group is disabled; 404 when the site runs in simple mode and does not offer this information.'),
          ],
          fields: [
            { name: '401', type: 'HTTP', description: l('API 密钥缺失、无效、已被停用，或所属账号已被停用。', 'API key is missing, invalid, switched off, or its owning account is disabled.') },
            { name: '403', type: 'HTTP', description: l('请求来源 IP 不在该密钥的白名单内（或命中黑名单），或所属分组不可用。', 'Source IP is outside the key’s allow-list (or blocked), or the bound group is disabled.') },
            { name: '404', type: 'HTTP', description: l('站点以简易模式运行，不开放自助查询接口。', 'The site runs in simple mode and does not expose self-service key endpoints.') },
            { name: '429', type: 'HTTP', description: l('单密钥单接口超过每分钟 30 次；按响应头 Retry-After 秒数等待后重试。', 'Exceeded 30 requests/minute for one key on one endpoint; wait Retry-After seconds and retry.') },
          ],
          examples: {
            json: JSON.stringify({ type: 'error', error: { type: 'rate_limit_error', code: 'key_info_rate_limited', message: 'Too many requests to the key information endpoints, please retry after 12 seconds' } }, null, 2),
          },
          links: [
            { label: l('需要创建、修改或轮换密钥？查看管理 API 密钥（控制台接口）', 'Need to create, edit or rotate keys? See Manage API keys (console API)'), page: 'manage-keys' },
            { label: l('密钥与鉴权', 'Keys and authentication'), page: 'authentication' },
          ],
        },
      ],
    },
    {
      id: 'manage-keys', category: 'account', title: l('管理 API 密钥（控制台接口）', 'Manage API keys (console API)'),
      description: l('网页控制台用来创建、查看、修改与轮换密钥的接口，使用登录令牌，不使用 API 密钥。', 'The endpoints the web console uses to create, list, change and rotate keys. They take a login token, not an API key.'),
      quickActions: [
        { kind: 'createKey', variant: 'primary', label: l('快捷创建 API 密钥', 'Quick create API key'), unauthenticatedLabel: l('登录并创建 API 密钥', 'Sign in to create key') },
        { kind: 'manageKeys', variant: 'secondary', label: l('打开密钥列表', 'Open API keys'), unauthenticatedLabel: l('登录控制台管理密钥', 'Sign in to manage keys') },
        { kind: 'doc', variant: 'ghost', page: 'key-self-service', label: l('用密钥自助查询', 'Key self-service API') },
      ],
      summaryCards: [
        { sectionId: 'console-auth', path: 'Bearer access_token', badge: l('登录令牌', 'Login token'), badgeTone: 'amber', title: l('登录令牌与响应格式', 'Login token & envelope'), subtitle: l('使用 access_token 调用，API 密钥不可用；约 60 分钟有效期支持 refresh 续期。', 'Requires access_token (never an API key); ~60 min lifetime renewed via refresh_token.') },
        { sectionId: 'console-create', method: 'POST', path: '/api/v1/keys', badge: l('单次明文', 'One-time secret'), badgeTone: 'blue', title: l('创建 API 密钥', 'Create an API key'), subtitle: l('支持单分组或智能路由、额度与限速、Idempotency-Key 安全重试。', 'Single group or smart routing, quotas, rate limits and Idempotency-Key retries.') },
        { sectionId: 'console-update', method: 'PUT', path: '/api/v1/keys/:id', badge: l('变更与轮换', 'Update & rotate'), badgeTone: 'emerald', title: l('修改、轮换与批量轮换', 'Update, rotate & bulk rotate'), subtitle: l('增量修改字段、单把轮换、单次最多 50 把批量轮换与封禁机房 IP。', 'Partial updates, single or up to 50 batch rotations and datacenter IP blocking.') },
        { sectionId: 'console-groups', method: 'GET', path: '/api/v1/groups/available', badge: l('分组与安全', 'Groups & security'), badgeTone: 'slate', title: l('可用分组与校验开关', 'Groups & verification switch'), subtitle: l('查询可绑定分组与倍率、当前校验因子，并调整 on / auto / off 保护强度。', 'Inspect bindable groups, active verification factors and on / auto / off protection.') },
      ],
      sections: [
        {
          id: 'console-auth', title: l('先看这个：登录令牌', 'Read this first: the login token'),
          paragraphs: [
            l('这些接口属于账号，不属于某把 API 密钥：请求头用登录后得到的 access_token（Authorization: Bearer），API 密钥不能调用它们。令牌默认约 60 分钟有效，用登录时一并返回的 refresh_token 调用 POST /api/v1/auth/refresh 续期。', 'These endpoints belong to your account, not to an API key: send the access_token you receive when you sign in (Authorization: Bearer). An API key cannot call them. The token lasts about 60 minutes by default; renew it with the refresh_token from the same response through POST /api/v1/auth/refresh.'),
            l('本站登录带人机验证，开启了 TOTP 或 Passkey 的账号还要多一步，所以无人值守的脚本通常拿不到令牌。需要自动化时，优先用「用密钥查询自身信息」的四个接口；创建与轮换密钥请在控制台里做。登录令牌的权限远大于 API 密钥，不要写进代码仓库或日志。', 'Sign-in on this site includes a human check, and accounts with TOTP or a Passkey have one more step, so an unattended script usually cannot obtain a token. For automation prefer the four key self-service endpoints, and create or rotate keys in the console. A login token is far more powerful than an API key: keep it out of repositories and logs.'),
          ],
          note: l('响应统一为 {"code":0,"message":"success","data":…}；出错时 code 非 0，reason 给出机器可读的原因（例如 VERIFY_REQUIRED）。列表接口的 data 是 {items,total,page,page_size,pages}。网关里的 /v1/sub2api/* 接口用的是另一种错误格式（type 与 error 对象），见上一页。', 'Responses share one envelope: {"code":0,"message":"success","data":…}. On failure code is non-zero and reason carries a machine-readable cause such as VERIFY_REQUIRED. List endpoints return {items,total,page,page_size,pages} in data. The /v1/sub2api/* gateway endpoints use a different error format (a type and an error object); see the previous page.'),
          examples: {
            curl: `curl "${origin}/api/v1/keys?page=1&page_size=20" \\\n  -H "Authorization: Bearer $ACCESS_TOKEN"`,
            shell: `# ${l('access_token 即将过期时用 refresh_token 续期', 'Renew access_token before expiry with refresh_token')}\ncurl -X POST ${origin}/api/v1/auth/refresh \\\n  -H "Content-Type: application/json" \\\n  -d '{"refresh_token":"YOUR_REFRESH_TOKEN"}'`,
          },
          links: [
            { label: l('脚本与状态栏监控优先查看：用密钥查询自身信息', 'For scripts and status bars: Key self-service API'), page: 'key-self-service' },
          ],
        },
        {
          id: 'console-list', title: l('查看密钥', 'List and read keys'),
          endpoint: { method: 'GET', path: '/api/v1/keys', auth: 'session' },
          paragraphs: [
            l('列表与单条查询只返回脱敏后的 key_masked 与展示前缀 key_prefix；需要一次性查看多把密钥近 24 小时调用与花费时，可配合 /api/v1/keys/overview 批量拉取。', 'List and detail endpoints only return key_masked and key_prefix. Use /api/v1/keys/overview to fetch 24-hour request and spend totals for multiple keys in one call.'),
          ],
          fields: [
            { name: 'GET /api/v1/keys', type: l('列表', 'list'), description: l('查询参数 page、page_size、sort_by、sort_order、search（最长 100 字符）、status、group_id。', 'Query: page, page_size, sort_by, sort_order, search (up to 100 characters), status, group_id.') },
            { name: 'GET /api/v1/keys/:id', type: l('单个', 'one'), description: l('只能读自己的密钥；别人的返回 404。', 'Your own keys only; someone else’s returns 404.') },
            { name: 'GET /api/v1/keys/overview?ids=1,2,3', type: l('概览', 'overview'), description: l('一次最多 100 把：近 24 小时的 requests_24h 与 cost_24h。', 'Up to 100 keys at once: requests_24h and cost_24h over the last 24 hours.') },
            { name: 'GET /api/v1/user/api-keys/:id/usage', type: l('用量', 'usage'), description: l('单把密钥的今日与区间汇总，另有 /usage/daily 给出按日序列；区间最长 90 天。', 'Today and range totals for one key; /usage/daily gives the per-day series. Ranges are limited to 90 days.') },
            { name: 'key_masked / key_prefix', type: 'string', description: l('列表里只有脱敏后的密钥，明文只在创建或轮换的那次响应里出现一次。', 'Lists carry only a masked key. The plaintext appears once, in the response that creates or rotates it.') },
          ],
          examples: {
            curl: `curl "${origin}/api/v1/keys?page=1&page_size=20&status=active" \\\n  -H "Authorization: Bearer $ACCESS_TOKEN"`,
            json: JSON.stringify({ code: 0, message: 'success', data: { items: [{ id: 12, name: 'ci-bot', key_prefix: 'sk-a1b2c', key_masked: 'sk-a1b2c****', group_id: 3, status: 'active', quota: 20, quota_used: 3.2, expires_at: null }], total: 1, page: 1, page_size: 20, pages: 1 } }, null, 2),
          },
        },
        {
          id: 'console-create', title: l('创建密钥', 'Create a key'),
          endpoint: { method: 'POST', path: '/api/v1/keys', auth: 'session' },
          paragraphs: [
            l('只有 name 必填。group_id 选一个分组，group_ids 按顺序给出多个分组即为智能路由。账号开了 TOTP 或绑定了 Passkey 时，请求里要带 verification；只有登录密码的账号不需要。', 'Only name is required. group_id picks one group; group_ids lists several in order for smart routing. When the account has TOTP or a Passkey the request must carry verification; an account with only a password does not need it.'),
            l('带上 Idempotency-Key 请求头可以安全重试：24 小时内重放同一个值，返回的是不含明文的记录，不会再造一把密钥。', 'Send an Idempotency-Key header to retry safely: replaying the same value within 24 hours returns the record without the plaintext and does not create another key.'),
          ],
          note: l('新密钥明文只在首次创建成功的响应 data.key 中出现一次，请立即保存；使用相同 Idempotency-Key 重放会返回不含明文的脱敏记录（若账号要求身份校验，跨请求重放同样需携带有效 verification）。', 'The plaintext secret appears only once in data.key of the initial successful response—save it immediately. Replaying the same Idempotency-Key returns a masked record without plaintext (and still requires valid verification when the account enforces it).'),
          action: {
            kind: 'createKey',
            title: l('直接前往控制台快捷创建密钥', 'Create a key directly in the console'),
            description: l('无需手写登录令牌与校验参数，支持可视化分组筛选、智能路由、限速配置与一键导入 CC Switch。', 'Skip crafting login tokens and verification payloads—use the console dialog with group picker, smart routing, limits and Import to CCS.'),
            label: l('快捷创建 API 密钥', 'Quick create API key'),
            unauthenticatedLabel: l('登录并创建 API 密钥', 'Sign in to create key'),
          },
          fields: [
            { name: 'name', type: 'string', required: true, description: l('密钥名称。', 'The key’s name.') },
            { name: 'group_id / group_ids', type: 'number / number[]', description: l('绑定的分组；group_ids 的顺序就是路由顺序。站点已有可用分组时必须绑定至少一个。', 'Bound group(s); the order of group_ids is the routing order. At least one is required once the site has usable groups.') },
            { name: 'custom_key', type: 'string', description: l('自定义密钥值：至少 16 个字符，只能用字母、数字、下划线和连字符，且不能是容易猜到的模式；不传由站点生成。', 'A key value of your own: at least 16 characters, letters, digits, underscore and hyphen only, and not a predictable pattern. Generated by the site when omitted.') },
            { name: 'quota', type: 'number', description: l('总额度上限（USD），0 为不限。', 'Total spend cap in USD; 0 means unlimited.') },
            { name: 'rate_limit_5h / rate_limit_1d / rate_limit_7d', type: 'number', description: l('各窗口花费上限（USD），0 为不限。', 'Spend cap per window in USD; 0 means unlimited.') },
            { name: 'expires_in_days', type: 'number', description: l('多少天后过期，必须大于 0；不传则不过期。', 'Days until it expires, greater than 0; omit for never.') },
            { name: 'ip_whitelist / ip_blacklist', type: 'string[]', description: l('IP 或 CIDR 列表。', 'Lists of IPs or CIDR ranges.') },
            { name: 'concurrency_limit', type: 'number', description: l('并发上限，0 为不限。', 'Concurrency cap; 0 means unlimited.') },
            { name: 'anthropic_cache_ttl_mode', type: 'string', description: l('inherit、off、1h 或 5m。', 'inherit, off, 1h or 5m.') },
            { name: 'verification', type: 'object', description: l('{"method":"password|totp|passkey"}，再加 password、totp_code 或 passkey_session_token 中对应的一项。', '{"method":"password|totp|passkey"} plus the matching password, totp_code or passkey_session_token.') },
          ],
          examples: {
            ...sessionCreateExamples(origin, '/api/v1/keys', { name: 'ci-bot', group_id: 3, quota: 20, expires_in_days: 30 }),
            json: JSON.stringify({ code: 0, message: 'success', data: { id: 42, name: 'ci-bot', key: 'YOUR_NEW_API_KEY', key_prefix: 'sk-a1b2c', key_masked: 'sk-a1b2c****', group_id: 3, status: 'active', quota: 20, quota_used: 0, expires_at: '2026-12-26T04:00:00Z' } }, null, 2),
          },
        },
        {
          id: 'console-update', title: l('修改、轮换与删除', 'Change, rotate and delete'),
          endpoint: { method: 'PUT', path: '/api/v1/keys/:id', auth: 'session' },
          paragraphs: [
            l('修改接口按需增量更新：未传的字段保持不变，传入空数组 [] 会清空 ip_whitelist 或 ip_blacklist。凡是签发新明文的轮换操作（regenerate_key 或 batch-rotate）与创建同级，需携带有效 verification。', 'Updates are partial: omitted fields remain unchanged, while an empty array [] clears ip_whitelist or ip_blacklist. Plaintext-issuing rotations (regenerate_key or batch-rotate) require the same verification as creation.'),
          ],
          fields: [
            { name: 'PUT /api/v1/keys/:id', type: l('修改', 'update'), description: l('只发要改的字段：name、status（active 或 inactive）、group_id、group_ids、quota、expires_at（ISO 8601）、各限速、ip_whitelist 与 ip_blacklist（空数组表示清空）、reset_quota、reset_rate_limit_usage。', 'Send only what changes: name, status (active or inactive), group_id, group_ids, quota, expires_at (ISO 8601), the rate limits, ip_whitelist and ip_blacklist (an empty array clears), reset_quota, reset_rate_limit_usage.') },
            { name: 'regenerate_key', type: 'boolean', description: l('设为 true 轮换密钥：旧值立刻失效，新明文在这次响应里出现一次；和创建一样需要 verification。', 'Set to true to rotate: the old value stops working at once and the new plaintext appears once in this response. Requires verification, like creation.') },
            { name: 'POST /api/v1/keys/batch-rotate', type: l('批量轮换', 'batch rotate'), description: l('{"ids":[…],"verification":{…}}，一次最多 50 把。身份校验不通过或 ids 中任一把不存在/不属于当前账号时整批拒绝（400 / 404），一把都不轮换；通过后逐把事务轮换，返回 ok 或 failed 与 error_code。', '{"ids":[…],"verification":{…}}, up to 50 keys. If verification fails or any ID is missing or not owned by this account, the entire batch is rejected (400 / 404) without rotating any key; otherwise each key rotates in its own transaction and reports ok or failed with an error_code.') },
            { name: 'DELETE /api/v1/keys/:id', type: l('删除', 'delete'), description: l('删除后密钥立刻失效，无法恢复。', 'The key stops working at once and cannot be restored.') },
            { name: 'POST /api/v1/keys/:id/block-datacenter-ips', type: l('机房 IP', 'datacenter IPs'), description: l('把这把密钥最近使用过的机房与代理 IP 加入黑名单。', 'Adds the datacenter and proxy IPs this key was recently used from to its deny-list.') },
          ],
          examples: {
            curl: `curl -X PUT ${origin}/api/v1/keys/42 \\\n  -H "Authorization: Bearer $ACCESS_TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ quota: 50, regenerate_key: true, verification: { method: 'totp', totp_code: '123456' } })}'`,
            shell: `# ${l('批量轮换（一次最多 50 把）', 'Batch rotate (up to 50 keys)')}\ncurl -X POST ${origin}/api/v1/keys/batch-rotate \\\n  -H "Authorization: Bearer $ACCESS_TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ ids: [12, 42], verification: { method: 'totp', totp_code: '123456' } })}'`,
            json: JSON.stringify({ code: 0, message: 'success', data: { results: [{ id: 12, status: 'ok', key: 'YOUR_ROTATED_API_KEY' }, { id: 42, status: 'failed', error_code: 'API_KEY_ROTATE_FAILED' }], requested_count: 2, succeeded_count: 1, failed_count: 1 } }, null, 2),
          },
        },
        {
          id: 'console-groups', title: l('可用分组与倍率', 'Groups and rates'),
          endpoint: { method: 'GET', path: '/api/v1/groups/available', auth: 'session' },
          paragraphs: [
            l('创建或修改密钥前，先拉取当前账号可绑定的分组与有效校验方式；若希望自定义创建与轮换密钥前的身份校验门槛，可通过安全开关在 on、auto、off 三档之间切换。', 'Fetch bindable groups and active verification factors before creating or updating keys. Use the security switch to choose on, auto or off for key creation and rotation.'),
          ],
          fields: [
            { name: 'GET /api/v1/groups/available', type: l('分组', 'groups'), description: l('当前账号可以绑定的分组，含状态、基础倍率与高峰倍率设置。', 'The groups this account may bind, with state, base multiplier and peak settings.') },
            { name: 'GET /api/v1/groups/rates', type: l('专属倍率', 'personal rates'), description: l('{分组 id: 倍率}，只列出账号有专属倍率的分组。', '{group id: multiplier}, only for groups where the account has a personal rate.') },
            { name: 'GET /api/v1/user/security/verification-factors', type: l('校验方式', 'verification'), description: l('这个账号现在可用的校验方式，以及创建与轮换密钥前是否必须校验（create_verification_required，是把你的开关与账号因子一起算出来的有效值；create_verification_default_required 是不设置开关时的默认值）。', 'The verification methods this account can use now, and whether creating or rotating a key requires one (create_verification_required is the effective value, combining your own switch with the account factors; create_verification_default_required is what the default would be without the switch).') },
            { name: 'PUT /api/v1/user/security/api-key-create-verification', type: l('校验开关', 'verification switch'), description: l('自己决定创建与轮换密钥前要不要验证身份，body 为 {"mode":"on"|"off"|"auto"}。保护强度 on > auto > off，只要是降低强度（on 改 auto / off，auto 改 off）就需要附带 verification，升高强度不需要。', 'Decide yourself whether creating or rotating a key asks you to verify. The body is {"mode":"on"|"off"|"auto"}. Protection strength is on > auto > off: any step down (on to auto or off, auto to off) needs a verification object; stepping up does not.') },
          ],
          examples: {
            curl: `curl ${origin}/api/v1/groups/available \\\n  -H "Authorization: Bearer $ACCESS_TOKEN"`,
            shell: `# ${l('查询当前校验方式并切换校验强度（降低强度时需携带 verification）', 'Inspect factors and adjust verification mode (stepping down requires verification)')}\ncurl ${origin}/api/v1/user/security/verification-factors \\\n  -H "Authorization: Bearer $ACCESS_TOKEN"\n\ncurl -X PUT ${origin}/api/v1/user/security/api-key-create-verification \\\n  -H "Authorization: Bearer $ACCESS_TOKEN" \\\n  -H "Content-Type: application/json" \\\n  -d '{"mode":"on"}'`,
          },
        },
        {
          id: 'console-errors', title: l('错误与限流', 'Errors and limits'),
          paragraphs: [
            l('控制台接口出错时，HTTP 状态码与响应体 code 保持一致，并在 reason 字段给出稳定的机器可读原因码；客户端应根据 reason 处理分支，不要解析 message 文案。', 'Console API errors mirror the HTTP status in code and include a stable machine-readable reason. Branch on reason rather than parsing message strings.'),
          ],
          fields: [
            { name: 'VERIFY_REQUIRED', type: '400', description: l('账号需要身份校验，但请求里没有带 verification。', 'The account requires verification and the request carried none.') },
            { name: 'VERIFY_PASSWORD_INVALID / VERIFY_TOTP_INVALID / VERIFY_PASSKEY_FAILED', type: '400', description: l('校验材料不正确。', 'The verification material is wrong.') },
            { name: 'VERIFY_FACTOR_UNAVAILABLE / VERIFY_FACTOR_MISMATCH', type: '400', description: l('这个账号没有该校验方式，或与已有授权的方式不一致。', 'The account does not have that method, or it does not match the one already authorised.') },
            { name: 'VERIFY_PASSWORD_RATE_LIMITED', type: '429', description: l('密码错误次数过多，按 Retry-After 等待。', 'Too many wrong passwords; wait for Retry-After.') },
            { name: 'RATE_LIMITED', type: '429', description: l('超过账号级限流，默认每分钟 240 次，统计类接口 60 次，创建与轮换另有每分钟 10 次；管理员可以调整。', 'Account-level limit exceeded: by default 240 a minute overall, 60 for statistics endpoints, and 10 a minute for create and rotate. An administrator can change these.') },
          ],
          examples: {
            json: JSON.stringify({ code: 400, message: 'Identity verification is required', reason: 'VERIFY_REQUIRED' }, null, 2),
          },
          links: [
            { label: l('用密钥查询自身信息（只读接口）', 'Key self-service API (read-only)'), page: 'key-self-service' },
            { label: l('Agent 新手配置指南（含 CC Switch）', 'Agent setup guide (with CC Switch)'), page: 'agent-setup' },
          ],
        },
      ],
    },
    {
      id: 'chat-completions', category: 'reference', title: 'Chat Completions', description: 'POST /v1/chat/completions', sections: [
        { id: 'chat-request', title: l('请求参数', 'Request parameters'), fields: [commonModel, { name: 'messages', type: 'array', required: true, description: l('按顺序提供 role 与 content；多轮请求携带应用需要保留的历史。', 'Provide ordered role/content messages, including the history your application needs.') }, streamField, { name: 'tools', type: 'array', description: l('模型支持的函数工具定义；实际支持范围由模型与上游决定。', 'Function tool definitions supported by the model and upstream.') }], examples: requestExamples(origin, '/v1/chat/completions', textRequest) },
        { id: 'chat-result', title: l('返回结构', 'Response structure'), paragraphs: [l('非流式读取 choices[].message；函数调用读取 tool_calls。流式读取 choices[].delta，并处理错误帧与结束标记。支持的可选字段以对应模型和上游协议为准。', 'Read choices[].message for non-streaming results and tool_calls for functions. For streams, consume choices[].delta, error frames, and completion markers. Optional fields depend on the model and provider protocol.')] },
      ],
    },
    {
      id: 'responses', category: 'reference', title: 'Responses', description: 'POST /v1/responses', sections: [
        { id: 'responses-request', title: l('请求参数', 'Request parameters'), fields: [commonModel, { name: 'input', type: 'string | array', required: true, description: l('文本输入或 Responses 输入项目。', 'Text input or Responses input items.') }, streamField, { name: 'previous_response_id', type: 'string', description: l('仅在对应上游支持并保留同一上下文时使用；切换上游账号可能无法继续旧响应。', 'Use only when the provider supports continuing that context. Switching provider accounts can make an older response unavailable.') }], examples: requestExamples(origin, '/v1/responses', responseRequest) },
        { id: 'responses-result', title: l('返回结构', 'Response structure'), paragraphs: [l('非流式结果读取 output 项目及其中的 content。流式关注 response.output_text.delta、工具事件及 response.completed / response.failed 终态。', 'Read output items and their content in non-streaming responses. In streams, handle response.output_text.delta, tool events, and response.completed / response.failed terminal states.')] },
      ],
    },
    {
      id: 'messages', category: 'reference', title: 'Anthropic Messages', description: 'POST /v1/messages', sections: [
        { id: 'messages-request', title: l('请求参数', 'Request parameters'), fields: [commonModel, { name: 'max_tokens', type: 'integer', required: true, description: l('输出 Token 上限，范围由模型和上游决定。', 'Output token limit, subject to model and provider limits.') }, { name: 'messages', type: 'array', required: true, description: l('按 Anthropic 格式提供 user / assistant 消息及内容块。', 'Anthropic-format user / assistant messages and content blocks.') }, { name: 'system', type: 'string | array', description: l('顶层系统提示；缓存控制可附在受支持的内容块中。', 'Top-level system prompt; supported content blocks may carry cache controls.') }, streamField], examples: requestExamples(origin, '/v1/messages', messageRequest, true) },
        { id: 'messages-result', title: l('返回结构', 'Response structure'), paragraphs: [l('非流式结果读取 content 数组。流式依序处理 message_start、content_block_*、message_delta 和 message_stop，并始终处理 error 事件。usage 的输入与缓存分类应按原始字段读取。', 'Read the content array for non-streaming responses. Stream consumers handle message_start, content_block_*, message_delta, message_stop, and error events. Preserve input and cache usage categories as returned.')] },
      ],
    },
    {
      id: 'deepseek-search', category: 'reference', title: l('网页搜索 Search', 'Web search'), description: 'POST /v1/tools/search', sections: [
        { id: 'search-access', title: l('直接调用', 'Direct access'), note: deepseekToolAccess, paragraphs: [l('无需 model、messages、tools 或 stream。使用本站 Bearer 密钥，官方 JSON 字段直接透传；仍受余额、密钥权限、并发和上游限制。', 'No model, messages, tools, or stream field is required. Use a site Bearer key; official JSON fields are forwarded. Balance, key permissions, concurrency, and upstream limits still apply.')], examples: requestExamples(origin, '/v1/tools/search', { query: 'DeepSeek search API', count: 5, need_content: false }) },
        { id: 'search-parameters', title: l('官方参数', 'Official parameters'), fields: [
          { name: 'query', type: 'string', required: true, description: l('1–128 个 Unicode 字符，不能全为空白。支持 site:、-site:、精确短语、lang:、after:、before: 等官方搜索语法。', '1–128 Unicode characters, not all whitespace. Supports official syntax such as site:, -site:, exact phrases, lang:, after:, and before:.') },
          { name: 'count', type: 'integer', description: l('1–50，默认 10。', '1–50, default 10.') },
          { name: 'ori_question', type: 'string | null', description: l('可选原始问题，1–128 字符；null 视为未提供。', 'Optional original question, 1–128 characters; null is treated as absent.') },
          { name: 'localization', type: 'string', description: l('用于本地化的 IP 字符串，默认空字符串。', 'IP string for localization; defaults to an empty string.') },
          { name: 'need_content', type: 'boolean', description: l('默认 false；true 时请求网页正文。', 'Defaults to false; true requests webpage content.') },
        ] },
        { id: 'search-response', title: l('结果与计费', 'Results and billing'), paragraphs: [l('响应 results 数组保留官方字段：rerank_score、title、url、snippet、summary、content、site_name、site_icon、date、authority。具体可空字段和结果数量由官方返回决定。', 'The results array preserves official fields: rerank_score, title, url, snippet, summary, content, site_name, site_icon, date, and authority. Nullable fields and actual result counts follow the provider response.'), l('HTTP 200 且 results 非空才计一次成功工具费，无模型 Token 消费；失败或空结果不收费。实际单价为本站配置的基准价乘以分组或用户生效倍率（分组开启工具独立倍率时按独立倍率计算，不含高峰倍率），可在模型广场查看实时折后价。', 'A successful tool fee is counted only for HTTP 200 with nonempty results, with no model tokens; failed or empty results are not billed. The effective unit price is the site-configured base price multiplied by the effective group or user multiplier (or the group’s independent tool multiplier when enabled, excluding peak rate); check Model Plaza for live rates.')], links: [{ label: l('DeepSeek 官方搜索文档', 'Official DeepSeek search guide'), href: 'https://api-docs.deepseek.com/zh-cn/guides/search/' }] },
      ],
    },
    {
      id: 'deepseek-fetch', category: 'reference', title: l('网页抓取 Fetch', 'Web fetch'), description: 'POST /v1/tools/fetch', sections: [
        { id: 'fetch-access', title: l('直接抓取正文', 'Fetch page content directly'), paragraphs: [l('入口分组与鉴权要求和 Search 相同。发送网页 URL 即可，不需要让模型先生成 web_fetch 工具调用。', 'Entry-group and authentication requirements match Search. Send a webpage URL directly; a model-generated web_fetch call is unnecessary.')], examples: requestExamples(origin, '/v1/tools/fetch', { url: 'https://api-docs.deepseek.com/zh-cn/guides/search/' }) },
        { id: 'fetch-parameters', title: l('官方参数', 'Official parameters'), fields: [{ name: 'url', type: 'string', required: true, description: l('非空，最长 8192 字符；必须是带主机名的 HTTP / HTTPS URL，不能包含用户名和密码。', 'Nonempty, at most 8192 characters. Must be an HTTP/HTTPS URL with a host and without username/password credentials.') }] },
        { id: 'fetch-response', title: l('结果与计费', 'Results and billing'), examples: { json: JSON.stringify({ title: 'Example page', content: '# Example\n\nPage content in Markdown.' }, null, 2) }, paragraphs: [l('官方响应为 title 与 Markdown content，不保证包含 url 字段。HTTP 200 且 content 非空才计一次成功工具费，无模型 Token 消费。Fetch 支持按分组单独配置基准价（留空默认继承 Search 基准价），并叠加分组或用户生效倍率（或工具独立倍率，不含高峰倍率）。', 'The official response contains title and Markdown content; it does not promise a url field. A successful call fee is counted only for HTTP 200 and nonempty content, with no model tokens. Fetch supports an independent per-group base price (inheriting the Search base price when unset) multiplied by the effective group or user multiplier (or the independent tool multiplier, excluding peak rate).'), l('独立 API 返回的正文与模型内工具的结果长度限制不同。网页可达性与反爬限制由上游和目标站点决定；工具可用不保证每个 URL 都能抓取。', 'The direct API and the model-hosted tool have different content-length handling. Provider reachability and target-site restrictions still apply; tool availability does not guarantee every URL can be fetched.')], links: [{ label: l('DeepSeek 官方 Fetch API', 'Official DeepSeek Fetch API'), href: 'https://api-docs.deepseek.com/zh-cn/api/fetch/' }] },
      ],
    },
    {
      id: 'deepseek-asr', category: 'reference', title: l('语音识别 ASR', 'Speech recognition ASR'), description: 'POST /v1/tools/asr', sections: [
        { id: 'asr-access', title: l('接入条件', 'Access requirements'), note: deepseekToolAccess, paragraphs: [l('使用本站 Authorization: Bearer 密钥发送 multipart/form-data。无需 model、messages 或语种参数；本接口不支持流式识别。余额、密钥权限、并发和上游限制仍然适用。', 'Send multipart/form-data with your site Authorization: Bearer key. No model, messages, or language parameter is needed; streaming recognition is not supported. Balance, key permissions, concurrency, and upstream limits still apply.')] },
        { id: 'asr-parameters', title: l('请求参数', 'Request parameters'), fields: [
          { name: 'file', type: 'file (multipart/form-data)', required: true, description: l('音频文件，只接受 16 位 PCM WAV 或 Ogg Opus；最大 8 MiB，最长 120 秒，上传须在 10 分钟内完成。双声道只识别左声道。', 'Audio file: 16-bit PCM WAV or Ogg Opus only. Maximum 8 MiB and 120 seconds; complete the upload within 10 minutes. Only the left channel of stereo audio is recognized.') },
        ], examples: multipartToolExamples(origin, '/v1/tools/asr', 'audio.wav', 'audio/wav') },
        { id: 'asr-response', title: l('响应结构', 'Response structure'), paragraphs: [l('成功后一次返回 JSON：request_id 用于关联请求，text 是识别文本，duration_ms 是音频时长（毫秒）。', 'A successful request returns one JSON object: request_id identifies the request, text is the transcript, and duration_ms is audio duration in milliseconds.')], examples: { json: JSON.stringify({ request_id: 'asr-example', text: l('上海今天天气怎么样？', 'What is the weather in Shanghai today?'), duration_ms: 3200 }, null, 2) } },
        { id: 'asr-errors', title: l('常见错误', 'Common errors'), fields: [
          { name: '400', type: 'HTTP', description: l('缺少 file、音频格式不对或请求无效；先核对编码，不要仅修改扩展名。', 'Missing file, unsupported audio format, or invalid request. Check the encoding rather than just changing the extension.') },
          { name: '408 / 413', type: 'HTTP', description: l('上传超时 / 文件超过 8 MiB；压缩或切分后重新上传。', 'Upload timed out / file exceeds 8 MiB. Compress or split the audio before uploading again.') },
          { name: '429', type: 'HTTP', description: l('并发超限；官方每账号最多同时 50 个请求，本站还受用户与密钥并发限制。', 'Concurrency limit reached. The provider allows up to 50 simultaneous requests per account; site user and key limits also apply.') },
          { name: '503 / 504', type: 'HTTP', description: l('上游暂不可用 / 处理超时；适当退避后重试。', 'Provider temporarily unavailable / processing timed out. Back off before retrying.') },
        ] },
        { id: 'asr-billing', title: l('计费与限制', 'Billing and limits'), paragraphs: [l('按音频时长计费，精确到秒，向上取整；不足 1 秒按 1 秒，例如 duration_ms=3200 按 4 秒计。每小时基准价以本站及分组配置为准，实际扣费默认叠加分组或用户生效倍率（开启工具独立倍率时按独立倍率计算，不含高峰倍率）。', 'Billing uses audio duration rounded up to whole seconds, with a minimum of one second; duration_ms=3200 is billed as 4 seconds. The hourly base price follows this site’s and group’s configuration, multiplied by the effective group or user multiplier by default (or the independent tool multiplier when enabled, excluding peak rate).'), l('不要发送 stream 或语种参数。长音频先切分，双声道中右声道的内容不会被识别。', 'Do not send stream or a language parameter. Split long audio first; speech in the right stereo channel is not recognized.')], links: [{ label: l('DeepSeek 官方 ASR 文档', 'Official DeepSeek ASR guide'), href: 'https://api-docs.deepseek.com/zh-cn/guides/asr/' }] },
      ],
    },
    {
      id: 'deepseek-tts', category: 'reference', title: l('语音合成 TTS', 'Speech synthesis TTS'), description: 'POST /v1/tools/tts', sections: [
        { id: 'tts-access', title: l('接入条件', 'Access requirements'), note: deepseekToolAccess, paragraphs: [l('使用本站 Authorization: Bearer 密钥发送 JSON，文本一次提交，音频边合成边返回。这是 HTTP 整段合成，不是 SSE，也不需要 model。', 'Send JSON with your site Authorization: Bearer key. Submit the full text once; audio is returned as it is synthesized. This is whole-text HTTP synthesis, not SSE, and needs no model.')] },
        { id: 'tts-parameters', title: l('请求参数', 'Request parameters'), fields: [
          { name: 'text', type: 'string', required: true, description: l('非空、可朗读的文本，最多 10,000 个计费字符；整个 JSON 请求体最大 64 KiB。', 'Nonempty, speakable text, at most 10,000 billable characters. The entire JSON body must not exceed 64 KiB.') },
          { name: 'voice_id', type: 'string', description: l('stella：默认女声；tide：男声。', 'stella: default female voice; tide: male voice.') },
          { name: 'format', type: 'string', description: l('wav（默认）或 pcm；单次最多输出 30 分钟音频。', 'wav (default) or pcm; up to 30 minutes of output audio per request.') },
          { name: 'filter_markdown', type: 'boolean', description: l('默认 true，过滤 Markdown 标记以便朗读。', 'Defaults to true; filters Markdown markup for speech.') },
        ], examples: {
          curl: `curl --fail-with-body --no-buffer ${origin}/v1/tools/tts \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(ttsRequest)}' \\\n  --output speech.wav`,
          python: `import os\nimport requests\n\nwith requests.post(\n    "${origin}/v1/tools/tts",\n    headers={"Authorization": "Bearer " + os.environ["API_KEY"]},\n    json=${JSON.stringify(ttsRequest).replace(/\btrue\b/g, 'True')},\n    stream=True,\n    timeout=1800,\n) as response:\n    response.raise_for_status()\n    with open("speech.wav", "wb") as audio:\n        for chunk in response.iter_content(chunk_size=8192):\n            audio.write(chunk)`,
          node: `import { createWriteStream } from "node:fs";\nimport { Readable } from "node:stream";\nimport { pipeline } from "node:stream/promises";\n\nconst response = await fetch("${origin}/v1/tools/tts", {\n  method: "POST",\n  headers: {\n    Authorization: \x60Bearer \x24{process.env.API_KEY}\x60,\n    "Content-Type": "application/json",\n  },\n  body: JSON.stringify(${JSON.stringify(ttsRequest, null, 2)}),\n});\nif (!response.ok) throw new Error(\x60HTTP \x24{response.status}\x60);\nif (!response.body) throw new Error("Missing audio body");\nawait pipeline(Readable.fromWeb(response.body), createWriteStream("speech.wav"));`,
        } },
        { id: 'tts-response', title: l('音频响应', 'Audio response'), paragraphs: [l('响应体直接是音频，不是 JSON，也不包含音频 URL。wav 的 Content-Type 为 audio/wav；pcm 是 24 kHz、16 位、单声道原始 PCM，播放时要显式设置这些参数。', 'The response body is audio, not JSON or an audio URL. wav uses Content-Type: audio/wav; pcm is raw 24 kHz, 16-bit, mono PCM, so set those parameters explicitly for playback.'), l('持续读取响应并写入文件，不要等整段生成完才开始接收。音频开始返回后合成仍可能失败并中途断开，响应提前中断必须当作失败；丢弃不完整文件，不要仅凭 HTTP 200 判断成功。示例让下载错误继续抛出，请在应用中处理并清理半成品。', 'Keep reading the response and writing it to a file without waiting for synthesis to finish. Synthesis may fail after audio starts and terminate the response early. Treat an interrupted response as a failure, discard the partial file, and do not assume HTTP 200 means success. The examples let download errors propagate; handle them and clean up partial files in your application.')], note: l('本站暂不支持 WebSocket 流式合成，请用上述 HTTP 整段合成。', 'This site does not support WebSocket streaming synthesis yet. Use the whole-text HTTP synthesis above.') },
        { id: 'tts-errors', title: l('常见错误', 'Common errors'), fields: [
          { name: '400', type: 'HTTP', description: l('没有可朗读文本、参数无效或内容审核不通过（Content Exists Risk）；调整文本后再提交。', 'No speakable text, invalid parameters, or content moderation rejection (Content Exists Risk). Adjust the text before resubmitting.') },
          { name: '429', type: 'HTTP', description: l('并发超限，降低并发并退避重试。', 'Concurrency limit reached. Reduce concurrency and back off before retrying.') },
          { name: '503 / 504', type: 'HTTP', description: l('上游暂不可用 / 合成超时。音频返回后的失败也可能表现为连接中断。', 'Provider unavailable / synthesis timed out. Failures after audio starts may appear as a disconnected response.') },
        ] },
        { id: 'tts-billing', title: l('计费与限制', 'Billing and limits'), paragraphs: [l('按请求 text 的计费字符数计费：汉字算 2 个字符，其余字符（英文字母、数字、空格等）算 1 个。例如“你好 A”计 6 个字符。即使过滤 Markdown，也按请求文本计。每百万字符基准价以本站及分组配置为准，实际扣费默认叠加分组或用户生效倍率（开启工具独立倍率时按独立倍率计算，不含高峰倍率）。', 'Billing counts characters in the request text: each Chinese character counts as 2; every other character (letters, digits, spaces, etc.) counts as 1. For example, “你好 A” counts as 6. Billing uses the request text even when Markdown filtering is enabled. The base price per million characters follows this site’s and group’s configuration, multiplied by the effective group or user multiplier by default (or the independent tool multiplier when enabled, excluding peak rate).'), l('单次请求须同时满足 10,000 个计费字符、64 KiB 请求体与 30 分钟输出音频限制。需要交互式逐句输入的 WebSocket 功能不在本站本次支持范围内。', 'Each request must fit all three limits: 10,000 billable characters, a 64 KiB request body, and 30 minutes of output audio. Interactive WebSocket input is outside the site’s current scope.')], links: [{ label: l('DeepSeek 官方 TTS 文档', 'Official DeepSeek TTS guide'), href: 'https://api-docs.deepseek.com/zh-cn/guides/tts' }] },
      ],
    },
    {
      id: 'deepseek-ocr', category: 'reference', title: l('文档识别 OCR', 'Document recognition OCR'), description: 'POST /v1/tools/ocr', sections: [
        { id: 'ocr-access', title: l('接入条件', 'Access requirements'), note: deepseekToolAccess, paragraphs: [l('使用本站 Authorization: Bearer 密钥发送 multipart/form-data。只发送 file 与 purpose 两个字段，不需要 model、messages 或 stream，也不能附带其他字段。', 'Send multipart/form-data with your site Authorization: Bearer key. Send only file and purpose: no model, messages, stream, or other fields.')] },
        { id: 'ocr-parameters', title: l('请求参数', 'Request parameters'), fields: [
          { name: 'file', type: 'file (multipart/form-data)', required: true, description: l('PDF、Word、PowerPoint 或图片，最大 100 MiB，最多 1,000 页。', 'PDF, Word, PowerPoint, or image file, up to 100 MiB and 1,000 pages.') },
          { name: 'purpose', type: 'string', required: true, description: l('text_recognition：文本识别；rich_layout_recognition：包含版面与坐标的识别。两个字段都必填；额外字段会返回 400。', 'text_recognition for text; rich_layout_recognition for layout and coordinates. Both fields are required; extra fields return 400.') },
        ], examples: multipartToolExamples(origin, '/v1/tools/ocr', 'document.pdf', 'application/pdf', { purpose: 'text_recognition' }) },
        { id: 'ocr-response', title: l('响应与保活', 'Response and keepalive'), paragraphs: [l('识别过程中服务端持续发送换行符保活，识别完成后才返回一个 JSON 对象。这些换行符只是 JSON 的前导空白，不是 SSE；读完整个响应后可以直接按 JSON 解析。总时长从开始上传算起，最多 180 秒。', 'During recognition the server sends newline keepalives, then one JSON object on completion. These newlines are leading JSON whitespace, not SSE. Read the full response and parse it as JSON. The total time limit is 180 seconds from the start of upload.'), l('成功结果的 page_count 是识别成功的页数，pages[] 按 index 返回内容，finish_reason 描述该页结束原因。usage 保留上游用量字段，最终扣费以本站使用记录为准。', 'On success, page_count is the number of successfully recognized pages; pages[] contains indexed content and a finish_reason per page. usage preserves provider usage fields; finalized charges appear in this site’s usage records.')], examples: { json: JSON.stringify({ id: 'ocr-example', object: 'ocr.result', created: 1700000000, purpose: 'text_recognition', page_count: 2, pages: [{ index: 0, text: l('第一页内容', 'First page'), finish_reason: 'stop' }, { index: 1, text: l('第二页内容', 'Second page'), finish_reason: 'stop' }], usage: {} }, null, 2) } },
        { id: 'ocr-layout', title: l('版面坐标标记', 'Layout coordinate markers'), paragraphs: [l('rich_layout_recognition 的文本可能带有 <｜ref｜> 与 <｜box｜> 标记，用来关联文字与边界框。坐标按 0–1000 归一化，不是原图像素；映射回原图时按图片宽高换算。纯文本用途请选择 text_recognition。', 'rich_layout_recognition text may contain <｜ref｜> and <｜box｜> markers linking text to bounding boxes. Coordinates are normalized to 0–1000 rather than source pixels; scale them by the image width and height. Choose text_recognition when you only need text.')] },
        { id: 'ocr-errors', title: l('HTTP 200 仍要检查 error', 'Check error even after HTTP 200'), note: l('识别开始后状态码已是 200，后续失败或超时会在响应体中返回 error 对象，而不是识别结果。客户端必须检查 error，不能只看 HTTP 状态码；type 与 code 以实际返回值为准，下例仅示意结构。', 'Once recognition starts the HTTP status is already 200. A later failure or timeout returns an error object in the body rather than a recognition result. Always inspect error instead of relying only on the HTTP status. Use the returned type and code; the example only illustrates the structure.'), examples: { json: JSON.stringify({ error: { message: 'Recognition timed out', type: 'UPSTREAM_ERROR_TYPE', param: null, code: 'UPSTREAM_ERROR_CODE' } }, null, 2) }, fields: [
          { name: '400 / 413', type: 'HTTP (upload)', description: l('参数缺失、格式不支持或带了额外字段 / 文件过大。', 'Missing fields, unsupported format, or extra fields / file too large.') },
          { name: '402', type: 'HTTP (upload)', description: l('余额不足；核对本站余额与上游返回的错误信息。', 'Insufficient balance. Check your site balance and the returned error message.') },
          { name: '408 / 504', type: 'HTTP (upload)', description: l('上传或处理超时；识别开始后超时改为响应体内的 error。', 'Upload or processing timeout. After recognition starts, timeouts appear as an error in the body.') },
          { name: '429', type: 'HTTP (upload)', description: l('并发超限；官方每账号最多同时 15 个识别，本站并发限制也适用。', 'Concurrency limit reached. The provider allows up to 15 recognitions per account; site limits also apply.') },
          { name: '500 / 503', type: 'HTTP (upload)', description: l('上游内部错误 / 暂不可用，退避后重试。', 'Provider internal error / temporary unavailability. Back off before retrying.') },
        ] },
        { id: 'ocr-billing', title: l('计费与限制', 'Billing and limits'), paragraphs: [l('只按识别成功的 page_count 页数计费，识别失败或超时不计费。每千页基准价以本站及分组配置为准，实际扣费默认叠加分组或用户生效倍率（开启工具独立倍率时按独立倍率计算，不含高峰倍率）。', 'Only successfully recognized pages reported by page_count are billed; recognition failures and timeouts are not charged. The base price per 1,000 pages follows this site’s and group’s configuration, multiplied by the effective group or user multiplier by default (or the independent tool multiplier when enabled, excluding peak rate).'), l('客户端断开不会取消识别：文件接收完成后识别继续执行，完成的照常计费。上传后不要把关闭页面当作取消，也不要盲目重试同一文件以免重复识别。', 'A client disconnect does not cancel recognition: once the file has been received, recognition continues and completed work remains billable. Closing the page is not cancellation; do not blindly retry the same file and create duplicate recognition work.')], links: [{ label: l('DeepSeek 官方 OCR 文档', 'Official DeepSeek OCR guide'), href: 'https://api-docs.deepseek.com/zh-cn/guides/ocr' }] },
      ],
    },
  ]
}

export function resolveDocsPage(pages: DocsPage[], value: unknown): DocsPage {
  const id = typeof value === 'string' ? value : DOCS_DEFAULT_PAGE
  return pages.find(page => page.id === id) || pages.find(page => page.id === DOCS_DEFAULT_PAGE) || pages[0]
}
