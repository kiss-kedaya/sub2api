import { describe, expect, it } from 'vitest'

import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import type { DepositNetworkStatus } from '@/api/crypto'
import {
  cardEtaText, etaNoticeText, formatEtaDuration, networkBadge, networkStateOf, parseEtaSeconds,
  pruneNetworkStatuses,
} from '../depositNetwork'

// The page renders real locale messages, so the mapping is checked against the
// shipped Chinese/English strings instead of a mocked translator. A missing key
// throws here as well, which keeps the mapping and the locale in step.
type Messages = Record<string, unknown>

function translator(messages: Messages) {
  return (key: string, params?: Record<string, unknown>): string => {
    let node: unknown = messages
    for (const part of key.split('.')) {
      if (!node || typeof node !== 'object') throw new Error('missing translation: ' + key)
      node = (node as Messages)[part]
    }
    if (typeof node !== 'string') throw new Error('missing translation: ' + key)
    return params
      ? node.replace(/\{(\w+)\}/g, (_match, name: string) => String(params[name] ?? ''))
      : node
  }
}

const t = translator(zh)
const tEn = translator(en)

function row(overrides: Partial<DepositNetworkStatus> = {}): DepositNetworkStatus {
  return { chain: 'bsc', status: 'available', expires_at: new Date(Date.now() + 60_000).toISOString(), ...overrides }
}

describe('deposit ETA formatting', () => {
  it.each([
    [1, '1 秒'],
    [35, '35 秒'],
    [89, '89 秒'],
    // 90s is where seconds stop being helpful: from here the copy rounds to
    // whole minutes (90s → 2 分钟).
    [90, '2 分钟'],
    [200, '3 分钟'],
    [480, '8 分钟'],
    [1800, '30 分钟'],
    // Past 30 minutes the exact figure would read as a promise.
    [1801, '30+ 分钟'],
    [7200, '30+ 分钟'],
  ])('formats %i seconds as %s', (seconds, expected) => {
    expect(formatEtaDuration(seconds, t)).toBe(expected)
  })

  it.each([null, undefined, 0, -5, Number.NaN, Number.POSITIVE_INFINITY, '', 'abc'])(
    'shows no estimate for %s',
    (value) => {
      expect(formatEtaDuration(value as number | null | undefined, t)).toBeNull()
      expect(parseEtaSeconds(value)).toBeNull()
    }
  )

  it('accepts a numeric string, which some encoders emit', () => {
    expect(parseEtaSeconds('35')).toBe(35)
    expect(formatEtaDuration('35' as unknown as number, t)).toBe('35 秒')
  })

  it('uses the English unit vocabulary too', () => {
    expect(formatEtaDuration(35, tEn)).toBe('35s')
    expect(formatEtaDuration(200, tEn)).toBe('3 min')
    expect(formatEtaDuration(7200, tEn)).toBe('30+ min')
  })
})

describe('deposit network badge', () => {
  // 状态 → 文案映射表：正常网络绝不出现「延迟」字样。
  it.each([
    ['available', { etaSeconds: 35 }, 'success', '可充值 · 约 35 秒到账'],
    ['available', { etaSeconds: 480 }, 'success', '可充值 · 约 8 分钟到账'],
    ['available', { etaSeconds: 7200 }, 'success', '可充值 · 约 30+ 分钟到账'],
    ['available', {}, 'success', '可充值'],
    ['slow', { etaSeconds: 200 }, 'neutral', '网络较慢 · 约 3 分钟到账'],
    ['slow', {}, 'neutral', '网络较慢'],
    ['delayed', { etaSeconds: 480 }, 'warning', '到账延迟 · 预计 8 分钟'],
    ['delayed', {}, 'warning', '到账延迟'],
    ['paused', {}, 'warning', '充值暂停'],
    ['stalled', {}, 'warning', '处理异常'],
    // 没有新鲜证据只说「检测中」，不说「可用」也不说「延迟」。
    ['unknown', {}, 'neutral', '状态检测中'],
    // A stale ETA never upgrades an unknown network into an estimate.
    ['unknown', { etaSeconds: 35 }, 'neutral', '状态检测中'],
  ] as const)('renders %s with %o as "%s"', (state, extra, color, text) => {
    expect(networkBadge({ state, ...extra }, t)).toEqual({ color, text })
  })

  it('instant credit replaces the ETA with "即时到账"', () => {
    expect(networkBadge({ state: 'available', etaSeconds: 35, instantCredit: true }, t))
      .toEqual({ color: 'success', text: '可充值 · 即时到账' })
    expect(networkBadge({ state: 'available', etaSeconds: 35, instantCredit: true }, tEn))
      .toEqual({ color: 'success', text: 'Available · credited instantly' })
  })

  it('instant credit does not claim an instant credit on a paused network', () => {
    expect(networkBadge({ state: 'paused', etaSeconds: 35, instantCredit: true }, t).text).toBe('充值暂停')
  })
})

