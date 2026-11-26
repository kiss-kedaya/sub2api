<template>
  <AppLayout>
    <section class="tickets-page" :class="{ 'tickets-detail-open': selectedID }">
      <header class="tickets-heading">
        <div class="tickets-heading-title">
          <span class="tickets-heading-icon"><Icon :name="admin ? 'inbox' : 'chat'" size="lg" /></span>
          <div><h1>{{ t(admin ? 'tickets.adminTitle' : 'tickets.title') }}</h1><p>{{ t('tickets.count', { count: stats.total }) }}<span v-if="stats.unread" class="tickets-unread-summary">{{ t('tickets.unread') }} {{ stats.unread }}</span></p></div>
        </div>
        <div class="tickets-heading-actions">
          <span v-if="syncFailed" class="tickets-sync tickets-sync-error" role="status"><span></span>{{ t('tickets.syncFailed') }}</span>
          <button class="ticket-icon-button" type="button" :disabled="refreshing" :title="t('tickets.refresh')" :aria-label="t('tickets.refresh')" @click="refresh(true)"><Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" /></button>
          <button v-if="!admin" class="ticket-button ticket-button-primary" type="button" :disabled="stats.can_create === false || loading" @click="newTicketOpen = true"><Icon name="plus" size="sm" />{{ t('tickets.newTicket') }}</button>
          <button v-if="admin" class="ticket-button ticket-button-primary" type="button" @click="openProactive"><Icon name="plus" size="sm" />{{ t('tickets.proactiveNew') }}</button>
        </div>
      </header>

      <div v-if="!admin" class="tickets-quota" :class="{ 'tickets-quota-used': stats.can_create === false }">
        <Icon :name="stats.can_create === false ? 'clock' : 'infoCircle'" size="sm" />
        <span>{{ t(stats.can_create === false ? 'tickets.dailyUsed' : 'tickets.dailyLimit') }}</span>
        <time v-if="stats.can_create === false && stats.next_create_at">{{ t('tickets.nextCreate', { time: quotaTime(stats.next_create_at) }) }}</time>
      </div>

      <nav class="tickets-status-tabs" :aria-label="t('tickets.status')">
        <button v-for="status in statusTabs" :key="status || 'all'" type="button" :aria-pressed="filters.status === status" :class="{ active: filters.status === status }" @click="filters.status = status">
          {{ t(statusTabLabel(status)) }}<span :class="{ 'tab-badge-unread': status === 'unread' && stats.unread > 0 }">{{ statusTabCount(status) }}</span>
        </button>
      </nav>

      <div class="tickets-workspace">
        <aside class="tickets-inbox" :aria-label="t(admin ? 'tickets.adminTitle' : 'tickets.title')">
          <div class="tickets-list-tools">
            <label class="tickets-search"><Icon name="search" size="sm" /><input v-model="filters.search" type="search" maxlength="160" :aria-label="t('tickets.search')" :placeholder="t(admin ? 'tickets.adminSearch' : 'tickets.search')" /></label>
            <div class="tickets-list-filters">
              <Select v-model="filters.category" :options="categoryOptions" :aria-label="t('tickets.category')" size="sm" class="min-w-26" />
            </div>
          </div>

          <div class="tickets-list" :aria-busy="loading">
            <div v-if="loading && !items.length" class="tickets-list-skeleton" role="status" :aria-label="t('tickets.loading')"><div v-for="i in 4" :key="i"><span></span><span></span><span></span></div></div>
            <div v-else-if="listError" class="tickets-empty" role="alert"><Icon name="exclamationCircle" size="lg" /><p>{{ listError }}</p><button class="ticket-text-button" @click="refresh(true)">{{ t('tickets.retry') }}</button></div>
            <div v-else-if="!items.length" class="tickets-empty"><Icon name="inbox" size="xl" /><p>{{ t(emptyMessageKey) }}</p><button v-if="hasFilters" class="ticket-text-button" @click="resetFilters">{{ t('tickets.resetFilters') }}</button><button v-else-if="!admin && stats.can_create !== false" class="ticket-text-button" @click="newTicketOpen = true">{{ t('tickets.newTicket') }}</button></div>
            <template v-else>
              <button v-for="item in items" :key="item.id" type="button" class="ticket-list-item" :class="{ selected: selectedID === item.id, unread: item.unread_count > 0 }" :aria-current="selectedID === item.id ? 'true' : undefined" @click="select(item.id)">
                <span class="ticket-item-top"><span class="ticket-number">#{{ item.id }}</span><time :datetime="item.last_message_at" :title="formatDate(item.last_message_at)">{{ shortDate(item.last_message_at) }}</time></span>
                <span class="ticket-item-subject"><strong>{{ item.subject }}</strong><span v-if="item.unread_count" class="ticket-unread" :aria-label="t('tickets.unread')">{{ item.unread_count > 99 ? '99+' : item.unread_count }}</span></span>
                <span class="ticket-item-preview">{{ item.last_message_preview }}</span>
                <span class="ticket-item-bottom"><TicketStatusBadge :status="item.status" /><span class="ticket-item-category">{{ admin ? item.user_name || item.user_email : t(`tickets.categories.${item.category}`) }}</span></span>
              </button>
            </template>
          </div>

          <footer class="tickets-pagination">
            <span>{{ t('tickets.page', { page: filters.page, pages }) }}</span>
            <div><button type="button" class="ticket-icon-button" :disabled="filters.page <= 1 || loading" :title="t('tickets.previous')" :aria-label="t('tickets.previous')" @click="changePage(filters.page - 1)"><Icon name="chevronLeft" size="sm" /></button><button type="button" class="ticket-icon-button" :disabled="filters.page >= pages || loading" :title="t('tickets.next')" :aria-label="t('tickets.next')" @click="changePage(filters.page + 1)"><Icon name="chevronRight" size="sm" /></button></div>
          </footer>
        </aside>

        <section class="tickets-detail" :aria-label="t('tickets.selectDetail')" :aria-busy="detailLoading">
          <div v-if="!selectedID" class="tickets-welcome"><Icon name="chat" size="xl" /><h2>{{ t('tickets.selectTicket') }}</h2></div>
          <div v-else-if="detailLoading" class="tickets-empty" role="status"><Icon name="refresh" size="lg" class="animate-spin" /><p>{{ t('tickets.loading') }}</p></div>
          <div v-else-if="!detail" class="tickets-empty" role="alert"><Icon name="exclamationCircle" size="lg" /><p>{{ detailError || t('tickets.loadFailed') }}</p><button class="ticket-text-button" @click="refresh(true)">{{ t('tickets.retry') }}</button><button class="ticket-text-button" @click="back">{{ t('tickets.back') }}</button></div>
          <template v-else>
            <header class="ticket-detail-header">
              <div class="ticket-detail-title-row">
                <button type="button" class="ticket-icon-button tickets-mobile-back" :title="t('tickets.back')" :aria-label="t('tickets.back')" @click="back"><Icon name="arrowLeft" size="sm" /></button>
                <div class="ticket-detail-title"><div><span class="ticket-number">#{{ detail.ticket.id }}</span><TicketStatusBadge :status="detail.ticket.status" /></div><h2>{{ detail.ticket.subject }}</h2></div>
                <button v-if="detail.ticket.status !== 'closed'" type="button" class="ticket-icon-button" :disabled="mutating" :title="t('tickets.close')" :aria-label="t('tickets.close')" @click="closeDialogOpen = true"><Icon name="xCircle" size="md" /></button>
              </div>
              <div class="ticket-detail-meta"><span>{{ t(`tickets.categories.${detail.ticket.category}`) }}</span><time :datetime="detail.ticket.created_at">{{ formatDate(detail.ticket.created_at) }}</time></div>
              <template v-if="admin && detail.requester">
              <dl class="ticket-requester-stats">
                <div><dt>{{ t('tickets.todayTokens') }}</dt><dd :title="detail.requester.usage_stats_available ? undefined : t('tickets.statsUnavailable')">{{ detail.requester.usage_stats_available ? formatTokensK(detail.requester.today_tokens ?? 0) : '—' }}</dd></div>
                <div><dt>{{ t('tickets.todayCost') }}</dt><dd class="ticket-user-balance" :title="detail.requester.usage_stats_available ? undefined : t('tickets.statsUnavailable')">{{ detail.requester.usage_stats_available ? formatCurrency(detail.requester.today_cost ?? 0) : '—' }}</dd></div>
                <div><dt :title="t('tickets.rechargeStatsHint')">{{ t('tickets.recharged14d') }}</dt><dd class="ticket-user-balance" :title="detail.requester.recharge_stats_available ? undefined : t('tickets.statsUnavailable')">{{ detail.requester.recharge_stats_available ? formatCurrency(detail.requester.recharged_14d ?? 0) : '—' }}</dd></div>
              </dl>
              <dl class="ticket-requester-info">
                <div><dt>{{ t('tickets.requester') }}</dt><dd>{{ detail.requester.username || detail.ticket.user_name || '#' + detail.requester.id }}<span class="ticket-requester-id">#{{ detail.requester.id }}</span></dd></div>
                <div><dt>{{ t('tickets.balance') }}</dt><dd class="ticket-user-balance">{{ formatCurrency(detail.requester.balance) }}</dd></div>
                <div><dt>{{ t('tickets.contact') }}</dt><dd>{{ detail.ticket.contact || t('tickets.noContact') }}</dd></div>
                <div><dt>{{ t('tickets.accountStatus') }}</dt><dd>{{ detail.requester.status === 'active' ? t('tickets.active') : detail.requester.status === 'disabled' ? t('tickets.disabled') : detail.requester.status }}</dd></div>
                <div class="ticket-requester-email"><dt>{{ t('common.email') }}</dt><dd>{{ detail.requester.email }}</dd></div>
                <div class="ticket-requester-registered"><dt>{{ t('tickets.registered') }}</dt><dd>{{ shortDate(detail.requester.created_at) }}</dd></div>
              </dl>
              </template>
              <div v-else-if="!admin && detail.ticket.contact" class="ticket-user-contact"><Icon name="userCircle" size="sm" /><span>{{ t('tickets.contact') }}: {{ detail.ticket.contact }}</span></div>
            </header>

            <div v-if="detailError" class="ticket-inline-error" role="alert">{{ detailError }}<button class="ticket-text-button" @click="refresh(true)">{{ t('tickets.retry') }}</button></div>
            <TicketTimeline ref="timeline" :messages="detail.messages" :admin="admin" :has-more="detail.has_more" :loading-older="loadingOlder" @older="loadOlder" @bottom="onBottom" />
            <div v-if="!atBottom && hasNewMessages" class="ticket-new-message-bar"><button type="button" class="ticket-button" @click="jumpToLatest"><Icon name="arrowDown" size="sm" />{{ t('tickets.newMessages') }}</button></div>

            <footer v-if="detail.ticket.status === 'closed'" class="ticket-closed-bar"><span><Icon name="lock" size="sm" />{{ t('tickets.closed') }}</span><button class="ticket-button" :disabled="mutating" @click="update({ status: 'open' })"><Icon name="refresh" size="sm" />{{ t('tickets.reopen') }}</button></footer>
            <form
              v-else
              class="ticket-composer"
              :class="{ 'drag-over': composerDragging }"
              @submit.prevent="sendReply"
              @keydown="handleComposeKey"
              @dragenter="onComposerDragEnter"
              @dragover="onComposerDragOver"
              @dragleave="onComposerDragLeave"
              @drop="onComposerDrop"
            >
              <div v-if="composerDragging" class="ticket-composer-drop-hint">{{ t('tickets.imageDropHint') }}</div>
              <TicketImagePicker
                ref="replyImagePicker"
                :items="draftAttachments"
                :disabled="mutating"
                :show-trigger="false"
                @select="addDraftImages"
                @remove="removeDraftImage"
              />
              <label class="sr-only" for="ticket-reply">{{ t('tickets.reply') }}</label>
              <textarea id="ticket-reply" ref="replyInput" v-model="draft" maxlength="10000" :disabled="mutating" :placeholder="t('tickets.replyPlaceholder')" rows="3" @paste="onReplyPaste"></textarea>
              <div class="ticket-composer-actions">
                <div class="ticket-composer-tools">
                  <button
                    type="button"
                    class="ticket-icon-button ticket-attach-button"
                    :disabled="mutating || draftAttachments.length >= TICKET_IMAGE_MAX_COUNT"
                    :title="t('tickets.imageHint')"
                    :aria-label="t('tickets.addImage')"
                    @click="replyImagePicker?.openPicker()"
                  >
                    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909M3.75 19.5h16.5A2.25 2.25 0 0022.5 17.25V6.75A2.25 2.25 0 0020.25 4.5H3.75A2.25 2.25 0 001.5 6.75v10.5A2.25 2.25 0 003.75 19.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
                    </svg>
                  </button>
                  <span class="ticket-character-count" aria-live="off">{{ draft.length ? draft.length + ' / 10000' : '' }}</span>
                </div>
                <button type="submit" class="ticket-button ticket-button-primary" :disabled="!canSend"><Icon :name="mutating || uploadingImages ? 'refresh' : 'arrowUp'" size="sm" :class="{ 'animate-spin': mutating || uploadingImages }" />{{ t('tickets.send') }}</button>
              </div>
            </form>
          </template>
        </section>
      </div>
    </section>

    <BaseDialog :show="newTicketOpen" :title="t('tickets.newTicket')" width="normal" :close-on-escape="!mutating" :show-close-button="!mutating" @close="closeNewTicket">
      <form
        id="new-ticket-form"
        class="ticket-create-form"
        :class="{ 'drag-over': newTicketDragging }"
        @submit.prevent="submitTicket"
        @dragenter="onNewTicketDragEnter"
        @dragover="onNewTicketDragOver"
        @dragleave="onNewTicketDragLeave"
        @drop="onNewTicketDrop"
      >
        <div v-if="newTicketDragging" class="ticket-composer-drop-hint">{{ t('tickets.imageDropHint') }}</div>
        <div class="ticket-create-note"><Icon name="clock" size="sm" />{{ t('tickets.dailyLimit') }}</div>
        <label for="ticket-subject">{{ t('tickets.subject') }}<input id="ticket-subject" v-model="newTicket.subject" class="input" maxlength="160" required :placeholder="t('tickets.subjectPlaceholder')" :disabled="mutating" /></label>
        <label for="ticket-category">{{ t('tickets.category') }}<Select id="ticket-category" v-model="newTicket.category" :options="newTicketCategoryOptions" :disabled="mutating" /></label>
        <label for="ticket-contact">{{ t('tickets.contact') }}<input id="ticket-contact" v-model="newTicket.contact" class="input" maxlength="200" :placeholder="t('tickets.contactPlaceholder')" :disabled="mutating" autocomplete="off" /></label>
        <label for="ticket-content">{{ t('tickets.content') }}<textarea id="ticket-content" v-model="newTicket.content" class="input" rows="6" maxlength="10000" :required="newTicketImages.length === 0" :placeholder="t('tickets.contentPlaceholder')" :disabled="mutating" @paste="onNewTicketPaste"></textarea></label>
        <div class="ticket-modal-attachments">
          <span class="ticket-modal-attachments-label">{{ t('tickets.imagesLabel', { max: TICKET_IMAGE_MAX_COUNT }) }}</span>
          <TicketImagePicker :items="newTicketImages" :disabled="mutating" @select="files => uploadModalImages(newTicketImages, files)" @remove="id => removeModalImage(newTicketImages, id)" />
        </div>
        <span class="ticket-character-count">{{ newTicket.content.length }} / 10000</span>
      </form>
      <template #footer><button class="btn btn-secondary" type="button" :disabled="mutating" @click="closeNewTicket">{{ t('tickets.cancel') }}</button><button class="btn btn-primary" type="submit" form="new-ticket-form" :disabled="mutating || !canSubmitNewTicket || stats.can_create === false"><Icon :name="mutating ? 'refresh' : 'plus'" size="sm" :class="{ 'animate-spin': mutating }" />{{ t('tickets.create') }}</button></template>
    </BaseDialog>
    <BaseDialog :show="proactiveOpen" :title="t('tickets.proactiveTitle')" width="normal" :close-on-escape="!proactiveSending" :show-close-button="!proactiveSending" @close="closeProactive">
      <form
        id="proactive-ticket-form"
        class="ticket-create-form"
        :class="{ 'drag-over': proactiveDragging }"
        @submit.prevent="submitProactiveTicket"
        @dragenter="onProactiveDragEnter"
        @dragover="onProactiveDragOver"
        @dragleave="onProactiveDragLeave"
        @drop="onProactiveDrop"
      >
        <div v-if="proactiveDragging" class="ticket-composer-drop-hint">{{ t('tickets.imageDropHint') }}</div>
        <div class="ticket-create-note"><Icon name="infoCircle" size="sm" />{{ t('tickets.proactiveHint') }}</div>
        <label for="proactive-user">{{ t('tickets.proactiveUser') }}<input id="proactive-user" v-model="proactiveUserSearch" class="input" maxlength="120" :placeholder="t('tickets.proactiveUserPlaceholder')" :disabled="proactiveSending" autocomplete="off" @input="onProactiveSearchInput" /></label>
        <div v-if="proactiveSearching" class="px-3 py-2 text-sm text-gray-500">{{ t('tickets.proactiveSearching') }}</div>
        <div v-else-if="proactiveUsers.length" class="max-h-56 overflow-y-auto rounded-lg border border-gray-200 bg-white shadow-xs dark:border-gray-700 dark:bg-gray-900">
          <button v-for="candidate in proactiveUsers" :key="candidate.id" type="button" class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-gray-50 dark:hover:bg-gray-800" @click="pickProactiveUser(candidate)">
            <span class="min-w-0"><strong class="block truncate">{{ candidate.username || '#' + candidate.id }}</strong><span class="block truncate text-xs text-gray-500">{{ candidate.email }}</span></span>
            <span class="shrink-0 text-xs text-gray-400">#{{ candidate.id }}</span>
          </button>
        </div>
        <p v-else-if="proactiveSearched && proactiveUserSearch.trim()" class="px-3 text-sm text-gray-500">{{ t('tickets.proactiveNoUsers') }}</p>
        <p v-if="proactiveUser" class="text-sm text-green-600 dark:text-green-400">{{ t('tickets.proactiveSelected', { user: proactiveUser.username || '#' + proactiveUser.id, email: proactiveUser.email }) }}</p>
        <label for="proactive-subject">{{ t('tickets.proactiveSubject') }}<input id="proactive-subject" v-model="proactiveSubject" class="input" maxlength="160" :placeholder="t('tickets.proactiveSubjectPlaceholder')" :disabled="proactiveSending" /></label>
        <label for="proactive-content">{{ t('tickets.proactiveContent') }}<textarea id="proactive-content" v-model="proactiveContent" class="input" rows="6" maxlength="10000" :required="proactiveImages.length === 0" :placeholder="t('tickets.proactiveContentPlaceholder')" :disabled="proactiveSending" @paste="onProactivePaste"></textarea></label>
        <div class="ticket-modal-attachments">
          <span class="ticket-modal-attachments-label">{{ t('tickets.imagesLabel', { max: TICKET_IMAGE_MAX_COUNT }) }}</span>
          <TicketImagePicker :items="proactiveImages" :disabled="proactiveSending" @select="files => uploadModalImages(proactiveImages, files)" @remove="id => removeModalImage(proactiveImages, id)" />
        </div>
        <span class="ticket-character-count">{{ proactiveContent.length }} / 10000</span>
      </form>
      <template #footer><button class="btn btn-secondary" type="button" :disabled="proactiveSending" @click="closeProactive">{{ t('tickets.cancel') }}</button><button class="btn btn-primary" type="submit" form="proactive-ticket-form" :disabled="proactiveSending || !canSubmitProactive"><Icon :name="proactiveSending ? 'refresh' : 'arrowUp'" size="sm" :class="{ 'animate-spin': proactiveSending }" />{{ t('tickets.proactiveSend') }}</button></template>
    </BaseDialog>
    <ConfirmDialog :show="closeDialogOpen" :title="t('tickets.closeTitle')" :message="t('tickets.closeMessage')" :confirm-text="t('tickets.close')" @cancel="closeDialogOpen = false" @confirm="closeTicket" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import TicketImagePicker from '@/components/tickets/TicketImagePicker.vue'
