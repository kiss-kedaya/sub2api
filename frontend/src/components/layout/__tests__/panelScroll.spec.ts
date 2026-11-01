import { afterEach, describe, expect, it } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { usePanelScroll } from '../panelScroll'

// The panel body is the scroll container; a new page starts at the top, back / forward
// (popstate) returns to where that history entry was left.
const Panel = defineComponent({
  setup() {
    const el = ref<HTMLElement | null>(null)
    usePanelScroll(el)
    return () => h('div', { ref: el, class: 'panel' })
  }
})

let wrapper: VueWrapper | undefined

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  history.replaceState(null, '')
})

async function mountPanel() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/a')
  wrapper = mount(Panel, { attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return wrapper.get('.panel').element as HTMLElement
}

describe('panel scroll memory', () => {
  it('starts a newly opened page at the top', async () => {
    history.replaceState({ position: 3, current: '/a' }, '')
    const panel = await mountPanel()
    expect(panel.scrollTop).toBe(0)
  })

  it('restores the position of the history entry after back / forward', async () => {
    history.replaceState({ position: 4, current: '/list' }, '')
    let panel = await mountPanel()
    panel.scrollTop = 420
    panel.dispatchEvent(new Event('scroll'))
    wrapper!.unmount()
    wrapper = undefined

    // Back to the same entry: popstate fires before the page mounts again.
    window.dispatchEvent(new PopStateEvent('popstate'))
    panel = await mountPanel()
    expect(panel.scrollTop).toBe(420)
  })
})
