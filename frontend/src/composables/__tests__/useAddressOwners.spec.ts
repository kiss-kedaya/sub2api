import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref, type EffectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { OWNER_BATCH_SIZE, clearAddressOwnerCache, useAddressOwners } from '../useAddressOwners'

const mocks = vi.hoisted(() => ({ getAddressOwners: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getAddressOwners: mocks.getAddressOwners }))

// Fixture addresses only: a counter padded to 40 hex digits.
const address = (n: number) => '0x' + n.toString(16).padStart(40, '0')
const owner = (id: number, over: Record<string, unknown> = {}) => ({ user_id: id, username: 'fixture-' + id, email: `fixture-${id}@example.test`, deleted: false, ...over })

let scope: EffectScope
beforeEach(() => {
  clearAddressOwnerCache()
  mocks.getAddressOwners.mockReset()
  mocks.getAddressOwners.mockImplementation(async (batch: string[]) =>
    Object.fromEntries(batch.map((key, index) => [key, owner(index + 1)])))
  scope = effectScope()
})
afterEach(() => scope.stop())

function use(addresses: ReturnType<typeof ref<string[]>>) {
  return scope.run(() => useAddressOwners(() => addresses.value ?? []))!
}

describe('useAddressOwners', () => {
  it('asks in batches of 40 and resolves every address', async () => {
    const list = Array.from({ length: 95 }, (_, i) => address(i + 1))
    const { ownerOf } = use(ref(list))
    await flushPromises()
    const sizes = mocks.getAddressOwners.mock.calls.map(([batch]) => (batch as string[]).length)
    expect(OWNER_BATCH_SIZE).toBe(40)
    expect(sizes).toEqual([40, 40, 15])
    expect(ownerOf(list[0])?.username).toBe('fixture-1')
    expect(ownerOf(list[94])).toBeTruthy()
  })

  it('never asks for the same address twice, across calls and across instances', async () => {
    const shared = [address(1), address(2)]
    const first = use(ref(shared))
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)

    // A second table showing the same addresses plus one new one only asks for the new one.
    const second = use(ref([...shared, address(3)]))
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(2)
    expect(mocks.getAddressOwners.mock.calls[1][0]).toEqual([address(3)])
    expect(first.ownerOf(address(3))).toBeTruthy() // the cache is shared
    expect(second.ownerOf(address(1))).toBeTruthy()
  })

  it('shares an in-flight request between instances instead of repeating it', async () => {
    let release!: (value: Record<string, unknown>) => void
    mocks.getAddressOwners.mockImplementation(() => new Promise(resolve => { release = resolve }))
    const a = use(ref([address(1)]))
    const b = use(ref([address(1)]))
    await nextTick()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)
    release({ [address(1)]: owner(9) })
    await flushPromises()
    expect(a.ownerOf(address(1))?.user_id).toBe(9)
    expect(b.ownerOf(address(1))?.user_id).toBe(9)
  })

  it('treats a request for the same address in different letter case as one address', async () => {
    const mixed = '0x' + 'AbCdEf'.repeat(6) + 'aabb'
    const { ownerOf } = use(ref([mixed, mixed.toLowerCase(), mixed.toUpperCase().replace('0X', '0x')]))
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)
    expect(mocks.getAddressOwners.mock.calls[0][0]).toEqual([mixed.toLowerCase()])
    expect(ownerOf(mixed)?.user_id).toBe(1)
    expect(ownerOf(mixed.toLowerCase())?.user_id).toBe(1)
  })

  it('only sends well-formed EVM addresses', async () => {
    const { ownerOf } = use(ref(['', 'nope', '0x12', '9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin', address(1)]))
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)
    expect(mocks.getAddressOwners.mock.calls[0][0]).toEqual([address(1)])
    expect(ownerOf('nope')).toBeUndefined()
    expect(ownerOf('')).toBeUndefined()
    expect(ownerOf(undefined)).toBeUndefined()
    expect(ownerOf(null)).toBeUndefined()
  })

  it('does not ask at all for an empty list', async () => {
    use(ref([]))
    await flushPromises()
    expect(mocks.getAddressOwners).not.toHaveBeenCalled()
  })

  it('distinguishes "no owner" (null) from "not looked up yet" (undefined)', async () => {
    mocks.getAddressOwners.mockResolvedValue({ [address(1)]: owner(5) })
    const { ownerOf } = use(ref([address(1), address(2)]))
    expect(ownerOf(address(1))).toBeUndefined()
    await flushPromises()
    expect(ownerOf(address(1))?.user_id).toBe(5)
    expect(ownerOf(address(2))).toBeNull()
  })

  it('keeps a soft-deleted owner and its flag', async () => {
    mocks.getAddressOwners.mockResolvedValue({ [address(1)]: owner(5, { deleted: true, username: '' }) })
    const { ownerOf } = use(ref([address(1)]))
    await flushPromises()
    expect(ownerOf(address(1))).toEqual({ user_id: 5, username: '', email: 'fixture-5@example.test', deleted: true })
  })

  it('does not trust a malformed owner entry', async () => {
    mocks.getAddressOwners.mockResolvedValue({
      [address(1)]: { user_id: 'abc', username: 'x' },
      [address(2)]: { user_id: 0 },
      [address(3)]: 'oops',
      [address(4)]: { user_id: 8, username: 5, email: null, deleted: 'yes' }
    })
    const { ownerOf } = use(ref([address(1), address(2), address(3), address(4)]))
    await flushPromises()
    expect(ownerOf(address(1))).toBeNull()
    expect(ownerOf(address(2))).toBeNull()
    expect(ownerOf(address(3))).toBeNull()
    expect(ownerOf(address(4))).toEqual({ user_id: 8, username: '', email: '', deleted: false })
  })

  it('goes quiet on a 404 from an older backend and stops asking', async () => {
    mocks.getAddressOwners.mockRejectedValue({ status: 404, message: 'page not found' })
    const list = ref([address(1)])
    const { ownerOf } = use(list)
    await flushPromises()
    expect(ownerOf(address(1))).toBeUndefined()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)

    list.value = [address(1), address(2)]
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1) // the endpoint is remembered as missing
    expect(ownerOf(address(2))).toBeUndefined()
  })

  it('swallows other failures, leaves the owner unknown, and retries when the list changes', async () => {
    mocks.getAddressOwners.mockRejectedValueOnce({ status: 500, message: 'boom fixture-user@example.test' })
    const list = ref([address(1)])
    const { ownerOf } = use(list)
    await flushPromises()
    expect(ownerOf(address(1))).toBeUndefined()

    list.value = [address(1), address(2)]
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(2)
    expect(ownerOf(address(1))).toBeTruthy()
    expect(ownerOf(address(2))).toBeTruthy()
  })

  it('does not report a failure through the console or an exception', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    mocks.getAddressOwners.mockRejectedValue(new Error('network down'))
    use(ref([address(1)]))
    await flushPromises()
    expect(warn).not.toHaveBeenCalled()
    expect(error).not.toHaveBeenCalled()
    warn.mockRestore()
    error.mockRestore()
  })

  it('stops after a failed batch rather than hammering a struggling backend', async () => {
    mocks.getAddressOwners.mockRejectedValue({ status: 502 })
    use(ref(Array.from({ length: 120 }, (_, i) => address(i + 1))))
    await flushPromises()
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)
  })

  it('notices an address pushed into the list in place', async () => {
    const list = ref<string[]>([address(1)])
    const { ownerOf } = use(list)
    await flushPromises()
    list.value.push(address(2))
    await flushPromises()
    expect(ownerOf(address(2))).toBeTruthy()
  })
})
