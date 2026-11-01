<template>
  <div class="flex items-center justify-end gap-0.5">
    <UButton
      color="neutral"
      variant="ghost"
      size="sm"
      square
      icon="i-lucide-pencil"
      :aria-label="t('common.edit')"
      :title="t('common.edit')"
      @click="$emit('edit', row)"
    />
    <UButton
      data-testid="monitor-duplicate"
      color="neutral"
      variant="ghost"
      size="sm"
      square
      icon="i-lucide-copy"
      :loading="duplicating"
      :title="duplicateTitle"
      :aria-label="duplicateTitle"
      :disabled="duplicating || Boolean(row.api_key_decrypt_failed)"
      @click="$emit('duplicate', row)"
    >
      <span class="sr-only">{{ duplicating ? t('admin.channelMonitor.duplicating') : t('admin.channelMonitor.duplicate') }}</span>
    </UButton>
    <UDropdownMenu :items="menuItems" :content="{ align: 'end' }">
      <UButton
        color="neutral"
        variant="ghost"
        size="sm"
        square
        icon="i-lucide-ellipsis-vertical"
        data-testid="monitor-more"
        :aria-label="t('admin.channelMonitor.columns.actions')"
      />
    </UDropdownMenu>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'
import type { ChannelMonitor } from '@/api/admin/channelMonitor'

const props = defineProps<{
  row: ChannelMonitor
  running: boolean
  duplicating: boolean
}>()

const emit = defineEmits<{
  (e: 'run', row: ChannelMonitor): void
  (e: 'duplicate', row: ChannelMonitor): void
  (e: 'edit', row: ChannelMonitor): void
  (e: 'delete', row: ChannelMonitor): void
}>()

const { t } = useI18n()
const duplicateTitle = computed(() => {
  if (props.row.api_key_decrypt_failed) return t('admin.channelMonitor.duplicateKeyUnavailable')
  if (props.duplicating) return t('admin.channelMonitor.duplicating')
  return t('admin.channelMonitor.duplicate')
})
const menuItems = computed<DropdownMenuItem[][]>(() => [
  [{ label: t('admin.channelMonitor.runNow'), icon: props.running ? 'i-lucide-loader-circle' : 'i-lucide-play', disabled: props.running, onSelect: () => emit('run', props.row) }],
  [{ label: t('common.delete'), icon: 'i-lucide-trash-2', color: 'error', onSelect: () => emit('delete', props.row) }],
])
</script>
