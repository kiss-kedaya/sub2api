import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  isChunkLoadError,
  pushOrReload,
  recoverFromChunkLoadError,
} from '../chunkLoadError'

describe('chunkLoadError', () => {
  afterEach(() => {
    window.sessionStorage.clear()
    vi.unstubAllGlobals()
  })

  it('detects dynamic import failures', () => {
    expect(isChunkLoadError(new Error('Failed to fetch dynamically imported module: https://kedaya.ai/assets/DashboardView-CRpF0aUO.js'))).toBe(true)
    expect(isChunkLoadError(Object.assign(new Error('Loading chunk failed'), { name: 'ChunkLoadError' }))).toBe(true)
    expect(isChunkLoadError(new Error('auth.loginFailed'))).toBe(false)
    expect(isChunkLoadError(null)).toBe(false)
  })

  it('hard-navigates once to recover a stale chunk', () => {
    const replace = vi.fn()
    vi.stubGlobal('location', { ...window.location, replace, pathname: '/login', search: '', hash: '' })
    expect(recoverFromChunkLoadError('/dashboard')).toBe(true)
    expect(replace).toHaveBeenCalledWith('/dashboard')
    expect(recoverFromChunkLoadError('/dashboard')).toBe(false)
  })

  it('recovers chunk failures from router.push instead of throwing', async () => {
    const replace = vi.fn()
    vi.stubGlobal('location', { ...window.location, replace, pathname: '/login', search: '', hash: '' })
    const router = {
      push: vi.fn().mockRejectedValue(new Error('Failed to fetch dynamically imported module')),
    }
    await expect(pushOrReload(router, '/dashboard')).resolves.toBeUndefined()
    expect(replace).toHaveBeenCalledWith('/dashboard')
  })
})
