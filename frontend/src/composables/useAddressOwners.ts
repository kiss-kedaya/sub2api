import { reactive, watch, type WatchSource } from 'vue'
import { getAddressOwners, type AddressOwner } from '@/api/admin/crypto'

// Resolves deposit addresses to the user they belong to, for the sweep tables.
//
// The cache is module level so every table on the page shares it: an address
// is asked about once per page load no matter how many tables list it.
//   undefined  never looked up (or the lookup failed; a later change retries)
//   null       looked up, nobody owns it
//   owner      looked up
// Lookups never throw and never block a table. A failure, or a backend that
// predates the endpoint (404), leaves the owner unknown and the cell shows "—".

/** Matches the backend's per-request cap (100) with headroom. */
export const OWNER_BATCH_SIZE = 40

const EVM_ADDRESS = /^0x[0-9a-fA-F]{40}$/

const cache = reactive(new Map<string, AddressOwner | null>())
const inflight = new Set<string>()
let endpointMissing = false

/** Test hook: forget everything, including a remembered 404. */
export function clearAddressOwnerCache(): void {
  cache.clear()
  inflight.clear()
  endpointMissing = false
}

function normalizeOwner(raw: unknown): AddressOwner | null {
  if (!raw || typeof raw !== 'object') return null
  const value = raw as Record<string, unknown>
  const id = Number(value.user_id)
  if (!Number.isFinite(id) || id <= 0) return null
  return {
    user_id: id,
    username: typeof value.username === 'string' ? value.username : '',
    email: typeof value.email === 'string' ? value.email : '',
    deleted: value.deleted === true
  }
}

async function resolveOwners(addresses: readonly string[]): Promise<void> {
  if (endpointMissing) return
  const wanted: string[] = []
  const queued = new Set<string>()
  for (const raw of addresses) {
    if (typeof raw !== 'string' || !EVM_ADDRESS.test(raw)) continue
    const key = raw.toLowerCase()
    if (cache.has(key) || inflight.has(key) || queued.has(key)) continue
    queued.add(key)
    wanted.push(key)
  }
  for (let start = 0; start < wanted.length; start += OWNER_BATCH_SIZE) {
    if (endpointMissing) return
    const batch = wanted.slice(start, start + OWNER_BATCH_SIZE)
    batch.forEach(key => inflight.add(key))
    try {
      const owners = await getAddressOwners(batch)
      for (const key of batch) cache.set(key, normalizeOwner(owners[key]))
    } catch (error) {
      if ((error as { status?: number } | null)?.status === 404) endpointMissing = true
      // Anything else: leave the batch unknown and stop; the next change retries.
      return
    } finally {
      batch.forEach(key => inflight.delete(key))
    }
  }
}

/**
 * Feed it the addresses a table currently shows. `ownerOf(address)` is
 * reactive: it returns undefined until the lookup lands, then the owner (or
 * null when the address has none).
 */
export function useAddressOwners(addresses: WatchSource<readonly string[]>) {
  watch(
    // A fresh array each time, so an in-place push is noticed too.
    () => [...(typeof addresses === 'function' ? addresses() : addresses.value)],
    list => { void resolveOwners(list) },
    { immediate: true }
  )

  function ownerOf(address: string | null | undefined): AddressOwner | null | undefined {
    if (!address) return undefined
    return cache.get(address.toLowerCase())
  }

  return { ownerOf }
}
