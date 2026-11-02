<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.syncFromCrsTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <!-- Step 1: Input credentials -->
    <form
      v-if="currentStep === 'input'"
      id="sync-from-crs-form"
      class="space-y-4"
      @submit.prevent="handlePreview"
    >
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.syncFromCrsDesc') }}
      </div>
      <div
        class="rounded-lg bg-gray-50 p-3 text-xs text-gray-500 dark:bg-dark-700/60 dark:text-dark-400"
      >
        {{ t('admin.accounts.crsUpdateBehaviorNote') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ t('admin.accounts.crsVersionRequirement') }}
      </div>

      <div class="grid grid-cols-1 gap-4">
        <UFormField :label="t('admin.accounts.crsBaseUrl')" required>
          <UInput id="crs-base-url" v-model="form.base_url" class="w-full" required :placeholder="t('admin.accounts.crsBaseUrlPlaceholder')" />
        </UFormField>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <UFormField :label="t('admin.accounts.crsUsername')" required>
            <UInput id="crs-username" v-model="form.username" class="w-full" required autocomplete="username" />
          </UFormField>
          <UFormField :label="t('admin.accounts.crsPassword')" required>
            <UInput id="crs-password" v-model="form.password" type="password" class="w-full" required autocomplete="current-password" />
          </UFormField>
        </div>

        <UCheckbox v-model="form.sync_proxies" :label="t('admin.accounts.syncProxies')" />
      </div>
    </form>

    <!-- Step 2: Preview & select -->
    <div v-else-if="currentStep === 'preview' && previewResult" class="space-y-4">
      <!-- Existing accounts (read-only info) -->
      <div
        v-if="previewResult.existing_accounts.length"
        class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700/60"
      >
        <div class="mb-2 text-sm font-medium text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.crsExistingAccounts') }}
          <span class="ml-1 text-xs text-dimmed">({{ previewResult.existing_accounts.length }})</span>
        </div>
        <div class="max-h-32 overflow-auto text-xs text-gray-500 dark:text-dark-400">
          <div
            v-for="acc in previewResult.existing_accounts"
            :key="acc.crs_account_id"
            class="flex items-center gap-2 py-0.5"
          >
            <span
              class="inline-block rounded-sm bg-blue-100 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400"
            >{{ acc.platform }} / {{ acc.type }}</span>
            <span class="truncate">{{ acc.name }}</span>
          </div>
        </div>
      </div>

      <!-- New accounts (selectable) -->
      <div v-if="previewResult.new_accounts.length">
        <div class="mb-2 flex items-center justify-between">
          <div class="text-sm font-medium text-highlighted">
            {{ t('admin.accounts.crsNewAccounts') }}
            <span class="ml-1 text-xs text-dimmed">({{ previewResult.new_accounts.length }})</span>
          </div>
          <div class="flex gap-2">
            <UButton variant="link" size="xs" class="p-0" :label="t('admin.accounts.crsSelectAll')" @click="selectAll" />
            <UButton color="neutral" variant="link" size="xs" class="p-0" :label="t('admin.accounts.crsSelectNone')" @click="selectNone" />
          </div>
        </div>
        <div
          class="max-h-48 overflow-auto rounded-md bg-muted/50 p-1"
        >
          <UCheckbox
            v-for="acc in previewResult.new_accounts"
            :key="acc.crs_account_id"
            :model-value="selectedIds.has(acc.crs_account_id)"
            class="rounded-md px-2 py-1.5 hover:bg-elevated"
            @update:model-value="toggleSelect(acc.crs_account_id)"
          >
            <template #label>
              <span class="flex min-w-0 items-center gap-2">
                <UBadge color="neutral" variant="subtle" size="sm">{{ acc.platform }} / {{ acc.type }}</UBadge>
                <span class="truncate text-sm text-default">{{ acc.name }}</span>
              </span>
            </template>
          </UCheckbox>
        </div>
        <div class="mt-1 text-xs text-gray-400">
          {{ t('admin.accounts.crsSelectedCount', { count: selectedIds.size }) }}
        </div>
      </div>

      <!-- Sync options summary -->
      <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
        <span>{{ t('admin.accounts.syncProxies') }}:</span>
        <span :class="form.sync_proxies ? 'text-green-600 dark:text-green-400' : 'text-gray-400 dark:text-dark-500'">
          {{ form.sync_proxies ? t('common.yes') : t('common.no') }}
        </span>
      </div>

      <!-- No new accounts -->
      <div
        v-if="!previewResult.new_accounts.length"
        class="rounded-lg bg-gray-50 p-4 text-center text-sm text-gray-500 dark:bg-dark-700/60 dark:text-dark-400"
      >
        {{ t('admin.accounts.crsNoNewAccounts') }}
        <span v-if="previewResult.existing_accounts.length">
          {{ t('admin.accounts.crsWillUpdate', { count: previewResult.existing_accounts.length }) }}
        </span>
      </div>
    </div>

    <!-- Step 3: Result -->
    <div v-else-if="currentStep === 'result' && result" class="space-y-4">
      <div
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-highlighted">
          {{ t('admin.accounts.syncResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.syncResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.syncErrors') }}
          </div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.crs_account_id }} — {{ item.action
              }}{{ item.error ? `: ${item.error}` : '' }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <!-- Step 1: Input -->
        <template v-if="currentStep === 'input'">
          <UButton color="neutral" variant="subtle" :disabled="previewing" :label="t('common.cancel')" @click="handleClose" />
          <UButton type="submit" form="sync-from-crs-form" :disabled="previewing" :label="previewing ? t('admin.accounts.crsPreviewing') : t('admin.accounts.crsPreview')" />
        </template>

        <!-- Step 2: Preview -->
        <template v-else-if="currentStep === 'preview'">
          <UButton color="neutral" variant="subtle" :disabled="syncing" :label="t('admin.accounts.crsBack')" @click="handleBack" />
          <UButton :disabled="syncing || hasNewButNoneSelected" :label="syncing ? t('admin.accounts.syncing') : t('admin.accounts.syncNow')" @click="handleSync" />
        </template>

        <!-- Step 3: Result -->
        <template v-else-if="currentStep === 'result'">
          <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="handleClose" />
        </template>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { PreviewFromCRSResult } from '@/api/admin/accounts'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'synced'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

type Step = 'input' | 'preview' | 'result'
const currentStep = ref<Step>('input')
const previewing = ref(false)
const syncing = ref(false)
const previewResult = ref<PreviewFromCRSResult | null>(null)
const selectedIds = ref(new Set<string>())
const result = ref<Awaited<ReturnType<typeof adminAPI.accounts.syncFromCrs>> | null>(null)

const form = reactive({
  base_url: '',
  username: '',
  password: '',
  sync_proxies: true
})

const hasNewButNoneSelected = computed(() => {
  if (!previewResult.value) return false
  return previewResult.value.new_accounts.length > 0 && selectedIds.value.size === 0
})

const errorItems = computed(() => {
  if (!result.value?.items) return []
  return result.value.items.filter(
    (i) => i.action === 'failed' || (i.action === 'skipped' && i.error !== 'not selected')
  )
})

watch(
  () => props.show,
  (open) => {
    if (open) {
      currentStep.value = 'input'
      previewResult.value = null
      selectedIds.value = new Set()
      result.value = null
      form.base_url = ''
      form.username = ''
      form.password = ''
      form.sync_proxies = true
    }
  }
)

const handleClose = () => {
  if (syncing.value || previewing.value) {
    return
  }
  emit('close')
}

const handleBack = () => {
  currentStep.value = 'input'
  previewResult.value = null
  selectedIds.value = new Set()
}

const selectAll = () => {
  if (!previewResult.value) return
  selectedIds.value = new Set(previewResult.value.new_accounts.map((a) => a.crs_account_id))
}

const selectNone = () => {
  selectedIds.value = new Set()
}

const toggleSelect = (id: string) => {
  const s = new Set(selectedIds.value)
  if (s.has(id)) {
    s.delete(id)
  } else {
    s.add(id)
  }
  selectedIds.value = s
}

const handlePreview = async () => {
  if (!form.base_url.trim() || !form.username.trim() || !form.password.trim()) {
    appStore.showError(t('admin.accounts.syncMissingFields'))
    return
  }

  previewing.value = true
  try {
    const res = await adminAPI.accounts.previewFromCrs({
      base_url: form.base_url.trim(),
      username: form.username.trim(),
      password: form.password
    })
    previewResult.value = res
    // Auto-select all new accounts
    selectedIds.value = new Set(res.new_accounts.map((a) => a.crs_account_id))
    currentStep.value = 'preview'
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.crsPreviewFailed'))
  } finally {
    previewing.value = false
  }
}

const handleSync = async () => {
  if (!form.base_url.trim() || !form.username.trim() || !form.password.trim()) {
    appStore.showError(t('admin.accounts.syncMissingFields'))
    return
  }

  syncing.value = true
  try {
    const res = await adminAPI.accounts.syncFromCrs({
      base_url: form.base_url.trim(),
      username: form.username.trim(),
      password: form.password,
      sync_proxies: form.sync_proxies,
      selected_account_ids: [...selectedIds.value]
    })
    result.value = res
    currentStep.value = 'result'

    if (res.failed > 0) {
      appStore.showError(t('admin.accounts.syncCompletedWithErrors', res))
    } else {
      appStore.showSuccess(t('admin.accounts.syncCompleted', res))
    }
    emit('synced')
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.syncFailed'))
  } finally {
    syncing.value = false
  }
}
</script>
