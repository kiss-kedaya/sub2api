import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, inject, nextTick, type Ref } from 'vue'
import { portalTargetInjectionKey } from '@nuxt/ui/composables/usePortal'
import BaseDialog from '../BaseDialog.vue'

enableAutoUnmount(afterEach)

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' },
      global: { stubs: { Icon: true } }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
    wrapper.unmount()
  })

  it('orders dialogs by opening time even when teleports mounted in the opposite order', async () => {
    const verification = mount(BaseDialog, { attachTo: document.body, props: { show: false, title: 'Verify' }, slots: { default: '<input aria-label="Code" />' } })
    const original = mount(BaseDialog, { attachTo: document.body, props: { show: true, title: 'Original' }, slots: { default: '<input aria-label="Original input" />' } })
    await flushPromises()
    await verification.setProps({ show: true })
    await flushPromises()
    const overlays = Array.from(document.body.querySelectorAll<HTMLElement>('.modal-overlay'))
    const top = overlays.find(overlay => overlay.textContent?.includes('Verify'))!
    const lower = overlays.find(overlay => overlay.textContent?.includes('Original'))!
    expect(Number(top.style.zIndex)).toBeGreaterThan(Number(lower.style.zIndex))
    expect(top.hasAttribute('inert')).toBe(false)
    expect(lower.hasAttribute('inert')).toBe(true)
    expect(lower.getAttribute('aria-hidden')).toBe('true')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(verification.emitted('close')).toHaveLength(1)
    expect(original.emitted('close')).toBeUndefined()
    await verification.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(true)
    expect(lower.hasAttribute('inert')).toBe(false)
    await original.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(false)
  })

  it('closes from the header close button', async () => {
    const wrapper = mount(BaseDialog, { attachTo: document.body, props: { show: true, title: 'Details' } })
    await flushPromises()
    document.querySelector<HTMLButtonElement>('.modal-header button[aria-label="Close modal"]')!.click()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('lets an open menu or popover inside the dialog take Escape first', async () => {
    const wrapper = mount(BaseDialog, { attachTo: document.body, props: { show: true, title: 'Menu host' }, slots: { default: '<div data-dismissable-layer>menu</div>' } })
    await flushPromises()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toBeUndefined()
    document.querySelector('[data-dismissable-layer]')!.remove()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('makes the overlay the portal target of Nuxt UI popovers opened inside it', async () => {
    let target: Ref<string | HTMLElement | boolean> | undefined
    const Probe = defineComponent({ setup() { target = inject(portalTargetInjectionKey); return () => h('span') } })
    mount(BaseDialog, { attachTo: document.body, props: { show: true, title: 'Host' }, slots: { default: Probe } })
    await flushPromises()
    expect(target?.value).toBe(document.querySelector('.modal-overlay'))
  })

  it('keeps keyboard focus within the top dialog', async () => {
    mount(BaseDialog, { attachTo: document.body, props: { show: true, title: 'Verify', showCloseButton: false }, slots: { default: '<input aria-label="Code" /><button>Cancel</button>' } })
    await flushPromises()
    const input = document.querySelector<HTMLInputElement>('input')!
    const button = document.querySelector<HTMLButtonElement>('.modal-body button')!
    button.focus()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(input)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(button)
  })
})
