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
    <slot name="beforeCreate"></slot>
    <button type="button" class="btn btn-primary account-create-action" @click="$emit('create')">
      <Icon name="plus" size="sm" />
      <span>{{ t('admin.accounts.createAccount') }}</span>
    </button>
    <slot name="afterCreate"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps(['loading'])
defineEmits(['refresh', 'create'])

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

.account-create-action {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
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

  .account-create-action {
    margin-left: auto;
  }
}

@media (max-width: 359px) {
  .account-table-actions {
    gap: 4px;
  }

  .account-create-action {
    padding-inline: 10px;
  }
}
</style>
