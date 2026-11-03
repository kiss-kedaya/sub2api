import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, type Component } from 'vue'
import RoutePageTransition from '../RoutePageTransition.vue'

/**
 * Regression cover for "a few sidebar jumps and the console body is blank".
 *
 * The page transition used to wrap <RouterView> directly:
 *
 *     <Transition name="page" mode="out-in"><slot><RouterView /></slot></Transition>
 *
 * vue-router 5 rejects that shape (VUE_ROUTER_R0060: "<router-view> can no longer be
 * used directly inside <transition>"). Every route reaches <Transition> as the same
 * RouterView vnode, so `mode="out-in"` never engages: the outgoing page is torn down
 * while the incoming page animates in, the two share one leaving-vnode cache keyed by
 * the RouterView type, old roots stay in `.signal-main`, and a page caught mid-enter
 * keeps `.page-enter-from` (opacity: 0). The result is an empty panel with the sidebar
 * and header intact until a hard refresh.
 *
 * These specs run the real router and the real transition with fake frames, so the
 * "three links inside one 220ms fade" case is deterministic. They fail on the markup
 * above and pass on the slot + keyed <component :is> shape used by the component.
 */

const PAGE_TRANSITION_MS = 220
const RAF_FAKES = ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'requestAnimationFrame', 'cancelAnimationFrame']

function page(name: string, mounted?: () => void): Component {
  return defineComponent({
    name: `Page${name}`,
    mounted() {
      mounted?.()
    },
    render: () => h('section', { class: `page-${name.toLowerCase()}` }, name),
  })
}

const routes = [
  { path: '/a', name: 'A', component: page('A'), meta: { requiresAuth: true } },
  { path: '/b', name: 'B', component: page('B'), meta: { requiresAuth: true } },
  { path: '/c', name: 'C', component: page('C'), meta: { requiresAuth: true } },
  { path: '/d', name: 'D', component: page('D'), meta: { requiresAuth: true } },
]

function createTestRouter(extra: Parameters<typeof createRouter>[0]['routes'] = []): Router {
  return createRouter({ history: createMemoryHistory(), routes: [...routes, ...extra] })
}

const Host = defineComponent({
  name: 'ShellHost',
  render: () => h('main', { class: 'signal-main' }, [h(RoutePageTransition)]),
})