import TicketStatusBadge from '@/components/tickets/TicketStatusBadge.vue'
import TicketTimeline from '@/components/tickets/TicketTimeline.vue'
import {
  TICKET_IMAGE_MAX_COUNT,
  clearPendingImages,
  createObjectURLSafe,
  extractClipboardImages,
  hasDraggedFiles,
  hasErroredImages,
  hasUploadingImages,
  isTicketAbortError,
  readyAttachmentIDs,
  revokeObjectURLSafe,
  validateTicketImageFile,
  type PendingTicketImage
} from '@/components/tickets/ticketImages'
import { useTicketWorkspace } from '@/composables/useTicketWorkspace'
import { newClientID, ticketsAPI, ticketCategories, type CreateTicket } from '@/api/tickets'
import { usersAPI } from '@/api/admin/users'
import type { AdminUser } from '@/types'
import { formatCurrency, formatTokensK } from '@/utils/format'
import { isComposerSendKey } from '@/utils/ticketComposer'
import '@/styles/tickets.css'

const props = defineProps<{ admin: boolean }>()
const { t, locale } = useI18n()
const app = useAppStore()
const { filters, items, pages, stats, detail, selectedID, loading, detailLoading, loadingOlder, refreshing, mutating,
  listError, detailError, syncFailed, revision, draft, draftAttachments, uploadingImages, canSend,
  refresh, older, markRead, addDraftImages, removeDraftImage, send, update, create, resetFilters, select, back, changePage } = useTicketWorkspace(toRef(props, 'admin'))
