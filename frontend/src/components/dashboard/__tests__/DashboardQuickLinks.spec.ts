import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it } from 'vitest'
import DashboardQuickLinks from '../DashboardQuickLinks.vue'
import type { DashboardQuickLink } from '../dashboardUi'

const items: DashboardQuickLink[] = [
  { label: 'API Keys', to: '/keys', icon: 'i-lucide-key-round' },
  { label: 'Infinite Canvas', to: '/infinite-canvas', icon: 'i-lucide-layout-grid' },
  { label: 'Usage', to: '/usage', icon: 'i-lucide-chart-column' },
  { label: 'Redeem', to: '/redeem', icon: 'i-lucide-gift' },
]

const Page = { render: () => null }

async function mountLinks() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: Page }, ...items.map(item => ({ path: item.to, component: Page }))],
  })
  await router.push('/')
  const wrapper = mount(DashboardQuickLinks, {
    props: { items, label: 'Quick actions' },
    global: { plugins: [router] },
  })
  return { router, wrapper }
}

describe('dashboard quick links', () => {
  it('renders each action as a router link covering the whole card', async () => {
    const { wrapper } = await mountLinks()
    const links = wrapper.findAll('a[href]')

    expect(links.map(link => link.attributes('href'))).toEqual(items.map(item => item.to))
    for (const link of links) {
      // UPageCard's stretched link is an empty anchor whose absolutely positioned child
      // covers the card; without the overlay the text below is not clickable.
      expect(link.find('span.absolute').exists()).toBe(true)
    }
    wrapper.unmount()
  })

  it('keeps the stretched link out of the console press-lift transform', async () => {
    const { wrapper } = await mountLinks()

    // style.css lifts every console anchor on hover/active. A transform on this anchor would
    // make it the containing block of its `absolute inset-0` overlay, collapsing the overlay to
    // the anchor's zero width and leaving the whole card unclickable (issue #400).
    for (const link of wrapper.findAll('a[href]')) {
      expect(link.attributes('data-no-lift')).toBe('')
    }
    wrapper.unmount()
  })

  it('navigates to the matching page when a card is clicked', async () => {
    const { router, wrapper } = await mountLinks()

    await wrapper.findAll('a[href]')[2].trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/usage')
    wrapper.unmount()
  })
})
