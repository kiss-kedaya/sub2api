<template>
  <div class="flex items-center">
    <UBadge :color="statusColor" variant="subtle" icon="i-lucide-layout-grid" class="font-mono tabular-nums">
      {{ current }}<span class="opacity-60">/</span>{{ max }}
    </UBadge>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  current: number
  max: number
}>()

// Status colour based on usage: full = error, in use = warning, idle = neutral.
const statusColor = computed(() => {
  const { current, max } = props
  if (current >= max && max > 0) return 'error' as const
  if (current > 0) return 'warning' as const
  return 'neutral' as const
})
</script>
