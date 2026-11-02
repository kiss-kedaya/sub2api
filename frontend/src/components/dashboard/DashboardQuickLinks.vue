<template>
  <nav class="grid grid-cols-2 gap-3" :class="items.length > 4 ? 'lg:grid-cols-5' : 'lg:grid-cols-4'" :aria-label="label">
    <UPageCard
      v-for="item in items"
      :key="item.to"
      :to="item.to"
      :icon="item.icon"
      :title="item.label"
      variant="outline"
      :ui="linkUi"
    />
  </nav>
</template>

<script setup lang="ts">
// Quick actions as quiet link cards: icon and label on one line, hover tint, no box art.
import type { DashboardQuickLink } from './dashboardUi'

defineProps<{
  items: DashboardQuickLink[]
  label: string
}>()

const linkUi = {
  container: 'flex-row lg:flex items-center gap-3 p-3 sm:p-3',
  wrapper: 'flex-row items-center gap-3 min-w-0',
  leading: 'mb-0',
  leadingIcon: 'size-4 text-muted',
  body: 'min-w-0',
  title: 'truncate text-sm font-medium text-highlighted',
}
</script>
