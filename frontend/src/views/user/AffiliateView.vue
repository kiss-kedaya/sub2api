<template>
  <AppLayout>
    <div class="mx-auto flex w-full max-w-4xl flex-col gap-4 sm:gap-6 lg:gap-10">
      <UserPageHeader flush :title="t('affiliate.title')" :description="t('affiliate.description')" />
      <div v-if="loading" class="flex justify-center py-12">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-muted" />
      </div>

      <template v-else-if="detail">
        <UPageGrid class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px">
          <UPageCard icon="i-lucide-percent" :title="t('affiliate.stats.rebateRate')" variant="subtle" :ui="statUi" class="first:rounded-l-lg last:rounded-r-lg hover:z-1 lg:rounded-none">
            <span class="text-2xl font-semibold tabular-nums text-highlighted" :title="t('affiliate.stats.rebateRateHint')">{{ formattedRebateRate }}<span class="ml-0.5 text-base font-medium">%</span></span>
          </UPageCard>
          <UPageCard icon="i-lucide-users" :title="t('affiliate.stats.invitedUsers')" variant="subtle" :ui="statUi" class="first:rounded-l-lg last:rounded-r-lg hover:z-1 lg:rounded-none">
            <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ formatCount(detail.aff_count) }}</span>
          </UPageCard>
          <UPageCard icon="i-lucide-wallet" :title="t('affiliate.stats.availableQuota')" variant="subtle" :ui="statUi" class="first:rounded-l-lg last:rounded-r-lg hover:z-1 lg:rounded-none">
            <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ formatCurrency(detail.aff_quota) }}</span>
          </UPageCard>
          <UPageCard icon="i-lucide-history" :title="t('affiliate.stats.totalQuota')" variant="subtle" :ui="statUi" class="first:rounded-l-lg last:rounded-r-lg hover:z-1 lg:rounded-none">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ formatCurrency(detail.aff_history_quota) }}</span>
              <UBadge v-if="detail.aff_frozen_quota > 0" color="warning" variant="subtle" class="tabular-nums">{{ t('affiliate.stats.frozenQuota') }}: {{ formatCurrency(detail.aff_frozen_quota) }}</UBadge>
            </div>
          </UPageCard>
        </UPageGrid>

        <section>
          <UPageCard :title="t('affiliate.inviteLink')" :description="t('affiliate.stats.rebateRateHint')" variant="naked" orientation="horizontal" class="mb-4">
            <UPopover :content="{ align: 'end' }">
              <UButton color="neutral" variant="ghost" icon="i-lucide-info" class="w-fit lg:ms-auto">{{ t('affiliate.tips.title') }}</UButton>
              <template #content>
                <ol class="max-w-sm space-y-1 p-4 text-sm text-toned">
                  <li>1. {{ t('affiliate.tips.line1') }}</li>
                  <li>2. {{ t('affiliate.tips.line2', { rate: `${formattedRebateRate}%` }) }}</li>
                  <li>3. {{ t('affiliate.tips.line3') }}</li>
                  <li v-if="detail.aff_frozen_quota > 0">4. {{ t('affiliate.tips.line4') }}</li>
                </ol>
              </template>
            </UPopover>
          </UPageCard>
          <UPageCard variant="subtle">
            <UFormField :label="t('affiliate.yourCode')" class="flex justify-between gap-4 max-sm:flex-col sm:items-center" :ui="{ container: 'sm:w-[60%]' }">
              <div class="flex flex-col items-stretch gap-2 rounded-md bg-default py-1.5 pl-3 pr-1.5 ring ring-default sm:flex-row sm:items-center">
                <code class="min-w-0 break-all font-mono text-sm font-semibold text-highlighted sm:flex-1 sm:truncate">{{ detail.aff_code }}</code>
                <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-copy" class="w-full justify-center sm:w-auto sm:shrink-0" @click="copyCode">{{ t('affiliate.copyCode') }}</UButton>
              </div>
            </UFormField>
            <USeparator />
            <UFormField
              :label="t('affiliate.inviteLink')"
              :description="t('affiliate.customCodes.stats', { count: detail.aff_code_invite_count ?? 0, amount: formatCurrency(detail.aff_code_rebate_amount ?? 0) })"
              class="flex justify-between gap-4 max-sm:flex-col sm:items-center"
              :ui="{ container: 'sm:w-[60%]' }"
            >
              <div class="flex flex-col items-stretch gap-2 rounded-md bg-default py-1.5 pl-3 pr-1.5 ring ring-default sm:flex-row sm:items-center">
                <code class="min-w-0 break-all font-mono text-sm text-toned sm:flex-1 sm:truncate">{{ inviteLink }}</code>
                <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-copy" class="w-full justify-center sm:w-auto sm:shrink-0" @click="copyInviteLink">{{ t('affiliate.copyLink') }}</UButton>
              </div>
            </UFormField>
          </UPageCard>
        </section>

        <section>
          <UPageCard :title="t('affiliate.customCodes.title')" :description="t('affiliate.customCodes.description', { max: customCodeQuota.max })" variant="naked" orientation="horizontal" class="mb-4">
            <UBadge color="neutral" variant="subtle" class="w-fit lg:ms-auto">{{ t('affiliate.customCodes.quota', { used: customCodeQuota.used, max: customCodeQuota.max }) }}</UBadge>
          </UPageCard>
          <UPageCard variant="subtle" :ui="{ container: 'p-0 sm:p-0 gap-y-0' }">
            <ul class="divide-y divide-default">
              <!-- Default code: read-only, cannot be changed. -->
              <li class="flex items-center gap-2 px-4 py-3 sm:px-6">
                <UBadge color="neutral" variant="outline" class="shrink-0">{{ t('affiliate.customCodes.defaultBadge') }}</UBadge>
                <code class="min-w-0 flex-1 break-all font-mono text-sm font-semibold text-highlighted">{{ detail.aff_code }}</code>
                <span class="shrink-0 text-xs text-muted">{{ t('affiliate.customCodes.stats', { count: detail.aff_code_invite_count ?? 0, amount: formatCurrency(detail.aff_code_rebate_amount ?? 0) }) }}</span>
                <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-copy" :aria-label="t('affiliate.copyCode')" :title="t('affiliate.copyCode')" :disabled="copyingId === 'default'" @click="copyAny(detail.aff_code, 'default')" />
              </li>
              <li v-for="item in customCodes" :key="item.code" class="flex items-center gap-2 px-4 py-3 sm:px-6">
                <UBadge color="neutral" variant="outline" class="shrink-0">{{ item.created_by_admin ? t('affiliate.customCodes.adminBadge') : t('affiliate.customCodes.customBadge') }}</UBadge>
                <code class="min-w-0 flex-1 break-all font-mono text-sm font-semibold text-highlighted">{{ item.code }}</code>
                <span class="shrink-0 text-xs text-muted">{{ t('affiliate.customCodes.stats', { count: item.invite_count ?? 0, amount: formatCurrency(item.rebate_amount ?? 0) }) }}</span>
                <UBadge v-if="item.retired" color="neutral" variant="subtle" class="shrink-0">{{ t('affiliate.customCodes.retiredBadge') }}</UBadge>
                <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-copy" :aria-label="t('affiliate.copyCode')" :title="t('affiliate.copyCode')" :disabled="copyingId === item.code" @click="copyAny(item.code, item.code)" />
                <UButton
                  v-if="!item.retired"
                  color="error"
                  variant="ghost"
                  size="sm"
                  :icon="deletingId === item.id ? 'i-lucide-loader-circle' : 'i-lucide-trash-2'"
                  :ui="{ leadingIcon: deletingId === item.id ? 'animate-spin' : '' }"
                  :aria-label="t('affiliate.customCodes.remove')"
                  :title="t('affiliate.customCodes.remove')"
                  :disabled="deletingId === item.id"
                  @click="removeCode(item)"
                />
              </li>
              <li v-if="customCodes.length === 0" class="px-4 py-5 text-center text-sm text-muted sm:px-6">{{ t('affiliate.customCodes.empty') }}</li>
            </ul>
            <div v-if="customCodeQuota.limit > 0" class="flex flex-col gap-2 border-t border-default px-4 py-3 sm:flex-row sm:items-center sm:px-6">
              <UInput v-model="newCode" type="text" maxlength="32" class="flex-1" :placeholder="t('affiliate.customCodes.addPlaceholder')" :aria-label="t('affiliate.customCodes.addPlaceholder')" @keyup.enter="addCode" />
              <UButton class="shrink-0 justify-center" :loading="adding" :icon="adding ? undefined : 'i-lucide-plus'" :disabled="adding || !newCode.trim()" @click="addCode">
                {{ adding ? t('affiliate.customCodes.adding') : t('affiliate.customCodes.addButton') }}
              </UButton>
            </div>
          </UPageCard>
        </section>

        <section>
          <UPageCard :title="t('affiliate.transfer.title')" :description="t('affiliate.transfer.description')" variant="naked" orientation="horizontal">
            <UButton
              class="w-fit lg:ms-auto"
              :loading="transferring"
              :icon="transferring ? undefined : 'i-lucide-arrow-right-left'"
              :disabled="transferring || detail.aff_quota <= 0"
              @click="transferQuota"
            >
              {{ transferring ? t('affiliate.transfer.transferring') : t('affiliate.transfer.button') }}
            </UButton>
          </UPageCard>
          <p v-if="detail.aff_quota <= 0" class="mt-2 text-sm text-warning">{{ t('affiliate.transfer.empty') }}</p>
        </section>

        <section>
          <UPageCard :title="t('affiliate.invitees.title')" variant="naked" class="mb-4" />
          <UTable
            :data="detail.invitees"
            :columns="inviteeColumns"
            :ui="{
              root: 'rounded-lg ring ring-default',
              base: 'min-w-[560px] border-separate border-spacing-0',
              thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
              tbody: '[&>tr]:last:[&>td]:border-b-0',
              th: 'py-2 border-b border-default',
              td: 'border-b border-default',
              separator: 'h-0'
            }"
          >
            <template #email-cell="{ row }"><span class="text-highlighted">{{ row.original.email || '-' }}</span></template>
            <template #username-cell="{ row }">{{ row.original.username || '-' }}</template>
            <template #total_rebate-cell="{ row }"><span class="block text-right font-medium tabular-nums text-highlighted">{{ formatCurrency(row.original.total_rebate) }}</span></template>
            <template #created_at-cell="{ row }"><span class="tabular-nums">{{ formatDateTime(row.original.created_at) || '-' }}</span></template>
            <template #empty><p class="py-6 text-center text-sm text-muted">{{ t('affiliate.invitees.empty') }}</p></template>
          </UTable>
        </section>
      </template>
    </div>
    <ConfirmDialog
      :show="pendingDelete !== null"
      :title="t('affiliate.customCodes.remove')"
      :message="pendingDelete ? t('affiliate.customCodes.removeConfirm', { code: pendingDelete.code }) : ''"
      danger
      @confirm="confirmRemoveCode"
      @cancel="pendingDelete = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import userAPI from '@/api/user'
