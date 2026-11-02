import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h, nextTick } from 'vue'
import { describe, expect, it } from 'vitest'
import { useClientAnchorNavigation } from '@/composables/useClientAnchorNavigation'

const routes = [
  { path: '/', component: { render: () => null } },
  { path: '/tickets/:id', component: { render: () => null } }
]

const mountHost = async (href: string) => {
  const router = createRouter({ history: createMemoryHistory(), routes })
  await router.push('/')
  const Host = defineComponent({
    setup() {
      useClientAnchorNavigation(router)
      return () => h('a', { href }, 'open')
    }
  })
  const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router] } })
  await nextTick()
  return { router, wrapper }
}

describe('client anchor navigation', () => {
  it('pushes a same-origin plain anchor instead of reloading', async () => {
    const { router, wrapper } = await mountHost('/tickets/1')
    await wrapper.get('a').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/tickets/1')
    wrapper.unmount()
  })

  it('leaves same-origin URLs that are not SPA routes to the browser', async () => {
    const { router, wrapper } = await mountHost('/exports/report.csv')
    const anchor = wrapper.get('a')
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })
    anchor.element.dispatchEvent(event)
    await flushPromises()

    expect(event.defaultPrevented).toBe(false)
    expect(router.currentRoute.value.fullPath).toBe('/')
    wrapper.unmount()
  })
})
