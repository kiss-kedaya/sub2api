<template>
  <BaseDialog
    :show="show"
    :title="dialogTitle"
    width="extra-wide"
    @close="handleClose"
  >
    <AccountModelTestPanel
      ref="panel"
      :show="show"
      :accounts="accounts"
      :variant="variant"
      :build-body="buildBody"
    />
    <template #footer>
      <div class="batch-test-footer">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import AccountModelTestPanel from '@/components/admin/account/AccountModelTestPanel.vue'
import type { BatchTestAccount } from '@/utils/accountModelTest'

const props = withDefaults(defineProps<{
  show: boolean
  accounts: BatchTestAccount[]
  variant?: 'single' | 'batch'
  buildBody?: (modelId: string, account: BatchTestAccount) => Record<string, unknown>
}>(), {
  variant: 'batch'
})

const emit = defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()
const panel = ref<InstanceType<typeof AccountModelTestPanel> | null>(null)

const dialogTitle = computed(() => {
  if (props.variant === 'single') return t('admin.accounts.testAccountConnection')
  return t('admin.accounts.batchTest.title')
})

const handleClose = () => {
  panel.value?.stopRun()
  emit('close')
}
</script>

<style scoped>
.batch-test-footer { display: flex; justify-content: flex-end; }
@media (max-width: 767px) {
  .batch-test-footer .btn { width: 100%; min-height: 40px; }
}
</style>
