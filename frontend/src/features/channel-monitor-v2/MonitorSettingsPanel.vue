<template>
  <section class="min-w-0 space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="max-w-3xl text-sm text-muted">{{ t('channelMonitorV2.settings.description') }}</p>
      <UButton icon="i-lucide-check" :disabled="saving || !dirty" :loading="saving" :label="t('channelMonitorV2.settings.save')" @click="save" />
    </div>
    <UAlert v-if="!systemModeV2" color="warning" variant="subtle" role="status" :title="t('channelMonitorV2.settings.modeBanner', { mode: systemModeLabel, modeV2: t('channelMonitorV2.settings.modeV2') })">
      <template #description><RouterLink to="/admin/settings" class="underline">{{ t('admin.settings.tabs.features') }}</RouterLink></template>
    </UAlert>
    <USkeleton v-if="loading" class="h-48 w-full" :aria-label="t('channelMonitorV2.settings.loading')" />
    <template v-else-if="draft">
      <UPageCard variant="outline" :ui="{ container: 'gap-y-0' }">
        <SettingRow :label="t('channelMonitorV2.settings.enableTitle')" :hint="t('channelMonitorV2.settings.enableHint')" :on="draft.enabled">
          <template #control><USwitch v-model="draft.enabled" :aria-label="t('channelMonitorV2.settings.enableTitle')" /></template>
        </SettingRow>
        <SettingRow :label="t('channelMonitorV2.settings.refreshTitle')" :hint="t('channelMonitorV2.settings.refreshHint')">
          <template #control>
            <div class="flex gap-1" role="group" :aria-label="t('channelMonitorV2.settings.refreshAria')">
              <UButton color="neutral" :variant="draft.refresh_interval_seconds === 60 ? 'soft' : 'ghost'" :aria-pressed="draft.refresh_interval_seconds === 60" @click="draft.refresh_interval_seconds = 60">1 {{ t('common.minutes') }}</UButton>
              <UButton color="neutral" :variant="draft.refresh_interval_seconds === 300 ? 'soft' : 'ghost'" :aria-pressed="draft.refresh_interval_seconds === 300" @click="draft.refresh_interval_seconds = 300">5 {{ t('common.minutes') }}</UButton>
            </div>
          </template>
        </SettingRow>
      </UPageCard>
      <UPageCard :title="t('channelMonitorV2.settings.platformsTitle')" :description="t('channelMonitorV2.settings.platformsHint')" variant="outline">
        <div v-for="platform in draft.platforms" :key="platform.platform" class="grid items-center gap-3 border-b border-default pb-3 last:border-0 last:pb-0 sm:grid-cols-[auto_7rem_minmax(0,1fr)_auto]">
          <USwitch v-model="platform.enabled" :aria-label="platformLabel(platform.platform)" />
          <strong class="text-sm font-medium text-highlighted">{{ platformLabel(platform.platform) }}</strong>
          <UInput :model-value="platform.models.join(', ')" class="w-full" type="text" :placeholder="t('channelMonitorV2.settings.modelsPlaceholder')" :aria-label="`${platformLabel(platform.platform)} ${t('channelMonitorV2.settings.modelsPlaceholder')}`" @change="setModels(platform, $event)" />
          <UBadge color="neutral" variant="subtle" class="w-fit">{{ platform.models.length ? t('channelMonitorV2.settings.badgeOther') : t('channelMonitorV2.settings.badgeAllModels') }}</UBadge>
        </div>
      </UPageCard>
      <UPageCard :title="t('channelMonitorV2.settings.groupsTitle')" variant="outline">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-sm text-muted">{{ draft.group_ids.length ? t('channelMonitorV2.settings.groupsSelected', { count: draft.group_ids.length }) : t('channelMonitorV2.settings.groupsAll') }}</p>
          <UButton v-if="draft.group_ids.length" color="neutral" variant="ghost" size="sm" :label="t('channelMonitorV2.settings.groupsAll')" @click="draft.group_ids = []" />
        </div>
        <div class="max-h-[min(40vh,280px)] overflow-y-auto">
          <div class="grid gap-3 sm:grid-cols-2">
            <div v-for="group in groups" :key="group.id" class="flex min-w-0 items-center justify-between gap-3 rounded-lg border border-default px-3 py-2">
              <UCheckbox :model-value="draft.group_ids.includes(group.id)" :label="group.name" @update:model-value="toggleGroup(group.id)" />
              <span class="shrink-0 text-xs text-muted">{{ platformLabel(group.platform) }} · #{{ group.id }}</span>
            </div>
          </div>
          <p v-if="!groups.length" class="py-6 text-center text-sm text-muted">{{ t('channelMonitorV2.settings.groupsEmpty') }}</p>
        </div>
      </UPageCard>
      <UPageCard :title="t('channelMonitorV2.settings.qualityTitle')" :description="t('channelMonitorV2.settings.qualityHint')" variant="outline">
        <div class="max-h-[min(40vh,280px)] overflow-y-auto">
          <div class="grid gap-3 sm:grid-cols-2">
            <div v-for="group in groups" :key="group.id" class="flex min-w-0 items-center gap-3 rounded-lg border border-default px-3 py-2">
              <USwitch :model-value="qualityEnabled(group.id)" :disabled="qualityUpdating.has(group.id)" :aria-label="group.name" @update:model-value="toggleQualityCheck(group.id)" />
              <span class="min-w-0 flex-1 break-words text-sm text-highlighted">{{ group.name }}</span>
              <UBadge v-if="qualityBadge(group.id)" :color="qualityStatuses[group.id]?.status === 'suspect' ? 'warning' : 'success'" variant="subtle">{{ qualityBadge(group.id) }}</UBadge>
              <span class="shrink-0 text-xs text-muted">#{{ group.id }}</span>
            </div>
          </div>
          <p v-if="!groups.length" class="py-6 text-center text-sm text-muted">{{ t('channelMonitorV2.settings.groupsEmpty') }}</p>
        </div>
      </UPageCard>
      <UPageCard :title="t('channelMonitorV2.settings.errorsTitle')" :description="t('channelMonitorV2.settings.errorsHint')" variant="outline">
        <div class="grid max-h-[min(40vh,320px)] gap-3 overflow-y-auto sm:grid-cols-2">
          <div v-for="category in errorCategories" :key="category" class="flex items-center justify-between gap-3 rounded-lg border border-default px-3 py-2">
            <UCheckbox :model-value="isCategoryIgnored(category)" :label="categoryLabel(category)" @update:model-value="toggleIgnoredCategory(category)" />
            <span class="shrink-0 font-mono text-[10px] text-muted">{{ category }}</span>
          </div>
        </div>
        <p class="text-xs text-muted">{{ t('channelMonitorV2.settings.ignoredSummary', { ignored: draft.ignored_error_categories?.length || 0, counted: countedErrorCategoryCount }) }}</p>
      </UPageCard>
      <UPageCard :title="t('channelMonitorV2.settings.healthTitle')" :description="t('channelMonitorV2.settings.healthHint')" variant="outline">
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <UFormField :label="t('channelMonitorV2.settings.fields.minimumSample')"><UInput v-model.number="draft.health_thresholds.minimum_sample" class="w-full" type="number" min="1" max="10000" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.warningError')"><UInput v-model.number="warningErrorPercent" class="w-full" type="number" min="0" max="100" step="0.1" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.criticalError')"><UInput v-model.number="criticalErrorPercent" class="w-full" type="number" min="0" max="100" step="0.1" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.targetTtft')"><UInput v-model.number="draft.health_thresholds.target_ttft_ms" class="w-full" type="number" min="1" step="100" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.warningTtft')"><UInput v-model.number="draft.health_thresholds.warning_ttft_ms" class="w-full" type="number" min="1" step="100" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.criticalTtft')"><UInput v-model.number="draft.health_thresholds.critical_ttft_ms" class="w-full" type="number" min="1" step="100" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.warningCache')"><UInput v-model.number="warningCachePercent" class="w-full" type="number" min="0" max="100" step="0.1" /></UFormField>
          <UFormField :label="t('channelMonitorV2.settings.fields.criticalCache')"><UInput v-model.number="criticalCachePercent" class="w-full" type="number" min="0" max="100" step="0.1" /></UFormField>
        </div>
      </UPageCard>
      <UAlert color="neutral" variant="subtle" :title="namedModelCount === 0 ? t('channelMonitorV2.settings.namedModelsEmpty') : t('channelMonitorV2.settings.namedModelsCount', { count: namedModelCount })" />
      <UCollapsible>
        <UButton color="neutral" variant="ghost" trailing-icon="i-lucide-chevron-down" :label="t('channelMonitorV2.settings.userContractTitle')" />
        <template #content><ul class="mt-3 list-disc space-y-1 pl-5 text-xs leading-6 text-muted"><li>{{ t('channelMonitorV2.settings.userContract.health') }}</li><li>{{ t('channelMonitorV2.settings.userContract.trend') }}</li><li>{{ t('channelMonitorV2.settings.userContract.latency') }}</li><li>{{ t('channelMonitorV2.settings.userContract.models') }}</li></ul></template>
      </UCollapsible>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingRow from '@/components/common/SettingRow.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { getChannelMonitorMode, isChannelMonitorV2Mode } from '@/utils/featureFlags'
