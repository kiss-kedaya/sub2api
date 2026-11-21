import { describe, expect, it } from 'vitest'
import { appendIPEntry, hasIPEntry, splitIPEntries } from '../ipWhitelist'

describe('ipWhitelist helpers', () => {
  it('splits entries on newlines, spaces and both comma styles', () => {
    expect(splitIPEntries('1.1.1.1\n2.2.2.2 3.3.3.3,4.4.4.4，5.5.5.5\n\n')).toEqual([
      '1.1.1.1',
      '2.2.2.2',
      '3.3.3.3',
      '4.4.4.4',
      '5.5.5.5'
    ])
  })

  it('fills an empty textarea directly', () => {
    expect(appendIPEntry('', '203.0.113.7')).toEqual({ value: '203.0.113.7', duplicate: false })
    expect(appendIPEntry('   \n', '203.0.113.7')).toEqual({ value: '203.0.113.7', duplicate: false })
  })

  it('appends instead of overwriting existing entries', () => {
    expect(appendIPEntry('10.0.0.1', '203.0.113.7')).toEqual({
      value: '10.0.0.1\n203.0.113.7',
      duplicate: false
    })
    // A trailing comma/whitespace from the user is normalized away.
    expect(appendIPEntry('10.0.0.1, ', '203.0.113.7')).toEqual({
      value: '10.0.0.1\n203.0.113.7',
      duplicate: false
    })
  })

  it('detects duplicates regardless of the separator the user typed', () => {
    expect(hasIPEntry('10.0.0.1, 203.0.113.7', '203.0.113.7')).toBe(true)
    expect(hasIPEntry('10.0.0.1\n203.0.113.7', ' 203.0.113.7 ')).toBe(true)
    expect(appendIPEntry('10.0.0.1 203.0.113.7', '203.0.113.7')).toEqual({
      value: '10.0.0.1 203.0.113.7',
      duplicate: true
    })
  })

  it('keeps CIDR entries as separate values', () => {
    expect(hasIPEntry('10.0.0.0/8', '10.0.0.1')).toBe(false)
    expect(appendIPEntry('10.0.0.0/8', '10.0.0.1').value).toBe('10.0.0.0/8\n10.0.0.1')
  })

  it('treats blank input as a duplicate and leaves the content untouched', () => {
    expect(appendIPEntry('10.0.0.1', '   ')).toEqual({ value: '10.0.0.1', duplicate: true })
  })

  it('treats IPv6 entries as the same address regardless of hex case', () => {
    expect(hasIPEntry('2001:DB8::1', '2001:db8::1')).toBe(true)
    expect(hasIPEntry('2001:db8::1', '2001:DB8::1')).toBe(true)
    expect(appendIPEntry('2001:DB8::1', '2001:db8::1')).toEqual({
      value: '2001:DB8::1',
      duplicate: true
    })
    // IPv4 has no case fold, so distinct addresses stay distinct.
    expect(hasIPEntry('10.0.0.1', '10.0.0.2')).toBe(false)
  })
})
