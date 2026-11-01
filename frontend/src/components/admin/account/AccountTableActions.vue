<template>
  <div class="account-table-actions">
    <slot name="before"></slot>
    <UButton
      color="neutral"
      variant="outline"
      icon="i-lucide-refresh-cw"
      square
      :loading="loading"
      :title="t('common.refresh')"
      :aria-label="t('common.refresh')"
      @click="$emit('refresh')"
    />
    <slot name="after"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// The create button lives in the page header (AccountsView); this is the toolbar's right side.
defineProps(['loading'])
defineEmits(['refresh'])

const { t } = useI18n()
</script>

<style scoped>
.account-table-actions {
  display: flex;
  flex: 0 0 auto;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
}

@media (max-width: 767px) {
  .account-table-actions {
    justify-content: flex-start;
  }
}
</style>
