import { onBeforeUnmount, onMounted, watch, type Ref } from 'vue'
import { useRoute } from 'vue-router'

/**
 * The dashboard panel body scrolls instead of the window, so the router's scrollBehavior
 * no longer applies. Every page mounts its own AppLayout; this keeps what the router did:
 * new pages start at the top, back / forward returns to where the page was left.
 */
const saved = new Map<string, number>()
let popped = false

if (typeof window !== 'undefined') {
  window.addEventListener('popstate', () => {
    popped = true
  })
}

// vue-router keeps the entry position and path in history.state.
function entryKey(): string {
  const state = (typeof history !== 'undefined' ? history.state : null) as { position?: number; current?: string } | null
  return state ? `${state.position ?? ''}:${state.current ?? ''}` : ''
}

export function usePanelScroll(container: Readonly<Ref<HTMLElement | null>>) {
  const route = useRoute()
  let frame = 0

  const onScroll = () => {
    const el = container.value
    if (el) saved.set(entryKey(), el.scrollTop)
  }

  function place() {
    const el = container.value
    if (!el) return
    const top = popped ? saved.get(entryKey()) ?? 0 : 0
    popped = false
    el.scrollTop = top
    if (top <= 0 || typeof requestAnimationFrame !== 'function') return
    // Data loads after mount; keep the position until the page is tall enough (up to 1s).
    const until = performance.now() + 1000
    const settle = () => {
      if (!container.value || container.value.scrollTop >= top - 1 || performance.now() > until) return
      container.value.scrollTop = top
      frame = requestAnimationFrame(settle)
    }
    frame = requestAnimationFrame(settle)
  }

  onMounted(() => {
    container.value?.addEventListener('scroll', onScroll, { passive: true })
    place()
  })

  // A route change that keeps this layout (same page component) starts at the top too.
  watch(() => route.path, place, { flush: 'post' })

  onBeforeUnmount(() => {
    cancelAnimationFrame(frame)
    container.value?.removeEventListener('scroll', onScroll)
  })
}
