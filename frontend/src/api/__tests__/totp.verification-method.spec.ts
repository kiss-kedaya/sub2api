import { beforeEach, describe, expect, it, vi } from 'vitest'
import { disable, getVerificationMethod } from '../totp'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get, post } }))

beforeEach(() => { get.mockReset(); post.mockReset() })

describe('TOTP verification operation', () => {
  it.each(['enable', 'disable'] as const)('sends %s as a query parameter', async operation => {
    const response = operation === 'disable'
      ? { method: 'totp' }
      : { method: 'password' }
    get.mockResolvedValueOnce({ data: response })
    expect(await getVerificationMethod(operation)).toEqual(response)
    expect(get).toHaveBeenCalledWith('/user/totp/verification-method', { params: { operation } })
  })

  it('keeps the default method lookup compatible without an operation', async () => {
    get.mockResolvedValueOnce({ data: { method: 'password' } })
    expect(await getVerificationMethod()).toEqual({ method: 'password' })
    expect(get).toHaveBeenCalledWith('/user/totp/verification-method', undefined)
  })

  it('sends the authenticator code as totp_code when disabling', async () => {
    post.mockResolvedValueOnce({ data: { success: true } })
    expect(await disable({ totp_code: '012345' })).toEqual({ success: true })
    expect(post).toHaveBeenCalledWith('/user/totp/disable', { totp_code: '012345' })
  })
})
