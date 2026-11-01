<template>
  <div
    v-if="uniqueIps.length > 0"
    class="flex shrink-0 items-center justify-end gap-2 px-4 py-2"
  >
    <span v-if="pendingCount > 0" class="text-xs text-muted tabular-nums">
      {{ t('usage.ipGeo.pending', { count: pendingCount }) }}
    </span>
    <UButton
      color="primary"
      variant="ghost"
      size="xs"
      icon="i-lucide-map-pin"
      :loading="loading"
      :disabled="loading || pendingCount === 0"
      :label="loading ? t('usage.ipGeo.batchFetching') : t('usage.ipGeo.batchFetch')"
      @click="run"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchBatch, getEntry } from '@/utils/ipGeoLookup'

// 当前页 IP 批量地理查询工具条:传入原始 IP 列表(可含空值),内部去重;
// 无 IP 时自身不渲染。批量失败 emit failed,由使用方弹提示。
const props = defineProps<{
  ips: Array<string | null | undefined>
}>()

const emit = defineEmits<{
  (e: 'failed'): void
}>()

const { t } = useI18n()

const uniqueIps = computed(() =>
  Array.from(new Set(props.ips.filter((ip): ip is string => Boolean(ip))))
)

const pendingCount = computed(() =>
  uniqueIps.value.filter((ip) => {
    const status = getEntry(ip).status
    return status === 'idle' || status === 'error'
  }).length
)

const loading = ref(false)

const run = async () => {
  loading.value = true
  try {
    const ok = await fetchBatch(uniqueIps.value)
    if (!ok) emit('failed')
  } finally {
    loading.value = false
  }
}
</script>
