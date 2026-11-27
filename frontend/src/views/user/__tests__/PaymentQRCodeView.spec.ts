import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))
const routerPush = vi.hoisted(() => vi.fn())
const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const qrToCanvas = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush }),
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

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    cancelOrder,
  },
}))

vi.mock('qrcode', () => ({
  default: {
    toCanvas: qrToCanvas,
  },
}))

import PaymentQRCodeView from '../PaymentQRCodeView.vue'

function mountQRCodeView() {
  return mount(PaymentQRCodeView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        UserPageHeader: { template: '<div />' },
        UIcon: true,
        UButton: {
          props: ['href', 'target', 'rel', 'disabled'],
          template: '<a v-if="href" :href="href" :target="target" :rel="rel"><slot /></a><button v-else :disabled="disabled"><slot /></button>',
        },
      },
    },
  })
}

const recoverySnapshotFactory = (orderId: number, payUrl: string, qrCode = '') => ({
  orderId,
  amount: 50,
  qrCode,
  expiresAt: '2099-01-01T00:30:00.000Z',
  paymentType: 'alipay',
  payUrl,
  outTradeNo: 'sub2_qr_42',
  clientSecret: '',
  intentId: '',
  currency: 'CNY',
  countryCode: '',
  paymentEnv: '',
  payAmount: 50,
  orderType: 'balance',
  paymentMode: 'redirect',
  resumeToken: 'resume-42',
  createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
})

