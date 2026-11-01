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

function update() {
  if (!document.documentElement.classList.contains('console-petals-ready')) return
  if (window.matchMedia('(max-width: 639px), (prefers-reduced-motion: reduce)').matches) {
    petals.value = []
    return
  }
  const surfaces = [...document.querySelectorAll<HTMLElement>(
    '.signal-main [data-slot="root"].rounded-lg.ring.bg-default, .signal-main .settings-section > [data-slot="root"]'
  )].filter(element => element.offsetWidth > 0 && element.offsetHeight > 0).map(rectOf)
  const blockers = [...document.querySelectorAll<HTMLElement>(
    '.signal-main :is(h1,h2,h3,h4,p,label,button,a,input,textarea,select,[role="button"],[role="switch"])'
  )].filter(element => element.offsetWidth > 0 && element.offsetHeight > 0).map(rectOf)
  const tree = document.querySelector('.ink-sakura')
  const excluded = tree ? [rectOf(tree)] : []
  const points = findSakuraPetalPlacements(surfaces, blockers, excluded, ++generation, 7)
  petals.value = points.map((point, index) => ({
    id: index,
    style: {
      left: `${point.x}px`, top: `${point.y}px`, width: `${point.size}px`, height: `${point.size}px`,
      '--wind-x': `${point.drift}px`, '--petal-rotate': `${point.rotation}deg`,
      '--fall-time': `${point.duration}s`, '--fall-delay': `${point.delay}s`,
    },
  }))
}

function scheduleUpdate() {
  window.clearTimeout(updateTimer)
  updateTimer = window.setTimeout(update, 240)
}

onMounted(() => {
  mainElement = document.querySelector<HTMLElement>('.signal-main')
  if (!mainElement) return
  observer = new ResizeObserver(scheduleUpdate)
  observer.observe(mainElement)
  window.addEventListener('resize', scheduleUpdate, { passive: true })
  mainElement.addEventListener('scroll', scheduleUpdate, { passive: true })
  const requestIdle = (window as Window & { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number }).requestIdleCallback
  if (requestIdle) idleId = requestIdle(update, { timeout: 1200 })
  else updateTimer = window.setTimeout(update, 600)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('resize', scheduleUpdate)
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
