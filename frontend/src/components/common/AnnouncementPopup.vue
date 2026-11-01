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
      <div
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
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeWithDateTime } from '@/utils/format'
import type { Announcement, UserAnnouncement } from '@/types'
import '@/styles/announcement-markdown.css'

type PreviewAnnouncement = Pick<Announcement | UserAnnouncement, 'title' | 'content' | 'created_at'>

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

const renderedContent = computed(() => {
  const content = displayedAnnouncement.value?.content
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
