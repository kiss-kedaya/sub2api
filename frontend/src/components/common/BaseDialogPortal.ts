import { Teleport, defineComponent, h } from 'vue'

/**
 * BaseDialog's portal to <body>. The slot's vnodes are handed to Teleport as they are (no <slot>
 * fragment), so <body> receives the overlay element itself.
 *
 * Named like Vue's Teleport, as reka-ui's own portal wrapper is: a spec's `stubs: { teleport: true }`
 * then replaces this component with the stable in-place stub from src/__tests__/setup.ts. Vue Test
 * Utils' own Teleport stub is rebuilt on every render, which would remount the Nuxt UI modal (and the
 * inputs in it) whenever a dialog re-renders.
 */
export default defineComponent({
  name: 'Teleport',
  setup(_props, { slots }) {
    return () => h(Teleport, { to: 'body' }, slots.default?.() ?? [])
  }
})
