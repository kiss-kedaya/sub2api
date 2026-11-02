<template>
  <div v-if="visible" class="space-y-1">
    <div class="flex flex-wrap items-center gap-1.5">
      <UButton
        color="neutral"
        variant="ghost"
        size="xs"
        icon="i-lucide-radar"
        class="px-1.5"
        :loading="loading"
        :disabled="loading"
        :title="t('admin.accounts.usageWindow.grokProbeTooltip')"
        :label="t('admin.accounts.usageWindow.grokProbe')"
        @click="handleProbe"
      />
    </div>

    <!-- Compact mode: parent already shows 7d/30d/prepaid or 24h — only surface errors. -->
    <div v-if="!compact && summary" class="text-xs text-muted">
      {{ summary }}
    </div>
    <div v-if="error" class="truncate text-xs text-error" :title="error">
      {{ truncatedError }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { GrokQuotaProbeResult } from '@/api/admin/grok'
import type { Account } from '@/types'

const props = withDefaults(
  defineProps<{
    account: Account
    /** When true, only show the probe button (+ errors). No duplicate weekly summary. */
    compact?: boolean
  }>(),
  { compact: false }
)

const emit = defineEmits<{ probed: [result: GrokQuotaProbeResult] }>()

const { t } = useI18n()

const visible = computed(() => props.account.platform === 'grok' && props.account.type === 'oauth')
const loading = ref(false)
const error = ref<string | null>(null)
const data = ref<GrokQuotaProbeResult | null>(null)

const extractErrorMessage = (e: unknown): string => {
  const err = e as {
    message?: string
    reason?: string
    response?: { data?: { message?: string; error?: string } }
  }
  return (
    err?.message ||
    err?.reason ||
    err?.response?.data?.message ||
    err?.response?.data?.error ||
    t('common.error')
  )
}

const summary = computed(() => {
  if (props.compact || !data.value) return ''
  // Non-compact fallback (rarely used): brief weekly percent if present.
  const billing = data.value.billing
  if (billing?.period_type?.toLowerCase() === 'weekly' && billing.usage_percent != null) {
    return t('admin.accounts.usageWindow.grokWeeklyUsage', {
      percent: Math.round(Math.min(100, Math.max(0, billing.usage_percent)))
    })
  }
  return ''
})

const truncatedError = computed(() => {
  if (!error.value) return ''
  return error.value.length > 80 ? `${error.value.slice(0, 80)}...` : error.value
})

const handleProbe = async () => {
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    data.value = await adminAPI.grok.queryQuota(props.account.id)
    error.value = data.value.probe_error || null
    emit('probed', data.value)
  } catch (e) {
    error.value = extractErrorMessage(e)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.account.id,
  () => {
    data.value = null
    error.value = null
    loading.value = false
  }
)
</script>
