<template>
  <div class="flex min-w-0 flex-col gap-5" data-testid="small-retention-panel">
    <PageToolbar>
      <template #right>
        <div class="flex items-center gap-2 text-sm font-medium text-default">
          <span id="small-retention-enabled-label">{{ t('admin.requestCapture.small.title') }}</span>
          <UBadge color="neutral" variant="subtle" size="sm">{{ t('admin.requestCapture.small.' + (enabled ? 'on' : 'off')) }}</UBadge>
          <USwitch :model-value="enabled" aria-labelledby="small-retention-enabled-label" :disabled="!configLoaded || busy || !ready" data-testid="small-retention-toggle" @update:model-value="emit('toggle', !!$event)" />
        </div>
        <UButton color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('admin.requestCapture.small.clear')" :disabled="loading || !stats?.records" @click="clear" />
      </template>
    </PageToolbar>
    <UAlert v-if="!ready" color="warning" variant="subtle" icon="i-lucide-database" :description="t('admin.requestCapture.small.notReady')" />
    <UCollapsible class="rounded-lg border border-default bg-default px-3 py-2">
      <UButton color="neutral" variant="link" icon="i-lucide-shield-check" trailing-icon="i-lucide-chevron-down" class="w-full justify-start p-0 text-xs" :label="t('admin.requestCapture.small.privacySummary')" />
      <template #content><p class="pt-3 text-sm leading-6 text-muted">{{ t('admin.requestCapture.small.privacy') }}</p></template>
    </UCollapsible>
    <UAlert v-if="stats?.quota_full" data-testid="small-retention-quota-full" color="warning" variant="subtle" icon="i-lucide-database" :description="t('admin.requestCapture.small.quotaFull')" />
    <UAlert v-if="error" role="alert" color="error" variant="subtle" :title="error" />

    <UPageCard v-if="configLoaded && !enabled && stats?.records === 0 && !loading" variant="outline" data-testid="small-retention-disabled-empty">
      <div class="flex flex-col items-center gap-4 py-8 text-center sm:py-12">
        <UIcon name="i-lucide-archive" class="size-9 text-dimmed" />
        <h2 class="text-lg font-semibold text-highlighted">{{ t('admin.requestCapture.small.emptyTitle') }}</h2>
        <p class="max-w-lg text-sm leading-6 text-muted">{{ t('admin.requestCapture.small.emptyHint') }}</p>
        <UButton icon="i-lucide-power" class="w-fit" :disabled="!ready || busy" :loading="busy" :label="t('admin.requestCapture.small.enable')" @click="emit('toggle', true)" />
      </div>
    </UPageCard>
    <template v-else>
      <UPageGrid v-if="stats" class="grid-cols-2 gap-3 lg:grid-cols-4">
        <UPageCard v-for="card in cards" :key="card.label" :title="card.label" variant="subtle" :ui="{ title: 'font-normal text-muted text-xs', container: 'gap-y-1.5 p-4 sm:p-4' }">
          <span class="text-2xl font-semibold text-highlighted">{{ card.value }}</span>
          <span v-if="card.hint" class="text-xs leading-5 text-muted">{{ card.hint }}</span>
        </UPageCard>
      </UPageGrid>
      <div v-if="stats" class="grid min-w-0 gap-4 lg:grid-cols-2">
        <UPageCard variant="outline" :ui="{ container: 'gap-y-3 p-4 sm:p-4' }">
          <h2 class="text-sm font-medium text-highlighted">{{ t('admin.requestCapture.small.repeatDistribution') }}</h2>
          <table class="w-full text-xs" :aria-label="t('admin.requestCapture.small.repeatDistribution')">
            <tbody><tr v-for="entry in distribution" :key="entry.bucket" :title="t('admin.requestCapture.small.distributionHint', { bucket: entry.bucket, count: entry.count })" tabindex="0">
              <th scope="row" class="w-20 py-1.5 text-left font-normal text-muted">{{ t('admin.requestCapture.small.bucket', { count: entry.bucket }) }}</th>
              <td class="px-3"><div class="h-2 rounded-sm bg-elevated"><div class="retention-bar h-2 rounded-e-sm" :style="{ width: entry.width + '%' }" /></div></td>
              <td class="w-12 text-right tabular-nums text-default">{{ entry.count }}</td>
            </tr></tbody>
          </table>
        </UPageCard>
        <UPageCard variant="outline" :ui="{ container: 'gap-y-3 p-4 sm:p-4' }">
          <h2 class="text-sm font-medium text-highlighted">{{ t('admin.requestCapture.small.skipped') }}</h2>
          <div class="flex flex-wrap items-start gap-2">
            <UBadge v-for="[reason, count] in skips" :key="reason" color="neutral" variant="subtle" :title="skipHint(reason)" tabindex="0">{{ skipReason(reason) }} <span class="ml-1 font-semibold tabular-nums">{{ count }}</span></UBadge>
            <span v-if="!skips.length" class="text-sm text-muted">{{ t('admin.requestCapture.small.noSkips') }}</span>
          </div>
          <p class="text-xs leading-5 text-muted">{{ t('admin.requestCapture.small.skipHint') }}</p>
        </UPageCard>
      </div>

      <section class="min-w-0 space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-sm font-semibold text-highlighted">{{ t('admin.requestCapture.small.evidence') }}</h2>
          <p v-if="stats" class="text-xs text-muted">{{ t('admin.requestCapture.small.range') }} {{ retentionTime(stats.oldest) }} → {{ retentionTime(stats.newest) }}</p>
        </div>
        <form data-testid="small-filters" class="grid min-w-0 grid-cols-2 items-center gap-2 rounded-lg bg-elevated/50 p-3 sm:flex sm:flex-wrap xl:flex-nowrap" @submit.prevent="page = 1; load()">
          <UInput v-model="filters.user_id" type="number" min="1" size="sm" class="w-full sm:w-24" :placeholder="t('admin.requestCapture.small.userID')" :aria-label="t('admin.requestCapture.small.userID')" />
          <UInput v-model="filters.api_key_id" type="number" min="1" size="sm" class="w-full sm:w-24" :placeholder="t('admin.requestCapture.small.key')" :aria-label="t('admin.requestCapture.small.key')" />
          <UInput v-model="filters.model" size="sm" class="w-full sm:w-28" :placeholder="t('admin.requestCapture.model')" :aria-label="t('admin.requestCapture.model')" />
          <UInput v-model="filters.request_id" size="sm" class="w-full sm:w-36" :placeholder="t('admin.requestCapture.small.requestID')" :aria-label="t('admin.requestCapture.small.requestID')" />
          <USelect v-model="statusSelection" :items="statusItems" size="sm" class="w-full sm:w-36" :aria-label="t('admin.requestCapture.status')" />
          <UInput v-if="otherStatus" v-model="filters.status" type="number" min="100" max="599" size="sm" class="w-24" :placeholder="t('admin.requestCapture.small.otherStatus')" :aria-label="t('admin.requestCapture.small.otherStatus')" />
          <UInput v-model="filters.min_repeats" type="number" min="1" size="sm" class="w-full sm:w-28" :placeholder="t('admin.requestCapture.small.minRepeats')" :aria-label="t('admin.requestCapture.small.minRepeats')" />
          <UPopover>
            <UButton type="button" color="neutral" variant="outline" size="sm" icon="i-lucide-calendar-days" :label="t('admin.requestCapture.small.timeRange')" :trailing-icon="filters.from || filters.to ? 'i-lucide-check' : 'i-lucide-chevron-down'" />
            <template #content><div class="grid max-w-[calc(100vw-2rem)] gap-3 p-4">
              <UFormField :label="t('admin.requestCapture.small.from')"><UInput v-model="filters.from" type="datetime-local" class="w-full" /></UFormField>
              <UFormField :label="t('admin.requestCapture.small.to')"><UInput v-model="filters.to" type="datetime-local" class="w-full" /></UFormField>
            </div></template>
          </UPopover>
          <div class="ml-auto flex w-full items-center justify-end gap-1 sm:w-auto">
            <UButton type="submit" size="sm" color="neutral" variant="solid" :label="t('admin.requestCapture.filter')" :loading="loading" />
            <UButton type="button" color="neutral" variant="ghost" size="sm" icon="i-lucide-refresh-cw" :aria-label="t('admin.requestCapture.refresh')" :title="t('admin.requestCapture.refresh')" :disabled="loading" @click="load" />
          </div>
        </form>
        <p v-if="filters.from || filters.to" class="text-xs text-muted">{{ t('admin.requestCapture.small.timeRange') }}: {{ retentionTime(filters.from) }} → {{ retentionTime(filters.to) }}</p>
        <UTable :data="records" :columns="columns" :loading="loading" :ui="{ root: 'max-w-full overflow-x-auto rounded-lg border border-default', base: 'min-w-[1080px]', tr: 'cursor-pointer', th: 'bg-elevated/50 text-xs', td: 'align-top py-4' }" @select="(_event, row) => open(row.original.id)">
          <template #request-cell="{ row: { original: record } }">
            <UButton variant="link" color="neutral" class="p-0 text-left text-xs tabular-nums" :data-testid="'small-detail-' + record.id" @click.stop="open(record.id)">{{ retentionTime(record.last_seen) }}</UButton>
            <p class="mt-1 max-w-44 truncate font-mono text-xs text-muted" :title="record.data.path">{{ record.data.path }}</p>
          </template>
          <template #identity-cell="{ row: { original: record } }"><p class="text-xs text-default">#{{ record.user_id }} / #{{ record.api_key_id }}</p><p class="mt-1 max-w-40 truncate text-xs text-muted" :title="record.model">{{ record.model }}</p></template>
          <template #status-cell="{ row: { original: record } }"><div class="flex flex-col items-start gap-1.5"><UBadge :color="record.status >= 500 ? 'error' : record.status >= 400 ? 'warning' : 'success'" variant="subtle" size="sm">HTTP {{ record.status }}</UBadge><UBadge :color="record.data.replay_reasons?.length ? 'warning' : 'neutral'" variant="outline" size="sm" :title="record.data.replay_reasons?.map(replayReason).join('；') || t('admin.requestCapture.small.replayHint')">{{ t('admin.requestCapture.small.' + (record.data.replay_reasons?.length ? 'notReplayable' : 'replayable')) }}</UBadge></div></template>
          <template #tokens-cell="{ row: { original: record } }"><span class="whitespace-nowrap text-xs tabular-nums" :title="tokenBreakdown(record)">{{ record.input_tokens + record.cache_read_tokens + record.cache_creation_tokens }} / {{ record.output_tokens }}</span></template>
          <template #cost-cell="{ row: { original: record } }"><span class="whitespace-nowrap font-mono text-xs text-highlighted" :title="record.actual_cost + ' USD'">{{ retentionCost(record.actual_cost) }}</span></template>
          <template #repeats-cell="{ row: { original: record } }"><span class="font-semibold tabular-nums text-highlighted">{{ record.repeat_count.toLocaleString() }}</span><p v-if="record.omitted_responses" class="mt-1 max-w-32 whitespace-normal text-xs text-muted">{{ t('admin.requestCapture.small.omitted', { count: record.omitted_responses }) }}</p></template>
          <template #expiry-cell="{ row: { original: record } }"><span class="whitespace-nowrap text-xs text-muted" :title="retentionTime(record.expires_at)">{{ expiryLabel(record.expires_at) }}</span></template>
          <template #empty><div class="py-10 text-center text-sm text-muted">{{ t('admin.requestCapture.small.empty') }}</div></template>
        </UTable>
        <div class="flex items-center justify-between gap-2 pt-1">
          <span class="text-xs text-muted">{{ t('admin.requestCapture.small.rowHint') }}</span>
          <div class="flex items-center gap-2">
            <UButton icon="i-lucide-chevron-left" color="neutral" variant="outline" size="sm" :aria-label="t('admin.requestCapture.previous')" :disabled="page <= 1 || loading" @click="page--; load()" />
            <span class="min-w-6 text-center text-sm tabular-nums text-muted">{{ page }}</span>
            <UButton icon="i-lucide-chevron-right" color="neutral" variant="outline" size="sm" :aria-label="t('admin.requestCapture.next')" :disabled="!more || loading" @click="page++; load()" />
          </div>
        </div>
      </section>
    </template>

    <BaseDialog :show="!!detail" :title="t('admin.requestCapture.detail')" width="extra-wide" @close="detail = undefined">
      <div v-if="detail" class="flex min-w-0 flex-col gap-4" data-testid="small-retention-detail">
        <div class="rounded-lg border border-default bg-elevated/40 p-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-0"><p class="break-all font-mono text-sm font-semibold text-highlighted">{{ detail.record.data.method }} {{ detail.record.data.path }}</p><p class="mt-1 break-all text-xs text-muted">{{ detail.record.model }} · {{ t('admin.requestCapture.small.userID') }} #{{ detail.record.user_id }} · {{ t('admin.requestCapture.small.key') }} #{{ detail.record.api_key_id }}</p></div>
            <UButton data-testid="small-copy-curl" color="neutral" variant="outline" size="sm" icon="i-lucide-copy" :label="t('admin.requestCapture.small.copyCurl')" @click="copyCurl" />
          </div>
          <dl class="mt-4 grid grid-cols-2 gap-x-5 gap-y-3 text-xs sm:grid-cols-3 lg:grid-cols-6">
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.repeats') }}</dt><dd class="mt-1 font-semibold tabular-nums text-highlighted">{{ detail.record.repeat_count.toLocaleString() }}</dd></div>
            <div :title="tokenBreakdown(detail.record)"><dt class="text-muted">{{ t('admin.requestCapture.small.tokens') }}</dt><dd class="mt-1 tabular-nums text-default">{{ detail.record.input_tokens + detail.record.cache_read_tokens + detail.record.cache_creation_tokens }} / {{ detail.record.output_tokens }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.cost') }}</dt><dd class="mt-1 font-mono text-highlighted">{{ retentionCost(detail.record.actual_cost) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.firstSeen') }}</dt><dd class="mt-1 tabular-nums text-default">{{ retentionTime(detail.record.first_seen) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.lastSeen') }}</dt><dd class="mt-1 tabular-nums text-default">{{ retentionTime(detail.record.last_seen) }}</dd></div>
            <div :title="retentionTime(detail.record.expires_at)"><dt class="text-muted">{{ t('admin.requestCapture.expiresAt') }}</dt><dd class="mt-1 text-default">{{ expiryLabel(detail.record.expires_at) }}</dd></div>
          </dl>
        </div>
        <div class="flex items-start gap-2 rounded-lg bg-elevated/40 p-3 text-xs leading-5">
          <UIcon :name="detail.record.data.replay_reasons?.length ? 'i-lucide-info' : 'i-lucide-check-check'" class="mt-0.5 size-4 shrink-0 text-muted" />
          <div><p class="font-medium text-highlighted">{{ t('admin.requestCapture.small.' + (detail.record.data.replay_reasons?.length ? 'notReplayable' : 'replayable')) }}</p><p class="text-muted">{{ detail.record.data.replay_reasons?.map(replayReason).join('；') || t('admin.requestCapture.small.replayHint') }}</p></div>
        </div>
        <div class="flex min-w-0 flex-wrap items-center gap-2"><span class="text-xs text-muted">{{ t('admin.requestCapture.small.requestID') }}</span><UButton v-for="id in detail.record.request_ids" :key="id" color="neutral" variant="subtle" size="xs" icon="i-lucide-copy" class="max-w-full font-mono" :ui="{ label: 'truncate' }" :label="id" :title="t('admin.requestCapture.small.copyRequestID')" @click="copyText(id)" /></div>
        <UCollapsible>
          <UButton color="neutral" variant="ghost" size="sm" trailing-icon="i-lucide-chevron-down" :label="t('admin.requestCapture.small.correlations')" />
          <template #content><dl class="grid grid-cols-2 gap-3 rounded-lg bg-elevated/40 p-3 text-xs sm:grid-cols-4">
            <div><dt class="text-muted">{{ t('admin.requestCapture.group') }}</dt><dd class="mt-1">#{{ detail.record.data.group_id }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.accountID') }}</dt><dd class="mt-1">#{{ detail.record.data.first_usage?.account_id || '—' }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.settlement') }}</dt><dd class="mt-1">{{ settlement(detail.record.data.first_usage?.settlement || '') }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.omittedLabel') }}</dt><dd class="mt-1">{{ detail.record.omitted_responses }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.platform') }}</dt><dd class="mt-1 break-all">{{ detail.record.data.first_usage?.platform || '—' }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.upstreamModel') }}</dt><dd class="mt-1 break-all">{{ detail.record.data.first_usage?.upstream_model || '—' }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.firstCharge') }}</dt><dd class="mt-1 font-mono">{{ retentionCost(detail.record.data.first_usage?.actual_cost || '') }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.calculatedCost') }}</dt><dd class="mt-1 font-mono">{{ retentionCost(detail.record.data.first_usage?.calculated_cost || '') }}</dd></div>
            <div class="col-span-2"><dt class="text-muted">{{ t('admin.requestCapture.small.firstTokens') }}</dt><dd class="mt-1">{{ tokenBreakdown(detail.record.data.first_usage) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.duration') }}</dt><dd class="mt-1">{{ t('admin.requestCapture.small.milliseconds', { count: detail.record.data.first_usage?.duration_ms || 0 }) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.delivery') }}</dt><dd class="mt-1">{{ t('admin.requestCapture.small.' + (detail.record.data.first_usage?.stream ? 'stream' : 'nonStream')) }}</dd></div>
          </dl></template>
        </UCollapsible>
        <UTabs v-model="part" :items="partItems" :content="false" variant="pill" size="sm" class="w-full" :ui="{ list: 'grid grid-cols-2 sm:flex', indicator: 'hidden sm:block', trigger: 'max-sm:data-[state=active]:bg-primary' }" />
        <USelect v-if="part.startsWith('response')" v-model="sample" :items="sampleItems" class="w-full" :aria-label="t('admin.requestCapture.small.samples')" />
        <div v-if="selectedResponse && part.startsWith('response')" class="flex flex-wrap items-center gap-2 text-xs text-muted">
          <UBadge :color="selectedResponse.data.status >= 400 ? 'warning' : 'success'" variant="subtle">HTTP {{ selectedResponse.data.status }}</UBadge>
          <span>{{ t('admin.requestCapture.small.sampleRepeats', { count: selectedResponse.repeat_count }) }}</span>
          <span>{{ settlement(selectedResponse.data.usage.settlement) }}</span>
          <span>{{ t('admin.requestCapture.small.accountID') }} #{{ selectedResponse.data.usage.account_id }}</span>
        </div>
        <UCollapsible v-if="selectedResponse && part.startsWith('response')">
          <UButton color="neutral" variant="ghost" size="sm" trailing-icon="i-lucide-chevron-down" :label="t('admin.requestCapture.small.sampleUsage')" />
          <template #content><dl class="grid grid-cols-2 gap-3 rounded-lg bg-elevated/40 p-3 text-xs sm:grid-cols-4">
            <div class="col-span-2"><dt class="text-muted">{{ t('admin.requestCapture.small.sampleTokens') }}</dt><dd class="mt-1">{{ tokenBreakdown(selectedResponse.data.usage) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.sampleCharge') }}</dt><dd class="mt-1 font-mono">{{ retentionCost(selectedResponse.data.usage.actual_cost) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.calculatedCost') }}</dt><dd class="mt-1 font-mono">{{ retentionCost(selectedResponse.data.usage.calculated_cost) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.group') }}</dt><dd class="mt-1">#{{ selectedResponse.data.usage.group_id }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.upstreamModel') }}</dt><dd class="mt-1 break-all">{{ selectedResponse.data.usage.upstream_model || '—' }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.firstSeen') }}</dt><dd class="mt-1">{{ retentionTime(selectedResponse.first_seen) }}</dd></div>
            <div><dt class="text-muted">{{ t('admin.requestCapture.small.lastSeen') }}</dt><dd class="mt-1">{{ retentionTime(selectedResponse.last_seen) }}</dd></div>
          </dl></template>
        </UCollapsible>
        <UAlert v-if="selectedResponse?.data.replay_reasons?.length && part.startsWith('response')" color="warning" variant="subtle" :title="t('admin.requestCapture.small.notReplayable')" :description="selectedResponse.data.replay_reasons.map(replayReason).join('；')" />
        <UTable v-if="part.endsWith('headers')" :data="headerRows" :columns="headerColumns" :ui="{ root: 'max-w-full overflow-x-auto rounded-lg border border-default', base: 'table-fixed w-full', th: 'first:w-1/3 bg-elevated/40 text-xs', td: 'whitespace-pre-wrap break-all align-top text-xs' }" />
        <template v-else>
          <div class="flex items-center justify-between gap-3"><USwitch v-model="pretty" :label="t('admin.requestCapture.small.pretty')" size="sm" /><UButton color="neutral" variant="outline" size="sm" icon="i-lucide-copy" :label="t('admin.requestCapture.small.copyBody')" data-testid="small-copy-body" @click="copyText(bodyText)" /></div>
          <pre class="max-h-[50vh] overflow-auto whitespace-pre-wrap break-all rounded-lg bg-elevated p-4 text-xs leading-5 text-default ring ring-inset ring-default"><code v-if="pretty && highlighted" v-html="highlighted" /><code v-else>{{ bodyText }}</code></pre>
        </template>
      </div>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn, SelectItem } from '@nuxt/ui'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { confirm } from '@/composables/useConfirmation'
