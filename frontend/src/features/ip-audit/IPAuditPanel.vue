<template>
  <section class="space-y-4 py-5" data-testid="ip-audit-panel">
    <p class="text-sm text-gray-500">{{ t('ipAudit.scopeHint') }}</p>
    <p class="rounded-sm bg-amber-50 p-3 text-sm text-amber-900" data-testid="host-disconnected">{{ t('ipAudit.externalHint') }}</p>
    <button type="button" class="inline-block text-sm text-primary-600 underline" data-testid="ip-open-settings" @click="$emit('configure')">{{ t('ipAudit.settings') }}</button>
    <details class="rounded-sm border p-3" data-testid="ip-source-inventory">
      <summary class="cursor-pointer">{{ t('ipAudit.inventory') }}</summary>
      <ul class="mt-3 space-y-2 text-sm"><li v-for="item in inventory" :key="item.id">
        <strong>{{ sourceName(item.id) }}</strong> · {{ label(item.scope) }} · {{ label(item.storage) }} · {{ label(item.enumeration) }} · {{ t(item.can_release ? 'ipAudit.sourceManaged' : 'ipAudit.readOnly') }}
        <a v-if="item.manage_url" :href="item.manage_url" class="ml-2 underline">{{ t('ipAudit.sourceManagement') }}</a>
      </li></ul>
    </details>
    <form @submit.prevent="apply">
      <fieldset :disabled="releasing" class="grid items-end gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <label class="text-sm">{{ t('ipAudit.source') }}<select v-model="source" class="input mt-1 w-full" data-testid="audit-source" @change="sourceChanged"><option v-for="item in inventory" :key="item.id" :value="item.id">{{ sourceName(item.id) }}</option></select></label>
      <label class="text-sm">IP / CIDR<input v-model="ip" class="input mt-1 w-full" maxlength="80" :disabled="source === 'admin_history' || source === 'security_penalty'" data-testid="audit-ip" /></label>
      <label v-if="source === 'security_penalty'" class="text-sm">{{ t('ipAudit.explicitUser') }}<input v-model="userId" class="input mt-1 w-full" type="number" min="1" data-testid="audit-user" /></label>
      <label class="text-sm">{{ t('ipAudit.state') }}<select v-model="state" class="input mt-1 w-full" data-testid="audit-state"><option value="">{{ t('ipAudit.allStates') }}</option><option v-for="s in states" :key="s" :value="s">{{ label(s) }}</option></select></label>
      <label class="text-sm">{{ t('ipAudit.order') }}<select v-model="sort" class="input mt-1 w-full" :disabled="source === 'admin_history'"><option value="expires_desc">{{ t('ipAudit.expiryDesc') }}</option><option value="expires_asc">{{ t('ipAudit.expiryAsc') }}</option></select></label>
      <label class="text-sm">{{ t('ipAudit.start') }}<input v-model="start" class="input mt-1 w-full" type="datetime-local" /></label>
      <label class="text-sm">{{ t('ipAudit.end') }}<input v-model="end" class="input mt-1 w-full" type="datetime-local" /></label>
      <div class="flex gap-2"><button type="submit" class="btn btn-secondary" :disabled="loading || releasing" data-testid="audit-filter">{{ t('common.search') }}</button><button type="button" class="btn btn-secondary" :disabled="loading || releasing" @click="refresh">{{ t('common.refresh') }}</button></div>
      </fieldset>
    </form>
    <p v-if="exactSource && !applied.ip" class="text-sm text-amber-700">{{ t('ipAudit.exactHint') }}</p>
    <p v-if="source === 'security_penalty'" class="text-sm text-gray-500">{{ t('ipAudit.penaltyHint') }}</p>
    <p v-if="source === 'admin_history'" class="text-sm text-gray-500">{{ t('ipAudit.historyHint') }}</p>
    <p v-if="selectedSource && !selectedSource.can_release" class="text-sm text-gray-500">{{ t('ipAudit.readOnlyHint') }} <a v-if="selectedSource.manage_url" :href="selectedSource.manage_url" class="underline">{{ t('ipAudit.sourceManagement') }}</a></p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="partial" class="text-sm text-amber-700">{{ t('ipAudit.partial') }}</p>
    <p v-if="loading" role="status">{{ t('common.loading') }}</p>
    <div v-else class="overflow-x-auto"><table class="w-full text-left text-sm">
      <thead><tr><th class="p-2">IP / {{ t('ipAudit.subject') }}</th><th class="p-2">{{ t('ipAudit.source') }}</th><th class="p-2">{{ t('ipAudit.reason') }}</th><th class="p-2">{{ t('ipAudit.state') }}</th><th class="p-2">{{ t('ipAudit.count') }}</th><th class="p-2">{{ t('ipAudit.start') }} / {{ t('ipAudit.expiry') }}</th><th class="p-2">{{ t('common.actions') }}</th></tr></thead>
      <tbody><tr v-for="row in rows" :key="row.id" class="border-t align-top">
        <td class="p-2"><code>{{ row.network || (row.user_id ? `UID ${row.user_id}` : t('ipAudit.unavailable')) }}</code><p v-if="row.actor_id">{{ t('ipAudit.operator') }} #{{ row.actor_id }}</p></td>
        <td class="p-2">{{ sourceName(row.origin_source || row.source) }}<p>{{ label(row.scope) }}</p><p v-if="row.manual">{{ t('ipAudit.manual') }}</p><p>{{ label(row.duration || 'unknown') }}</p></td>
        <td class="p-2">{{ label(row.reason) }}</td><td class="p-2">{{ label(row.state) }}<p>{{ label(row.effective) }}</p><p v-if="row.result">{{ label(row.result) }}</p></td>
        <td class="p-2">{{ row.count ?? t('ipAudit.unavailable') }} / {{ row.threshold ?? t('ipAudit.unavailable') }}</td>
        <td class="whitespace-nowrap p-2">{{ date(row.started_at) }}<p>{{ date(row.expires_at) }}</p></td>
        <td class="p-2"><button v-if="row.can_release" type="button" class="btn btn-secondary btn-sm" :disabled="releasing" data-testid="release-source" @click="confirmEntry = row">{{ t('ipAudit.release') }}</button><span v-else>{{ t('ipAudit.readOnly') }}</span></td>
      </tr></tbody>
    </table><p v-if="!rows.length" class="p-4 text-sm text-gray-500">{{ t('ipAudit.empty') }}</p></div>
    <div class="flex justify-between"><button class="btn btn-secondary btn-sm" :disabled="loading || releasing || !previous.length" @click="previousPage">{{ t('ipAudit.previous') }}</button><button class="btn btn-secondary btn-sm" :disabled="loading || releasing || !hasMore" data-testid="audit-next" @click="nextPage">{{ t('ipAudit.next') }}</button></div>
    <div v-if="confirmEntry" role="dialog" aria-modal="true" :aria-label="t('ipAudit.confirmTitle')" class="space-y-3 rounded-sm border border-amber-300 p-4" data-testid="release-dialog">
      <p>{{ t('ipAudit.confirmTitle') }} · {{ sourceName(confirmEntry.source) }} · <code>{{ confirmEntry.network || `UID ${confirmEntry.user_id}` }}</code></p>
      <p class="text-sm">{{ t(confirmEntry.scope === 'all_user_states' ? 'ipAudit.confirmUserScope' : 'ipAudit.confirmHint') }}</p>
      <label class="block text-sm">{{ t('ipAudit.reason') }}<select v-model="reason" class="input ml-2"><option v-for="r in reasons" :key="r" :value="r">{{ label(r) }}</option></select></label>
      <button type="button" class="btn btn-primary btn-sm" :disabled="releasing" data-testid="release-confirm" @click="release">{{ t('ipAudit.confirm') }}</button>
      <button type="button" class="btn btn-secondary btn-sm ml-2" :disabled="releasing" @click="confirmEntry = null">{{ t('common.cancel') }}</button>
    </div>
    <div v-if="outcome" role="status" class="space-y-2 rounded-sm border p-4" data-testid="release-outcome">
      <p>{{ t(outcome.readback === 'released' ? 'ipAudit.released' : outcome.readback === 'still_present' ? 'ipAudit.stillRestricted' : 'ipAudit.unverified') }}</p>
      <p>{{ t('ipAudit.notUnrestricted') }}</p>
      <p v-if="outcome.remaining.length">{{ t('ipAudit.remaining') }}: {{ outcome.remaining.map(r => `${sourceName(r.source)} (${r.network})`).join(', ') }}</p>
      <p v-if="outcome.unknown_sources.length">{{ t('ipAudit.unchecked') }}: {{ outcome.unknown_sources.map(sourceName).join(', ') }}</p>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listIPAudit, releaseIPAudit, type AuditEntry, type AuditQuery, type AuditSource, type ReleaseResult } from './api'
