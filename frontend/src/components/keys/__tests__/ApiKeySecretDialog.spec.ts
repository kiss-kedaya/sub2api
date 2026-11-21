import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey } from '@/types'
import ApiKeySecretDialog from '../ApiKeySecretDialog.vue'

const copyToClipboard = vi.hoisted(() => vi.fn().mockResolvedValue(true))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

const messages: Record<string, string> = {
  'keys.secretDialog.title': 'Plaintext key (shown once)',
  'keys.secretDialog.description': 'The server will not return the plaintext again.',
  'keys.secretDialog.saveNotice': 'Copy and store it now.',
  'keys.secretDialog.cannotViewAgain': 'You cannot view it again after closing.',
  'keys.secretDialog.waitHint': 'This dialog unlocks after at least 1 second.',
  'keys.secretDialog.copy': 'Copy key',
  'keys.secretDialog.copied': 'Copied',
  'keys.secretDialog.use': 'Use key',
  'keys.secretDialog.importCcs': 'Import to CCS',
  'keys.secretDialog.done': 'Done',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

enableAutoUnmount(afterEach)

const SECRET = 'sk-once-abc123'

const createApiKey = (): ApiKey => ({
  id: 1,
  user_id: 1,
  key_prefix: 'sk-once-',
  key_masked: 'sk-once-****-6789',
  name: 'prod key',
  group_id: null,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-11-21T00:00:00Z',
  updated_at: '2026-11-21T00:00:00Z',
  current_concurrency: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
})

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

const mountDialog = async (): Promise<VueWrapper> => {
  const wrapper = mount(ApiKeySecretDialog, {
    attachTo: document.body,
    props: { show: true, secret: SECRET, apiKey: createApiKey() },
    global: {
      stubs: { teleport: true, Icon: true },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

const dialogBody = (wrapper: VueWrapper) => wrapper.get('[data-test="api-key-secret-dialog"]')
const doneButton = (wrapper: VueWrapper) => wrapper.get('[data-test="api-key-secret-done"]')
const closeButton = (wrapper: VueWrapper) => wrapper.get('.base-dialog-header [data-slot="close"]')
const clickNatively = (element: { element: Element }) =>
  (element.element as HTMLButtonElement).click()

describe('ApiKeySecretDialog minimum display window', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
    copyToClipboard.mockResolvedValue(true)
    localStorage.clear()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the plaintext, the cannot-view-again notice and the wait hint on open', async () => {
    const wrapper = await mountDialog()

    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(SECRET)
    expect(wrapper.text()).toContain('You cannot view it again after closing.')
    expect(wrapper.get('[data-test="api-key-secret-wait-hint"]').exists()).toBe(true)
    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('false')
  })

  it('blocks X, Escape, the veil and the footer close button during the first second', async () => {
    const wrapper = await mountDialog()

    const x = closeButton(wrapper)
    expect(x.attributes('disabled')).toBeDefined()
    expect(doneButton(wrapper).attributes('disabled')).toBeDefined()

    clickNatively(x)
    clickNatively(doneButton(wrapper))
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await wrapper.get('.modal-overlay').trigger('click')
    await nextTick()

    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(SECRET)
    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('false')
  })

  it('unlocks the footer close button and header X after one second', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()
    await flushPromises()

    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('true')
    expect(wrapper.find('[data-test="api-key-secret-wait-hint"]').exists()).toBe(false)
    expect(doneButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(closeButton(wrapper).attributes('disabled')).toBeUndefined()

    clickNatively(closeButton(wrapper))
    await nextTick()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('unlocks Escape after one second', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()

    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('unlocks clicking the veil after one second', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()

    await wrapper.get('.modal-overlay').trigger('click')
    await nextTick()

    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('restarts the one-second window when the dialog is reopened for another key', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()
    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('true')

    await wrapper.setProps({ show: false })
    await nextTick()
    await wrapper.setProps({ show: true, secret: 'sk-second-xyz' })
    await nextTick()

    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('false')
    expect(wrapper.get('[data-test="api-key-secret-wait-hint"]').exists()).toBe(true)
  })

  it('copies the one-time plaintext from inside the dialog', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-test="api-key-secret-copy"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith(SECRET, 'Copied')
    expect(wrapper.get('[data-test="api-key-secret-copy"]').text()).toContain('Copied')
  })

  it('never persists the plaintext to browser storage', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-test="api-key-secret-copy"]').trigger('click')
    await flushPromises()

    const stored = [
      ...Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)),
      ...Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)),
    ]
      .filter((key): key is string => key !== null)
      .map((key) => `${key}=${localStorage.getItem(key) ?? sessionStorage.getItem(key) ?? ''}`)
      .join('\n')

    expect(stored).not.toContain(SECRET)
  })
})
