<template>
  <UBadge :color="color" variant="subtle" size="sm" class="ticket-status shrink-0" :data-status="status">{{ t(`tickets.statuses.${status}`) }}</UBadge>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TicketStatus } from '@/api/tickets'
const props = defineProps<{ status: TicketStatus }>()
const { t } = useI18n()
// open = needs attention, in progress = being handled, waiting = on the user, resolved = done.
const color = computed(() => ({
  open: 'warning',
  in_progress: 'info',
  waiting_user: 'primary',
  resolved: 'success',
  closed: 'neutral'
} as const)[props.status] ?? 'neutral')
</script>
