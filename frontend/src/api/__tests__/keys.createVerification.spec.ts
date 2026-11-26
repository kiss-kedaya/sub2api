import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))

import { keysAPI } from '@/api/keys'

describe('API key create-verification switch endpoint', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('PUTs just the mode when no verification is needed (turning it on is never looser)', async () => {
    const factors = { create_verification_required: true, create_verification_mode: 'on' }
    put.mockResolvedValue({ data: factors })

    const result = await keysAPI.setCreateVerificationMode('on')

    expect(put).toHaveBeenCalledTimes(1)
    expect(put).toHaveBeenCalledWith('/user/security/api-key-create-verification', { mode: 'on' })
    // 请求体里根本没有 verification 这个键（不是 undefined 值）。
    expect(Object.keys(put.mock.calls[0][1])).toEqual(['mode'])
    expect(result).toBe(factors)
  })

  it('sends the verification payload as given when turning the protection off', async () => {
    put.mockResolvedValue({ data: {} })
    await keysAPI.setCreateVerificationMode('off', { method: 'password', password: 'login-password' })
    expect(put).toHaveBeenCalledWith('/user/security/api-key-create-verification', {
      mode: 'off',
      verification: { method: 'password', password: 'login-password' },
    })

    put.mockClear()
    await keysAPI.setCreateVerificationMode('off', { method: 'totp', totp_code: '123456' })
    expect(put).toHaveBeenCalledWith('/user/security/api-key-create-verification', {
      mode: 'off',
      verification: { method: 'totp', totp_code: '123456' },
    })

    put.mockClear()
    await keysAPI.setCreateVerificationMode('auto', { method: 'passkey', passkey_session_token: 'ticket' })
    expect(put).toHaveBeenCalledWith('/user/security/api-key-create-verification', {
      mode: 'auto',
      verification: { method: 'passkey', passkey_session_token: 'ticket' },
    })
  })

  it('does not swallow server rejections (the caller maps reason to a message)', async () => {
    const rejection = { status: 400, reason: 'VERIFY_REQUIRED', message: 'verification required' }
    put.mockRejectedValue(rejection)
    await expect(keysAPI.setCreateVerificationMode('off')).rejects.toBe(rejection)
  })

  it('keeps reading the effective value from the same GET', async () => {
    get.mockResolvedValue({ data: { create_verification_required: false } })
    await keysAPI.getVerificationFactors()
    expect(get).toHaveBeenCalledWith('/user/security/verification-factors')
  })
})
