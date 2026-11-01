import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import PageNavbar from '../PageNavbar.vue'
import PageToolbar from '../PageToolbar.vue'
import { FRAME_TARGETS, provideDashboardFrame, type DashboardFrame } from '../dashboardFrame'

// A stand-in for AppLayout + AppHeader: the frame context and the teleport targets.
let frame: DashboardFrame | undefined
const Frame = defineComponent({
  setup(_, { slots }) {
    frame = provideDashboardFrame()
    return () => h('div', [
      h('header', { class: 'navbar' }, [
        h('h1', { class: 'navbar-title' }, [
          frame!.titles.at(-1)?.slot ? null : h('span', { class: 'navbar-text' }, frame!.titles.at(-1)?.text() || 'Route title'),
          h('span', { id: FRAME_TARGETS.title })
        ]),
        h('span', { id: FRAME_TARGETS.titleTrailing }),
        h('span', { id: FRAME_TARGETS.actions })
      ]),
      h('div', { class: 'toolbar' }, [h('div', { id: FRAME_TARGETS.toolbarStart }), h('div', { id: FRAME_TARGETS.toolbarEnd })]),
      h('main', slots.default?.())
    ])
  }
})

let wrapper: VueWrapper | undefined

async function inFrame(page: () => ReturnType<typeof h>) {
  wrapper = mount({ render: () => h(Frame, null, { default: page }) }, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  frame = undefined
})

describe('page headers in the console frame', () => {
  it('names the page in the navbar, moves the actions there and drops the description', async () => {
    const w = await inFrame(() => h(AdminPageHeader, { title: 'Accounts', description: 'Long explanation' }, {
      actions: () => h('button', { id: 'create' }, 'Create'),
      meta: () => h('span', { id: 'chips' }, 'chips')
    }))
    expect(w.get('.navbar-text').text()).toBe('Accounts')
    // matchMedia is stubbed to match in specs, i.e. a wide screen: actions in the navbar.
    expect(w.find(`#${FRAME_TARGETS.actions} #create`).exists()).toBe(true)
    expect(w.text()).not.toContain('Long explanation')
    expect(w.find('main h2').exists()).toBe(false)
    // #meta stays in the page.
    expect(w.find('main #chips').exists()).toBe(true)
  })

  it('lets a page bring its own title markup and badge', async () => {
    const w = await inFrame(() => h(UserPageHeader, null, {
      title: () => h('span', { id: 'custom-title' }, 'Keys'),
      badge: () => h('span', { id: 'count' }, '26'),
      description: () => h('span', { id: 'status' }, 'updated 12:00')
    }))
    expect(w.find('.navbar-text').exists()).toBe(false)
    expect(w.find(`#${FRAME_TARGETS.title} #custom-title`).exists()).toBe(true)
    expect(w.find(`#${FRAME_TARGETS.titleTrailing} #count`).exists()).toBe(true)
    // A live status line from #description sits next to the title (wide screens).
    expect(w.find(`#${FRAME_TARGETS.titleTrailing} #status`).exists()).toBe(true)
    expect(w.findAll('h1')).toHaveLength(1)
  })

  it('hands the navbar back to the route title when the header goes away', async () => {
    const shown = ref(true)
    const w = await inFrame(() => (shown.value ? h(AdminPageHeader, { title: 'Plans' }) : h('p', 'no header')))
    expect(w.get('.navbar-text').text()).toBe('Plans')
    shown.value = false
    await flushPromises()
    expect(frame!.titles).toHaveLength(0)
    expect(w.get('.navbar-text').text()).toBe('Route title')
  })

  it('renders in place outside the frame (stubbed layouts, public pages)', () => {
    wrapper = mount(AdminPageHeader, {
      props: { title: 'Accounts', description: 'Long explanation' },
      attrs: { class: 'mb-4' },
      slots: { actions: '<button id="create">Create</button>' }
    })
    expect(wrapper.get('header.admin-page-header').classes()).toContain('mb-4')
    expect(wrapper.get('h2').text()).toBe('Accounts')
    expect(wrapper.text()).toContain('Long explanation')
    expect(wrapper.find('#create').exists()).toBe(true)
  })
})

describe('PageNavbar and PageToolbar', () => {
  it('teleport page controls into the navbar and the toolbar row', async () => {
    const w = await inFrame(() => [
      h(PageNavbar, null, { default: () => h('button', { id: 'export' }, 'Export') }),
      h(PageToolbar, null, {
        default: () => h('input', { id: 'search' }),
        right: () => h('button', { id: 'columns' }, 'Columns')
      })
    ])
    expect(w.find(`#${FRAME_TARGETS.actions} #export`).exists()).toBe(true)
    expect(w.find(`#${FRAME_TARGETS.toolbarStart} #search`).exists()).toBe(true)
    expect(w.find(`#${FRAME_TARGETS.toolbarEnd} #columns`).exists()).toBe(true)
  })

  it('render in place without the frame', () => {
    wrapper = mount(PageToolbar, { slots: { default: '<input id="search" />', right: '<button id="columns" />' } })
    expect(wrapper.find('.page-toolbar-inline #search').exists()).toBe(true)
    expect(wrapper.find('.page-toolbar-inline__end #columns').exists()).toBe(true)
  })
})
