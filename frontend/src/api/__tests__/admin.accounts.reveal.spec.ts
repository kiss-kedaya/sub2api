import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))

import { revealAPIKey } from '@/api/admin/accounts'

describe('admin account API key reveal', () => {
  beforeEach(() => get.mockReset())

  it('requests the credential endpoint with no-store headers and returns only the key', async () => {
    get.mockResolvedValue({ data: { credential_key: 'api_key', value: 'sk-live' } })

    await expect(revealAPIKey(42)).resolves.toEqual({ credential_key: 'api_key', value: 'sk-live' })
    expect(get).toHaveBeenCalledWith('/admin/accounts/42/credential', {
      headers: { 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    })
  })
})
