<template>
  <div ref="scroller" class="ticket-timeline-scroll" tabindex="0" :aria-label="t('tickets.conversation')" @scroll="handleScroll">
    <div class="timeline-participants" aria-hidden="true">
      <span><Icon :name="admin ? 'user' : 'shield'" size="sm" />{{ t(admin ? 'tickets.user' : 'tickets.team') }}</span>
      <span><Icon :name="admin ? 'shield' : 'user'" size="sm" />{{ t(admin ? 'tickets.admin' : 'tickets.you') }}</span>
    </div>
    <div v-if="hasMore" class="timeline-older">
      <button type="button" class="ticket-text-button" :disabled="loadingOlder" @click="emit('older')">
        <Icon :name="loadingOlder ? 'refresh' : 'chevronUp'" size="sm" :class="{ 'animate-spin': loadingOlder }" />{{ t('tickets.older') }}
      </button>
    </div>
    <ol class="ticket-timeline" role="log" aria-live="polite" :aria-label="t('tickets.conversation')">
      <li v-for="message in visibleMessages" :key="message.id" class="timeline-entry" :class="message.kind === 'event' ? 'timeline-event' : isSelf(message) ? 'timeline-self' : 'timeline-other'" :data-message-id="message.id" :data-author-role="message.author_role">
        <template v-if="message.kind === 'event'">
          <span class="timeline-event-label">{{ eventText(message) }}</span>
          <time :datetime="message.created_at" :title="fullTime(message.created_at)">{{ fullTime(message.created_at) }}</time>
        </template>
        <template v-else>
          <div class="timeline-marker" aria-hidden="true"><Icon :name="message.author_role === 'admin' ? 'shield' : 'user'" size="sm" /></div>
          <article class="timeline-message">
            <div class="timeline-author">
              <strong>{{ author(message) }}</strong>
              <span>{{ t(message.author_role === 'admin' ? 'tickets.admin' : 'tickets.user') }}</span>
            </div>
            <p>{{ message.content }}</p>
            <time :datetime="message.created_at" :title="fullTime(message.created_at)">{{ fullTime(message.created_at) }}</time>
          </article>
        </template>
      </li>
    </ol>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { TicketMessage } from '@/api/tickets'

const props = defineProps<{ messages: TicketMessage[]; admin: boolean; hasMore: boolean; loadingOlder: boolean }>()
const emit = defineEmits<{ older: []; bottom: [value: boolean] }>()
const { t, locale } = useI18n()
const scroller = ref<HTMLElement | null>(null)
const visibleMessages = computed(() => props.messages.filter(message => {
  if (message.kind !== 'event') return true
  if (message.event_type === 'assignment_changed' || message.event_type === 'priority_changed') return false
  if (message.event_type === 'status_changed') return message.event_data?.from === 'closed' || message.event_data?.to === 'closed'
  return true
}))
const isSelf = (message: TicketMessage) => message.author_role === (props.admin ? 'admin' : 'user')
const author = (message: TicketMessage) => {
  if (!props.admin && message.author_role === 'admin') return t('tickets.team')
  return message.author_name || t(isSelf(message) ? 'tickets.you' : 'tickets.user')
}
const eventText = (message: TicketMessage) => {
  if (message.event_type === 'status_changed') {
    return t(message.event_data?.to === 'closed' ? 'tickets.closed' : 'tickets.reopened')
  }
  return message.content
}
const fullTime = (value: string) => new Intl.DateTimeFormat(locale.value === 'zh' ? 'zh-CN' : 'en-GB', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false
}).format(new Date(value))

