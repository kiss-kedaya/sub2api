<!-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later. -->
<template>
  <AppLayout>
    <div class="flex flex-col gap-6">
      <AdminPageHeader :title="t('admin.groupCacheTest.title')" :description="t('admin.groupCacheTest.description')">
        <template #actions><UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loading" :disabled="busy || loading" :label="t('admin.groupCacheTest.reload')" @click="loadGroups" /></template>
      </AdminPageHeader>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
      <UAlert v-if="storageError" role="status" color="warning" variant="subtle" icon="i-lucide-hard-drive" :description="t('admin.groupCacheTest.storageError')" />
      <div class="grid items-start gap-6 xl:grid-cols-[360px_minmax(0,1fr)]">
        <UPageCard variant="subtle" as="section" aria-labelledby="cache-config-title" :ui="{ container: 'gap-y-4 sm:p-5' }">
          <h2 id="cache-config-title" class="text-base font-semibold text-highlighted">{{ t('admin.groupCacheTest.configuration') }}</h2>
          <form class="flex flex-col gap-4" @submit.prevent="start">
            <fieldset :disabled="busy || loading" class="flex flex-col gap-4">
              <UFormField :label="t('admin.groupCacheTest.group')">
                <USelect v-model="groupModel" class="w-full" data-testid="cache-group" :items="groupItems" :placeholder="t('admin.groupCacheTest.selectGroup')" />
              </UFormField>
              <UFormField :label="t('admin.groupCacheTest.key')" :help="t('admin.groupCacheTest.ownedKeyHint')">
                <USelect v-model="keyModel" class="w-full" :disabled="keysLoading || !groupId" data-testid="cache-key" :items="keyItems" :placeholder="keyPlaceholder" />
              </UFormField>
              <UAlert
                v-if="keyError"
                role="alert"
                color="error"
                variant="subtle"
                icon="i-lucide-circle-alert"
                :description="t('admin.groupCacheTest.keysError')"
                :actions="[{ label: t('admin.groupCacheTest.reload'), color: 'error', variant: 'outline', size: 'xs', onClick: loadKeys }]"
              />
              <p v-else-if="groupId && !keysLoading && !keys.length" class="-mt-2 text-xs text-muted">{{ t('admin.groupCacheTest.noKeys') }} <RouterLink to="/keys" class="text-default underline underline-offset-4">{{ t('admin.groupCacheTest.manageKeys') }}</RouterLink></p>
              <UFormField :label="t('admin.groupCacheTest.protocol')">
                <USelect v-model="settings.protocol" class="w-full" data-testid="cache-protocol" :items="protocolItems" />
              </UFormField>
              <UFormField :label="t('admin.groupCacheTest.model')" required>
                <UInput v-model.trim="settings.model" list="cache-models" class="w-full" maxlength="200" required autocomplete="off" :placeholder="t('admin.groupCacheTest.modelPlaceholder')" data-testid="cache-model" />
                <datalist id="cache-models"><option v-for="model in models" :key="model" :value="model" /></datalist>
              </UFormField>
              <div class="grid grid-cols-2 gap-3">
                <UFormField :label="t('admin.groupCacheTest.rounds')">
                  <USelect v-model="settings.rounds" class="w-full" :items="roundItems" />
                </UFormField>
                <UFormField :label="t('admin.groupCacheTest.prefix')">
                  <USelect v-model="settings.prefixBlocks" class="w-full" :items="prefixItems" />
                </UFormField>
              </div>
              <p class="-mt-2 text-xs text-muted">{{ t('admin.groupCacheTest.prefixHint', { count: prefixLength.toLocaleString() }) }}</p>
              <UCollapsible :unmount-on-hide="false" class="flex flex-col gap-3">
                <UButton type="button" color="neutral" variant="ghost" size="sm" class="-mx-2.5 self-start" icon="i-lucide-sliders-horizontal" trailing-icon="i-lucide-chevron-down" :label="t('admin.groupCacheTest.advanced')" />
                <template #content>
                  <div class="flex flex-col gap-3">
                    <UFormField :label="t('admin.groupCacheTest.interval')"><UInput v-model.number="settings.intervalMs" class="w-full" type="number" min="0" max="10000" step="100" required /></UFormField>
                    <UFormField :label="t('admin.groupCacheTest.timeout')"><USelect v-model="settings.timeoutMs" class="w-full" :items="timeoutItems" /></UFormField>
                    <UFormField :label="t('admin.groupCacheTest.maxOutput')"><UInput v-model.number="settings.maxOutputTokens" class="w-full" type="number" min="64" max="4096" required /></UFormField>
                    <template v-if="settings.protocol !== 'messages'">
                      <UFormField :label="t('admin.groupCacheTest.reasoning')"><USelect v-model="reasoningModel" class="w-full" :items="reasoningItems" /></UFormField>
                      <UFormField :label="t('admin.groupCacheTest.tier')"><USelect v-model="tierModel" class="w-full" :items="tierItems" /></UFormField>
                    </template>
                    <p class="text-xs text-muted">{{ t('admin.groupCacheTest.compatibility') }}</p>
                  </div>
                </template>
              </UCollapsible>
            </fieldset>
            <UAlert color="warning" variant="subtle" icon="i-lucide-badge-dollar-sign" :description="t('admin.groupCacheTest.chargeWarning', { count: settings.rounds })" />
            <div class="flex gap-2">
              <UButton type="submit" block class="flex-1" icon="i-lucide-play" :disabled="!canStart" data-testid="cache-start" :label="t('admin.groupCacheTest.start')" />
              <UButton v-if="busy" type="button" color="neutral" variant="outline" icon="i-lucide-square" :disabled="stopping" data-testid="cache-stop" :label="stopping ? t('admin.groupCacheTest.stopping') : t('admin.groupCacheTest.stop')" @click="stop" />
            </div>
          </form>
        </UPageCard>
        <div class="flex min-w-0 flex-col gap-6">
          <section class="flex min-w-0 flex-col gap-4" aria-labelledby="cache-results-title">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h2 id="cache-results-title" class="text-base font-semibold text-highlighted">{{ t('admin.groupCacheTest.results') }}</h2>
              <div v-if="selected" class="flex flex-wrap gap-1.5">
                <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-receipt" :disabled="busy || !hasPendingBills" data-testid="cache-reconcile" :label="t('admin.groupCacheTest.reconcile')" @click="refreshBills" />
                <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-download" :disabled="busy" :label="t('admin.groupCacheTest.export')" @click="download" />
              </div>
            </div>
            <div v-if="!selected" class="flex flex-col items-center gap-2 rounded-lg bg-elevated/50 px-6 py-16 text-center">
              <UIcon name="i-lucide-database-zap" class="size-8 text-dimmed" />
              <p class="font-medium text-highlighted">{{ t('admin.groupCacheTest.emptyTitle') }}</p>
              <p class="max-w-md text-sm leading-6 text-muted">{{ t('admin.groupCacheTest.emptyHint') }}</p>
            </div>
            <template v-else>
              <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm">
                <strong class="text-highlighted">{{ selected.groupName }}</strong>
                <span class="text-default">{{ selected.settings.model }}</span>
                <UBadge color="neutral" variant="outline" size="sm" class="font-mono">{{ CACHE_ENDPOINTS[selected.settings.protocol] }}</UBadge>
                <UBadge role="status" aria-live="polite" class="ml-auto" :color="busy ? 'info' : selected.status === 'completed' ? 'success' : selected.status === 'failed' ? 'error' : 'neutral'" variant="subtle">{{ busy ? t(reconciling ? 'admin.groupCacheTest.reconciling' : 'admin.groupCacheTest.testing') : t('admin.groupCacheTest.status.' + selected.status) }}</UBadge>
              </div>
              <p class="-mt-2 text-xs text-muted">{{ runConfiguration(selected) }}</p>
              <UPageGrid class="gap-4 sm:gap-6 lg:grid-cols-2 2xl:grid-cols-4 2xl:gap-px">
                <UPageCard icon="i-lucide-flag" :title="t('admin.groupCacheTest.baselineHit')" variant="subtle" :ui="statUi" :class="statClass">
                  <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ percent(cacheHitRate(selected.rounds[0]?.tokens ?? null)) }}</span>
                </UPageCard>
                <UPageCard icon="i-lucide-repeat" :title="t('admin.groupCacheTest.repeatHit')" variant="subtle" :ui="statUi" :class="statClass">
                  <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ percent(cacheHitRate(repeatTokens)) }}</span>
                  <span class="text-xs text-muted">{{ t('admin.groupCacheTest.reportedRounds', { count: comparableRounds }) }}</span>
                </UPageCard>
                <UPageCard icon="i-lucide-database" :title="t('admin.groupCacheTest.cacheRead')" variant="subtle" :ui="statUi" :class="statClass">
                  <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ number(repeatTokens.read) }}</span>
                  <span class="text-xs text-muted">{{ t('admin.groupCacheTest.repeatOnly') }}</span>
                </UPageCard>
                <UPageCard icon="i-lucide-wallet" :title="t('admin.groupCacheTest.billed')" variant="subtle" :ui="statUi" :class="statClass">
                  <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ money(knownCost) }}</span>
                  <span class="text-xs text-muted">{{ t('admin.groupCacheTest.billsMatched', { count: matchedBills, total: selected.rounds.length }) }}</span>
                </UPageCard>
              </UPageGrid>
              <p class="text-xs text-muted">{{ t('admin.groupCacheTest.resultHint') }}</p>
              <UTable :data="selected.rounds" :columns="roundColumns" :ui="tableUi" class="whitespace-nowrap tabular-nums">
                <template #round-cell="{ row }">
                  <UButton variant="link" class="p-0" :aria-expanded="detailIndex === row.original.index" @click="detailIndex = detailIndex === row.original.index ? null : row.original.index">{{ row.original.index === 1 ? t('admin.groupCacheTest.baseline') : t('admin.groupCacheTest.repeat', { index: row.original.index - 1 }) }}</UButton>
                  <span class="mt-1 block text-xs" :class="row.original.status === 'failed' ? 'text-error' : 'text-muted'">{{ t('admin.groupCacheTest.status.' + row.original.status) }}</span>
                </template>
                <template #billed-cell="{ row }">
                  {{ money(row.original.bill?.actualCost) }}<span class="mt-1 block text-xs text-muted">{{ t('admin.groupCacheTest.billing.' + row.original.billingState) }}</span>
                </template>
              </UTable>
              <div v-if="detail" class="rounded-lg bg-elevated/50 p-4" data-testid="cache-detail">
                <h3 class="text-sm font-semibold text-highlighted">{{ t('admin.groupCacheTest.roundDetails', { index: detail.index }) }}</h3>
                <p v-if="detail.error" role="alert" class="mt-2 text-sm text-error">{{ t('admin.groupCacheTest.errors.' + detail.error) }}<span v-if="detail.httpStatus"> · HTTP {{ detail.httpStatus }}</span></p>
                <dl class="cache-details mt-3"><dt>{{ t('admin.groupCacheTest.requestId') }}</dt><dd class="break-all font-mono text-xs">{{ detail.requestId || t('admin.groupCacheTest.notReported') }}</dd><dt>{{ t('admin.groupCacheTest.modelReturned') }}</dt><dd>{{ detail.responseModel || '—' }}</dd><dt>{{ t('admin.groupCacheTest.tierReturned') }}</dt><dd>{{ detail.serviceTier || t('admin.groupCacheTest.notReported') }}</dd><dt>{{ t('admin.groupCacheTest.duration') }}</dt><dd>{{ ms(detail.durationMs) }}</dd></dl>
                <template v-if="detail.bill"><h4 class="mt-4 text-sm font-semibold text-highlighted">{{ t('admin.groupCacheTest.billDetails') }}</h4><dl class="cache-details mt-3"><dt>{{ t('admin.groupCacheTest.route') }}</dt><dd>{{ t('admin.groupCacheTest.routeValue', { group: detail.bill.groupId ?? '—', account: detail.bill.accountId ?? '—' }) }} · {{ detail.bill.upstreamModel || detail.bill.model }}</dd><dt>{{ t('admin.groupCacheTest.tier') }} / {{ t('admin.groupCacheTest.reasoning') }}</dt><dd>{{ detail.bill.serviceTier || '—' }} / {{ detail.bill.reasoningEffort || '—' }}</dd><dt>{{ t('admin.groupCacheTest.billedTokens') }}</dt><dd>{{ number(detail.bill.tokens.input) }} / {{ number(detail.bill.tokens.read) }} / {{ number(detail.bill.tokens.write) }} / {{ number(detail.bill.tokens.output) }}</dd><dt>{{ t('admin.groupCacheTest.componentCosts') }}</dt><dd>{{ money(detail.bill.inputCost) }} / {{ money(detail.bill.readCost) }} / {{ money(detail.bill.writeCost) }} / {{ money(detail.bill.outputCost) }}</dd><dt>{{ t('admin.groupCacheTest.standardCost') }}</dt><dd>{{ money(detail.bill.totalCost) }} · {{ t('admin.groupCacheTest.multiplier') }} {{ number(detail.bill.multiplier) }}</dd><dt>{{ t('admin.groupCacheTest.billed') }}</dt><dd>{{ money(detail.bill.actualCost) }}</dd><dt>{{ t('admin.groupCacheTest.serverFirstToken') }}</dt><dd>{{ ms(detail.bill.firstTokenMs) }}</dd></dl></template>
                <p class="mt-3 text-xs text-muted">{{ t('admin.groupCacheTest.billingHint') }}</p>
              </div>
              <UAlert v-if="hasPendingBills" color="warning" variant="subtle" icon="i-lucide-clock" :description="t('admin.groupCacheTest.pendingHint')" />
            </template>
          </section>
          <USeparator />
          <section class="flex flex-col gap-2" aria-labelledby="cache-history-title">
            <div class="flex items-center justify-between gap-3">
              <div class="flex items-center gap-1.5">
                <h2 id="cache-history-title" class="text-base font-semibold text-highlighted">{{ t('admin.groupCacheTest.history') }}</h2>
                <UPopover mode="hover">
                  <UIcon name="i-lucide-info" class="size-4 text-dimmed" />
                  <template #content><p class="max-w-xs p-2.5 text-xs text-muted">{{ t('admin.groupCacheTest.historyHint') }}</p></template>
                </UPopover>
              </div>
              <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-trash-2" :disabled="busy || !history.length" :label="t('admin.groupCacheTest.clearHistory')" @click="clearHistory" />
            </div>
            <p v-if="!history.length" class="py-6 text-sm text-muted">{{ t('admin.groupCacheTest.noHistory') }}</p>
            <ul v-else class="flex flex-col divide-y divide-default">
              <li v-for="run in history" :key="run.id" class="py-0.5">
                <UButton
                  color="neutral"
                  variant="ghost"
                  block
                  class="flex-wrap justify-start gap-x-4 gap-y-1 text-left"
                  :class="selected?.id === run.id ? 'bg-elevated' : ''"
                  :disabled="busy"
                  :aria-pressed="selected?.id === run.id"
                  @click="selected = run; detailIndex = null"
                >
                  <span class="min-w-32 font-medium text-highlighted">{{ run.groupName }}</span>
                  <span class="text-default">{{ run.settings.model }}</span>
                  <span class="text-xs text-muted">{{ date(run.startedAt) }}</span>
                  <span class="ml-auto text-muted">{{ t('admin.groupCacheTest.status.' + run.status) }} · {{ run.rounds.length }} / {{ run.settings.rounds }}</span>
                </UButton>
              </li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import type { TableColumn } from '@nuxt/ui'