import {
  getConfig,
  updateConfig,
  MONITOR_ERROR_CATEGORIES,
  type MonitorConfig,
} from '@/api/channelMonitorV2'
import { adminAPI } from '@/api/admin'
import { qualityCheckAPI, type GroupQualityCheckStatus } from '@/api/admin/qualityCheck'
import type { AdminGroup } from '@/types'

const { t, te } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const draft = ref<MonitorConfig | null>(null)
const original = ref('')
const groups = ref<AdminGroup[]>([])
const qualityStatuses = ref<Record<string, GroupQualityCheckStatus>>({})
const qualityUpdating = ref(new Set<number>())

const dirty = computed(() => (draft.value ? JSON.stringify(draft.value) !== original.value : false))
const namedModelCount = computed(
  () => draft.value?.platforms.filter((p) => p.enabled).reduce((sum, p) => sum + p.models.length, 0) || 0
)
const errorCategories = MONITOR_ERROR_CATEGORIES
const countedErrorCategoryCount = computed(
  () => errorCategories.length - (draft.value?.ignored_error_categories?.length || 0)
)
/** System settings mode must be v2 for aggregation to run; config remains editable for prep. */
const systemModeV2 = computed(() => isChannelMonitorV2Mode())
const systemModeLabel = computed(() => {
  if (!appStore.cachedPublicSettings?.channel_monitor_enabled) {
    return t('channelMonitorV2.settings.modeClosed')
  }
  return getChannelMonitorMode() === 'v1'
    ? t('channelMonitorV2.settings.modeV1')
    : t('channelMonitorV2.settings.modeV2')
})
const defaultThresholds = {
  minimum_sample: 50,
  warning_error_rate: 0.05,
  critical_error_rate: 0.20,
  target_ttft_ms: 3000,
  warning_ttft_ms: 3000,
  critical_ttft_ms: 10000,
  // Higher is better: below 85% watch, below 60% critical.
  warning_cache_rate: 0.85,
  critical_cache_rate: 0.60,
  error_weight: 0.60,
  ttft_weight: 0.20,
  cache_weight: 0.20,
}

