<template>
  <AppLayout>
    <div class="account-ops mx-auto max-w-6xl space-y-6">
      <AdminPageHeader :title="t('accountOps.title')" :description="t('accountOps.description')">
        <template v-if="remote" #actions>
          <UBadge :color="enabledDraft ? 'success' : 'neutral'" variant="subtle">{{ enabledDraft ? t('accountOps.observing') : t('accountOps.disabled') }}</UBadge>
        </template>
      </AdminPageHeader>
      <UPageGrid v-if="remote" class="gap-4 sm:grid-cols-3 lg:grid-cols-3 lg:gap-px" data-testid="account-ops-summary">
        <UPageCard variant="subtle" :ui="{ container: 'gap-y-1.5', title: 'text-xs font-normal text-muted' }" :title="t('accountOps.enabledChannels')">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ enabledChannelCount }}</span>
        </UPageCard>
        <UPageCard variant="subtle" :ui="{ container: 'gap-y-1.5', title: 'text-xs font-normal text-muted' }" :title="t('accountOps.enabledRules')">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ enabledRuleCount }}</span>
        </UPageCard>
        <UPageCard variant="subtle" :ui="{ container: 'gap-y-1.5', title: 'text-xs font-normal text-muted' }" :title="t('accountOps.loadedPending')">
          <span class="text-2xl font-semibold tabular-nums" :class="pendingEventCount ? 'text-warning' : 'text-highlighted'">{{ pendingEventCount }}</span>
        </UPageCard>
      </UPageGrid>
      <UAlert v-if="loadError" role="alert" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="`${t('accountOps.previousData')} ${loadError}`" />

      <!-- Toolbar: section tabs on the left, the global switch and refresh on the right. -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div role="tablist" :aria-label="t('accountOps.pageSections')" class="flex gap-1 rounded-lg bg-elevated p-1">
          <UButton
            v-for="tab in tabs"
            :id="`ops-tab-${tab}`"
            :key="tab"
            role="tab"
            color="neutral"
            :variant="activeTab === tab ? 'outline' : 'ghost'"
            :class="activeTab === tab ? 'bg-default shadow-xs' : 'text-muted'"
            :aria-selected="activeTab === tab"
            :aria-controls="`ops-panel-${tab}`"
            :tabindex="activeTab === tab ? 0 : -1"
            :data-testid="`account-ops-tab-${tab}`"
            :label="t(tab === 'records' ? 'accountOps.recordsTab' : 'accountOps.settingsTab')"
            @click="selectTab(tab)"
            @keydown="tabKey($event, tab)"
          />
        </div>
        <div class="flex items-center gap-3">
          <div class="flex items-center gap-2 text-sm font-medium text-default"><span id="account-ops-enabled-label">{{ t('accountOps.enabled') }}</span>
            <Toggle :model-value="enabledDraft" aria-labelledby="account-ops-enabled-label"
              :disabled="!remote || loading || mutating || globalSaving" class="disabled:cursor-wait disabled:opacity-50"
              data-testid="notifications-enabled" @update:model-value="toggleNotifications" />
          </div>
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loading" :disabled="loading || thresholdLoading || mutating || globalSaving" :label="t('qualityOps.refresh')" @click="refresh" />
        </div>
      </div>

      <section v-if="activeTab === 'records'" id="ops-panel-records" role="tabpanel" aria-labelledby="ops-tab-records" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-sm text-muted">{{ t('accountOps.recordsOnceHint') }}</p>
          <div class="flex flex-wrap items-center gap-1.5">
            <UInput v-model="query" icon="i-lucide-search" class="w-56" :placeholder="t('accountOps.search')" :aria-label="t('accountOps.search')" />
            <USelect v-model="kind" :items="kindItems" class="min-w-36" :aria-label="t('accountOps.alertTypes')" />
            <USelect v-model="phase" :items="phaseItems" class="min-w-28" :aria-label="t('accountOps.eventPhase')" />
          </div>
        </div>
        <UAlert v-if="remote?.dropped_signals || remote?.storage_failures" role="alert" color="warning" variant="subtle" :description="t('accountOps.captureIssue')" />
        <div class="overflow-x-auto" data-testid="account-events-scroll" :aria-busy="loading">
          <UTable v-if="filteredEvents.length" :data="filteredEvents" :columns="eventColumns" class="min-w-[760px]" :ui="tableUi">
            <template #account-cell="{ row }">
              <div data-testid="notification-record">
                <p class="font-medium text-highlighted">{{ row.original.group_name || row.original.account_name }}</p>
                <p class="mt-1 text-xs text-muted">#{{ row.original.account_id }} · {{ t(`accountOps.${row.original.kind}`) }}</p>
              </div>
            </template>
            <template #phase-cell="{ row }">
              <UBadge :color="row.original.phase === 'recovery' ? 'success' : 'warning'" variant="subtle" size="sm">
                {{ t(row.original.phase ? `accountOps.phases.${row.original.phase}` : 'accountOps.failureSignal') }}
              </UBadge>
            </template>
            <template #value-cell="{ row }">
              <div class="text-xs text-default">
                <template v-if="row.original.kind === 'balance_threshold'">{{ row.original.details?.balance }} {{ row.original.details?.unit }}<span class="mt-1 block text-muted">{{ t('accountOps.thresholdValue') }} {{ row.original.details?.threshold }} {{ row.original.details?.unit }}</span></template>
                <template v-else-if="row.original.kind === 'quota_threshold'">{{ row.original.details?.used_percent }}% · {{ row.original.details?.window }}<span class="mt-1 block text-muted">{{ t('accountOps.thresholdValue') }} {{ row.original.details?.threshold_percent }}%</span></template>
                <template v-else>HTTP {{ row.original.http_status }}<span class="mt-1 block text-muted">{{ t(`accountOps.signals.${row.original.signal}`) }}</span></template>
              </div>
            </template>
            <template #occurredAt-cell="{ row }">
              <span class="whitespace-nowrap text-xs tabular-nums text-muted">{{ date(row.original.first_seen) }}</span>
            </template>
            <template #mailStatus-cell="{ row }">
              <p class="whitespace-nowrap text-xs" :class="row.original.state === 'failed' ? 'text-error' : row.original.state === 'sent' ? 'text-success' : 'text-muted'">
                {{ t(row.original.notification_enabled === false ? 'accountOps.policyRecord' : `accountOps.states.${row.original.state}`) }}
              </p>
              <p v-if="row.original.state === 'failed'" class="mt-1 text-xs text-muted">
                {{ t(row.original.attempts < 3 ? 'accountOps.retryAt' : 'accountOps.retryStopped', { time: date(row.original.next_send_at) }) }}
              </p>
            </template>
            <template #deliveryDetails-cell="{ row }">
              <AccountOpsDeliveryDetails :deliveries="row.original.deliveries" />
            </template>
          </UTable>
        </div>
        <div v-if="!filteredEvents.length" class="flex min-h-64 flex-col items-center justify-center gap-2 p-8 text-center">
          <UIcon name="i-lucide-bell" class="size-8 text-dimmed" />
          <p class="text-sm text-highlighted">{{ t(loading ? 'common.loading' : events.length ? 'accountOps.noMatchingRecords' : 'accountOps.empty') }}</p>
          <p class="max-w-lg text-xs leading-5 text-muted">{{ t('accountOps.recordsEmptyHint') }}</p>
          <UButton color="neutral" variant="outline" class="mt-1" :label="t('accountOps.settingsTab')" @click="selectTab('settings')" />
        </div>
        <div class="flex items-center justify-between gap-3 border-t border-default pt-3 text-xs text-muted">
          <span>{{ t('accountOps.noRawErrors') }}</span>
          <UButton v-if="hasMore" variant="link" size="xs" :disabled="loadingMore" :label="t(loadingMore ? 'common.loading' : 'qualityOps.loadMore')" @click="more" />
        </div>
      </section>

      <section v-if="activeTab === 'settings' && remote" id="ops-panel-settings" role="tabpanel" aria-labelledby="ops-tab-settings" class="space-y-8">
        <!-- Notification channels -->
        <div class="space-y-3" data-testid="account-ops-channels">
          <div class="flex flex-wrap items-end justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('accountOps.notificationChannels') }}</h2>
              <p class="mt-1 text-sm text-muted">{{ t('accountOps.channelSummaryHint') }}</p>
            </div>
            <UButton color="neutral" variant="outline" icon="i-lucide-plus" :disabled="!canAddChannel" data-testid="account-ops-add-channel" :label="t('accountOps.addChannel')" @click="showAddChannel = true" />
          </div>
          <UPageCard variant="subtle" :ui="{ container: 'p-0 sm:p-0 gap-y-0' }">
            <ul class="divide-y divide-default">
              <li v-if="remote.config.recipient" class="flex flex-wrap items-center justify-between gap-3 px-4 py-3" data-testid="account-ops-email-channel">
                <div class="min-w-0">
                  <p class="truncate text-sm font-medium text-highlighted" :title="remote.config.email_name || t('accountOps.providers.email')">{{ remote.config.email_name || t('accountOps.providers.email') }}</p>
                  <p class="mt-0.5 truncate text-xs text-muted">{{ remote.config.email_name ? `${t('accountOps.providers.email')} · ` : '' }}{{ remote.config.recipient }}</p>
                </div>
                <div class="flex items-center gap-2">
                  <UBadge color="neutral" variant="subtle" size="sm">{{ t(remote.config.recipient ? 'accountOps.channelConfigured' : 'accountOps.channelUnconfigured') }}</UBadge>
                  <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-pencil" data-testid="edit-email-channel" :label="t('common.edit')" @click="showEmail = true" />
                  <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-trash-2" :disabled="mutating" data-testid="delete-email-channel" :aria-label="t('common.delete')" :title="t('common.delete')" @click="removeEmail" />
                </div>
              </li>
              <li v-for="(hook, index) in remote.config.webhooks" :key="hook.id" class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
                <div class="min-w-0">
                  <p class="truncate text-sm font-medium text-highlighted" :title="hook.name || `${t(`accountOps.providers.${hook.provider}`)} ${index + 1}`">{{ hook.name || `${t(`accountOps.providers.${hook.provider}`)} ${index + 1}` }}</p>
                  <p class="mt-0.5 text-xs text-muted">{{ hook.name ? `${t(`accountOps.providers.${hook.provider}`)} · ` : '' }}{{ t(hook.url_configured ? 'accountOps.credentialSaved' : 'accountOps.channelUnconfigured') }}</p>
                </div>
                <div class="flex items-center gap-2">
                  <UBadge :color="hook.enabled ? 'success' : 'neutral'" variant="subtle" size="sm">{{ t(hook.enabled ? 'accountOps.channelEnabled' : 'accountOps.channelDisabled') }}</UBadge>
                  <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-send" :disabled="!hook.url_configured || !!testingId" :data-testid="`account-ops-webhook-test-${hook.id}`" :label="t(testingId === hook.id ? 'common.loading' : 'accountOps.testRobot')" @click="testWebhook(hook.id)" />
                  <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-pencil" :data-testid="`edit-webhook-${hook.id}`" :label="t('common.edit')" @click="editWebhook(hook)" />
                  <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-trash-2" :aria-label="t('common.delete')" :title="t('common.delete')" @click="removeWebhook(hook.id)" />
                </div>
              </li>
              <li v-if="!remote.config.recipient && !(remote.config.webhooks?.length)" class="px-4 py-6 text-center text-sm text-muted">
                {{ t('accountOps.channelUnconfigured') }}
              </li>
            </ul>
          </UPageCard>
        </div>

        <!-- Account groups -->
        <div class="flex flex-wrap items-center justify-between gap-3" data-testid="account-ops-groups">
          <div>
            <h2 class="text-base font-semibold text-highlighted">{{ t('accountOps.accountGroups') }}</h2>
            <p class="mt-1 text-sm text-muted">{{ t('accountOps.accountGroupsSummary', { count: accountGroups.length }) }}</p>
          </div>
          <UButton color="neutral" variant="outline" icon="i-lucide-pencil" :disabled="thresholdLoading || mutating" data-testid="edit-account-groups" :label="t('common.edit')" @click="showGroupDialog = true" />
        </div>

        <AccountOpsRuleList ref="ruleList" :accounts="thresholdAccounts" :groups="accountGroups" :config="remote.config" :loading="thresholdLoading"
          :ready="thresholdReady" :error="thresholdError" :busy="mutating"
          @batch-edit="batchAccounts = $event" @edit-group="showGroupDialog = true" @toggle="toggleGroup">
          <template #global-settings>
            <UCollapsible :unmount-on-hide="false" class="border-y border-default">
              <UButton color="neutral" variant="link" class="px-0 py-3 font-medium" trailing-icon="i-lucide-chevron-down" :label="t('accountOps.advancedSettings')" />
              <template #content>
                <AccountOpsGlobalSettings :config="remote.config" :disabled="mutating" @saving="globalSaving = $event" @saved="acceptConfig"
                  @error="app.showError" />
              </template>
            </UCollapsible>
          </template>
        </AccountOpsRuleList>
      </section>
      <template v-if="remote && auth.user">
        <AccountOpsBatchRuleDialog :show="batchAccounts.length > 0" :accounts="batchAccounts" :config="remote.config" @close="batchAccounts = []"
          @saved="acceptBatchConfig" @error="app.showError" />
        <AccountOpsAddChannelDialog :show="showAddChannel" :email-configured="!!remote.config.recipient"
          :webhook-count="remote.config.webhooks?.length ?? 0" :encryption-configured="remote.encryption_key_configured === true"
          @close="showAddChannel = false" @select="addChannel" />
        <AccountOpsWebhookDialog :show="showWebhook" :hook="editingHook" :initial-provider="editingProvider" :encryption-configured="remote.encryption_key_configured === true"
          :testing-id="testingId" @close="showWebhook = false" @saved="acceptConfig" @error="app.showError" @test="testWebhook" />
        <AccountOpsEmailDialog :show="showEmail" :config="remote.config" :smtp-configured="remote.smtp_configured"
          @close="showEmail = false" @saved="acceptConfig" @error="app.showError" />
        <AccountOpsGroupDialog :show="showGroupDialog" :groups="accountGroups" :accounts="thresholdAccounts"
          @close="showGroupDialog = false" @saved="acceptGroupConfig" @error="app.showError" />
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useAccountOpsStore } from '@/stores/accountOps'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { accountOpsMessages } from '@/components/admin/operations/accountOpsMessages'
import Toggle from '@/components/common/Toggle.vue'
import AccountOpsRuleList from '@/components/admin/operations/AccountOpsRuleList.vue'
import AccountOpsBatchRuleDialog from '@/components/admin/operations/AccountOpsBatchRuleDialog.vue'
import AccountOpsDeliveryDetails from '@/components/admin/operations/AccountOpsDeliveryDetails.vue'
import AccountOpsWebhookDialog from '@/components/admin/operations/AccountOpsWebhookDialog.vue'
import AccountOpsEmailDialog from '@/components/admin/operations/AccountOpsEmailDialog.vue'
import AccountOpsAddChannelDialog from '@/components/admin/operations/AccountOpsAddChannelDialog.vue'
import AccountOpsGlobalSettings from '@/components/admin/operations/AccountOpsGlobalSettings.vue'
import AccountOpsGroupDialog from '@/components/admin/operations/AccountOpsGroupDialog.vue'
import { getAccountOpsSettings, saveAccountOpsNotificationSettings, getAccountOpsEvents, getAccountOpsThresholdAccounts, getAccountOpsThresholdGroups, testAccountOpsWebhook, saveAccountOpsRuleGroups, deleteAccountOpsWebhook } from '@/api/admin/accountOps'
import type { AccountOpsConfig, AccountOpsEvent, AccountOpsThresholdAccount, AccountOpsWebhook, AccountOpsWebhookProvider, AccountOpsGroup, AccountOpsRuleBatchGroup } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages }), auth = useAuthStore(), app = useAppStore()
const { remote, events, hasMore } = storeToRefs(useAccountOpsStore())
const tabs = ['settings','records'] as const, activeTab = ref<'records'|'settings'>('settings')
const loading = ref(false), loadingMore = ref(false), mutating = ref(false)
const thresholdAccounts = ref<AccountOpsThresholdAccount[]>([]), thresholdLoading = ref(false), thresholdReady = ref(false), thresholdError = ref('')
const accountGroups = ref<AccountOpsGroup[]>([]), showGroupDialog = ref(false)
const enabledDraft = ref(false), globalSaving = ref(false), showAddChannel = ref(false)
const canAddChannel = computed(() => !!remote.value && (!remote.value.config.recipient || ((remote.value.config.webhooks?.length ?? 0) < 5 && remote.value.encryption_key_configured === true)))
const loadError = ref('')
const enabledChannelCount = computed(() => (remote.value?.config.recipient ? 1 : 0) + (remote.value?.config.webhooks ?? []).filter(h => h.enabled).length)
const enabledRuleCount = computed(() => [...(remote.value?.config.balance_thresholds ?? []), ...(remote.value?.config.quota_thresholds ?? [])].filter(r => r.enabled).length)
const pendingEventCount = computed(() => events.value.filter(e => ['pending', 'sending', 'failed'].includes(e.state)).length)
watch(() => remote.value?.config.enabled, enabled => { enabledDraft.value = enabled ?? false }, { immediate: true })
const ruleList = ref<InstanceType<typeof AccountOpsRuleList> | null>(null)
const batchAccounts = ref<AccountOpsThresholdAccount[]>([])
watch(thresholdAccounts, accounts => {
  if (!batchAccounts.value.length) return
  const selected = new Set(batchAccounts.value.map(account => account.account_id))
  batchAccounts.value = accounts.filter(account => selected.has(account.account_id))
})
const query = ref(''), kind = ref('all'), phase = ref('all'), alertKinds = ['balance_threshold','quota_threshold','balance_low','weekly_quota']
const kindItems = computed(() => [{ value: 'all', label: t('accountOps.allTypes') }, ...alertKinds.map(value => ({ value, label: t(`accountOps.${value}`) }))])
const phaseItems = computed(() => [{ value: 'all', label: t('accountOps.allPhases') }, { value: 'alert', label: t('accountOps.phases.alert') }, { value: 'recovery', label: t('accountOps.phases.recovery') }])
// Records list in the template table style: elevated header row, hairline row borders.
const eventColumns = computed<TableColumn<AccountOpsEvent>[]>(() => [
  { id: 'account', header: t('qualityOps.accounts') },
  { id: 'phase', header: t('accountOps.eventPhase') },
  { id: 'value', header: t('accountOps.eventValue') },
  { id: 'occurredAt', header: t('accountOps.occurredAt') },
  { id: 'mailStatus', header: t('accountOps.mailStatus') },
  { id: 'deliveryDetails', header: t('accountOps.deliveryDetails') }
])
const tableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 text-xs font-medium text-muted first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'py-3 align-top border-b border-default',
  separator: 'h-0'
}
const editingHook = ref<AccountOpsWebhook|null>(null), editingProvider = ref<AccountOpsWebhookProvider>('custom'), showWebhook = ref(false), showEmail = ref(false), testingId = ref<string|null>(null)
let version = 0, accountsVersion = 0, testSequence = 0, mutationSequence = 0, paginationSequence = 0, alive = true, timer: ReturnType<typeof setInterval>|null = null
const normalize = (c: AccountOpsConfig): AccountOpsConfig => ({ enabled:c.enabled, recipient:c.recipient,email_name:c.email_name ?? '', balance_low:c.balance_low, weekly_quota:c.weekly_quota, cooldown_minutes:c.cooldown_minutes, webhooks:(c.webhooks??[]).map(h=>({id:h.id,name:h.name,provider:h.provider,enabled:h.enabled,url_configured:h.url_configured===true,secret_configured:h.secret_configured===true,message_template:h.message_template})), groups:(c.groups??[]).map(g=>({id:g.id,name:g.name,provider:g.provider,site:g.site,account_ids:g.account_ids ? [...g.account_ids] : []})), balance_thresholds:(c.balance_thresholds??[]).map(r=>({...r,notify_alert:r.notify_alert??true,notify_recovery:r.notify_recovery??true})), quota_thresholds:(c.quota_thresholds??[]).map(r=>({...r,notify_alert:r.notify_alert??true,notify_recovery:r.notify_recovery??true})) })
const eventKey = (e: AccountOpsEvent) => e.id ?? `${e.account_id}:${e.kind}:${e.phase??'legacy'}:${e.first_seen}`
const filteredEvents = computed(()=>events.value.filter(e=>(kind.value==='all'||kind.value===e.kind)&&(phase.value==='all'||phase.value===(e.phase??'alert'))&&`${e.account_name} ${e.account_id}`.toLowerCase().includes(query.value.toLowerCase().trim())))
const date = (value: string) => { const d=new Date(value);return Number.isFinite(d.getTime())?d.toLocaleString():'-' }
const message = (e: unknown) => extractApiErrorMessage(e,t('accountOps.saveFailed'))
async function load() {
  if (!auth.user || mutating.value || globalSaving.value) return
  const current=++version;loading.value=true
  const [settings, page] = await Promise.allSettled([getAccountOpsSettings(), getAccountOpsEvents()])
  if (!alive || current !== version) return
  const failures: string[] = []
  if (settings.status === 'fulfilled') remote.value = { ...settings.value, config: normalize(settings.value.config) }; else failures.push(message(settings.reason))
  if (page.status === 'fulfilled') { events.value = page.value.items; hasMore.value = page.value.has_more } else failures.push(message(page.reason))
  loadError.value = [...new Set(failures)].join(' · ')
  if (loadError.value) app.showError(loadError.value)
  loading.value = false
}
async function refresh() {
  if (mutating.value || globalSaving.value) return
  await Promise.all([load(), ...(activeTab.value === 'settings' ? [loadAccounts()] : [])])
}
async function loadAccounts() {
  if(!auth.user)return
  const current=++accountsVersion;thresholdLoading.value=true
  try{const [a, g] = await Promise.all([getAccountOpsThresholdAccounts(), getAccountOpsThresholdGroups()]);if(alive&&current===accountsVersion){thresholdAccounts.value=a;accountGroups.value=g;thresholdReady.value=true;thresholdError.value=''}}
  catch(e){if(alive&&current===accountsVersion){thresholdError.value=message(e);app.showError(thresholdError.value)}}
  finally{if(alive&&current===accountsVersion)thresholdLoading.value=false}
}
function selectTab(tab:'records'|'settings'){activeTab.value=tab;if(tab==='settings'&&!thresholdReady.value&&!thresholdLoading.value)void loadAccounts()}
function tabKey(event:KeyboardEvent,tab:'records'|'settings'){if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;event.preventDefault();const next=event.key==='Home'?'settings':event.key==='End'?'records':tab==='records'?'settings':'records';selectTab(next);document.getElementById(`ops-tab-${next}`)?.focus()}
function acceptConfig(config: AccountOpsConfig){if(!alive||!auth.user||!remote.value)return;version++;loading.value=false;remote.value={...remote.value,config:normalize(config)};app.showSuccess(t('accountOps.settingsSaved'))}
function acceptGroupConfig(config: AccountOpsConfig){showGroupDialog.value=false;acceptConfig(config);void loadGroups()}
async function loadGroups(){try{const groups=await getAccountOpsThresholdGroups();if(alive)accountGroups.value=groups}catch(e){if(alive)app.showError(message(e))}}
function acceptBatchConfig(config: AccountOpsConfig){acceptConfig(config);ruleList.value?.clearSelection()}
function editWebhook(hook:AccountOpsWebhook|null, provider: AccountOpsWebhookProvider = 'custom'){editingHook.value=hook;editingProvider.value=provider;showWebhook.value=true}
function addChannel(provider: 'email'|'webhook'|'bark') {
  if (!remote.value) return
  if (provider === 'email') {
    if (remote.value.config.recipient) return
    showEmail.value = true
  } else {
    if (remote.value.encryption_key_configured !== true || (remote.value.config.webhooks?.length ?? 0) >= 5) return
    editWebhook(null, provider === 'bark' ? 'bark' : 'custom')
  }
  showAddChannel.value = false
}
async function removeEmail(){await mutate(()=>saveAccountOpsNotificationSettings({recipient:'',email_name:''}))}
async function mutate(action:()=>Promise<AccountOpsConfig>){if(mutating.value)return;const current=++mutationSequence;mutating.value=true;try{const c=await action();if(alive&&current===mutationSequence)acceptConfig(c)}catch(e){if(alive&&current===mutationSequence)app.showError(message(e))}finally{if(alive&&current===mutationSequence)mutating.value=false}}
async function toggleNotifications(enabled: boolean) {
  enabledDraft.value = enabled
  await mutate(() => saveAccountOpsNotificationSettings({ enabled: enabledDraft.value }))
  enabledDraft.value = remote.value?.config.enabled ?? false
}
async function toggleGroup(_group:AccountOpsGroup, accounts:AccountOpsThresholdAccount[], enabled:boolean){
  if (!remote.value) return
  const groups: AccountOpsRuleBatchGroup[] = []
  const apiKeys = accounts.filter(account => account.type === 'apikey')
  const oauth = accounts.filter(account => account.type === 'oauth')
  if (apiKeys.length) groups.push({ account_ids: apiKeys.map(account => account.account_id), rule: { metric: 'balance' as const, enabled, enabled_only: true } })
  if (oauth.length) groups.push({ account_ids: oauth.map(account => account.account_id), rule: { metric: 'quota' as const, enabled, enabled_only: true } })
  if (groups.length) await mutate(() => saveAccountOpsRuleGroups(groups))
}
async function removeWebhook(id:string){await mutate(()=>deleteAccountOpsWebhook(id))}
async function testWebhook(id:string){if(testingId.value)return;const current=++testSequence;testingId.value=id;try{await testAccountOpsWebhook(id);if(alive&&current===testSequence)app.showSuccess(t('accountOps.testSuccess'))}catch(e){if(alive&&current===testSequence)app.showError(message(e))}finally{if(alive&&current===testSequence)testingId.value=null}}
async function more(){if(loadingMore.value||loading.value)return;const current=version, sequence=++paginationSequence;loadingMore.value=true;try{const p=await getAccountOpsEvents(events.value.length);if(alive&&current===version){const merged=new Map([...events.value,...p.items].map(e=>[eventKey(e),e]));events.value=[...merged.values()];hasMore.value=p.has_more}}catch(e){if(alive&&current===version)app.showError(message(e))}finally{if(alive&&sequence===paginationSequence)loadingMore.value=false}}
watch(()=>auth.user?`${auth.user.id}:${auth.user.role}`:'',()=>{version++;accountsVersion++;testSequence++;mutationSequence++;paginationSequence++;loading.value=loadingMore.value=mutating.value=thresholdLoading.value=false;thresholdAccounts.value=[];accountGroups.value=[];thresholdReady.value=false;showWebhook.value=showEmail.value=showAddChannel.value=showGroupDialog.value=false;testingId.value=null;enabledDraft.value=globalSaving.value=false;activeTab.value='settings';batchAccounts.value=[];loadError.value=''},{flush:'sync'})
onMounted(()=>{void load();void loadAccounts();timer=setInterval(()=>{if(document.visibilityState==='visible'&&!loadingMore.value&&!mutating.value&&!globalSaving.value){void load();if(activeTab.value==='settings')void loadAccounts()}},30000)})
onBeforeUnmount(()=>{alive=false;version++;accountsVersion++;testSequence++;mutationSequence++;paginationSequence++;if(timer)clearInterval(timer)})
</script>
