<template>
  <div ref="scroller" class="relative min-h-[140px] flex-1 overflow-auto overscroll-contain px-4 pb-6 sm:px-6" tabindex="0" :aria-label="t('tickets.conversation')" @scroll="handleScroll">
    <div class="sticky top-0 z-[2] flex justify-between bg-default py-3 text-xs text-muted" aria-hidden="true">
      <span class="flex items-center gap-1.5"><UIcon :name="admin ? 'i-lucide-user' : 'i-lucide-shield'" class="size-3.5" />{{ t(admin ? 'tickets.user' : 'tickets.team') }}</span>
      <span class="flex items-center gap-1.5"><UIcon :name="admin ? 'i-lucide-shield' : 'i-lucide-user'" class="size-3.5" />{{ t(admin ? 'tickets.admin' : 'tickets.you') }}</span>
    </div>
    <div v-if="hasMore" class="timeline-older mb-4 text-center">
      <UButton variant="link" size="sm" :icon="loadingOlder ? 'i-lucide-loader-circle' : 'i-lucide-chevron-up'" :ui="{ leadingIcon: loadingOlder ? 'animate-spin' : '' }" :disabled="loadingOlder" @click="emit('older')">
        {{ t('tickets.older') }}
      </UButton>
    </div>
    <ol class="m-0 flex list-none flex-col gap-4 p-0" role="log" aria-live="polite" :aria-label="t('tickets.conversation')">
      <li
        v-for="message in visibleMessages"
        :key="message.id"
        class="timeline-entry flex"
        :class="message.kind === 'event' ? 'timeline-event justify-center' : isSelf(message) ? 'timeline-self justify-end' : 'timeline-other justify-start'"
        :data-message-id="message.id"
        :data-author-role="message.author_role"
      >
        <template v-if="message.kind === 'event'">
          <p class="flex max-w-[92%] flex-wrap items-center justify-center gap-x-2 text-center text-xs text-muted">
            <span class="break-words">{{ eventText(message) }}</span>
            <time class="tabular-nums text-dimmed" :datetime="message.created_at" :title="fullTime(message.created_at)">{{ fullTime(message.created_at) }}</time>
          </p>
        </template>
        <article
          v-else
          class="min-w-0 max-w-[85%] rounded-lg px-3.5 py-2.5 sm:max-w-[75%]"
          :class="isSelf(message) ? 'bg-primary/10' : 'bg-elevated'"
        >
          <div class="mb-1 flex flex-wrap items-baseline gap-x-2 break-words">
            <strong class="text-xs font-semibold text-highlighted">{{ author(message) }}</strong>
            <span class="text-xs text-muted">{{ t(message.author_role === 'admin' ? 'tickets.admin' : 'tickets.user') }}</span>
          </div>
          <p class="m-0 whitespace-pre-wrap break-words text-sm leading-relaxed text-default">{{ message.content }}</p>
          <time class="mt-1.5 block text-xs tabular-nums text-dimmed" :datetime="message.created_at" :title="fullTime(message.created_at)">{{ fullTime(message.created_at) }}</time>
        </article>
      </li>
    </ol>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
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