/** Factory ignored categories (matches backend DefaultChannelMonitorV2IgnoredErrorCategories). */
const defaultIgnoredErrorCategories = [
  'authentication',
  'client_cancelled',
  'content_policy',
  'context_limit',
  'group_access',
  'model_unsupported',
  'not_found',
  'quota_or_balance',
] as const
function percentModel(key: 'warning_error_rate' | 'critical_error_rate' | 'warning_cache_rate' | 'critical_cache_rate') {
  return computed({
    get: () => ((draft.value?.health_thresholds?.[key] ?? defaultThresholds[key]) * 100),
    set: (value: number) => {
      if (!draft.value) return
      draft.value.health_thresholds[key] = Math.max(0, Math.min(100, Number(value) || 0)) / 100
    },
  })
}
const warningErrorPercent = percentModel('warning_error_rate')
const criticalErrorPercent = percentModel('critical_error_rate')
const warningCachePercent = percentModel('warning_cache_rate')
const criticalCachePercent = percentModel('critical_cache_rate')

function setModels(platform: MonitorConfig['platforms'][number], event: Event) {
  platform.models = [
    ...new Set(
      (event.target as HTMLInputElement).value
        .split(',')
        .map((v) => v.trim())
        .filter(Boolean)
    ),
  ].sort()
}

