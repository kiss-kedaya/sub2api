<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.balanceAlert.title')"
    width="normal"
    @close="emit('close')"
  >
    <div class="space-y-5">
      <!-- Enable toggle -->
      <div class="flex items-center justify-between">
        <div>
          <p class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.accounts.balanceAlert.enabled') }}</p>
          <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">{{ t('admin.accounts.balanceAlert.enabledHint') }}</p>
        </div>
        <Toggle v-model="form.enabled" @update:modelValue="handleToggleChange" />
      </div>

      <template v-if="form.enabled">
        <!-- Threshold -->
        <div>
          <label class="input-label">{{ t('admin.accounts.balanceAlert.threshold') }}</label>
          <div class="flex items-center gap-2">
            <span class="text-gray-500">$</span>
            <input
              v-model.number="form.threshold"
              type="number"
              min="0"
              step="0.01"
              class="input flex-1"
              :placeholder="t('admin.accounts.balanceAlert.thresholdPlaceholder')"
            />
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.balanceAlert.thresholdHint') }}</p>
        </div>

        <!-- Recharge URL -->
        <div>
          <label class="input-label">{{ t('admin.accounts.balanceAlert.rechargeUrl') }}</label>
          <input
            v-model="form.rechargeUrl"
            type="url"
            class="input"
            :placeholder="currentOrigin"
          />
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="saving" @click="handleSave">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

interface Props {
  show: boolean
  /** 当前系统设置初始值（由父组件加载后传入）。 */
  enabled: boolean
  threshold: number
  rechargeUrl: string
}

const props = defineProps<Props>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'saved'): void }>()

const { t } = useI18n()
const appStore = useAppStore()

const currentOrigin = typeof window !== 'undefined' ? window.location.origin : ''

const form = ref({
  enabled: props.enabled,
  threshold: props.threshold,
  rechargeUrl: props.rechargeUrl,
})

// 每次 modal 打开时同步最新 props 到表单
watch(
  () => props.show,
  (open) => {
    if (open) {
      form.value = {
        enabled: props.enabled,
        threshold: props.threshold,
        rechargeUrl: props.rechargeUrl,
      }
    }
  }
)

const saving = ref(false)

const handleToggleChange = async (val: boolean) => {
  // 直接持久化开关，不等 Save 按钮
  try {
    await adminAPI.settings.updateSettings({ balance_low_notify_enabled: val })
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
    form.value.enabled = !val
  }
}

const handleSave = async () => {
  saving.value = true
  try {
    await adminAPI.settings.updateSettings({
      balance_low_notify_enabled: form.value.enabled,
      balance_low_notify_threshold: form.value.threshold ?? 0,
      balance_low_notify_recharge_url: form.value.rechargeUrl ?? '',
    })
    appStore.showSuccess(t('common.saved'))
    emit('saved')
    emit('close')
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
