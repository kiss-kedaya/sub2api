<template>
  <canvas
    ref="canvasRef"
    class="ink-sakura"
    :class="{ 'ink-sakura--shown': shown, 'ink-sakura--front': front }"
    aria-hidden="true"
  />
</template>

<script setup lang="ts">
// Ink-wash sakura on the right of the page: in bloom by day, buds closed at night.
// Painted once per theme and size; only the blossoms are redrawn during a theme change.
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { hasPaintedOnce, mountInkSakura } from './inkSakura/scene'

// front: paint above an absolutely positioned page background (auth pages) instead of behind everything.
defineProps<{ front?: boolean }>()

const canvasRef = ref<HTMLCanvasElement | null>(null)
// After the first visit the layers are cached, so later pages show the tree without fading in again.
const shown = ref(hasPaintedOnce())
let stop: (() => void) | null = null

onMounted(() => {
  // jsdom has no canvas; skip instead of logging "not implemented" in unit tests.
  if (!canvasRef.value || /jsdom/i.test(navigator.userAgent)) return
  stop = mountInkSakura(canvasRef.value, () => {
    shown.value = true
  })
})

onBeforeUnmount(() => stop?.())
</script>

<style scoped>
.ink-sakura {
  position: fixed;
  right: 0;
  bottom: 0;
  z-index: -1;
  pointer-events: none;
  user-select: none;
  opacity: 0;
  transition: opacity 900ms ease;
}

.ink-sakura--front {
  z-index: 0;
}

.ink-sakura--shown {
  opacity: 1;
}

@media (max-width: 639px) {
  .ink-sakura {
    display: none;
  }
}

@media print {
  .ink-sakura {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .ink-sakura {
    transition: none;
  }
}
</style>
