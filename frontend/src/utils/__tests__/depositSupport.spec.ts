import { describe, expect, it } from 'vitest'
import { supportContactHref } from '../depositSupport'

describe('support contact link', () => {
  it.each([
    ['https://t.me/kedaya_support', 'https://t.me/kedaya_support'],
    ['http://example.com/support', 'http://example.com/support'],
    ['mailto:help@example.com', 'mailto:help@example.com'],
    ['help@example.com', 'mailto:help@example.com'],
    ['@kedaya_support', 'https://t.me/kedaya_support'],
    ['t.me/kedaya_support', 'https://t.me/kedaya_support'],
  ])('turns %s into a safe link', (contact, href) => {
    expect(supportContactHref(contact)).toBe(href)
  })

  it.each(['', '   ', 'QQ: 123456789', '微信 abc123', '123456789'])(
    'keeps free text %s out of a link (it is shown as copyable text instead)',
    (contact) => {
      expect(supportContactHref(contact)).toBe('')
    }
  )
})
