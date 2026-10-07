import { describe, expect, it } from 'vitest'
import { parseTableDefaultPageSizeInput, parseTablePageSizeOptionsInput } from '@/utils/tablePreferences'

describe('table preference input validation', () => {
  it.each([0, 4, -1, 1001, 20.5, NaN, Infinity, ''])('rejects invalid defaults: %s', (value) => {
    expect(parseTableDefaultPageSizeInput(value)).toBeNull()
  })

  it.each([5, 20, 1000])('accepts integral defaults: %s', (value) => {
    expect(parseTableDefaultPageSizeInput(value)).toBe(value)
  })

  it.each(['', '4,20', '20,1001', '20.5,50', '10,,20', '10,', '1e2,20', '0x20,50'])('rejects invalid options: %s', (value) => {
    expect(parseTablePageSizeOptionsInput(value)).toBeNull()
  })

  it('deduplicates and sorts valid options', () => {
    expect(parseTablePageSizeOptionsInput('50, 10,20,10')).toEqual([10, 20, 50])
    expect(parseTablePageSizeOptionsInput('1000,5')).toEqual([5, 1000])
  })
})
