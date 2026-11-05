import { describe, expect, it } from 'vitest'
import type { SweepRecord } from '@/api/admin/crypto'
import zh from '@/i18n/locales/zh/admin/crypto'
import en from '@/i18n/locales/en/admin/crypto'
import {
  SWEEP_FILTER_ALL,
  SWEEP_STATUS_GROUPS,
  SWEEP_STATUS_LABEL_KEYS,
  formatSweepFullTime,
  formatSweepTime,
  redactSweepError,
  shortValue,
  sweepRecordMatches,
  sweepStatusColor,
  sweepStatusLabelKey,
  sweepStatusMatches
} from '../cryptoSweepView'

const record = (over: Partial<SweepRecord> = {}): SweepRecord => ({
  id: 120, chain: 'bsc', from_address: '0x' + 'a1'.repeat(20), to_address: '0x' + 'b2'.repeat(20), token: '0xtoken',
  amount: '12.5', gas_tx_hash: '0x' + 'c3'.repeat(32), sweep_tx_hash: '0x' + 'd4'.repeat(32),
  status: 'swept', error: '', created_at: '2026-11-05T00:00:00Z', ...over
})

describe('status filter groups', () => {
  it('maps each filter entry to the statuses the operator expects', () => {
    expect(SWEEP_STATUS_GROUPS.swept).toEqual(['swept'])
    expect(SWEEP_STATUS_GROUPS.running).toEqual(['gas_pending', 'gas_confirmed', 'sweep_pending', 'token_pending'])
    expect(SWEEP_STATUS_GROUPS.failed).toEqual(['failed'])
    expect(SWEEP_STATUS_GROUPS.pending).toEqual(['planned', 'manual_review'])
  })

  it('matches by group and lets everything through for "all" or an unknown filter', () => {
    expect(sweepStatusMatches('running', 'sweep_pending')).toBe(true)
    expect(sweepStatusMatches('running', 'swept')).toBe(false)
    expect(sweepStatusMatches('pending', 'manual_review')).toBe(true)
    expect(sweepStatusMatches('failed', 'failed')).toBe(true)
    expect(sweepStatusMatches(SWEEP_FILTER_ALL, 'anything')).toBe(true)
    expect(sweepStatusMatches('nonsense', 'swept')).toBe(true)
    expect(sweepStatusMatches('__proto__', 'swept')).toBe(true)
  })

  it('colours by outcome: green done, amber in flight or waiting, red failed', () => {
    expect(sweepStatusColor('swept')).toBe('success')
    expect(sweepStatusColor('failed')).toBe('error')
    for (const status of ['planned', 'gas_pending', 'gas_confirmed', 'sweep_pending', 'token_pending', 'manual_review']) {
      expect(sweepStatusColor(status)).toBe('warning')
    }
    expect(sweepStatusColor('mystery')).toBe('neutral')
  })
})

describe('status labels', () => {
  // "admin.crypto.statusSwept" lives at crypto.statusSwept in the admin locale file.
  const messageFor = (messages: { crypto: Record<string, unknown> }, key: string): unknown =>
    key.replace(/^admin\.crypto\./, '').split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages.crypto)

  it('has a real message in both languages for every status key (dynamic keys escape the static key check)', () => {
    expect(SWEEP_STATUS_LABEL_KEYS.length).toBeGreaterThan(0)
    for (const key of SWEEP_STATUS_LABEL_KEYS) {
      expect(key.startsWith('admin.crypto.'), key).toBe(true)
      for (const messages of [zh, en]) {
        const value = messageFor(messages, key)
        expect(typeof value, `${key} missing`).toBe('string')
        expect((value as string).trim(), `${key} empty`).not.toBe('')
      }
    }
  })

  it('returns "" for an unknown status so the caller can show the raw value', () => {
    expect(sweepStatusLabelKey('swept')).toBe('admin.crypto.statusSwept')
    expect(sweepStatusLabelKey('sweep_pending')).toBe('admin.crypto.statusTokenPending')
    expect(sweepStatusLabelKey('mystery')).toBe('')
    expect(sweepStatusLabelKey('constructor')).toBe('')
  })
})

