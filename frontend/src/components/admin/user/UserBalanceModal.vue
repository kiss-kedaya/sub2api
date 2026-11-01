<template>
  <BaseDialog :show="show" :title="operation === 'add' ? t('admin.users.deposit') : t('admin.users.withdraw')" width="narrow" @close="$emit('close')">
    <UForm v-if="user" id="balance-form" :state="form" class="space-y-4" @submit="handleBalanceSubmit">
      <div class="flex items-center gap-3">
        <UAvatar :alt="user.email" size="md" />
        <div class="min-w-0 flex-1">
          <p class="truncate font-medium text-highlighted">{{ user.email }}</p>
          <p class="text-sm text-muted">{{ t('admin.users.currentBalance') }}: ${{ formatBalance(user.balance) }}</p>
        </div>
      </div>
      <UFormField :label="operation === 'add' ? t('admin.users.depositAmount') : t('admin.users.withdrawAmount')" required>
        <div class="flex gap-2">
          <UInput v-model.number="form.amount" type="number" step="any" min="0" required class="flex-1">
            <template #leading><span class="text-muted">$</span></template>
          </UInput>
          <UButton v-if="operation === 'subtract'" color="neutral" variant="outline" :label="t('admin.users.withdrawAll')" @click="fillAllBalance" />
        </div>
      </UFormField>
      <UFormField :label="t('admin.users.notes')">
        <UTextarea v-model="form.notes" :rows="3" class="w-full" />
      </UFormField>
      <p v-if="form.amount > 0" class="flex items-center justify-between text-sm">
        <span class="text-muted">{{ t('admin.users.newBalance') }}:</span>
        <span class="font-semibold tabular-nums text-highlighted">${{ formatBalance(calculateNewBalance()) }}</span>
      </p>
    </UForm>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="$emit('close')" />
        <UButton
          type="submit"
          form="balance-form"
          :color="operation === 'add' ? 'success' : 'error'"
          :loading="submitting"
          :disabled="submitting || !form.amount"
          :label="submitting ? t('common.saving') : t('common.confirm')"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{ show: boolean, user: AdminUser | null, operation: 'add' | 'subtract' }>()
const emit = defineEmits(['close', 'success']); const { t } = useI18n(); const appStore = useAppStore()

const submitting = ref(false); const form = reactive({ amount: 0, notes: '' })
watch(() => props.show, (v) => { if(v) { form.amount = 0; form.notes = '' } })

// 格式化余额：显示完整精度，去除尾部多余的0
const formatBalance = (value: number) => {
  if (value === 0) return '0.00'
  // 最多保留8位小数，去除尾部的0
  const formatted = value.toFixed(8).replace(/\.?0+$/, '')
  // 确保至少有2位小数
  const parts = formatted.split('.')
  if (parts.length === 1) return formatted + '.00'
  if (parts[1].length === 1) return formatted + '0'
  return formatted
}

// 填入全部余额
const fillAllBalance = () => {
  if (props.user) {
    form.amount = props.user.balance
  }
}

const calculateNewBalance = () => {
  if (!props.user) return 0
  const result = props.operation === 'add' ? props.user.balance + form.amount : props.user.balance - form.amount
  // 避免浮点数精度问题导致的 -0.00 显示
  return Math.abs(result) < 1e-10 ? 0 : result
}
const handleBalanceSubmit = async () => {
  if (!props.user) return
  if (!form.amount || form.amount <= 0) {
    appStore.showError(t('admin.users.amountRequired'))
    return
  }
  // 退款时验证金额不超过实际余额
  if (props.operation === 'subtract' && form.amount > props.user.balance) {
    appStore.showError(t('admin.users.insufficientBalance'))
    return
  }
  submitting.value = true
  try {
    await adminAPI.users.updateBalance(props.user.id, form.amount, props.operation, form.notes)
    appStore.showSuccess(t('common.success')); emit('success'); emit('close')
  } catch (e: any) {
    console.error('Failed to update balance:', e)
    appStore.showError(e.response?.data?.detail || t('common.error'))
  } finally { submitting.value = false }
}
</script>
