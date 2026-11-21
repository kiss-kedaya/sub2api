import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router'
import { describe, expect, it } from 'vitest'
import { routes } from '@/router/records'
import DashboardQuickLinks from '../DashboardQuickLinks.vue'
import type { DashboardQuickLink } from '../dashboardUi'

const items: DashboardQuickLink[] = [
  { label: 'API Keys', to: '/keys', icon: 'i-lucide-key-round' },
  { label: 'Infinite Canvas', to: '/infinite-canvas', icon: 'i-lucide-layout-grid' },
  { label: 'Usage', to: '/usage', icon: 'i-lucide-chart-column' },
  { label: 'Redeem', to: '/redeem', icon: 'i-lucide-gift' },
]

const Page = { render: () => null }

async function mountLinks(quickLinks: DashboardQuickLink[] = items) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: Page }, ...quickLinks.map(item => ({ path: item.to, component: Page }))],
  })
  await router.push('/')
  const wrapper = mount(DashboardQuickLinks, {
    props: { items: quickLinks, label: 'Quick actions' },
    global: { plugins: [router] },
  })
  return { router, wrapper }
}

type FlatRoute = { path: string; meta?: Record<string, unknown> }

function flattenRoutes(list: RouteRecordRaw[], prefix = ''): FlatRoute[] {
  return list.flatMap((route) => {
    const path = route.path.startsWith('/') ? route.path : `${prefix}/${route.path}`.replace(/\/+/g, '/')
    return [
      { path, meta: route.meta as Record<string, unknown> | undefined },
      ...flattenRoutes(route.children ?? [], path),
    ]
  })
}

const routeIndex = flattenRoutes(routes)

describe('dashboard quick links', () => {
  it('renders each action as a router link covering the whole card', async () => {
    const { wrapper } = await mountLinks()
    const links = wrapper.findAll('a[href]')

    expect(links.map(link => link.attributes('href'))).toEqual(items.map(item => item.to))
    for (const link of links) {
      // UPageCard's stretched link is an empty anchor whose absolutely positioned child
      // covers the card; without the overlay the text below is not clickable.
      const overlay = link.find('span.absolute')
      expect(overlay.exists()).toBe(true)
      // The overlay is decorative: the anchor already carries the action name.
      expect(overlay.attributes('aria-hidden')).toBe('true')
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

  it('navigates to every destination when a card is clicked', async () => {
    const { router, wrapper } = await mountLinks()
    const links = wrapper.findAll('a[href]')

    for (const [index, item] of items.entries()) {
      await links[index].trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe(item.to)
    }
    wrapper.unmount()
  })

  it('keeps Tab focus visible on the card, not on the zero-size overlay anchor', async () => {
    const { wrapper } = await mountLinks()

    // The only focusable node is UPageCard's zero-size stretched anchor, so the focus ring
    // has to be painted on the card root; otherwise Tab moves focus invisibly (#400).
    for (const card of wrapper.findAll('nav > *')) {
      const classes = card.classes()
      expect(classes).toContain('has-[:focus-visible]:outline-2')
      expect(classes).toContain('has-[:focus-visible]:outline-primary')
    }

    for (const [index, link] of wrapper.findAll('a[href]').entries()) {
      // A native anchor is keyboard reachable and Enter activates it; nothing here is a
      // click-only <div>, and the anchor is exposed with a readable name.
      expect(link.element.tagName).toBe('A')
      expect(link.attributes('tabindex')).toBeUndefined()
      expect(link.attributes('aria-label')).toBe(items[index].label)
    }
    wrapper.unmount()
  })

  it('only targets real user routes, so the click never lands on a 404 or an admin-only page', () => {
    for (const item of items) {
      const route = routeIndex.find(entry => entry.path === item.to)
      // Unauthenticated visitors are sent to /login by the guard; authenticated users and
      // admins both pass, because these destinations are plain user pages.
      expect(route, `missing route for ${item.to}`).toBeDefined()
      expect(route?.meta?.requiresAuth).not.toBe(false)
      expect(route?.meta?.requiresAdmin).not.toBe(true)
    }
  })

  it('navigates the admin dashboard quick actions too, including the admin-only destination', async () => {
    const adminItems: DashboardQuickLink[] = [
      { label: 'Batch Image', to: '/batch-image', icon: 'i-lucide-sparkles' },
      { label: 'Group Pricing', to: '/admin/groups', icon: 'i-lucide-layout-grid' },
    ]
    const { router, wrapper } = await mountLinks(adminItems)
    const links = wrapper.findAll('a[href]')

    for (const [index, item] of adminItems.entries()) {
      await links[index].trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe(item.to)
      expect(routeIndex.find(entry => entry.path === item.to), `missing route for ${item.to}`).toBeDefined()
    }

    // Only the admin card is gated; the batch-image card is a user page an admin can also open.
    expect(routeIndex.find(entry => entry.path === '/admin/groups')?.meta?.requiresAdmin).toBe(true)
    expect(routeIndex.find(entry => entry.path === '/batch-image')?.meta?.requiresAdmin).not.toBe(true)
    wrapper.unmount()
  })

  it('does not open any destination in a new window', async () => {
    const { wrapper } = await mountLinks()

    // Every quick action is an in-app route. If an external destination (docs/Telegram) is
    // ever added it must be handled explicitly with target="_blank" and rel="noopener
    // noreferrer"; silently rendering it as a same-tab internal anchor is a regression.
    expect(wrapper.findAll('a[href^="http"]')).toHaveLength(0)
    for (const link of wrapper.findAll('a[href]')) {
      expect(link.attributes('target')).toBeUndefined()
    }
    wrapper.unmount()
  })
})
