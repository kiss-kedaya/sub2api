import { beforeEach, describe, expect, it, vi } from 'vitest'
import { logout } from '@/api/auth'
import { apiClient } from '@/api/client'

vi.mock('@/api/client', () => ({ apiClient: { post: vi.fn() } }))

describe('logout server and browser state', () => {
  beforeEach(() => { vi.resetAllMocks(); localStorage.clear() })

  it('clears the pending OAuth session when no refresh token exists', async () => {
    localStorage.setItem('auth_token', 'test-access')
    vi.mocked(apiClient.post).mockResolvedValue({ data: {} })
    await logout()
    expect(apiClient.post).toHaveBeenCalledWith('/auth/logout', {})
    expect(localStorage.getItem('auth_token')).toBeNull()
  })

  it('also submits an existing refresh token for revocation', async () => {
    localStorage.setItem('refresh_token', 'test-refresh')
    vi.mocked(apiClient.post).mockResolvedValue({ data: {} })
    await logout()
    expect(apiClient.post).toHaveBeenCalledWith('/auth/logout', { refresh_token: 'test-refresh' })
    expect(localStorage.getItem('refresh_token')).toBeNull()
  })

  it('still removes local credentials after a failed logout request', async () => {
    localStorage.setItem('auth_token', 'test-access')
    localStorage.setItem('refresh_token', 'test-refresh')
    vi.mocked(apiClient.post).mockRejectedValue(new Error('network unavailable'))
    await expect(logout()).resolves.toBeUndefined()
    expect(localStorage.getItem('auth_token')).toBeNull()
    expect(localStorage.getItem('refresh_token')).toBeNull()
  })
})
