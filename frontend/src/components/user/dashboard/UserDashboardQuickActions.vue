<template>
  <nav class="signal-actions" :aria-label="t('dashboard.quickActions')">
    <span class="signal-actions-label">{{ t('dashboard.quickActions') }}</span>
    <div class="signal-actions-list">
      <button @click="router.push('/infinite-canvas')"><Icon name="grid" size="sm" /><span>{{ t('dashboard.infiniteCanvas') }}</span></button>
      <button @click="router.push('/keys')"><Icon name="key" size="sm" /><span>{{ t('dashboard.createApiKey') }}</span></button>
      <button @click="router.push('/usage')"><Icon name="chart" size="sm" /><span>{{ t('dashboard.viewUsage') }}</span></button>
      <button v-if="canUseBatchImage" @click="router.push('/batch-image')"><Icon name="sparkles" size="sm" /><span>{{ t('dashboard.batchImageAgent') }}</span></button>
      <button @click="router.push('/redeem')"><Icon name="gift" size="sm" /><span>{{ t('dashboard.redeemCode') }}</span></button>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
const router = useRouter()
const { t } = useI18n()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

onMounted(() => {
  void refreshBatchImageAccess()
})
</script>

<style scoped>
.signal-actions { display: flex; align-items: center; gap: 18px; padding: 12px 0; min-width: 0; }
.signal-actions-label { flex-shrink: 0; font-size: 11px; color: var(--signal-muted, #65716a); }
.signal-actions-list { display: flex; flex-wrap: wrap; gap: 4px 8px; min-width: 0; }
.signal-actions button { display: inline-flex; align-items: center; justify-content: flex-start; gap: 7px; min-height: 36px; padding: 6px 9px; border-radius: 4px; font-size: 12px; font-weight: 500; color: var(--signal-text, #202423); text-align: left; }
.signal-actions button :deep(svg) { color: var(--signal-accent, #087f68); flex-shrink: 0; }
.signal-actions button:hover { background: var(--signal-surface, #fff); color: var(--signal-accent, #087f68); }
.signal-actions button:focus-visible { outline: 2px solid var(--signal-accent, #087f68); outline-offset: 2px; }
@media (max-width: 640px) {
  .signal-actions { align-items: flex-start; gap: 8px; padding: 10px 0; }
  .signal-actions-label { padding-top: 14px; }
  .signal-actions-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); flex: 1; gap: 0 6px; }
  .signal-actions button { min-height: 44px; padding: 7px 4px; overflow-wrap: anywhere; }
}
</style>
