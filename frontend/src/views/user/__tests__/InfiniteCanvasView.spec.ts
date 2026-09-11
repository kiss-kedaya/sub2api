import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import InfiniteCanvasView from '../InfiniteCanvasView.vue'

const { ensureInfiniteCanvasApiKey, showSuccess, showWarning, fetchPublicSettings } = vi.hoisted(() => ({
  ensureInfiniteCanvasApiKey: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  fetchPublicSettings: vi.fn(),
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
    locale: { value: 'zh-CN' },
  }),
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    publicSettingsLoaded: true,
    cachedPublicSettings: { api_base_url: 'https://api.example.com' },
    fetchPublicSettings,
    showSuccess,
    showWarning,
  }),
}))
vi.mock('@/utils/infiniteCanvasSession', () => ({
  InfiniteCanvasSetupError: class InfiniteCanvasSetupError extends Error {
    constructor(public code: string, message?: string) {
      super(message)
    }
  },
  ensureInfiniteCanvasApiKey,
}))

const wrappers: ReturnType<typeof mount>[] = []

describe('InfiniteCanvasView', () => {
  beforeEach(() => {
    ensureInfiniteCanvasApiKey.mockReset()
    showSuccess.mockReset()
    showWarning.mockReset()
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  })

  it('embeds Infinite Canvas with the smart-routing key and OpenAI-compatible base URL', async () => {
    ensureInfiniteCanvasApiKey.mockResolvedValue({
      apiKey: 'sk-canvas',
      groupIds: [2, 5],
      truncated: false,
      created: true,
      keyId: 9,
    })
    const wrapper = mount(InfiniteCanvasView, {
      global: { stubs: { Icon: true } },
    })
    wrappers.push(wrapper)
    await flushPromises()

    const src = wrapper.get('iframe').attributes('src') || ''
    const url = new URL(src)
    expect(url.pathname).toBe('/canvas/')
    expect(url.searchParams.get('apiKey')).toBe('sk-canvas')
    expect(url.searchParams.get('baseUrl')).toBe('https://api.example.com')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(showSuccess).toHaveBeenCalled()
  })

  it('shows a retryable error when the user has no groups', async () => {
    const { InfiniteCanvasSetupError } = await import('@/utils/infiniteCanvasSession')
    ensureInfiniteCanvasApiKey.mockRejectedValue(new InfiniteCanvasSetupError('no-groups', 'none'))
    const wrapper = mount(InfiniteCanvasView, {
      global: { stubs: { Icon: true } },
    })
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('infiniteCanvas.noGroups')
  })
})
