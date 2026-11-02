<template>
  <!-- Must be acknowledged with the button: no outside-click / Escape dismissal, no close icon. -->
  <UModal
    :open="!!displayedAnnouncement"
    :dismissible="false"
    :close="false"
    :title="displayedAnnouncement?.title"
    :ui="{ content: 'sm:max-w-2xl', body: 'max-h-[50vh] overflow-y-auto', footer: 'justify-end' }"
  >
    <template #description>
      <span class="flex flex-wrap items-center gap-2">
        <UBadge v-if="!preview" color="primary" variant="subtle" size="sm" :label="t('announcements.unread')" />
        <span class="inline-flex items-center gap-1.5 text-muted">
          <UIcon name="i-lucide-clock" class="size-4" />
          <time>{{ displayedAnnouncement ? formatRelativeWithDateTime(displayedAnnouncement.created_at) : '' }}</time>
        </span>
      </span>
    </template>

    <template #body>
      <div v-if="bodyLoading" class="flex items-center justify-center py-10">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
      </div>
      <div v-else-if="bodyError" class="flex flex-col items-center gap-3 py-10 text-center">
        <UIcon name="i-lucide-triangle-alert" class="size-6 text-error" />
        <p class="text-sm text-muted">{{ t('announcements.loadFailed') }}</p>
        <UButton
          color="neutral"
          variant="subtle"
          size="sm"
          icon="i-lucide-refresh-cw"
          :label="t('common.tryAgain')"
          @click="retryBody"
        />
      </div>
      <div
        v-else
        class="markdown-body prose prose-sm max-w-none dark:prose-invert"
        v-html="renderedContent"
      ></div>
    </template>

    <template #footer>
      <UButton
        data-testid="announcement-popup-dismiss"
        :color="preview ? 'neutral' : 'primary'"
        :variant="preview ? 'subtle' : 'solid'"
        :icon="preview ? 'i-lucide-x' : 'i-lucide-check'"
        :label="preview ? t('common.close') : t('announcements.markRead')"
        @click="handleDismiss"
      />
    </template>
  </UModal>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeWithDateTime } from '@/utils/format'
import type { Announcement, UserAnnouncement } from '@/types'
import '@/styles/announcement-markdown.css'

type PreviewAnnouncement = Pick<Announcement | UserAnnouncement, 'title' | 'content' | 'created_at'> & {
  id?: number
}

const props = withDefaults(defineProps<{
  announcement?: PreviewAnnouncement | null
  preview?: boolean
}>(), {
  announcement: null,
  preview: false,
})

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const displayedAnnouncement = computed(() => (
  props.preview ? props.announcement : announcementStore.currentPopup
))

marked.setOptions({
  breaks: true,
  gfm: true,
})

// The popup bootstrap only ships metadata for the list path; the body arrives
// from the detail endpoint the first time the popup is shown.
const bodyId = computed(() => (props.preview ? null : displayedAnnouncement.value?.id ?? null))

const resolvedContent = computed(() => {
  const id = bodyId.value
  if (id) {
    const cached = announcementStore.detailFor(id)
    if (cached?.content) return cached.content
  }
  return displayedAnnouncement.value?.content || ''
})

const bodyLoading = computed(() => {
  const id = bodyId.value
  return !!id && announcementStore.isDetailLoading(id)
})

const bodyError = computed(() => {
  const id = bodyId.value
  if (!id) return null
  if (resolvedContent.value) return null
  return announcementStore.detailErrorFor(id)
})

watch(
  () => bodyId.value,
  (id) => {
    if (!id) return
    if (resolvedContent.value) return
    void announcementStore.fetchDetail(id)
  },
  { immediate: true }
)

function retryBody() {
  const id = bodyId.value
  if (id) void announcementStore.fetchDetail(id, true)
}

const renderedContent = computed(() => {
  const content = resolvedContent.value
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

function handleDismiss() {
  if (props.preview) {
    emit('close')
    return
  }
  announcementStore.dismissPopup()
}
// Body scroll locking comes from the modal (shared with the announcement bell's overlays).
</script>
