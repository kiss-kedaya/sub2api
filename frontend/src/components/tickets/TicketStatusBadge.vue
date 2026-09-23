<template>
  <span class="ticket-status" :data-status="status"><span aria-hidden="true"></span>{{ t(`tickets.statuses.${status}`) }}</span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TicketStatus } from '@/api/tickets'
defineProps<{ status: TicketStatus }>()
const { t } = useI18n()
</script>

<style scoped>
.ticket-status { display: inline-flex; align-items: center; gap: 6px; flex-shrink: 0; font-size: 12px; font-weight: 500; line-height: 20px; color: var(--signal-muted); white-space: nowrap; }
.ticket-status > span { height: 6px; width: 6px; border-radius: 50%; background: currentColor; }
.ticket-status[data-status="open"] { color: #ad640c; }
.ticket-status[data-status="in_progress"] { color: #0066d6; }
.ticket-status[data-status="waiting_user"] { color: #8c5299; }
.ticket-status[data-status="resolved"] { color: #16805c; }
:global(.dark) .ticket-status[data-status="open"] { color: #efbb71; }
:global(.dark) .ticket-status[data-status="in_progress"] { color: #8cbeff; }
:global(.dark) .ticket-status[data-status="waiting_user"] { color: #d0a2dc; }
:global(.dark) .ticket-status[data-status="resolved"] { color: #77d7b5; }
</style>