import type { UserAffiliateCode, UserAffiliateDetail } from '@/types'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useClipboard } from '@/composables/useClipboard'
import { formatCurrency, formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { copyToClipboard } = useClipboard()

const loading = ref(true)
const transferring = ref(false)
const detail = ref<UserAffiliateDetail | null>(null)
const newCode = ref('')
const adding = ref(false)
const deletingId = ref<number | null>(null)
const copyingId = ref<string | null>(null)
const pendingDelete = ref<UserAffiliateCode | null>(null)

const customCodes = computed<UserAffiliateCode[]>(() => detail.value?.custom_codes ?? [])

type Invitee = UserAffiliateDetail['invitees'][number]
const inviteeColumns = computed<TableColumn<Invitee>[]>(() => [
  { accessorKey: 'email', header: t('affiliate.invitees.columns.email') },
  { accessorKey: 'username', header: t('affiliate.invitees.columns.username') },
  { accessorKey: 'total_rebate', header: () => h('span', { class: 'block text-right' }, t('affiliate.invitees.columns.rebate')) },
  { accessorKey: 'created_at', header: t('affiliate.invitees.columns.joinedAt') }
])

// Stat cards like the template HomeStats.
const statUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
}
const customCodeQuota = computed(() => detail.value?.custom_code_quota ?? { max: 9, used: 0, limit: 9 })

