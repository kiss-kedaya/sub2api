import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  buildIdentityModelMapping,
  useSerialModelWhitelistSync,
} from '../useSerialModelWhitelistSync'
import { syncUpstreamModels, update } from '@/api/admin/accounts'

vi.mock('@/api/admin/accounts', () => ({
  syncUpstreamModels: vi.fn(),
  update: vi.fn(),
}))

const syncMock = vi.mocked(syncUpstreamModels)
const updateMock = vi.mocked(update)

describe('buildIdentityModelMapping', () => {
  it('maps every model onto itself and drops blanks', () => {
    expect(buildIdentityModelMapping([' gpt-5 ', '', 'claude-opus'])).toEqual({
      'gpt-5': 'gpt-5',
      'claude-opus': 'claude-opus',
    })
  })
})

describe('useSerialModelWhitelistSync', () => {
  beforeEach(() => {
    syncMock.mockReset()
    updateMock.mockReset()
  })

  it('walks accounts one at a time and never overlaps requests', async () => {
    const order: string[] = []
    syncMock.mockImplementation(async (id: number) => {
      order.push(`start:${id}`)
      await new Promise(resolve => setTimeout(resolve, 0))
      order.push(`end:${id}`)
      return { models: [`m-${id}`] }
    })
    updateMock.mockImplementation(async (id: number) => {
      order.push(`write:${id}`)
      return {} as never
    })

    const sync = useSerialModelWhitelistSync()
    const state = await sync.start([7, 8])

    expect(order).toEqual(['start:7', 'end:7', 'write:7', 'start:8', 'end:8', 'write:8'])
    expect(state).toMatchObject({ total: 2, completed: 2, succeeded: 2, failed: 0, running: false })
  })

  it('preserves the existing whitelist when upstream returns an empty catalog', async () => {
    syncMock.mockResolvedValue({ models: [] })

    const sync = useSerialModelWhitelistSync()
    await sync.start([1, 2])

    expect(updateMock).not.toHaveBeenCalled()
    expect(sync.state.value.failedIds).toEqual([1, 2])
    expect(sync.failures.value.map(f => f.reason)).toEqual([
      'empty upstream model list',
      'empty upstream model list',
    ])
  })

  it('keeps going after a single account fails', async () => {
    syncMock.mockImplementation(async (id: number) => {
      if (id === 1) throw new Error('upstream 502')
      return { models: ['ok'] }
    })
    updateMock.mockResolvedValue({} as never)

    const sync = useSerialModelWhitelistSync()
    const state = await sync.start([1, 2])

    expect(state).toMatchObject({ succeeded: 1, failed: 1, completed: 2 })
    expect(state.failedIds).toEqual([1])
    expect(updateMock).toHaveBeenCalledTimes(1)
  })

  it('refuses to start twice concurrently', async () => {
    let release: (() => void) | null = null
    syncMock.mockImplementation(() => new Promise((resolve) => {
      release = () => resolve({ models: ['m'] })
    }))
    updateMock.mockResolvedValue({} as never)

    const sync = useSerialModelWhitelistSync()
    const first = sync.start([1])
    const second = sync.start([2])
    expect(await second).toBe(sync.state.value)

    release?.()
    await first
    expect(syncMock).toHaveBeenCalledTimes(1)
  })

  it('stops the queue when cancelled', async () => {
    syncMock.mockResolvedValue({ models: ['m'] })
    updateMock.mockResolvedValue({} as never)

    const sync = useSerialModelWhitelistSync()
    const running = sync.start([1, 2, 3])
    sync.cancel()
    await running

    expect(syncMock.mock.calls.length).toBeLessThan(3)
  })
})