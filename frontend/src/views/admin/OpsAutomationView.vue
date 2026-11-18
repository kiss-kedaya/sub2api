<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl min-w-0 space-y-5">
      <AdminPageHeader :title="t('opsAutomation.title')" :description="t('opsAutomation.description')">
        <template #actions><UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :disabled="busy" :label="t('opsAutomation.refresh')" @click="load" /></template>
      </AdminPageHeader>
      <UAlert v-if="error" role="alert" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
      <UAlert v-if="notice" role="status" color="success" variant="subtle" :title="notice" />
      <UAlert v-if="draft?.ramp_suspended" role="alert" color="warning" variant="subtle" :title="t('opsAutomation.rampSuspended')" />
      <form v-if="draft" class="space-y-4" @submit.prevent="save">
        <UPageCard :title="t('opsAutomation.oauth')" :description="t('opsAutomation.oauthHint')" variant="outline" :ui="{ container: 'min-w-0', wrapper: 'min-w-0', description: 'whitespace-normal break-words' }">
          <UTable :data="draft.oauth" :columns="oauthColumns" :ui="{ root: 'max-w-full overflow-x-auto', base: 'min-w-[760px]' }">
            <template #enabled-cell="{ row }"><USwitch v-model="row.original.enabled" :aria-label="`${row.original.platform} ${t('opsAutomation.enabled')}`" /></template>
            <template #concurrency-cell="{ row }"><UInput v-model.number="row.original.concurrency" type="number" min="1" max="10000" class="w-24" :aria-label="t('opsAutomation.concurrency')" /></template>
            <template #priority-cell="{ row }"><UInput v-model.number="row.original.priority" type="number" min="0" max="10000" class="w-24" :aria-label="t('opsAutomation.priority')" /></template>
            <template #load_factor-cell="{ row }"><UInput v-model.number="row.original.load_factor" type="number" min="1" max="10000" class="w-24" :aria-label="t('opsAutomation.loadFactor')" /></template>
            <template #groups-cell="{ row }"><div class="min-w-64"><GroupSelector v-model="row.original.group_ids" :groups="groupOptions.filter(g => g.platform === row.original.platform)" :searchable="true" /></div></template>
          </UTable>
        </UPageCard>
        <UPageCard variant="outline" :ui="{ container: 'gap-y-0' }">
          <SettingRow :label="t('opsAutomation.ramp')" :hint="t('opsAutomation.rampHint')" :on="draft.ramp.enabled">
            <template #control><USwitch v-model="draft.ramp.enabled" :aria-label="t('opsAutomation.ramp')" /></template>
            <template #fields>
              <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <UFormField :label="t('opsAutomation.groups')" class="sm:col-span-2"><GroupSelector v-model="draft.ramp.group_ids" :groups="groupOptions" :searchable="true" /></UFormField>
                <UFormField :label="t('opsAutomation.successes')"><UInput v-model.number="draft.ramp.successes_per_step" class="w-full" type="number" min="1" max="100000" /></UFormField>
                <UFormField :label="t('opsAutomation.step')"><UInput v-model.number="draft.ramp.step" class="w-full" type="number" min="1" max="1000" /></UFormField>
                <UFormField :label="t('opsAutomation.maximum')"><UInput v-model.number="draft.ramp.maximum" class="w-full" type="number" min="1" max="10000" /></UFormField>
              </div>
              <UCollapsible class="mt-3">
                <UButton type="button" color="neutral" variant="link" trailing-icon="i-lucide-chevron-down" :label="t('opsAutomation.advanced')" />
                <template #content><UFormField :label="t('opsAutomation.cooldown')" class="mt-3"><UInput v-model.number="draft.ramp.cooldown_seconds" type="number" min="1" max="86400" class="w-36" /></UFormField></template>
              </UCollapsible>
            </template>
          </SettingRow>
          <SettingRow :label="t('opsAutomation.ranking')" :hint="t('opsAutomation.rankingHint')" :on="draft.ranking.enabled">
            <template #control><USwitch v-model="draft.ranking.enabled" :aria-label="t('opsAutomation.ranking')" /></template>
            <template #fields>
              <div class="grid gap-4 sm:grid-cols-3">
                <UFormField :label="t('opsAutomation.groups')" class="sm:col-span-2"><GroupSelector v-model="draft.ranking.group_ids" :groups="groupOptions" :searchable="true" /></UFormField>
                <UFormField :label="t('opsAutomation.mode')"><USelect v-model="draft.ranking.mode" :items="['experience', 'balanced', 'cost', 'custom'].map(value => ({ value, label: t('opsAutomation.' + value) }))" class="w-full" /></UFormField>
                <UFormField :label="t('opsAutomation.window')"><UInput v-model.number="draft.ranking.window_minutes" class="w-full" type="number" min="5" max="1440" /></UFormField>
              </div>
              <UCollapsible class="mt-3">
                <UButton type="button" color="neutral" variant="link" trailing-icon="i-lucide-chevron-down" :label="t('opsAutomation.advanced')" />
                <template #content>
                  <div class="mt-3 grid gap-4 sm:grid-cols-4">
                    <UFormField :label="t('opsAutomation.samples')"><UInput v-model.number="draft.ranking.minimum_samples" class="w-full" type="number" min="1" max="100000" /></UFormField>
                    <UFormField v-for="key in ['quality_weight', 'latency_weight', 'cost_weight'] as const" :key="key" :label="t('opsAutomation.' + key)"><UInput v-model.number="draft.ranking[key]" class="w-full" type="number" min="0" max="100" :disabled="draft.ranking.mode !== 'custom'" /></UFormField>
                  </div>
                </template>
              </UCollapsible>
            </template>
          </SettingRow>
          <SettingRow :label="t('opsAutomation.fallback')" :hint="t('opsAutomation.fallbackHint')" :on="draft.oauth_fallback.enabled">
            <template #control><USwitch v-model="draft.oauth_fallback.enabled" :aria-label="t('opsAutomation.fallback')" /></template>
            <template #fields>
              <div class="grid gap-4 sm:grid-cols-4">
                <UFormField :label="t('opsAutomation.groups')" class="sm:col-span-2"><GroupSelector v-model="draft.oauth_fallback.group_ids" :groups="groupOptions" :searchable="true" /></UFormField>
                <UFormField :label="t('opsAutomation.minimumQuality')"><UInput v-model.number="draft.oauth_fallback.minimum_quality" class="w-full" type="number" min="0" max="1" step="0.01" /></UFormField>
                <UFormField :label="t('opsAutomation.minimumQuota')"><UInput v-model.number="draft.oauth_fallback.minimum_quota_percent" class="w-full" type="number" min="0" max="100" /></UFormField>
                <UFormField :label="t('opsAutomation.maximumLoad')"><UInput v-model.number="draft.oauth_fallback.maximum_load_percent" class="w-full" type="number" min="0" max="100" /></UFormField>
              </div>
            </template>
          </SettingRow>
          <SettingRow :label="t('opsAutomation.admission')" :hint="t('opsAutomation.admissionHint')" :on="draft.admission.enabled">
            <template #control><USwitch v-model="draft.admission.enabled" :aria-label="t('opsAutomation.admission')" /></template>
            <template #fields>
              <div class="grid gap-4 sm:grid-cols-2">
                <UFormField :label="t('opsAutomation.admissionPlatforms')"><UInput :model-value="draft.admission.platforms.join(', ')" class="w-full" placeholder="openai, anthropic" @update:model-value="draft.admission.platforms = String($event).split(',').map(p => p.trim()).filter(Boolean)" /></UFormField>
                <UFormField :label="t('opsAutomation.admissionModel')"><UInput v-model.trim="draft.admission.model" class="w-full" maxlength="200" /></UFormField>
              </div>
            </template>
          </SettingRow>
        </UPageCard>
        <div class="flex justify-end"><UButton type="submit" :disabled="busy" :loading="busy" :label="t('opsAutomation.save')" /></div>
      </form>
      <USkeleton v-else-if="!error" class="h-48 w-full" />
      <UPageCard :title="t('opsAutomation.reasoning')" :description="t('opsAutomation.reasoningHint')" variant="outline">
        <UButton type="button" icon="i-lucide-plus" color="neutral" variant="outline" class="w-fit" :disabled="busy" :label="t('opsAutomation.newPlan')" @click="newPlan" />
        <UTable :data="plans" :columns="planColumns" :ui="{ root: 'max-w-full overflow-x-auto', base: 'min-w-[720px]' }">
          <template #id-cell="{ row }">#{{ row.original.id }}</template>
          <template #account-cell="{ row }">{{ accountNames.get(row.original.account_id) || '#' + row.original.account_id }}</template>
          <template #enabled-cell="{ row }"><UBadge color="neutral" variant="subtle">{{ row.original.enabled ? t('opsAutomation.yes') : t('opsAutomation.no') }}</UBadge></template>
          <template #outcome-cell="{ row }">{{ row.original.last_outcome ? t('opsAutomation.' + row.original.last_outcome) : '—' }}</template>
          <template #actions-cell="{ row }">
            <div class="flex gap-1">
              <UButton color="neutral" variant="ghost" size="sm" :disabled="busy" :label="t('opsAutomation.edit')" @click="editPlan = { ...row.original }" />
              <UButton color="neutral" variant="ghost" size="sm" :disabled="busy" :label="t('opsAutomation.run')" @click="run(row.original)" />
              <UButton color="neutral" variant="ghost" size="sm" :disabled="busy" :label="t('opsAutomation.ownership')" @click="review(row.original)" />
            </div>
          </template>
          <template #empty>{{ t('common.noData') }}</template>
        </UTable>
      </UPageCard>
      <BaseDialog :show="!!editPlan" :title="t('opsAutomation.reasoning')" @close="!busy && (editPlan = null)">
        <form v-if="editPlan" class="space-y-4" @submit.prevent="savePlan">
          <UFormField :label="t('opsAutomation.account')"><Select :model-value="editPlan.account_id" :options="accountOptions" :searchable="true" :remote="true" :loading="accountsLoading" @search="searchAccounts" @update:model-value="editPlan.account_id = Number($event) || 0" /></UFormField>
          <UFormField :label="t('opsAutomation.model')"><UInput v-model.trim="editPlan.model" class="w-full" maxlength="200" required /></UFormField>
          <UFormField :label="t('opsAutomation.interval')"><UInput v-model.number="editPlan.interval_minutes" class="w-full" type="number" min="5" max="10080" required /></UFormField>
          <UFormField :label="t('opsAutomation.judge')">
            <USelect v-model="editPlan.judge_kind" class="w-full" :items="[{ value: 'local', label: t('opsAutomation.localJudge') }, { value: 'llm', label: t('opsAutomation.llmJudge') }, { value: 'jev', label: t('opsAutomation.jevJudge') }]" @update:model-value="editPlan.judge_group_id = 0; editPlan.judge_model = ''" />
          </UFormField>
          <p class="text-sm text-muted">{{ t('opsAutomation.judgeHint') }}</p>
          <template v-if="editPlan.judge_kind === 'llm'">
            <UFormField :label="t('opsAutomation.judgeGroup')"><Select :model-value="editPlan.judge_group_id" :options="groupSelectOptions" :searchable="true" @update:model-value="editPlan.judge_group_id = Number($event) || 0" /></UFormField>
            <UFormField :label="t('opsAutomation.judgeModel')"><UInput v-model.trim="editPlan.judge_model" class="w-full" maxlength="200" required /></UFormField>
          </template>
          <USwitch v-model="editPlan.enabled" :label="t('opsAutomation.enabled')" />
          <UFormField :label="t('opsAutomation.action')"><USelect v-model="editPlan.action" class="w-full" :items="[{ value: 'observe', label: t('opsAutomation.observe') }, { value: 'pause', label: t('opsAutomation.pause') }]" /></UFormField>
          <UFormField :label="t('opsAutomation.scope')"><USelect v-model="editPlan.pause_scope" class="w-full" :items="[{ value: 'model', label: t('opsAutomation.scopeModel') }, { value: 'group', label: t('opsAutomation.scopeGroup') }, { value: 'account', label: t('opsAutomation.scopeAccount') }]" @update:model-value="editPlan.target_group_id = 0" /></UFormField>
          <UFormField v-if="editPlan.pause_scope === 'group'" :label="t('opsAutomation.targetGroup')"><Select :model-value="editPlan.target_group_id" :options="groupSelectOptions" :searchable="true" @update:model-value="editPlan.target_group_id = Number($event) || 0" /></UFormField>
          <USwitch v-model="editPlan.auto_restore" :disabled="editPlan.action !== 'pause'" :label="t('opsAutomation.autoRestore')" />
          <UButton type="submit" :disabled="busy" :label="t('opsAutomation.save')" />
        </form>
      </BaseDialog>
      <BaseDialog :show="!!ownership" :title="t('opsAutomation.ownership')" @close="!busy && (ownership = null)">
        <div v-if="ownership" class="space-y-4">
          <p>{{ t('opsAutomation.account') }} #{{ ownership.account_id }} · {{ ownership.owned ? t('opsAutomation.owned') : t('opsAutomation.unowned') }}</p>
          <p class="text-sm text-muted">{{ t('opsAutomation.ownershipHint') }}</p>
          <div class="flex flex-wrap gap-3">
            <UButton color="neutral" variant="outline" :disabled="busy || !ownership.can_restore" :label="t('opsAutomation.restore')" @click="reconcile(false)" />
            <UButton color="error" variant="subtle" :disabled="busy" :label="t('opsAutomation.remove')" @click="reconcile(true)" />
          </div>
        </div>
      </BaseDialog>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import Select from '@/components/common/Select.vue'
