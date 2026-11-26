import { defineComponent, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useRouteIntentPrefetch } from '../useRouteIntentPrefetch'

describe('useRouteIntentPrefetch', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('prefetches lazy route chunks once on pointer hover or keyboard focus and ignores external links', async () => {
    const keysLoader = vi.fn().mockResolvedValue({ default: defineComponent({ render: () => h('div') }) })
    const docsLoader = vi.fn().mockResolvedValue({ default: defineComponent({ render: () => h('div') }) })

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: defineComponent({ render: () => h('div') }) },
        { path: '/keys', component: keysLoader },
        { path: '/docs', component: docsLoader },
      ],
    })
    await router.push('/')
    await router.isReady()

    const Host = defineComponent({
      setup() {
        useRouteIntentPrefetch(router)
        return () =>
          h('div', [
            h('a', { id: 'keys-link', href: '/keys' }, 'Keys'),
            h('a', { id: 'docs-link', href: '/docs?page=agent-setup' }, 'Docs'),
            h('a', { id: 'ext-link', href: 'https://example.com', target: '_blank' }, 'External'),
          ])
      },
    })

    const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router] } })
    await nextTick()

    // Pointer hover warms /keys once
    const keysEl = wrapper.get('#keys-link').element
    keysEl.dispatchEvent(new MouseEvent('pointerover', { bubbles: true }))
    keysEl.dispatchEvent(new MouseEvent('pointerover', { bubbles: true }))
    await flushPromises()
    expect(keysLoader).toHaveBeenCalledTimes(1)

    // Keyboard focus warms /docs once
    const docsEl = wrapper.get('#docs-link').element
    docsEl.dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
    await flushPromises()
    expect(docsLoader).toHaveBeenCalledTimes(1)

    // External link never triggers anything
    const extEl = wrapper.get('#ext-link').element
    extEl.dispatchEvent(new MouseEvent('pointerover', { bubbles: true }))
    await flushPromises()
    expect(keysLoader).toHaveBeenCalledTimes(1)
    expect(docsLoader).toHaveBeenCalledTimes(1)

    wrapper.unmount()
  })
})