const inviteLink = computed(() => {
  if (!detail.value) return ''
  if (typeof window === 'undefined') return `/register?aff=${encodeURIComponent(detail.value.aff_code)}`
  return `${window.location.origin}/register?aff=${encodeURIComponent(detail.value.aff_code)}`
})

// Rebate rate is a percentage in the range [0, 100]; backend already clamps it.
// We trim trailing zeros (e.g. 20.00 → "20", 12.50 → "12.5") for a cleaner UI.
const formattedRebateRate = computed(() => {
  const v = detail.value?.effective_rebate_rate_percent ?? 0
  const rounded = Math.round(v * 100) / 100
  return Number.isInteger(rounded) ? String(rounded) : rounded.toString()
})

function formatCount(value: number): string {
  return value.toLocaleString()
}

async function loadAffiliateDetail(silent = false): Promise<void> {
  if (!silent) {
    loading.value = true
  }
  try {
    detail.value = await userAPI.getAffiliateDetail()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('affiliate.loadFailed')))
  } finally {
    if (!silent) {
      loading.value = false
    }
  }
}

async function copyCode(): Promise<void> {
  if (!detail.value?.aff_code) return
  await copyToClipboard(detail.value.aff_code, t('affiliate.codeCopied'))
}

async function copyInviteLink(): Promise<void> {
  if (!inviteLink.value) return
  await copyToClipboard(inviteLink.value, t('affiliate.linkCopied'))
}

