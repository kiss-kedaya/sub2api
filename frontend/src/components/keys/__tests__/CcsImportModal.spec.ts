import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import CcsImportModal from '../CcsImportModal.vue'
import Select from '@/components/common/Select.vue'

const mocks = vi.hoisted(() => ({
  copyToClipboard: vi.fn().mockResolvedValue(true),
  showError: vi.fn(),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copyToClipboard }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: mocks.showError }),
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

describe('CcsImportModal', () => {
  beforeEach(() => {
    mocks.copyToClipboard.mockClear()
    mocks.copyToClipboard.mockResolvedValue(true)
    mocks.showError.mockClear()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('disables confirm for Pi-family apps when /v1/models returns empty until a model is entered', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: [] }),
      }),
    )

    const wrapper = mount(CcsImportModal, {
      attachTo: document.body,
      props: {
        show: true,
        apiKey: 'demo-ccs-key-1234',
        baseUrl: 'https://gateway.example.test',
        siteName: 'kedaya.ai',
        defaultApp: 'pi',
      },
      global: { stubs: { Transition: { template: '<slot />' }, teleport: true } },
    })
    await flushPromises()

    const confirmBtn = wrapper.get('[data-test="ccs-import-confirm"]')
    expect(confirmBtn.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="ccs-import-model-hint"]').text()).toContain('keys.ccsImport.modelEmpty')

    const modelSelect = wrapper.findAllComponents(Select)[1]
    expect(modelSelect.props('creatable')).toBe(true)
    modelSelect.vm.$emit('update:modelValue', 'claude-sonnet-4-6')
    await nextTick()

    const readyLink = wrapper.get('[data-test="ccs-import-confirm"]')
    expect(readyLink.element.tagName).toBe('A')
    expect(readyLink.attributes('disabled')).toBeUndefined()
    expect(readyLink.attributes('href')).toMatch(/^ccswitch:\/\/v1\/import\?/)
    wrapper.unmount()
  })

  it('renders a native ccswitch:// anchor, avoids 100ms error toasts, shows 1300ms calm hint strip, and completes on blur/visibilitychange/copy', async () => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: [{ id: 'claude-sonnet-4-6' }] }),
      }),
    )

    const wrapper = mount(CcsImportModal, {
      attachTo: document.body,
      props: {
        show: true,
        apiKey: 'demo-ccs-key-1234',
        baseUrl: 'https://gateway.example.test',
        siteName: 'kedaya.ai',
        defaultApp: 'claude',
      },
      global: { stubs: { Transition: { template: '<slot />' }, teleport: true } },
    })
    await flushPromises()

    const confirmLink = wrapper.get('[data-test="ccs-import-confirm"]')
    expect(confirmLink.element.tagName).toBe('A')
    expect(confirmLink.attributes('href')).toMatch(/^ccswitch:\/\/v1\/import\?/)

    let preventedByProd = true
    const blockJsdomNav = (event: Event) => {
      preventedByProd = event.defaultPrevented
      event.preventDefault()
    }
    document.addEventListener('click', blockJsdomNav, { once: true })
    await confirmLink.trigger('click')
    expect(preventedByProd).toBe(false)
    expect(wrapper.emitted('imported')).toBeUndefined()

    // No false-positive error toast at 100ms or 1300ms
    vi.advanceTimersByTime(150)
    await flushPromises()
    expect(mocks.showError).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="ccs-import-fallback-notice"]').exists()).toBe(false)

    // Calm info hint strip appears at ~1300ms with Retry <a :href>, Copy, and Download links
    vi.advanceTimersByTime(1200)
    await flushPromises()
    expect(mocks.showError).not.toHaveBeenCalled()
    expect(wrapper.emitted('imported')).toBeUndefined()
    expect(wrapper.find('[data-test="ccs-import-fallback-notice"]').exists()).toBe(true)
    const retryLink = wrapper.get('[data-test="ccs-import-retry-link"]')
    expect(retryLink.element.tagName).toBe('A')
    expect(retryLink.attributes('href')).toBe(confirmLink.attributes('href'))
    expect(wrapper.find('[data-test="ccs-import-download-link"]').exists()).toBe(true)

    // Copying the fallback deeplink emits imported cleanly without error toast
    await wrapper.get('[data-test="ccs-import-copy-deeplink"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('imported')).toHaveLength(1)

    // Re-clicking confirm/retry and losing window focus (OS launched CC Switch) emits imported and closes
    document.addEventListener('click', blockJsdomNav, { once: true })
    await confirmLink.trigger('click')
    window.dispatchEvent(new Event('blur'))
    await flushPromises()
    expect(wrapper.emitted('imported')).toHaveLength(2)
    expect(wrapper.emitted('close')).toHaveLength(1)

    // Re-open modal and verify visibilitychange (document.hidden = true) also completes import
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    document.addEventListener('click', blockJsdomNav, { once: true })
    await wrapper.get('[data-test="ccs-import-confirm"]').trigger('click')
    const hiddenSpy = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    hiddenSpy.mockRestore()
    expect(wrapper.emitted('imported')).toHaveLength(3)
    expect(wrapper.emitted('close')).toHaveLength(2)
    expect(mocks.showError).not.toHaveBeenCalled()

    wrapper.unmount()
  })
})
