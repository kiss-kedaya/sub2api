<template>
  <div>
    <!-- 铃铛按钮 -->
    <UButton
      color="neutral"
      variant="ghost"
      square
      :aria-label="t('announcements.title')"
      @click="openModal"
    >
      <UChip color="error" :show="totalUnreadCount > 0" inset>
        <UIcon name="i-lucide-bell" class="size-5 shrink-0" />
      </UChip>
    </UButton>

    <!-- 公告与分组订阅通知抽屉 -->
    <USlideover
      v-model:open="isModalOpen"
      :title="t('announcements.title')"
      :description="activeUnreadCount > 0 ? `${activeUnreadCount} ${t('announcements.unread')}` : undefined"
    >
      <template #actions>
        <UButton
          v-if="activeUnreadCount > 0"
          color="neutral"
          variant="subtle"
          size="sm"
          icon="i-lucide-check-check"
          :label="t('announcements.markAllRead')"
          :disabled="activeTab === 'announcements' ? loading : groupWatchLoading"
          @click="markActiveAllRead"
        />
      </template>

      <template #body>
        <!-- 分区切换：站点公告 / 分组订阅 -->
        <div
          class="mb-4 grid grid-cols-2 gap-1 rounded-xl bg-elevated p-1"
          role="tablist"
          :aria-label="t('announcements.title')"
        >
          <button
            type="button"
            role="tab"
            :aria-selected="activeTab === 'announcements'"
            class="flex items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
            :class="activeTab === 'announcements' ? 'bg-default text-highlighted shadow-xs ring-1 ring-default' : 'text-muted hover:text-highlighted'"
            data-test="bell-tab-announcements"
            @click="switchTab('announcements')"
          >
            <span>{{ t('announcements.tabAnnouncements') }}</span>
            <UBadge v-if="unreadCount > 0" color="error" variant="subtle" size="xs" class="tabular-nums">{{ unreadCount }}</UBadge>
          </button>
          <button
            type="button"
            role="tab"
            :aria-selected="activeTab === 'group_watch'"
            class="flex items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
            :class="activeTab === 'group_watch' ? 'bg-default text-highlighted shadow-xs ring-1 ring-default' : 'text-muted hover:text-highlighted'"
            data-test="bell-tab-group-watch"
            @click="switchTab('group_watch')"
          >
            <span>{{ t('announcements.tabGroupWatch') }}</span>
            <UBadge v-if="groupWatchUnreadCount > 0" color="error" variant="subtle" size="xs" class="tabular-nums">{{ groupWatchUnreadCount }}</UBadge>
          </button>
        </div>

        <template v-if="activeTab === 'announcements'">
          <!-- Loading -->
          <div v-if="loading && announcements.length === 0" class="flex items-center justify-center py-16">
            <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
          </div>

          <!-- Failure: keep it visible instead of falling back to an empty timeline -->
          <div v-else-if="listError" class="flex flex-col items-center gap-3 py-16 text-center">
            <UIcon name="i-lucide-triangle-alert" class="size-6 text-error" />
            <p class="text-sm text-muted">{{ t('announcements.loadFailed') }}</p>
            <UButton
              color="neutral"
              variant="subtle"
              size="sm"
              icon="i-lucide-refresh-cw"
              :label="t('common.tryAgain')"
              @click="retryList"
            />
          </div>

          <!-- Announcements List -->
          <div v-else-if="announcements.length > 0" class="-mx-3 -my-3 space-y-0.5">
            <UButton
              v-for="item in announcements"
              :key="item.id"
              color="neutral"
              variant="ghost"
              block
              class="justify-start gap-3 px-3 py-2.5 text-left font-normal hover:bg-elevated/50"
              data-test="announcement-row"
              @click="openDetail(item)"
            >
              <UChip color="error" :show="!item.read_at" inset>
                <UAvatar
                  :icon="item.read_at ? 'i-lucide-megaphone' : 'i-lucide-bell-ring'"
                  size="md"
                  :ui="{ root: item.read_at ? 'bg-elevated' : 'bg-primary/10', icon: item.read_at ? 'text-dimmed' : 'text-primary' }"
                />
              </UChip>
              <div class="min-w-0 flex-1 text-sm">
                <p class="flex items-center justify-between gap-3">
                  <span class="truncate font-medium text-highlighted">{{ item.title }}</span>
                  <time class="shrink-0 text-xs text-muted tabular-nums">{{ formatRelativeTime(item.created_at) }}</time>
                </p>
                <p class="truncate text-dimmed">{{ rowExcerpt(item) }}</p>
              </div>
            </UButton>
          </div>

          <!-- Empty State -->
          <EmptyState
            v-else
            :title="t('announcements.empty')"
            :description="t('announcements.emptyDescription')"
          />
        </template>

        <template v-else>
          <div v-if="groupWatchLoading && groupWatchNotifications.length === 0" class="flex items-center justify-center py-16">
            <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
          </div>

          <div v-else-if="groupWatchError" class="flex flex-col items-center gap-3 py-16 text-center">
            <UIcon name="i-lucide-triangle-alert" class="size-6 text-error" />
            <p class="text-sm text-muted">{{ t('announcements.groupWatchLoadFailed') }}</p>
            <UButton
              color="neutral"
              variant="subtle"
              size="sm"
              icon="i-lucide-refresh-cw"
              :label="t('common.tryAgain')"
              @click="retryGroupWatchList"
            />
          </div>

          <div v-else-if="groupWatchNotifications.length > 0" class="-mx-2 space-y-1.5">
            <button
              v-for="item in groupWatchNotifications"
              :key="item.id"
              type="button"
              class="flex w-full items-start gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-elevated/60"
              :class="!item.read_at ? 'bg-primary/[0.04]' : ''"
              data-test="group-watch-row"
              @click="openGroupWatchItem(item)"
            >
              <UChip color="error" :show="!item.read_at" inset>
                <span
                  class="grid size-9 shrink-0 place-items-center rounded-xl"
                  :class="groupWatchEventIconClass(item.event_type)"
                >
                  <UIcon :name="groupWatchEventIcon(item.event_type)" class="size-4" />
                </span>
              </UChip>
              <div class="min-w-0 flex-1 text-sm">
                <p class="flex items-center justify-between gap-2">
                  <span class="truncate font-medium text-highlighted">{{ item.title }}</span>
                  <time class="shrink-0 text-xs text-muted tabular-nums">{{ formatRelativeTime(item.created_at) }}</time>
                </p>
                <p class="mt-0.5 line-clamp-2 text-xs leading-relaxed text-muted">{{ item.content }}</p>
              </div>
            </button>

            <div class="pt-2 text-center">
              <UButton
                color="neutral"
                variant="subtle"
                size="xs"
                icon="i-lucide-activity"
                :label="t('announcements.goToChannelStatus')"
                @click="goToChannelStatus"
              />
            </div>
          </div>

          <EmptyState
            v-else
            :title="t('announcements.groupWatchEmpty')"
            :description="t('announcements.groupWatchEmptyDescription')"
          >
            <template #action>
              <UButton
                color="primary"
                variant="soft"
                size="sm"
                icon="i-lucide-activity"
                :label="t('announcements.goToChannelStatus')"
                @click="goToChannelStatus"
              />
            </template>
          </EmptyState>
        </template>
      </template>
    </USlideover>

    <!-- 公告详情：点开才拉正文 -->
    <UModal
      v-model:open="detailModalOpen"
      :title="selectedAnnouncement?.title"
      :ui="{ content: 'sm:max-w-3xl', body: 'max-h-[60dvh] overflow-y-auto', footer: 'justify-between gap-3' }"
      @after:leave="onDetailClosed"
    >
      <template #description>
        <span v-if="selectedAnnouncement" class="flex flex-wrap items-center gap-x-4 gap-y-1 text-muted">
          <span class="inline-flex items-center gap-1.5">
            <UIcon name="i-lucide-clock" class="size-4" />
            <time class="tabular-nums">{{ formatRelativeWithDateTime(selectedAnnouncement.created_at) }}</time>
          </span>
          <UBadge
            :color="selectedRead ? 'neutral' : 'primary'"
            variant="subtle"
            size="sm"
            :label="selectedRead ? t('announcements.read') : t('announcements.unread')"
          />
        </span>
      </template>

      <template #body>
        <div v-if="detailLoading" class="flex items-center justify-center py-12">
          <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
        </div>
        <div v-else-if="detailError" class="flex flex-col items-center gap-3 py-12 text-center">
          <UIcon name="i-lucide-triangle-alert" class="size-6 text-error" />
          <p class="text-sm text-muted">{{ t('announcements.loadFailed') }}</p>
          <UButton
            color="neutral"
            variant="subtle"
            size="sm"
            icon="i-lucide-refresh-cw"
            :label="t('common.tryAgain')"
            @click="retryDetail"
          />
        </div>
        <div
          v-else-if="detailContent"
          class="markdown-body prose prose-sm max-w-none dark:prose-invert"
          v-html="renderMarkdown(detailContent)"
        ></div>
        <p v-else class="py-8 text-center text-sm text-muted">{{ t('announcements.empty') }}</p>
      </template>

      <template #footer>
        <span class="text-xs text-muted">
          {{ selectedRead ? t('announcements.readStatus') : t('announcements.markReadHint') }}
        </span>
        <div class="flex items-center gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="closeDetail" />
          <UButton
            v-if="selectedAnnouncement && !selectedRead"
            color="primary"
            icon="i-lucide-check"
            :label="t('announcements.markRead')"
            @click="markAsReadAndClose(selectedAnnouncement.id)"
          />
        </div>
      </template>
    </UModal>
  </div>
