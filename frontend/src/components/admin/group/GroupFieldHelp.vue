<template>
  <UPopover :content="{ side: 'top', align: 'start' }" arrow>
    <UButton
      icon="i-lucide-circle-help"
      color="neutral"
      variant="link"
      size="xs"
      square
      class="text-dimmed hover:text-default"
      :aria-label="label"
    />
    <template #content>
      <div class="max-w-72 space-y-1.5 p-3 text-xs leading-relaxed">
        <p v-if="title" class="font-medium text-highlighted">{{ title }}</p>
        <p v-if="text" class="text-muted">{{ text }}</p>
        <slot />
      </div>
    </template>
  </UPopover>
</template>

<script setup lang="ts">
// Click-to-open help for a group form field (replaces the old hover-only tooltip bubbles).
defineProps<{
  label: string
  title?: string
  text?: string
}>()
</script>
