<template>
  <AppLayout>
    <section class="tickets-page-root flex min-w-0 flex-col gap-4">
      <PageToolbar>
        <template #left>
          <UTabs v-model="statusTab" :items="statusTabItems" :content="false" size="sm" :aria-label="t('tickets.status')" />
        </template>
        <template #right>
          <UBadge v-if="syncFailed" color="warning" variant="subtle" icon="i-lucide-cloud-off" role="status">{{ t('tickets.syncFailed') }}</UBadge>
          <UBadge v-if="stats.unread" color="neutral" variant="subtle">{{ t('tickets.unread') }} {{ stats.unread }}</UBadge>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            :ui="{ leadingIcon: refreshing ? 'animate-spin' : '' }"
            :disabled="refreshing"
            :title="t('tickets.refresh')"
            :aria-label="t('tickets.refresh')"
            @click="refresh(true)"
          />
          <UButton v-if="!admin" icon="i-lucide-plus" :disabled="stats.can_create === false || loading" @click="newTicketOpen = true">{{ t('tickets.newTicket') }}</UButton>
          <UButton v-if="admin" icon="i-lucide-plus" @click="openProactive">{{ t('tickets.proactiveNew') }}</UButton>
        </template>
      </PageToolbar>

      <p v-if="!admin" class="-mt-1 flex flex-wrap items-center gap-1.5 text-xs" :class="stats.can_create === false ? 'text-warning' : 'text-muted'">
        <UIcon :name="stats.can_create === false ? 'i-lucide-clock' : 'i-lucide-info'" class="size-3.5 shrink-0" />
        <span>{{ t(stats.can_create === false ? 'tickets.dailyUsed' : 'tickets.dailyLimit') }}</span>
        <time v-if="stats.can_create === false && stats.next_create_at" class="ms-auto">{{ t('tickets.nextCreate', { time: quotaTime(stats.next_create_at) }) }}</time>
      </p>

      <div class="tickets-workspace flex h-[calc(100dvh-12rem)] max-h-[1100px] min-h-[560px] overflow-hidden rounded-lg bg-default ring ring-default max-[900px]:min-h-[520px]">
        <aside
          class="flex min-h-0 min-w-0 flex-col border-default min-[901px]:w-[min(340px,32%)] min-[901px]:min-w-[250px] min-[901px]:shrink-0 min-[901px]:border-r max-[900px]:flex-1"
          :class="{ 'max-[900px]:hidden': selectedID }"
          :aria-label="t(admin ? 'tickets.adminTitle' : 'tickets.title')"
        >
          <div class="flex flex-col gap-2 border-b border-default p-3">
            <UInput
              v-model="filters.search"
              type="search"
              icon="i-lucide-search"
              maxlength="160"
              class="w-full"
              :aria-label="t('tickets.search')"
              :placeholder="t(admin ? 'tickets.adminSearch' : 'tickets.search')"
            />
            <Select v-model="filters.category" :options="categoryOptions" :aria-label="t('tickets.category')" size="sm" class="w-full" />
          </div>

          <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain" :aria-busy="loading">
            <div v-if="loading && !items.length" class="divide-y divide-default" role="status" :aria-label="t('tickets.loading')">
              <div v-for="i in 4" :key="i" class="space-y-2.5 px-4 py-5">
                <USkeleton class="h-2.5 w-1/3" />
                <USkeleton class="h-2.5 w-full" />
                <USkeleton class="h-2.5 w-3/5" />
              </div>
            </div>
            <div v-else-if="listError" class="flex min-h-64 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted" role="alert">
              <UIcon name="i-lucide-circle-alert" class="size-6" />
              <p>{{ listError }}</p>
              <UButton variant="link" @click="refresh(true)">{{ t('tickets.retry') }}</UButton>
            </div>
            <div v-else-if="!items.length" class="flex min-h-64 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted">
              <UIcon name="i-lucide-inbox" class="size-8 text-dimmed" />
              <p>{{ t(hasFilters ? 'tickets.emptyFiltered' : 'tickets.empty') }}</p>
              <UButton v-if="hasFilters" variant="link" @click="resetFilters">{{ t('tickets.resetFilters') }}</UButton>
              <UButton v-else-if="!admin && stats.can_create !== false" variant="link" @click="newTicketOpen = true">{{ t('tickets.newTicket') }}</UButton>
            </div>
            <div v-else class="divide-y divide-default">
              <UButton
                v-for="item in items"
                :key="item.id"
                color="neutral"
                variant="ghost"
                class="w-full flex-col items-stretch justify-start gap-1 rounded-none border-l-2 px-4 py-3 text-left text-sm font-normal"
                :class="[
                  selectedID === item.id ? 'border-primary bg-primary/10 hover:bg-primary/10' : 'border-transparent hover:border-primary hover:bg-primary/5',
                  item.unread_count > 0 ? 'text-highlighted' : 'text-toned'
                ]"
                :aria-current="selectedID === item.id ? 'true' : undefined"
                @click="select(item.id)"
              >
                <span class="flex items-center justify-between gap-2 text-xs text-muted">
                  <span class="font-mono tabular-nums">#{{ item.id }}</span>
                  <time :datetime="item.last_message_at" :title="formatDate(item.last_message_at)">{{ shortDate(item.last_message_at) }}</time>
                </span>
                <span class="flex items-start justify-between gap-2">
                  <strong class="line-clamp-2 break-words" :class="item.unread_count > 0 ? 'font-semibold' : 'font-medium'">{{ item.subject }}</strong>
                  <UBadge v-if="item.unread_count" color="primary" size="sm" class="shrink-0 tabular-nums" :aria-label="t('tickets.unread')">{{ item.unread_count > 99 ? '99+' : item.unread_count }}</UBadge>
                </span>
                <span class="line-clamp-1 text-dimmed">{{ item.last_message_preview }}</span>
                <span class="mt-1 flex flex-wrap items-center gap-2">
                  <TicketStatusBadge :status="item.status" />
                  <span class="ms-auto max-w-[50%] truncate text-xs text-muted">{{ admin ? item.user_name || item.user_email : t(`tickets.categories.${item.category}`) }}</span>
                </span>
              </UButton>
            </div>
          </div>

          <footer class="flex items-center justify-between gap-2 border-t border-default px-3 py-2 text-xs text-muted">
            <span class="tabular-nums">{{ t('tickets.page', { page: filters.page, pages }) }}</span>
            <div class="flex gap-0.5">
              <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-chevron-left" :disabled="filters.page <= 1 || loading" :title="t('tickets.previous')" :aria-label="t('tickets.previous')" @click="changePage(filters.page - 1)" />
              <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-chevron-right" :disabled="filters.page >= pages || loading" :title="t('tickets.next')" :aria-label="t('tickets.next')" @click="changePage(filters.page + 1)" />
            </div>
          </footer>
        </aside>

        <section
          class="relative flex min-h-0 min-w-0 flex-1 flex-col"
          :class="{ 'max-[900px]:hidden': !selectedID }"
          :aria-label="t('tickets.selectDetail')"
          :aria-busy="detailLoading"
        >
          <div v-if="!selectedID" class="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
            <UIcon name="i-lucide-messages-square" class="size-16 text-dimmed" />
            <h2 class="text-sm text-muted">{{ t('tickets.selectTicket') }}</h2>
          </div>
          <div v-else-if="detailLoading" class="flex flex-1 flex-col items-center justify-center gap-3 text-sm text-muted" role="status">
            <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin" />
            <p>{{ t('tickets.loading') }}</p>
          </div>
          <div v-else-if="!detail" class="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted" role="alert">
            <UIcon name="i-lucide-circle-alert" class="size-6" />
            <p>{{ detailError || t('tickets.loadFailed') }}</p>
            <div class="flex gap-3">
              <UButton variant="link" @click="refresh(true)">{{ t('tickets.retry') }}</UButton>
              <UButton color="neutral" variant="link" @click="back">{{ t('tickets.back') }}</UButton>
            </div>
          </div>
          <template v-else>
            <header class="max-h-[min(44%,340px)] shrink overflow-y-auto border-b border-default p-4 sm:px-6">
              <div class="flex items-start gap-2">
                <UButton color="neutral" variant="ghost" icon="i-lucide-arrow-left" class="-ms-1.5 min-[901px]:hidden" :title="t('tickets.back')" :aria-label="t('tickets.back')" @click="back" />
                <div class="ticket-detail-title min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-mono text-xs tabular-nums text-muted">#{{ detail.ticket.id }}</span>
                    <TicketStatusBadge :status="detail.ticket.status" />
                  </div>
                  <h2 class="mt-1 break-words text-base font-semibold text-highlighted">{{ detail.ticket.subject }}</h2>
                </div>
                <UButton v-if="detail.ticket.status !== 'closed'" color="neutral" variant="ghost" icon="i-lucide-circle-x" :disabled="mutating" :title="t('tickets.close')" :aria-label="t('tickets.close')" @click="closeDialogOpen = true" />
              </div>
              <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
                <span>{{ t(`tickets.categories.${detail.ticket.category}`) }}</span>
                <time class="ms-auto tabular-nums" :datetime="detail.ticket.created_at">{{ formatDate(detail.ticket.created_at) }}</time>
              </div>
              <template v-if="admin && detail.requester">
                <dl class="ticket-requester-stats mt-3 grid grid-cols-3 gap-3 border-t border-default pt-3 text-sm">
                  <div><dt class="text-xs text-muted">{{ t('tickets.todayTokens') }}</dt><dd class="tabular-nums text-highlighted" :title="detail.requester.usage_stats_available ? undefined : t('tickets.statsUnavailable')">{{ detail.requester.usage_stats_available ? formatTokensK(detail.requester.today_tokens ?? 0) : '—' }}</dd></div>
                  <div><dt class="text-xs text-muted">{{ t('tickets.todayCost') }}</dt><dd class="font-semibold tabular-nums text-highlighted" :title="detail.requester.usage_stats_available ? undefined : t('tickets.statsUnavailable')">{{ detail.requester.usage_stats_available ? formatCurrency(detail.requester.today_cost ?? 0) : '—' }}</dd></div>
                  <div><dt class="text-xs text-muted" :title="t('tickets.rechargeStatsHint')">{{ t('tickets.recharged14d') }}</dt><dd class="font-semibold tabular-nums text-highlighted" :title="detail.requester.recharge_stats_available ? undefined : t('tickets.statsUnavailable')">{{ detail.requester.recharge_stats_available ? formatCurrency(detail.requester.recharged_14d ?? 0) : '—' }}</dd></div>
                </dl>
                <dl class="ticket-requester-info mt-3 grid grid-cols-1 gap-x-5 gap-y-2 border-t border-default pt-3 text-sm sm:grid-cols-2">
                  <div class="flex min-w-0 items-baseline gap-2"><dt class="shrink-0 text-xs text-muted">{{ t('tickets.requester') }}</dt><dd class="min-w-0 break-words text-highlighted">{{ detail.requester.username || detail.ticket.user_name || '#' + detail.requester.id }}<span class="ms-1.5 text-xs text-muted">#{{ detail.requester.id }}</span></dd></div>
                  <div class="flex min-w-0 items-baseline gap-2"><dt class="shrink-0 text-xs text-muted">{{ t('tickets.balance') }}</dt><dd class="font-semibold tabular-nums text-highlighted">{{ formatCurrency(detail.requester.balance) }}</dd></div>
                  <div class="flex min-w-0 items-baseline gap-2"><dt class="shrink-0 text-xs text-muted">{{ t('tickets.contact') }}</dt><dd class="min-w-0 break-words text-highlighted">{{ detail.ticket.contact || t('tickets.noContact') }}</dd></div>
                  <div class="flex min-w-0 items-baseline gap-2"><dt class="shrink-0 text-xs text-muted">{{ t('tickets.accountStatus') }}</dt><dd class="text-highlighted">{{ detail.requester.status === 'active' ? t('tickets.active') : detail.requester.status === 'disabled' ? t('tickets.disabled') : detail.requester.status }}</dd></div>
                  <div class="flex min-w-0 items-baseline gap-2"><dt class="shrink-0 text-xs text-muted">{{ t('common.email') }}</dt><dd class="min-w-0 break-all text-highlighted">{{ detail.requester.email }}</dd></div>
                  <div class="flex min-w-0 items-baseline gap-2"><dt class="shrink-0 text-xs text-muted">{{ t('tickets.registered') }}</dt><dd class="tabular-nums text-highlighted">{{ shortDate(detail.requester.created_at) }}</dd></div>
                </dl>
              </template>
              <p v-else-if="!admin && detail.ticket.contact" class="ticket-user-contact mt-2 flex items-center gap-1.5 break-words text-xs text-muted">
                <UIcon name="i-lucide-circle-user" class="size-3.5 shrink-0" />
                <span>{{ t('tickets.contact') }}: {{ detail.ticket.contact }}</span>
              </p>
            </header>

            <div v-if="detailError" class="flex items-center justify-between gap-2 bg-elevated px-4 py-1.5 text-xs text-highlighted sm:px-6" role="alert">
              {{ detailError }}
              <UButton variant="link" size="xs" @click="refresh(true)">{{ t('tickets.retry') }}</UButton>
            </div>
            <TicketTimeline ref="timeline" :messages="detail.messages" :admin="admin" :has-more="detail.has_more" :loading-older="loadingOlder" @older="loadOlder" @bottom="onBottom" />
            <div v-if="!atBottom && hasNewMessages" class="absolute bottom-48 left-1/2 z-10 -translate-x-1/2">
              <UButton color="neutral" variant="outline" icon="i-lucide-arrow-down" class="bg-default shadow-sm" @click="jumpToLatest">{{ t('tickets.newMessages') }}</UButton>
            </div>

            <footer v-if="detail.ticket.status === 'closed'" class="flex shrink-0 items-center justify-between gap-3 border-t border-default px-4 py-3 sm:px-6">
              <span class="flex items-center gap-2 text-sm text-muted"><UIcon name="i-lucide-lock" class="size-4" />{{ t('tickets.closed') }}</span>
              <UButton color="neutral" variant="outline" icon="i-lucide-rotate-ccw" :disabled="mutating" @click="update({ status: 'open' })">{{ t('tickets.reopen') }}</UButton>
            </footer>
            <div v-else class="shrink-0 px-4 pb-4 sm:px-6">
              <UCard variant="subtle">
                <form @submit.prevent="sendReply" @keydown="handleComposeKey">
                  <label class="sr-only" for="ticket-reply">{{ t('tickets.reply') }}</label>
                  <UTextarea
                    id="ticket-reply"
                    ref="replyInput"
                    v-model="draft"
                    color="neutral"
                    variant="none"
                    autoresize
                    :maxrows="8"
                    :rows="3"
                    maxlength="10000"
                    class="w-full"
                    :ui="{ base: 'p-0 resize-none' }"
                    :disabled="mutating"
                    :placeholder="t('tickets.replyPlaceholder')"
                  />
                  <div class="flex items-center justify-between gap-2 pt-2">
                    <span class="text-xs tabular-nums text-dimmed" aria-live="off">{{ draft.length ? draft.length + ' / 10000' : '' }}</span>
                    <UButton type="submit" :loading="mutating" :icon="mutating ? undefined : 'i-lucide-send'" :disabled="mutating || !draft.trim()">{{ t('tickets.send') }}</UButton>
                  </div>
                </form>
              </UCard>
            </div>
          </template>
        </section>
      </div>
    </section>

    <BaseDialog :show="newTicketOpen" :title="t('tickets.newTicket')" width="normal" :close-on-escape="!mutating" :show-close-button="!mutating" @close="newTicketOpen = false">
      <form id="new-ticket-form" class="flex flex-col gap-4" @submit.prevent="submitTicket">
        <p class="flex items-center gap-1.5 text-xs text-muted"><UIcon name="i-lucide-clock" class="size-3.5" />{{ t('tickets.dailyLimit') }}</p>
        <UFormField :label="t('tickets.subject')" required>
          <UInput id="ticket-subject" v-model="newTicket.subject" class="w-full" maxlength="160" required :placeholder="t('tickets.subjectPlaceholder')" :disabled="mutating" />
        </UFormField>
        <UFormField :label="t('tickets.category')">
          <Select id="ticket-category" v-model="newTicket.category" :options="newTicketCategoryOptions" :disabled="mutating" />
        </UFormField>
        <UFormField :label="t('tickets.contact')">
          <UInput id="ticket-contact" v-model="newTicket.contact" class="w-full" maxlength="200" :placeholder="t('tickets.contactPlaceholder')" :disabled="mutating" autocomplete="off" />
        </UFormField>
        <UFormField :label="t('tickets.content')" required :help="`${newTicket.content.length} / 10000`">
          <UTextarea id="ticket-content" v-model="newTicket.content" class="w-full" :rows="6" maxlength="10000" required :placeholder="t('tickets.contentPlaceholder')" :disabled="mutating" />
        </UFormField>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :disabled="mutating" @click="newTicketOpen = false">{{ t('tickets.cancel') }}</UButton>
          <UButton type="submit" form="new-ticket-form" icon="i-lucide-plus" :loading="mutating" :disabled="mutating || !newTicket.subject.trim() || !newTicket.content.trim() || stats.can_create === false">{{ t('tickets.create') }}</UButton>
        </div>
      </template>
    </BaseDialog>
    <BaseDialog :show="proactiveOpen" :title="t('tickets.proactiveTitle')" width="normal" :close-on-escape="!proactiveSending" :show-close-button="!proactiveSending" @close="closeProactive">
      <form id="proactive-ticket-form" class="flex flex-col gap-4" @submit.prevent="submitProactiveTicket">
        <p class="flex items-center gap-1.5 text-xs text-muted"><UIcon name="i-lucide-info" class="size-3.5" />{{ t('tickets.proactiveHint') }}</p>
        <UFormField :label="t('tickets.proactiveUser')">
          <UInput id="proactive-user" v-model="proactiveUserSearch" icon="i-lucide-search" class="w-full" maxlength="120" :placeholder="t('tickets.proactiveUserPlaceholder')" :disabled="proactiveSending" autocomplete="off" @update:model-value="onProactiveSearchInput" />
        </UFormField>
        <p v-if="proactiveSearching" class="px-1 text-sm text-muted">{{ t('tickets.proactiveSearching') }}</p>
        <div v-else-if="proactiveUsers.length" class="max-h-56 divide-y divide-default overflow-y-auto rounded-md ring ring-default">
          <UButton
            v-for="candidate in proactiveUsers"
            :key="candidate.id"
            color="neutral"
            variant="ghost"
            class="w-full justify-between rounded-none px-3 py-2 text-left"
            @click="pickProactiveUser(candidate)"
          >
            <span class="min-w-0"><strong class="block truncate text-highlighted">{{ candidate.username || '#' + candidate.id }}</strong><span class="block truncate text-xs text-muted">{{ candidate.email }}</span></span>
            <span class="shrink-0 text-xs text-dimmed">#{{ candidate.id }}</span>
          </UButton>
        </div>
        <p v-else-if="proactiveSearched && proactiveUserSearch.trim()" class="px-1 text-sm text-muted">{{ t('tickets.proactiveNoUsers') }}</p>
        <p v-if="proactiveUser" class="flex items-center gap-1.5 text-sm text-success"><UIcon name="i-lucide-circle-check" class="size-4" />{{ t('tickets.proactiveSelected', { user: proactiveUser.username || '#' + proactiveUser.id, email: proactiveUser.email }) }}</p>
        <UFormField :label="t('tickets.proactiveSubject')">
          <UInput id="proactive-subject" v-model="proactiveSubject" class="w-full" maxlength="160" :placeholder="t('tickets.proactiveSubjectPlaceholder')" :disabled="proactiveSending" />
        </UFormField>
        <UFormField :label="t('tickets.proactiveContent')" required :help="`${proactiveContent.length} / 10000`">
          <UTextarea id="proactive-content" v-model="proactiveContent" class="w-full" :rows="6" maxlength="10000" required :placeholder="t('tickets.proactiveContentPlaceholder')" :disabled="proactiveSending" />
        </UFormField>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :disabled="proactiveSending" @click="closeProactive">{{ t('tickets.cancel') }}</UButton>
          <UButton type="submit" form="proactive-ticket-form" icon="i-lucide-send" :loading="proactiveSending" :disabled="proactiveSending || !proactiveUser || !proactiveContent.trim()">{{ t('tickets.proactiveSend') }}</UButton>
        </div>
      </template>
    </BaseDialog>
    <ConfirmDialog :show="closeDialogOpen" :title="t('tickets.closeTitle')" :message="t('tickets.closeMessage')" :confirm-text="t('tickets.close')" @cancel="closeDialogOpen = false" @confirm="closeTicket" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TabsItem } from '@nuxt/ui'
