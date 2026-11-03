import { defineNuxtPlugin } from '#app'

/**
 * Nuxt SSR ships a `document` shim that exists but has no event API, and no
 * `defaultView`. @floating-ui/dom (used by every Nuxt UI popover/select/menu)
 * calls `getWindow(el).addEventListener(...)` unconditionally in `autoUpdate`,
 * so any admin page that renders such a component during SSR throws
 * "Cannot read properties of undefined (reading 'addEventListener')" and the
 * whole route answers 500 — /admin/redeem was the first one hit.
 *
 * Rather than patch each call site, give the server-side shim the two things
 * floating-ui assumes: an object returned by `ownerDocument.defaultView` and
 * no-op event methods. Nothing runs on the client; this file is a no-op there.
 */
export default defineNuxtPlugin({
  name: 'ssr-dom-guard',
  enforce: 'pre',
  setup() {
    if (typeof window !== 'undefined') return

    const g = globalThis as unknown as {
      document?: Record<string, unknown>
      window?: Record<string, unknown>
    }
    const doc = g.document
    if (!doc) return

    const noop = () => {}
    // The stand-in "window" only needs to absorb listener registration and
    // measurement reads floating-ui performs before it is torn down on hydrate.
    const inertWindow: Record<string, unknown> = {
      addEventListener: noop,
      removeEventListener: noop,
      matchMedia: () => ({
        matches: false,
        addEventListener: noop,
        removeEventListener: noop,
        addListener: noop,
        removeListener: noop,
      }),
      getComputedStyle: () => ({ getPropertyValue: () => '' }),
      innerWidth: 0,
      innerHeight: 0,
      devicePixelRatio: 1,
      requestAnimationFrame: noop,
      cancelAnimationFrame: noop,
    }
    inertWindow.window = inertWindow
    inertWindow.self = inertWindow

    Object.defineProperty(doc, 'defaultView', {
      configurable: true,
      get: () => inertWindow,
    })
    // Some shims define document without any listener API at all.
    if (typeof doc.addEventListener !== 'function') {
      doc.addEventListener = noop
      doc.removeEventListener = noop
    }
    if (!g.window) g.window = inertWindow
  },
})
