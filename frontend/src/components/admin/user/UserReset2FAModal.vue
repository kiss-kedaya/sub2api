<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.reset2fa.title')"
    width="normal"
    :close-on-click-outside="false"
    @close="handleClose"
  >
    <UForm v-if="user" id="reset-2fa-form" :state="form" class="space-y-4" @submit="handleSubmit">
      <!-- 目标用户：重置前必须让管理员再确认一遍对象，避免点错行 -->
      <div class="flex items-center gap-3 rounded-lg border border-default p-3">
        <UAvatar :alt="user.email" :text="user.email.charAt(0).toUpperCase()" size="md" />
        <div class="min-w-0 flex-1">
          <p class="truncate font-medium text-highlighted" data-test="reset-2fa-target-email">{{ user.email }}</p>
          <p class="mt-0.5 text-xs text-muted">
            {{ t('admin.users.reset2fa.targetHint', { id: user.id, role: user.role === 'admin' ? t('admin.users.admin') : t('admin.users.user') }) }}
          </p>
        </div>
        <UBadge color="error" variant="subtle" size="sm" :label="t('admin.users.reset2fa.dangerBadge')" />
      </div>

      <!-- 高风险警告：重置后 2FA 失效、必须重新绑定、会审计并通知用户 -->
      <UAlert
        color="error"
        variant="subtle"
        icon="i-lucide-shield-alert"
        data-test="reset-2fa-warning"
        :title="t('admin.users.reset2fa.warningTitle')"
        :description="t('admin.users.reset2fa.warningBody')"
      />

      <UAlert
        v-if="isTargetAdmin"
        color="warning"
        variant="subtle"
        icon="i-lucide-user-cog"
        :description="t('admin.users.reset2fa.adminTargetWarning')"
      />

      <UAlert
        color="info"
        variant="subtle"
        icon="i-lucide-key-round"
        :description="t('admin.users.reset2fa.passkeyNotice')"
      />

      <UFormField
        :label="t('admin.users.reset2fa.reasonLabel')"
        required
        :error="reasonError"
        :help="t('admin.users.reset2fa.reasonHelp')"
      >
        <UTextarea
          v-model="form.reason"
          :rows="4"
          autoresize
          class="w-full"
          :placeholder="t('admin.users.reset2fa.reasonPlaceholder')"
          :ui="{ base: reasonCounterUi }"
          data-test="reset-2fa-reason"
          :aria-label="t('admin.users.reset2fa.reasonLabel')"
        />
        <template #hint>
          <span
            class="tabular-nums"
            :class="reasonTooLong ? 'text-error' : 'text-muted'"
            data-test="reset-2fa-reason-counter"
          >{{ reasonLength }}/{{ MAX_REASON_LENGTH }}</span>
        </template>
      </UFormField>

      <UAlert
        v-if="errorMessage"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        data-test="reset-2fa-error"
        :description="errorMessage"
      />
    </UForm>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton
          color="neutral"
          variant="subtle"
          :label="t('common.cancel')"
          :disabled="submitting"
          @click="handleClose"
        />
        <!-- 二次确认：只有原因合法（1~500 字）才能点 -->
        <UButton
          type="submit"
          form="reset-2fa-form"
          color="error"
          variant="solid"
          icon="i-lucide-shield-off"
          :loading="submitting"
          :disabled="!canSubmit"
          :label="submitting ? t('admin.users.reset2fa.submitting') : t('admin.users.reset2fa.confirm')"
          data-test="reset-2fa-confirm"
        />
      </div>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminUser } from '@/types'
import type { ResetUser2FAResponse } from '@/api/admin/users'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'

/** 与后端 service.Admin2FAResetReasonMaxLength 保持一致（按字符计）。 */
const MAX_REASON_LENGTH = 500

const props = defineProps<{ show: boolean, user: AdminUser | null }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'success', result: ResetUser2FAResponse): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()

const submitting = ref(false)
const errorMessage = ref('')
const form = reactive({ reason: '' })

// 按字符（码点）计长度，与后端 utf8.RuneCountInString 对齐：中文一句 20 个字，
// 不能因为 UTF-8 三字节被算成 60。
const reasonLength = computed(() => Array.from(form.reason.trim()).length)
const reasonTooLong = computed(() => reasonLength.value > MAX_REASON_LENGTH)
const reasonError = computed(() => {
  if (form.reason.length === 0) return ''
  // 只在用户已经输入后提示，避免刚打开就一片红。
  if (form.reason.trim().length === 0) return t('admin.users.reset2fa.reasonRequired')
  if (reasonTooLong.value) return t('admin.users.reset2fa.reasonTooLong', { max: MAX_REASON_LENGTH })
  return ''
})
const canSubmit = computed(
  () => !submitting.value && reasonLength.value >= 1 && !reasonTooLong.value,
)
const isTargetAdmin = computed(() => props.user?.role === 'admin')
const reasonCounterUi = computed(() =>
  reasonTooLong.value ? 'ring-1 ring-error/60' : '',
)

// 每次打开都清空：原因不能复用上一次的（审计要写真实原因）。
watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    form.reason = ''
    errorMessage.value = ''
    submitting.value = false
  },
)

function handleClose() {
  if (submitting.value) return
  emit('close')
}

async function handleSubmit() {
  if (!props.user || !canSubmit.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    // 破玻璃重置属高风险管理操作：后端要求 step-up 二次验证，这里弹码并重试一次。
    const result = await stepUp.run(() => adminAPI.users.resetUser2FA(props.user!.id, form.reason.trim()))
    // 邮件没发出去时必须明确告诉管理员——不能报「全部完成」。
    if (!result.notification_queued) {
      appStore.showSuccess(t('admin.users.reset2fa.successWithoutEmail', { email: props.user!.email }), 8000)
    } else if (result.passkey_bound) {
      appStore.showSuccess(t('admin.users.reset2fa.successWithPasskey', { email: props.user!.email }), 6000)
    } else {
      appStore.showSuccess(t('admin.users.reset2fa.success', { email: props.user!.email }))
    }
    if (!result.revoked_step_up_grants) {
      appStore.showError(t('admin.users.reset2fa.stepUpNotRevoked'), 8000)
    }
    emit('success', result)
    emit('close')
  } catch (error: any) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      errorMessage.value = stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
        ? t('stepUp.adminApiKeyForbidden')
        : t('stepUp.notEnabled')
      return
    }
    const code = error?.response?.data?.reason as string | undefined
    errorMessage.value = reset2FAErrorMessage(code, error)
  } finally {
    submitting.value = false
  }
}

/**
 * 把后端的机器码翻成本地化提示；未知码一律回落到后端 message（已是人话）。
 */
function reset2FAErrorMessage(code: string | undefined, error: any): string {
  switch (code) {
    case 'RESET_2FA_REASON_REQUIRED':
      return t('admin.users.reset2fa.reasonRequired')
    case 'RESET_2FA_REASON_TOO_LONG':
      return t('admin.users.reset2fa.reasonTooLong', { max: MAX_REASON_LENGTH })
    case 'RESET_2FA_TARGET_NOT_ENABLED':
      return t('admin.users.reset2fa.targetNotEnabled')
    case 'USER_NOT_FOUND':
      return t('admin.users.reset2fa.userNotFound')
    case 'RESET_2FA_UNAVAILABLE':
      return t('admin.users.reset2fa.unavailable')
    default:
      return error?.response?.data?.message || t('admin.users.reset2fa.failed')
  }
}
</script>
