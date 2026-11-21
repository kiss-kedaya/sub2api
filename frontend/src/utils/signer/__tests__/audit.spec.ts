import { describe, expect, it } from 'vitest'

import { buildSweepAuditInfo } from '../audit'

describe('sweep audit summary', () => {
  it('reports addresses, amount and unique transaction hashes', () => {
    const audit = buildSweepAuditInfo({
      plan: { TotalUSD: 12.5, Entries: [{ Address: 'a' }, { Address: 'b' }] },
      summary: {
        planned: 2,
        swept: 2,
        failed: 0,
        dry_run: false,
        entries: [
          { sweep_tx_hash: '0xaa', gas_tx_hash: '0xbb' },
          { sweep_tx_hash: '0xaa', gas_tx_hash: '0xcc' }
        ]
      },
      finished: true
    })
    expect(audit).toMatchObject({
      addresses: 2,
      planned: 2,
      swept: 2,
      failed: 0,
      amountUsd: 12.5,
      txHashes: ['0xaa', '0xbb', '0xcc'],
      status: 'completed',
      dryRun: false
    })
  })

  it('marks a run with failures as partial and an errored run as error', () => {
    expect(buildSweepAuditInfo({
      summary: { planned: 3, swept: 2, failed: 1, entries: [] }, finished: true
    }).status).toBe('partial')
    expect(buildSweepAuditInfo({
      summary: { planned: 1, swept: 0, failed: 0, entries: [] }, runError: 'rpc down', finished: true
    }).status).toBe('error')
  })

  it('falls back to the plan address count and a null amount when no summary exists', () => {
    const audit = buildSweepAuditInfo({
      plan: { Entries: [{}, {}] },
      summary: null,
      finished: true
    })
    expect(audit.addresses).toBe(2)
    expect(audit.amountUsd).toBeNull()
    expect(audit.txHashes).toEqual([])
  })
})
