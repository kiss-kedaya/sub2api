import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'
import EphemeralKeyBanner from '../EphemeralKeyBanner.vue'

const copyToClipboard = vi.hoisted(() => vi.fn())

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

const messages: Record<string, string> = {
  'keys.ephemeralBar.title': 'New key created — copy it again within 5 minutes',
  'keys.ephemeralBar.hint': 'The plaintext lives only in this tab’s memory for {minutes} minutes.',
  'keys.ephemeralBar.copy': 'Copy new key',
  'common.copied': 'Copied',
  'common.close': 'Close',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const template = messages[key] ?? key
        if (!params) return template
        return Object.entries(params).reduce(
          (text, [name, value]) => text.replace(`{${name}}`, String(value)),
          template
        )
      },
    }),
  }
})

const SECRET = 'ephemeral-bar-fixture-secret'

const mountBar = (keyId = 7) =>
  mount(EphemeralKeyBanner, {
    props: { keyId },
    attachTo: document.body,
  })

beforeEach(() => {
  setActivePinia(createPinia())
  copyToClipboard.mockReset()
  copyToClipboard.mockResolvedValue(true)
  useEphemeralKeyStore().remember(7, SECRET)
  localStorage.clear()
  sessionStorage.clear()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('EphemeralKeyBanner', () => {
  it('renders the store plaintext and copies it without touching web storage', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const wrapper = mountBar()

    expect(wrapper.get('[data-test="ephemeral-key-bar"]').text()).toContain(
      'New key created — copy it again within 5 minutes'
    )
    await wrapper.get('[data-test="ephemeral-key-copy"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith(SECRET)
    // 明文只能来自内存 store：任何 localStorage/sessionStorage 写入都不允许带它。
    const written = setItem.mock.calls.map((call) => String(call[1])).join('\n')
    expect(written).not.toContain(SECRET)
    wrapper.unmount()
  })

  it('disappears and emits expired after five minutes', async () => {
    vi.useFakeTimers()
    const wrapper = mountBar()
    expect(wrapper.find('[data-test="ephemeral-key-bar"]').exists()).toBe(true)

    vi.advanceTimersByTime(5 * 60 * 1000 - 1)
    await nextTick()
    expect(wrapper.find('[data-test="ephemeral-key-bar"]').exists()).toBe(true)

    vi.advanceTimersByTime(1)
    await nextTick()
    expect(wrapper.emitted('expired')).toHaveLength(1)
    expect(wrapper.find('[data-test="ephemeral-key-bar"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('can be dismissed early and clears its timer on unmount', async () => {
    vi.useFakeTimers()
    const wrapper = mountBar()
    await wrapper.get('[data-test="ephemeral-key-close"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('expired')).toHaveLength(1)
    expect(wrapper.find('[data-test="ephemeral-key-bar"]').exists()).toBe(false)

    // 提前卸载后，原来的 5 分钟计时器不能再触发一次 expired。
    vi.advanceTimersByTime(5 * 60 * 1000)
    await nextTick()
    expect(wrapper.emitted('expired')).toHaveLength(1)
    wrapper.unmount()
  })

  it('hides itself when the store no longer has the plaintext', async () => {
    const wrapper = mountBar(999)
    expect(wrapper.find('[data-test="ephemeral-key-bar"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
