// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { formatTrafficBytes, getTraffic, getTrafficKeys, getTrafficHealth } from '../traffic'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get } }))
beforeEach(() => vi.clearAllMocks())
describe('traffic API', () => {
  it('uses separate authenticated user and admin routes', async () => {
    get.mockResolvedValue({ data: { requests: 0 } })
    const params = { period: 'year' as const, page: 1, page_size: 20 }
    await getTraffic(false, params)
    expect(get).toHaveBeenLastCalledWith('/usage/traffic', { params, signal: undefined })
    await getTraffic(true, params)
    expect(get).toHaveBeenLastCalledWith('/admin/usage/traffic', { params, signal: undefined })
  })
  it('scopes history key searches by route and only sends user filters for admins', async () => {
    get.mockResolvedValue({ data: [] })
    const controller = new AbortController()
    await getTrafficKeys(false, 999, 'Old', controller.signal)
    expect(get).toHaveBeenLastCalledWith('/usage/traffic/keys', { params: { q: 'Old' }, signal: controller.signal })
    await getTrafficKeys(true, 7, 'Old')
    expect(get).toHaveBeenLastCalledWith('/admin/usage/traffic/keys', { params: { q: 'Old', user_id: 7 }, signal: undefined })
    await getTrafficHealth(controller.signal)
    expect(get).toHaveBeenLastCalledWith('/admin/usage/traffic/health', { signal: controller.signal })
  })
  it('formats binary units without changing raw metering values', () => {
    expect(formatTrafficBytes(0)).toBe('0 B')
    expect(formatTrafficBytes(1023)).toBe('1023 B')
    expect(formatTrafficBytes(1024)).toBe('1.00 KiB')
    expect(formatTrafficBytes(1024 ** 3)).toBe('1.00 GiB')
  })
})
