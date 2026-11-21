import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

const { getIPWhitelistAssistMock } = vi.hoisted(() => ({
  getIPWhitelistAssistMock: vi.fn()
}))

vi.mock('@/api/keys', () => ({
  getIPWhitelistAssist: getIPWhitelistAssistMock
}))

const messages: Record<string, string> = {
  'common.cancel': '取消',
  'keys.ipWhitelistAssist.fillCurrent': '填入当前 IP',
  'keys.ipWhitelistAssist.commonIPs': '常用 IP',
  'keys.ipWhitelistAssist.loading': '加载中…',
  'keys.ipWhitelistAssist.loadFailed': '获取 IP 信息失败，请稍后重试',
  'keys.ipWhitelistAssist.noCurrentIP': '未能识别当前 IP，请手动填写',
  'keys.ipWhitelistAssist.confirmHint': '确认前请先确认这是你自己的出口 IP',
  'keys.ipWhitelistAssist.confirm': '确认使用这个 IP',
  'keys.ipWhitelistAssist.confirmCounting': '确认使用这个 IP ({seconds})',
  'keys.ipWhitelistAssist.countdownHint': '倒计时结束后才能确认',
  'keys.ipWhitelistAssist.hostingWarning': '这个 IP 像机房/代理，不是你家宽带，填错会把自己锁在外面',
  'keys.ipWhitelistAssist.unknownRegion': '地区未知',
  'keys.ipWhitelistAssist.recentHint': '最近 30 天使用过的 IP',
  'keys.ipWhitelistAssist.recentEmpty': '最近没有可用的 IP 记录',
  'keys.ipWhitelistAssist.usedCount': '使用 {count} 次',
  'keys.ipWhitelistAssist.added': '已填入 {ip}',
  'keys.ipWhitelistAssist.duplicate': '已存在'
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        let text = messages[key] ?? key
        for (const [name, value] of Object.entries(params ?? {})) {
          text = text.replace(`{${name}}`, String(value))
        }
        return text
      }
    })
  }
})

import IpWhitelistAssist from '../IpWhitelistAssist.vue'

enableAutoUnmount(afterEach)

const GEO_SHENZHEN = {
  country: '中国',
  region: '广东',
  city: '深圳',
  isp: '中国电信',
  hosting: false
}

const EMPTY_GEO = { country: '', region: '', city: '', isp: '', hosting: false }

function mockAssist(overrides: Record<string, unknown> = {}) {
  getIPWhitelistAssistMock.mockResolvedValue({
    current_ip: '203.0.113.7',
    current_geo: GEO_SHENZHEN,
    recent_ips: [],
    ...overrides
  })
}

function mountAssist(modelValue = ''): VueWrapper {
  return mount(IpWhitelistAssist, { props: { modelValue } })
}

const confirmButton = (wrapper: VueWrapper) => wrapper.get('[data-test="ip-assist-confirm"]')

async function advance(seconds: number): Promise<void> {
  await vi.advanceTimersByTimeAsync(seconds * 1000)
  await nextTick()
}

