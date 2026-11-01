<template>
  <BaseDialog :show="show" :title="t('usage.errors.detail.title')" width="wide" @close="emit('update:show', false)">
    <div v-if="loading" class="flex justify-center py-10">
      <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
    </div>

    <UAlert
      v-else-if="loadError"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :description="t('usage.errors.detail.loadFailed')"
    />

    <div v-else-if="detail" class="space-y-4 text-sm">
      <dl class="grid grid-cols-2 gap-x-6 gap-y-3">
        <div>
          <dt class="text-muted">{{ t('usage.errors.time') }}</dt>
          <dd class="mt-0.5 text-highlighted">{{ formatDateTime(detail.created_at) }}</dd>
        </div>
        <div>
          <dt class="text-muted">{{ t('usage.errors.model') }}</dt>
          <dd class="mt-0.5 text-highlighted">{{ detail.model || '-' }}</dd>
        </div>
        <div>
          <dt class="text-muted">{{ t('usage.errors.endpoint') }}</dt>
          <dd class="mt-0.5 break-all text-highlighted">{{ detail.inbound_endpoint || '-' }}</dd>
        </div>
        <div>
          <dt class="text-muted">{{ t('usage.errors.status') }}</dt>
          <dd class="mt-0.5">
            <UBadge :color="statusColor(detail.status_code)" variant="subtle" class="tabular-nums">{{ detail.status_code || '-' }}</UBadge>
          </dd>
        </div>
        <div>
          <dt class="text-muted">{{ t('usage.errors.category') }}</dt>
          <dd class="mt-0.5 text-highlighted">{{ t('usage.errors.categories.' + detail.category) }}</dd>
        </div>
        <div>
          <dt class="text-muted">{{ t('usage.errors.platform') }}</dt>
          <dd class="mt-0.5 text-highlighted">{{ detail.platform || '-' }}</dd>
        </div>
        <div v-if="detail.upstream_status_code != null">
          <dt class="text-muted">{{ t('usage.errors.detail.upstreamStatus') }}</dt>
          <dd class="mt-0.5 tabular-nums text-highlighted">{{ detail.upstream_status_code }}</dd>
        </div>
      </dl>

      <div v-if="detail.message">
        <p class="text-muted">{{ t('usage.errors.message') }}</p>
        <p class="mt-0.5 break-all text-highlighted">{{ detail.message }}</p>
      </div>

      <div v-if="detail.error_body">
        <p class="text-muted">{{ t('usage.errors.detail.responseBody') }}</p>
        <pre class="mt-1 max-h-[40vh] overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 font-mono text-xs text-toned">{{ detail.error_body }}</pre>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getMyErrorDetail } from '@/api/usage'
import { formatDateTime } from '@/utils/format'
import type { UserErrorRequestDetail } from '@/types'

const props = defineProps<{
  show: boolean
  errorId: number | null
}>()

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
}>()

const { t } = useI18n()

const loading = ref(false)
const loadError = ref(false)
const detail = ref<UserErrorRequestDetail | null>(null)
let requestVersion = 0

watch(
  () => [props.show, props.errorId] as const,
  ([show, id], _, onCleanup) => {
    onCleanup(() => { requestVersion++ })
    if (show && id != null) {
      fetchDetail(id)
    } else if (!show) {
      detail.value = null
      loadError.value = false
    }
  }
)

async function fetchDetail(id: number) {
  const version = ++requestVersion
  loading.value = true
  loadError.value = false
  detail.value = null
  try {
    const result = await getMyErrorDetail(id)
    if (version === requestVersion) detail.value = result
  } catch (e) {
    if (version !== requestVersion) return
    console.error('[UserErrorDetailModal] Failed to load error detail:', e)
    loadError.value = true
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

function statusColor(code: number) {
  if (code >= 500) return 'error' as const
  if (code === 429) return 'warning' as const
  return 'neutral' as const
}
</script>
