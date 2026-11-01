<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.editUser')"
    width="normal"
    @close="$emit('close')"
  >
    <UForm v-if="user" id="edit-user-form" :state="form" class="space-y-4" @submit="handleUpdateUser">
      <UFormField :label="t('admin.users.email')">
        <UInput v-model="form.email" type="email" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.users.password')">
        <div class="flex gap-2">
          <UInput v-model="form.password" class="flex-1" :placeholder="t('admin.users.enterNewPassword')" :ui="{ trailing: 'pe-1' }">
            <template v-if="form.password" #trailing>
              <UButton
                :color="passwordCopied ? 'success' : 'neutral'"
                variant="link"
                size="sm"
                :icon="passwordCopied ? 'i-lucide-check' : 'i-lucide-copy'"
                :aria-label="t('common.copy')"
                @click="copyPassword"
              />
            </template>
          </UInput>
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" square :aria-label="t('admin.users.generatePassword')" :title="t('admin.users.generatePassword')" @click="generatePassword" />
        </div>
      </UFormField>
      <UFormField :label="t('admin.users.username')">
        <UInput v-model="form.username" maxlength="50" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.users.form.roleLabel')">
        <Select v-model="form.role" :options="roleOptions" :searchable="false" />
      </UFormField>
      <UFormField :label="t('admin.users.notes')">
        <UTextarea v-model="form.notes" :rows="3" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.users.columns.concurrency')" :help="t('admin.users.form.concurrencyHint')">
        <UInput
          v-model.number="form.concurrency"
          type="number"
          min="0"
          step="1"
          class="w-full"
          :placeholder="t('admin.users.form.concurrencyPlaceholder')"
          data-test="concurrency-input"
        />
      </UFormField>
      <UCollapsible :unmount-on-hide="false" class="flex flex-col gap-4">
        <UButton
          color="neutral"
          variant="link"
          size="sm"
          class="self-start px-0"
          trailing-icon="i-lucide-chevron-down"
          :label="t('admin.users.advancedOptions')"
          :ui="{ trailingIcon: 'group-data-[state=open]:rotate-180 transition-transform' }"
        />
        <template #content>
          <div class="space-y-4">
            <UFormField :label="t('admin.users.form.rpmLimit')" :help="t('admin.users.form.rpmLimitHint')">
              <UInput
                v-model.number="form.rpm_limit"
                type="number"
                min="0"
                step="1"
                class="w-full"
                :placeholder="t('admin.users.form.rpmLimitPlaceholder')"
              />
            </UFormField>
            <UserAttributeForm v-model="form.customAttributes" :user-id="user?.id" />
          </div>
        </template>
      </UCollapsible>
    </UForm>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="$emit('close')" />
        <UButton type="submit" form="edit-user-form" :loading="submitting" :label="submitting ? t('admin.users.updating') : t('common.update')" />
      </div>
    </template>
  </BaseDialog>

  <!-- 角色提升为管理员时后端要求 step-up 2FA，弹出 TOTP 验证后自动重试 -->
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { adminAPI } from '@/api/admin'
import type { AdminUser, UserAttributeValuesMap } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import UserAttributeForm from '@/components/user/UserAttributeForm.vue'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isUsernameValid } from '@/utils/usernamePolicy'

const props = defineProps<{ show: boolean, user: AdminUser | null }>()
const emit = defineEmits(['close', 'success'])
const { t } = useI18n(); const appStore = useAppStore(); const { copyToClipboard } = useClipboard()

const submitting = ref(false); const passwordCopied = ref(false)
const roleOptions = computed(() => [
  { value: 'user', label: t('admin.users.roles.user') },
  { value: 'admin', label: t('admin.users.roles.admin') }
])
const form = reactive({
  email: '',
  password: '',
  username: '',
  notes: '',
  role: 'user' as AdminUser['role'],
  concurrency: 1,
  rpm_limit: 0,
  customAttributes: {} as UserAttributeValuesMap
})

watch(() => props.user, (u) => {
  if (u) {
    Object.assign(form, { email: u.email, password: '', username: u.username || '', notes: u.notes || '', role: u.role || 'user', concurrency: u.concurrency, rpm_limit: u.rpm_limit ?? 0, customAttributes: {} })
    passwordCopied.value = false
  }
}, { immediate: true })

const generatePassword = () => {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%^&*'
  let p = ''; for (let i = 0; i < 16; i++) p += chars.charAt(Math.floor(Math.random() * chars.length))
  form.password = p
}
const copyPassword = async () => {
  if (form.password && await copyToClipboard(form.password, t('admin.users.passwordCopied'))) {
    passwordCopied.value = true; setTimeout(() => passwordCopied.value = false, 2000)
  }
}
const stepUp = useStepUp()

const handleUpdateUser = async () => {
  if (!props.user) return
  if (!form.email.trim()) {
    appStore.showError(t('admin.users.emailRequired'))
    return
  }
  // 与后端 ValidateUsername 同口径：标记字符/超长用户名会被 400 拒绝，这里先提示。
  if (!isUsernameValid(form.username)) {
    appStore.showError(t('admin.users.usernameInvalid'))
    return
  }
  // 0 = 不限制，与网关 (AcquireUserSlot: maxConcurrency <= 0) 和批量改限额一致
  if (!Number.isInteger(form.concurrency) || form.concurrency < 0) {
    appStore.showError(t('admin.users.concurrencyNonNegative'))
    return
  }
  const userId = props.user.id
  submitting.value = true
  try {
    const data: any = { email: form.email, username: form.username, notes: form.notes, role: form.role, concurrency: form.concurrency, rpm_limit: form.rpm_limit }
    if (form.password.trim()) data.password = form.password.trim()
    // 提升为管理员属敏感操作：后端返回 STEP_UP_REQUIRED 时弹 TOTP 验证并重试
    await stepUp.run(() => adminAPI.users.update(userId, data))
    if (Object.keys(form.customAttributes).length > 0) await adminAPI.userAttributes.updateUserAttributeValues(userId, form.customAttributes)
    appStore.showSuccess(t('admin.users.userUpdated'))
    emit('success'); emit('close')
  } catch (e: any) {
    if (isStepUpCancelled(e)) {
      // 用户主动取消二次验证：静默返回，表单保持打开。
    } else if (isStepUpBlocked(e)) {
      appStore.showError(
        stepUpBlockReason(e) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled')
      )
    } else {
      appStore.showError(e?.message || t('admin.users.failedToUpdate'))
    }
  } finally { submitting.value = false }
}
</script>
