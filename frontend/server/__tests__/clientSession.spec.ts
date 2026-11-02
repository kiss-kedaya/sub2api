import { describe, expect, it, vi } from 'vitest'
import { restoreClientRuntimeSession } from '../../app/runtime/clientSession'

function state(hydrated = false) {
  return {
    app: { publicSettingsLoaded: hydrated, fetchPublicSettings: vi.fn().mockResolvedValue(undefined) },
    auth: { cookieSession: hydrated, user: hydrated ? { id: 1 } : null, refreshUser: vi.fn().mockResolvedValue({ id: 1 }) },
    probe: vi.fn().mockResolvedValue({ data: { authenticated: true, csrf_token: 'csrf-value' } }),
  }
}

describe('client-only route runtime initialization', () => {
  it('restores the real cookie user and public settings before navigation', async () => {
    const ctx = state()
    ctx.auth.refreshUser.mockImplementation(async () => { expect(ctx.auth.cookieSession).toBe(true); return { id: 1 } })
    expect(await restoreClientRuntimeSession(ctx)).toBe('csrf-value')
    expect(ctx.auth.refreshUser).toHaveBeenCalledOnce()
    expect(ctx.app.fetchPublicSettings).toHaveBeenCalledOnce()
  })

  it('does not refetch settings or the user already hydrated by SSR', async () => {
    const ctx = state(true)
    await restoreClientRuntimeSession(ctx)
    expect(ctx.auth.refreshUser).not.toHaveBeenCalled()
    expect(ctx.app.fetchPublicSettings).not.toHaveBeenCalled()
  })

  it('does not treat an anonymous probe as a login', async () => {
    const ctx = state()
    ctx.probe.mockResolvedValue({ data: { authenticated: false, csrf_token: '' } })
    await restoreClientRuntimeSession(ctx)
    expect(ctx.auth.cookieSession).toBe(false)
    expect(ctx.auth.refreshUser).not.toHaveBeenCalled()
  })

  it('keeps hydrated identity on transient probe failure', async () => {
    const ctx = state(true)
    ctx.probe.mockRejectedValue(new Error('temporary network failure'))
    expect(await restoreClientRuntimeSession(ctx)).toBeNull()
    expect(ctx.auth.user).toEqual({ id: 1 })
    expect(ctx.auth.cookieSession).toBe(true)
  })
})
