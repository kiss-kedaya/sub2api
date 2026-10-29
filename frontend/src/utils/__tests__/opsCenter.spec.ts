import { describe, expect, it } from 'vitest'
import type { AccountOpsEvent, AccountOpsThresholdAccount } from '@/api/admin/accountOps'
import { buildAttentionItems, maxUsedPercent, summarizeBalances } from '../opsCenter'

function account(overrides: Partial<AccountOpsThresholdAccount>): AccountOpsThresholdAccount {
  return {
    account_id: 1,
    account_name: 'account',
    platform: 'openai',
    type: 'apikey',
    balance: 0,
    unit: 'USD',
    balance_status: 'ok',
    received_at: null,
    usage_windows: [],
    ...overrides,
  }
}

function event(overrides: Partial<AccountOpsEvent>): AccountOpsEvent {
  return {
    account_id: 1,
    account_name: 'account',
    kind: 'balance_low',
    signal: 'balance_low',
    http_status: 200,
    first_seen: '2026-10-29T00:00:00Z',
    last_seen: '2026-10-29T00:00:00Z',
    occurrences: 1,
    state: 'pending',
    last_sent_at: null,
    next_send_at: '2026-10-29T00:05:00Z',
    attempts: 0,
    ...overrides,
  }
}

describe('summarizeBalances', () => {
  it('groups numeric balances by unit and skips missing values', () => {
    const totals = summarizeBalances([
      account({ account_id: 1, balance: 10.5, unit: 'usd' }),
      account({ account_id: 2, balance: 4.5, unit: 'USD' }),
      account({ account_id: 3, balance: 7, unit: 'CNY' }),
      account({ account_id: 4, balance: null }),
    ])
    expect(totals).toEqual([
      { unit: 'USD', total: 15, accounts: 2 },
      { unit: 'CNY', total: 7, accounts: 1 },
    ])
  })

  it('returns an empty list when no account reports a balance', () => {
    expect(summarizeBalances([account({ balance: null })])).toEqual([])
  })
})

describe('maxUsedPercent', () => {
  it('returns the highest reported quota window', () => {
    const value = maxUsedPercent(account({
      usage_windows: [
        { window: '5h', label: '5h', used_percent: 12, status: 'ok' },
        { window: '7d', label: '7d', used_percent: 91.4, status: 'ok' },
        { window: 'any', label: '额度', used_percent: null, status: 'unknown' },
      ],
    }))
    expect(value).toBe(91.4)
  })
})

describe('buildAttentionItems', () => {
  it('prioritises failed notifications, then balance and quota risks', () => {
    const items = buildAttentionItems(
      [
        account({ account_id: 2, account_name: 'failed-balance', balance_status: 'failed' }),
        account({
          account_id: 3,
          account_name: 'quota',
          usage_windows: [{ window: '5h', label: '5h', used_percent: 95, status: 'ok' }],
        }),
      ],
      [event({ id: 'evt-1', account_id: 1, account_name: 'alert', state: 'failed' })],
    )
    expect(items.map(item => item.severity)).toEqual(['critical', 'critical', 'warning'])
    expect(items[0].title).toBe('余额不足')
    expect(items[1].title).toBe('余额读取失败')
    expect(items[2].title).toBe('额度即将用尽')
  })

  it('drops resolved events and applies the limit', () => {
    const items = buildAttentionItems(
      [
        account({ account_id: 1, balance_status: 'stale' }),
        account({ account_id: 2, balance_status: 'failed' }),
      ],
      [event({ state: 'resolved' })],
      1,
    )
    expect(items).toHaveLength(1)
    expect(items[0].title).toBe('余额读取失败')
  })
})
