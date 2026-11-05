import { describe, expect, it } from 'vitest'
import { buildErrorLogColumns } from '../errorLogColumns'

describe('buildErrorLogColumns', () => {
  const columns = buildErrorLogColumns((key) => `T:${key}`)

  it('keeps one stable order, the same one the ops table renders and the usage page lists', () => {
    expect(columns.map((c) => c.key)).toEqual([
      'user', 'api_key', 'account', 'platform', 'model', 'endpoint', 'group', 'type', 'category',
      'status', 'message', 'request_id', 'upstream_request_id', 'created_at', 'user_agent', 'client_ip', 'actions',
    ])
  })

  it('has no duplicate keys', () => {
    expect(new Set(columns.map((c) => c.key)).size).toBe(columns.length)
  })

  it('labels every column through the translator it is given', () => {
    expect(columns.every((c) => c.label.startsWith('T:'))).toBe(true)
  })

  it('makes only model, status and time sortable', () => {
    expect(columns.filter((c) => c.sortable).map((c) => c.key)).toEqual(['model', 'status', 'created_at'])
  })
})
