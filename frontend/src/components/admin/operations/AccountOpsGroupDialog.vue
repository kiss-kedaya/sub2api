<template>
  <BaseDialog :show="show" :title="t('accountOps.accountGroups')" width="wide" @close="close">
    <div class="space-y-4">
      <p class="text-sm leading-6 text-muted">{{ t('accountOps.accountGroupsHint') }}</p>
      <p v-if="!draftGroups.length" class="py-6 text-center text-sm text-muted">
        {{ t('accountOps.noAccountGroups') }}
      </p>
      <div class="divide-y divide-default">
        <div v-for="group in draftGroups" :key="group.id" class="space-y-3 py-4 first:pt-0">
          <div class="flex flex-wrap items-end justify-between gap-3">
            <UFormField :label="t('accountOps.groupName')" class="min-w-0 flex-1">
              <UInput v-model="group.name" class="w-full" :placeholder="group.default_name" :data-testid="`account-group-name-${group.id}`" />
            </UFormField>
            <div class="flex items-center gap-3 text-right text-xs text-muted">
              <div>
                <p>{{ t('accountOps.manualGroup') }}</p>
                <p class="mt-1 max-w-56 truncate" :title="group.site || group.id">{{ group.site || group.id }}</p>
              </div>
              <UButton color="error" variant="ghost" size="xs" icon="i-lucide-trash-2" :disabled="busy" :data-testid="`delete-account-group-${group.id}`" :label="t('accountOps.deleteAccountGroup')" @click="removeGroup(group.id)" />
            </div>
          </div>
          <div class="grid max-h-48 gap-2 overflow-y-auto sm:grid-cols-2">
            <UCheckbox
              v-for="account in accounts"
              :key="account.account_id"
              :model-value="group.account_ids.includes(account.account_id)"
              :data-testid="`account-group-member-${group.id}-${account.account_id}`"
              class="rounded-md px-2 py-1.5 hover:bg-elevated"
              @update:model-value="toggleMember(group.id, account.account_id, $event === true)"
            >
              <template #label>
                <span class="min-w-0 truncate">{{ account.account_name }} <span class="text-xs text-muted">#{{ account.account_id }}</span></span>
              </template>
            </UCheckbox>
          </div>
        </div>
      </div>
      <UButton color="neutral" variant="outline" icon="i-lucide-plus" data-testid="add-account-group" :label="t('accountOps.addAccountGroup')" @click="addGroup" />
      <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>
    </div>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="close" />
        <UButton data-testid="save-account-groups" :disabled="busy" :label="busy ? t('common.loading') : t('accountOps.saveAccountGroups')" @click="save" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { saveAccountOpsGroups } from '@/api/admin/accountOps'
import type { AccountOpsConfig, AccountOpsGroup, AccountOpsThresholdAccount } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ show: boolean; groups: AccountOpsGroup[]; accounts: AccountOpsThresholdAccount[] }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved', config: AccountOpsConfig): void; (event: 'error', message: string): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const draftGroups = ref<AccountOpsGroup[]>([])
const busy = ref(false)
const error = ref('')
let sequence = 0

watch(() => [props.show, props.groups] as const, () => {
  sequence++
  error.value = ''
  draftGroups.value = props.groups.map(group => ({ ...group, account_ids: Array.isArray(group.account_ids) ? [...group.account_ids] : [] }))
}, { immediate: true, deep: true })

function close() {
  sequence++
  busy.value = false
  emit('close')
}

function addGroup() {
  const id = `manual-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
  draftGroups.value.push({ id, name: '', default_name: t('accountOps.newAccountGroup'), provider: 'manual', site: '', mode: 'manual', account_ids: [] })
}

function removeGroup(groupID: string) {
  draftGroups.value = draftGroups.value.filter(group => group.id !== groupID)
}

function toggleMember(groupID: string, accountID: number, checked: boolean) {
  for (const group of draftGroups.value) {
    group.account_ids = group.account_ids.filter(id => id !== accountID)
  }
  if (checked) draftGroups.value.find(group => group.id === groupID)?.account_ids.push(accountID)
}

async function save() {
  if (!props.show || busy.value) return
    const manualMissing = draftGroups.value.some(group => !group.name.trim())
  if (manualMissing) {
    error.value = t('accountOps.manualGroupNameRequired')
    return
  }
  const current = ++sequence
  busy.value = true
  error.value = ''
  try {
    const config = await saveAccountOpsGroups(draftGroups.value.map(group => ({ id: group.id, name: group.name.trim(), provider: group.provider, site: group.site, account_ids: [...group.account_ids] })))
    if (current === sequence) emit('saved', config)
  } catch (cause) {
    if (current === sequence) {
      error.value = extractApiErrorMessage(cause, t('accountOps.saveFailed'))
      emit('error', error.value)
    }
  } finally {
    if (current === sequence) busy.value = false
  }
}
</script>
