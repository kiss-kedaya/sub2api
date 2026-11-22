import { describe, expect, it } from 'vitest'
import {
  filterDepositHistory, isDepositFilterActive, matchesDepositStatus, matchesDepositTimeRange,
} from '../depositHistoryFilter'
import type { DepositRecord } from '@/api/crypto'

const NOW = Date.parse('2026-03-01T00:00:00Z')
const DAY = 24 * 60 * 60 * 1000

function record(status: string, daysAgo: number, hash = status): DepositRecord {
  return {
    chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status,
    credited: 0, created_at: new Date(NOW - daysAgo * DAY).toISOString(),
  }
}

describe('status filter', () => {
  it('keeps everything under "all"', () => {
    expect(matchesDepositStatus({ status: 'pending' }, 'all')).toBe(true)
    expect(matchesDepositStatus({ status: 'ignored' }, 'all')).toBe(true)
  })

  it.each(['credited', 'provisional', 'revoked', 'ignored'])('matches only %s rows in its own bucket', (status) => {
    expect(matchesDepositStatus({ status }, status as never)).toBe(true)
    expect(matchesDepositStatus({ status: 'pending' }, status as never)).toBe(false)
  })
})

describe('time range filter', () => {
  it('keeps everything under "all"', () => {
    expect(matchesDepositTimeRange(record('credited', 400), 'all', NOW)).toBe(true)
  })

  it.each([
    ['7d', 6, true],
    ['7d', 8, false],
    ['30d', 29, true],
    ['30d', 31, false],
    ['90d', 89, true],
    ['90d', 91, false],
  ] as const)('%s includes a row %s days old: %s', (range, daysAgo, expected) => {
    expect(matchesDepositTimeRange(record('credited', daysAgo), range, NOW)).toBe(expected)
  })

  it('keeps a row with an unparsable timestamp instead of hiding a real deposit', () => {
    expect(matchesDepositTimeRange({ created_at: 'not-a-time' }, '7d', NOW)).toBe(true)
  })
})

describe('filter combinations', () => {
  const rows = [
    record('credited', 1, 'c1'),
    record('credited', 40, 'c40'),
    record('provisional', 2, 'p2'),
    record('revoked', 3, 'r3'),
    record('ignored', 60, 'i60'),
    record('pending', 1, 'pend1'),
  ]

  it('applies the status and the time range together', () => {
    expect(filterDepositHistory(rows, 'credited', '7d', NOW).map(r => r.tx_hash)).toEqual(['c1'])
    expect(filterDepositHistory(rows, 'credited', '90d', NOW).map(r => r.tx_hash)).toEqual(['c1', 'c40'])
    expect(filterDepositHistory(rows, 'ignored', '7d', NOW)).toEqual([])
    expect(filterDepositHistory(rows, 'ignored', '90d', NOW).map(r => r.tx_hash)).toEqual(['i60'])
  })

  it('keeps pending rows visible under "all" but never in a named bucket', () => {
    expect(filterDepositHistory(rows, 'all', 'all', NOW).map(r => r.tx_hash)).toContain('pend1')
    for (const status of ['credited', 'provisional', 'revoked', 'ignored'] as const) {
      expect(filterDepositHistory(rows, status, 'all', NOW).map(r => r.tx_hash)).not.toContain('pend1')
    }
  })

  it('does not mutate the input list', () => {
    const before = [...rows]
    filterDepositHistory(rows, 'credited', '7d', NOW)
    expect(rows).toEqual(before)
  })
})

describe('active filter detection', () => {
  it('is inactive only when both filters are "all"', () => {
    expect(isDepositFilterActive('all', 'all')).toBe(false)
    expect(isDepositFilterActive('revoked', 'all')).toBe(true)
    expect(isDepositFilterActive('all', '30d')).toBe(true)
    expect(isDepositFilterActive('credited', '7d')).toBe(true)
  })
})
