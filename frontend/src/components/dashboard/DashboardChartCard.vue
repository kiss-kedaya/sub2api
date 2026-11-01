<template>
  <UCard :ui="{ root: 'min-w-0 overflow-visible', body: 'p-0 sm:p-0 overflow-hidden rounded-b-[inherit]' }">
    <template #header>
      <!-- With a big number the actions sit at its top; a title-only header centres them. -->
      <div class="flex flex-wrap justify-between gap-3" :class="value !== undefined ? 'items-start' : 'items-center'">
        <div class="min-w-0">
          <p class="text-xs uppercase text-muted" :class="{ 'mb-1.5': value !== undefined }">{{ title }}</p>
          <p v-if="value !== undefined" class="text-3xl font-semibold text-highlighted">{{ value }}</p>
        </div>
        <div v-if="$slots.actions" class="flex min-w-0 flex-wrap items-center gap-2">
          <slot name="actions" />
        </div>
      </div>
    </template>
    <slot />
  </UCard>
</template>

<script setup lang="ts">
// Chart card with the template's HomeChart header: small uppercase title over one big number.
defineProps<{
  title: string
  value?: string | number
}>()
</script>
