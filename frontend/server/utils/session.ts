/**
 * SSR session cookie.
 *
 * The Go API issues bearer tokens. They are stored here, sealed into an
 * HTTPOnly cookie, and never returned to the browser payload. Every piece of
 * this module is dependency free so it can be unit tested without Nitro.
 */

import { createCipheriv, createDecipheriv, randomBytes, scryptSync, timingSafeEqual } from 'node:crypto'

export const SESSION_COOKIE = 'sub2_session'
export const CSRF_HEADER = 'x-csrf-token'
export const CSRF_COOKIE = 'sub2_csrf'

const SEAL_VERSION = 'v1'
const AAD = Buffer.from('sub2api:ssr-session:v1', 'utf8')
const KEY_SALT = 'sub2api.ssr.session'
/** Refresh tokens outlive access tokens; the cookie is dropped on refresh failure. */
export const SESSION_MAX_AGE_SECONDS = 30 * 24 * 60 * 60

export interface ServerSession {
  v: 1
  /** Go access token. Never sent to the browser. */
  accessToken: string
  /** Go refresh token. Never sent to the browser. */
  refreshToken: string
  /** Access token expiry, epoch milliseconds. */
  accessExp: number
  /** User id, used only for diagnostics and session-change detection. */
  uid: number
  /** Double-submit CSRF token; exposed through the session endpoint only. */
  csrf: string
}

const keyCache = new Map<string, Buffer>()

function deriveKey(password: string): Buffer {
  if (!password) throw new Error('session password is not configured')
  const cached = keyCache.get(password)
  if (cached) return cached
  const key = scryptSync(password, KEY_SALT, 32, { N: 16384, r: 8, p: 1 })
  if (keyCache.size > 8) keyCache.clear()
  keyCache.set(password, key)
  return key
}

export function randomToken(bytes = 32): string {
  return randomBytes(bytes).toString('base64url')
}

export function createSession(input: {
  accessToken: string
  refreshToken: string
  expiresIn: number
  uid: number
  now?: number
}): ServerSession {
  const now = input.now ?? Date.now()
  const expiresIn = Number.isFinite(input.expiresIn) && input.expiresIn > 0 ? input.expiresIn : 0
  return {
    v: 1,
    accessToken: input.accessToken,
    refreshToken: input.refreshToken,
    // 0 means "unknown"; an unknown expiry must not look expired on every request.
    accessExp: expiresIn > 0 ? now + expiresIn * 1000 : 0,
    uid: input.uid,
    csrf: randomToken(24),
  }
}

export function sealBffSession(session: ServerSession, password: string): string {
  const key = deriveKey(password)
  const iv = randomBytes(12)
  const cipher = createCipheriv('aes-256-gcm', key, iv)
  cipher.setAAD(AAD)
  const body = Buffer.concat([
    cipher.update(Buffer.from(JSON.stringify(session), 'utf8')),
    cipher.final(),
  ])
  const tag = cipher.getAuthTag()
  return [SEAL_VERSION, iv.toString('base64url'), body.toString('base64url'), tag.toString('base64url')].join('.')
}

/** Returns null for anything that is not a sealed session this service wrote. */
export function unsealBffSession(sealed: string | undefined | null, password: string): ServerSession | null {
  if (!sealed || typeof sealed !== 'string') return null
  const parts = sealed.split('.')
  if (parts.length !== 4 || parts[0] !== SEAL_VERSION) return null
  try {
    const key = deriveKey(password)
    const iv = Buffer.from(parts[1], 'base64url')
    const body = Buffer.from(parts[2], 'base64url')
    const tag = Buffer.from(parts[3], 'base64url')
    if (iv.length !== 12 || tag.length !== 16) return null
    const decipher = createDecipheriv('aes-256-gcm', key, iv)
    decipher.setAAD(AAD)
    decipher.setAuthTag(tag)
    const plain = Buffer.concat([decipher.update(body), decipher.final()]).toString('utf8')
    const parsed = JSON.parse(plain) as unknown
    if (!isServerSession(parsed)) return null
    return parsed
  } catch {
    return null
  }
}

function isServerSession(value: unknown): value is ServerSession {
  if (!value || typeof value !== 'object') return false
  const v = value as Record<string, unknown>
  return (
    v.v === 1 &&
    typeof v.accessToken === 'string' &&
    v.accessToken.length > 0 &&
    typeof v.refreshToken === 'string' &&
    typeof v.accessExp === 'number' &&
    typeof v.uid === 'number' &&
    typeof v.csrf === 'string' &&
    v.csrf.length >= 16
  )
}

/** Constant-time CSRF comparison; a missing or short header never matches. */
export function csrfMatches(session: ServerSession, provided: string | undefined | null): boolean {
  if (!provided || typeof provided !== 'string') return false
  if (provided.length < 16 || provided.length !== session.csrf.length) return false
  const a = Buffer.from(provided, 'utf8')
  const b = Buffer.from(session.csrf, 'utf8')
  if (a.length !== b.length) return false
  return timingSafeEqual(a, b)
}

export function isSessionExpired(session: ServerSession, now = Date.now()): boolean {
  return session.accessExp > 0 && now >= session.accessExp
}

export interface SessionProbe {
  authenticated: boolean
  session_managed: boolean
  configured: boolean
  csrf_token: string | null
  expires_at: number | null
}

/**
 * Shape of GET /api/v1/auth/session.
 *
 * Without a session password the BFF cannot manage sessions at all, so the
 * probe answers as an anonymous, unconfigured instance instead of 503. Public
 * pages still render; nothing claims to be logged in.
 */
export function buildSessionProbe(session: ServerSession | null, configured: boolean): SessionProbe {
  if (!configured) {
    return { authenticated: false, session_managed: false, configured: false, csrf_token: null, expires_at: null }
  }
  if (!session) {
    return { authenticated: false, session_managed: true, configured: true, csrf_token: null, expires_at: null }
  }
  return {
    authenticated: true,
    session_managed: true,
    configured: true,
    csrf_token: session.csrf,
    expires_at: session.accessExp || null,
  }
}
