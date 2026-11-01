import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import PageSubnav from '../PageSubnav.vue'
import { FRAME_TARGETS, provideDashboardFrame } from '../dashboardFrame'

const routes = ['/admin/settings', '/admin/settings/general', '/admin/settings/security', '/admin/settings/security/keys']
  .map((path) => ({ path, component: { template: '<div />' } }))

// A stand-in for AppLayout + AppHeader: the frame context and the toolbar targets.
const Frame = defineComponent({
  setup(_, { slots }) {
    provideDashboardFrame()
    return () => h('div', [
      h('div', { class: 'console-toolbar' }, [
        h('div', { id: FRAME_TARGETS.toolbarStart, class: 'console-toolbar-start' }),
        h('div', { id: FRAME_TARGETS.toolbarEnd, class: 'console-toolbar-end' })
      ]),
      h('main', slots.default?.())
    ])
  }
})

const items = [
  { label: 'General', icon: 'i-lucide-user', to: '/admin/settings/general' },
  { label: 'Security', icon: 'i-lucide-shield', to: '/admin/settings/security' },
  { label: 'Overview', to: '/admin/settings' }
]

let wrapper: VueWrapper | undefined

async function render(path: string, framed = true) {
  const router = createRouter({ history: createMemoryHistory(), routes })
  await router.push(path)
  await router.isReady()
  const page = () => h(PageSubnav, { items, label: 'Settings' }, { right: () => h('button', { id: 'docs' }, 'Docs') })
  wrapper = mount(framed ? { render: () => h(Frame, null, { default: page }) } : { render: page }, {
    attachTo: document.body,
    global: { plugins: [router] }
  })
  await flushPromises()
  return wrapper
}

const current = (w: VueWrapper) => w.findAll('a[aria-current="page"]').map((link) => link.text())

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('PageSubnav', () => {
  it('puts the sub-pages in the toolbar row, with an optional right side', async () => {
    const w = await render('/admin/settings/general')
    const start = w.get(`#${FRAME_TARGETS.toolbarStart}`)
    expect(start.findAll('a').map((link) => link.text())).toEqual(['General', 'Security', 'Overview'])
    expect(start.get('nav').attributes('aria-label')).toBe('Settings')
    expect(w.find(`#${FRAME_TARGETS.toolbarEnd} #docs`).exists()).toBe(true)
    expect(w.find('main nav').exists()).toBe(false)
  })

  it('lights the deepest matching sub-page, also on a deeper route', async () => {
    expect(current(await render('/admin/settings/security/keys'))).toEqual(['Security'])
  })

  it('lights the area root only on the root itself', async () => {
    expect(current(await render('/admin/settings'))).toEqual(['Overview'])
  })

  it('renders in place without the frame', async () => {
    const w = await render('/admin/settings/general', false)
    expect(w.find('.page-toolbar-inline nav a').exists()).toBe(true)
    expect(current(w)).toEqual(['General'])
  })
})
