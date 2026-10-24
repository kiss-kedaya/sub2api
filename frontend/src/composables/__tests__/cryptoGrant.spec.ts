import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCryptoGrant, cryptoGrantExpiresAt, getCryptoGrant, isCryptoGrantRejected, retainCryptoGrant } from '../cryptoGrant'

const credential = 'opaque-server-payment-credential-at-least-32-bytes'
const session = 'current-admin-jwt-session'
const grant = (seconds = 300) => ({ credential, expires_at: new Date(Date.now() + seconds * 1000).toISOString(), expires_in: seconds })

beforeEach(() => { clearCryptoGrant(); localStorage.setItem('auth_token', session) })
afterEach(() => { clearCryptoGrant(); vi.useRealTimers(); vi.restoreAllMocks() })

describe('in-memory payment credential lifecycle', () => {
  it('keeps authority out of browser storage and never extends the original five minutes', () => {
    vi.useFakeTimers()
    const localWrite = vi.spyOn(localStorage, 'setItem')
    const sessionWrite = vi.spyOn(sessionStorage, 'setItem')
    const start = Date.now()
    retainCryptoGrant(grant(), session, start)
    const originalExpiry = cryptoGrantExpiresAt.value
    vi.advanceTimersByTime(299000)
    expect(getCryptoGrant()).toBe(credential)
    expect(cryptoGrantExpiresAt.value).toBe(originalExpiry)
    vi.advanceTimersByTime(1000)
    expect(getCryptoGrant()).toBeUndefined()
    expect(cryptoGrantExpiresAt.value).toBe(0)
    expect(localWrite).not.toHaveBeenCalled()
    expect(sessionWrite).not.toHaveBeenCalled()
  })

  it('drops credentials after logout or a JWT/account change', () => {
    retainCryptoGrant(grant(), session, Date.now())
    localStorage.setItem('auth_token', 'other-admin-session')
    expect(getCryptoGrant()).toBeUndefined()
    localStorage.setItem('auth_token', session)
    expect(getCryptoGrant()).toBeUndefined()
    retainCryptoGrant(grant(), session, Date.now())
    localStorage.removeItem('auth_token')
    expect(getCryptoGrant()).toBeUndefined()
  })

  it('rejects late verification from a previous session and malformed or expired server grants', () => {
    expect(() => retainCryptoGrant(grant(), 'old-session', Date.now())).toThrow()
    expect(() => retainCryptoGrant({ ...grant(), expires_at: 'invalid' }, session, Date.now())).toThrow()
    expect(() => retainCryptoGrant({ ...grant(), credential: 'short' }, session, Date.now())).toThrow()
    expect(() => retainCryptoGrant(grant(-1), session, Date.now())).toThrow()
    expect(getCryptoGrant()).toBeUndefined()
  })

  it('allows re-verification only for an exact server pre-operation grant refusal', () => {
    expect(isCryptoGrantRejected({ status: 403, reason: 'CRYPTO_GRANT_REQUIRED' })).toBe(true)
    expect(isCryptoGrantRejected({ status: 503, reason: 'CRYPTO_GRANT_REQUIRED' })).toBe(false)
    expect(isCryptoGrantRejected({ status: 503, reason: 'CRYPTO_TOTP_REQUIRED' })).toBe(false)
    expect(isCryptoGrantRejected({ status: 403, reason: 'CRYPTO_OPERATOR_REQUIRED' })).toBe(false)
    expect(isCryptoGrantRejected(new Error('Network error'))).toBe(false)
  })
})
