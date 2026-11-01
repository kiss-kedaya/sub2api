<template>
  <section class="space-y-4" data-testid="account-ops-rules">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h3 class="text-base font-semibold text-highlighted">{{ t('accountOps.accountRules') }}</h3>
        <p class="mt-1 text-sm text-muted">{{ t('accountOps.rulesOnceSummary') }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <UInput v-model="search" icon="i-lucide-search" class="w-56" :placeholder="t('accountOps.search')" :aria-label="t('accountOps.search')" />
        <USelect v-model="type" :items="typeItems" class="min-w-36" :aria-label="t('accountOps.accountType')" />
        <USelect v-model="state" :items="stateItems" class="min-w-32" :aria-label="t('accountOps.ruleState')" />
      </div>
    </div>
    <slot name="global-settings" />
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-muted" aria-live="polite">
        {{ t('accountOps.selectedGroups', { count: selectedGroups.length }) }}
        <span v-if="selectedTypes.length" class="ml-2 text-xs">{{ selectedTypes.join(' / ') }}</span>
        <span class="ml-2 text-xs text-dimmed">{{ t('accountOps.batchSelectionHint') }}</span>
      </p>
      <div class="flex shrink-0 items-center gap-1.5">
        <UButton v-if="selectedGroups.length" color="neutral" variant="ghost" :disabled="busy" :label="t('accountOps.clearSelection')" @click="clearSelection" />
        <UButton color="neutral" variant="outline" icon="i-lucide-list-checks" data-testid="batch-edit-rules" :disabled="selectionUnavailable || !selectedGroups.length || !selectedAccounts.length" @click="editBatch">
          {{ t('accountOps.batchEditRules') }}
          <template v-if="selectedGroups.length" #trailing>
            <UBadge color="neutral" variant="subtle" size="sm" class="tabular-nums">{{ selectedGroups.length }}</UBadge>
          </template>
        </UButton>
      </div>
    </div>
    <!-- Rule rows: a quiet list in the template table style (elevated header row, hairline row borders). -->
    <div class="overflow-x-auto">
      <div class="min-w-[820px] text-sm">
        <div class="rule-grid rounded-lg border border-default bg-elevated px-1 py-2 text-xs font-medium text-muted">
          <div class="flex items-center pl-3">
            <UCheckbox
              data-testid="select-visible-groups"
              :aria-label="t('accountOps.selectVisibleGroups')"
              :model-value="allVisibleSelected ? true : selectedGroups.length > 0 ? 'indeterminate' : false"
              :disabled="selectionUnavailable || !filtered.length"
              @update:model-value="selectVisible($event === true)"
            />
          </div>
          <div class="px-3">{{ t('accountOps.accountGroup') }}</div>
          <div class="px-4">{{ t('accountOps.metric') }}</div>
          <div class="px-4">{{ t('accountOps.currentValue') }}</div>
          <div class="px-4">{{ t('accountOps.ruleSummary') }}</div>
          <div class="px-4 text-right">{{ t('common.actions') }}</div>
        </div>
        <div
          v-for="row in filtered"
          :key="row.group.id"
          class="rule-grid border-b border-default px-1 py-3 last:border-b-0"
          :data-testid="`rule-row-${row.group.id}`"
        >
          <div class="flex items-start pl-3 pt-0.5">
            <UCheckbox
              :data-testid="`select-group-${row.group.id}`"
              :aria-label="t('accountOps.selectGroup', { name: row.group.name })"
              :model-value="selectedIds.has(row.group.id)"
              :disabled="selectionUnavailable"
              @update:model-value="selectGroup(row.group.id, $event === true)"
            />
          </div>
          <div class="min-w-0 px-3">
            <p class="font-medium text-highlighted">{{ row.group.name }}</p>
            <p class="mt-1 text-xs text-muted">{{ row.group.site || row.group.provider || '—' }} · {{ t('accountOps.groupMemberCount', { count: row.accounts.length }) }}</p>
            <p class="mt-1 max-w-72 truncate text-xs text-muted" :title="row.memberNames">{{ row.memberNames || t('accountOps.noAccountGroups') }}</p>
          </div>
          <div class="px-4 text-xs text-muted">
            <span v-if="row.balanceAccounts.length" class="block">{{ t('accountOps.balanceMetric') }}</span>
            <span v-if="row.quotaAccounts.length" class="mt-1 block">{{ t('accountOps.quotaMetric') }}</span>
            <span v-if="!row.balanceAccounts.length && !row.quotaAccounts.length">—</span>
            <span v-if="row.quotaWindow" class="mt-1 block">{{ row.quotaWindow }}</span>
          </div>
          <div class="px-4">
            <p v-if="row.balanceValue" class="font-mono text-xs text-default">{{ row.balanceValue }}</p>
            <p v-if="row.quotaValue" class="mt-1 font-mono text-xs text-default">{{ row.quotaValue }}</p>
            <UBadge class="mt-1" :color="row.state === 'warning' ? 'warning' : row.state === 'normal' ? 'success' : 'neutral'" variant="subtle" size="sm">
              {{ t(`accountOps.ruleStates.${row.state}`) }}
            </UBadge>
          </div>
          <div class="px-4">
            <template v-if="row.balanceRule || row.quotaRule">
              <p v-if="row.balanceRule" class="text-xs text-default">≤ {{ row.balanceRule.threshold.toFixed(2) }} {{ row.balanceRule.unit }}</p>
              <p v-if="row.quotaRule" class="mt-1 text-xs text-default">≥ {{ row.quotaRule.threshold_percent }}%</p>
              <p class="mt-1 text-xs text-muted">{{ policy(row) }}</p>
            </template>
            <span v-else class="text-xs text-dimmed">{{ t('accountOps.ruleStates.unconfigured') }}</span>
          </div>
          <div class="px-4">
            <div class="flex items-center justify-end gap-2">
              <USwitch
                v-if="row.balanceRule || row.quotaRule"
                size="sm"
                :model-value="(row.balanceRule?.enabled ?? row.quotaRule?.enabled) === true"
                :disabled="busy"
                :aria-label="`${row.group.name} ${t('accountOps.ruleEnabled')}`"
                :title="t('accountOps.channelEnabled')"
                @update:model-value="emit('toggle', row.group, row.accounts, $event === true)"
              />
              <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-pencil" :data-testid="`edit-rule-${row.group.id}`" :label="t('common.edit')" @click="editRow(row)" />
            </div>
          </div>
        </div>
      </div>
    </div>
    <p v-if="loading" role="status" class="p-8 text-center text-sm text-muted">{{ t('common.loading') }}</p>
    <p v-else-if="!filtered.length && !error" class="p-8 text-center text-sm text-muted">{{ t('accountOps.noThresholdAccounts') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import type { AccountOpsConfig, AccountOpsGroup, AccountOpsThresholdAccount } from '@/api/admin/accountOps'

interface GroupRow {
  group: AccountOpsGroup
  accounts: AccountOpsThresholdAccount[]
  balanceAccounts: AccountOpsThresholdAccount[]
  quotaAccounts: AccountOpsThresholdAccount[]
  balanceRule?: NonNullable<AccountOpsConfig['balance_thresholds']>[number]
  quotaRule?: NonNullable<AccountOpsConfig['quota_thresholds']>[number]
  balanceValue: string
  quotaValue: string
  quotaWindow: string
  memberNames: string
  state: string
}

const props = withDefaults(defineProps<{ accounts: AccountOpsThresholdAccount[]; groups?: AccountOpsGroup[]; config: AccountOpsConfig; loading: boolean; ready: boolean; error: string; busy?: boolean }>(), { groups: () => [] })
const emit = defineEmits<{ (event: 'batch-edit', accounts: AccountOpsThresholdAccount[]): void; (event: 'edit-group', group: AccountOpsGroup): void; (event: 'toggle', group: AccountOpsGroup, accounts: AccountOpsThresholdAccount[], enabled: boolean): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const search = ref(''), type = ref('all'), state = ref('all'), selectedIds = ref(new Set<string>())
const states = ['warning', 'normal', 'unknown', 'unconfigured', 'disabled']
const typeItems = computed(() => [
  { value: 'all', label: t('accountOps.allAccounts') },
  { value: 'apikey', label: 'API Key' },
  { value: 'oauth', label: 'OAuth' },
  { value: 'mixed', label: 'API Key + OAuth' }
])
const stateItems = computed(() => [{ value: 'all', label: t('accountOps.allStates') }, ...states.map(value => ({ value, label: t(`accountOps.ruleStates.${value}`) }))])
const accountByID = computed(() => new Map(props.accounts.map(account => [account.account_id, account])))
const groups = computed(() => {
  const result = props.groups.filter(group => group.mode === 'manual').map(group => ({ ...group, account_ids: Array.isArray(group.account_ids) ? [...group.account_ids] : [] }))
  const known = new Set(result.map(group => group.id))
  for (const account of props.accounts) {
    if (account.group_mode !== 'manual' || !account.group_id || known.has(account.group_id)) continue
    result.push({ id: account.group_id, name: account.group_name || account.group_id, default_name: account.group_name || account.group_id, provider: account.platform, site: account.platform, mode: 'manual', account_ids: [] })
    known.add(account.group_id)
  }
  return result
})
function metricState(account: AccountOpsThresholdAccount, rule: GroupRow['balanceRule'] | GroupRow['quotaRule'] | undefined): string {
  if (!rule) return 'unconfigured'
  if (!rule.enabled) return 'disabled'
  if ('unit' in rule) {
    if (account.balance_status !== 'ok' || account.balance == null || rule.unit !== account.unit) return 'unknown'
    return account.balance <= rule.threshold ? 'warning' : 'normal'
  }
  const selected = rule.window || 'any'
  const windows = account.usage_windows.filter(item => selected === 'any' || selected === '' || selected === item.window)
  const valid = windows.filter(item => item.status === 'ok' && item.used_percent != null && Number.isFinite(item.used_percent))
  if (!windows.length || valid.length !== windows.length) return 'unknown'
  return valid.some(item => item.used_percent! >= rule.threshold_percent) ? 'warning' : 'normal'
}
function metricValue(account: AccountOpsThresholdAccount, rule: GroupRow['balanceRule'] | GroupRow['quotaRule'] | undefined): string {
  if (!rule) return ''
  if ('unit' in rule) return account.balance_status === 'ok' && account.balance != null ? `${account.balance.toFixed(2)} ${account.unit}` : t(`accountOps.observations.${account.balance_status}`)
  const selected = rule.window || 'any'
  const current = account.usage_windows.find(item => (selected === 'any' || selected === '' || selected === item.window) && item.status === 'ok' && item.used_percent != null)
  return current ? `${current.used_percent!.toFixed(1)}%` : t('accountOps.observations.unknown')
}
const rows = computed<GroupRow[]>(() => groups.value.map(group => {
  const accounts = group.account_ids.map(id => accountByID.value.get(id)).filter((account): account is AccountOpsThresholdAccount => !!account)
  const balanceAccounts = accounts.filter(account => account.type === 'apikey')
  const quotaAccounts = accounts.filter(account => account.type === 'oauth')
  const balanceRule = balanceAccounts.map(account => props.config.balance_thresholds?.find(rule => rule.account_id === account.account_id)).find((rule): rule is NonNullable<GroupRow['balanceRule']> => !!rule)
  const quotaRule = quotaAccounts.map(account => props.config.quota_thresholds?.find(rule => rule.account_id === account.account_id)).find((rule): rule is NonNullable<GroupRow['quotaRule']> => !!rule)
  const statesForGroup = [...balanceAccounts.map(account => metricState(account, balanceRule)), ...quotaAccounts.map(account => metricState(account, quotaRule))]
  const rowState = statesForGroup.includes('warning') ? 'warning' : statesForGroup.includes('unknown') ? 'unknown' : statesForGroup.length > 0 && statesForGroup.every(value => value === 'disabled') ? 'disabled' : statesForGroup.length > 0 && statesForGroup.every(value => value === 'unconfigured') ? 'unconfigured' : 'normal'
  return { group, accounts, balanceAccounts, quotaAccounts, balanceRule, quotaRule, balanceValue: balanceRule && balanceAccounts[0] ? metricValue(balanceAccounts[0], balanceRule) : '', quotaValue: quotaRule && quotaAccounts[0] ? metricValue(quotaAccounts[0], quotaRule) : '', quotaWindow: quotaRule?.window && quotaRule.window !== 'any' ? quotaRule.window : '', memberNames: accounts.map(account => account.account_name).join('、'), state: rowState }
}))
const filtered = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return rows.value.filter(row => {
    const hasType = type.value === 'all' || (type.value === 'apikey' && row.balanceAccounts.length > 0 && row.quotaAccounts.length === 0) || (type.value === 'oauth' && row.quotaAccounts.length > 0 && row.balanceAccounts.length === 0) || (type.value === 'mixed' && row.balanceAccounts.length > 0 && row.quotaAccounts.length > 0)
    const matches = !needle || `${row.group.name} ${row.group.id} ${row.group.site} ${row.memberNames}`.toLowerCase().includes(needle)
    return hasType && (state.value === 'all' || row.state === state.value) && matches
  })
})
const selectionUnavailable = computed(() => props.busy || props.loading || !props.ready || !!props.error)
const selectedGroups = computed(() => filtered.value.filter(row => selectedIds.value.has(row.group.id)))
const selectedAccounts = computed(() => {
  const unique = new Map<number, AccountOpsThresholdAccount>()
  for (const row of selectedGroups.value) for (const account of row.accounts) unique.set(account.account_id, account)
  return [...unique.values()]
})
const selectedTypes = computed(() => [...(selectedAccounts.value.some(account => account.type === 'apikey') ? ['API Key'] : []), ...(selectedAccounts.value.some(account => account.type === 'oauth') ? ['OAuth'] : [])])
const allVisibleSelected = computed(() => filtered.value.length > 0 && filtered.value.every(row => selectedIds.value.has(row.group.id)))
const policy = (row: GroupRow) => {
  const rules = [row.balanceRule, row.quotaRule].filter(Boolean)
  const alert = rules.some(rule => rule?.notify_alert ?? true), recovery = rules.some(rule => rule?.notify_recovery ?? true)
  return t(alert ? (recovery ? 'accountOps.policyBoth' : 'accountOps.policyAlert') : (recovery ? 'accountOps.policyRecovery' : 'accountOps.policyRecord'))
}
function clearSelection() { selectedIds.value = new Set() }
watch([type, state], clearSelection, { flush: 'sync' })
watch(filtered, visible => { const visibleIDs = new Set(visible.map(row => row.group.id)); selectedIds.value = new Set([...selectedIds.value].filter(id => visibleIDs.has(id))) }, { flush: 'sync' })
function selectGroup(id: string, checked: boolean) { if (selectionUnavailable.value) return; const next = new Set(selectedIds.value); if (checked) next.add(id); else next.delete(id); selectedIds.value = next }
function selectVisible(checked: boolean) { if (selectionUnavailable.value) return; selectedIds.value = new Set(checked ? filtered.value.map(row => row.group.id) : []) }
function editRow(row: GroupRow) {
  if (selectionUnavailable.value) return
  if (row.accounts.length) emit('batch-edit', row.accounts)
  else emit('edit-group', row.group)
}
function editBatch() { if (!selectionUnavailable.value && selectedAccounts.value.length) emit('batch-edit', selectedAccounts.value) }
defineExpose({ clearSelection })
</script>

<style scoped>
/* One grid for the header row and every rule row, so the columns line up like a table. */
.rule-grid {
  display: grid;
  grid-template-columns: 2.75rem minmax(12rem, 1.6fr) minmax(6rem, 0.7fr) minmax(8rem, 0.9fr) minmax(9rem, 1fr) minmax(9rem, auto);
  align-items: start;
}
</style>
