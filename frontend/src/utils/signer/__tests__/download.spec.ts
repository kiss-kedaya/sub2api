import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { buildShareSheetHtml, downloadTextFile, openSharePrintWindow, shareFileName } from '../download'

const labels = {
  title: 'sub2api wallet recovery shard',
  warning: 'Keep this offline',
  shareLabel: 'Share',
  createdAtLabel: 'Created',
  recoveryTitle: 'Recovery',
  recoverySteps: ['step one', 'step two'],
  footer: 'sensitive'
}

describe('share export helpers', () => {
  beforeEach(() => {
    URL.createObjectURL = vi.fn(() => 'blob:shard')
    URL.revokeObjectURL = vi.fn()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('builds a printable A4 sheet with the share, timestamp, recovery steps and warning', () => {
    const html = buildShareSheetHtml({
      shareText: 'S2V1-AAAA-BBBB',
      slotLabel: 'C',
      shareIndex: 3,
      createdAt: '2026-11-22 10:00:00',
      qrDataUrl: 'data:image/png;base64,AAAA',
      labels
    })
    expect(html).toContain('@page { size: A4;')
    expect(html).toContain('S2V1-AAAA-BBBB')
    expect(html).toContain('2026-11-22 10:00:00')
    expect(html).toContain('Keep this offline')
    expect(html).toContain('step one')
    expect(html).toContain('data:image/png;base64,AAAA')
  })

  it('escapes HTML in the share and labels instead of injecting it', () => {
    const html = buildShareSheetHtml({
      shareText: '<script>alert(1)</script>',
      slotLabel: 'B',
      shareIndex: 2,
      createdAt: 'now',
      labels: { ...labels, title: '<b>title</b>' }
    })
    expect(html).not.toContain('<script>alert(1)</script>')
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;')
    expect(html).toContain('&lt;b&gt;title&lt;/b&gt;')
  })

  it('names the .txt file after the slot and the date', () => {
    expect(shareFileName('C', new Date(2026, 10, 22))).toBe('sub2api-shard-C-20261122.txt')
  })

  it('triggers a text download through a blob anchor and cleans the URL up', () => {
    vi.useFakeTimers()
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    expect(downloadTextFile('S2V1-X', 'shard.txt')).toBe(true)
    expect(click).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
    vi.runAllTimers()
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:shard')
    vi.useRealTimers()
  })

  it('reports a blocked print popup instead of throwing', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    expect(openSharePrintWindow('<html></html>')).toBe(false)
    open.mockRestore()
  })
})
