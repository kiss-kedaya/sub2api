<template>
  <details id="ip-risk-settings" class="card" :open="initiallyOpen" data-testid="ip-risk-settings" @toggle="onToggle">
    <summary class="cursor-pointer px-6 py-4 font-semibold">{{ tr('title') }}</summary>
    <div class="space-y-5 border-t border-gray-100 p-6 dark:border-dark-700">
      <p class="text-sm text-gray-500">{{ tr('description') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-green-600">{{ notice }}</p>
      <div class="flex flex-wrap items-center gap-3">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ tr('load') }}</button>
        <span v-if="snapshot" class="text-xs text-gray-500">{{ tr('currentIP') }}: {{ snapshot.current_ip }} · Redis: {{ snapshot.health.redis_available ? tr('available') : tr('unavailable') }}</span>
      </div>
      <template v-if="draft && snapshot">
        <p v-if="snapshot.health.emergency_disabled" class="rounded bg-amber-50 p-3 text-sm text-amber-800">{{ tr('emergencyActive') }}</p>
        <p v-if="!snapshot.trusted_proxies_configured" class="rounded bg-amber-50 p-3 text-sm text-amber-800">{{ tr('proxyWarning') }}</p>
        <p v-if="snapshot.health.config_error" class="text-sm text-amber-700">{{ tr('staleConfig') }}</p>
        <form class="space-y-5" @submit.prevent="save" @invalid.capture="revealInvalidField">
          <fieldset :disabled="busy" class="space-y-5">
            <div class="flex flex-wrap gap-5">
              <label class="flex items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" data-testid="risk-enabled" />{{ tr('enabled') }}</label>
              <label class="flex items-center gap-2 text-sm"><input v-model="draft.observe_only" type="checkbox" data-testid="risk-observe" />{{ tr('observeOnly') }}</label>
            </div>
            <p class="text-xs text-gray-500">{{ tr('scopeHint') }}</p>
            <section class="grid gap-4 md:grid-cols-2">
              <label class="block text-sm">{{ tr('whitelist') }}
                <textarea v-model="whitelist" rows="4" class="input mt-2 w-full font-mono" placeholder="203.0.113.10&#10;2001:db8::/64" data-testid="risk-whitelist" />
                <span class="mt-1 block text-xs text-gray-500">{{ tr('whitelistHint') }}</span>
              </label>
              <div class="space-y-2 text-sm">
                <p>{{ tr('trustedIP') }}</p>
                <p class="text-xs text-gray-500">{{ tr('trustedIPHint') }}</p>
                <button type="button" class="btn btn-secondary btn-sm" data-testid="risk-whitelist-current" @click="addCurrentIP">{{ tr('whitelistCurrent') }}</button>
              </div>
            </section>
            <section class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-testid="risk-blacklist-summary">
              <div class="space-y-1">
                <h3 class="font-medium">{{ tr('blacklist') }}</h3>
                <p class="text-xs text-gray-500">{{ draft.blacklist.length ? tr('ruleCount', { count: draft.blacklist.length }) : tr('noRules') }}</p>
              </div>
              <button type="button" class="btn btn-secondary btn-sm" data-testid="risk-manage-blacklist" @click="showBlacklist = true">{{ tr('manageBlacklist') }}</button>
            </section>
            <div class="space-y-3">
              <RiskSettingsSection :title="tr('countryRules')" data-testid="risk-country-settings">
                <label class="block text-sm">{{ tr('allowedCountries') }}<input v-model="allowedCountries" class="input mt-2 w-full" placeholder="CN" data-testid="risk-allowed-countries" /></label>
                <button type="button" class="btn btn-secondary btn-sm" data-testid="risk-china-only" @click="allowChinaOnly">{{ tr('chinaOnly') }}</button>
                <p class="text-xs text-gray-500">{{ tr('allowedCountriesHint') }}</p>
                <label class="block text-sm">{{ tr('countries') }}<input v-model="countries" class="input mt-2 w-full" placeholder="US, JP" data-testid="risk-countries" /></label>
                <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" @click="addCountry('US')">{{ tr('unitedStates') }}</button><button type="button" class="btn btn-secondary btn-sm" @click="addCountry('JP')">{{ tr('japan') }}</button></div>
                <label class="flex items-center gap-2 text-sm"><input v-model="draft.block_unknown_country" type="checkbox" />{{ tr('unknownCountry') }}</label>
                <p class="text-xs text-gray-500">{{ tr('geoHint') }}</p>
              </RiskSettingsSection>
              <RiskSettingsSection :title="tr('uaRules')" data-testid="risk-ua-settings">
                <label class="block text-sm">{{ tr('userAgents') }}<textarea v-model="userAgents" rows="4" class="input mt-2 w-full font-mono" data-testid="risk-user-agents" /></label>
                <label class="flex items-center gap-2 text-sm"><input v-model="draft.block_empty_ua" type="checkbox" />{{ tr('emptyUA') }}</label>
                <p class="text-xs text-gray-500">{{ tr('uaHint') }}</p>
              </RiskSettingsSection>
            </div>
            <RiskSettingsSection :title="tr('automatic')" data-testid="risk-automatic-settings">
              <p class="text-xs text-gray-500">{{ tr('counterHint') }}</p>
              <div v-for="key in counterKeys" :key="key" class="grid items-end gap-3 sm:grid-cols-3">
                <span class="text-sm">{{ tr(key) }}</span>
                <label class="text-xs text-gray-500">{{ tr('limit') }}<input v-model.number="draft[key].limit" type="number" min="0" max="100000" step="1" required class="input mt-1 w-full" :data-testid="`risk-${key}-limit`" /></label>
                <label class="text-xs text-gray-500">{{ tr('window') }}<input v-model.number="draft[key].window_seconds" type="number" min="1" max="86400" step="1" required class="input mt-1 w-full" /></label>
              </div>
              <div class="grid gap-3 sm:grid-cols-3">
                <label class="text-sm">{{ tr('banDuration') }}<input v-model.number="draft.ban_seconds" type="number" min="60" max="604800" step="1" required class="input mt-1 w-full" /></label>
                <label class="text-sm">{{ tr('ipv6Prefix') }}<input v-model.number="draft.ipv6_prefix" type="number" min="64" max="128" step="1" required class="input mt-1 w-full" /></label>
                <label class="text-sm">{{ tr('retention') }}<input v-model.number="draft.event_retention_hours" type="number" min="1" max="720" step="1" required class="input mt-1 w-full" /></label>
              </div>
            </RiskSettingsSection>
            <RiskSettingsSection :title="tr('anonymousTitle')" :status="draft.anonymous_api.enabled ? tr('protectionEnabled') : tr('protectionDisabled')" data-testid="anonymous-settings">
              <p class="text-xs text-gray-500">{{ tr('anonymousHint') }}</p>
              <label class="flex items-center gap-2 text-sm"><input v-model="draft.anonymous_api.enabled" type="checkbox" data-testid="anonymous-enabled" />{{ tr('anonymousEnabled') }}</label>
              <div class="grid gap-3 sm:grid-cols-2">
                <label class="text-sm">{{ tr('limit') }}<input v-model.number="draft.anonymous_api.threshold" type="number" min="2" max="1000" required class="input mt-1 w-full" data-testid="anonymous-threshold" /></label>
                <label class="text-sm">{{ tr('window') }}<input v-model.number="draft.anonymous_api.window_seconds" type="number" min="1" max="3600" required class="input mt-1 w-full" /></label>
                <label class="text-sm">{{ tr('banDuration') }}<input v-model.number="draft.anonymous_api.ban_seconds" type="number" min="1" max="86400" required class="input mt-1 w-full" /></label>
                <label class="text-sm">{{ tr('anonymousCapacity') }}<input v-model.number="draft.anonymous_api.max_tracked_ips" type="number" min="100" max="50000" required class="input mt-1 w-full" /></label>
              </div>
              <label class="flex items-center gap-2 text-sm"><input v-model="draft.anonymous_api.reset_on_success" type="checkbox" />{{ tr('anonymousReset') }}</label>
              <p class="text-xs text-gray-500">{{ tr('anonymousFailurePolicy') }}</p>
            </RiskSettingsSection>
            <button type="submit" class="btn btn-primary btn-sm" data-testid="risk-save">{{ tr('save') }}</button>
          </fieldset>
        </form>
        <BlacklistDialog :show="showBlacklist" :blacklist="draft.blacklist" @close="showBlacklist = false" @apply="applyBlacklist" />
        <RiskSettingsSection :title="tr('anonymousBans')" :status="tr('banCount', { count: anonymousBans.length })" data-testid="anonymous-bans">
          <div class="flex justify-end"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="refreshRecords">{{ tr('refreshRecords') }}</button></div>
          <p class="text-xs text-gray-500">{{ tr('anonymousUnbanHint') }}</p>
          <div class="flex flex-wrap items-center gap-2">
            <input v-model="anonymousReleaseIP" class="input" :placeholder="tr('anonymousExactIP')" :aria-label="tr('anonymousExactIP')" data-testid="anonymous-release-ip" />
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !anonymousReleaseIP.trim()" data-testid="anonymous-release-manual" @click="unbanAnonymous(anonymousReleaseIP.trim())">{{ tr('anonymousUnban') }}</button>
          </div>
          <div v-for="ban in anonymousBans" :key="ban.network" class="flex flex-wrap items-center justify-between gap-2 rounded border p-3 text-sm">
            <span><code>{{ ban.network }}</code> · {{ label(ban.reason) }} · {{ tr('expiresAt') }} {{ date(ban.expires_at) }}</span>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-testid="anonymous-unban" @click="unbanAnonymous(ban.network)">{{ tr('anonymousUnban') }}</button>
          </div>
          <p v-if="!anonymousBans.length" class="text-xs text-gray-500">{{ tr('noBans') }}</p>
        </RiskSettingsSection>
        <RiskSettingsSection :title="tr('checkTitle')" data-testid="risk-check-settings">
          <p class="text-xs text-gray-500">{{ tr('checkHint') }}</p>
          <div class="flex flex-wrap items-center gap-2">
            <input v-model="checkIP" class="input" :placeholder="tr('network')" :aria-label="tr('checkIP')" data-testid="risk-check-ip" />
            <input v-model="checkUA" class="input" placeholder="User-Agent" aria-label="User-Agent" />
            <label class="flex items-center gap-2 text-sm"><input v-model="checkAuth" type="checkbox" />{{ tr('authRequest') }}</label>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !checkIP.trim()" data-testid="risk-check" @click="check">{{ tr('check') }}</button>
          </div>
          <p v-if="checkResult" role="status" class="text-sm">{{ label(checkResult.decision) }} · {{ label(checkResult.reason) }} · {{ checkResult.country || '—' }} · {{ checkResult.network }}</p>
        </RiskSettingsSection>
        <RiskSettingsSection :title="tr('bans')" :status="tr('banCount', { count: bans.length })" data-testid="risk-bans">
          <div class="flex justify-end"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="refreshRecords">{{ tr('refreshRecords') }}</button></div>
          <p class="text-xs text-gray-500">{{ tr('bansHint') }}</p>
          <div v-for="ban in bans" :key="`${ban.observe_only}-${ban.network}`" class="flex flex-wrap items-center justify-between gap-2 rounded border border-gray-200 p-3 text-sm dark:border-dark-700">
            <span><code>{{ ban.network }}</code> · {{ label(ban.reason) }} · {{ ban.observe_only ? tr('observeOnly') : tr('enforced') }} · {{ tr('expiresAt') }} {{ date(ban.expires_at) }}</span>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="unban(ban.network)">{{ tr('unban') }}</button>
          </div>
          <p v-if="!bans.length" class="text-xs text-gray-500">{{ tr('noBans') }}</p>
        </RiskSettingsSection>
        <section class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <h3 class="font-medium">{{ tr('events') }}</h3>
          <p class="text-xs text-gray-500">{{ tr('eventsHint') }}</p>
          <div class="flex flex-wrap items-center gap-2">
            <input v-model="filterIP" class="input" :placeholder="tr('filterIP')" :aria-label="tr('filterIP')" />
            <select v-model="filterCategory" class="input" :aria-label="tr('category')" data-testid="risk-category"><option value="">{{ tr('allCategories') }}</option><option v-for="category in ['login', 'register', 'verification']" :key="category" :value="category">{{ label(category) }}</option></select>
            <select v-model="filterReason" class="input" :aria-label="tr('filterReason')"><option value="">{{ tr('allReasons') }}</option><option v-for="reason in reasons" :key="reason" :value="reason">{{ label(reason) }}</option></select>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="refreshRecords">{{ tr('filter') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !events.length" @click="confirmClear = !confirmClear">{{ tr('clearEvents') }}</button>
          </div>
          <div v-if="confirmClear" class="flex items-center gap-2 text-sm"><span>{{ tr('clearConfirm') }}</span><button type="button" class="btn btn-danger btn-sm" :disabled="busy" @click="clearEvents">{{ tr('confirm') }}</button><button type="button" class="btn btn-secondary btn-sm" @click="confirmClear = false">{{ tr('cancel') }}</button></div>
          <div class="overflow-x-auto"><table class="w-full text-left text-xs">
            <thead><tr><th class="p-2">{{ tr('time') }}</th><th class="p-2">IP / {{ tr('source') }}</th><th class="p-2">{{ tr('request') }}</th><th class="p-2">{{ tr('result') }}</th><th class="p-2">User-Agent</th></tr></thead>
            <tbody><tr v-for="event in events" :key="event.id" class="border-t border-gray-100 align-top dark:border-dark-700">
              <td class="whitespace-nowrap p-2">{{ date(event.at) }}</td>
              <td class="p-2"><code>{{ event.ip }}</code><p>{{ event.country || '—' }} · {{ label(event.ip_source) }}</p><p class="text-gray-500">{{ tr('peer') }}: {{ event.peer }}</p></td>
              <td class="p-2">{{ event.method }} {{ event.path }}<p>{{ label(event.category) }} · {{ event.status }}</p></td>
              <td class="p-2">{{ label(event.decision) }}<p>{{ label(event.reason) }}</p><p class="text-gray-500">{{ event.error_reason }}</p></td>
              <td class="max-w-xs break-all p-2">{{ event.user_agent || '—' }}</td>
            </tr></tbody>
          </table></div>
          <p v-if="!events.length" class="text-xs text-gray-500">{{ tr('noEvents') }}</p>
          <fieldset v-if="eventsTotal > 0" :disabled="busy" :aria-busy="busy" class="min-w-0">
            <Pagination
              :total="eventsTotal"
              :page="eventsPage"
              :page-size="eventsPageSize"
              show-jump
              @update:page="changeEventsPage"
              @update:page-size="changeEventsPageSize"
            />
          </fieldset>
        </section>
        <details class="text-sm"><summary class="cursor-pointer">{{ tr('recovery') }}</summary><p class="mt-2 text-xs text-gray-500">{{ tr('recoveryHint') }}</p></details>
      </template>
    </div>
  </details>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, onDeactivated, ref, toRaw } from 'vue'
import { useI18n } from 'vue-i18n'
import Pagination from '@/components/common/Pagination.vue'
import BlacklistDialog from './BlacklistDialog.vue'
import RiskSettingsSection from './RiskSettingsSection.vue'
import { ipRiskAPI, type IPBan, type IPBlockRule, type IPRiskConfig, type IPRiskEvent, type IPRiskSnapshot } from './api'

const props = defineProps<{ expanded?: boolean }>()
const initiallyOpen = props.expanded || (typeof window !== 'undefined' && window.location.hash === '#ip-risk-settings')
import zh from './locale.zh'
import en from './locale.en'
const {t,te}=useI18n({useScope:'local',messages:{zh:{admin:{settings:{ipRisk:zh}}},en:{admin:{settings:{ipRisk:en}}}},inheritLocale:true})
const tr = (key: string, values: Record<string, number> = {}) => t(`admin.settings.ipRisk.${key}`, values)
const label = (key: string) => key && te(`admin.settings.ipRisk.labels.${key}`) ? tr(`labels.${key}`) : key || '—'
const snapshot = ref<IPRiskSnapshot | null>(null), draft = ref<IPRiskConfig | null>(null)
const busy = ref(false), error = ref(''), notice = ref(''), confirmClear = ref(false)
const whitelist = ref(''), countries = ref(''), allowedCountries = ref(''), userAgents = ref('')
const events = ref<IPRiskEvent[]>([]), bans = ref<IPBan[]>([]), anonymousBans = ref<IPBan[]>([])
const eventsPage = ref(1), eventsPageSize = ref(20), eventsTotal = ref(0)
let appliedEventFilters = { ip: '', reason: '', category: '' }
const filterIP = ref(''), filterReason = ref(''), filterCategory = ref(''), checkIP = ref(''), checkUA = ref(''), checkAuth = ref(true)
const checkResult = ref<IPRiskEvent | null>(null)
const anonymousReleaseIP = ref('')
const showBlacklist = ref(false)
onDeactivated(() => { showBlacklist.value = false })
const counterKeys = ['login_attempts', 'login_failures', 'register_attempts', 'register_failures', 'verification_attempts'] as const
const reasons = [...counterKeys, 'ip_blacklist', 'country_blocked', 'country_not_allowed', 'country_unknown', 'user_agent_blocked', 'empty_user_agent', 'invalid_client_ip', 'risk_store_unavailable']
let disposed = false
onBeforeUnmount(() => { disposed = true })
onMounted(() => { if (initiallyOpen) void load() })
const lines = (value: string) => [...new Set(value.split(/[\n,]+/).map(v => v.trim()).filter(Boolean))]
const date = (seconds: number) => new Date(seconds * 1000).toLocaleString()
function revealInvalidField(event: Event) {
  let details = (event.target as HTMLElement).closest('details')
  while (details) {
    details.open = true
    details = details.parentElement?.closest('details') ?? null
  }
}
function applyBlacklist(rules: IPBlockRule[]) {
  if (!draft.value) return
  draft.value.blacklist = rules
  showBlacklist.value = false
  notice.value = tr('blacklistApplied')
}
async function action(fn: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await fn() } catch (cause: unknown) {
    if (!disposed) {
      const detail = cause as { response?: { data?: { message?: string } }; message?: string }
      error.value = detail.response?.data?.message || detail.message || tr('failed')
    }
  } finally { if (!disposed) busy.value = false }
}
function applyConfig(config: IPRiskConfig) {
  draft.value = structuredClone(config)
  whitelist.value = config.whitelist.join('\n'); countries.value = config.blocked_countries.join(', '); allowedCountries.value = (config.allowed_countries || []).join(', '); userAgents.value = config.deny_user_agents.join('\n')
}
async function loadEvents(page = eventsPage.value, pageSize = eventsPageSize.value, filters = appliedEventFilters) {
  const result = await ipRiskAPI.events(filters.ip, filters.reason, filters.category, page, pageSize)
  if (disposed) return
  events.value = result.items
  eventsTotal.value = result.total
  eventsPage.value = result.page
  eventsPageSize.value = result.page_size
  appliedEventFilters = filters
}
async function changeEventsPage(page: number) { await action(() => loadEvents(page)) }
async function changeEventsPageSize(pageSize: number) { await action(() => loadEvents(1, pageSize)) }
async function records() {
  const filters = { ip: filterIP.value.trim(), reason: filterReason.value, category: filterCategory.value }
  const result = await Promise.allSettled([loadEvents(1, eventsPageSize.value, filters), ipRiskAPI.bans(), ipRiskAPI.anonymousBans()])
  if (disposed) return
  if (result[1].status === 'fulfilled') bans.value = result[1].value
  if (result[2].status === 'fulfilled') anonymousBans.value = result[2].value
  const failed = result.find(item => item.status === 'rejected')
  if (failed?.status === 'rejected') throw failed.reason
}
async function load() {
  await action(async () => {
    const result = await ipRiskAPI.get()
    if (disposed) return
    snapshot.value = result; applyConfig(result.config)
    checkIP.value ||= result.current_ip
    await records()
  })
}
function onToggle(event: Event) { if ((event.target as HTMLDetailsElement).open && !snapshot.value) void load() }
function addCurrentIP() { if (snapshot.value) whitelist.value = [...new Set([...lines(whitelist.value), snapshot.value.current_ip])].join('\n') }
function addCountry(code: string) { countries.value = [...new Set([...lines(countries.value).map(v => v.toUpperCase()), code])].join(', ') }
function allowChinaOnly() { allowedCountries.value = 'CN'; countries.value = lines(countries.value).map(v => v.toUpperCase()).filter(v => v !== 'CN').join(', ') }
async function save() {
  if (!draft.value) return
  await action(async () => {
    const payload = { ...structuredClone(toRaw(draft.value!)), whitelist: lines(whitelist.value), allowed_countries: lines(allowedCountries.value).map(v => v.toUpperCase()), blocked_countries: lines(countries.value).map(v => v.toUpperCase()), deny_user_agents: lines(userAgents.value) }
    const result = await ipRiskAPI.save(payload)
    if (disposed) return
    applyConfig(result); notice.value = tr('saved'); checkResult.value = null
  })
}
async function refreshRecords() { await action(records) }
async function unban(network: string) { await action(async () => { await ipRiskAPI.unban(network); await records(); if (!disposed) notice.value = tr('unbanned') }) }
async function unbanAnonymous(network: string) { await action(async () => { await ipRiskAPI.unbanAnonymous(network); await records(); if (!disposed) notice.value = tr('unbanned') }) }
async function clearEvents() { await action(async () => { await ipRiskAPI.clearEvents(); if (!disposed) { events.value = []; eventsTotal.value = 0; eventsPage.value = 1; confirmClear.value = false; notice.value = tr('cleared') } }) }
async function check() { await action(async () => { const result = await ipRiskAPI.check(checkIP.value.trim(), checkUA.value, checkAuth.value); if (!disposed) checkResult.value = result }) }
</script>
