<template>
  <UPopover :content="{ align: 'start', side: 'bottom' }">
    <UButton icon="i-lucide-info" color="neutral" variant="ghost" size="xs" square :aria-label="label" :title="label" class="align-middle text-dimmed" />
    <template #content>
      <p class="max-w-sm p-3 text-sm text-pretty text-muted">{{ text }}</p>
    </template>
  </UPopover>
</template>

<script setup lang="ts">
// Long help text behind an info button (UTooltip needs UApp's provider, a popover does not).
defineProps<{ text: string; label: string }>()
</script>