import { statCardClass, statCardUi, templateTableUi } from '@/views/admin/ops/utils/templateUi'
import { useAuthStore } from '@/stores/auth'
import * as groupsAPI from '@/api/admin/groups'
import * as keysAPI from '@/api/keys'
import { executeCacheRun, reconcileCacheBills } from '@/api/groupCacheTest'
import { CACHE_ENDPOINTS, DEFAULT_CACHE_SETTINGS, aggregateCacheTokens, buildCachePrefix, cacheHitRate, createCacheTestId, exportCacheRun, loadCacheHistory, saveCacheHistory, validCacheSettings, type CacheTestRun } from '@/utils/groupCacheTest'
import type { AdminGroup, ApiKey } from '@/types'
import cacheTestZh from '@/i18n/locales/zh/admin/groupCacheTest'
import cacheTestEn from '@/i18n/locales/en/admin/groupCacheTest'

const { t } = useI18n({ useScope: 'local', messages: { zh: { admin: cacheTestZh }, en: { admin: cacheTestEn } } }), auth = useAuthStore()
const settings = reactive({ ...DEFAULT_CACHE_SETTINGS })
const groups = ref<AdminGroup[]>([]), keys = ref<Pick<ApiKey, 'id' | 'name'>[]>([]), models = ref<string[]>([])
const groupId = ref<number | null>(null), keyId = ref<number | null>(null)
const loading = ref(false), keysLoading = ref(false), keyError = ref(false), busy = ref(false), reconciling = ref(false), stopping = ref(false)
const error = ref(''), storageError = ref(false), selected = ref<CacheTestRun | null>(null), history = ref<CacheTestRun[]>([]), detailIndex = ref<number | null>(null)
let alive = true, generation = 0, keysController: AbortController | null = null, runController: AbortController | null = null
const group = computed(() => groups.value.find(g => g.id === groupId.value))
const canStart = computed(() => !!auth.user && !!group.value && !!keyId.value && keys.value.some(k => k.id === keyId.value) && !busy.value && !loading.value && !keysLoading.value && !keyError.value && validCacheSettings(settings))
const prefixLength = computed(() => buildCachePrefix('00000000-0000-0000-0000-000000000000', settings.prefixBlocks).length)
const detail = computed(() => selected.value?.rounds.find(r => r.index === detailIndex.value))
const repeatTokens = computed(() => aggregateCacheTokens(selected.value?.rounds.slice(1) ?? []))
const comparableRounds = computed(() => selected.value?.rounds.slice(1).filter(r => r.status === 'completed' && cacheHitRate(r.tokens) !== null).length ?? 0)
const matchedBills = computed(() => selected.value?.rounds.filter(r => r.bill?.actualCost != null).length ?? 0)
const knownCost = computed(() => matchedBills.value ? selected.value!.rounds.reduce((sum, r) => sum + (r.bill?.actualCost ?? 0), 0) : null)
const hasPendingBills = computed(() => selected.value?.rounds.some(r => !r.bill) ?? false)
const statUi = statCardUi, statClass = statCardClass, tableUi = templateTableUi
// Reka select items cannot carry '' as a value; "unspecified" maps to a sentinel and back.
const UNSET = '__unset__'
const groupItems = computed(() => groups.value.map(g => ({ value: g.id, label: `${g.name} · ${g.platform}` })))
const keyItems = computed(() => keys.value.map(k => ({ value: k.id, label: `${k.name} · #${k.id}` })))
const keyPlaceholder = computed(() => keysLoading.value ? t('admin.groupCacheTest.loading') : t('admin.groupCacheTest.selectKey'))
// USelect models use undefined for "nothing selected"; the view keeps null.
const groupModel = computed({ get: () => groupId.value ?? undefined, set: (value: number | undefined) => { groupId.value = value ?? null } })
const keyModel = computed({ get: () => keyId.value ?? undefined, set: (value: number | undefined) => { keyId.value = value ?? null } })
const protocolItems = [{ value: 'responses', label: 'Responses' }, { value: 'chat', label: 'Chat Completions' }, { value: 'messages', label: 'Messages' }]
const roundItems = [2, 3, 4, 5, 6].map(count => ({ value: count, label: String(count) }))
const prefixItems = computed(() => [16, 48, 96, 128].map(count => ({ value: count, label: t('admin.groupCacheTest.blocks', { count }) })))
const timeoutItems = [60000, 120000, 300000].map(value => ({ value, label: `${value / 1000} s` }))
const reasoningItems = computed(() => [{ value: UNSET, label: t('admin.groupCacheTest.unspecified') }, ...['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'].map(effort => ({ value: effort, label: effort }))])
const tierItems = computed(() => [{ value: UNSET, label: t('admin.groupCacheTest.unspecified') }, { value: 'default', label: 'default' }, { value: 'priority', label: 'priority' }])
const reasoningModel = computed({ get: () => settings.reasoningEffort || UNSET, set: (value: string) => { settings.reasoningEffort = (value === UNSET ? '' : value) as typeof settings.reasoningEffort } })
const tierModel = computed({ get: () => settings.serviceTier || UNSET, set: (value: string) => { settings.serviceTier = (value === UNSET ? '' : value) as typeof settings.serviceTier } })
type CacheRound = CacheTestRun['rounds'][number]
const roundColumns = computed<TableColumn<CacheRound>[]>(() => [
  { id: 'round', header: t('admin.groupCacheTest.round') },
  { id: 'input', header: t('admin.groupCacheTest.input'), cell: ({ row }) => number(row.original.tokens?.input) },
  { id: 'read', header: t('admin.groupCacheTest.cacheRead'), cell: ({ row }) => number(row.original.tokens?.read) },
  { id: 'write', header: t('admin.groupCacheTest.cacheWrite'), cell: ({ row }) => number(row.original.tokens?.write) },
  { id: 'hit', header: t('admin.groupCacheTest.hitRate'), cell: ({ row }) => percent(cacheHitRate(row.original.tokens)) },
  { id: 'output', header: t('admin.groupCacheTest.output'), cell: ({ row }) => number(row.original.tokens?.output) },
  { id: 'first', header: t('admin.groupCacheTest.firstText'), cell: ({ row }) => ms(row.original.firstTextMs) },
  { id: 'billed', header: t('admin.groupCacheTest.billed') }
])
const number = (n: number | null | undefined) => n == null ? '—' : n.toLocaleString()
const percent = (n: number | null) => n == null ? '—' : (n * 100).toFixed(1) + '%'
const money = (n: number | null | undefined) => n == null ? '—' : '$' + n.toFixed(6)
const ms = (n: number | null | undefined) => n == null ? '—' : (n / 1000).toFixed(2) + ' s'
const date = (value: string) => new Date(value).toLocaleString()
function runConfiguration(run: CacheTestRun) {
  const s = run.settings
  return t('admin.groupCacheTest.runConfig', { blocks: s.prefixBlocks, output: s.maxOutputTokens, interval: s.intervalMs, tier: s.serviceTier || t('admin.groupCacheTest.unspecified'), reasoning: s.reasoningEffort || t('admin.groupCacheTest.unspecified') })
}
// Smart-routing keys can select another group, so use a single-group key for an attributable test.
function isSingleGroupKey(key: ApiKey, id: number): boolean {
  return key.group_id === id && (!key.group_ids?.length || (key.group_ids.length === 1 && key.group_ids[0] === id))
}
async function loadGroups() {
  const request = ++generation
  loading.value = true; error.value = ''
  try {
    const result = await groupsAPI.getAll()
    if (!alive || request !== generation) return
    groups.value = result
    if (!result.some(g => g.id === groupId.value)) groupId.value = null
    else await loadKeys()
  } catch { if (alive && request === generation) error.value = t('admin.groupCacheTest.loadError') }
  finally { if (alive && request === generation) loading.value = false }
}
async function loadKeys() {
  keysController?.abort()
  const controller = new AbortController(); keysController = controller
  const id = groupId.value, userId = auth.user?.id
  keys.value = []; keyId.value = null; keyError.value = false
  if (!id || !userId) { keysLoading.value = false; return }
  keysLoading.value = true
  try {
    const all: Pick<ApiKey, 'id' | 'name'>[] = []
    for (let page = 1; ; page++) {
      const result = await keysAPI.list(page, 100, { group_id: id, status: 'active' }, { signal: controller.signal })
      if (!alive || controller.signal.aborted) return
      all.push(...result.items.filter(k => k.user_id === userId && isSingleGroupKey(k, id) && k.status === 'active').map(k => ({ id: k.id, name: k.name })))
      if (page >= result.pages || !result.items.length) break
    }
    keys.value = all
    if (all.length === 1) keyId.value = all[0].id
  } catch { if (!controller.signal.aborted && alive) keyError.value = true }
  finally { if (!controller.signal.aborted && alive) keysLoading.value = false }
}
watch(groupId, async () => {
  models.value = []; settings.model = ''
  settings.protocol = group.value?.platform === 'anthropic' ? 'messages' : 'responses'
  void loadKeys()
  const id = groupId.value
  if (!id) return
  try { const result = await groupsAPI.getModelAllowlistCandidates(id); if (alive && id === groupId.value) models.value = result } catch { /* Manual model entry stays available. */ }
})
watch(() => settings.protocol, value => { if (value === 'messages') { settings.reasoningEffort = ''; settings.serviceTier = '' } })
function persist(run: CacheTestRun, userId: number) {
  if (!alive || userId !== auth.user?.id) return
  history.value = [run, ...history.value.filter(item => item.id !== run.id)].slice(0, 20)
  storageError.value = !saveCacheHistory(userId, history.value)
}
async function start() {
  if (!canStart.value || !auth.user || !group.value || !keyId.value) return
  const userId = auth.user.id, targetGroup = group.value, targetKey = keyId.value
  const snapshot = { ...settings }, controller = new AbortController(); runController = controller
  busy.value = true; stopping.value = false; error.value = ''; detailIndex.value = null
  try {
    const key = await keysAPI.getById(targetKey)
    if (controller.signal.aborted || !alive || userId !== auth.user?.id) return
    if (key.user_id !== userId || !isSingleGroupKey(key, targetGroup.id) || key.status !== 'active' || !key.key || (key.expires_at && Date.parse(key.expires_at) <= Date.now()) || (key.quota > 0 && key.quota_used >= key.quota)) {
      error.value = t('admin.groupCacheTest.keyChanged'); return
    }
    selected.value = { version: 1, id: createCacheTestId(), startedAt: new Date().toISOString(), finishedAt: null, groupId: targetGroup.id, groupName: targetGroup.name, keyId: key.id, keyName: key.name, settings: snapshot, status: 'running', rounds: [] }
    const run = selected.value
    const changed = () => persist(run, userId)
    await executeCacheRun(run, key.key, controller.signal, changed)
    if (!controller.signal.aborted) {
      reconciling.value = true
      await reconcileCacheBills(run, userId, controller.signal, changed)
    }
  } catch { if (alive && !controller.signal.aborted) error.value = t('admin.groupCacheTest.startError') }
  finally { if (alive) { busy.value = false; reconciling.value = false; stopping.value = false } }
}
function stop() { stopping.value = true; runController?.abort() }
async function refreshBills() {
  if (busy.value || !selected.value || !auth.user) return
  const run = selected.value, userId = auth.user.id, controller = new AbortController(); runController = controller
  busy.value = true; reconciling.value = true; stopping.value = false
  try { await reconcileCacheBills(run, userId, controller.signal, () => persist(run, userId)) }
  finally { if (alive) { busy.value = false; reconciling.value = false; stopping.value = false } }
}
function download() {
  if (selected.value) saveAs(new Blob([exportCacheRun(selected.value)], { type: 'application/json' }), 'group-cache-test-' + selected.value.id + '.json')
}
function clearHistory() { history.value = []; selected.value = null; if (auth.user) storageError.value = !saveCacheHistory(auth.user.id, []) }
watch(() => auth.user?.id, () => {
  generation++; keysController?.abort(); runController?.abort(); selected.value = null; history.value = []; keys.value = []; groups.value = []; groupId.value = null
  if (auth.user && alive) { history.value = loadCacheHistory(auth.user.id); void loadGroups() }
})
onMounted(() => { if (auth.user) history.value = loadCacheHistory(auth.user.id); void loadGroups() })
onBeforeUnmount(() => { alive = false; generation++; keysController?.abort(); runController?.abort() })
</script>

<style scoped>
@reference "../../style.css";
.cache-details { @apply grid grid-cols-1 gap-x-4 gap-y-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)]; }
.cache-details dt { @apply text-muted; }
.cache-details dd { @apply wrap-break-word text-default; }
</style>
