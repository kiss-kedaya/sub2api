<template>
  <UPageCard :icon="icon" :title="title" variant="subtle" :ui="statCardUi">
    <template v-if="$slots.title" #title><slot name="title" /></template>
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      <span class="text-2xl font-semibold text-highlighted">
        <slot name="value">{{ value }}</slot>
      </span>
      <slot name="badge" />
    </div>
    <div v-if="$slots.sub" class="min-w-0 text-xs text-muted tabular-nums">
      <slot name="sub" />
    </div>
  </UPageCard>
</template>

<script setup lang="ts">
// One tile of a dashboard stat grid, as the template's HomeStats card. The grid and the
// corner classes come from the parent (see statCorners); class falls through to UPageCard.
import { statCardUi } from './dashboardUi'

defineProps<{
  icon: string
  title?: string
  value?: string | number
}>()
</script>
