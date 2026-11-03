<template>
  <div v-if="petals.length" class="sakura-petal-field" aria-hidden="true">
    <svg
      v-for="petal in petals"
      :key="petal.id"
      class="sakura-falling-flower"
      viewBox="-12 -12 24 24"
      :style="petal.style"
      focusable="false"
    >
      <g v-for="(path, index) in petalPaths" :key="index" :transform="`rotate(${index * 72})`">
        <path :d="path" />
      </g>
      <circle r="1.05" />
    </svg>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { findSakuraPetalPlacements, type SakuraRect } from './sakuraPetalPlacement'
import { petalPathData } from './inkSakura/blossom'

const petalPaths = Array.from({ length: 5 }, () => petalPathData(8, 6, 1.3))

const petals = ref<Array<{ id: number; style: Record<string, string> }>>([])
let observer: ResizeObserver | undefined
let idleId = 0
let updateTimer = 0
let generation = 0
let mainElement: HTMLElement | null = null

const rectOf = (element: Element): SakuraRect => {
  const rect = element.getBoundingClientRect()
  return { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom }
}

// Measuring every card, label, button and link on the page is a full-document layout.
// It used to run again 240ms after each scroll tick, which is why long tables stuttered
// while scrolling. The rects only move with scrolling, so they are measured once and
// shifted by the scroll delta until the page layout actually changes.
interface SakuraLayout {
  scrollTop: number
  /** Scroll height the rects were measured at; content growth invalidates them. */
  contentHeight: number
  surfaces: SakuraRect[]
  blockers: SakuraRect[]
  excluded: SakuraRect[]
}

let layout: SakuraLayout | null = null

const shiftRect = (rect: SakuraRect, dy: number): SakuraRect => ({
  left: rect.left,
  top: rect.top - dy,
  right: rect.right,
  bottom: rect.bottom - dy,
})

function measureLayout(): SakuraLayout {
  const visible = (element: HTMLElement) => element.offsetWidth > 0 && element.offsetHeight > 0
  const surfaces = [...document.querySelectorAll<HTMLElement>(
    '.signal-main [data-slot="root"].rounded-lg.ring.bg-default, .signal-main .settings-section > [data-slot="root"]'
  )].filter(visible).map(rectOf)
  const blockers = [...document.querySelectorAll<HTMLElement>(
    '.signal-main :is(h1,h2,h3,h4,p,label,button,a,input,textarea,select,[role="button"],[role="switch"])'
  )].filter(visible).map(rectOf)
  const tree = document.querySelector('.ink-sakura')
  const excluded = tree ? [rectOf(tree)] : []
  return {
    scrollTop: mainElement?.scrollTop ?? 0,
    contentHeight: mainElement?.scrollHeight ?? 0,
    surfaces,
    blockers,
    excluded,
  }
}

function render(snapshot: SakuraLayout) {
  const dy = (mainElement?.scrollTop ?? 0) - snapshot.scrollTop
  const viewportHeight = window.innerHeight
  // Only the cards that are on screen can host a petal; the rest would land off screen.
  const surfaces = snapshot.surfaces
    .map(rect => shiftRect(rect, dy))
    .filter(rect => rect.bottom > 0 && rect.top < viewportHeight)
  const points = findSakuraPetalPlacements(
    surfaces,
    snapshot.blockers.map(rect => shiftRect(rect, dy)),
    snapshot.excluded.map(rect => shiftRect(rect, dy)),
    ++generation,
    7,
  )
  petals.value = points.map((point, index) => ({
    id: index,
    style: {
      left: `${point.x}px`, top: `${point.y}px`, width: `${point.size}px`, height: `${point.size}px`,
      '--wind-x': `${point.drift}px`, '--petal-rotate': `${point.rotation}deg`,
      '--fall-time': `${point.duration}s`, '--fall-delay': `${point.delay}s`,
    },
  }))
}

function update() {
  if (!document.documentElement.classList.contains('console-petals-ready')) return
  if (document.visibilityState === 'hidden') return
  if (window.matchMedia('(max-width: 639px), (prefers-reduced-motion: reduce)').matches) {
    petals.value = []
    return
  }
  // One scrollHeight read per settled update is enough to notice that rows appeared
  // or a card grew; the expensive rect pass only runs when that happened.
  const contentHeight = mainElement?.scrollHeight ?? 0
  if (!layout || layout.contentHeight !== contentHeight) layout = measureLayout()
  render(layout)
}

/** Re-measure the page before the next placement (layout changed, not just scrolled). */
function invalidateLayout() {
  layout = null
}

function scheduleUpdate() {
  window.clearTimeout(updateTimer)
  updateTimer = window.setTimeout(update, 240)
}

function onWindowResize() {
  invalidateLayout()
  scheduleUpdate()
}

onMounted(() => {
  mainElement = document.querySelector<HTMLElement>('.signal-main')
  if (!mainElement) return
  // The scroll container's size changes when a table re-renders its rows, so the cached
  // rects are only valid until the next ResizeObserver callback.
  observer = new ResizeObserver(() => {
    invalidateLayout()
    scheduleUpdate()
  })
  observer.observe(mainElement)
  window.addEventListener('resize', onWindowResize, { passive: true })
  mainElement.addEventListener('scroll', scheduleUpdate, { passive: true })
  const requestIdle = (window as Window & { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number }).requestIdleCallback
  if (requestIdle) idleId = requestIdle(update, { timeout: 1200 })
  else updateTimer = window.setTimeout(update, 600)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('resize', onWindowResize)
  mainElement?.removeEventListener('scroll', scheduleUpdate)
  window.clearTimeout(updateTimer)
  const cancelIdle = (window as Window & { cancelIdleCallback?: (id: number) => void }).cancelIdleCallback
  if (idleId) cancelIdle?.(idleId)
})
</script>

<style scoped>
.sakura-petal-field { position: fixed; inset: 0; z-index: 1; pointer-events: none; overflow: hidden; }
.sakura-falling-flower {
  position: fixed;
  z-index: 1;
  overflow: visible;
  opacity: 0;
  fill: color-mix(in oklab, var(--ui-primary) 55%, transparent);
  animation: sakura-fall var(--fall-time) ease-in-out var(--fall-delay) infinite;
  will-change: transform;
}
.sakura-falling-flower circle { fill: color-mix(in oklab, var(--ui-primary) 70%, transparent); }
@keyframes sakura-fall {
  0% { opacity: 0; transform: translate3d(0,-8px,0) rotate(var(--petal-rotate)); }
  12%, 82% { opacity: 0.42; }
  100% { opacity: 0; transform: translate3d(var(--wind-x),44px,0) rotate(calc(var(--petal-rotate) + 110deg)); }
}
@media (max-width: 639px), (prefers-reduced-motion: reduce) { .sakura-petal-field { display: none; } }
</style>