const statusTabs = ['', 'unread', 'active', 'closed'] as const
const hasFilters = computed(() => !!(filters.search || filters.status || filters.category))
const emptyMessageKey = computed(() => {
  if (filters.status === 'unread' && !filters.search && !filters.category) return 'tickets.emptyUnread'
  return hasFilters.value ? 'tickets.emptyFiltered' : 'tickets.empty'
})
function statusTabLabel(status: typeof statusTabs[number]) {
  if (status === 'unread') return 'tickets.unreadTab'
  if (status === 'active') return 'tickets.notClosed'
  return status ? `tickets.statuses.${status}` : 'tickets.all'
}
function statusTabCount(status: typeof statusTabs[number]) {
  if (status === 'active') return Math.max(0, stats.value.total - stats.value.closed)
  if (status === 'unread') return stats.value.unread
  return status ? stats.value[status] : stats.value.total
}
const categoryOptions = computed(() => [
  { value: '', label: t('tickets.allCategories') },
  ...ticketCategories.map((category) => ({ value: category, label: t(`tickets.categories.${category}`) }))
])
const newTicketCategoryOptions = computed(() =>
  ticketCategories.map((category) => ({ value: category, label: t(`tickets.categories.${category}`) }))
)
const newTicketOpen = ref(false)
const ticketAPI = computed(() => ticketsAPI(props.admin))
const proactiveOpen = ref(false)
const proactiveUserSearch = ref('')
const proactiveUsers = ref<AdminUser[]>([])
const proactiveSearched = ref(false)
const proactiveSearching = ref(false)
const proactiveUser = ref<AdminUser | null>(null)
const proactiveSubject = ref('')
const proactiveContent = ref('')
const proactiveSending = ref(false)
const proactiveImages = reactive<PendingTicketImage[]>([])
let proactiveSearchTimer: number | undefined
const closeDialogOpen = ref(false)
const replyInput = ref<HTMLTextAreaElement | null>(null)
const replyImagePicker = ref<InstanceType<typeof TicketImagePicker> | null>(null)
const composerDragging = ref(false)
let composerDragDepth = 0
const newTicketDragging = ref(false)
let newTicketDragDepth = 0
const proactiveDragging = ref(false)
let proactiveDragDepth = 0
const modalUploadControllers = new Map<string, AbortController>()
let mounted = true
const timeline = ref<InstanceType<typeof TicketTimeline> | null>(null)
const atBottom = ref(true)
const hasNewMessages = ref(false)
const newTicket = reactive<CreateTicket>({ subject: '', content: '', contact: '', category: 'api', priority: 'normal', client_id: newClientID() })
const newTicketImages = reactive<PendingTicketImage[]>([])
let lastTicketID = 0
let lastMessageID = 0
let createFingerprint = ''

