<template>
  <div id="ip-risk-settings" class="mx-auto w-full max-w-4xl" data-testid="ip-risk-settings">
    <UCollapsible v-model:open="open" :unmount-on-hide="false">
      <!-- Embedded use keeps the collapsed card; on its own page (`expanded`) there is no trigger. -->
      <template v-if="!expanded" #default>
        <UButton
          color="neutral"
          variant="ghost"
          block
          trailing-icon="i-lucide-chevron-down"
          class="px-0 py-3 text-base font-semibold text-highlighted hover:bg-transparent active:bg-transparent"
          :ui="{ trailingIcon: ['transition-transform duration-200', open ? 'rotate-180' : ''] }"
        >
          <span class="flex-1 text-left">{{ tr('title') }}</span>
        </UButton>
      </template>
      <template #content>
        <div class="flex flex-col gap-10 pt-1 pb-8">
          <section class="flex flex-col gap-4">
            <UPageCard :title="tr('title')" :description="tr('description')" variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
              <div class="flex flex-wrap items-center gap-2 lg:ms-auto">
                <UButton :label="tr('load')" icon="i-lucide-refresh-cw" color="neutral" variant="outline" :disabled="busy" @click="load" />
                <UButton v-if="draft && snapshot" :label="tr('save')" type="submit" form="ip-risk-form" :disabled="busy" data-testid="risk-save" />
              </div>
            </UPageCard>
            <div v-if="snapshot" class="flex flex-wrap items-center gap-2 text-sm">
              <UBadge color="neutral" variant="subtle" icon="i-lucide-globe">{{ tr('currentIP') }}: {{ snapshot.current_ip }}</UBadge>
              <UBadge :color="snapshot.health.redis_available ? 'success' : 'error'" variant="subtle" icon="i-lucide-database">Redis: {{ snapshot.health.redis_available ? tr('available') : tr('unavailable') }}</UBadge>
            </div>
            <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
            <UAlert v-if="notice" role="status" color="success" variant="subtle" icon="i-lucide-circle-check" :description="notice" />
            <template v-if="draft && snapshot">
              <UAlert v-if="snapshot.health.emergency_disabled" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="tr('emergencyActive')" />
              <UAlert v-if="!snapshot.trusted_proxies_configured" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="tr('proxyWarning')" />
              <UAlert v-if="snapshot.health.config_error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="tr('staleConfig')" />
            </template>
          </section>
          <template v-if="draft && snapshot">
            <form id="ip-risk-form" @submit.prevent="save" @invalid.capture="revealInvalidField">
              <fieldset :disabled="busy" class="flex min-w-0 flex-col gap-6">
                <UPageCard variant="subtle">
                  <UFormField :label="tr('enabled')" class="flex items-center justify-between gap-4">
                    <template #hint><HelpPopover :text="tr('scopeHint')" :label="tr('moreInfo')" /></template>
                    <USwitch v-model="draft.enabled" data-testid="risk-enabled" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="tr('observeOnly')" class="flex items-center justify-between gap-4">
                    <USwitch v-model="draft.observe_only" data-testid="risk-observe" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="tr('whitelist')" :description="tr('whitelistHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
                    <UTextarea v-model="whitelist" :rows="4" autoresize class="w-full sm:w-72" :ui="{ base: 'font-mono' }" placeholder="203.0.113.10&#10;2001:db8::/64" data-testid="risk-whitelist" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="tr('trustedIP')" :description="tr('trustedIPHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
                    <UButton :label="tr('whitelistCurrent')" icon="i-lucide-plus" color="neutral" variant="outline" class="shrink-0" data-testid="risk-whitelist-current" @click="addCurrentIP" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="tr('blacklist')" :description="draft.blacklist.length ? tr('ruleCount', { count: draft.blacklist.length }) : tr('noRules')" class="flex max-sm:flex-col justify-between items-start gap-4" data-testid="risk-blacklist-summary">
                    <UButton :label="tr('manageBlacklist')" icon="i-lucide-list" color="neutral" variant="outline" class="shrink-0" data-testid="risk-manage-blacklist" @click="showBlacklist = true" />
                  </UFormField>
                </UPageCard>

                <UPageCard variant="subtle" :ui="{ container: 'gap-y-2 sm:py-4' }">
                  <RiskSettingsSection :title="tr('countryRules')" data-testid="risk-country-settings">
                    <UFormField :label="tr('allowedCountries')" class="flex max-sm:flex-col justify-between items-start gap-4">
                      <template #hint><HelpPopover :text="tr('allowedCountriesHint')" :label="tr('moreInfo')" /></template>
                      <div class="flex shrink-0 flex-wrap items-center gap-2">
                        <UInput v-model="allowedCountries" placeholder="CN" class="w-40" data-testid="risk-allowed-countries" />
                        <UButton :label="tr('chinaOnly')" color="neutral" variant="outline" data-testid="risk-china-only" @click="allowChinaOnly" />
                      </div>
                    </UFormField>
                    <UFormField :label="tr('countries')" class="flex max-sm:flex-col justify-between items-start gap-4">
                      <div class="flex shrink-0 flex-wrap items-center gap-2">
                        <UInput v-model="countries" placeholder="US, JP" class="w-40" data-testid="risk-countries" />
                        <UButton :label="tr('unitedStates')" color="neutral" variant="outline" @click="addCountry('US')" />
                        <UButton :label="tr('japan')" color="neutral" variant="outline" @click="addCountry('JP')" />
                      </div>
                    </UFormField>
                    <UFormField :label="tr('unknownCountry')" class="flex items-center justify-between gap-4">
                      <template #hint><HelpPopover :text="tr('geoHint')" :label="tr('moreInfo')" /></template>
                      <USwitch v-model="draft.block_unknown_country" />
                    </UFormField>
                  </RiskSettingsSection>
                  <USeparator />
                  <RiskSettingsSection :title="tr('uaRules')" data-testid="risk-ua-settings">
                    <UFormField :label="tr('userAgents')" :description="tr('uaHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
                      <UTextarea v-model="userAgents" :rows="4" autoresize class="w-full sm:w-72" :ui="{ base: 'font-mono' }" data-testid="risk-user-agents" />
                    </UFormField>
                    <UFormField :label="tr('emptyUA')" class="flex items-center justify-between gap-4">
                      <USwitch v-model="draft.block_empty_ua" />
                    </UFormField>
                  </RiskSettingsSection>
                  <USeparator />
                  <RiskSettingsSection :title="tr('automatic')" data-testid="risk-automatic-settings">
                    <p class="text-sm text-muted">{{ tr('counterHint') }}</p>
                    <div class="grid gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_9rem_9rem] sm:items-center">
                      <span class="hidden text-xs text-muted sm:block" aria-hidden="true" />
                      <span class="hidden text-xs font-medium text-muted sm:block" aria-hidden="true">{{ tr('limit') }}</span>
                      <span class="hidden text-xs font-medium text-muted sm:block" aria-hidden="true">{{ tr('window') }}</span>
                      <template v-for="key in counterKeys" :key="key">
                        <span class="text-sm font-medium text-default max-sm:pt-2">{{ tr(key) }}</span>
                        <label class="flex flex-col gap-1 text-xs text-muted">
                          <span class="sm:sr-only">{{ tr('limit') }}</span>
                          <UInput v-model.number="draft[key].limit" type="number" min="0" max="100000" step="1" required class="w-full" :aria-label="`${tr(key)} · ${tr('limit')}`" :data-testid="`risk-${key}-limit`" />
                        </label>
                        <label class="flex flex-col gap-1 text-xs text-muted">
                          <span class="sm:sr-only">{{ tr('window') }}</span>
                          <UInput v-model.number="draft[key].window_seconds" type="number" min="1" max="86400" step="1" required class="w-full" :aria-label="`${tr(key)} · ${tr('window')}`" />
                        </label>
                      </template>
                    </div>
                    <div class="grid gap-4 sm:grid-cols-3">
                      <UFormField :label="tr('banDuration')"><UInput v-model.number="draft.ban_seconds" type="number" min="60" max="604800" step="1" required class="w-full" /></UFormField>
                      <UFormField :label="tr('ipv6Prefix')"><UInput v-model.number="draft.ipv6_prefix" type="number" min="64" max="128" step="1" required class="w-full" /></UFormField>
                      <UFormField :label="tr('retention')"><UInput v-model.number="draft.event_retention_hours" type="number" min="1" max="720" step="1" required class="w-full" /></UFormField>
                    </div>
                  </RiskSettingsSection>
                  <USeparator />
                  <RiskSettingsSection :title="tr('anonymousTitle')" :status="draft.anonymous_api.enabled ? tr('protectionEnabled') : tr('protectionDisabled')" data-testid="anonymous-settings">
                    <UFormField :label="tr('anonymousEnabled')" class="flex items-center justify-between gap-4">
                      <template #hint><HelpPopover :text="tr('anonymousHint')" :label="tr('moreInfo')" /></template>
                      <USwitch v-model="draft.anonymous_api.enabled" data-testid="anonymous-enabled" />
                    </UFormField>
                    <div class="grid gap-4 sm:grid-cols-2">
                      <UFormField :label="tr('limit')"><UInput v-model.number="draft.anonymous_api.threshold" type="number" min="2" max="1000" required class="w-full" data-testid="anonymous-threshold" /></UFormField>
                      <UFormField :label="tr('window')"><UInput v-model.number="draft.anonymous_api.window_seconds" type="number" min="1" max="3600" required class="w-full" /></UFormField>
                      <UFormField :label="tr('banDuration')"><UInput v-model.number="draft.anonymous_api.ban_seconds" type="number" min="1" max="86400" required class="w-full" /></UFormField>
                      <UFormField :label="tr('anonymousCapacity')"><UInput v-model.number="draft.anonymous_api.max_tracked_ips" type="number" min="100" max="50000" required class="w-full" /></UFormField>
                    </div>
                    <UFormField :label="tr('anonymousReset')" class="flex items-center justify-between gap-4">
                      <template #hint><HelpPopover :text="tr('anonymousFailurePolicy')" :label="tr('moreInfo')" /></template>
                      <USwitch v-model="draft.anonymous_api.reset_on_success" />
                    </UFormField>
                  </RiskSettingsSection>
                </UPageCard>
              </fieldset>
            </form>
            <BlacklistDialog :show="showBlacklist" :blacklist="draft.blacklist" @close="showBlacklist = false" @apply="applyBlacklist" />

            <section class="flex flex-col gap-4">
              <UPageCard :title="tr('records')" variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
                <UButton :label="tr('refreshRecords')" icon="i-lucide-refresh-cw" color="neutral" variant="outline" class="w-fit lg:ms-auto" :disabled="busy" @click="refreshRecords" />
              </UPageCard>
              <UPageCard variant="subtle" :ui="{ container: 'gap-y-2 sm:py-4' }">
                <RiskSettingsSection :title="tr('anonymousBans')" :status="tr('banCount', { count: anonymousBans.length })" data-testid="anonymous-bans">
                  <p class="text-sm text-muted">{{ tr('anonymousUnbanHint') }}</p>
                  <div class="flex flex-wrap items-center gap-2">
                    <UInput v-model="anonymousReleaseIP" icon="i-lucide-search" class="w-full sm:w-80" :placeholder="tr('anonymousExactIP')" :aria-label="tr('anonymousExactIP')" data-testid="anonymous-release-ip" />
                    <UButton :label="tr('anonymousUnban')" icon="i-lucide-unlock" color="neutral" variant="outline" :disabled="busy || !anonymousReleaseIP.trim()" data-testid="anonymous-release-manual" @click="unbanAnonymous(anonymousReleaseIP.trim())" />
                  </div>
                  <UTable :data="anonymousBans" :columns="anonymousBanColumns" :empty="tr('noBans')" :ui="tableUi">
                    <template #network-cell="{ row }"><code class="text-default">{{ row.original.network }}</code></template>
                    <template #reason-cell="{ row }">{{ label(row.original.reason) }}</template>
                    <template #expires-cell="{ row }">{{ date(row.original.expires_at) }}</template>
                    <template #actions-cell="{ row }">
                      <div class="flex justify-end">
                        <UButton :label="tr('anonymousUnban')" icon="i-lucide-unlock" color="neutral" variant="ghost" size="sm" :disabled="busy" data-testid="anonymous-unban" @click="unbanAnonymous(row.original.network)" />
                      </div>
                    </template>
                  </UTable>
                </RiskSettingsSection>
                <USeparator />
                <RiskSettingsSection :title="tr('checkTitle')" data-testid="risk-check-settings">
                  <p class="text-sm text-muted">{{ tr('checkHint') }}</p>
                  <div class="flex flex-wrap items-center gap-2">
                    <UInput v-model="checkIP" class="w-full sm:w-52" :placeholder="tr('network')" :aria-label="tr('checkIP')" data-testid="risk-check-ip" />
                    <UInput v-model="checkUA" class="w-full sm:w-64" placeholder="User-Agent" aria-label="User-Agent" />
                    <UCheckbox v-model="checkAuth" :label="tr('authRequest')" />
                    <UButton :label="tr('check')" icon="i-lucide-flask-conical" color="neutral" variant="outline" :disabled="busy || !checkIP.trim()" data-testid="risk-check" @click="check" />
                  </div>
                  <div v-if="checkResult" role="status" class="flex flex-wrap items-center gap-2 text-sm text-muted">
                    <UBadge :color="decisionTone(checkResult.decision)" variant="subtle">{{ label(checkResult.decision) }}</UBadge>
                    <span>{{ label(checkResult.reason) }}</span>
                    <span aria-hidden="true">·</span>
                    <span>{{ checkResult.country || '—' }}</span>
                    <span aria-hidden="true">·</span>
                    <code class="text-default">{{ checkResult.network }}</code>
                  </div>
                </RiskSettingsSection>
                <USeparator />
                <RiskSettingsSection :title="tr('bans')" :status="tr('banCount', { count: bans.length })" data-testid="risk-bans">
                  <p class="text-sm text-muted">{{ tr('bansHint') }}</p>
                  <UTable :data="bans" :columns="banColumns" :empty="tr('noBans')" :ui="tableUi">
                    <template #network-cell="{ row }"><code class="text-default">{{ row.original.network }}</code></template>
                    <template #reason-cell="{ row }">{{ label(row.original.reason) }}</template>
                    <template #mode-cell="{ row }">
                      <UBadge :color="row.original.observe_only ? 'warning' : 'error'" variant="subtle">{{ row.original.observe_only ? tr('observeOnly') : tr('enforced') }}</UBadge>
                    </template>
                    <template #expires-cell="{ row }">{{ date(row.original.expires_at) }}</template>
                    <template #actions-cell="{ row }">
                      <div class="flex justify-end">
                        <UButton :label="tr('unban')" icon="i-lucide-unlock" color="neutral" variant="ghost" size="sm" :disabled="busy" @click="unban(row.original.network)" />
                      </div>
                    </template>
                  </UTable>
                </RiskSettingsSection>
              </UPageCard>
            </section>

            <section class="flex flex-col gap-4">
              <UPageCard variant="naked">
                <template #title>
                  <span class="inline-flex items-center gap-1">{{ tr('events') }}<HelpPopover :text="tr('eventsHint')" :label="tr('moreInfo')" /></span>
                </template>
              </UPageCard>
              <div class="flex flex-wrap items-center justify-between gap-1.5">
                <UInput v-model="filterIP" icon="i-lucide-search" class="w-full sm:max-w-xs" :placeholder="tr('filterIP')" :aria-label="tr('filterIP')" />
                <div class="flex flex-wrap items-center gap-1.5">
                  <USelect v-model="categoryFilter" :items="categoryItems" class="min-w-36" :aria-label="tr('category')" data-testid="risk-category" />
                  <USelect v-model="reasonFilter" :items="reasonItems" class="min-w-40" :aria-label="tr('filterReason')" />
                  <UButton :label="tr('filter')" icon="i-lucide-list-filter" color="neutral" variant="outline" :disabled="busy" data-testid="risk-filter" @click="refreshRecords" />
                  <UButton :label="tr('clearEvents')" icon="i-lucide-trash-2" color="error" variant="soft" :disabled="busy || !events.length" @click="confirmClear = !confirmClear" />
                </div>
              </div>
              <UAlert
                v-if="confirmClear"
                color="warning"
                variant="subtle"
                icon="i-lucide-triangle-alert"
                orientation="horizontal"
                :description="tr('clearConfirm')"
                :actions="[
                  { label: tr('confirm'), color: 'error', disabled: busy, onClick: clearEvents },
                  { label: tr('cancel'), color: 'neutral', variant: 'subtle', onClick: () => { confirmClear = false } }
                ]"
              />
              <UTable :data="events" :columns="eventColumns" :empty="tr('noEvents')" :ui="eventTableUi">
                <template #at-cell="{ row }">{{ date(row.original.at) }}</template>
                <template #ip-cell="{ row }">
                  <code class="text-default">{{ row.original.ip }}</code>
                  <p>{{ row.original.country || '—' }} · {{ label(row.original.ip_source) }}</p>
                  <p class="text-dimmed">{{ tr('peer') }}: {{ row.original.peer }}</p>
                </template>
                <template #request-cell="{ row }">
                  <span class="block max-w-48 whitespace-normal break-all text-default">{{ row.original.method }} {{ row.original.path }}</span>
                  <p>{{ label(row.original.category) }} · {{ row.original.status }}</p>
                </template>
                <template #result-cell="{ row }">
                  <UBadge :color="decisionTone(row.original.decision)" variant="subtle">{{ label(row.original.decision) }}</UBadge>
                  <p class="mt-1">{{ label(row.original.reason) }}</p>
                  <p v-if="row.original.error_reason" class="text-dimmed">{{ row.original.error_reason }}</p>
                </template>
                <template #user_agent-cell="{ row }"><span class="block min-w-40 max-w-xs whitespace-normal wrap-break-word">{{ row.original.user_agent || '—' }}</span></template>
              </UTable>
              <fieldset v-if="eventsTotal > 0" :disabled="busy" :aria-busy="busy" class="min-w-0 border-t border-default pt-4">
                <Pagination
                  :total="eventsTotal"
                  :page="eventsPage"
                  :page-size="eventsPageSize"
                  show-jump
                  @update:page="changeEventsPage"
                  @update:page-size="changeEventsPageSize"
                />
              </fieldset>
              <UPopover :content="{ align: 'start' }">
                <UButton :label="tr('recovery')" icon="i-lucide-life-buoy" color="neutral" variant="link" class="w-fit px-0" />
                <template #content><p class="max-w-md p-3 text-sm text-pretty text-muted">{{ tr('recoveryHint') }}</p></template>
              </UPopover>
            </section>
          </template>
        </div>
      </template>
    </UCollapsible>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, onDeactivated, ref, toRaw, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import Pagination from '@/components/common/Pagination.vue'
