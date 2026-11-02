<template>
  <UBadge :color="color" variant="subtle" class="gap-1.5">
    <span class="inline-block size-1.5 rounded-full bg-current" aria-hidden="true"></span>
    {{ label }}
  </UBadge>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  status: string
  label: string
}>()

const color = computed<'success' | 'warning' | 'error' | 'neutral'>(() => {
  switch (props.status) {
    case 'active':
    case 'success':
      return 'success'
    case 'disabled':
    case 'inactive':
    case 'warning':
      return 'warning'
    case 'error':
    case 'danger':
      return 'error'
    default:
      return 'neutral'
  }
})
</script>