const canSubmitNewTicket = computed(() =>
  !hasUploadingImages(newTicketImages) &&
  !hasErroredImages(newTicketImages) &&
  Boolean(newTicket.subject.trim()) &&
  (Boolean(newTicket.content.trim()) || readyAttachmentIDs(newTicketImages).length > 0)
)
const canSubmitProactive = computed(() =>
  !hasUploadingImages(proactiveImages) &&
  !hasErroredImages(proactiveImages) &&
  Boolean(proactiveUser.value) &&
  (Boolean(proactiveContent.value.trim()) || readyAttachmentIDs(proactiveImages).length > 0)
)

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
watch(selectedID, () => { closeDialogOpen.value = false; atBottom.value = true; hasNewMessages.value = false; composerDragging.value = false; composerDragDepth = 0 })

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
    if (id === selectedID.value) { await timeline.value?.scrollToBottom(true); replyInput.value?.focus() }
  }
}
function handleComposeKey(event: KeyboardEvent) {
  if (!isComposerSendKey(event)) return
  event.preventDefault()
  if (!canSend.value) return
  void sendReply()
}
function onReplyPaste(event: ClipboardEvent) {
  if (mutating.value) return
  const images = extractClipboardImages(event)
  if (!images.length) return
  event.preventDefault()
  void addDraftImages(images)
}
function onComposerDragEnter(event: DragEvent) {
  if (mutating.value || !hasDraggedFiles(event)) return
  event.preventDefault()
  composerDragDepth += 1
  composerDragging.value = true
}
function onComposerDragOver(event: DragEvent) {
  if (mutating.value || !hasDraggedFiles(event)) return
  event.preventDefault()
}
function onComposerDragLeave(event: DragEvent) {
  if (!hasDraggedFiles(event)) return
  composerDragDepth = Math.max(0, composerDragDepth - 1)
  if (composerDragDepth === 0) composerDragging.value = false
}
function onComposerDrop(event: DragEvent) {
  if (!hasDraggedFiles(event)) return
  event.preventDefault()
  composerDragDepth = 0
  composerDragging.value = false
  if (mutating.value) return
  const files = event.dataTransfer?.files ? Array.from(event.dataTransfer.files) : []
  if (files.length) void addDraftImages(files)
}
function closeNewTicket() {
  if (mutating.value) return
  newTicketOpen.value = false
  newTicketDragging.value = false
  newTicketDragDepth = 0
}
function onNewTicketDragEnter(event: DragEvent) {
  if (mutating.value || !hasDraggedFiles(event)) return
  event.preventDefault()
  newTicketDragDepth += 1
  newTicketDragging.value = true
}
function onNewTicketDragOver(event: DragEvent) {
  if (mutating.value || !hasDraggedFiles(event)) return
  event.preventDefault()
}
function onNewTicketDragLeave(event: DragEvent) {
  if (!hasDraggedFiles(event)) return
  newTicketDragDepth = Math.max(0, newTicketDragDepth - 1)
  if (newTicketDragDepth === 0) newTicketDragging.value = false
}
function onNewTicketDrop(event: DragEvent) {
  if (!hasDraggedFiles(event)) return
  event.preventDefault()
  newTicketDragDepth = 0
  newTicketDragging.value = false
  if (mutating.value) return
  const files = event.dataTransfer?.files ? Array.from(event.dataTransfer.files) : []
  if (files.length) void uploadModalImages(newTicketImages, files)
}
function onProactiveDragEnter(event: DragEvent) {
  if (proactiveSending.value || !hasDraggedFiles(event)) return
  event.preventDefault()
  proactiveDragDepth += 1
  proactiveDragging.value = true
}
function onProactiveDragOver(event: DragEvent) {
  if (proactiveSending.value || !hasDraggedFiles(event)) return
  event.preventDefault()
}
function onProactiveDragLeave(event: DragEvent) {
  if (!hasDraggedFiles(event)) return
  proactiveDragDepth = Math.max(0, proactiveDragDepth - 1)
  if (proactiveDragDepth === 0) proactiveDragging.value = false
}
function onProactiveDrop(event: DragEvent) {
  if (!hasDraggedFiles(event)) return
  event.preventDefault()
  proactiveDragDepth = 0
  proactiveDragging.value = false
  if (proactiveSending.value) return
  const files = event.dataTransfer?.files ? Array.from(event.dataTransfer.files) : []
  if (files.length) void uploadModalImages(proactiveImages, files)
}
async function uploadModalImages(target: PendingTicketImage[], files: File[]) {
  const currentAPI = ticketAPI.value
  const remaining = TICKET_IMAGE_MAX_COUNT - target.length
  if (remaining <= 0) {
    app.showError(t('tickets.imageMaxCount', { max: TICKET_IMAGE_MAX_COUNT }))
    return
  }
  const validFiles: File[] = []
  for (const file of files) {
    const invalid = validateTicketImageFile(file)
    if (invalid === 'type') {
      app.showError(t('tickets.imageUnsupportedType'))
      continue
    }
    if (invalid === 'size') {
      app.showError(t('tickets.imageTooLarge'))
      continue
    }
    validFiles.push(file)
  }
  if (validFiles.length === 0) return
  if (validFiles.length > remaining) {
    app.showError(t('tickets.imageMaxCount', { max: TICKET_IMAGE_MAX_COUNT }))
  }
  const candidates = validFiles.slice(0, remaining)
  await Promise.all(candidates.map(async (file) => {
    const localID = newClientID()
    const controller = new AbortController()
    modalUploadControllers.set(localID, controller)
    const pending: PendingTicketImage = reactive({
      localID,
      fileName: file.name || 'image.png',
      previewURL: createObjectURLSafe(file),
      status: 'uploading'
    })
    target.push(pending)
    try {
      const uploaded = await currentAPI.uploadAttachment(file, controller.signal)
      if (!mounted || controller.signal.aborted || !target.includes(pending)) {
        if (uploaded?.id) void currentAPI.deletePendingAttachment?.(uploaded.id)?.catch(() => {})
        return
      }
      pending.attachment = uploaded
      pending.status = 'ready'
    } catch (error) {
      if (!mounted || controller.signal.aborted || !target.includes(pending) || isTicketAbortError(error)) return
      pending.status = 'error'
      pending.error = error && typeof error === 'object' && 'message' in error && typeof error.message === 'string'
        ? error.message
        : t('tickets.imageUploadFailed')
      app.showError(pending.error)
    } finally {
      if (modalUploadControllers.get(localID) === controller) modalUploadControllers.delete(localID)
    }
  }))
}
function removeModalImage(target: PendingTicketImage[], localID: string) {
  modalUploadControllers.get(localID)?.abort()
  modalUploadControllers.delete(localID)
  const index = target.findIndex((item) => item.localID === localID)
  if (index === -1) return
  const [removed] = target.splice(index, 1)
  revokeObjectURLSafe(removed.previewURL)
  if (removed.status === 'ready' && removed.attachment?.id) {
    void ticketAPI.value.deletePendingAttachment?.(removed.attachment.id)?.catch(() => {})
  }
}
function onNewTicketPaste(event: ClipboardEvent) {
  if (mutating.value) return
  const images = extractClipboardImages(event)
  if (!images.length) return
  event.preventDefault()
  void uploadModalImages(newTicketImages, images)
}
function onProactivePaste(event: ClipboardEvent) {
  if (proactiveSending.value) return
  const images = extractClipboardImages(event)
  if (!images.length) return
  event.preventDefault()
  void uploadModalImages(proactiveImages, images)
}
function openProactive() {
  proactiveOpen.value = true
  proactiveUsers.value = []
  proactiveSearched.value = false
}
function closeProactive() {
  if (!proactiveSending.value) {
    proactiveOpen.value = false
    proactiveDragging.value = false
    proactiveDragDepth = 0
  }
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
  if (hasUploadingImages(proactiveImages)) {
    app.showError(t('tickets.imageUploadingWait'))
    return
  }
  if (hasErroredImages(proactiveImages)) {
    app.showError(t('tickets.imageRemoveFailedFirst'))
    return
  }
  const attachmentIDs = readyAttachmentIDs(proactiveImages)
  if (!proactiveContent.value.trim() && attachmentIDs.length === 0) {
    app.showError(t('tickets.proactiveContentRequired'))
    return
  }
  proactiveSending.value = true
  try {
    const created = await ticketAPI.value.proactiveCreate({
      user_id: proactiveUser.value.id,
      subject: proactiveSubject.value.trim(),
      content: proactiveContent.value.trim(),
      ...(attachmentIDs.length ? { attachment_ids: attachmentIDs } : {})
    })
    proactiveOpen.value = false
    proactiveUser.value = null
    proactiveUserSearch.value = ''
    proactiveUsers.value = []
    proactiveSubject.value = ''
    proactiveContent.value = ''
    clearPendingImages(proactiveImages)
    await refresh(true)
    if (created?.ticket?.id) select(created.ticket.id)
    app.showSuccess(t('tickets.proactiveCreated'))
  } catch (error) {
    const message = error && typeof error === 'object' && 'message' in error && typeof error.message === 'string'
      ? error.message
      : t('tickets.failed')
    app.showError(message)
  } finally {
    proactiveSending.value = false
  }
}
async function closeTicket() {
  closeDialogOpen.value = false
  await update({ status: 'closed' })
}
async function submitTicket() {
  if (hasUploadingImages(newTicketImages)) {
    app.showError(t('tickets.imageUploadingWait'))
    return
  }
  if (hasErroredImages(newTicketImages)) {
    app.showError(t('tickets.imageRemoveFailedFirst'))
    return
  }
  const attachmentIDs = readyAttachmentIDs(newTicketImages)
  if (!newTicket.subject.trim() || (!newTicket.content.trim() && attachmentIDs.length === 0)) {
    app.showError(t('tickets.required'))
    return
  }
  const fingerprint = JSON.stringify([newTicket.subject, newTicket.content, newTicket.contact, newTicket.category, newTicket.priority, attachmentIDs])
  if (fingerprint !== createFingerprint) { newTicket.client_id = newClientID(); createFingerprint = fingerprint }
  const payload: CreateTicket = {
    ...newTicket,
    subject: newTicket.subject.trim(),
    content: newTicket.content.trim(),
    contact: newTicket.contact.trim(),
    ...(attachmentIDs.length ? { attachment_ids: attachmentIDs } : {})
  }
  if (await create(payload)) {
    newTicketOpen.value = false
    Object.assign(newTicket, { subject: '', content: '', contact: '', category: 'api', priority: 'normal', client_id: newClientID() })
    clearPendingImages(newTicketImages)
    createFingerprint = ''
  }
}
onBeforeUnmount(() => {
  mounted = false
  window.clearTimeout(proactiveSearchTimer)
  modalUploadControllers.forEach((controller) => controller.abort())
  modalUploadControllers.clear()
  clearPendingImages(newTicketImages)
  clearPendingImages(proactiveImages)
})
</script>