describe('IpWhitelistAssist', () => {
  beforeEach(() => {
    // Only fake the countdown interval; flushPromises still needs real timers.
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    getIPWhitelistAssistMock.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows the current IP with its location and blocks confirm for 3 seconds', async () => {
    mockAssist()
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()

    const candidate = wrapper.get('[data-test="ip-assist-candidate"]').text()
    expect(candidate).toContain('203.0.113.7')
    expect(candidate).toContain('中国 广东 深圳')
    expect(candidate).toContain('中国电信')

    expect(confirmButton(wrapper).attributes('disabled')).toBeDefined()
    expect(confirmButton(wrapper).text()).toContain('确认使用这个 IP (3)')

    await advance(1)
    expect(confirmButton(wrapper).text()).toContain('确认使用这个 IP (2)')
    expect(confirmButton(wrapper).attributes('disabled')).toBeDefined()

    await advance(2)
    expect(confirmButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(confirmButton(wrapper).text()).toBe('确认使用这个 IP')

    await confirmButton(wrapper).trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['203.0.113.7']])
  })

  it('appends to existing textarea content instead of overwriting it', async () => {
    mockAssist()
    const wrapper = mountAssist('10.0.0.1\n10.0.0.0/8')

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()
    await advance(3)
    await confirmButton(wrapper).trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([['10.0.0.1\n10.0.0.0/8\n203.0.113.7']])
  })

  it('reports an existing entry instead of appending it twice', async () => {
    mockAssist()
    const wrapper = mountAssist('10.0.0.1, 203.0.113.7')

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()
    await advance(3)
    await confirmButton(wrapper).trigger('click')

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.get('[data-test="ip-assist-feedback"]').text()).toBe('已存在')
  })

  it('lists recent IPs with location and requires the same 3 second confirmation', async () => {
    mockAssist({
      recent_ips: [
        { ip: '198.51.100.9', count: 12, geo: { country: '美国', city: '阿什本', isp: '', hosting: true } },
        { ip: '192.0.2.44', count: 4, geo: EMPTY_GEO }
      ]
    })
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-recent-toggle"]').trigger('click')
    await flushPromises()
    await nextTick()

    const list = wrapper.get('[data-test="ip-assist-recent-list"]').text()
    expect(list).toContain('198.51.100.9')
    expect(list).toContain('美国 阿什本')
    expect(list).toContain('地区未知')
    expect(list).toContain('使用 12 次')

    await wrapper.get('[data-test="ip-assist-recent-192.0.2.44"]').trigger('click')
    await nextTick()
    expect(wrapper.get('[data-test="ip-assist-candidate"]').text()).toContain('192.0.2.44')
    expect(confirmButton(wrapper).attributes('disabled')).toBeDefined()

    await advance(3)
    await confirmButton(wrapper).trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['192.0.2.44']])
  })

  it('degrades to "region unknown" when geo is empty and never throws', async () => {
    mockAssist({ current_geo: EMPTY_GEO })
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()

    const candidate = wrapper.get('[data-test="ip-assist-candidate"]').text()
    expect(candidate).toContain('203.0.113.7')
    expect(candidate).toContain('地区未知')
    expect(wrapper.find('[data-test="ip-assist-hosting-warning"]').exists()).toBe(false)

    await advance(3)
    await confirmButton(wrapper).trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['203.0.113.7']])
  })

  it('shows the datacenter/proxy warning in red when hosting is set', async () => {
    mockAssist({ current_geo: { ...GEO_SHENZHEN, hosting: true } })
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()

    const warning = wrapper.get('[data-test="ip-assist-hosting-warning"]')
    expect(warning.text()).toContain('这个 IP 像机房/代理')
    expect(warning.classes().join(' ')).toContain('text-error')
  })

  it('reports a loading failure without crashing', async () => {
    getIPWhitelistAssistMock.mockRejectedValue(new Error('network down'))
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()

    expect(wrapper.get('[data-test="ip-assist-load-error"]').text()).toBe('获取 IP 信息失败，请稍后重试')
    expect(wrapper.find('[data-test="ip-assist-candidate"]').exists()).toBe(false)
  })

  it('re-fetches the current IP on every click so a network switch is not missed', async () => {
    getIPWhitelistAssistMock
      .mockResolvedValueOnce({
        current_ip: '203.0.113.7',
        current_geo: GEO_SHENZHEN,
        recent_ips: []
      })
      .mockResolvedValueOnce({
        current_ip: '198.51.100.9',
        current_geo: { country: '美国', city: '阿什本', hosting: true },
        recent_ips: []
      })
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()
    expect(wrapper.get('[data-test="ip-assist-candidate"]').text()).toContain('203.0.113.7')

    // Same modal, user switched network (e.g. enabled a VPN) and clicks again.
    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()
    expect(getIPWhitelistAssistMock).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="ip-assist-candidate"]').text()).toContain('198.51.100.9')
    // The new answer keeps the datacenter warning.
    expect(wrapper.find('[data-test="ip-assist-hosting-warning"]').exists()).toBe(true)
  })

  it('lets a candidate be cancelled before the countdown ends', async () => {
    mockAssist()
    const wrapper = mountAssist()

    await wrapper.get('[data-test="ip-assist-fill-current"]').trigger('click')
    await flushPromises()
    await nextTick()
    await wrapper.get('[data-test="ip-assist-cancel"]').trigger('click')

    expect(wrapper.find('[data-test="ip-assist-candidate"]').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})