async function mountAt(router: Router, path: string): Promise<VueWrapper> {
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Host, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

/** Advance fake time in frames so rAF-based enter/leave steps run, then drain promises. */
const tick = async (ms: number) => {
  for (let elapsed = 0; elapsed < ms; elapsed += 20) {
    vi.advanceTimersByTime(20)
    await flushPromises()
  }
  await flushPromises()
}

const settle = (ms = 1200) => tick(ms)

function roots(wrapper: VueWrapper): Element[] {
  return Array.from(wrapper.find('.signal-main').element.children)
}

function classes(wrapper: VueWrapper): string[] {
  return roots(wrapper).map((el) => el.className.toString())
}

describe('RoutePageTransition', () => {
  beforeEach(() => {
    // jsdom reports `prefers-reduced-motion: reduce` as true for every query, which
    // would switch the component to its instant (motion-free) path. These specs cover
    // the animated path, so report what a default browser reports.
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  it('plays out-in: the outgoing page leaves before the next one mounts', async () => {
    vi.useFakeTimers({ toFake: RAF_FAKES })
    const router = createTestRouter()
    const wrapper = await mountAt(router, '/a')
    await settle()
    expect(classes(wrapper)).toEqual(['page-a'])

    await router.push('/b')
    await flushPromises()
    await tick(40)

    // Half-way through the fade the old page is still the only page on screen and the
    // new one must not exist yet. The pre-fix markup mounted page-b here immediately,
    // so this assertion is the red/green line between the two shapes.
    expect(classes(wrapper)).toHaveLength(1)
    expect(classes(wrapper)[0]).toMatch(/^page-a\b/)
    expect(classes(wrapper)[0]).toContain('page-leave-active')
    expect(wrapper.find('.page-b').exists()).toBe(false)

    await settle()
    expect(classes(wrapper)).toEqual(['page-b'])
    wrapper.unmount()
  })

  it('keeps exactly one page mounted while a user clicks three links quickly', async () => {
    vi.useFakeTimers({ toFake: RAF_FAKES })
    const router = createTestRouter()
    const wrapper = await mountAt(router, '/a')
    await settle()

    // The fade the product shipped must still run: every switch has to mount a page
    // with the leave classes. The pre-fix shape swapped pages without ever leaving.
    const seen = { enter: 0, leave: 0 }
    const observer = new MutationObserver((records) => {
      for (const record of records) {
        const el = record.target as Element
        if (el.classList?.contains('page-enter-active')) seen.enter += 1
        if (el.classList?.contains('page-leave-active')) seen.leave += 1
      }
    })
    observer.observe(wrapper.find('.signal-main').element, { attributes: true, attributeFilter: ['class'], subtree: true })

    for (const path of ['/b', '/c', '/b', '/d']) {
      await router.push(path)
      await flushPromises()
      await tick(30)
    }
    await settle()
    observer.disconnect()

    expect(seen.leave).toBeGreaterThan(0)
    expect(seen.enter).toBeGreaterThan(0)
    expect(classes(wrapper)).toEqual(['page-d'])
    expect(wrapper.find('.page-d').text()).toBe('D')
    wrapper.unmount()
  })

  it('never strands a page at .page-enter-from (opacity: 0)', async () => {
    vi.useFakeTimers({ toFake: RAF_FAKES })
    const router = createTestRouter()
    const wrapper = await mountAt(router, '/a')
    await settle()

    for (const path of ['/b', '/c', '/d']) {
      await router.push(path)
      await flushPromises()
      await tick(40)
    }
    await settle()

    // jsdom has no layout; the class list is the visibility contract because
    // `.page-enter-from` is `opacity: 0` in the shipped stylesheet.
    const hidden = roots(wrapper).filter((el) => el.classList.contains('page-enter-from'))
    expect(hidden).toHaveLength(0)
    expect(wrapper.findAll('.page-leave-active')).toHaveLength(0)
    expect(wrapper.findAll('.page-enter-active')).toHaveLength(0)
    expect(classes(wrapper)).toEqual(['page-d'])
    wrapper.unmount()
  })

  it('finishes the leave on a timer when transitionend never fires', async () => {
    vi.useFakeTimers({ toFake: RAF_FAKES })
    const router = createTestRouter()
    const wrapper = await mountAt(router, '/a')
    await settle()

    // jsdom never emits transitionend, and the explicit duration on <Transition>
    // gives Vue its own timer, so out-in still hands over instead of holding the
    // empty placeholder forever.
    await router.push('/b')
    await flushPromises()
    await tick(PAGE_TRANSITION_MS + 80)
    await settle()

    expect(classes(wrapper)).toEqual(['page-b'])
    expect(wrapper.findAll('.page-enter-from')).toHaveLength(0)
    wrapper.unmount()
  })

  it('waits for a lazy route component without leaving a blank frame behind', async () => {
    vi.useFakeTimers({ toFake: RAF_FAKES })
    // Same contract as `component: () => import(...)`: vue-router awaits this promise
    // while the route is still on the previous page.
    let resolveLazy: ((module: { default: Component }) => void) | undefined
    const lazyRoute = () =>
      new Promise<{ default: Component }>((resolve) => {
        resolveLazy = resolve
      })
    const router = createTestRouter([
      { path: '/lazy', name: 'Lazy', component: lazyRoute, meta: { requiresAuth: true } },
    ])
    const wrapper = await mountAt(router, '/a')
    await settle()

    // Start the lazy navigation, then change your mind before the chunk resolves.
    const pending = router.push('/lazy')
    await flushPromises()
    await router.push('/b')
    await flushPromises()
    await tick(20)
    resolveLazy?.({ default: page('LazyPage') })
    await pending.catch(() => undefined)
    await settle()

    expect(classes(wrapper)).toEqual(['page-b'])
    expect(wrapper.findAll('.page-enter-from')).toHaveLength(0)
    wrapper.unmount()
  })

  it('keys pages by route pattern so query changes do not remount the page', async () => {
    vi.useFakeTimers({ toFake: RAF_FAKES })
    let mounts = 0
    const router = createTestRouter([
      { path: '/query', name: 'Query', component: page('Query', () => { mounts += 1 }), meta: { requiresAuth: true } },
    ])
    const wrapper = await mountAt(router, '/query?page=1')
    await settle()
    expect(mounts).toBe(1)

    await router.push('/query?page=2')
    await settle()
    expect(mounts).toBe(1)
    wrapper.unmount()
  })
})
