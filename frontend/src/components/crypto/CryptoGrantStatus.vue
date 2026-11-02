<template>
  <UBadge v-if="remaining > 0" class="crypto-grant-status whitespace-nowrap" role="status" color="neutral" variant="subtle" icon="i-lucide-timer">
    {{ t('admin.crypto.securityGrantActive', { minutes: Math.ceil(remaining / 60) }) }}
  </UBadge>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { cryptoGrantExpiresAt, getCryptoGrant } from '@/composables/cryptoGrant'

const { t } = useI18n()
const remaining = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
function tick() {
  remaining.value = getCryptoGrant() ? Math.max(0, Math.ceil((cryptoGrantExpiresAt.value - Date.now()) / 1000)) : 0
}
onMounted(() => { tick(); timer = setInterval(tick, 1000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>