function handleScroll() {
  const el = scroller.value
  if (el) emit('bottom', el.scrollHeight - el.scrollTop - el.clientHeight < 64)
}
async function scrollToBottom(smooth = false) {
  await nextTick()
  const el = scroller.value
  if (!el) return
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  el.scrollTo({ top: el.scrollHeight, behavior: smooth && !reduced ? 'smooth' : 'instant' })
  handleScroll()
}
function capturePosition() {
  const el = scroller.value
  return el ? { height: el.scrollHeight, top: el.scrollTop } : null
}
async function restorePosition(position: ReturnType<typeof capturePosition>) {
  await nextTick()
  const el = scroller.value
  if (el && position) el.scrollTop = position.top + el.scrollHeight - position.height
}
defineExpose({ scrollToBottom, capturePosition, restorePosition })
</script>

<style scoped>
.ticket-timeline-scroll { position: relative; min-height: 140px; flex: 1; overflow: auto; overscroll-behavior: contain; padding: 0 20px 28px; scrollbar-gutter: stable; }
.timeline-participants { position: sticky; top: 0; z-index: 2; display: flex; justify-content: space-between; padding: 12px 0; background: var(--signal-bg); color: var(--signal-muted); font-size: 12px; }
.timeline-participants > span { display: flex; align-items: center; gap: 6px; }
.timeline-older { position: relative; z-index: 1; text-align: center; margin: 0 0 18px; }
.ticket-timeline { position: relative; display: flex; flex-direction: column; gap: 22px; margin: 4px 0 0; padding: 0; list-style: none; }
.ticket-timeline::before { content: ''; position: absolute; top: 0; bottom: 0; left: 50%; width: 1px; background: var(--signal-line); }
.timeline-entry { position: relative; display: grid; grid-template-columns: minmax(0, 1fr) 38px minmax(0, 1fr); align-items: start; animation: ticket-entry-in 180ms ease-out; }
.timeline-marker { grid-column: 2; grid-row: 1; justify-self: center; display: grid; place-items: center; width: 26px; height: 26px; border: 1px solid var(--signal-line); border-radius: 50%; background: var(--signal-surface); color: var(--signal-muted); box-shadow: 0 0 0 5px var(--signal-bg); margin-top: 8px; }
.timeline-self .timeline-marker { color: var(--signal-accent); border-color: var(--signal-accent); }
.timeline-message { grid-row: 1; grid-column: 1; min-width: 0; padding: 14px; border: 1px solid var(--signal-line); border-radius: 8px; background: var(--signal-surface); box-shadow: 0 1px 2px #00000004; }
.timeline-self .timeline-message { grid-column: 3; background: var(--signal-accent-soft); border-color: color-mix(in srgb, var(--signal-accent) 22%, var(--signal-line)); }
.timeline-author { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; margin-bottom: 8px; overflow-wrap: anywhere; }
.timeline-author strong { font-size: 12px; font-weight: 600; }
.timeline-author span { font-size: 11px; color: var(--signal-muted); }
.timeline-message p { margin: 0 0 12px; font-size: 14px; line-height: 1.8; white-space: pre-wrap; overflow-wrap: anywhere; }
.timeline-entry time { display: block; color: var(--signal-muted); font-size: 11px; font-variant-numeric: tabular-nums; line-height: 1.6; }
.timeline-event { display: flex; align-items: center; flex-direction: column; gap: 3px; text-align: center; }
.timeline-event-label { max-width: 92%; padding: 5px 10px; background: var(--signal-bg); color: var(--signal-muted); font-size: 11px; overflow-wrap: anywhere; }
.timeline-event time { padding-inline: 8px; background: var(--signal-bg); }
@keyframes ticket-entry-in { from { opacity: 0; transform: translateY(5px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) { .timeline-entry { animation: none; } }
@media (max-width: 640px) {
  .ticket-timeline-scroll { padding: 0 10px 20px; }
  .timeline-entry { grid-template-columns: minmax(0, 1fr) 28px minmax(0, 1fr); }
  .timeline-message { padding: 10px; }
  .timeline-message p { font-size: 13px; line-height: 1.7; }
  .timeline-marker { width: 22px; height: 22px; box-shadow: 0 0 0 3px var(--signal-bg); }
  .ticket-timeline { gap: 18px; }
}
</style>