async function copyAny(code: string, key: string): Promise<void> {
  copyingId.value = key
  try {
    await copyToClipboard(code, t('affiliate.customCodes.copied'))
  } finally {
    copyingId.value = null
  }
}

async function addCode(): Promise<void> {
  const code = newCode.value.trim().toUpperCase()
  if (!code || adding.value) return
  if (!/^[A-Z0-9_-]{4,32}$/.test(code)) {
    appStore.showError(t('affiliate.customCodes.invalidFormat'))
    return
  }
  adding.value = true
  try {
    await userAPI.createUserAffiliateCode(code)
    newCode.value = ''
    appStore.showSuccess(t('affiliate.customCodes.createSuccess'))
    await loadAffiliateDetail(true)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('affiliate.customCodes.createFailed')))
  } finally {
    adding.value = false
  }
}

function removeCode(item: UserAffiliateCode): void {
  pendingDelete.value = item
}

async function confirmRemoveCode(): Promise<void> {
  const item = pendingDelete.value
  if (!item || deletingId.value !== null) return
  deletingId.value = item.id
  try {
    await userAPI.deleteUserAffiliateCode(item.id)
    pendingDelete.value = null
    appStore.showSuccess(t('affiliate.customCodes.deleteSuccess'))
    await loadAffiliateDetail(true)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('affiliate.customCodes.deleteFailed')))
  } finally {
    deletingId.value = null
  }
}

async function transferQuota(): Promise<void> {
  if (!detail.value || detail.value.aff_quota <= 0 || transferring.value) return
  transferring.value = true
  try {
    const resp = await userAPI.transferAffiliateQuota()
    appStore.showSuccess(t('affiliate.transfer.success', { amount: formatCurrency(resp.transferred_quota) }))
    await Promise.all([
      loadAffiliateDetail(true),
      authStore.refreshUser().catch(() => undefined),
    ])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('affiliate.transferFailed')))
  } finally {
    transferring.value = false
  }
}

onMounted(() => {
  void loadAffiliateDetail()
})
</script>
