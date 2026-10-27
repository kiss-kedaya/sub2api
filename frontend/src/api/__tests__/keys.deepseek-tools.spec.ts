import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post, put } = vi.hoisted(() => ({ post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post, put } }))

import { keysAPI } from '@/api/keys'

describe('API key DeepSeek tool injection payloads', () => {
  beforeEach(() => {
    post.mockReset().mockResolvedValue({ data: { id: 1 } })
    put.mockReset().mockResolvedValue({ data: { id: 1 } })
  })

  it('defaults new keys to enabled when no option is supplied', async () => {
    await keysAPI.create('Default key')
    expect(post).toHaveBeenCalledWith('/keys', {
      name: 'Default key', deepseek_tool_injection_enabled: true,
    }, undefined)
  })

  it.each([true, false])('serializes the explicit %s setting while retaining idempotency headers', async (enabled) => {
    await keysAPI.create('Configured key', 3, undefined, [], [], 0, undefined,
      undefined, [3, 4], 'inherit', {
        idempotencyKey: 'test-key-create-once', deepseekToolInjectionEnabled: enabled,
      })
    expect(post).toHaveBeenCalledWith('/keys', {
      name: 'Configured key', group_id: 3, group_ids: [3, 4],
      deepseek_tool_injection_enabled: enabled,
    }, { headers: { 'Idempotency-Key': 'test-key-create-once' } })
  })

  it('sends an independent concurrency limit only when explicitly configured', async () => {
    await keysAPI.create('Limited', 3, undefined, [], [], 0, undefined,
      undefined, undefined, 'inherit', { concurrencyLimit: 4 })
    expect(post).toHaveBeenCalledWith('/keys', {
      name: 'Limited', group_id: 3, concurrency_limit: 4, deepseek_tool_injection_enabled: true,
    }, undefined)
  })

  it('keeps false in updates without changing unrelated settings', async () => {
    await keysAPI.update(1, { deepseek_tool_injection_enabled: false })
    expect(put).toHaveBeenCalledWith('/keys/1', { deepseek_tool_injection_enabled: false })
  })

  it('does not implicitly change the injection setting in an unrelated update', async () => {
    await keysAPI.update(1, { name: 'Renamed key' })
    expect(put).toHaveBeenCalledWith('/keys/1', { name: 'Renamed key' })
  })
})
