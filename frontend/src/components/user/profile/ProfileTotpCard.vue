<template>
  <!-- Security section of the dashboard template: title card with the enable/disable action,
       then a subtle card with the current state. -->
  <section>
    <UPageCard
      :title="t('profile.totp.title')"
      :description="t('profile.totp.description')"
      variant="naked"
      :orientation="hasAction ? 'horizontal' : 'vertical'"
      class="mb-4"
    >
      <template v-if="!loading">
        <UButton
          v-if="status?.enabled"
          data-testid="totp-disable"
          color="error"
          variant="outline"
          class="w-fit lg:ms-auto"
          @click="showDisableDialog = true"
        >
          {{ t('profile.totp.disable') }}
        </UButton>
        <UButton
          v-else-if="!status || status.feature_enabled"
          data-testid="totp-enable"
          class="w-fit lg:ms-auto"
          @click="showSetupModal = true"
        >
          {{ t('profile.totp.enable') }}
        </UButton>
      </template>
    </UPageCard>

    <UPageCard variant="subtle">
      <!-- Loading state -->
      <div v-if="loading" class="flex justify-center py-4">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>

      <!-- Feature disabled globally -->
      <div v-else-if="status && !status.enabled && !status.feature_enabled" class="flex items-start gap-3">
        <UIcon name="i-lucide-shield-off" class="mt-0.5 size-5 shrink-0 text-dimmed" />
        <div>
          <p class="text-sm font-medium text-highlighted">
            {{ t('profile.totp.featureDisabled') }}
          </p>
          <p class="text-sm text-muted">
            {{ t('profile.totp.featureDisabledHint') }}
          </p>
        </div>
      </div>

      <!-- 2FA Enabled -->
      <div v-else-if="status?.enabled" class="flex items-start gap-3">
        <UIcon name="i-lucide-shield-check" class="mt-0.5 size-5 shrink-0 text-success" />
        <div>
          <p class="text-sm font-medium text-highlighted">
            {{ t('profile.totp.enabled') }}
          </p>
          <p v-if="status.enabled_at" class="text-sm text-muted">
            {{ t('profile.totp.enabledAt') }}: {{ formatDate(status.enabled_at) }}
          </p>
        </div>
      </div>

      <!-- 2FA Not Enabled -->
      <div v-else class="flex items-start gap-3">
        <UIcon name="i-lucide-shield" class="mt-0.5 size-5 shrink-0 text-dimmed" />
        <div>
          <p class="text-sm font-medium text-highlighted">
            {{ t('profile.totp.notEnabled') }}
          </p>
          <p class="text-sm text-muted">
            {{ t('profile.totp.notEnabledHint') }}
          </p>
        </div>
      </div>
    </UPageCard>

    <!-- Setup Modal -->
    <TotpSetupModal
      v-if="showSetupModal"
      @close="showSetupModal = false"
      @success="handleSetupSuccess"
    />

    <!-- Disable Dialog -->
    <TotpDisableDialog
      v-if="showDisableDialog"
      @close="showDisableDialog = false"
      @success="handleDisableSuccess"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { totpAPI } from '@/api'
import type { TotpStatus } from '@/types'
import TotpSetupModal from './TotpSetupModal.vue'
import TotpDisableDialog from './TotpDisableDialog.vue'

const { t } = useI18n()

const loading = ref(true)
const status = ref<TotpStatus | null>(null)
const showSetupModal = ref(false)
const showDisableDialog = ref(false)
// Header shows an enable/disable button unless loading or enrollment is closed for this account.
const hasAction = computed(() => !loading.value && (!!status.value?.enabled || !status.value || status.value.feature_enabled))

const loadStatus = async () => {
  loading.value = true
  try {
    status.value = await totpAPI.getStatus()
  } catch (error) {
    console.error('Failed to load TOTP status:', error)
  } finally {
    loading.value = false
  }
}

const handleSetupSuccess = () => {
  showSetupModal.value = false
  loadStatus()
}

const handleDisableSuccess = () => {
  showDisableDialog.value = false
  loadStatus()
}

const formatDate = (timestamp: number) => {
  // Backend returns Unix timestamp in seconds, convert to milliseconds
  const date = new Date(timestamp * 1000)
  return date.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  })
}

onMounted(() => {
  loadStatus()
})
</script>
