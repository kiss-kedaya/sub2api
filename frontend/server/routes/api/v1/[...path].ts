import { handleBffRequest } from '../../../utils/backendProxy'
import {
  isAllowedMethod,
  isBffOnlyRoute,
  resolveUpstreamPath,
  resolveClientIp,
  isTrustedPeer,
} from '../../../utils/proxyPolicy'
import {
  clearServerSession,
  normalizeSecureMode,
  readServerSession,
  setServerSession,
} from '../../../utils/sessionCookie'

const envelope = (code: number, message: string) => ({ code, message, reason: '', metadata: null })

/**
 * BFF for the management API only.
 *
 * Gateway traffic (`/v1/messages`, `/v1/responses`, `/v1/chat/completions`,
 * WebSocket upgrades) never comes through here: those stay on the direct Go
 * origin so streaming and socket semantics are untouched.
 */
export default defineEventHandler(async (event) => {
  const runtime = useRuntimeConfig(event)
  const sessionPassword = String(runtime.sessionPassword || '')
  const backendOrigin = String(runtime.backendOrigin || '')
  if (!backendOrigin) {
    setResponseStatus(event, 503)
    setResponseHeader(event, 'cache-control', 'no-store')
    return envelope(503, 'Backend origin is not configured')
  }

  const url = getRequestURL(event)
  const decision = resolveUpstreamPath(url.pathname)
  if (!decision.ok || !decision.path) {
    setResponseStatus(event, 400)
    setResponseHeader(event, 'cache-control', 'no-store')
    return envelope(400, 'Invalid API path')
  }
  if (isBffOnlyRoute(decision.path)) {
    setResponseStatus(event, 404)
    setResponseHeader(event, 'cache-control', 'no-store')
    return envelope(404, 'Not found')
  }

  const method = event.method.toUpperCase()
  if (!isAllowedMethod(method)) {
    setResponseStatus(event, 405)
    setResponseHeader(event, 'cache-control', 'no-store')
    setResponseHeader(event, 'allow', 'GET, HEAD, POST, PUT, PATCH, DELETE')
    return envelope(405, 'Method not allowed')
  }

  const headers = getHeaders(event) as Record<string, string | string[] | undefined>
  const forwardedProto = String(headers['x-forwarded-proto'] || '').split(',')[0].trim()
  const peer = event.node.req.socket?.remoteAddress || ''
  const protocol = isTrustedPeer(peer) && forwardedProto ? forwardedProto : url.protocol.replace(':', '')
  const host = String(headers['host'] || url.host)

  const body =
    method === 'GET' || method === 'HEAD'
      ? null
      : ((await readRawBody(event, false)) as Buffer | undefined) ?? null

  const result = await handleBffRequest({
    method,
    path: decision.path,
    query: url.search.replace(/^\?/, ''),
    headers,
    body,
    session: readServerSession(event, sessionPassword),
    sessionPassword,
    backendOrigin,
    publicOrigin: `${protocol}://${host}`,
    protocol,
    clientIp: resolveClientIp(headers['x-forwarded-for'], event.node.req.socket?.remoteAddress),
    // Optional override; the proxy falls back to its own default when absent.
    timeoutMs: Number(runtime.backendTimeoutMs) || undefined,
  })

  if (result.session.action === 'set') {
    setServerSession(
      event,
      result.session.value,
      sessionPassword,
      normalizeSecureMode(runtime.sessionCookieSecure)
    )
  } else if (result.session.action === 'clear') {
    clearServerSession(event)
  }

  for (const [name, value] of Object.entries(result.headers)) {
    setResponseHeader(event, name, value)
  }
  if (String(result.headers['content-type'] || '').includes('text/event-stream')) {
    setResponseHeader(event, 'x-accel-buffering', 'no')
  }
  setResponseStatus(event, result.status)

  // Bodies must never be handed back as a bare web stream: Nitro would keep the
  // response open while the caller waits for it. sendStream pipes and ends it.
  const responseBody = result.body
  if (result.status === 204 || result.status === 304) {
    return null
  }
  if (responseBody && typeof (responseBody as ReadableStream<Uint8Array>).getReader === 'function') {
    await sendStream(event, responseBody as ReadableStream<Uint8Array>)
    return
  }
  if (responseBody) {
    // A known-size buffer can be closed immediately; no chunked framing needed.
    setResponseHeader(event, 'content-length', Buffer.byteLength(responseBody as Buffer))
  }
  return responseBody
})
