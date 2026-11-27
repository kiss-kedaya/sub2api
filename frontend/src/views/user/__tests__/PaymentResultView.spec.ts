import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))

const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const pollOrderStatus = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const verifyOrderPublic = vi.hoisted(() => vi.fn())
const resolveOrderPublicByResumeToken = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  isAuthenticated: true,
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush, replace: routerReplace }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    pollOrderStatus,
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isAuthenticated() {
      return authState.isAuthenticated
    },
    refreshUser,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    verifyOrder,
    verifyOrderPublic,
    resolveOrderPublicByResumeToken,
  },
}))

import PaymentResultView from '../PaymentResultView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'

const orderFactory = (status: string) => ({
  id: 42,
  user_id: 9,
  amount: 88,
  pay_amount: 88,
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260420abcd1234',
  status,
  order_type: 'balance',
  created_at: '2026-04-20T12:00:00Z',
  expires_at: '2026-04-20T12:30:00Z',
  refund_amount: 0,
})

const recoverySnapshotFactory = (resumeToken: string) => ({
  orderId: 42,
  amount: 88,
  qrCode: '',
  expiresAt: '2099-01-01T00:10:00.000Z',
  paymentType: 'alipay',
  payUrl: 'https://pay.example.com/session/42',
  outTradeNo: 'sub2_20260420abcd1234',
  clientSecret: '',
  intentId: '',
  currency: '',
  countryCode: '',
  paymentEnv: '',
  payAmount: 88,
  orderType: 'balance',
  paymentMode: 'popup',
  resumeToken,
  createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
})