</template>
<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAppStore } from '@/stores/app'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeTime, formatRelativeWithDateTime } from '@/utils/format'
import type { AnnouncementListItem } from '@/api/announcements'
import type { GroupWatchEventType, GroupWatchNotification } from '@/api/groupWatch'
import EmptyState from './EmptyState.vue'
import '@/styles/announcement-markdown.css'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const announcementStore = useAnnouncementStore()

// Configure marked
marked.setOptions({
  breaks: true,
  gfm: true,
})

// Badge counts come from the summary endpoint; the timeline is fetched on open.
const {
  announcements,
  loading,
  listError,
  unreadCount,
  groupWatchUnreadCount,
  groupWatchNotifications,
  groupWatchLoading,
  groupWatchError,
  totalUnreadCount,
} = storeToRefs(announcementStore)

// Local overlay state (the slideover / modal handle Escape, outside click and body scroll lock)
const isModalOpen = ref(false)
const activeTab = ref<'announcements' | 'group_watch'>('announcements')
const detailModalOpen = ref(false)
const selectedAnnouncement = ref<AnnouncementListItem | null>(null)
const detailId = ref<number | null>(null)

const activeUnreadCount = computed(() => (
  activeTab.value === 'announcements' ? unreadCount.value : groupWatchUnreadCount.value
))

