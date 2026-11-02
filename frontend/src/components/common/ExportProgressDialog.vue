<template>
  <BaseDialog :show="show" :title="t('usage.exporting')" width="narrow" @close="handleCancel">
    <div class="space-y-4">
      <div class="text-sm text-muted">
        {{ t('usage.exportingProgress') }}
      </div>
      <div class="flex items-center justify-between text-sm text-default">
        <span>{{ t('usage.exportedCount', { current, total }) }}</span>
        <span class="font-medium tabular-nums text-highlighted">{{ normalizedProgress }}%</span>
      </div>
      <UProgress
        :model-value="normalizedProgress"
        size="sm"
        color="primary"
        :get-value-label="() => `${t('usage.exportingProgress')}: ${normalizedProgress}%`"
      />
      <div v-if="estimatedTime" class="text-xs text-muted" aria-live="polite" aria-atomic="true">
        {{ t('usage.estimatedTime', { time: estimatedTime }) }}
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <UButton
          color="neutral"
          variant="subtle"
          :label="t('usage.cancelExport')"
          @click="handleCancel"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from './BaseDialog.vue'

interface Props {
  show: boolean
  progress: number
  current: number
  total: number
  estimatedTime: string
}

interface Emits {
  (e: 'cancel'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()

const normalizedProgress = computed(() => {
  const value = Number.isFinite(props.progress) ? props.progress : 0
  return Math.min(100, Math.max(0, Math.round(value)))
})

const handleCancel = () => {
  emit('cancel')
}
</script>
