/**
 * Keeps the current entry of a sideways-scrolling nav strip on screen.
 *
 * Moves the strip only (its own scrollLeft), never the page, so it is safe to call on every
 * route change. Does nothing when the strip fits, or when the current entry is already in
 * view with a small margin. Returns true when the strip was moved.
 */
const CURRENT_ENTRY = '[aria-current="page"], [aria-selected="true"]'
const EDGE_MARGIN = 16

export function revealActiveNavItem(strip: HTMLElement | null | undefined, options: { smooth?: boolean } = {}): boolean {
  if (!strip || strip.scrollWidth <= strip.clientWidth) return false
  const current = strip.querySelector<HTMLElement>(CURRENT_ENTRY)
  if (!current) return false

  const box = strip.getBoundingClientRect()
  const item = current.getBoundingClientRect()
  let delta = 0
  if (item.left < box.left + EDGE_MARGIN) delta = item.left - box.left - EDGE_MARGIN
  else if (item.right > box.right - EDGE_MARGIN) delta = item.right - box.right + EDGE_MARGIN
  if (delta === 0) return false

  const target = Math.min(strip.scrollWidth - strip.clientWidth, Math.max(0, strip.scrollLeft + delta))
  if (target === strip.scrollLeft) return false

  const calm = window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches === true
  if (options.smooth && !calm && typeof strip.scrollTo === 'function') strip.scrollTo({ left: target, behavior: 'smooth' })
  else strip.scrollLeft = target
  return true
}