const selectedRead = computed(() => {
  const selected = selectedAnnouncement.value
  if (!selected) return false
  const current = announcements.value.find((item) => item.id === selected.id)
  return !!(current ?? selected).read_at
})

const detailLoading = computed(() => announcementStore.isDetailLoading(detailId.value ?? 0))
const detailError = computed(() => announcementStore.detailErrorFor(detailId.value))

const detailContent = computed(() => {
  const id = detailId.value
  if (!id) return ''
  const cached = announcementStore.detailFor(id)
  return cached?.content || selectedAnnouncement.value?.content || ''
})

// Methods
function renderMarkdown(content: string): string {
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
}

// One line of plain text under each title, like the template's notification rows.
// The list endpoint already ships an excerpt; older payloads fall back to the body.
function rowExcerpt(item: AnnouncementListItem): string {
  if (item.excerpt) return item.excerpt
  return excerpt(item.content || '')
}

function excerpt(content: string): string {
  if (!content) return ''
  const html = renderMarkdown(content)
  const text = new DOMParser().parseFromString(html, 'text/html').body.textContent || ''
  return text.replace(/\s+/g, ' ').trim().slice(0, 140)
}

function openModal() {
  isModalOpen.value = true
  // If only group-watch alerts have unread items, land directly on that tab.
  if (unreadCount.value === 0 && groupWatchUnreadCount.value > 0) {
    activeTab.value = 'group_watch'
  }
  // Lazy: the timelines are only fetched when the panel is actually opened.
  void announcementStore.ensureList()
  void announcementStore.ensureGroupWatchList()
}

