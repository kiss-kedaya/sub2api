import { beforeEach, describe, expect, it, vi } from 'vitest'
import { listBatchImageItems } from '@/api/batchImage'

describe('batch image item pagination', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('follows numeric cursors until all detail pages are merged', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ object: 'list', data: [{ custom_id: 'a' }], has_more: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ object: 'list', data: [{ custom_id: 'b' }], has_more: false }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await listBatchImageItems('sk-test', 'batch-1')

    expect(result.data.map(item => item.custom_id)).toEqual(['a', 'b'])
    expect(result.has_more).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(String(fetchMock.mock.calls[1][0])).toContain('cursor=1')
  })
})
