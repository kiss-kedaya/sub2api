import type { Snapshot } from '@/api/admin/channelOperations'

// Old values retain their original scope and timestamp. Never borrow metrics
// from a different filter or time window when a section query fails.
export function keepSuccessfulSections(previous: Snapshot | null, incoming: Snapshot): Snapshot {
  if (!previous || previous.from !== incoming.from || previous.until !== incoming.until ||
    !(Object.keys(incoming.scope) as Array<keyof Snapshot['scope']>).every(key => previous.scope[key] === incoming.scope[key])) return incoming
  const merged = { ...incoming }
  for (const key of ['usage', 'quality', 'errors', 'capacity'] as const) {
    if (incoming[key].status === 'query_failed' && previous[key].status === 'ready') {
      Object.assign(merged, { [key]: { ...previous[key], stale_error: incoming[key].error || 'query_failed' } })
    }
  }
  return merged
}
