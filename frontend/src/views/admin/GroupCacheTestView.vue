<!-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later. -->
<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.groupCacheTest.title') }}</h1><p class="mt-2 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.groupCacheTest.description') }}</p></div>
        <button class="btn btn-secondary" :disabled="busy || loading" @click="loadGroups">{{ t('admin.groupCacheTest.reload') }}</button>
      </header>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
      <p v-if="storageError" role="status" class="text-sm text-amber-600">{{ t('admin.groupCacheTest.storageError') }}</p>
      <div class="grid items-start gap-6 xl:grid-cols-[340px_minmax(0,1fr)]">
        <section class="cache-panel" aria-labelledby="cache-config-title">
          <h2 id="cache-config-title" class="cache-heading">{{ t('admin.groupCacheTest.configuration') }}</h2>
          <form class="mt-4 space-y-4" @submit.prevent="start">
            <fieldset :disabled="busy || loading" class="space-y-4">
              <label class="cache-field"><span>{{ t('admin.groupCacheTest.group') }}</span><select v-model="groupId" class="input" data-testid="cache-group"><option :value="null">{{ t('admin.groupCacheTest.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} · {{ group.platform }}</option></select></label>
              <label class="cache-field"><span>{{ t('admin.groupCacheTest.key') }}</span><select v-model="keyId" class="input" :disabled="keysLoading || !groupId" data-testid="cache-key"><option :value="null">{{ keysLoading ? t('admin.groupCacheTest.loading') : t('admin.groupCacheTest.selectKey') }}</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }} · #{{ key.id }}</option></select></label>
              <p class="cache-hint">{{ t('admin.groupCacheTest.ownedKeyHint') }}</p>
              <p v-if="keyError" role="alert" class="text-sm text-red-600">{{ t('admin.groupCacheTest.keysError') }} <button type="button" class="underline" @click="loadKeys">{{ t('admin.groupCacheTest.reload') }}</button></p>
              <p v-else-if="groupId && !keysLoading && !keys.length" class="cache-hint">{{ t('admin.groupCacheTest.noKeys') }} <RouterLink to="/keys" class="text-primary-600 underline">{{ t('admin.groupCacheTest.manageKeys') }}</RouterLink></p>
              <label class="cache-field"><span>{{ t('admin.groupCacheTest.protocol') }}</span><select v-model="settings.protocol" class="input" data-testid="cache-protocol"><option value="responses">Responses</option><option value="chat">Chat Completions</option><option value="messages">Messages</option></select></label>
              <label class="cache-field"><span>{{ t('admin.groupCacheTest.model') }}</span><input v-model.trim="settings.model" list="cache-models" class="input" maxlength="200" required autocomplete="off" :placeholder="t('admin.groupCacheTest.modelPlaceholder')" data-testid="cache-model" /><datalist id="cache-models"><option v-for="model in models" :key="model" :value="model" /></datalist></label>
              <div class="grid grid-cols-2 gap-3">
                <label class="cache-field"><span>{{ t('admin.groupCacheTest.rounds') }}</span><select v-model.number="settings.rounds" class="input"><option v-for="count in [2, 3, 4, 5, 6]" :key="count" :value="count">{{ count }}</option></select></label>
                <label class="cache-field"><span>{{ t('admin.groupCacheTest.prefix') }}</span><select v-model.number="settings.prefixBlocks" class="input"><option v-for="count in [16, 48, 96, 128]" :key="count" :value="count">{{ t('admin.groupCacheTest.blocks', { count }) }}</option></select></label>
              </div>
              <p class="cache-hint">{{ t('admin.groupCacheTest.prefixHint', { count: prefixLength.toLocaleString() }) }}</p>
              <details class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
                <summary class="cursor-pointer text-sm font-medium">{{ t('admin.groupCacheTest.advanced') }}</summary>
                <div class="mt-3 space-y-3">
                  <label class="cache-field"><span>{{ t('admin.groupCacheTest.interval') }}</span><input v-model.number="settings.intervalMs" class="input" type="number" min="0" max="10000" step="100" required /></label>
                  <label class="cache-field"><span>{{ t('admin.groupCacheTest.timeout') }}</span><select v-model.number="settings.timeoutMs" class="input"><option :value="60000">60 s</option><option :value="120000">120 s</option><option :value="300000">300 s</option></select></label>
                  <label class="cache-field"><span>{{ t('admin.groupCacheTest.maxOutput') }}</span><input v-model.number="settings.maxOutputTokens" class="input" type="number" min="64" max="4096" required /></label>
                  <template v-if="settings.protocol !== 'messages'">
                    <label class="cache-field"><span>{{ t('admin.groupCacheTest.reasoning') }}</span><select v-model="settings.reasoningEffort" class="input"><option value="">{{ t('admin.groupCacheTest.unspecified') }}</option><option v-for="effort in ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']" :key="effort" :value="effort">{{ effort }}</option></select></label>
                    <label class="cache-field"><span>{{ t('admin.groupCacheTest.tier') }}</span><select v-model="settings.serviceTier" class="input"><option value="">{{ t('admin.groupCacheTest.unspecified') }}</option><option value="default">default</option><option value="priority">priority</option></select></label>
                  </template>
                  <p class="cache-hint">{{ t('admin.groupCacheTest.compatibility') }}</p>
                </div>
              </details>
            </fieldset>
            <div class="rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">{{ t('admin.groupCacheTest.chargeWarning', { count: settings.rounds }) }}</div>
            <div class="flex gap-2"><button type="submit" class="btn btn-primary flex-1" :disabled="!canStart" data-testid="cache-start">{{ t('admin.groupCacheTest.start') }}</button><button v-if="busy" type="button" class="btn btn-secondary" :disabled="stopping" data-testid="cache-stop" @click="stop">{{ stopping ? t('admin.groupCacheTest.stopping') : t('admin.groupCacheTest.stop') }}</button></div>
          </form>
        </section>
        <div class="min-w-0 space-y-5">
          <section class="cache-panel" aria-labelledby="cache-results-title">
            <div class="flex flex-wrap items-center justify-between gap-3"><h2 id="cache-results-title" class="cache-heading">{{ t('admin.groupCacheTest.results') }}</h2><div v-if="selected" class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" :disabled="busy || !hasPendingBills" data-testid="cache-reconcile" @click="refreshBills">{{ t('admin.groupCacheTest.reconcile') }}</button><button class="btn btn-secondary btn-sm" :disabled="busy" @click="download">{{ t('admin.groupCacheTest.export') }}</button></div></div>
            <div v-if="!selected" class="py-16 text-center"><p class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.groupCacheTest.emptyTitle') }}</p><p class="mx-auto mt-2 max-w-md text-sm leading-6 text-gray-500">{{ t('admin.groupCacheTest.emptyHint') }}</p></div>
            <template v-else>
              <div class="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm"><strong>{{ selected.groupName }}</strong><span>{{ selected.settings.model }}</span><code class="text-xs">{{ CACHE_ENDPOINTS[selected.settings.protocol] }}</code><span role="status" aria-live="polite" class="ml-auto">{{ busy ? t(reconciling ? 'admin.groupCacheTest.reconciling' : 'admin.groupCacheTest.testing') : t('admin.groupCacheTest.status.' + selected.status) }}</span></div>
              <p class="mt-2 text-xs text-gray-500">{{ runConfiguration(selected) }}</p>
              <div class="my-5 grid grid-cols-2 gap-3 2xl:grid-cols-4">
                <div class="cache-stat"><span>{{ t('admin.groupCacheTest.baselineHit') }}</span><strong>{{ percent(cacheHitRate(selected.rounds[0]?.tokens ?? null)) }}</strong></div>
                <div class="cache-stat"><span>{{ t('admin.groupCacheTest.repeatHit') }}</span><strong>{{ percent(cacheHitRate(repeatTokens)) }}</strong><small>{{ t('admin.groupCacheTest.reportedRounds', { count: comparableRounds }) }}</small></div>
                <div class="cache-stat"><span>{{ t('admin.groupCacheTest.cacheRead') }}</span><strong>{{ number(repeatTokens.read) }}</strong><small>{{ t('admin.groupCacheTest.repeatOnly') }}</small></div>
                <div class="cache-stat"><span>{{ t('admin.groupCacheTest.billed') }}</span><strong>{{ money(knownCost) }}</strong><small>{{ t('admin.groupCacheTest.billsMatched', { count: matchedBills, total: selected.rounds.length }) }}</small></div>
              </div>
              <p class="cache-hint">{{ t('admin.groupCacheTest.resultHint') }}</p>
              <div class="mt-4 overflow-x-auto">
                <table class="cache-table"><thead><tr><th>{{ t('admin.groupCacheTest.round') }}</th><th>{{ t('admin.groupCacheTest.input') }}</th><th>{{ t('admin.groupCacheTest.cacheRead') }}</th><th>{{ t('admin.groupCacheTest.cacheWrite') }}</th><th>{{ t('admin.groupCacheTest.hitRate') }}</th><th>{{ t('admin.groupCacheTest.output') }}</th><th>{{ t('admin.groupCacheTest.firstText') }}</th><th>{{ t('admin.groupCacheTest.billed') }}</th></tr></thead><tbody>
                  <tr v-for="round in selected.rounds" :key="round.index"><td><button class="text-left text-primary-600 underline underline-offset-4" :aria-expanded="detailIndex === round.index" @click="detailIndex = detailIndex === round.index ? null : round.index">{{ round.index === 1 ? t('admin.groupCacheTest.baseline') : t('admin.groupCacheTest.repeat', { index: round.index - 1 }) }}</button><small :class="round.status === 'failed' ? 'text-red-600' : ''">{{ t('admin.groupCacheTest.status.' + round.status) }}</small></td><td>{{ number(round.tokens?.input) }}</td><td>{{ number(round.tokens?.read) }}</td><td>{{ number(round.tokens?.write) }}</td><td>{{ percent(cacheHitRate(round.tokens)) }}</td><td>{{ number(round.tokens?.output) }}</td><td>{{ ms(round.firstTextMs) }}</td><td>{{ money(round.bill?.actualCost) }}<small>{{ t('admin.groupCacheTest.billing.' + round.billingState) }}</small></td></tr>
                </tbody></table>
              </div>
              <div v-if="detail" class="mt-4 rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-900" data-testid="cache-detail">
                <h3 class="text-sm font-semibold">{{ t('admin.groupCacheTest.roundDetails', { index: detail.index }) }}</h3>
                <p v-if="detail.error" role="alert" class="mt-2 text-sm text-red-600">{{ t('admin.groupCacheTest.errors.' + detail.error) }}<span v-if="detail.httpStatus"> · HTTP {{ detail.httpStatus }}</span></p>
                <dl class="cache-details mt-3"><dt>{{ t('admin.groupCacheTest.requestId') }}</dt><dd class="break-all font-mono text-xs">{{ detail.requestId || t('admin.groupCacheTest.notReported') }}</dd><dt>{{ t('admin.groupCacheTest.modelReturned') }}</dt><dd>{{ detail.responseModel || '—' }}</dd><dt>{{ t('admin.groupCacheTest.tierReturned') }}</dt><dd>{{ detail.serviceTier || t('admin.groupCacheTest.notReported') }}</dd><dt>{{ t('admin.groupCacheTest.duration') }}</dt><dd>{{ ms(detail.durationMs) }}</dd></dl>
                <template v-if="detail.bill"><h4 class="mt-4 text-sm font-semibold">{{ t('admin.groupCacheTest.billDetails') }}</h4><dl class="cache-details mt-3"><dt>{{ t('admin.groupCacheTest.route') }}</dt><dd>{{ t('admin.groupCacheTest.routeValue', { group: detail.bill.groupId ?? '—', account: detail.bill.accountId ?? '—' }) }} · {{ detail.bill.upstreamModel || detail.bill.model }}</dd><dt>{{ t('admin.groupCacheTest.tier') }} / {{ t('admin.groupCacheTest.reasoning') }}</dt><dd>{{ detail.bill.serviceTier || '—' }} / {{ detail.bill.reasoningEffort || '—' }}</dd><dt>{{ t('admin.groupCacheTest.billedTokens') }}</dt><dd>{{ number(detail.bill.tokens.input) }} / {{ number(detail.bill.tokens.read) }} / {{ number(detail.bill.tokens.write) }} / {{ number(detail.bill.tokens.output) }}</dd><dt>{{ t('admin.groupCacheTest.componentCosts') }}</dt><dd>{{ money(detail.bill.inputCost) }} / {{ money(detail.bill.readCost) }} / {{ money(detail.bill.writeCost) }} / {{ money(detail.bill.outputCost) }}</dd><dt>{{ t('admin.groupCacheTest.standardCost') }}</dt><dd>{{ money(detail.bill.totalCost) }} · {{ t('admin.groupCacheTest.multiplier') }} {{ number(detail.bill.multiplier) }}</dd><dt>{{ t('admin.groupCacheTest.billed') }}</dt><dd>{{ money(detail.bill.actualCost) }}</dd><dt>{{ t('admin.groupCacheTest.serverFirstToken') }}</dt><dd>{{ ms(detail.bill.firstTokenMs) }}</dd></dl></template>
                <p class="mt-3 cache-hint">{{ t('admin.groupCacheTest.billingHint') }}</p>
              </div>
              <p v-if="hasPendingBills" class="mt-4 text-sm text-amber-700 dark:text-amber-300">{{ t('admin.groupCacheTest.pendingHint') }}</p>
            </template>
          </section>
          <section class="cache-panel" aria-labelledby="cache-history-title">
            <div class="flex items-center justify-between gap-3"><h2 id="cache-history-title" class="cache-heading">{{ t('admin.groupCacheTest.history') }}</h2><button class="text-sm text-gray-500 underline" :disabled="busy || !history.length" @click="clearHistory">{{ t('admin.groupCacheTest.clearHistory') }}</button></div>
            <p class="mt-2 cache-hint">{{ t('admin.groupCacheTest.historyHint') }}</p>
            <p v-if="!history.length" class="py-6 text-sm text-gray-500">{{ t('admin.groupCacheTest.noHistory') }}</p>
            <ul v-else class="mt-3 divide-y divide-gray-100 dark:divide-dark-700"><li v-for="run in history" :key="run.id"><button class="flex w-full flex-wrap items-center gap-x-4 gap-y-1 rounded-sm p-3 text-left text-sm hover:bg-gray-50 disabled:opacity-50 dark:hover:bg-dark-700" :class="selected?.id === run.id ? 'bg-gray-50 dark:bg-dark-700' : ''" :disabled="busy" :aria-pressed="selected?.id === run.id" @click="selected = run; detailIndex = null"><span class="min-w-32 font-medium">{{ run.groupName }}</span><span>{{ run.settings.model }}</span><span class="text-xs text-gray-500">{{ date(run.startedAt) }}</span><span class="ml-auto">{{ t('admin.groupCacheTest.status.' + run.status) }} · {{ run.rounds.length }} / {{ run.settings.rounds }}</span></button></li></ul>
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
.cache-panel { @apply min-w-0 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800; }
.cache-heading { @apply text-base font-semibold text-gray-900 dark:text-gray-100; }
.cache-field { @apply flex flex-col gap-1.5 text-sm font-medium text-gray-700 dark:text-gray-300; }
.cache-hint { @apply text-xs leading-5 text-gray-500 dark:text-gray-400; }
.cache-stat { @apply flex flex-col gap-1 rounded-lg bg-gray-50 p-3 dark:bg-dark-900; }
.cache-stat > span, .cache-stat > small { @apply text-xs text-gray-500 dark:text-gray-400; }
.cache-stat > strong { @apply text-xl font-semibold tabular-nums text-gray-900 dark:text-gray-100; }
.cache-table { @apply w-full whitespace-nowrap text-left text-sm tabular-nums; }
.cache-table th { @apply border-b border-gray-200 px-3 py-3 text-xs font-medium text-gray-500 dark:border-dark-600; }
.cache-table td { @apply border-b border-gray-100 px-3 py-4 dark:border-dark-700; }
.cache-table small { @apply mt-1 block text-xs text-gray-500; }
.cache-details { @apply grid grid-cols-1 gap-x-4 gap-y-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)]; }
.cache-details dt { @apply text-gray-500; }
.cache-details dd { @apply wrap-break-word text-gray-800 dark:text-gray-200; }
</style>