function switchTab(tab: 'announcements' | 'group_watch') {
  activeTab.value = tab
  if (tab === 'group_watch') {
    void announcementStore.ensureGroupWatchList()
  } else {
    void announcementStore.ensureList()
  }
}

function retryList() {
  void announcementStore.ensureList(true)
}

function retryGroupWatchList() {
  void announcementStore.ensureGroupWatchList(true)
}

function groupWatchEventIcon(eventType: GroupWatchEventType): string {
  switch (eventType) {
    case 'price_changed':
      return 'i-lucide-trending-up'
    case 'status_black':
      return 'i-lucide-alert-octagon'
    case 'status_green':
      return 'i-lucide-check-circle-2'
    default:
      return 'i-lucide-bell-ring'
  }
}

function groupWatchEventIconClass(eventType: GroupWatchEventType): string {
  switch (eventType) {
    case 'price_changed':
      return 'bg-amber-500/12 text-amber-600 dark:bg-amber-500/15 dark:text-amber-400'
    case 'status_black':
      return 'bg-neutral-900 text-white dark:bg-zinc-800 dark:text-zinc-100 dark:ring-1 dark:ring-zinc-600/70'
    case 'status_green':
      return 'bg-emerald-500/12 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-400'
    default:
      return 'bg-primary/10 text-primary'
  }
}

async function openGroupWatchItem(item: GroupWatchNotification) {
  if (!item.read_at) {
    try {
      await announcementStore.markGroupWatchRead(item.id)
    } catch (err: any) {
      appStore.showError(err?.message || t('common.unknownError'))
    }
  }
}

function goToChannelStatus() {
  isModalOpen.value = false
  if (router?.push) void router.push('/monitor')
}

async function markActiveAllRead() {
  if (activeTab.value === 'group_watch') {
    try {
      await announcementStore.markAllGroupWatchRead()
      appStore.showSuccess(t('announcements.groupWatchAllMarkedRead'))
    } catch (err: any) {
      appStore.showError(err?.message || t('common.unknownError'))
    }
    return
  }
  await markAllAsRead()
}

function openDetail(announcement: AnnouncementListItem) {
  selectedAnnouncement.value = announcement
  detailId.value = announcement.id
  detailModalOpen.value = true
  // The row has no body: load it now, and keep the failure visible if it fails.
  if (!announcement.content && !announcementStore.detailFor(announcement.id)) {
    void announcementStore.fetchDetail(announcement.id)
  }
  if (!announcement.read_at) {
    markAsRead(announcement.id)
  }
}

function retryDetail() {
  if (detailId.value) void announcementStore.fetchDetail(detailId.value, true)
}

function closeDetail() {
  detailModalOpen.value = false
}

function onDetailClosed() {
  selectedAnnouncement.value = null
  detailId.value = null
}

async function markAsRead(id: number) {
  try {
    await announcementStore.markAsRead(id)
  } catch (err: any) {
    appStore.showError(err?.message || t('common.unknownError'))
  }
}

async function markAsReadAndClose(id: number) {
  await markAsRead(id)
  appStore.showSuccess(t('announcements.markedAsRead'))
  closeDetail()
}

async function markAllAsRead() {
  try {
    await announcementStore.markAllAsRead()
    appStore.showSuccess(t('announcements.allMarkedAsRead'))
  } catch (err: any) {
    appStore.showError(err?.message || t('common.unknownError'))
  }
}
</script>
