import { describe, expect, it } from 'vitest'
import type { DepositRecord } from '@/api/crypto'
import {
  depositTransferIdentity,
  findNewDepositKeys,
  isSameDepositHistory,
  isSameDepositRecord,
  uniqueDepositRowKeys
} from '../depositHistorySync'

function record(overrides: Partial<DepositRecord> = {}): DepositRecord {
  return {
    chain: 'ethereum',
    tx_hash: '0x' + 'a1'.repeat(32),
    token: 'ETH',
    amount: '0.5',
    status: 'credited',
    credited: 10753.902,
    credited_known: true,
    created_at: '2026-11-26T14:00:00Z',
    ...overrides
  }
}

describe('depositHistorySync', () => {
  it('treats identical snapshots as unchanged even if created_at ticks in local preview', () => {
    const a = [
      record(),
      record({
        chain: 'bsc',
        tx_hash: '0x' + 'b2'.repeat(32),
        token: 'BNB',
        amount: '1.25',
        status: 'pending',
        credited: 0,
        confirmations: 1,
        required_confirmations: 3
      })
    ]
    const b = [
      record({ created_at: '2026-11-26T14:00:05Z' }),
      record({
        chain: 'bsc',
        tx_hash: '0x' + 'b2'.repeat(32),
        token: 'BNB',
        amount: '1.25',
        status: 'pending',
        credited: 0,
        confirmations: 1,
        required_confirmations: 3,
        created_at: '2026-11-26T14:00:05Z'
      })
    ]
    expect(isSameDepositHistory(a, b)).toBe(true)
    expect(isSameDepositHistory([], [])).toBe(true)
  })

  it('detects length, order, confirmation, status, credit, and reason changes', () => {
    const base = record({ status: 'pending', credited: 0, confirmations: 1, required_confirmations: 3 })
    expect(isSameDepositRecord(base, { ...base, confirmations: 2 })).toBe(false)
    expect(isSameDepositRecord(base, { ...base, status: 'credited', credited: 67 })).toBe(false)
    expect(isSameDepositRecord(base, { ...base, reason: 'below_min' })).toBe(false)
    expect(isSameDepositRecord(base, { ...base, revoke_reason: 'reorg_noncanonical_block' })).toBe(false)
    expect(isSameDepositRecord(base, { ...base, credited_known: false })).toBe(false)
    expect(isSameDepositRecord(base, { ...base, created_at: '' })).toBe(false)

    const second = record({ tx_hash: '0x' + 'c3'.repeat(32) })
    expect(isSameDepositHistory([base], [base, second])).toBe(false)
    expect(isSameDepositHistory([base, second], [second, base])).toBe(false)
  })

  it('handles NaN credited values safely without false diffs', () => {
    const a = record({ credited: Number.NaN })
    const b = record({ credited: Number.NaN })
    expect(isSameDepositRecord(a, b)).toBe(true)
  })

  it('finds newly added deposit keys without flagging existing updated rows', () => {
    const existing = record({ tx_hash: '0x' + 'a1'.repeat(32), status: 'pending', confirmations: 1 })
    const updatedExisting = record({ tx_hash: '0x' + 'a1'.repeat(32), status: 'credited', credited: 67 })
    const brandNew = record({ chain: 'bsc', tx_hash: '0x' + 'ff'.repeat(32), status: 'pending' })

    expect(findNewDepositKeys([existing], [updatedExisting])).toEqual([])
    expect(findNewDepositKeys([existing], [brandNew, updatedExisting])).toEqual([depositTransferIdentity(brandNew)])
    expect(findNewDepositKeys([], [brandNew])).toEqual([depositTransferIdentity(brandNew)])
  })

  it('detects a newly arriving second transfer in the same tx_hash (different token or identical amount)', () => {
    const first = record({ chain: 'bsc', tx_hash: '0x1', token: 'USDT', amount: '10' })
    const secondToken = record({ chain: 'bsc', tx_hash: '0x1', token: 'USDC', amount: '20' })
    expect(findNewDepositKeys([first], [secondToken, first])).toEqual(['bsc:0x1:USDC:20'])

    const duplicateAmount = record({ chain: 'bsc', tx_hash: '0x1', token: 'USDT', amount: '10' })
    expect(findNewDepositKeys([first], [duplicateAmount, first])).toEqual(['bsc:0x1:USDT:10#2'])
  })

  it('numbers duplicate rows oldest-to-newest so prepending a new row preserves older row keys', () => {
    const rows = [
      record({ chain: 'bsc', tx_hash: '0x1', token: 'USDT', amount: '10' }),
      record({ chain: 'bsc', tx_hash: '0x2', token: 'USDT', amount: '10' }),
      record({ chain: 'bsc', tx_hash: '0x1', token: 'USDT', amount: '10' }),
      record({ chain: 'bsc', tx_hash: '0x1', token: 'USDT', amount: '10' })
    ]
    expect(uniqueDepositRowKeys(rows)).toEqual([
      'bsc:0x1:USDT:10#3',
      'bsc:0x2:USDT:10',
      'bsc:0x1:USDT:10#2',
      'bsc:0x1:USDT:10'
    ])
  })
})
