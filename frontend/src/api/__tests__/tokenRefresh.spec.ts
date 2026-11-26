import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import axios from 'axios'

vi.mock('axios', () => ({
  default: {
    post: vi.fn()
  }
}))

const mockedPost = vi.mocked(axios.post)

function seedSession(overrides: Partial<Record<string, string>> = {}): void {
  localStorage.setItem('auth_token', overrides.auth_token || 'old-access')
  localStorage.setItem('refresh_token', overrides.refresh_token || 'old-refresh')
  localStorage.setItem('token_expires_at', overrides.token_expires_at || String(Date.now() - 1))
  localStorage.setItem('auth_user', JSON.stringify({ id: 7, email: 'admin@example.com' }))
}

function refreshedResponse() {
  return {
    data: {
      code: 0,
      message: 'ok',
      data: {
        access_token: 'new-access',
        refresh_token: 'new-refresh',
        expires_in: 3600,
        token_type: 'Bearer'
      }
    }
  }
}

describe('refreshAuthTokens', () => {
  beforeEach(() => {
    localStorage.clear()
    mockedPost.mockReset()
    vi.resetModules()
    Object.defineProperty(navigator, 'locks', {
      configurable: true,
      value: undefined
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shares one refresh request between concurrent callers in the same document', async () => {
    seedSession()
    let resolveRequest!: (value: ReturnType<typeof refreshedResponse>) => void
    mockedPost.mockImplementationOnce(
      () => new Promise((resolve) => {
        resolveRequest = resolve
      })
    )
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    const first = refreshAuthTokens({ failedAccessToken: 'old-access' })
    const second = refreshAuthTokens({ failedAccessToken: 'old-access' })

    expect(mockedPost).toHaveBeenCalledTimes(1)
    resolveRequest(refreshedResponse())

    await expect(first).resolves.toMatchObject({ access_token: 'new-access' })
    await expect(second).resolves.toMatchObject({ refresh_token: 'new-refresh' })
    expect(localStorage.getItem('refresh_token')).toBe('new-refresh')
  })

  it('adopts tokens refreshed by another tab after acquiring the Web Lock', async () => {
    seedSession()
    const request = vi.fn(async (_name: string, callback: () => Promise<unknown>) => {
      localStorage.setItem('auth_token', 'peer-access')
      localStorage.setItem('token_expires_at', String(Date.now() + 3600_000))
      localStorage.setItem('refresh_token', 'peer-refresh')
      return callback()
    })
    Object.defineProperty(navigator, 'locks', {
      configurable: true,
      value: { request }
    })
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    const result = await refreshAuthTokens({ failedAccessToken: 'old-access' })

    expect(request).toHaveBeenCalledTimes(1)
    expect(mockedPost).not.toHaveBeenCalled()
    expect(result).toMatchObject({
      access_token: 'peer-access',
      refresh_token: 'peer-refresh'
    })
  })

  it('does not mistake boundary timer jitter for a completed peer refresh', async () => {
    vi.useFakeTimers()
    seedSession({ token_expires_at: String(Date.now() + 120_001) })
    mockedPost.mockResolvedValueOnce(refreshedResponse())
    const request = vi.fn(async (_name: string, callback: () => Promise<unknown>) => callback())
    Object.defineProperty(navigator, 'locks', {
      configurable: true,
      value: { request }
    })
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    await expect(refreshAuthTokens()).resolves.toMatchObject({ access_token: 'new-access' })

    expect(request).toHaveBeenCalledTimes(1)
    expect(mockedPost).toHaveBeenCalledTimes(1)
  })

  it('recovers when a peer publishes the rotated token just after this request fails', async () => {
    seedSession()
    mockedPost.mockRejectedValueOnce(new Error('refresh token already used'))
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    window.setTimeout(() => {
      localStorage.setItem('auth_token', 'peer-access')
      localStorage.setItem('token_expires_at', String(Date.now() + 3600_000))
      localStorage.setItem('refresh_token', 'peer-refresh')
    }, 10)

    await expect(
      refreshAuthTokens({ failedAccessToken: 'old-access' })
    ).resolves.toMatchObject({
      access_token: 'peer-access',
      refresh_token: 'peer-refresh'
    })
  })

  it('waits for a slow peer after losing a refresh-token race without Web Locks', async () => {
    vi.useFakeTimers()
    seedSession()
    let resolveWinningRequest!: (value: ReturnType<typeof refreshedResponse>) => void
    mockedPost.mockImplementationOnce(
      () => new Promise((resolve) => {
        resolveWinningRequest = resolve
      })
    )
    const firstTab = await import('@/api/tokenRefresh')
    vi.resetModules()
    const secondTab = await import('@/api/tokenRefresh')

    const winner = firstTab.refreshAuthTokens({ failedAccessToken: 'old-access' })
    mockedPost.mockRejectedValueOnce({ response: { status: 401 } })
    const loser = secondTab.refreshAuthTokens({ failedAccessToken: 'old-access' })

    window.setTimeout(() => resolveWinningRequest(refreshedResponse()), 1_500)
    await vi.advanceTimersByTimeAsync(1_600)

    await expect(winner).resolves.toMatchObject({ access_token: 'new-access' })
    await expect(loser).resolves.toMatchObject({ refresh_token: 'new-refresh' })
    expect(mockedPost).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem('refresh_token')).toBe('new-refresh')
  })

  it('does not adopt a token from a different signed-in user', async () => {
    vi.useFakeTimers()
    seedSession()
    mockedPost.mockRejectedValueOnce(new Error('refresh token already used'))
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    window.setTimeout(() => {
      localStorage.setItem('auth_user', JSON.stringify({ id: 8, email: 'other@example.com' }))
      localStorage.setItem('auth_token', 'other-access')
      localStorage.setItem('token_expires_at', String(Date.now() + 3600_000))
      localStorage.setItem('refresh_token', 'other-refresh')
    }, 10)

    const rejection = expect(
      refreshAuthTokens({ failedAccessToken: 'old-access' })
    ).rejects.toThrow('refresh token already used')
    await vi.advanceTimersByTimeAsync(1_100)
    await rejection
  })

  it('does not restore a session that was logged out while refresh was in flight', async () => {
    vi.useFakeTimers()
    seedSession()
    let resolveRequest!: (value: ReturnType<typeof refreshedResponse>) => void
    mockedPost.mockImplementationOnce(
      () => new Promise((resolve) => {
        resolveRequest = resolve
      })
    )
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
    localStorage.clear()
    resolveRequest(refreshedResponse())

    const rejection = expect(pending).rejects.toThrow('Session changed during token refresh')
    await vi.advanceTimersByTimeAsync(1_100)
    await rejection
    expect(localStorage.getItem('auth_token')).toBeNull()
    expect(localStorage.getItem('refresh_token')).toBeNull()
  })

  it('times the refresh request out after 10 seconds, not 30', async () => {
    seedSession()
    mockedPost.mockResolvedValueOnce(refreshedResponse())
    const { refreshAuthTokens } = await import('@/api/tokenRefresh')

    await refreshAuthTokens({ failedAccessToken: 'old-access' })

    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining('/auth/refresh'),
      { refresh_token: 'old-refresh' },
      expect.objectContaining({ timeout: 10_000 })
    )
  })

  describe('one retry with the same refresh token after a transient failure', () => {
    beforeEach(() => {
      vi.useFakeTimers()
      // 重试等待 = 2s + 随机 0–2s：固定成 2s，测试才确定。
      vi.spyOn(Math, 'random').mockReturnValue(0)
      seedSession()
    })

    afterEach(() => {
      vi.restoreAllMocks()
    })

    it.each([
      ['a network error', { code: 'ERR_NETWORK', message: 'Network Error' }],
      ['a request timeout', { code: 'ECONNABORTED', message: 'timeout of 10000ms exceeded' }],
      ['a 502 from the gateway', { response: { status: 502, data: '<html>bad gateway</html>' } }],
      ['a 503 from the service', { response: { status: 503, data: { code: 503, message: 'busy' } } }]
    ])('retries once after %s, with the very same token', async (_name, failure) => {
      mockedPost.mockRejectedValueOnce(failure).mockResolvedValueOnce(refreshedResponse())
      const { refreshAuthTokens } = await import('@/api/tokenRefresh')

      const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
      await vi.advanceTimersByTimeAsync(2_100)

      await expect(pending).resolves.toMatchObject({ refresh_token: 'new-refresh' })
      expect(mockedPost).toHaveBeenCalledTimes(2)
      expect(mockedPost.mock.calls[0][1]).toEqual({ refresh_token: 'old-refresh' })
      expect(mockedPost.mock.calls[1][1]).toEqual({ refresh_token: 'old-refresh' })
      expect(localStorage.getItem('refresh_token')).toBe('new-refresh')
    })

    it('retries a 200 response that is not the expected JSON (a WAF or gateway page)', async () => {
      mockedPost
        .mockResolvedValueOnce({ data: '<html>please verify you are human</html>' })
        .mockResolvedValueOnce(refreshedResponse())
      const { refreshAuthTokens } = await import('@/api/tokenRefresh')

      const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
      await vi.advanceTimersByTimeAsync(2_100)

      await expect(pending).resolves.toMatchObject({ access_token: 'new-access' })
      expect(mockedPost).toHaveBeenCalledTimes(2)
    })

    it('gives up after the single retry fails too', async () => {
      mockedPost.mockRejectedValue({ code: 'ERR_NETWORK', message: 'Network Error' })
      const { refreshAuthTokens } = await import('@/api/tokenRefresh')

      const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
      const rejection = expect(pending).rejects.toMatchObject({ code: 'ERR_NETWORK' })
      await vi.advanceTimersByTimeAsync(3_300)
      await rejection

      expect(mockedPost).toHaveBeenCalledTimes(2)
      expect(localStorage.getItem('refresh_token')).toBe('old-refresh')
    })

    it.each([
      ['a 401 rejection', { response: { status: 401, data: { code: 401, reason: 'REFRESH_TOKEN_REUSE' } } }],
      ['a 403 from the edge', { response: { status: 403, data: '<html>blocked</html>' } }],
      ['a 429 rate limit', { response: { status: 429, data: { message: 'slow down' } } }],
      ['a 400', { response: { status: 400, data: { message: 'bad request' } } }]
    ])('does not retry %s', async (_name, failure) => {
      mockedPost.mockRejectedValueOnce(failure)
      const { refreshAuthTokens } = await import('@/api/tokenRefresh')

      const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
      const rejection = expect(pending).rejects.toMatchObject({ response: failure.response })
      await vi.advanceTimersByTimeAsync(11_500)
      await rejection

      expect(mockedPost).toHaveBeenCalledTimes(1)
    })

    it('adopts the tokens another tab refreshed while waiting to retry, without a second request', async () => {
      mockedPost.mockRejectedValueOnce({ code: 'ERR_NETWORK', message: 'Network Error' })
      const { refreshAuthTokens } = await import('@/api/tokenRefresh')

      const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
      window.setTimeout(() => {
        localStorage.setItem('auth_token', 'peer-access')
        localStorage.setItem('token_expires_at', String(Date.now() + 3600_000))
        localStorage.setItem('refresh_token', 'peer-refresh')
      }, 500)
      await vi.advanceTimersByTimeAsync(2_100)

      await expect(pending).resolves.toMatchObject({ access_token: 'peer-access', refresh_token: 'peer-refresh' })
      expect(mockedPost).toHaveBeenCalledTimes(1)
    })

    it('does not retry for a session that was logged out while waiting', async () => {
      mockedPost.mockRejectedValueOnce({ code: 'ERR_NETWORK', message: 'Network Error' })
      const { refreshAuthTokens } = await import('@/api/tokenRefresh')

      const pending = refreshAuthTokens({ failedAccessToken: 'old-access' })
      const rejection = expect(pending).rejects.toMatchObject({ code: 'ERR_NETWORK' })
      window.setTimeout(() => localStorage.clear(), 500)
      await vi.advanceTimersByTimeAsync(2_100)
      await rejection

      expect(mockedPost).toHaveBeenCalledTimes(1)
      expect(localStorage.getItem('refresh_token')).toBeNull()
    })
  })
})

