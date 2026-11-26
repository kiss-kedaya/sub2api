<template>
  <div class="ticket-image-picker">
    <input
      ref="fileInput"
      type="file"
      class="sr-only"
      :accept="TICKET_IMAGE_ACCEPT"
      multiple
      :disabled="disabled || items.length >= maxCount"
      @change="onFileChange"
    />

    <div v-if="items.length" class="ticket-pending-images" role="list" :aria-label="t('tickets.addImage')">
      <div
        v-for="item in items"
        :key="item.localID"
        class="ticket-pending-image"
        :class="{ uploading: item.status === 'uploading', error: item.status === 'error' }"
        role="listitem"
      >
        <img v-if="item.previewURL" :src="item.previewURL" :alt="item.fileName" />
        <div v-else class="ticket-pending-fallback">{{ item.fileName }}</div>
        <span v-if="item.status === 'uploading'" class="ticket-pending-overlay" :aria-label="t('tickets.imageUploading')">
          <Icon name="refresh" size="sm" class="animate-spin" />
        </span>
        <span v-else-if="item.status === 'error'" class="ticket-pending-overlay ticket-pending-error" :title="item.error || t('tickets.imageUploadFailed')">
          <Icon name="exclamationCircle" size="sm" />
        </span>
        <button
          type="button"
          class="ticket-pending-remove"
          :disabled="disabled"
          :title="t('tickets.imageRemove')"
          :aria-label="t('tickets.imageRemove')"
          @click="emit('remove', item.localID)"
        >
          <Icon name="x" size="xs" />
        </button>
      </div>
    </div>

    <div v-if="showTrigger" class="ticket-picker-trigger-row">
      <button
        type="button"
        class="ticket-button ticket-picker-button"
        :disabled="disabled || items.length >= maxCount"
        @click="openPicker"
      >
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909M3.75 19.5h16.5A2.25 2.25 0 0022.5 17.25V6.75A2.25 2.25 0 0020.25 4.5H3.75A2.25 2.25 0 001.5 6.75v10.5A2.25 2.25 0 003.75 19.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
        </svg>
        <span>{{ t('tickets.addImage') }} ({{ items.length }}/{{ maxCount }})</span>
      </button>
      <span class="ticket-picker-hint">{{ t('tickets.imageHint') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { TICKET_IMAGE_ACCEPT, TICKET_IMAGE_MAX_COUNT, type PendingTicketImage } from './ticketImages'

withDefaults(defineProps<{
  items: PendingTicketImage[]
  disabled?: boolean
  maxCount?: number
  showTrigger?: boolean
}>(), {
  disabled: false,
  maxCount: TICKET_IMAGE_MAX_COUNT,
  showTrigger: true
})

const emit = defineEmits<{
  select: [files: File[]]
  remove: [localID: string]
}>()

const { t } = useI18n()
const fileInput = ref<HTMLInputElement | null>(null)

function openPicker() {
  fileInput.value?.click()
}

function onFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const files = input.files ? Array.from(input.files) : []
  input.value = ''
  if (files.length) emit('select', files)
}

defineExpose({ openPicker })
</script>

<style scoped>
.ticket-image-picker { display: flex; flex-direction: column; gap: 8px; }
.ticket-pending-images { display: flex; flex-wrap: wrap; gap: 8px; }
.ticket-pending-image { position: relative; width: 68px; height: 68px; border-radius: 8px; overflow: hidden; border: 1px solid var(--signal-line); background: var(--signal-bg); flex-shrink: 0; }
.ticket-pending-image.error { border-color: #dc2626; }
.ticket-pending-image img { width: 100%; height: 100%; object-fit: cover; display: block; }
.ticket-pending-fallback { width: 100%; height: 100%; display: grid; place-items: center; padding: 4px; font-size: 10px; color: var(--signal-muted); word-break: break-all; text-align: center; }
.ticket-pending-overlay { position: absolute; inset: 0; display: grid; place-items: center; background: rgba(15, 23, 42, 0.45); color: #fff; }
.ticket-pending-error { background: rgba(220, 38, 38, 0.65); }
.ticket-pending-remove { position: absolute; top: 3px; right: 3px; width: 20px; height: 20px; border-radius: 50%; display: grid; place-items: center; background: rgba(15, 23, 42, 0.72); color: #fff; border: 0; cursor: pointer; transition: background-color 120ms; }
.ticket-pending-remove:hover:not(:disabled) { background: rgba(15, 23, 42, 0.92); }
.ticket-picker-trigger-row { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; }
.ticket-picker-button { min-height: 32px; padding: 5px 10px; font-size: 12px; }
.ticket-picker-hint { font-size: 11px; color: var(--signal-muted); line-height: 1.5; }
</style>
