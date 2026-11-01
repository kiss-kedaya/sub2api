<template>
  <AppLayout>
    <div class="signal-account-page signal-affiliate space-y-6">
      <UserPageHeader flush :title="t('affiliate.title')" :description="t('affiliate.description')" />
      <div v-if="loading" class="flex justify-center py-12">
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"
        ></div>
      </div>

      <template v-else-if="detail">
        <div class="signal-affiliate-metrics affiliate-metrics grid sm:grid-cols-2 lg:grid-cols-4">
          <div class="card p-5">
            <p class="flex items-center gap-1.5 text-sm text-gray-500 dark:text-dark-400">
              <Icon name="dollar" size="sm" class="text-primary-500" />
              {{ t('affiliate.stats.rebateRate') }}
            </p>
            <p class="mt-2 text-2xl font-semibold text-primary-600 dark:text-primary-400">
              {{ formattedRebateRate }}<span class="ml-0.5 text-base font-medium">%</span>
            </p>
            <p class="mt-1 text-xs text-gray-400 dark:text-dark-500">
              {{ t('affiliate.stats.rebateRateHint') }}
            </p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('affiliate.stats.invitedUsers') }}</p>
            <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">
              {{ formatCount(detail.aff_count) }}
            </p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('affiliate.stats.availableQuota') }}</p>
            <p class="mt-2 text-2xl font-semibold text-emerald-600 dark:text-emerald-400">
              {{ formatCurrency(detail.aff_quota) }}
            </p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('affiliate.stats.totalQuota') }}</p>
            <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">
              {{ formatCurrency(detail.aff_history_quota) }}
            </p>
            <p v-if="detail.aff_frozen_quota > 0" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
              {{ t('affiliate.stats.frozenQuota') }}: {{ formatCurrency(detail.aff_frozen_quota) }}
            </p>
          </div>
        </div>

        <section class="affiliate-section">

          <div class="grid gap-4 md:grid-cols-2">
            <div class="space-y-2">
              <p class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('affiliate.yourCode') }}</p>
              <div class="flex flex-col items-stretch gap-2 rounded-xl border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900 sm:flex-row sm:items-center">
                <code class="min-w-0 break-all text-sm font-semibold text-gray-900 dark:text-white sm:flex-1 sm:truncate">{{ detail.aff_code }}</code>
                <button class="btn btn-secondary btn-sm w-full sm:w-auto sm:shrink-0" @click="copyCode">
                  <Icon name="copy" size="sm" />
                  <span>{{ t('affiliate.copyCode') }}</span>
                </button>
              </div>
            </div>

            <div class="space-y-2">
              <p class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('affiliate.inviteLink') }}</p>
              <p class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('affiliate.customCodes.stats', { count: detail.aff_code_invite_count ?? 0, amount: formatCurrency(detail.aff_code_rebate_amount ?? 0) }) }}
              </p>
              <div class="flex flex-col items-stretch gap-2 rounded-xl border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900 sm:flex-row sm:items-center">
                <code class="min-w-0 break-all text-sm text-gray-700 dark:text-gray-300 sm:flex-1 sm:truncate">{{ inviteLink }}</code>
                <button class="btn btn-secondary btn-sm w-full sm:w-auto sm:shrink-0" @click="copyInviteLink">
                  <Icon name="copy" size="sm" />
                  <span>{{ t('affiliate.copyLink') }}</span>
                </button>
              </div>
            </div>
          </div>

          <UAlert class="mt-5 dark:bg-primary/10 dark:text-primary dark:ring-primary/25" color="neutral" variant="subtle" icon="i-lucide-info" :title="t('affiliate.tips.title')">
            <template #description>
              <ul class="mt-1 space-y-1">
                <li>1. {{ t('affiliate.tips.line1') }}</li>
                <li>2. {{ t('affiliate.tips.line2', { rate: `${formattedRebateRate}%` }) }}</li>
                <li>3. {{ t('affiliate.tips.line3') }}</li>
                <li v-if="detail.aff_frozen_quota > 0">4. {{ t('affiliate.tips.line4') }}</li>
              </ul>
            </template>
          </UAlert>
        </section>

        <section class="affiliate-section">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('affiliate.customCodes.title') }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
                {{ t('affiliate.customCodes.description', { max: customCodeQuota.max }) }}
              </p>
            </div>
            <span class="badge badge-gray shrink-0">
              {{ t('affiliate.customCodes.quota', { used: customCodeQuota.used, max: customCodeQuota.max }) }}
            </span>
          </div>

          <!-- 默认码：只读，锁死不可改 -->
          <div class="mt-4 flex items-center gap-2 rounded-xl border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900">
            <span class="badge badge-gray shrink-0">{{ t('affiliate.customCodes.defaultBadge') }}</span>
            <code class="min-w-0 flex-1 break-all text-sm font-semibold text-gray-900 dark:text-white">{{ detail.aff_code }}</code>
            <span class="shrink-0 text-xs text-gray-500 dark:text-dark-400">
              {{ t('affiliate.customCodes.stats', { count: detail.aff_code_invite_count ?? 0, amount: formatCurrency(detail.aff_code_rebate_amount ?? 0) }) }}
            </span>
            <button class="btn btn-secondary btn-sm shrink-0" :disabled="copyingId === 'default'" @click="copyAny(detail.aff_code, 'default')">
              <Icon name="copy" size="sm" />
            </button>
          </div>

          <div v-if="customCodes.length === 0" class="mt-3 rounded-xl border border-dashed border-gray-300 p-5 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
            {{ t('affiliate.customCodes.empty') }}
          </div>
          <ul v-else class="mt-3 space-y-2">
            <li
              v-for="item in customCodes"
              :key="item.code"
              class="flex items-center gap-2 rounded-xl border border-gray-200 px-3 py-2 dark:border-dark-700"
            >
              <span class="badge badge-gray shrink-0">{{ item.created_by_admin ? t('affiliate.customCodes.adminBadge') : t('affiliate.customCodes.customBadge') }}</span>
              <code class="min-w-0 flex-1 break-all text-sm font-semibold text-gray-900 dark:text-white">{{ item.code }}</code>
              <span class="shrink-0 text-xs text-gray-500 dark:text-dark-400">
                {{ t('affiliate.customCodes.stats', { count: item.invite_count ?? 0, amount: formatCurrency(item.rebate_amount ?? 0) }) }}
              </span>
              <span v-if="item.retired" class="badge badge-gray shrink-0">{{ t('affiliate.customCodes.retiredBadge') }}</span>
              <button class="btn btn-secondary btn-sm shrink-0" :disabled="copyingId === item.code" @click="copyAny(item.code, item.code)">
                <Icon name="copy" size="sm" />
              </button>
              <button
                v-if="!item.retired"
                class="btn btn-secondary btn-sm shrink-0 text-red-600 dark:text-red-400"
                :disabled="deletingId === item.id"
                @click="removeCode(item)"
              >
                <Icon v-if="deletingId === item.id" name="refresh" size="sm" class="animate-spin" />
                <Icon v-else name="trash" size="sm" />
                <span class="sr-only">{{ t('affiliate.customCodes.remove') }}</span>
              </button>
            </li>
          </ul>

          <div v-if="customCodeQuota.limit > 0" class="mt-4 flex flex-col gap-2 sm:flex-row sm:items-center">
            <input
              v-model="newCode"
              type="text"
              maxlength="32"
              class="input flex-1"
              :placeholder="t('affiliate.customCodes.addPlaceholder')"
              @keyup.enter="addCode"
            />
            <button class="btn btn-primary shrink-0" :disabled="adding || !newCode.trim()" @click="addCode">
              <Icon v-if="adding" name="refresh" size="sm" class="animate-spin" />
              <Icon v-else name="plus" size="sm" />
              <span>{{ adding ? t('affiliate.customCodes.adding') : t('affiliate.customCodes.addButton') }}</span>
            </button>
          </div>
        </section>

        <section class="affiliate-section">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-base font-semibold text-highlighted">{{ t('affiliate.transfer.title') }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('affiliate.transfer.description') }}</p>
            </div>
            <button
              class="btn btn-primary"
              :disabled="transferring || detail.aff_quota <= 0"
              @click="transferQuota"
            >
              <Icon v-if="transferring" name="refresh" size="sm" class="animate-spin" />
              <Icon v-else name="dollar" size="sm" />
              <span>{{ transferring ? t('affiliate.transfer.transferring') : t('affiliate.transfer.button') }}</span>
            </button>
          </div>
          <p v-if="detail.aff_quota <= 0" class="mt-3 text-sm text-amber-600 dark:text-amber-400">
            {{ t('affiliate.transfer.empty') }}
          </p>
        </section>

        <section class="affiliate-section">
          <h2 class="text-base font-semibold text-highlighted">{{ t('affiliate.invitees.title') }}</h2>
          <div v-if="detail.invitees.length === 0" class="mt-4 rounded-xl border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
            {{ t('affiliate.invitees.empty') }}
          </div>
          <div v-else class="mt-4 overflow-x-auto">
            <table class="w-full min-w-[560px] text-left text-sm">
              <thead>
                <tr class="border-b border-gray-200 text-gray-500 dark:border-dark-700 dark:text-dark-400">
                  <th class="px-3 py-2 font-medium">{{ t('affiliate.invitees.columns.email') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('affiliate.invitees.columns.username') }}</th>
                  <th class="px-3 py-2 font-medium text-right">{{ t('affiliate.invitees.columns.rebate') }}</th>
                  <th class="px-3 py-2 font-medium">{{ t('affiliate.invitees.columns.joinedAt') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="item in detail.invitees"
                  :key="item.user_id"
                  class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
                >
                  <td class="px-3 py-3 text-gray-900 dark:text-white">{{ item.email || '-' }}</td>
                  <td class="px-3 py-3 text-gray-700 dark:text-gray-300">{{ item.username || '-' }}</td>
                  <td class="px-3 py-3 text-right font-medium text-emerald-600 dark:text-emerald-400">{{ formatCurrency(item.total_rebate) }}</td>
                  <td class="px-3 py-3 text-gray-700 dark:text-gray-300">{{ formatDateTime(item.created_at) || '-' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
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
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import Icon from '@/components/icons/Icon.vue'
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

<style scoped>
/* Sections as cards; the four figures as one joined strip. */
.affiliate-section { padding: 20px; border-radius: 8px; background: var(--ui-bg); box-shadow: 0 0 0 1px var(--ui-border), 0 1px 2px rgba(10, 10, 10, 0.05); }
.dark .affiliate-section { box-shadow: 0 0 0 1px var(--ui-border); }
.affiliate-metrics { overflow: hidden; border-radius: 8px; box-shadow: 0 0 0 1px var(--ui-border), 0 1px 2px rgba(10, 10, 10, 0.05); }
.dark .affiliate-metrics { box-shadow: 0 0 0 1px var(--ui-border); }
.console-signal .affiliate-metrics.signal-affiliate-metrics { border-block: 0; }
.console-signal .affiliate-metrics > .card { box-shadow: none; }
@media (min-width: 640px) { .affiliate-section { padding: 24px; } }
</style>
