<template>
  <div class="account-table-actions">
    <slot name="before"></slot>
    <button
      type="button"
      class="btn btn-secondary account-icon-action"
      :disabled="loading"
      :title="t('common.refresh')"
      :aria-label="t('common.refresh')"
      @click="$emit('refresh')"
    >
      <Icon name="refresh" size="md" :class="[loading ? 'animate-spin' : '']" />
    </button>
    <slot name="after"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

// The create button lives in the page header (AccountsView); this is the toolbar's right side.
defineProps(['loading'])
defineEmits(['refresh'])

const { t } = useI18n()
</script>

<style scoped>
.account-table-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
}

.account-icon-action {
  width: 40px;
  min-width: 40px;
  padding-inline: 0;
}

@media (max-width: 767px) {
  .account-table-actions {
    width: 100%;
  }

  .account-table-actions :deep(.btn) {
    min-height: 44px;
  }

  .account-icon-action {
    width: 44px;
    min-width: 44px;
  }

}

@media (max-width: 359px) {
  .account-table-actions {
    gap: 4px;
  }
}
</style>
