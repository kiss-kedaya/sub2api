<template>
  <div class="timeline-image-item">
    <div v-if="loading" class="timeline-image-skeleton rounded-lg" :style="aspectStyle" role="status" :aria-label="t('tickets.loading')">
      <Icon name="refresh" size="sm" class="animate-spin" />
    </div>
    <div v-else-if="failed || !objectURL" class="timeline-image-error rounded-lg" role="alert">
      <Icon name="exclamationCircle" size="sm" />
      <span>{{ t('tickets.imageLoadFailed') }}</span>
      <button type="button" class="ticket-text-button" @click="load">{{ t('tickets.retry') }}</button>
    </div>
    <button
      v-else
      type="button"
      class="timeline-image-thumb rounded-lg"
      :title="attachment.file_name || t('tickets.imagePreview')"
      :aria-label="t('tickets.imagePreview')"
      @click="emit('preview', { url: objectURL, attachment })"
    >
      <img :src="objectURL" :alt="attachment.file_name || t('tickets.addImage')" loading="lazy" @error="onImageError" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { ticketsAPI, type TicketAttachment } from '@/api/tickets'
import { createObjectURLSafe, isSupportedTicketImageType, revokeObjectURLSafe } from './ticketImages'

const props = defineProps<{
  ticketId: number
  attachment: TicketAttachment
  admin: boolean
}>()

const emit = defineEmits<{
  preview: [payload: { url: string; attachment: TicketAttachment }]
}>()

const { t } = useI18n()
const objectURL = ref('')
const loading = ref(true)
const failed = ref(false)
let controller: AbortController | null = null
let active = true

const aspectStyle = computed(() => {
  const w = props.attachment.width
  const h = props.attachment.height
  if (w > 0 && h > 0) {
    const clampedRatio = Math.min(2.2, Math.max(0.5, w / h))
    return { aspectRatio: `${clampedRatio}` }
  }
  return undefined
})

function clearURL() {
  if (objectURL.value) {
    revokeObjectURLSafe(objectURL.value)
    objectURL.value = ''
  }
}

function onImageError() {
  clearURL()
  failed.value = true
}

async function load() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  failed.value = false
  try {
    const blob = await ticketsAPI(props.admin).fetchAttachmentBlob(props.ticketId, props.attachment.id, current.signal)
    if (!active || current.signal.aborted) return
    if (!(blob instanceof Blob) || blob.size <= 0 || (blob.type && !isSupportedTicketImageType(blob.type))) {
      clearURL()
      failed.value = true
      return
    }
    clearURL()
    const nextURL = createObjectURLSafe(blob)
    if (!nextURL) {
      failed.value = true
      return
    }
    objectURL.value = nextURL
  } catch {
    if (active && !current.signal.aborted) failed.value = true
  } finally {
    if (active && !current.signal.aborted) loading.value = false
  }
}

watch([() => props.ticketId, () => props.attachment.id, () => props.admin], () => { void load() }, { immediate: true })

onBeforeUnmount(() => {
  active = false
  controller?.abort()
  clearURL()
})
</script>

<style scoped>
.timeline-image-item { min-width: 0; }
.timeline-image-thumb { display: block; padding: 0; border: 1px solid var(--signal-line); overflow: hidden; background: var(--signal-bg); cursor: zoom-in; max-width: 100%; transition: border-color 140ms var(--motion-out, cubic-bezier(0.23, 1, 0.32, 1)), transform 140ms var(--motion-out, cubic-bezier(0.23, 1, 0.32, 1)); }
@media (hover: hover) and (pointer: fine) {
  .timeline-image-thumb:hover { border-color: var(--signal-accent); }
}
.timeline-image-thumb img { display: block; max-width: 240px; max-height: 220px; width: auto; height: auto; object-fit: cover; }
.timeline-image-skeleton { display: grid; place-items: center; width: 140px; min-height: 96px; max-height: 180px; border: 1px solid var(--signal-line); background: var(--signal-hover); color: var(--signal-muted); }
.timeline-image-error { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 4px; width: 150px; min-height: 96px; padding: 10px; border: 1px dashed var(--signal-line); background: var(--signal-bg); color: var(--signal-muted); font-size: 11px; text-align: center; }
@media (max-width: 640px) {
  .timeline-image-thumb img { max-width: 100%; max-height: 180px; }
}
</style>