import { useClipboard } from '@/composables/useClipboard'
import * as api from '@/api/admin/smallRequestRetention'
import zh from '@/i18n/locales/zh/admin/requestCapture'
import en from '@/i18n/locales/en/admin/requestCapture'
import { highlightRetentionJSON, retentionTime, retentionCost, retentionHours, retentionDistribution, retentionReasonKey, retentionSettlementKey } from './smallRequestRetentionFormat'

withDefaults(defineProps<{ enabled?: boolean; ready?: boolean; configLoaded?: boolean; busy?: boolean }>(), { enabled: false, ready: true, configLoaded: false, busy: false })
const emit = defineEmits<{ toggle: [enabled: boolean] }>()
const { t } = useI18n({ useScope: 'local', messages: { zh: { admin: zh }, en: { admin: en } }, inheritLocale: true })
const stepUp = useStepUp()
const { copyToClipboard } = useClipboard()
const stats = shallowRef<api.SmallStats>(), records = shallowRef<api.SmallRecord[]>([]), detail = shallowRef<api.SmallDetail>()
const error = ref(''), loading = ref(false), page = ref(1), more = ref(false), part = ref('request_headers'), sample = ref(0), pretty = ref(false)
const filters = reactive({ user_id: '', api_key_id: '', model: '', request_id: '', status: '', min_repeats: '', from: '', to: '' })
const otherStatus = ref(false)
const distribution = computed(() => retentionDistribution(stats.value?.repeat_distribution || {}))
const skips = computed(() => Object.entries(stats.value?.skipped || {}).filter(([, count]) => count > 0))
const statusSelection = computed({ get: () => otherStatus.value ? 'other' : filters.status || 'all', set: (value: string) => { otherStatus.value = value === 'other'; filters.status = value === 'all' || value === 'other' ? '' : value } })
const statusItems = computed<SelectItem[]>(() => [
  { value: 'all', label: t('admin.requestCapture.small.allStatuses') },
  ...[200, 400, 401, 403, 429, 500, 502, 503].map(status => ({ value: String(status), label: t('admin.requestCapture.small.' + (status >= 500 ? 'serverStatus' : status >= 400 ? 'clientStatus' : 'successStatus'), { status }) })),
  { value: 'other', label: t('admin.requestCapture.small.otherStatus') },
])
function expiryLabel(value: string) { const hours = retentionHours(value); return hours === null ? '—' : t('admin.requestCapture.small.' + (hours ? 'hoursLeft' : 'expired'), { hours }) }
function replayReason(reason: string) { const key = retentionReasonKey(reason); return key ? t(key) : reason }
function settlement(value: string) { const key = retentionSettlementKey(value); return key ? t(key) : value || '—' }
function tokenBreakdown(record?: Pick<api.SmallRecord, 'input_tokens' | 'cache_read_tokens' | 'cache_creation_tokens' | 'output_tokens'>) { return record ? t('admin.requestCapture.small.tokenBreakdown', { input: record.input_tokens || 0, read: record.cache_read_tokens || 0, write: record.cache_creation_tokens || 0, output: record.output_tokens || 0 }) : '—' }
function skipHint(reason: string) { return reason in zh.requestCapture.small.skipReasons ? t('admin.requestCapture.small.skipHints.' + reason) : reason }
let disposed = false, detailVersion = 0
function report(err: unknown) { if (!isStepUpCancelled(err) && !disposed) error.value = err instanceof Error ? err.message : t('admin.requestCapture.failed') }
const cards = computed(() => stats.value ? [
  { label: t('admin.requestCapture.small.records'), value: stats.value.records },
  { label: t('admin.requestCapture.small.duplicates'), value: stats.value.duplicates, hint: t('admin.requestCapture.small.requests', { count: stats.value.requests }) },
  { label: t('admin.requestCapture.small.stored'), value: bytes(stats.value.stored_bytes), hint: t('admin.requestCapture.small.storageHint', { limit: bytes(stats.value.stored_limit_bytes), saved: bytes(stats.value.saved_bytes) }) },
  { label: t('admin.requestCapture.small.skipped'), value: Object.values(stats.value.skipped).reduce((a, b) => a + b, 0), hint: t('admin.requestCapture.small.skipHint') },
] : [])
const columns = computed<TableColumn<api.SmallRecord>[]>(() => [
  { id: 'request', header: t('admin.requestCapture.small.timeEntry') }, { id: 'identity', header: t('admin.requestCapture.small.identity') },
  { id: 'status', header: t('admin.requestCapture.status') }, { id: 'tokens', header: t('admin.requestCapture.small.tokens') },
  { id: 'cost', header: t('admin.requestCapture.small.cost') }, { id: 'repeats', header: t('admin.requestCapture.small.repeats') }, { id: 'expiry', header: t('admin.requestCapture.expiresAt') },
])
const headerColumns = computed<TableColumn<{ name: string; value: string }>[]>(() => [{ accessorKey: 'name', header: t('admin.requestCapture.small.header') }, { accessorKey: 'value', header: t('admin.requestCapture.small.value') }])
const partItems = computed(() => ['request_headers', 'request_body', 'response_headers', 'response_body'].map(value => ({ value, label: t('admin.requestCapture.small.parts.' + value) })))
const selectedResponse = computed(() => detail.value?.responses[sample.value])
const sampleItems = computed(() => (detail.value?.responses || []).map((value, index) => ({ value: index, label: t('admin.requestCapture.small.sampleLabel', { index: index + 1, status: value.data.status, count: value.repeat_count, time: retentionTime(value.first_seen) }) })))
const headerRows = computed(() => Object.entries(part.value === 'request_headers' ? detail.value?.request_headers || {} : selectedResponse.value?.headers || {}).flatMap(([name, values]) => values.map(value => ({ name, value }))))
const bodyText = computed(() => part.value === 'request_body' ? detail.value?.request_body || '' : selectedResponse.value?.body || '')
const highlighted = computed(() => highlightRetentionJSON(bodyText.value))
function bytes(n: number) { return (n / 1048576).toFixed(2) + ' MiB' }
function skipReason(reason: string) { return reason in zh.requestCapture.small.skipReasons ? t('admin.requestCapture.small.skipReasons.' + reason) : reason }
async function load() {
  if (disposed || loading.value) return
  loading.value = true; error.value = ''
  try {
    const params: api.SmallFilter = { page: page.value, page_size: 20 }
    for (const key of ['user_id', 'api_key_id', 'status', 'min_repeats'] as const) if (filters[key]) params[key] = Number(filters[key])
    if (filters.model) params.model = filters.model
    if (filters.request_id) params.request_id = filters.request_id
    if (filters.from) params.from = new Date(filters.from).toISOString()
    if (filters.to) params.to = new Date(filters.to).toISOString()
    const [summary, result] = await Promise.all([api.getSmallStats(), api.listSmallRecords(params)])
    if (!disposed) { stats.value = summary; records.value = result.items; more.value = result.has_more }
  } catch (err) { report(err) } finally { loading.value = false }
}
async function open(id: number) {
  const version = ++detailVersion
  try { const result = await stepUp.run(() => api.getSmallDetail(id)); if (!disposed && version === detailVersion) { detail.value = result; sample.value = 0; part.value = 'request_headers'; pretty.value = false } } catch (err) { report(err) }
}
async function clear() {
  if (!(await confirm(t('admin.requestCapture.small.clearConfirm')))) return
  try { await stepUp.run(api.clearSmallRecords); detailVersion++; detail.value = undefined; page.value = 1; await load() } catch (err) { report(err) }
}
async function copyText(text: string) { await copyToClipboard(text, t('admin.requestCapture.small.copied')) }
async function copyCurl() { if (detail.value) await copyText(detail.value.curl) }
onMounted(load)
onUnmounted(() => { disposed = true; detailVersion++; stepUp.onCancel(); detail.value = undefined })
</script>

<style scoped>
.retention-bar { background: #2563eb; }
:global(.dark .retention-bar) { background: #3b82f6; }
@media (forced-colors: active) { .retention-bar { background: Highlight; forced-color-adjust: none; } }
</style>