import SettingRow from '@/components/common/SettingRow.vue'
import * as groupsApi from '@/api/admin/groups'
import * as accountsApi from '@/api/admin/accounts'
import type { AdminGroup } from '@/types'
import * as api from '@/api/admin/opsAutomation'
import { extractApiErrorMessage } from '@/utils/apiError'
import { opsAutomationMessages } from '@/components/admin/operations/opsAutomationMessages'
const { t } = useI18n({ useScope: 'local', messages: opsAutomationMessages })
const oauthColumns = computed<TableColumn<api.OAuthDefaults>[]>(() => [
  { id: 'enabled', header: t('opsAutomation.enabled') }, { accessorKey: 'platform', header: t('opsAutomation.platform') },
  { id: 'concurrency', header: t('opsAutomation.concurrency') }, { id: 'priority', header: t('opsAutomation.priority') },
  { id: 'load_factor', header: t('opsAutomation.loadFactor') }, { id: 'groups', header: t('opsAutomation.groups') },
])
const planColumns = computed<TableColumn<api.LocalReasoningPlan>[]>(() => [
  { id: 'id', header: 'ID' }, { id: 'account', header: t('opsAutomation.account') }, { accessorKey: 'model', header: t('opsAutomation.model') },
  { id: 'enabled', header: t('opsAutomation.enabled') }, { id: 'outcome', header: t('opsAutomation.outcome') }, { id: 'actions', header: t('opsAutomation.actions') },
])
const draft = ref<api.OpsAutomationConfig | null>(null), plans = ref<api.LocalReasoningPlan[]>([]), editPlan = ref<api.LocalReasoningPlan | null>(null), ownership = ref<api.LocalOwnership | null>(null)
const busy = ref(false), error = ref(''), notice = ref('')
let alive = true, sequence = 0
const groupOptions = ref<AdminGroup[]>([])
const accountRows = ref<Array<{ id: number; name: string }>>([]), accountsLoading = ref(false)
const accountNames = computed(() => new Map(accountRows.value.map(a => [a.id, a.name])))
const accountOptions = computed(() => { const values = accountRows.value.map(a => ({ value: a.id, label: a.name + ' (#' + a.id + ')' })); const selected = editPlan.value?.account_id; if (selected && !values.some(v => v.value === selected)) values.unshift({ value: selected, label: '#' + selected }); return values })
const groupSelectOptions = computed(() => groupOptions.value.map(g => ({ value: g.id, label: g.name + ' (#' + g.id + ')' })))
let accountSequence = 0
async function searchAccounts(query = '') { const current = ++accountSequence; accountsLoading.value = true; try { const value = await accountsApi.list(1, 30, { search: query, status: 'active', lite: 'true' }); if (alive && current === accountSequence) accountRows.value = value.items.map(a => ({ id: a.id, name: a.name })) } catch (e) { if (alive && current === accountSequence) error.value = message(e) } finally { if (alive && current === accountSequence) accountsLoading.value = false } }
async function loadGroups() { try { const value = await groupsApi.getAll(); if (alive) groupOptions.value = value } catch (e) { if (alive) error.value = message(e) } }
const message = (e: unknown) => extractApiErrorMessage(e, t('opsAutomation.failedMessage'))
async function load() { const current = ++sequence; const [config, list] = await Promise.allSettled([api.getAutomation(), api.getReasoningPlans()]); if (!alive || current !== sequence) return; error.value = ''; if (config.status === 'fulfilled') draft.value = config.value; else error.value = message(config.reason); if (list.status === 'fulfilled') plans.value = list.value; else error.value = message(list.reason) }
async function action(fn: () => Promise<unknown>) { if (busy.value) return; busy.value = true; error.value = notice.value = ''; try { await fn(); if (alive) { notice.value = t('opsAutomation.saved'); await load() } } catch (e) { if (alive) error.value = message(e) } finally { if (alive) busy.value = false } }
async function save() { if (draft.value) await action(() => api.saveAutomation(draft.value!)) }
function newPlan() { editPlan.value = { id: 0, account_id: 0, model: '', enabled: false, interval_minutes: 60, action: 'observe', auto_restore: false, revision: 0, last_outcome: '', next_run_at: '', judge_kind: 'local', judge_group_id: 0, judge_model: '', pause_scope: 'model', target_group_id: 0 } }
async function savePlan() { if (editPlan.value) await action(async () => { await api.saveReasoningPlan(editPlan.value!); editPlan.value = null }) }
async function run(plan: api.LocalReasoningPlan) { await action(() => api.runReasoningPlan(plan.id)) }
async function review(plan: api.LocalReasoningPlan) { await action(async () => { ownership.value = await api.getReasoningOwnership(plan.id) }) }
async function reconcile(remove: boolean) { if (ownership.value) await action(async () => { const v = ownership.value!; await api.updateReasoningOwnership(v.plan_id, v.fence, v.owned && v.can_restore, remove); ownership.value = null }) }
onMounted(() => { void load(); void loadGroups(); void searchAccounts() })
onBeforeUnmount(() => { alive = false; sequence++ })
</script>
