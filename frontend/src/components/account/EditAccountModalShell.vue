<template>
  <BaseDialog :show="show" :title="t('admin.accounts.editAccount')" width="wide" @close="$emit('close')">
    <div data-testid="edit-account-shell" class="space-y-6">
      <div class="flex items-center gap-3">
        <USkeleton class="size-9 rounded-full" />
        <div class="flex-1 space-y-2">
          <USkeleton class="h-4 w-40" />
          <USkeleton class="h-3 w-24" />
        </div>
      </div>
      <div class="space-y-3">
        <USkeleton class="h-3 w-20" />
        <USkeleton class="h-9 w-full rounded-md" />
        <USkeleton class="h-3 w-20" />
        <USkeleton class="h-20 w-full rounded-md" />
      </div>
      <p class="text-xs text-muted">{{ t('admin.accounts.editLoading') }}</p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'

defineProps<{ show: boolean }>()
defineEmits<{ close: [] }>()

const { t } = useI18n()
</script>
