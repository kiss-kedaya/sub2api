<template>
  <details class="group rounded-xl border border-gray-200 dark:border-dark-700" :open="initiallyOpen">
    <summary class="flex cursor-pointer list-none items-center justify-between gap-3 rounded-xl px-4 py-3 font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 [&::-webkit-details-marker]:hidden">
      <span>{{ title }}</span>
      <span class="flex shrink-0 items-center gap-2">
        <span v-if="status" class="text-xs font-normal text-gray-500 dark:text-gray-400">{{ status }}</span>
        <Icon name="chevronDown" size="sm" class="transition-transform group-open:rotate-180" />
      </span>
    </summary>
    <div class="space-y-3 border-t border-gray-100 p-4 dark:border-dark-700"><slot /></div>
  </details>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'

defineProps<{ title: string; status?: string; initiallyOpen?: boolean }>()
</script>