describe('isRefreshTokenRejection', () => {
  const rejection = (status: number, data: unknown) => ({ response: { status, data } })

  it.each([
    'REFRESH_TOKEN_INVALID',
    'REFRESH_TOKEN_EXPIRED',
    'REFRESH_TOKEN_REUSE',
    'REFRESH_TOKEN_REUSED',
    'REFRESH_TOKEN_SESSION_EXPIRED',
    'SESSION_BINDING_MISMATCH',
    'TOKEN_REVOKED'
  ])('401 with the business code %s means the token was rejected', async (reason) => {
    const { isRefreshTokenRejection } = await import('@/api/tokenRefresh')
    expect(isRefreshTokenRejection(rejection(401, { code: 401, message: 'x', reason }))).toBe(true)
  })

  it('403 USER_NOT_ACTIVE means the account was disabled', async () => {
    const { isRefreshTokenRejection } = await import('@/api/tokenRefresh')
    expect(isRefreshTokenRejection(rejection(403, { reason: 'USER_NOT_ACTIVE' }))).toBe(true)
  })

  it.each([
    ['a 401 without a business code', rejection(401, { code: 401, message: 'Unauthorized' })],
    ['a 401 with an unrelated code', rejection(401, { code: 401, reason: 'SOMETHING_ELSE' })],
    ['a 401 whose body is an HTML page', rejection(401, '<html>login</html>')],
    ['a Cloudflare 403', rejection(403, '<html>blocked</html>')],
    ['a WAF 468', rejection(468, '<html>challenge</html>')],
    ['a gateway 408', rejection(408, '')],
    ['a 400', rejection(400, { reason: 'REFRESH_TOKEN_INVALID' })],
    ['a 429', rejection(429, { reason: 'REFRESH_TOKEN_INVALID' })],
    ['a 503', rejection(503, { reason: 'REFRESH_TOKEN_INVALID' })],
    ['a network error without a response', { code: 'ERR_NETWORK', message: 'Network Error' }],
    ['a plain Error', new Error('Token refresh failed')],
    ['null', null]
  ])('%s is not a token rejection', async (_name, error) => {
    const { isRefreshTokenRejection } = await import('@/api/tokenRefresh')
    expect(isRefreshTokenRejection(error)).toBe(false)
  })
})