import { useAppStore } from '@/stores/app'
import AppLayout from '@/components/layout/AppLayout.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import TicketStatusBadge from '@/components/tickets/TicketStatusBadge.vue'
import TicketTimeline from '@/components/tickets/TicketTimeline.vue'
import { useTicketWorkspace } from '@/composables/useTicketWorkspace'
import { newClientID, ticketsAPI, ticketCategories, type CreateTicket } from '@/api/tickets'
import { usersAPI } from '@/api/admin/users'
import type { AdminUser } from '@/types'
import { formatCurrency, formatTokensK } from '@/utils/format'
import { isComposerSendKey } from '@/utils/ticketComposer'

const props = defineProps<{ admin: boolean }>()
const { t, locale } = useI18n()
const app = useAppStore()
const { filters, items, pages, stats, detail, selectedID, loading, detailLoading, loadingOlder, refreshing, mutating,
  listError, detailError, syncFailed, revision, draft, refresh, older, markRead, send, update, create, resetFilters, select, back, changePage } = useTicketWorkspace(toRef(props, 'admin'))
const statusTabs = ['', 'active', 'closed'] as const
// Status tabs with their counts; 'all' stands in for the empty filter value.
const statusTabItems = computed<TabsItem[]>(() => statusTabs.map((status) => ({
  label: t(status === 'active' ? 'tickets.notClosed' : status ? `tickets.statuses.${status}` : 'tickets.all'),
  value: status || 'all',
  badge: { label: String(status === 'active' ? stats.value.total - stats.value.closed : status ? stats.value[status] : stats.value.total), color: 'neutral' as const, variant: 'subtle' as const }
})))
const statusTab = computed({
  get: () => filters.status || 'all',
  set: (value: string | number) => { filters.status = value === 'all' ? '' : value as typeof filters.status }
})
const hasFilters = computed(() => !!(filters.search || filters.status || filters.category))
const categoryOptions = computed(() => [
  { value: '', label: t('tickets.allCategories') },
  ...ticketCategories.map((category) => ({ value: category, label: t(`tickets.categories.${category}`) }))
])
const newTicketCategoryOptions = computed(() =>
  ticketCategories.map((category) => ({ value: category, label: t(`tickets.categories.${category}`) }))
)
const newTicketOpen = ref(false)
const ticketAPI = ticketsAPI(props.admin)
const proactiveOpen = ref(false)
const proactiveUserSearch = ref('')
const proactiveUsers = ref<AdminUser[]>([])
const proactiveSearched = ref(false)
const proactiveSearching = ref(false)
const proactiveUser = ref<AdminUser | null>(null)
const proactiveSubject = ref('')
const proactiveContent = ref('')
const proactiveSending = ref(false)
let proactiveSearchTimer: number | undefined
const closeDialogOpen = ref(false)
const replyInput = ref<{ textareaRef?: HTMLTextAreaElement | null } | null>(null)
const timeline = ref<InstanceType<typeof TicketTimeline> | null>(null)
const atBottom = ref(true)
const hasNewMessages = ref(false)
const newTicket = reactive<CreateTicket>({ subject: '', content: '', contact: '', category: 'api', priority: 'normal', client_id: newClientID() })
let lastTicketID = 0
let lastMessageID = 0
let createFingerprint = ''