describe('PaymentQRCodeView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    routeState.query = {}
    routerPush.mockReset()
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    showError.mockReset()
    qrToCanvas.mockClear()
    window.localStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it.each([
    ['javascript scheme', 'javascript:alert(1)'],
    ['data scheme', 'data:text/html,<script>alert(1)</script>'],
    ['arbitrary external https without snapshot', 'https://evil.example/fake-checkout'],
    ['protocol-relative url', '//evil.example/fake-checkout'],
    ['external http url even with snapshot', 'http://pay.example.com/session/42'],
    ['url with credentials', 'https://user:pass@pay.example.com/session/42'],
    ['backslash injection', '/payment/\\evil.example'],
  ])('refuses to bind unsafe pay_url (%s)', async (_label, maliciousPayUrl) => {
    routeState.query = {
      order_id: '42',
      pay_url: maliciousPayUrl,
    }
    if (maliciousPayUrl.startsWith('http://')) {
      window.localStorage.setItem(
        PAYMENT_RECOVERY_STORAGE_KEY,
        JSON.stringify(recoverySnapshotFactory(42, maliciousPayUrl)),
      )
    }

    const wrapper = mountQRCodeView()
    await flushPromises()

    expect(wrapper.find('a').exists()).toBe(false)
    wrapper.unmount()
  })

  it('refuses mismatched external pay_url even when a local checkout snapshot exists', async () => {
    routeState.query = {
      order_id: '42',
      pay_url: 'https://evil.example/phish',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory(42, 'https://pay.trusted.example/session/42')),
    )

    const wrapper = mountQRCodeView()
    await flushPromises()

    expect(wrapper.find('a').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders external HTTPS pay_url when it matches the active checkout snapshot for the order', async () => {
    const trustedPayUrl = 'https://pay.trusted.example/session/42'
    routeState.query = {
      order_id: '42',
      pay_url: trustedPayUrl,
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory(42, trustedPayUrl)),
    )

    const wrapper = mountQRCodeView()
    await flushPromises()

    const link = wrapper.find('a')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe(trustedPayUrl)
    wrapper.unmount()
  })

  it('allows same-origin internal /payment/ pay_url paths', async () => {
    routeState.query = {
      order_id: '42',
      pay_url: '/payment/stripe?order_id=42',
    }

    const wrapper = mountQRCodeView()
    await flushPromises()

    const link = wrapper.find('a')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe(`${window.location.origin}/payment/stripe?order_id=42`)
    wrapper.unmount()
  })

  it('fails closed without crashing when localStorage throws SecurityError', async () => {
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('storage blocked', 'SecurityError')
    })
    routeState.query = {
      order_id: '42',
      pay_url: 'https://pay.trusted.example/session/42',
    }

    try {
      const wrapper = mountQRCodeView()
      await flushPromises()
      expect(wrapper.find('a').exists()).toBe(false)
      wrapper.unmount()
    } finally {
      getItemSpy.mockRestore()
    }
  })

  it('rejects arbitrary external qr query parameters when no matching recovery snapshot exists', async () => {
    routeState.query = {
      order_id: '42',
      qr: 'https://evil.example/phish-alipay-qr',
    }

    const wrapper = mountQRCodeView()
    await flushPromises()

    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(qrToCanvas).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects mismatched qr query parameters or wrong orderId even when a local checkout snapshot exists', async () => {
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(
        recoverySnapshotFactory(42, 'https://pay.trusted.example/session/42', 'weixin://wxpay/bizpayurl?pr=trusted-42'),
      ),
    )

    routeState.query = {
      order_id: '42',
      qr: 'weixin://wxpay/bizpayurl?pr=attacker-phish',
    }
    const mismatchedWrapper = mountQRCodeView()
    await flushPromises()
    expect(mismatchedWrapper.find('canvas').exists()).toBe(false)
    expect(qrToCanvas).not.toHaveBeenCalled()
    mismatchedWrapper.unmount()

    routeState.query = {
      order_id: '99',
      qr: 'weixin://wxpay/bizpayurl?pr=trusted-42',
    }
    const wrongOrderWrapper = mountQRCodeView()
    await flushPromises()
    expect(wrongOrderWrapper.find('canvas').exists()).toBe(false)
    expect(qrToCanvas).not.toHaveBeenCalled()
    wrongOrderWrapper.unmount()
  })

  it('renders qr cleanly when it matches snapshot.qrCode or snapshot.payUrl for the order', async () => {
    const trustedQr = 'weixin://wxpay/bizpayurl?pr=trusted-42'
    const trustedPayUrl = 'https://pay.trusted.example/qr/42'
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory(42, trustedPayUrl, trustedQr)),
    )

    routeState.query = {
      order_id: '42',
      qr: trustedQr,
    }
    const qrWrapper = mountQRCodeView()
    await flushPromises()
    expect(qrWrapper.find('canvas').exists()).toBe(true)
    expect(qrToCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), trustedQr, expect.any(Object))
    qrWrapper.unmount()

    qrToCanvas.mockClear()
    routeState.query = {
      order_id: '42',
      qr: trustedPayUrl,
    }
    const payUrlQrWrapper = mountQRCodeView()
    await flushPromises()
    expect(payUrlQrWrapper.find('canvas').exists()).toBe(true)
    expect(qrToCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), trustedPayUrl, expect.any(Object))
    payUrlQrWrapper.unmount()
  })

  it('fails closed for qr without crashing when localStorage throws SecurityError', async () => {
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('storage blocked', 'SecurityError')
    })
    routeState.query = {
      order_id: '42',
      qr: 'weixin://wxpay/bizpayurl?pr=trusted-42',
    }

    try {
      const wrapper = mountQRCodeView()
      await flushPromises()
      expect(wrapper.find('canvas').exists()).toBe(false)
      expect(qrToCanvas).not.toHaveBeenCalled()
      wrapper.unmount()
    } finally {
      getItemSpy.mockRestore()
    }
  })

  it('rejects negative/non-integer order_id and array qr query values', async () => {
    const trustedQr = 'weixin://wxpay/bizpayurl?pr=trusted-42'
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory(42, 'https://pay.trusted.example/session/42', trustedQr)),
    )

    routeState.query = {
      order_id: '-5',
      qr: trustedQr,
    }
    const negWrapper = mountQRCodeView()
    await flushPromises()
    expect(negWrapper.find('canvas').exists()).toBe(false)
    expect(negWrapper.find('button').exists()).toBe(false)
    expect(qrToCanvas).not.toHaveBeenCalled()
    negWrapper.unmount()

    routeState.query = {
      order_id: '42',
      qr: ['https://evil.example/phish', trustedQr],
    }
    const arrWrapper = mountQRCodeView()
    await flushPromises()
    expect(arrWrapper.find('canvas').exists()).toBe(false)
    expect(qrToCanvas).not.toHaveBeenCalled()
    arrWrapper.unmount()
  })
})