describe('deposit ETA on the card and in the top notice', () => {
  it('shows the estimate under the network card', () => {
    expect(cardEtaText({ state: 'available', etaSeconds: 35 }, t)).toBe('预计 35 秒到账')
    expect(cardEtaText({ state: 'slow', etaSeconds: 200, instantCredit: false }, t)).toBe('预计 3 分钟到账')
  })

  it('shows 即时到账 on an instant-credit card', () => {
    expect(cardEtaText({ state: 'available', etaSeconds: 35, instantCredit: true }, t)).toBe('即时到账')
  })

  it.each(['unknown', 'paused', 'stalled'] as const)('shows no estimate for a %s network', (state) => {
    expect(cardEtaText({ state, etaSeconds: 35 }, t)).toBeNull()
  })

  it('falls back to nothing when a working network has no estimate', () => {
    expect(cardEtaText({ state: 'available' }, t)).toBeNull()
  })

  it('explains the instant-credit rule in the top notice', () => {
    // 即时入账必须说清「失败会回滚」，否则用户以为余额已经稳了。
    expect(etaNoticeText({ state: 'available', instantCredit: true }, t)).toBe(
      '检测到转账后立即入账；若交易最终失败会自动回滚，请以完成确认为准。'
    )
    expect(etaNoticeText({ state: 'available', instantCredit: true }, tEn)).toContain('rolled back automatically')
  })

  it('shows the arrival estimate in the top notice otherwise', () => {
    expect(etaNoticeText({ state: 'available', etaSeconds: 35 }, t)).toBe('预计 35 秒到账')
    expect(etaNoticeText({ state: 'available', etaSeconds: 200 }, tEn)).toBe('About 3 min to credit')
  })

  it('stays quiet for problem states, which carry their own notice', () => {
    for (const state of ['slow', 'delayed', 'paused', 'stalled', 'unknown'] as const) {
      expect(etaNoticeText({ state, etaSeconds: 35, instantCredit: true }, t)).toBeNull()
    }
  })
})

describe('deposit network status expiry', () => {
  const now = Date.parse('2026-01-01T00:00:00Z')
  const at = (offsetMs: number) => new Date(now + offsetMs).toISOString()

  it('keeps a row the backend still vouches for and reports its expiry', () => {
    const first = row({ chain: 'bsc', expires_at: at(60_000) })
    const second = row({ chain: 'base', expires_at: at(30_000) })
    const pruned = pruneNetworkStatuses([first, second], now)

    expect(pruned.rows).toEqual([first, second])
    // The timer must fire at the earliest expiry, not the last one.
    expect(pruned.nextExpiry).toBe(Date.parse(at(30_000)))
  })

  it.each([
    ['an expired row', at(-1)],
    ['a row expiring exactly now', at(0)],
    ['a malformed expiry', 'not-a-time'],
  ])('drops %s, so a stale status cannot stay on screen', (_reason, expiresAt) => {
    // 「到账延迟」过期后必须回到 unknown，不能沿用旧状态。
    const pruned = pruneNetworkStatuses([row({ status: 'delayed', expires_at: expiresAt })], now)

    expect(pruned.rows).toEqual([])
    expect(pruned.nextExpiry).toBeNull()
    expect(networkStateOf(new Map(pruned.rows.map(r => [r.chain, r])), 'bsc')).toBe('unknown')
  })

  it('keeps an explicit unknown that carries no expiry', () => {
    const unknown = row({ status: 'unknown', expires_at: undefined })
    expect(pruneNetworkStatuses([unknown], now).rows).toEqual([unknown])
  })

  it('drops a confirmed state that carries no expiry', () => {
    // 没有证据年龄的「暂停/延迟」等于旧数据，不能当新鲜结论展示。
    for (const status of ['available', 'slow', 'delayed', 'paused', 'stalled'] as const) {
      expect(pruneNetworkStatuses([row({ status, expires_at: undefined })], now).rows).toEqual([])
    }
  })

  it('treats a null expiry like an absent one instead of dropping the row', () => {
    const unknown = row({ status: 'unknown', expires_at: undefined })
    // The API layer maps JSON null to undefined; an unknown stays unknown.
    expect(pruneNetworkStatuses([{ ...unknown, expires_at: null } as unknown as DepositNetworkStatus], now).rows.length).toBe(1)
    expect(pruneNetworkStatuses([{ ...row({ status: 'paused' }), expires_at: null } as unknown as DepositNetworkStatus], now).rows).toEqual([])
  })

  it('reports unknown for a chain that is not in the fresh set', () => {
    const states = new Map(pruneNetworkStatuses([row()], now).rows.map(r => [r.chain, r]))
    expect(networkStateOf(states, 'bsc')).toBe('available')
    expect(networkStateOf(states, 'base')).toBe('unknown')
  })
})