const dateLocale = computed(() => locale.value === 'zh' ? 'zh-CN' : 'en-GB')
const formatDate = (value: string) => new Intl.DateTimeFormat(dateLocale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
const shortDate = (value: string) => new Intl.DateTimeFormat(dateLocale.value, { month: '2-digit', day: '2-digit' }).format(new Date(value))
const quotaTime = (value: string) => new Intl.DateTimeFormat(dateLocale.value, { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))

watch(revision, async () => {
  if (!detail.value) return
  const id = detail.value.ticket.id
  const messageID = detail.value.messages.at(-1)?.id || 0
  const initial = lastTicketID !== id
  const shouldScroll = initial || atBottom.value
  if (!initial && messageID > lastMessageID && !atBottom.value) hasNewMessages.value = true
  lastTicketID = id
  lastMessageID = messageID
  await nextTick()
  if (shouldScroll) { await timeline.value?.scrollToBottom(); void markRead() }
})
watch(selectedID, () => { closeDialogOpen.value = false; atBottom.value = true; hasNewMessages.value = false })

function onBottom(bottom: boolean) {
  atBottom.value = bottom
  if (bottom) { hasNewMessages.value = false; void markRead() }
}
async function jumpToLatest() { await timeline.value?.scrollToBottom(true); void markRead() }
async function loadOlder() {
  const id = selectedID.value
  const position = timeline.value?.capturePosition()
  await older()
  if (id === selectedID.value && position) await timeline.value?.restorePosition(position)
}
async function sendReply() {
  const id = selectedID.value
  if (await send()) {
    if (id === selectedID.value) { await timeline.value?.scrollToBottom(true); replyInput.value?.textareaRef?.focus() }
  }
}
function handleComposeKey(event: KeyboardEvent) {
  if (!isComposerSendKey(event)) return
  event.preventDefault()
  if (mutating.value || !draft.value.trim()) return
  void sendReply()
}
function openProactive() {
  proactiveOpen.value = true
  proactiveUsers.value = []
  proactiveSearched.value = false
}
function closeProactive() {
  if (!proactiveSending.value) proactiveOpen.value = false
}
function onProactiveSearchInput() {
  proactiveUser.value = null
  window.clearTimeout(proactiveSearchTimer)
  proactiveSearchTimer = window.setTimeout(() => void searchProactiveUsers(), 300)
}
async function searchProactiveUsers() {
  const query = proactiveUserSearch.value.trim()
  proactiveSearched.value = false
  if (!query) {
    proactiveUsers.value = []
    return
  }
  proactiveSearching.value = true
  try {
    const result = await usersAPI.list(1, 10, { search: query })
    proactiveUsers.value = result.items || []
    proactiveSearched.value = true
  } catch {
    proactiveUsers.value = []
  } finally {
    proactiveSearching.value = false
  }
}
function pickProactiveUser(user: AdminUser) {
  proactiveUser.value = user
  proactiveUserSearch.value = `${user.username || '#' + user.id}${user.email ? ' · ' + user.email : ''}`
  proactiveUsers.value = []
  proactiveSearched.value = false
}
async function submitProactiveTicket() {
  if (!proactiveUser.value) {
    app.showError(t('tickets.proactiveSelectUser'))
    return
  }
  if (!proactiveContent.value.trim()) {
    app.showError(t('tickets.proactiveContentRequired'))
    return
  }
  proactiveSending.value = true
  try {
    const created = await ticketAPI.proactiveCreate({
      user_id: proactiveUser.value.id,
      subject: proactiveSubject.value.trim(),
      content: proactiveContent.value.trim()
    })
    proactiveOpen.value = false
    proactiveUser.value = null
    proactiveUserSearch.value = ''
    proactiveUsers.value = []
    proactiveSubject.value = ''
    proactiveContent.value = ''
    await refresh(true)
    if (created?.ticket?.id) select(created.ticket.id)
    app.showSuccess(t('tickets.proactiveCreated'))
  } catch {
    app.showError(t('tickets.failed'))
  } finally {
    proactiveSending.value = false
  }
}
async function closeTicket() {
  closeDialogOpen.value = false
  await update({ status: 'closed' })
}
async function submitTicket() {
  if (!newTicket.subject.trim() || !newTicket.content.trim()) { app.showError(t('tickets.required')); return }
  const fingerprint = JSON.stringify([newTicket.subject, newTicket.content, newTicket.contact, newTicket.category, newTicket.priority])
  if (fingerprint !== createFingerprint) { newTicket.client_id = newClientID(); createFingerprint = fingerprint }
  if (await create({ ...newTicket, subject: newTicket.subject.trim(), content: newTicket.content.trim(), contact: newTicket.contact.trim() })) {
    newTicketOpen.value = false
    Object.assign(newTicket, { subject: '', content: '', contact: '', category: 'api', priority: 'normal', client_id: newClientID() })
    createFingerprint = ''
  }
}
</script>

<style>
/* Tickets: the list + detail panel fills the console panel body (it scrolls, not the window). */
.console-frame .signal-main:has(> .tickets-page-root) { display: flex; flex: 1 1 auto; min-height: 0; flex-direction: column; }
.console-frame .signal-main > .tickets-page-root { flex: 1 1 auto; min-height: 0; }
.console-frame .signal-main > .tickets-page-root .tickets-workspace { flex: 1 1 auto; height: auto; }
</style>