describe('PaymentResultView', () => {
  beforeEach(() => {
    routeState.query = {}
    routerPush.mockReset()
    routerReplace.mockReset()
    pollOrderStatus.mockReset()
    verifyOrder.mockReset()
    verifyOrderPublic.mockReset()
    resolveOrderPublicByResumeToken.mockReset()
    refreshUser.mockReset()
    refreshUser.mockResolvedValue({})
    authState.isAuthenticated = true
    window.localStorage.clear()
    window.sessionStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders a pending state instead of a failure state when the restored order is still pending', async () => {
    routeState.query = {
      resume_token: 'resume-42',
      order_id: '999',
      status: 'success',
    }
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 42,
      amount: 88,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/42',
      outTradeNo: 'sub2_20260420abcd1234',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 88,
      orderType: 'balance',
      paymentMode: 'redirect',
      resumeToken: 'resume-42',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('PENDING'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-42')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('prefers the public resume-token result over a stale restored DB snapshot', async () => {
    routeState.query = {
      resume_token: 'resume-authoritative',
      order_id: '42',
      status: 'success',
    }
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 42,
      amount: 88,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/42',
      outTradeNo: 'sub2_20260420abcd1234',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 88,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-authoritative',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('COMPLETED'),
        amount: 100,
        pay_amount: 103,
        fee_rate: 3,
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-authoritative')
    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).toContain('103.00')
    expect(wrapper.text()).toContain('100.00')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('waits for completed fulfillment before refreshing the user balance', async () => {
    vi.useFakeTimers()
    routeState.query = {
      resume_token: 'resume-77',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-77')),
    )
    resolveOrderPublicByResumeToken
      .mockResolvedValueOnce({
        data: orderFactory('PENDING'),
      })
      .mockResolvedValueOnce({
        data: orderFactory('COMPLETED'),
      })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(1)
    expect(refreshUser).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(2)
    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('keeps the successful result when refreshing the user balance fails', async () => {
    routeState.query = {
      resume_token: 'resume-refresh-failure',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })
    refreshUser.mockRejectedValueOnce(new Error('profile refresh failed'))

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('falls back to order_id polling when resume-token recovery fails', async () => {
    routeState.query = {
      resume_token: 'resume-fail',
      order_id: '77',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify({
        ...recoverySnapshotFactory('resume-fail'),
        orderId: 42,
      }),
    )
    resolveOrderPublicByResumeToken.mockRejectedValueOnce(new Error('resume failed'))
    pollOrderStatus.mockResolvedValueOnce({
      ...orderFactory('PAID'),
      id: 77,
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-fail')
    expect(pollOrderStatus).toHaveBeenCalledWith(77)
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('falls back to public out_trade_no verification when resume_token recovery fails in legacy return flows', async () => {
    routeState.query = {
      resume_token: 'resume-fail',
      out_trade_no: 'legacy-should-not-run',
      trade_status: 'TRADE_SUCCESS',
    }
    resolveOrderPublicByResumeToken.mockRejectedValueOnce(new Error('resume failed'))
    verifyOrderPublic.mockResolvedValueOnce({
      data: {
        ...orderFactory('PAID'),
        out_trade_no: 'legacy-should-not-run',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-fail')
    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-should-not-run')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('ignores a stale global recovery snapshot when legacy return markers do not identify the order', async () => {
    routeState.query = {
      trade_status: 'TRADE_SUCCESS',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-stale')),
    )

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.failed')
    expect(wrapper.text()).not.toContain('sub2_20260420abcd1234')
  })

  it('uses public out_trade_no verification when no signed resume context is available', async () => {
    routeState.query = {
      out_trade_no: 'legacy-123',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockRejectedValue(new Error('auth required'))
    verifyOrderPublic.mockResolvedValue({
      data: orderFactory('PAID'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('legacy-123')
    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-123')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('renders the minimal public out_trade_no verification result without payment_type', async () => {
    routeState.query = {
      out_trade_no: 'legacy-minimal',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockRejectedValue(new Error('auth required'))
    verifyOrderPublic.mockResolvedValue({
      data: {
        out_trade_no: 'legacy-minimal',
        status: 'PAID',
        paid: true,
        created_at: '2026-04-20T12:00:00Z',
        expires_at: '2026-04-20T12:30:00Z',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).toContain('legacy-minimal')
    expect(wrapper.text()).not.toContain('payment.orders.paymentMethod')
  })

  it('prefers authenticated order verification before falling back to public lookup', async () => {
    routeState.query = {
      out_trade_no: 'auth-verify-123',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('auth-verify-123')
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('does not use public out_trade_no verification for bare order numbers without legacy return markers', async () => {
    routeState.query = {
      out_trade_no: 'legacy-bare',
    }

    mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrderPublic).not.toHaveBeenCalled()
  })

  it('resolves order by resume token when local recovery snapshot is missing', async () => {
    routeState.query = {
      resume_token: 'resume-77',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('PAID'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-77')
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('uses the currency returned by the order API when rendering amounts', async () => {
    routeState.query = {
      resume_token: 'resume-hkd',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('PAID'),
        currency: 'HKD',
        amount: 100,
        pay_amount: 103,
        fee_rate: 3,
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(103, 'HKD'))
  })

  it('normalizes aliased payment methods before rendering the label', async () => {
    routeState.query = {
      resume_token: 'resume-88',
    }
    resolveOrderPublicByResumeToken.mockResolvedValueOnce({
      data: {
        ...orderFactory('PAID'),
        payment_type: 'alipay_direct',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.methods.alipay')
    expect(wrapper.text()).not.toContain('payment.methods.alipay_direct')
  })

  it('strips resume_token from the URL via router.replace while preserving order_id, out_trade_no, and status', async () => {
    routeState.query = {
      resume_token: 'sensitive-resume-token',
      order_id: '42',
      out_trade_no: 'sub2_20260420abcd1234',
      status: 'success',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(routerReplace).toHaveBeenCalledTimes(1)
    expect(routerReplace).toHaveBeenCalledWith({
      query: {
        order_id: '42',
        out_trade_no: 'sub2_20260420abcd1234',
        status: 'success',
      },
    })
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('sensitive-resume-token')
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('does not call router.replace when no resume_token is present in the URL', async () => {
    routeState.query = {
      order_id: '42',
      status: 'success',
    }
    pollOrderStatus.mockResolvedValue(orderFactory('COMPLETED'))

    mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(routerReplace).not.toHaveBeenCalled()
  })

  it('scrubs duplicated array resume_token query values and resolves the non-empty token', async () => {
    routeState.query = {
      resume_token: ['', 'sensitive-duplicate-token'],
      order_id: '42',
      status: 'success',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(routerReplace).toHaveBeenCalledTimes(1)
    expect(routerReplace).toHaveBeenCalledWith({
      query: {
        order_id: '42',
        status: 'success',
      },
    })
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('sensitive-duplicate-token')
  })

  it('resolves order cleanly even when localStorage throws SecurityError', async () => {
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('storage blocked', 'SecurityError')
    })
    const removeItemSpy = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new DOMException('storage blocked', 'SecurityError')
    })
    routeState.query = {
      resume_token: 'resume-storage-blocked',
      order_id: '42',
      status: 'success',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    try {
      const wrapper = mount(PaymentResultView, {
        global: {
          stubs: {
            OrderStatusBadge: true,
          },
        },
      })

      await flushPromises()

      expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-storage-blocked')
      expect(wrapper.text()).toContain('payment.result.success')
    } finally {
      getItemSpy.mockRestore()
      removeItemSpy.mockRestore()
    }
  })

  it('survives unauthenticated F5 reload after scrubbing resume_token without calling authenticated endpoints', async () => {
    authState.isAuthenticated = false
    routeState.query = {
      resume_token: 'cross-origin-resume-42',
      order_id: '42',
      out_trade_no: 'sub2_20260420abcd1234',
      status: 'success',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const firstMount = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledTimes(1)
    expect(routerReplace).toHaveBeenCalledWith({
      query: {
        order_id: '42',
        out_trade_no: 'sub2_20260420abcd1234',
        status: 'success',
      },
    })
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('cross-origin-resume-42')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(refreshUser).not.toHaveBeenCalled()
    firstMount.unmount()

    // Simulate F5 reload after router.replace scrubbed resume_token from the URL.
    routerReplace.mockClear()
    resolveOrderPublicByResumeToken.mockClear()
    routeState.query = {
      order_id: '42',
      out_trade_no: 'sub2_20260420abcd1234',
      status: 'success',
    }

    const reloadMount = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(1)
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('cross-origin-resume-42')
    expect(routerReplace).not.toHaveBeenCalled()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(refreshUser).not.toHaveBeenCalled()
    expect(reloadMount.text()).toContain('payment.result.success')
    reloadMount.unmount()
  })

  it('survives F5 reload on a token-only URL using sessionStorage while keeping the URL scrubbed', async () => {
    authState.isAuthenticated = false
    routeState.query = {
      resume_token: 'token-only-resume-99',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const firstMount = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledTimes(1)
    expect(routerReplace).toHaveBeenCalledWith({ query: {} })
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('token-only-resume-99')
    firstMount.unmount()

    // Simulate F5 reload on the scrubbed empty-query URL.
    routerReplace.mockClear()
    resolveOrderPublicByResumeToken.mockClear()
    routeState.query = {}

    const reloadMount = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(1)
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('token-only-resume-99')
    expect(routerReplace).not.toHaveBeenCalled()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(reloadMount.text()).toContain('payment.result.success')
    reloadMount.unmount()
  })

  it('skips authenticated order endpoints and falls back directly to verifyOrderPublic when unauthenticated without resume_token', async () => {
    authState.isAuthenticated = false
    routeState.query = {
      order_id: '42',
      out_trade_no: 'sub2_unauth_reload',
      status: 'success',
    }
    verifyOrderPublic.mockResolvedValue({
      data: {
        out_trade_no: 'sub2_unauth_reload',
        status: 'COMPLETED',
        paid: true,
        created_at: '2026-04-20T12:00:00Z',
        expires_at: '2026-04-20T12:30:00Z',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })
    await flushPromises()

    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(refreshUser).not.toHaveBeenCalled()
    expect(verifyOrderPublic).toHaveBeenCalledWith('sub2_unauth_reload')
    expect(wrapper.text()).toContain('payment.result.success')
    wrapper.unmount()
  })

  it('does not leak a stashed token-only resume_token to a different order or legacy return query', async () => {
    authState.isAuthenticated = false
    routeState.query = {
      resume_token: 'token-only-secret',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const firstMount = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })
    await flushPromises()
    firstMount.unmount()

    resolveOrderPublicByResumeToken.mockClear()
    routeState.query = {
      order_id: '99',
    }
    const otherOrderMount = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })
    await flushPromises()
    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    otherOrderMount.unmount()

    routeState.query = {
      trade_status: 'TRADE_SUCCESS',
    }
    const legacyMount = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })
    await flushPromises()
    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    legacyMount.unmount()
  })

  it('resolves cleanly when sessionStorage.setItem throws SecurityError', async () => {
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('storage blocked', 'SecurityError')
    })
    authState.isAuthenticated = false
    routeState.query = {
      resume_token: 'resume-session-blocked',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    try {
      const wrapper = mount(PaymentResultView, {
        global: { stubs: { OrderStatusBadge: true } },
      })
      await flushPromises()
      expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-session-blocked')
      expect(wrapper.text()).toContain('payment.result.success')
      wrapper.unmount()
    } finally {
      setItemSpy.mockRestore()
    }
  })
})
