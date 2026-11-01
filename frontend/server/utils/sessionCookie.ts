/**
 * Cookie helpers for the SSR session.
 *
 * The heavy lifting lives in `session.ts` (pure crypto) and `backendProxy.ts`
 * (pure request orchestration); this file only moves a value between the
 * sealed cookie and an H3 event.
 */

import type { H3Event } from 'h3'
import {
  CSRF_COOKIE,
  SESSION_COOKIE,
  SESSION_MAX_AGE_SECONDS,
  sealBffSession,
  unsealBffSession,
  type ServerSession,
} from './session'
import { isTrustedPeer } from './proxyPolicy'

export type SecureMode = 'auto' | 'true' | 'false'

const SECURE_MODES = new Set<SecureMode>(['auto', 'true', 'false'])

export function normalizeSecureMode(value: unknown): SecureMode {
  const mode = String(value ?? 'auto').toLowerCase()
  return (SECURE_MODES.has(mode as SecureMode) ? mode : 'auto') as SecureMode
}

/**
 * Secure flag for the session cookie.
 *
 * `x-forwarded-proto` is only trusted when the immediate peer is our own nginx
 * (loopback / private range). A client that connects directly cannot downgrade
 * the flag by sending the header itself.
 */
function isSecure(event: H3Event, mode: SecureMode): boolean {
  if (mode === 'true') return true
  if (mode === 'false') return false
  const socket = event.node.req.socket as { encrypted?: boolean; remoteAddress?: string } | undefined
  const peer = socket?.remoteAddress
  const forwarded = String(event.node.req.headers['x-forwarded-proto'] || '').split(',')[0].trim().toLowerCase()
  if (forwarded && isTrustedPeer(peer)) return forwarded === 'https'
  return Boolean(socket?.encrypted)
}

/** Reads and unseals the session cookie. Returns null for anything invalid. */
export function readServerSession(event: H3Event, password: string): ServerSession | null {
  if (!password) return null
  return unsealBffSession(getCookie(event, SESSION_COOKIE), password)
}

export interface BffCookieOptions {
  httpOnly: boolean
  secure: boolean
  sameSite: 'lax'
  path: string
  maxAge: number
}

/** Attributes for the session cookie; exported so tests can pin them. */
export function sessionCookieOptions(secure: boolean): BffCookieOptions {
  return { httpOnly: true, secure, sameSite: 'lax', path: '/', maxAge: SESSION_MAX_AGE_SECONDS }
}

/**
 * The CSRF value is public by design (double submit); the app reads it to echo
 * it back in `x-sub2api-csrf`. It is a different cookie from the session.
 */
export function csrfCookieOptions(secure: boolean): BffCookieOptions {
  return { httpOnly: false, secure, sameSite: 'lax', path: '/', maxAge: SESSION_MAX_AGE_SECONDS }
}

export function setServerSession(
  event: H3Event,
  session: ServerSession,
  password: string,
  secureMode: SecureMode = 'auto'
): void {
  const sealed = sealBffSession(session, password)
  const secure = isSecure(event, secureMode)
  setCookie(event, SESSION_COOKIE, sealed, sessionCookieOptions(secure))
  setCookie(event, CSRF_COOKIE, session.csrf, csrfCookieOptions(secure))
}

export function clearServerSession(event: H3Event): void {
  deleteCookie(event, SESSION_COOKIE, { path: '/' })
  deleteCookie(event, CSRF_COOKIE, { path: '/' })
}
