export type DocsCategory = 'start' | 'guides' | 'integrations' | 'reference'
export type DocsLanguage = 'curl' | 'python' | 'node' | 'json' | 'toml' | 'shell'

export interface DocsField {
  name: string
  type: string
  required?: boolean
  description: string
}

export interface DocsSection {
  id: string
  title: string
  paragraphs?: string[]
  note?: string
  fields?: DocsField[]
  examples?: Partial<Record<DocsLanguage, string>>
  links?: { label: string; href: string }[]
}

export interface DocsPage {
  id: string
  category: DocsCategory
  title: string
  description: string
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

export function getApiDocs(locale: string, originValue: string): DocsPage[] {
  const zh = locale.toLowerCase().startsWith('zh')
  const l = (cn: string, en: string) => zh ? cn : en
  const origin = resolveDocsOrigin(originValue)
  const textRequest = { model: 'YOUR_MODEL_ID', messages: [{ role: 'user', content: 'Hello!' }] }
  const responseRequest = { model: 'YOUR_MODEL_ID', input: 'Hello!' }
  const messageRequest = { ...textRequest, max_tokens: 1024 }
  const commonModel: DocsField = { name: 'model', type: 'string', required: true, description: l('从本站模型列表复制完整模型 ID；可用模型取决于密钥绑定的分组。', 'Copy the complete model ID from this site. Availability depends on the groups attached to your API key.') }
  const streamField: DocsField = { name: 'stream', type: 'boolean', description: l('设为 true 接收 SSE 事件。客户端必须同时处理正常结束与错误终态。', 'Set to true for SSE events. Handle both successful completion and terminal errors.') }
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
      ],
    },
    {
      id: 'authentication', category: 'start', title: l('密钥与鉴权', 'Keys and authentication'), description: l('同一个本站密钥用于模型接口及可用的独立工具接口。', 'A site API key authenticates model endpoints and available independent tool endpoints.'), sections: [
        { id: 'bearer', title: 'Authorization: Bearer', paragraphs: [l('OpenAI 兼容协议、模型列表与独立 Search/Fetch 接口使用 Bearer 密钥。密钥继承所属用户、绑定分组、额度与访问限制。', 'Use Bearer authentication for OpenAI-compatible endpoints, model listing, and independent Search/Fetch. Your key is subject to its user, assigned groups, quotas, and access restrictions.')], examples: { shell: `curl ${origin}/v1/models \\\n  -H "Authorization: Bearer $API_KEY"` } },
        { id: 'anthropic-auth', title: l('Anthropic 原生鉴权', 'Anthropic authentication'), paragraphs: [l('/v1/messages 支持 x-api-key，搭配 anthropic-version: 2023-06-01。模型与分组还必须允许该协议入口。', '/v1/messages accepts x-api-key with anthropic-version: 2023-06-01. Your model and group must also allow this protocol endpoint.')], examples: { shell: `curl ${origin}/v1/messages \\\n  -H "x-api-key: $API_KEY" \\\n  -H "anthropic-version: 2023-06-01" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(messageRequest)}'` } },
        { id: 'safe-config', title: l('配置建议', 'Configuration notes'), paragraphs: [l('将密钥保存在客户端的私有环境变量或安全配置中。401 通常表示密钥无效；403 需结合错误正文判断分组权限、余额或业务限制。不要把所有错误都当成密钥失效。', 'Store your key in a private environment variable or secure client configuration. 401 generally indicates invalid authentication. For 403, inspect the error body for group permissions, balance, or policy restrictions; not every error means your key is invalid.')] },
      ],
    },
    {
      id: 'models', category: 'start', title: l('模型与价格', 'Models and pricing'), description: l('查询当前密钥真正可以调用的模型，而不是根据客户端图标猜测。', 'Discover models actually available to your API key rather than relying on client icons.'), sections: [
        { id: 'list-models', title: 'GET /v1/models', examples: { curl: `curl ${origin}/v1/models -H "Authorization: Bearer $API_KEY"`, json: JSON.stringify({ object: 'list', data: [{ id: 'YOUR_MODEL_ID', object: 'model' }] }, null, 2) }, paragraphs: [l('响应 data[].id 是请求 model 字段所需的完整标识。绑定分组与上游可用性会影响实际可调用范围。', 'Use the complete data[].id value as the model field. Assigned groups and provider availability affect what can actually be called.')] },
        { id: 'pricing', title: l('了解计费', 'Understand billing'), paragraphs: [l('模型价格、缓存读写价格、分组倍率和工具价格以本站配置为准。使用记录分别保留标准费用和实际扣费；费用已计量不代表内容一定生成成功。', 'Model prices, cache prices, group multipliers, and tool fees follow this site’s configuration. Usage records retain both base cost and actual charges. Metered consumption is not a guarantee of successful delivery.'), l('Search/Fetch 独立调用按成功次数固定计费，不随模型 Token 倍率变化；模型内搜索与抓取的成功工具费固定，模型 Token 仍按适用倍率计算。失败或空结果不计成功工具调用次数。', 'Independent Search/Fetch calls use a fixed fee per successful call, separate from model token multipliers. Hosted search/fetch tool fees are fixed while model tokens retain the applicable multiplier. Failed or empty tools do not count as successful tool calls.')] },
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
      ],
    },
    {
      id: 'codex', category: 'integrations', title: l('接入 Codex', 'Connect Codex'), description: l('使用 OpenAI Responses 协议接入，保持模型与密钥权限一致。', 'Connect through OpenAI Responses with a model your key is authorized to use.'), sections: [
        { id: 'codex-settings', title: l('Provider 配置', 'Provider configuration'), paragraphs: [l('将地址填入 Codex 的自定义 provider 配置。model 使用本站模型列表中的完整 ID。示例不包含真实密钥。', 'Add this base URL to your custom Codex provider. Set model to a complete ID from this site’s model list. No real API key is included in the example.')], examples: { toml: `model = "YOUR_MODEL_ID"\nmodel_provider = "site"\n\n[model_providers.site]\nname = "API gateway"\nbase_url = "${origin}/v1"\nwire_api = "responses"\nenv_key = "SITE_API_KEY"`, shell: 'export SITE_API_KEY="YOUR_API_KEY"\ncodex' } },
        { id: 'codex-tools', title: l('搜索与工具', 'Search and tools'), paragraphs: [l('Codex 的搜索行为取决于客户端是否发出搜索请求，以及本站对应分组是否启用了网页工具。出现 web_search 或 web_fetch 只说明工具可用，最终仍应检查工具结果与引用。', 'Search depends on whether Codex requests it and whether web tools are configured for the selected group. Seeing web_search or web_fetch means a tool is available; still verify its results and citations.'), l('不要把 /v1/responses 手工改成 /v1/chat/completions 来绕过流错误，这会改变协议与工具行为。', 'Do not manually replace /v1/responses with /v1/chat/completions to work around stream errors; that changes protocol and tool behavior.')] },
      ],
    },
    {
      id: 'claude-code', category: 'integrations', title: l('接入 Claude Code', 'Connect Claude Code'), description: l('使用 Anthropic Messages 入口与本站密钥。', 'Use the Anthropic Messages endpoint and a site API key.'), sections: [
        { id: 'claude-environment', title: l('环境变量', 'Environment variables'), paragraphs: [l('ANTHROPIC_BASE_URL 填站点根地址，客户端会追加 /v1/messages。模型必须在该密钥可用范围内，且分组允许 messages 入口。', 'Set ANTHROPIC_BASE_URL to the site origin; the client appends /v1/messages. Select an authorized model, and ensure the group permits the messages endpoint.')], examples: { shell: `export ANTHROPIC_BASE_URL="${origin}"\nexport ANTHROPIC_AUTH_TOKEN="YOUR_API_KEY"\nexport ANTHROPIC_MODEL="YOUR_MODEL_ID"\nclaude` } },
        { id: 'claude-cache', title: l('缓存与请求结构', 'Caching and request structure'), paragraphs: [l('客户端发送的 cache_control 标记应保留。不要为了缓存随意更改工具顺序、系统消息或历史内容；缓存命中与上游支持、前缀稳定性及本站现有开关有关。', 'Preserve client-provided cache_control markers. Avoid changing tool ordering, system messages, or history solely to force caching. Cache hits depend on provider support, stable prefixes, and this site’s existing configuration.')] },
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
        { id: 'search-access', title: l('直接调用', 'Direct access'), note: l('当前独立工具入口要求密钥的入口分组为 DeepSeek 平台。仅把 DeepSeek 添加为其他平台入口的后备分组，仍可能得到 404。', 'The current independent tool handler requires a DeepSeek entry group. Merely adding a DeepSeek fallback to another platform’s entry group may still return 404.'), paragraphs: [l('无需 model、messages、tools 或 stream。使用本站 Bearer 密钥，官方 JSON 字段直接透传；仍受余额、密钥权限、并发和上游限制。', 'No model, messages, tools, or stream field is required. Use a site Bearer key; official JSON fields are forwarded. Balance, key permissions, concurrency, and upstream limits still apply.')], examples: requestExamples(origin, '/v1/tools/search', { query: 'DeepSeek search API', count: 5, need_content: false }) },
        { id: 'search-parameters', title: l('官方参数', 'Official parameters'), fields: [
          { name: 'query', type: 'string', required: true, description: l('1–128 个 Unicode 字符，不能全为空白。支持 site:、-site:、精确短语、lang:、after:、before: 等官方搜索语法。', '1–128 Unicode characters, not all whitespace. Supports official syntax such as site:, -site:, exact phrases, lang:, after:, and before:.') },
          { name: 'count', type: 'integer', description: l('1–50，默认 10。', '1–50, default 10.') },
          { name: 'ori_question', type: 'string | null', description: l('可选原始问题，1–128 字符；null 视为未提供。', 'Optional original question, 1–128 characters; null is treated as absent.') },
          { name: 'localization', type: 'string', description: l('用于本地化的 IP 字符串，默认空字符串。', 'IP string for localization; defaults to an empty string.') },
          { name: 'need_content', type: 'boolean', description: l('默认 false；true 时请求网页正文。', 'Defaults to false; true requests webpage content.') },
        ] },
        { id: 'search-response', title: l('结果与计费', 'Results and billing'), paragraphs: [l('响应 results 数组保留官方字段：rerank_score、title、url、snippet、summary、content、site_name、site_icon、date、authority。具体可空字段和结果数量由官方返回决定。', 'The results array preserves official fields: rerank_score, title, url, snippet, summary, content, site_name, site_icon, date, and authority. Nullable fields and actual result counts follow the provider response.'), l('HTTP 200 且 results 非空才计一次成功工具费。固定按次费用以本站配置为准，无模型 Token 消费；失败或空结果不收成功工具费。', 'A successful tool fee is counted only for HTTP 200 with nonempty results. The fixed per-call price follows this site’s configuration; no model tokens are used. Failed or empty results do not incur a successful tool fee.')], links: [{ label: l('DeepSeek 官方搜索文档', 'Official DeepSeek search guide'), href: 'https://api-docs.deepseek.com/zh-cn/guides/search/' }] },
      ],
    },
    {
      id: 'deepseek-fetch', category: 'reference', title: l('网页抓取 Fetch', 'Web fetch'), description: 'POST /v1/tools/fetch', sections: [
        { id: 'fetch-access', title: l('直接抓取正文', 'Fetch page content directly'), paragraphs: [l('入口分组与鉴权要求和 Search 相同。发送网页 URL 即可，不需要让模型先生成 web_fetch 工具调用。', 'Entry-group and authentication requirements match Search. Send a webpage URL directly; a model-generated web_fetch call is unnecessary.')], examples: requestExamples(origin, '/v1/tools/fetch', { url: 'https://api-docs.deepseek.com/zh-cn/guides/search/' }) },
        { id: 'fetch-parameters', title: l('官方参数', 'Official parameters'), fields: [{ name: 'url', type: 'string', required: true, description: l('非空，最长 8192 字符；必须是带主机名的 HTTP / HTTPS URL，不能包含用户名和密码。', 'Nonempty, at most 8192 characters. Must be an HTTP/HTTPS URL with a host and without username/password credentials.') }] },
        { id: 'fetch-response', title: l('结果与计费', 'Results and billing'), examples: { json: JSON.stringify({ title: 'Example page', content: '# Example\n\nPage content in Markdown.' }, null, 2) }, paragraphs: [l('官方响应为 title 与 Markdown content，不保证包含 url 字段。HTTP 200 且 content 非空才计一次成功工具费，按本站配置固定收费，无模型 Token 消费。', 'The official response contains title and Markdown content; it does not promise a url field. A successful call fee is counted only for HTTP 200 and nonempty content. The site-configured fee is fixed per call, with no model tokens.'), l('独立 API 返回的正文与模型内工具的结果长度限制不同。网页可达性与反爬限制由上游和目标站点决定；工具可用不保证每个 URL 都能抓取。', 'The direct API and the model-hosted tool have different content-length handling. Provider reachability and target-site restrictions still apply; tool availability does not guarantee every URL can be fetched.')], links: [{ label: l('DeepSeek 官方 Fetch API', 'Official DeepSeek Fetch API'), href: 'https://api-docs.deepseek.com/zh-cn/api/fetch/' }] },
      ],
    },
  ]
}

export function resolveDocsPage(pages: DocsPage[], value: unknown): DocsPage {
  const id = typeof value === 'string' ? value : DOCS_DEFAULT_PAGE
  return pages.find(page => page.id === id) || pages.find(page => page.id === DOCS_DEFAULT_PAGE) || pages[0]
}
