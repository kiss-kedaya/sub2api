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
      <UChip color="error" :show="unreadCount > 0" inset>
        <UIcon name="i-lucide-bell" class="size-5 shrink-0" />
      </UChip>
    </UButton>

    <!-- 公告列表 -->
    <USlideover
      v-model:open="isModalOpen"
      :title="t('announcements.title')"
      :description="unreadCount > 0 ? `${unreadCount} ${t('announcements.unread')}` : undefined"
    >
      <template #actions>
        <UButton
          v-if="unreadCount > 0"
          color="neutral"
          variant="subtle"
          size="sm"
          icon="i-lucide-check-check"
          :label="t('announcements.markAllRead')"
          :disabled="loading"
          @click="markAllAsRead"
        />
      </template>

      <template #body>
        <!-- Loading -->
        <div v-if="loading" class="flex items-center justify-center py-16">
          <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
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
                <time class="shrink-0 text-xs text-muted">{{ formatRelativeTime(item.created_at) }}</time>
              </p>
              <p class="truncate text-dimmed">{{ excerpt(item.content) }}</p>
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
    </USlideover>

    <!-- 公告详情 -->
    <UModal
      v-model:open="detailModalOpen"
      :title="selectedAnnouncement?.title"
      :ui="{ content: 'sm:max-w-3xl', body: 'max-h-[60vh] overflow-y-auto', footer: 'justify-between gap-3' }"
      @after:leave="selectedAnnouncement = null"
    >
      <template #description>
        <span v-if="selectedAnnouncement" class="flex flex-wrap items-center gap-x-4 gap-y-1 text-muted">
          <span class="inline-flex items-center gap-1.5">
            <UIcon name="i-lucide-clock" class="size-4" />
            <time>{{ formatRelativeWithDateTime(selectedAnnouncement.created_at) }}</time>
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
        <div
          v-if="selectedAnnouncement"
          class="markdown-body prose prose-sm max-w-none dark:prose-invert"
          v-html="renderMarkdown(selectedAnnouncement.content)"
        ></div>
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
import { storeToRefs } from 'pinia'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAppStore } from '@/stores/app'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeTime, formatRelativeWithDateTime } from '@/utils/format'
import type { UserAnnouncement } from '@/types'
import EmptyState from './EmptyState.vue'
import '@/styles/announcement-markdown.css'

const { t } = useI18n()
const appStore = useAppStore()
const announcementStore = useAnnouncementStore()

// Configure marked
marked.setOptions({
  breaks: true,
  gfm: true,
})

// Use store state (storeToRefs for reactivity)
const { announcements, loading } = storeToRefs(announcementStore)
const unreadCount = computed(() => announcementStore.unreadCount)

// Local overlay state (the slideover / modal handle Escape, outside click and body scroll lock)
const isModalOpen = ref(false)
const detailModalOpen = ref(false)
const selectedAnnouncement = ref<UserAnnouncement | null>(null)

// Read state from the store, so the detail updates as soon as it is marked read.
const selectedRead = computed(() => {
  const selected = selectedAnnouncement.value
  if (!selected) return false
  const current = announcements.value.find((item) => item.id === selected.id)
  return !!(current ?? selected).read_at
})

// Methods
function renderMarkdown(content: string): string {
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
}

// One line of plain text under each title, like the template's notification rows.
function excerpt(content: string): string {
  if (!content) return ''
  const html = renderMarkdown(content)
  const text = new DOMParser().parseFromString(html, 'text/html').body.textContent || ''
  return text.replace(/\s+/g, ' ').trim().slice(0, 140)
}

function openModal() {
  isModalOpen.value = true
}

function openDetail(announcement: UserAnnouncement) {
  selectedAnnouncement.value = announcement
  detailModalOpen.value = true
  if (!announcement.read_at) {
    markAsRead(announcement.id)
  }
}

function closeDetail() {
  detailModalOpen.value = false
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
