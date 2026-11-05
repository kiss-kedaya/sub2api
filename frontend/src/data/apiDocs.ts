export type DocsCategory = 'start' | 'guides' | 'media' | 'integrations' | 'reference'
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
  links?: { label: string; href?: string; page?: string }[]
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
  const imageGenerateRequest = { model: 'YOUR_IMAGE_MODEL', prompt: 'A red panda astronaut floating above a city at sunset', size: '1024x1024', n: 1 }
  const grokImageEditRequest = { model: 'grok-imagine-edit', prompt: 'Make it snow', image: { url: 'https://example.test/source.png' }, size: '1024x1024' }
  const videoCreateRequest = { model: 'grok-imagine-video', prompt: 'A slow drone shot over a misty lake at dawn', duration: 8, resolution: '720p' }
  const minimaxVideoRequest = { model: 'MiniMax-H3', prompt: 'A drone shot over a misty lake', seconds: '8', size: '1280x720' }
  const seedanceRequest = { model: 'YOUR_SEEDANCE_MODEL', content: [{ type: 'text', text: 'A wave breaking at sunset' }], duration: 5, resolution: '720p' }
  const nanoGenerateRequest = { contents: [{ role: 'user', parts: [{ text: 'A watercolor fox in a forest' }] }], generationConfig: { responseModalities: ['TEXT', 'IMAGE'], imageConfig: { aspectRatio: '1:1', imageSize: '2K' } } }
  const nanoEditRequest = { contents: [{ role: 'user', parts: [{ text: 'Turn this photo into a watercolor painting' }, { inlineData: { mimeType: 'image/png', data: 'BASE64_SOURCE_IMAGE' } }] }], generationConfig: { responseModalities: ['TEXT', 'IMAGE'] } }
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
        { id: 'media-generation', title: l('图像与视频生成', 'Image and video generation'), paragraphs: [l('图像生成、Nano Banana 与视频生成使用与聊天不同的入口，并且是否可用取决于密钥绑定的分组平台。按任务继续阅读：图像生成（OpenAI/Grok）、Nano Banana（Gemini 图像模型）与视频生成（Grok/MiniMax 异步任务）。', 'Image generation, Nano Banana, and video generation use endpoints separate from chat, and availability depends on the platform of the group bound to your key. Continue by task: image generation (OpenAI/Grok), Nano Banana (Gemini image models), and video generation (asynchronous Grok/MiniMax jobs).')], links: [
          { label: l('图像生成', 'Image generation'), page: 'image-generation' },
          { label: l('Nano Banana 图像模型', 'Nano Banana image models'), page: 'nano-banana' },
          { label: l('视频生成', 'Video generation'), page: 'video-generation' },
        ] },
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