describe('error line redaction', () => {
  it('hides URLs, key/token pairs and 64-digit hex runs', () => {
    const out = redactSweepError('rpc https://node.example.test/v1/abcd?key=fixture-secret failed; apikey=fixture-secret token: fixture-secret 0x' + 'ab'.repeat(32))
    expect(out).not.toContain('https://')
    expect(out).not.toContain('fixture-secret')
    expect(out).not.toContain('ab'.repeat(32))
    expect(out).toContain('[url]')
    expect(out).toContain('[hex]')
  })

  it('keeps an ordinary message, bounds a long one, and survives empty input', () => {
    expect(redactSweepError('awaiting canonical receipt and required confirmations')).toBe('awaiting canonical receipt and required confirmations')
    expect(redactSweepError('x'.repeat(1000)).length).toBeLessThanOrEqual(301)
    expect(redactSweepError(undefined)).toBe('')
    expect(redactSweepError(null)).toBe('')
    expect(redactSweepError('')).toBe('')
  })
})

describe('time display', () => {
  const now = Date.parse('2026-11-05T12:00:00Z')

  it('omits the year for anything under a year old and keeps it beyond', () => {
    const recent = formatSweepTime('2026-09-01T08:00:00Z', 'en-US', now)
    const old = formatSweepTime('2024-09-01T08:00:00Z', 'en-US', now)
    expect(recent).not.toContain('2026')
    expect(old).toContain('2024')
  })

  it('treats a future time the same way and rejects bad input', () => {
    expect(formatSweepTime('2026-11-06T08:00:00Z', 'en-US', now)).not.toContain('2026')
    expect(formatSweepTime('', 'en-US', now)).toBe('—')
    expect(formatSweepTime(undefined, 'en-US', now)).toBe('—')
    expect(formatSweepTime('not a date', 'en-US', now)).toBe('—')
    expect(formatSweepFullTime('not a date')).toBe('—')
    expect(formatSweepFullTime('')).toBe('—')
    expect(formatSweepFullTime('2026-11-05T00:00:00Z')).not.toBe('—')
  })

  it('never renders hour 24', () => {
    expect(formatSweepTime('2026-11-05T16:30:00Z', 'zh-CN', now)).not.toMatch(/24:/)
  })
})

describe('client-side search', () => {
  const owner = { user_id: 7, username: 'fixture-name', email: 'fixture-user@example.test', deleted: false }

  it('matches user name, email, address and both transaction hashes, ignoring case', () => {
    const r = record()
    expect(sweepRecordMatches(r, owner, 'FIXTURE-NAME')).toBe(true)
    expect(sweepRecordMatches(r, owner, 'user@example')).toBe(true)
    expect(sweepRecordMatches(r, owner, r.from_address.slice(0, 12).toUpperCase())).toBe(true)
    expect(sweepRecordMatches(r, owner, r.sweep_tx_hash.slice(0, 10))).toBe(true)
    expect(sweepRecordMatches(r, owner, r.gas_tx_hash.slice(-8))).toBe(true)
    expect(sweepRecordMatches(r, owner, 'nobody')).toBe(false)
  })

  it('matches the record id exactly, with or without #, not as a substring', () => {
    // Letters only, so no address or hash can contain the digits being searched.
    const noDigits = { from_address: '0x' + 'ab'.repeat(20), sweep_tx_hash: '0x' + 'cd'.repeat(32), gas_tx_hash: '0x' + 'ef'.repeat(32) }
    const r = record({ id: 12, ...noDigits })
    expect(sweepRecordMatches(r, null, '12')).toBe(true)
    expect(sweepRecordMatches(r, null, '#12')).toBe(true)
    expect(sweepRecordMatches(r, null, '1')).toBe(false)
    expect(sweepRecordMatches(record({ id: 120, ...noDigits }), null, '12')).toBe(false)
  })

  it('an empty query matches everything, and an unknown owner just skips the owner fields', () => {
    expect(sweepRecordMatches(record(), undefined, '   ')).toBe(true)
    expect(sweepRecordMatches(record(), undefined, 'fixture-name')).toBe(false)
    expect(sweepRecordMatches(record({ gas_tx_hash: '', sweep_tx_hash: '' }), null, '0xdd')).toBe(false)
  })
})

describe('shortValue', () => {
  it('shortens long values like the rest of the page and dashes empty ones', () => {
    expect(shortValue('0x' + 'ab'.repeat(20))).toBe('0xabababab'.slice(0, 8) + '…' + 'ababab')
    expect(shortValue('short')).toBe('short')
    expect(shortValue('')).toBe('—')
    expect(shortValue(undefined)).toBe('—')
  })
})
