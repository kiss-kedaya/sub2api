<template>
  <span class="animated-number">{{ display }}</span>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  value: number
  // 格式化函数：把中间值渲染成用户可读文本（货币/Token 数等）
  format?: (value: number) => string
  duration?: number
  decimals?: number
}>(), {
  format: undefined,
  duration: 640,
  decimals: 2,
})

const progress = ref(1)
let raf = 0
let fromValue = 0

const display = computed(() => {
  const current = fromValue + (props.value - fromValue) * easeOutExpo(progress.value)
  if (props.format) return props.format(current)
  return current.toFixed(props.decimals)
})

function easeOutExpo(x: number) {
  return x >= 1 ? 1 : 1 - Math.pow(2, -10 * x)
}

watch(
  () => props.value,
  (next, prev) => {
    fromValue = Number.isFinite(prev) ? prev : next
    const start = performance.now()
    cancelAnimationFrame(raf)
    const tick = (now: number) => {
      const elapsed = now - start
      progress.value = props.duration <= 0 ? 1 : Math.min(elapsed / props.duration, 1)
      if (progress.value < 1) {
        raf = requestAnimationFrame(tick)
      }
    }
    raf = requestAnimationFrame(tick)
  },
  { flush: 'post' },
)

onBeforeUnmount(() => cancelAnimationFrame(raf))
</script>

<script lang="ts">
export default { name: 'AnimatedNumber' }
</script>
