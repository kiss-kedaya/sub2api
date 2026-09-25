import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getCachedUsage, getConfig, queryUsage, saveConfig } from '../customUsage'
import { createUsageDraft } from '@/utils/customUsage'
const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
describe('custom usage API contract', () => {
  beforeEach(() => { vi.resetAllMocks(); Object.values(client).forEach(fn => fn.mockResolvedValue({ data: {} })) })
  it('reads and saves configuration using the shared API client', async () => {
    const signal = new AbortController().signal
    const config = createUsageDraft('https://example.test')
    await getConfig(8, signal); await saveConfig(8, config, signal)
    expect(client.get).toHaveBeenCalledWith('/admin/accounts/8/custom-usage-config', { signal })
    expect(client.put).toHaveBeenCalledWith('/admin/accounts/8/custom-usage-config', config, { signal })
  })
  it('tests draft without PUT and preserves force / abort signal', async () => {
    const signal = new AbortController().signal
    const body = { force: true, config: createUsageDraft('https://example.test') }
    await queryUsage(8, body, signal)
    expect(client.post).toHaveBeenCalledWith('/admin/accounts/8/custom-usage-query', body, { signal, timeout: 35000 })
    expect(client.put).not.toHaveBeenCalled()
  })
  it('only reads the batch cache and enforces 50 distinct positive IDs', async () => {
    await getCachedUsage([1, 1, 2])
    expect(client.post).toHaveBeenCalledWith('/admin/accounts/custom-usage-batch', { account_ids: [1, 2] }, { signal: undefined })
    await expect(getCachedUsage(Array.from({ length: 51 }, (_, i) => i + 1))).rejects.toThrow()
    await expect(getCachedUsage([-1])).rejects.toThrow()
    expect(await getCachedUsage([])).toEqual({ items: {} })
    expect(client.post).toHaveBeenCalledTimes(1)
  })
})