defineEmits<{ configure: [] }>()
import zh from './locale.zh'
import en from './locale.en'
const {t,te}=useI18n({useScope:'local',messages:{zh:{ipAudit:zh},en:{ipAudit:en}},inheritLocale:true})
const sourceName = (id: string) => t(`ipAudit.sources.${id}`)
const label = (value: string) => te(`ipAudit.labels.${value}`) ? t(`ipAudit.labels.${value}`) : value || t('ipAudit.unavailable')
const source = ref('anonymous_api'), ip = ref(''), state = ref(''), sort = ref('expires_desc'), start = ref(''), end = ref(''), userId = ref('')
const inventory = ref<AuditSource[]>([]), rows = ref<AuditEntry[]>([]), hasMore = ref(false), partial = ref(false)
const loading = ref(false), releasing = ref(false), error = ref(''), confirmEntry = ref<AuditEntry | null>(null), outcome = ref<ReleaseResult | null>(null)
const reason = ref('operator_review'), reasons = ['operator_review', 'false_positive', 'configuration_fix', 'support_request']
const states = ['active', 'disabled', 'expired', 'observed', 'historical', 'allowlisted']
const applied = ref<AuditQuery>({ source: 'anonymous_api', cursor: 0, limit: 20 }), previous = ref<number[]>([]), nextCursor = ref(0)
const selectedSource = computed(() => inventory.value.find(s => s.id === source.value))
const exactSource = computed(() => source.value === 'login_failures' || source.value === 'login_limiter')
let controller: AbortController | undefined, disposed = false
const date = (value: number | null) => value == null ? t('ipAudit.unavailable') : new Date(value * 1000).toLocaleString()
const timestamp = (value: string) => value ? Math.floor(new Date(value).getTime() / 1000) : undefined
async function load() {
  controller?.abort(); const current = new AbortController(); controller = current
  loading.value = true; error.value = ''; rows.value = []; hasMore.value = false; confirmEntry.value = null
  try {
    const result = await listIPAudit({ ...applied.value }, current.signal)
    if (current.signal.aborted || disposed) return
    rows.value = result.items; inventory.value = result.sources; hasMore.value = result.has_more; nextCursor.value = result.next_cursor; partial.value = !!result.partial
  } catch { if (!current.signal.aborted && !disposed) error.value = t('ipAudit.loadError') }
  finally { if (controller === current && !disposed) loading.value = false }
}
function sourceChanged() { state.value = ''; ip.value = ''; sort.value = 'expires_desc'; outcome.value = null; apply() }
function apply() {
  previous.value = []; outcome.value = null
  applied.value = { source: source.value, ip: ip.value.trim() || undefined, state: state.value || undefined, sort: sort.value, start: timestamp(start.value), end: timestamp(end.value), user_id: userId.value ? Number(userId.value) : undefined, cursor: 0, limit: 20 }
  void load()
}
function refresh() { previous.value = []; applied.value.cursor = 0; void load() }
function nextPage() { previous.value.push(applied.value.cursor); applied.value.cursor = nextCursor.value; void load() }
function previousPage() { applied.value.cursor = previous.value.pop() ?? 0; void load() }
async function release() {
  const entry = confirmEntry.value; if (!entry || releasing.value) return
  releasing.value = true; error.value = ''; outcome.value = null
  try {
    const result = await releaseIPAudit(entry, reason.value)
    if (disposed) return
    outcome.value = result; confirmEntry.value = null
    await load()
  } catch {
    if (!disposed) { await load(); error.value = t('ipAudit.releaseError'); confirmEntry.value = null }
  }
  finally { if (!disposed) releasing.value = false }
}
onMounted(load)
onUnmounted(() => { disposed = true; controller?.abort() })
</script>