function qualityEnabled(groupId: number): boolean {
  return Boolean(qualityStatuses.value[groupId]?.enabled)
}

function qualityBadge(groupId: number): string | null {
  const status = qualityStatuses.value[groupId]
  if (!status?.enabled || status.status === 'unknown') return null
  const key = `channelMonitorV2.settings.qualityStatus.${status.status}`
  return te(key) ? t(key) : status.status
}

async function toggleQualityCheck(groupId: number) {
  if (qualityUpdating.value.has(groupId)) return
  const previous = qualityStatuses.value[groupId]
  const enabled = !previous?.enabled
  // Optimistic update: flip immediately, revert on failure.
  qualityStatuses.value = {
    ...qualityStatuses.value,
    [groupId]: {
      group_id: groupId,
      enabled,
      status: previous?.status ?? 'unknown',
      checked_accounts: previous?.checked_accounts ?? 0,
      degraded_accounts: previous?.degraded_accounts ?? 0,
      last_run_at: previous?.last_run_at ?? null
    }
  }
  qualityUpdating.value = new Set(qualityUpdating.value).add(groupId)
  try {
    await qualityCheckAPI.setEnabled(groupId, enabled)
    const refreshed = await qualityCheckAPI.list()
    qualityStatuses.value = refreshed
  } catch (error) {
    qualityStatuses.value = {
      ...qualityStatuses.value,
      [groupId]: previous ?? {
        group_id: groupId,
        enabled: false,
        status: 'unknown',
        checked_accounts: 0,
        degraded_accounts: 0,
        last_run_at: null
      }
    }
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV2.settings.qualityToggleFailed')))
  } finally {
    const next = new Set(qualityUpdating.value)
    next.delete(groupId)
    qualityUpdating.value = next
  }
}

function toggleGroup(id: number) {
  if (!draft.value) return
  draft.value.group_ids = draft.value.group_ids.includes(id)
    ? draft.value.group_ids.filter((value) => value !== id)
    : [...draft.value.group_ids, id].sort((a, b) => a - b)
}

function isCategoryIgnored(category: string): boolean {
  return Boolean(draft.value?.ignored_error_categories?.includes(category))
}

function toggleIgnoredCategory(category: string) {
  if (!draft.value) return
  const current = new Set(draft.value.ignored_error_categories || [])
  if (current.has(category)) current.delete(category)
  else current.add(category)
  draft.value.ignored_error_categories = [...current].sort()
}

function categoryLabel(category: string) {
  const key = `channelMonitorV2.errorCategories.${category}`
  return te(key) ? t(key) : category
}

function platformLabel(value: string) {
  return (
    {
      anthropic: 'Claude',
      openai: 'OpenAI',
      grok: 'Grok',
      kiro: 'Kiro',
      gemini: 'Gemini',
      antigravity: 'Antigravity',
      kimi: 'Kimi',
      zhipu: 'Zhipu',
      deepseek: 'DeepSeek',
      minimax: 'MiniMax',
      composite: 'Composite',
    } as Record<string, string>
  )[value] || value
}

function normalizeConfig(value: MonitorConfig): MonitorConfig {
  const ignored = value.ignored_error_categories
  return {
    ...value,
    health_thresholds: { ...defaultThresholds, ...(value.health_thresholds || {}) },
    // Preserve explicit empty arrays from the server (operator cleared all).
    ignored_error_categories: [
      ...(ignored == null ? [...defaultIgnoredErrorCategories] : ignored),
    ].sort(),
  }
}

async function load() {
  loading.value = true
  try {
    const [value, groupRows, qualityRows] = await Promise.all([
      getConfig(),
      adminAPI.groups.getAllIncludingInactive(),
      qualityCheckAPI.list().catch(() => ({}))
    ])
    const normalized = normalizeConfig(value)
    draft.value = structuredClone(normalized)
    groups.value = groupRows
    qualityStatuses.value = qualityRows
    original.value = JSON.stringify(normalized)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV2.settings.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value) return
  saving.value = true
  try {
    const payload = normalizeConfig(draft.value)
    const value = await updateConfig(payload)
    const normalized = normalizeConfig(value)
    draft.value = structuredClone(normalized)
    original.value = JSON.stringify(normalized)
    appStore.showSuccess(t('channelMonitorV2.settings.saveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV2.settings.saveFailed')))
    await load()
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
