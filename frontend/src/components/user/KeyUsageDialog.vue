<template>
  <BaseDialog
    :show="show"
    :title="t('keys.usageDialog.title')"
    :description="t('keys.usageDialog.subtitle')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4" data-test="key-usage-dialog">
      <div class="flex flex-wrap items-center gap-1.5">
        <UButton
          v-for="item in presets"
          :key="item.key"
          size="xs"
          :color="preset === item.key ? 'primary' : 'neutral'"
          :variant="preset === item.key ? 'solid' : 'outline'"
          :label="item.label"
          @click="selectPreset(item.key)"
        />
        <div v-if="preset === 'custom'" class="ml-1 flex flex-wrap items-center gap-1.5">
          <UInput v-model="customStart" type="date" size="xs" :max="todayISO" :aria-label="t('keys.usageDialog.startDate')" />
          <span class="text-xs text-dimmed">-</span>
          <UInput v-model="customEnd" type="date" size="xs" :max="todayISO" :aria-label="t('keys.usageDialog.endDate')" />
          <UButton size="xs" :label="t('keys.usageDialog.apply')" @click="loadUsage" />
        </div>
      </div>

      <p class="text-[11px] text-dimmed">
        {{ t('keys.usageDialog.rangeHint', { start: data?.start_date || '-', end: data?.end_date || '-', timezone: timezone }) }}
      </p>

      <div v-if="loading" class="space-y-2">
        <USkeleton class="h-16 w-full" />
        <USkeleton class="h-16 w-full" />
      </div>
      <UAlert
        v-else-if="error"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="t('keys.usageDialog.failed')"
      />
      <template v-else>
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="rounded-lg border border-default/70 p-3">
            <div class="text-[11px] font-medium uppercase tracking-wide text-dimmed">{{ t('keys.usageDialog.today') }}</div>
            <div class="mt-1 font-mono text-xl font-semibold tabular-nums text-highlighted">{{ usd(data?.today.actual_cost) }}</div>
            <div class="mt-1 text-xs text-muted">
              {{ fmtNum(data?.today.requests) }} {{ t('keys.usageDialog.requests') }} · {{ fmtNum(data?.today.total_tokens) }} {{ t('keys.usageDialog.tokens') }}
            </div>
          </div>
          <div class="rounded-lg border border-default/70 p-3">
            <div class="text-[11px] font-medium uppercase tracking-wide text-dimmed">{{ t('keys.usageDialog.range') }}</div>
            <div class="mt-1 font-mono text-xl font-semibold tabular-nums text-highlighted">{{ usd(data?.range.actual_cost) }}</div>
            <div class="mt-1 text-xs text-muted">
              {{ fmtNum(data?.range.requests) }} {{ t('keys.usageDialog.requests') }} · {{ fmtNum(data?.range.total_tokens) }} {{ t('keys.usageDialog.tokens') }}
            </div>
          </div>
        </div>

        <UTable
          :data="dailyRows"
          :columns="columns"
          :empty="t('keys.usageDialog.empty')"
          class="min-w-0"
          :ui="{ td: 'py-2 text-xs', th: 'text-[11px]' }"
        >
          <template #date-cell="{ row }">
            <span class="font-mono tabular-nums text-default">{{ row.original.date }}</span>
          </template>
          <template #requests-cell="{ row }">
            <span class="font-mono tabular-nums text-default">{{ fmtNum(row.original.requests) }}</span>
          </template>
          <template #tokens-cell="{ row }">
            <span class="font-mono tabular-nums text-default">{{ fmtNum(row.original.total_tokens) }}</span>
          </template>
          <template #actual_cost-cell="{ row }">
            <span class="font-mono font-medium tabular-nums text-highlighted">{{ usd(row.original.actual_cost) }}</span>
          </template>
        </UTable>
      </template>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="outline" :label="t('common.close')" @click="emit('close')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { usageAPI } from '@/api'
import type { ApiKeyDailyUsagePoint, ApiKeyUsageResponse } from '@/api/usage'
import type { ApiKey } from '@/types'

const props = defineProps<{
  show: boolean
  apiKey: ApiKey | null
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

type PresetKey = 'today' | '7d' | '30d' | 'custom'
const presets = computed<{ key: PresetKey; label: string }[]>(() => [
  { key: 'today', label: t('keys.usageDialog.presetToday') },
  { key: '7d', label: t('keys.usageDialog.preset7d') },
  { key: '30d', label: t('keys.usageDialog.preset30d') },
  { key: 'custom', label: t('keys.usageDialog.presetCustom') },
])

const preset = ref<PresetKey>('today')
const customStart = ref('')
const customEnd = ref('')
const data = ref<ApiKeyUsageResponse | null>(null)
const loading = ref(false)
const error = ref(false)
let controller: AbortController | null = null

const timezone = computed(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai')
const todayISO = computed(() => {
  const now = new Date()
  const offset = now.getTimezoneOffset() * 60_000
  return new Date(now.getTime() - offset).toISOString().slice(0, 10)
})

const columns = computed<TableColumn<ApiKeyDailyUsagePoint>[]>(() => [
  { id: 'date', header: t('keys.usageDialog.date') },
  { id: 'requests', header: t('keys.usageDialog.requests') },
  { id: 'tokens', header: t('keys.usageDialog.tokens') },
  { id: 'actual_cost', header: t('keys.usageDialog.actualCost') },
])

const dailyRows = computed(() => [...(data.value?.items ?? [])].sort((a, b) => b.date.localeCompare(a.date)))

function fmtNum(value?: number) {
  return Number(value ?? 0).toLocaleString()
}
function usd(value?: number) {
  return `$${Number(value ?? 0).toFixed(4)}`
}

function selectPreset(next: PresetKey) {
  preset.value = next
  if (next !== 'custom') {
    void loadUsage()
  }
}

async function loadUsage() {
  if (!props.apiKey) return
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = false
  try {
    const params: { days?: number; start_date?: string; end_date?: string; timezone: string } = { timezone: timezone.value }
    if (preset.value === 'today') params.days = 1
    else if (preset.value === '7d') params.days = 7
    else if (preset.value === '30d') params.days = 30
    else {
      if (!customStart.value || !customEnd.value || customEnd.value < customStart.value) {
        error.value = true
        return
      }
      params.start_date = customStart.value
      params.end_date = customEnd.value
    }
    data.value = await usageAPI.getMyApiKeyUsage(props.apiKey.id, params, { signal: request.signal })
  } catch (cause) {
    if ((cause as { name?: string })?.name === 'CanceledError' || request.signal.aborted) return
    error.value = true
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

watch(() => props.show, (show) => {
  if (!show) {
    controller?.abort()
    return
  }
  preset.value = 'today'
  customStart.value = todayISO.value
  customEnd.value = todayISO.value
  data.value = null
  void loadUsage()
}, { immediate: true })
</script>
