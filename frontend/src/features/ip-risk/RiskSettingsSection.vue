<template>
  <div @invalid.capture="open = true">
    <UCollapsible v-model:open="open" :unmount-on-hide="false">
      <UButton
        color="neutral"
        variant="ghost"
        block
        trailing-icon="i-lucide-chevron-down"
        class="px-0 py-2 font-medium text-highlighted hover:bg-transparent active:bg-transparent"
        :ui="{ trailingIcon: ['transition-transform duration-200', open ? 'rotate-180' : ''] }"
      >
        <span class="flex min-w-0 flex-1 items-center justify-between gap-3 text-left">
          <span class="truncate">{{ title }}</span>
          <UBadge v-if="status" color="neutral" variant="subtle" size="sm" class="shrink-0">{{ status }}</UBadge>
        </span>
      </UButton>
      <template #content>
        <div class="flex flex-col gap-4 pt-2 pb-1"><slot /></div>
      </template>
    </UCollapsible>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

// Collapsible block of IPRiskSettings. It opens itself when a field inside fails native validation (capture
// `invalid`), like the <details> it replaces. Keep a single root element: attrs such as data-testid fall through.
const props = defineProps<{ title: string; status?: string; initiallyOpen?: boolean }>()
const open = ref(!!props.initiallyOpen)
</script>