import BlacklistDialog from './BlacklistDialog.vue'
import HelpPopover from './HelpPopover.vue'
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
// USelect items cannot carry '' (reka-ui), so "all" is a sentinel mapped back to '' for the API.
const ALL = '__all__'
const categoryFilter = computed({ get: () => filterCategory.value || ALL, set: (value: string) => { filterCategory.value = value === ALL ? '' : value } })
const reasonFilter = computed({ get: () => filterReason.value || ALL, set: (value: string) => { filterReason.value = value === ALL ? '' : value } })
const categoryItems = computed(() => [{ label: tr('allCategories'), value: ALL }, ...['login', 'register', 'verification'].map(value => ({ label: label(value), value }))])
const reasonItems = computed(() => [{ label: tr('allReasons'), value: ALL }, ...reasons.map(value => ({ label: label(value), value }))])
const tableUi = {
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
  separator: 'h-0'
}
const eventTableUi = { ...tableUi, td: 'border-b border-default align-top' }
const anonymousBanColumns = computed<TableColumn<IPBan>[]>(() => [
  { id: 'network', header: tr('network') },
  { id: 'reason', header: tr('filterReason') },
  { id: 'expires', header: tr('expiresAt') },
  { id: 'actions', header: '' }
])
const banColumns = computed<TableColumn<IPBan>[]>(() => [
  { id: 'network', header: tr('network') },
  { id: 'reason', header: tr('filterReason') },
  { id: 'mode', header: tr('ruleStatus') },
  { id: 'expires', header: tr('expiresAt') },
  { id: 'actions', header: '' }
])
const eventColumns = computed<TableColumn<IPRiskEvent>[]>(() => [
  { id: 'at', header: tr('time') },
  { id: 'ip', header: `IP / ${tr('source')}` },
  { id: 'request', header: tr('request') },
  { id: 'result', header: tr('result') },
  { id: 'user_agent', header: 'User-Agent' }
])
type Tone = 'success' | 'error' | 'warning' | 'neutral'
const decisionTones: Record<string, Tone> = { allowed: 'success', whitelisted: 'success', blocked: 'error', auto_banned: 'error', would_block: 'warning', would_ban: 'warning', unavailable: 'warning', disabled: 'neutral' }
const decisionTone = (decision: string): Tone => decisionTones[decision] ?? 'neutral'
const open = ref(initiallyOpen)
watch(open, value => { if (value && !snapshot.value) void load() })
let disposed = false
onBeforeUnmount(() => { disposed = true })
onMounted(() => { if (initiallyOpen) void load() })
const lines = (value: string) => [...new Set(value.split(/[\n,]+/).map(v => v.trim()).filter(Boolean))]
const date = (seconds: number) => new Date(seconds * 1000).toLocaleString()
// A section opens itself on `invalid` (capture). The browser drops its validation bubble for a field that was
// hidden when it fired, so once the section has rendered open, focus the field and report it again.
let revealing = false
function revealInvalidField(event: Event) {
  const field = event.target as HTMLInputElement
  if (revealing || field.getClientRects().length) return
  revealing = true
  setTimeout(() => {
    revealing = false
    if (disposed || !field.getClientRects().length) return
    field.focus({ preventScroll: false })
    field.reportValidity()
  }, 0)
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
