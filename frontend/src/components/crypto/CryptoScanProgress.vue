<template>
  <UAlert v-if="scanning || activeScan" :color="alertColor" variant="subtle" :icon="running ? 'i-lucide-loader-circle' : 'i-lucide-circle-alert'" :title="alertTitle" data-testid="crypto-scan-progress">
    <template #description>
      <div class="space-y-0.5">
        <p v-if="running">{{ t('admin.crypto.scanDepositsRunningHint') }}</p>
        <p v-if="scopeLine">{{ scopeLine }}</p>
        <p v-if="progressLine">{{ progressLine }}</p>
        <p v-for="line in chainLines" :key="line">{{ line }}</p>
        <p v-if="activeScan?.last_error">{{ t('admin.crypto.scanRunLastError', { message: activeScan.last_error }) }}</p>
      </div>
    </template>
    <template v-if="running && activeScan" #actions>
      <UButton color="neutral" variant="outline" size="xs" :loading="cancelling" :disabled="cancelling" @click="cancel">{{ t('admin.crypto.scanRunCancel') }}</UButton>
    </template>
  </UAlert>
</template>
<script setup lang="ts">
import { inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { cryptoScanKey } from '@/composables/useCryptoScan'

const { t } = useI18n()
const { scanning, activeScan, cancelling, running, alertColor, alertTitle, scopeLine, progressLine, chainLines, cancel } = inject(cryptoScanKey)!
</script>
