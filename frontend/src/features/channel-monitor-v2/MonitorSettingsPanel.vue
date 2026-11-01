<template>
  <section class="mx-auto flex w-full max-w-5xl flex-col gap-6">
    <UPageCard
      :title="t('channelMonitorV2.settings.title')"
      :description="t('channelMonitorV2.settings.description')"
      variant="naked"
      orientation="horizontal"
      class="monitor-settings-header"
    >
      <UButton
        class="w-fit lg:ms-auto"
        icon="i-lucide-check"
        :loading="saving"
        :disabled="saving || !dirty"
        :label="t('channelMonitorV2.settings.save')"
        @click="save"
      />
    </UPageCard>

    <UAlert
      v-if="!systemModeV2"
      role="status"
      color="warning"
      variant="subtle"
      icon="i-lucide-triangle-alert"
    >
      <template #description>
        {{
          t('channelMonitorV2.settings.modeBanner', {
            mode: systemModeLabel,
            modeV2: t('channelMonitorV2.settings.modeV2'),
          })
        }}
        <router-link class="ml-1 font-medium underline underline-offset-4" to="/admin/settings">{{ t('admin.settings.tabs.features') }}</router-link>
      </template>
    </UAlert>

    <UPageCard v-if="loading" variant="subtle" :ui="{ container: 'min-h-[200px] items-center justify-center' }">
      <span class="animate-pulse text-sm text-muted">{{ t('channelMonitorV2.settings.loading') }}</span>
    </UPageCard>

    <template v-else-if="draft">
      <UPageCard variant="subtle" :ui="{ container: 'gap-y-4' }">
        <UFormField :label="t('channelMonitorV2.settings.enableTitle')" :description="t('channelMonitorV2.settings.enableHint')" orientation="horizontal" :class="rowClass">
          <Toggle v-model="draft.enabled" />
        </UFormField>
        <USeparator />
        <UFormField :label="t('channelMonitorV2.settings.refreshTitle')" :description="t('channelMonitorV2.settings.refreshHint')" orientation="horizontal" :class="rowClass">
          <UTabs
            v-model="refreshModel"
            :items="refreshItems"
            :content="false"
            size="sm"
            class="w-auto"
            :aria-label="t('channelMonitorV2.settings.refreshAria')"
          />
        </UFormField>
      </UPageCard>

      <UPageCard :title="t('channelMonitorV2.settings.platformsTitle')" :description="t('channelMonitorV2.settings.platformsHint')" variant="subtle" :ui="{ container: 'gap-y-3' }">
        <div class="flex flex-col divide-y divide-default">
          <div
            v-for="platform in draft.platforms"
            :key="platform.platform"
            class="grid grid-cols-1 items-center gap-3 py-3 first:pt-0 last:pb-0 sm:grid-cols-[auto_7rem_minmax(0,1fr)_auto]"
          >
            <Toggle v-model="platform.enabled" />
            <strong class="text-sm font-medium text-highlighted">{{ platformLabel(platform.platform) }}</strong>
            <UInput
              class="w-full"
              :model-value="platform.models.join(', ')"
              type="text"
              :placeholder="t('channelMonitorV2.settings.modelsPlaceholder')"
              @change="setModels(platform, $event)"
            />
            <UBadge :color="platform.models.length ? 'neutral' : 'primary'" variant="subtle" class="justify-self-start sm:justify-self-end">
              {{ platform.models.length ? t('channelMonitorV2.settings.badgeOther') : t('channelMonitorV2.settings.badgeAllModels') }}
            </UBadge>
          </div>
        </div>
      </UPageCard>
      <UPageCard variant="subtle" :ui="{ container: 'gap-y-3' }">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 class="text-base font-semibold text-highlighted">{{ t('channelMonitorV2.settings.groupsTitle') }}</h3>
            <p class="mt-0.5 text-sm text-muted">
              {{
                draft.group_ids.length
                  ? t('channelMonitorV2.settings.groupsSelected', { count: draft.group_ids.length })
                  : t('channelMonitorV2.settings.groupsAll')
              }}
            </p>
          </div>
          <UButton
            v-if="draft.group_ids.length"
            type="button"
            color="neutral"
            variant="ghost"
            size="sm"
            :label="t('channelMonitorV2.settings.groupsAll')"
            @click="draft.group_ids = []"
          />
        </div>
        <div class="max-h-[min(40vh,280px)] overflow-y-auto">
          <div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
            <div
              v-for="group in groups"
              :key="group.id"
              class="flex items-center gap-3 rounded-md px-2 py-2 text-sm transition hover:bg-elevated/60"
            >
              <UCheckbox
                :model-value="draft.group_ids.includes(group.id)"
                :label="group.name"
                class="min-w-0 flex-1"
                :ui="{ label: 'truncate font-medium text-default', wrapper: 'min-w-0' }"
                @update:model-value="toggleGroup(group.id)"
              />
              <small class="shrink-0 text-xs text-dimmed">{{ platformLabel(group.platform) }} · #{{ group.id }}</small>
            </div>
          </div>
          <p v-if="groups.length === 0" class="py-8 text-center text-sm text-muted">{{ t('channelMonitorV2.settings.groupsEmpty') }}</p>
        </div>
      </UPageCard>

      <UPageCard :title="t('channelMonitorV2.settings.qualityTitle')" :description="t('channelMonitorV2.settings.qualityHint')" variant="subtle" :ui="{ container: 'gap-y-3' }">
        <div class="max-h-[min(40vh,280px)] overflow-y-auto">
          <div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
            <div
              v-for="group in groups"
              :key="group.id"
              class="flex items-center gap-3 rounded-md px-2 py-2 text-sm transition hover:bg-elevated/60"
            >
              <Toggle
                :model-value="qualityEnabled(group.id)"
                :disabled="qualityUpdating.has(group.id)"
                @update:model-value="toggleQualityCheck(group.id)"
              />
              <span class="min-w-0 flex-1 truncate font-medium text-default">{{ group.name }}</span>
              <UBadge v-if="qualityBadge(group.id)" :color="qualityStatuses[group.id]?.status === 'suspect' ? 'warning' : 'success'" variant="subtle" size="sm" class="shrink-0">
                {{ qualityBadge(group.id) }}
              </UBadge>
              <small class="shrink-0 text-xs text-dimmed">#{{ group.id }}</small>
            </div>
          </div>
          <p v-if="groups.length === 0" class="py-8 text-center text-sm text-muted">{{ t('channelMonitorV2.settings.groupsEmpty') }}</p>
        </div>
      </UPageCard>

      <UPageCard :title="t('channelMonitorV2.settings.errorsTitle')" :description="t('channelMonitorV2.settings.errorsHint')" variant="subtle" :ui="{ container: 'gap-y-3' }">
        <div class="max-h-[min(40vh,320px)] overflow-y-auto">
          <div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
            <div
              v-for="category in errorCategories"
              :key="category"
              class="flex items-center gap-3 rounded-md px-2 py-2 text-sm transition hover:bg-elevated/60"
            >
              <UCheckbox
                :model-value="isCategoryIgnored(category)"
                :label="categoryLabel(category)"
                class="min-w-0 flex-1"
                :ui="{ label: 'truncate font-medium text-default', wrapper: 'min-w-0' }"
                @update:model-value="toggleIgnoredCategory(category)"
              />
              <small class="shrink-0 font-mono text-[10px] text-dimmed">{{ category }}</small>
            </div>
          </div>
        </div>
        <p class="text-xs text-muted">
          {{
            t('channelMonitorV2.settings.ignoredSummary', {
              ignored: draft.ignored_error_categories?.length || 0,
              counted: countedErrorCategoryCount,
            })
          }}
        </p>
      </UPageCard>

      <UPageCard :title="t('channelMonitorV2.settings.healthTitle')" :description="t('channelMonitorV2.settings.healthHint')" variant="subtle" :ui="{ container: 'gap-y-4' }">
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
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

      <UAlert
        color="neutral"
        variant="subtle"
        icon="i-lucide-info"
        :title="namedModelCount === 0 ? t('channelMonitorV2.settings.namedModelsEmpty') : t('channelMonitorV2.settings.namedModelsCount', { count: namedModelCount })"
      >
        <template #description>
          <p class="font-medium text-default">{{ t('channelMonitorV2.settings.userContractTitle') }}</p>
          <ul class="mt-1 list-disc space-y-0.5 pl-4">
            <li>{{ t('channelMonitorV2.settings.userContract.health') }}</li>
            <li>{{ t('channelMonitorV2.settings.userContract.trend') }}</li>
            <li>{{ t('channelMonitorV2.settings.userContract.latency') }}</li>
            <li>{{ t('channelMonitorV2.settings.userContract.models') }}</li>
          </ul>
        </template>
      </UAlert>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
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
// Template settings rows: label + description left, control right.
const rowClass = 'flex max-sm:flex-col justify-between items-start sm:items-center gap-4'
const refreshItems = [{ value: '60', label: '1 min' }, { value: '300', label: '5 min' }]
const refreshModel = computed({
  get: () => String(draft.value?.refresh_interval_seconds ?? 60),
  set: (value: string | number) => { if (draft.value) draft.value.refresh_interval_seconds = Number(value) === 300 ? 300 : 60 },
})
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
