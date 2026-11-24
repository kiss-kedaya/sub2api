/**
 * Height + opacity disclosure motion for a block that is toggled with v-show (it stays mounted, so field state,
 * validation and focus order are untouched). Meant for `<Transition :css="false">` hooks:
 *
 *   <Transition :css="false" @enter="expandHeight" @leave="collapseHeight"
 *               @enter-cancelled="cancelCollapseMotion" @leave-cancelled="cancelCollapseMotion">
 *     <div v-show="open">…</div>
 *   </Transition>
 *
 * Why height at all (a disclosure is the one place the layout really has to move): 220 ms, ease-out, plays once per
 * click, content below the block is pushed down smoothly instead of jumping by the block's whole height.
 *
 * Interruptible: when the user reverses mid-way, Vue first calls the `*-cancelled` hook and then the opposite hook in
 * the same tick, so the first one leaves the height the block had a moment ago for the second to continue from.
 * The `cancel` event of a Web Animation is queued, never synchronous, so nothing here waits for it.
 * `prefers-reduced-motion` and engines without Web Animations finish immediately (no animation, same end state).
 */
export const COLLAPSE_MS = 220
export const COLLAPSE_EASE = 'cubic-bezier(0.23, 1, 0.32, 1)'
/** How long a height left behind by a cancelled motion may be picked up by the motion that follows it. */
const MIDWAY_TTL_MS = 50

const running = new WeakMap<Element, Animation>()
/** `overflow` as it was before the first motion of a chain; restored only when the last motion of the chain ends. */
const original = new WeakMap<Element, string>()
const midway = new WeakMap<Element, { height: number; at: number }>()

function reducedMotion(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function restoreOverflow(node: HTMLElement): void {
  if (!original.has(node)) return
  node.style.overflow = original.get(node) ?? ''
  original.delete(node)
}

function play(el: Element, direction: 'open' | 'close', done: () => void): void {
  const node = el as HTMLElement

  // Height to continue from when this call reverses a motion that was interrupted.
  let from: number | null = null
  const recorded = midway.get(node)
  midway.delete(node)
  if (recorded && performance.now() - recorded.at < MIDWAY_TTL_MS) from = recorded.height
  const live = running.get(node)
  if (live) {
    from = from ?? node.getBoundingClientRect().height
    running.delete(node)
    live.cancel()
  }

  if (reducedMotion() || typeof node.animate !== 'function') {
    restoreOverflow(node)
    done()
    return
  }

  const full = node.scrollHeight
  const start = from ?? (direction === 'open' ? 0 : full)
  const end = direction === 'open' ? full : 0
  if (!original.has(node)) original.set(node, node.style.overflow)
  node.style.overflow = 'hidden'
  const animation = node.animate(
    [
      { height: `${start}px`, opacity: direction === 'open' ? 0 : 1 },
      { height: `${end}px`, opacity: direction === 'open' ? 1 : 0 },
    ],
    { duration: COLLAPSE_MS, easing: COLLAPSE_EASE },
  )
  running.set(node, animation)
  animation.onfinish = () => {
    if (running.get(node) !== animation) return // superseded by a reversal: that motion owns `done` and the overflow
    running.delete(node)
    restoreOverflow(node)
    done()
  }
}

export const expandHeight = (el: Element, done: () => void): void => play(el, 'open', done)
export const collapseHeight = (el: Element, done: () => void): void => play(el, 'close', done)

export function cancelCollapseMotion(el: Element): void {
  const node = el as HTMLElement
  const animation = running.get(node)
  if (!animation) return
  midway.set(node, { height: node.getBoundingClientRect().height, at: performance.now() })
  running.delete(node)
  animation.cancel()
  // The reversal runs synchronously right after this and takes over; if nothing follows, put the overflow back.
  queueMicrotask(() => {
    if (!running.has(node)) restoreOverflow(node)
  })
}
