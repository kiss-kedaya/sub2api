import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import RedeemView from '../RedeemView.vue'

const { redeem, getHistory, refreshUser, fetchActiveSubscriptions, showError, showWarning, showSuccess, authStoreState } = vi.hoisted(() => ({
  redeem: vi.fn(),
  getHistory: vi.fn(),
  refreshUser: vi.fn(),
  fetchActiveSubscriptions: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
  showSuccess: vi.fn(),
  authStoreState: { instance: null as any },
}))

vi.mock('@/api', () => ({
  redeemAPI: { redeem, getHistory },
  authAPI: { getPublicSettings: vi.fn().mockResolvedValue({}) },
}))
vi.mock('@/stores/auth', async () => {
  const { reactive } = await import('vue')
  const state = reactive({ user: { balance: 10, concurrency: 2 }, refreshUser })
  authStoreState.instance = state
  return { useAuthStore: () => state }
})
vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({ fetchActiveSubscriptions }),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showWarning, showSuccess }),
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const mountedWrappers: VueWrapper[] = []
const mountRedeemView = () => {
  const wrapper = mount(RedeemView, {
    attachTo: document.body,
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      Icon: true,
      UModal: {
        props: ['open', 'title', 'description'],
        template: `
          <section v-if="open" data-testid="redeem-result-modal">
            <h2>{{ title }}</h2>
            <p>{{ description }}</p>
            <slot name="content" />
          </section>
        `,
      },
    },
    },
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

async function submitCode() {
  const wrapper = mountRedeemView()
  await flushPromises()
  await wrapper.get('input#code').setValue(' REDEEM-CODE ')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  return wrapper
}

describe('RedeemView refresh after redemption', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    if (authStoreState.instance) authStoreState.instance.user = { balance: 10, concurrency: 2 }
    redeem.mockResolvedValue({ type: 'balance', value: 20, id: 1, code: 'REDEEM-CODE', status: 'used' })
    getHistory.mockResolvedValue({ items: [], total: 0 })
    refreshUser.mockImplementation(async () => {
      const refreshed = { ...authStoreState.instance.user, balance: 30, concurrency: 2 }
      authStoreState.instance.user = refreshed
      return refreshed
    })
    fetchActiveSubscriptions.mockResolvedValue([])
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    for (const wrapper of mountedWrappers.splice(0)) wrapper.unmount()
    vi.restoreAllMocks()
  })

  it.each(['balance', 'concurrency', 'subscription'])(
    'keeps a successful %s redemption when profile refresh fails', async (type) => {
      redeem.mockResolvedValue({ type, value: 20, id: 1, code: 'REDEEM-CODE', status: 'used' })
      refreshUser.mockRejectedValue({ status: 503, message: 'Service unavailable' })
      getHistory.mockResolvedValueOnce({ items: [], total: 0 }).mockResolvedValueOnce({ total: 1, items: [{
        id: 1, code: 'REDEEM-CODE', type, value: 20, used_at: '2026-03-08T00:00:00Z',
      }] })

      const wrapper = await submitCode()

      expect(redeem).toHaveBeenCalledWith('REDEEM-CODE')
      expect(showError).not.toHaveBeenCalled()
      if (type === 'subscription') {
        expect(refreshUser).not.toHaveBeenCalled()
        expect(showWarning).not.toHaveBeenCalledWith('redeem.userRefreshFailed')
      } else {
        expect(showWarning).toHaveBeenCalledWith('redeem.userRefreshFailed')
      }
      expect(showSuccess).toHaveBeenCalledWith('redeem.codeRedeemSuccess')
      expect(document.body.textContent).toContain('redeem.redeemSuccess')
      expect(document.body.textContent).not.toContain('redeem.failedToRedeem')
      expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe('')
      expect((wrapper.get('input#code').element as HTMLInputElement).disabled).toBe(false)
      expect(getHistory).toHaveBeenCalledTimes(2)
      expect(wrapper.text()).toContain('REDEEM-C...')
      if (type === 'subscription') {
        expect(fetchActiveSubscriptions).toHaveBeenCalledWith(true)
      } else {
        expect(fetchActiveSubscriptions).not.toHaveBeenCalled()
      }
      wrapper.unmount()
    }
  )

  it('submits one redemption flight for rapid duplicate submits and opens the typed result modal', async () => {
    let resolveRedeem!: (value: unknown) => void
    redeem.mockImplementationOnce(() => new Promise((resolve) => { resolveRedeem = resolve }))
    const wrapper = mountRedeemView()
    await flushPromises()
    await wrapper.get('input#code').setValue('REDEEM-CODE')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(redeem).toHaveBeenCalledTimes(1)
    expect((wrapper.get('input#code').element as HTMLInputElement).disabled).toBe(true)
    expect(wrapper.get('[data-testid="redeem-submit"]').attributes('disabled')).toBeDefined()

    resolveRedeem({ type: 'concurrency', value: 3, id: 1, code: 'REDEEM-CODE', status: 'used' })
    await flushPromises()

    expect(document.body.querySelector('[data-testid="redeem-result-modal-content"]')).not.toBeNull()
    expect(document.body.textContent).toContain('redeem.concurrencyChange')
    expect(document.body.textContent).toContain('+3')
    expect(document.body.textContent).toContain('redeem.newConcurrency')
    expect(authStoreState.instance.user.concurrency).toBe(5)
    expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe('')
    expect(refreshUser).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('labels a negative concurrency result as a reduction rather than a cash credit', async () => {
    redeem.mockResolvedValue({ type: 'concurrency', value: -1, id: 1, code: 'REDEEM-CODE', status: 'used' })
    refreshUser.mockResolvedValue({ balance: 10, concurrency: 1 })
    const wrapper = await submitCode()

    expect(document.body.textContent).toContain('redeem.concurrencyReduced')
    expect(document.body.textContent).toContain('-1')
    expect(document.body.textContent).toContain('redeem.newConcurrency')
    expect(document.body.textContent).toContain('1')
    expect(document.body.textContent).not.toContain('redeem.balanceChange')
    expect(document.body.textContent).not.toContain('$20.00')
    wrapper.unmount()
  })

  it('pages on the server, changes size, and resets page and total after redeeming', async () => {
    getHistory.mockResolvedValue({ items: [], total: 101 })
    const wrapper = mountRedeemView()
    await flushPromises()
    const button = (text: string) => wrapper.findAll('button').find(b => b.text() === text)!
    expect(getHistory).toHaveBeenLastCalledWith(1, 20)
    expect(button('pagination.previous').attributes('disabled')).toBeDefined()
    await button('pagination.next').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(2, 20)
    await button('pagination.previous').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(1, 20)
    expect(wrapper.findAll('select option').map(o => o.text())).toEqual(['20', '50', '100'])
    await wrapper.get('select').setValue('50')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(1, 50)
    await wrapper.get('select').setValue('100')
    await flushPromises()
    await button('pagination.next').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(2, 100)
    expect(button('pagination.next').attributes('disabled')).toBeDefined()
    getHistory.mockResolvedValue({ items: [], total: 102 })
    await wrapper.get('input#code').setValue('NEW-CODE')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(1, 100)
    expect(wrapper.text()).toContain('102')
    expect(button('pagination.previous').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('restores the loaded size and keeps rows and navigation usable after a size request fails', async () => {
    const item = { id: 1, code: 'OLD-ROWS', type: 'balance', value: 20, used_at: '2026-03-08T00:00:00Z' }
    getHistory.mockResolvedValue({ items: [item], total: 61 })
    const wrapper = mountRedeemView()
    await flushPromises()
    const button = (text: string) => wrapper.findAll('button').find(b => b.text() === text)!
    await button('pagination.next').trigger('click')
    await flushPromises()
    let rejectRequest!: (error: Error) => void
    getHistory.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectRequest = reject }))
    await wrapper.get('select').setValue('50')
    expect(getHistory).toHaveBeenLastCalledWith(1, 50)
    expect(button('pagination.next').attributes('disabled')).toBeDefined()
    rejectRequest(new Error('Network error'))
    await flushPromises()
    expect(wrapper.get('select').element.value).toBe('20')
    expect(wrapper.text()).toContain('OLD-ROWS')
    expect(wrapper.text()).toContain('61')
    expect(button('pagination.previous').attributes('disabled')).toBeUndefined()
    expect(button('pagination.next').attributes('disabled')).toBeUndefined()
    expect(showError).toHaveBeenCalledWith('redeem.historyLoadFailed')
    await button('pagination.next').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(3, 20)
    await wrapper.get('select').setValue('50')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(1, 50)
    expect(wrapper.get('select').element.value).toBe('50')
    expect(button('pagination.previous').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it.each(['success', 'failure'])('ignores a stale history %s after a newer size request succeeds', async (outcome) => {
    let resolveOld!: (value: unknown) => void
    let rejectOld!: (error: Error) => void
    getHistory.mockImplementationOnce(() => new Promise((resolve, reject) => {
      resolveOld = resolve
      rejectOld = reject
    }))
    const wrapper = mountRedeemView()
    getHistory.mockResolvedValue({ items: [{
      id: 2, code: 'NEW-ROWS', type: 'balance', value: 30, used_at: '2026-03-08T00:00:00Z',
    }], total: 61 })
    // Force overlapping requests to exercise responses arriving out of order.
    await wrapper.get('select').setValue('50')
    await flushPromises()
    const button = (text: string) => wrapper.findAll('button').find(b => b.text() === text)!
    await button('pagination.next').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(2, 50)
    if (outcome === 'success') {
      resolveOld({ items: [], total: 0 })
    } else {
      rejectOld(new Error('Stale network error'))
    }
    await flushPromises()
    expect(wrapper.get('select').element.value).toBe('50')
    expect(wrapper.text()).toContain('NEW-ROWS')
    expect(wrapper.text()).toContain('61')
    expect(button('pagination.previous').attributes('disabled')).toBeUndefined()
    expect(button('pagination.next').attributes('disabled')).toBeDefined()
    expect(wrapper.get('select').attributes('disabled')).toBeUndefined()
    expect(showError).not.toHaveBeenCalled()
    await button('pagination.previous').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(1, 50)
    wrapper.unmount()
  })

  it('disables both paging buttons for empty history', async () => {
    const wrapper = await submitCode()
    for (const button of wrapper.findAll('button').filter(b => b.text().startsWith('pagination.'))) {
      expect(button.attributes('disabled')).toBeDefined()
    }
    wrapper.unmount()
  })

  it('finishes normally without a warning when profile refresh succeeds', async () => {
    const wrapper = await submitCode()

    expect(refreshUser).toHaveBeenCalledOnce()
    expect(showWarning).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledWith('redeem.codeRedeemSuccess')
    expect(document.body.textContent).toContain('redeem.balanceChange')
    expect(document.body.textContent).toContain('+$20.00')
    expect(document.body.textContent).toContain('$30.00')
    expect(authStoreState.instance.user.balance).toBe(30)
    expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })

  it('preserves the existing subscription refresh warning after successful redemption', async () => {
    redeem.mockResolvedValue({ type: 'subscription', value: 20, id: 1, code: 'REDEEM-CODE', status: 'used' })
    fetchActiveSubscriptions.mockRejectedValue(new Error('Network Error'))
    const wrapper = await submitCode()

    expect(showWarning).toHaveBeenCalledWith('redeem.subscriptionRefreshFailed')
    expect(showError).not.toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledWith('redeem.codeRedeemSuccess')
    expect(getHistory).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('reports subscription group and duration without presenting a cash amount', async () => {
    redeem.mockResolvedValue({
      type: 'subscription', value: 14, validity_days: 14, group_id: 42,
      id: 1, code: 'REDEEM-CODE', status: 'used',
    })
    const wrapper = await submitCode()

    expect(document.body.textContent).toContain('redeem.subscriptionAssigned')
    expect(document.body.textContent).toContain('redeem.subscriptionGroup #42')
    expect(document.body.textContent).toContain('redeem.subscriptionDays')
    expect(document.body.textContent).not.toContain('redeem.balanceChange')
    expect(document.body.textContent).not.toContain('$20.00')
    expect(fetchActiveSubscriptions).toHaveBeenCalledWith(true)
    wrapper.unmount()
  })

  it('reports a negative balance redemption as a deduction and reflects its applied balance delta', async () => {
    redeem.mockResolvedValue({ type: 'balance', value: -2.5, id: 1, code: 'REDEEM-CODE', status: 'used' })
    refreshUser.mockResolvedValue({ balance: 7.5, concurrency: 2 })
    const wrapper = await submitCode()

    expect(document.body.textContent).toContain('redeem.balanceDeducted')
    expect(document.body.textContent).toContain('-$2.50')
    expect(document.body.textContent).toContain('$7.50')
    expect(document.body.textContent).not.toContain('redeem.balanceChange')
    wrapper.unmount()
  })

  it('shows the locally reconciled balance immediately while the profile refresh is still pending', async () => {
    let resolveRefresh!: (value: unknown) => void
    refreshUser.mockImplementationOnce(() => new Promise((resolve) => { resolveRefresh = resolve }))
    const wrapper = await submitCode()

    expect(wrapper.get('[data-testid="redeem-result-modal"]').exists()).toBe(true)
    expect(document.body.textContent).toContain('$30.00')
    expect(authStoreState.instance.user.balance).toBe(30)

    resolveRefresh({ balance: 30, concurrency: 2 })
    await flushPromises()
    expect(document.body.textContent).toContain('$30.00')
    wrapper.unmount()
  })

  it('keeps the code and reports failure when the redemption request itself fails', async () => {
    redeem.mockRejectedValue({ status: 400, message: 'Invalid code' })
    const wrapper = await submitCode()

    expect(showError).toHaveBeenCalledWith('redeem.redeemFailed')
    expect(wrapper.text()).toContain('redeem.failedToRedeem')
    expect(wrapper.text()).not.toContain('Code applied')
    expect(document.body.querySelector('[data-testid="redeem-result-modal-content"]')).toBeNull()
    expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe(' REDEEM-CODE ')
    expect(refreshUser).not.toHaveBeenCalled()
    expect(fetchActiveSubscriptions).not.toHaveBeenCalled()
    expect(getHistory).toHaveBeenCalledOnce()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showWarning).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the layout reserved and allows a retry after a failed redemption', async () => {
    redeem.mockRejectedValueOnce({ response: { data: { detail: 'Invalid code' } } })
      .mockResolvedValueOnce({ type: 'balance', value: 5, id: 1, code: 'REDEEM-CODE', status: 'used' })
    const wrapper = mountRedeemView()
    await flushPromises()
    const errorRegion = wrapper.get('[aria-live="polite"]')
    const reservedHeightClass = errorRegion.classes().find((name) => name.startsWith('min-h-'))
    await wrapper.get('input#code').setValue('REDEEM-CODE')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(redeem).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[aria-live="polite"]').classes()).toContain(reservedHeightClass)
    expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe('REDEEM-CODE')
    expect(document.body.querySelector('[data-testid="redeem-result-modal-content"]')).toBeNull()

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(redeem).toHaveBeenCalledTimes(2)
    expect(document.body.querySelector('[data-testid="redeem-result-modal-content"]')).not.toBeNull()
    expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })

  it('keeps a confirmed redemption successful when only the history refresh fails', async () => {
    getHistory.mockResolvedValueOnce({ items: [], total: 0 }).mockRejectedValueOnce(new Error('History unavailable'))
    const wrapper = await submitCode()

    expect(document.body.querySelector('[data-testid="redeem-result-modal-content"]')).not.toBeNull()
    expect((wrapper.get('input#code').element as HTMLInputElement).value).toBe('')
    expect(showSuccess).toHaveBeenCalledWith('redeem.codeRedeemSuccess')
    expect(showError).toHaveBeenCalledWith('redeem.historyLoadFailed')
    expect(showError).not.toHaveBeenCalledWith('redeem.redeemFailed')
    wrapper.unmount()
  })
})
