<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.createUser')"
    width="normal"
    @close="$emit('close')"
  >
    <UForm id="create-user-form" :state="form" class="space-y-4" @submit="submit">
      <UFormField :label="t('admin.users.email')" required>
        <UInput v-model="form.email" type="email" required class="w-full" :placeholder="t('admin.users.enterEmail')" />
      </UFormField>
      <UFormField :label="t('admin.users.password')" required>
        <div class="flex gap-2">
          <UInput v-model="form.password" required class="flex-1" :placeholder="t('admin.users.enterPassword')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" square :aria-label="t('admin.users.generatePassword')" :title="t('admin.users.generatePassword')" @click="generateRandomPassword" />
        </div>
      </UFormField>
      <UFormField :label="t('admin.users.username')">
        <UInput v-model="form.username" maxlength="50" class="w-full" :placeholder="t('admin.users.enterUsername')" />
      </UFormField>
      <UFormField :label="t('admin.users.form.roleLabel')">
        <USelect v-model="form.role" :items="roleItems" class="w-full" />
      </UFormField>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <UFormField :label="t('admin.users.columns.balance')">
          <UInput v-model="form.balance" type="number" step="any" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.users.columns.concurrency')">
          <UInput v-model.number="form.concurrency" type="number" min="0" step="1" class="w-full" />
        </UFormField>
      </div>
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
          <UFormField :label="t('admin.users.form.rpmLimit')" :help="t('admin.users.form.rpmLimitHint')">
            <UInput v-model.number="form.rpm_limit" type="number" min="0" step="1" class="w-full" :placeholder="t('admin.users.form.rpmLimitPlaceholder')" />
          </UFormField>
        </template>
      </UCollapsible>
    </UForm>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="$emit('close')" />
        <UButton type="submit" form="create-user-form" :loading="loading" :label="loading ? t('admin.users.creating') : t('common.create')" />
      </div>
    </template>
  </BaseDialog>

  <!-- 创建管理员账号时后端要求 step-up 2FA，弹出 TOTP 验证后自动重试 -->
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'; import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isUsernameValid } from '@/utils/usernamePolicy'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits(['close', 'success']); const { t } = useI18n()
const appStore = useAppStore()

const form = reactive({ email: '', password: '', username: '', notes: '', role: 'user' as 'user' | 'admin', balance: '', concurrency: 1, rpm_limit: 0 })
const roleItems = computed(() => [
  { label: t('admin.users.roles.user'), value: 'user' },
  { label: t('admin.users.roles.admin'), value: 'admin' }
])

const stepUp = useStepUp()
const loading = ref(false)

const submit = async () => {
  if (loading.value) return
  // 与后端 ValidateUsername 同口径：标记字符/超长用户名会被 400 拒绝，这里先提示。
  if (!isUsernameValid(form.username)) {
    appStore.showError(t('admin.users.usernameInvalid'))
    return
  }
  loading.value = true
  try {
    const { balance: rawBalance, ...rest } = { ...form }
    const balance = String(rawBalance).trim()
    const payload: typeof rest & { balance?: number } = { ...rest }
    if (balance !== '') {
      payload.balance = Number(balance)
    }
    // 创建管理员属敏感操作：后端返回 STEP_UP_REQUIRED 时弹 TOTP 验证并重试
    await stepUp.run(() => adminAPI.users.create(payload))
    appStore.showSuccess(t('admin.users.userCreated'))
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
      appStore.showError(e?.message || t('admin.users.failedToCreate'))
    }
  } finally { loading.value = false }
}

watch(() => props.show, (v) => { if(v) Object.assign(form, { email: '', password: '', username: '', notes: '', role: 'user', balance: '', concurrency: 1, rpm_limit: 0 }) })

const generateRandomPassword = () => {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%^&*'
  let p = ''; for (let i = 0; i < 16; i++) p += chars.charAt(Math.floor(Math.random() * chars.length))
  form.password = p
}
</script>
