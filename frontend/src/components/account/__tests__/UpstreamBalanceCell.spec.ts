import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UpstreamBalanceCell from '../UpstreamBalanceCell.vue'
import UpstreamKindBadge from '../UpstreamKindBadge.vue'
import { formatUpstreamAmount, upstreamBalanceView, upstreamKindOf } from '../upstreamBalance'
import type { Account, UpstreamBalanceSnapshot } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'en' },
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${Object.values(params).join(',')}` : key
    })
  }
})

const NOW = Date.parse('2026-11-11T10:00:00Z')
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString()

const makeAccount = (probe?: Record<string, unknown>, type: Account['type'] = 'apikey'): Account => ({
  id: 1,
  name: 'upstream',
  platform: 'openai',
  type,
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-07-13T00:00:00Z',
  updated_at: '2026-07-13T00:00:00Z',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null,
  extra: probe ? { upstream_billing_probe: { status: 'ok', last_attempt_at: iso(0), next_probe_at: iso(30 * 60_000), ...probe } as never } : {}
})

const ok = (data: Record<string, unknown>, source = 'sub2api_usage'): UpstreamBalanceSnapshot => ({
  status: 'ok', source, data, received_at: iso(-10 * 60_000), fresh_until: iso(60 * 60_000), last_attempt_at: iso(-10 * 60_000), http_status: 200
})

// The real HelpTooltip only mounts its body while open; open/close and keyboard focus are checked in a
// real browser. Here the body is rendered inline so its content can be asserted.
const HelpTooltipStub = { template: '<div><slot name="trigger" /><slot /></div>' }

const mountCell = (account: Account) => mount(UpstreamBalanceCell, {
  props: { account, now: NOW },
  global: { stubs: { UIcon: true, HelpTooltip: HelpTooltipStub, UButton: { template: '<button v-bind="$attrs" />' } } }
})

const headline = (account: Account) => mountCell(account).get('[data-testid="upstream-balance-value"]').text()
const details = (account: Account) => mountCell(account).get('[data-testid="upstream-balance-details"]')

describe('upstreamKindOf', () => {
  it.each(['sub2api', 'newapi', 'openai_compatible', 'official', 'custom'])('reads %s', kind => {
    expect(upstreamKindOf(makeAccount({ upstream_kind: kind }))).toBe(kind)
  })

  it('reports unknown when the probe has not identified the upstream, or names a kind this build does not know', () => {
    expect(upstreamKindOf(makeAccount())).toBe('unknown')
    expect(upstreamKindOf(makeAccount({ upstream_kind: 'future_kind' }))).toBe('unknown')
  })

  it('has no kind for accounts that are not API keys', () => {
    expect(upstreamKindOf(makeAccount({ upstream_kind: 'sub2api' }, 'oauth'))).toBeNull()
  })
})

describe('UpstreamKindBadge', () => {
  it.each(['sub2api', 'newapi', 'openai_compatible', 'official', 'custom', 'unknown'] as const)('renders a text label for %s', kind => {
    const wrapper = mount(UpstreamKindBadge, { props: { kind } })
    const badge = wrapper.get('[data-testid="upstream-kind-badge"]')
    expect(badge.attributes('data-upstream-kind')).toBe(kind)
    expect(badge.text()).toBe(`admin.accounts.upstreamKind.label.${kind}`)
    expect(badge.attributes('title')).toBe(`admin.accounts.upstreamKind.hint.${kind}`)
  })
})

describe('formatUpstreamAmount', () => {
  it('always shows two decimals', () => {
    expect(formatUpstreamAmount(5, 'USD', 'en')).toBe('$5.00')
    expect(formatUpstreamAmount(5.1234, 'USD', 'en')).toBe('$5.12')
    expect(formatUpstreamAmount(1234567.89, 'USD', 'en')).toBe('$1,234,567.89')
    expect(formatUpstreamAmount(0, 'USD', 'en')).toBe('$0.00')
  })

  it('does not round a non-empty balance down to zero', () => {
    expect(formatUpstreamAmount(0.0001, 'USD', 'en')).toBe('< $0.01')
  })

  it('keeps the sign of a negative balance and does not invent a currency', () => {
    expect(formatUpstreamAmount(-3.2, 'USD', 'en')).toBe('-$3.20')
    expect(formatUpstreamAmount(12, undefined, 'en')).toBe('12.00')
    expect(formatUpstreamAmount(12, 'CNY', 'en')).toBe('12.00 CNY')
  })
})

describe('UpstreamBalanceCell', () => {
  it('shows the key remaining quota as the headline with tabular figures', () => {
    const wrapper = mountCell(makeAccount({ upstream_kind: 'sub2api', balance: ok({ unit: 'USD', remaining: 128.4, quota_limit: 200, quota_used: 71.6 }) }))
    const value = wrapper.get('[data-testid="upstream-balance-value"]')
    expect(value.text()).toBe('$128.40')
    expect(value.classes()).toContain('tabular-nums')
    expect(wrapper.get('[data-testid="upstream-balance-subline"]').text()).toBe('admin.accounts.upstreamBalance.usedPercent:36')
    expect(wrapper.get('[data-testid="upstream-balance-cell"]').attributes('data-balance-state')).toBe('value')
  })

  it('never shows an unlimited key as zero', () => {
    const account = makeAccount({ upstream_kind: 'newapi', balance: ok({ unit: 'USD', unit_assumed: true, unlimited: true, quota_used: 3 }, 'newapi_token_usage') })
    expect(upstreamBalanceView(account, NOW).state).toBe('unlimited')
    const text = headline(account)
    expect(text).toBe('admin.accounts.upstreamBalance.unlimited')
    expect(text).not.toMatch(/0\.00/)
  })

  it('shows the spend and says the balance was not provided when only used is known', () => {
    const account = makeAccount({
      upstream_kind: 'newapi',
      balance: {
        status: 'unsupported',
        data: { is_valid: true, unit: 'USD', quota_used: 53035.92, balance_unavailable: true },
        last_attempt_at: iso(0),
        http_status: 404,
        last_error: 'unsupported'
      }
    })
    const view = upstreamBalanceView(account, NOW)
    expect(view.state).toBe('used_only')
    expect(view.used).toBe(53035.92)
    const wrapper = mountCell(account)
    expect(wrapper.get('[data-testid="upstream-balance-value"]').text()).toBe('admin.accounts.upstreamBalance.notProvided')
    expect(wrapper.get('[data-testid="upstream-balance-subline"]').text()).toBe('admin.accounts.upstreamBalance.usedShort:$53,035.92')
    // The 1e8 sentinel must never reappear as an "unlimited" headline.
    expect(wrapper.text()).not.toContain('admin.accounts.upstreamBalance.unlimited')
    expect(wrapper.get('[data-testid="upstream-balance-note"]').text()).toBe('admin.accounts.upstreamBalance.notProvidedNote')
  })

  it('keeps the account wallet and the key quota as two separate numbers', () => {
    const account = makeAccount({ upstream_kind: 'newapi', balance: ok({ unit: 'USD', unit_assumed: true, remaining: 37.5, wallet_balance: 412.09 }, 'newapi_token_usage') })
    const wrapper = mountCell(account)
    expect(wrapper.get('[data-testid="upstream-balance-value"]').text()).toBe('$37.50')
    expect(wrapper.get('[data-testid="upstream-balance-subline"]').text()).toBe('admin.accounts.upstreamBalance.walletShort:$412.09')
    const detail = wrapper.get('[data-testid="upstream-balance-details"]')
    expect(detail.get('[data-detail="remaining"]').text()).toContain('$37.50')
    expect(detail.get('[data-detail="wallet"]').text()).toContain('$412.09')
    expect(wrapper.find('[data-testid="upstream-balance-wallet-note"]').exists()).toBe(true)
  })

  it('labels a wallet-only balance as the account balance', () => {
    const wrapper = mountCell(makeAccount({ upstream_kind: 'newapi', balance: ok({ unit: 'USD', unit_assumed: true, wallet_balance: 9.87 }, 'newapi_token_usage') }))
    expect(wrapper.get('[data-testid="upstream-balance-value"]').text()).toBe('$9.87')
    expect(wrapper.get('[data-testid="upstream-balance-subline"]').text()).toBe('admin.accounts.upstreamBalance.walletOnlyShort')
  })

  it('explains amounts converted with the default new-api ratio', () => {
    const wrapper = mountCell(makeAccount({ upstream_kind: 'newapi', balance: ok({ unit: 'USD', unit_assumed: true, remaining: 1 }, 'newapi_token_usage') }))
    expect(wrapper.get('[data-testid="upstream-balance-unit-assumed"]').text()).toBe('admin.accounts.upstreamBalance.unitAssumed')
    const plain = mountCell(makeAccount({ upstream_kind: 'sub2api', balance: ok({ unit: 'USD', remaining: 1 }) }))
    expect(plain.find('[data-testid="upstream-balance-unit-assumed"]').exists()).toBe(false)
  })

  it('marks a value past fresh_until as old data', () => {
    const account = makeAccount({ upstream_kind: 'sub2api', balance: { ...ok({ unit: 'USD', remaining: 5 }), received_at: iso(-3 * 3_600_000), fresh_until: iso(-60_000) } })
    const view = upstreamBalanceView(account, NOW)
    expect(view.stale).toBe(true)
    expect(view.lastReadFailed).toBe(false)
    const wrapper = mountCell(account)
    expect(wrapper.get('[data-testid="upstream-balance-subline"]').text()).toBe('admin.accounts.upstreamBalance.staleShort:admin.accounts.upstreamBilling.hoursAgo:3')
    expect(wrapper.get('[data-testid="upstream-balance-note"]').text()).toBe('admin.accounts.upstreamBalance.staleNote')
  })

  it('keeps the last value when the latest read failed, and shows why', () => {
    const account = makeAccount({
      upstream_kind: 'sub2api',
      status: 'failed',
      balance: { status: 'failed', data: { unit: 'USD', remaining: 5.1234 }, received_at: iso(-3_600_000), fresh_until: iso(3_600_000), last_attempt_at: iso(-60_000), http_status: 502, last_error: 'http_error', source: 'sub2api_usage' }
    })
    const wrapper = mountCell(account)
    expect(wrapper.get('[data-testid="upstream-balance-value"]').text()).toBe('$5.12')
    expect(wrapper.get('[data-testid="upstream-balance-subline"]').text()).toBe('admin.accounts.upstreamBalance.lastReadFailedShort')
    const detail = wrapper.get('[data-testid="upstream-balance-details"]')
    expect(detail.get('[data-detail="reason"]').text()).toContain('admin.accounts.upstreamBalance.failure.http_error')
    expect(detail.get('[data-detail="httpStatus"]').text()).toContain('502')
    expect(detail.get('[data-detail="source"]').text()).toContain('admin.accounts.upstreamBalance.source.sub2api_usage')
  })

  it('shows a failure without an earlier value as a failure, not a blank', () => {
    const account = makeAccount({ status: 'failed', balance: { status: 'failed', last_attempt_at: iso(-60_000), last_error: 'request_failed' } })
    expect(headline(account)).toBe('admin.accounts.upstreamBalance.failed')
    expect(details(account).get('[data-detail="reason"]').text()).toContain('admin.accounts.upstreamBalance.failure.request_failed')
  })

  it('distinguishes unsupported, official and not-read', () => {
    expect(headline(makeAccount({ upstream_kind: 'custom', balance: { status: 'unsupported', last_attempt_at: iso(0), http_status: 404, last_error: 'unsupported' } })))
      .toBe('admin.accounts.upstreamBalance.unsupported')
    expect(headline(makeAccount({ upstream_kind: 'official', status: 'unsupported' }))).toBe('admin.accounts.upstreamBalance.official')
    expect(headline(makeAccount())).toBe('admin.accounts.upstreamBalance.notRead')
    expect(headline(makeAccount({ upstream_kind: 'sub2api' }))).toBe('admin.accounts.upstreamBalance.notRead')
  })

  it('renders zero, tiny and negative balances without hiding them', () => {
    expect(headline(makeAccount({ upstream_kind: 'sub2api', balance: ok({ unit: 'USD', remaining: 0 }) }))).toBe('$0.00')
    expect(headline(makeAccount({ upstream_kind: 'sub2api', balance: ok({ unit: 'USD', remaining: 0.0001 }) }))).toBe('< $0.01')
    const negative = mountCell(makeAccount({ upstream_kind: 'sub2api', balance: ok({ unit: 'USD', remaining: -3.2 }) }))
    expect(negative.get('[data-testid="upstream-balance-value"]').text()).toBe('-$3.20')
  })

  it('shows nothing interactive for accounts that are not API keys', () => {
    const wrapper = mountCell(makeAccount(undefined, 'oauth'))
    expect(wrapper.find('[data-testid="upstream-balance-na"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="upstream-balance-trigger"]').exists()).toBe(false)
  })

  it('emits probe from the refresh button', async () => {
    const wrapper = mountCell(makeAccount())
    await wrapper.get('[data-testid="upstream-balance-probe"]').trigger('click')
    expect(wrapper.emitted('probe')).toHaveLength(1)
  })

  // The refresh button used to be a plain <button> with no type, so inside any
  // surrounding form it defaulted to type=submit and reloaded the page. It must
  // stay a non-submitting button and swallow the default action.
  it('does not submit a surrounding form or navigate when refresh is clicked', () => {
    const form = document.createElement('form')
    const onSubmit = vi.fn((event: Event) => event.preventDefault())
    form.addEventListener('submit', onSubmit)
    document.body.appendChild(form)
    const wrapper = mount(UpstreamBalanceCell, {
      props: { account: makeAccount(), now: NOW },
      attachTo: form,
      global: { stubs: { UIcon: true, HelpTooltip: HelpTooltipStub, UButton: { template: '<button v-bind="$attrs" />' } } }
    })
    try {
      const button = wrapper.get('[data-testid="upstream-balance-probe"]')
      expect(button.attributes('type')).toBe('button')
      const click = new MouseEvent('click', { bubbles: true, cancelable: true })
      button.element.dispatchEvent(click)
      expect(click.defaultPrevented).toBe(true)
      expect(wrapper.emitted('probe')).toHaveLength(1)
      expect(onSubmit).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
      form.remove()
    }
  })
})
