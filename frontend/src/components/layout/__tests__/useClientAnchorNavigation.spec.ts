import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import { describe, expect, it } from 'vitest'
import { useClientAnchorNavigation } from '@/composables/useClientAnchorNavigation'

describe('client anchor navigation', () => {
  it('pushes a same-origin plain anchor instead of reloading', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { render: () => null } },
        { path: '/tickets/:id', component: { render: () => null } }
      ]
    })
    await router.push('/')

    const Host = defineComponent({
      setup() {
        useClientAnchorNavigation(router)
        return () => h('a', { href: '/tickets/1' }, 'open')
      }
    })
    const wrapper = mount(Host, { global: { plugins: [router] } })
    await wrapper.get('a').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/tickets/1')
    wrapper.unmount()
  })
})