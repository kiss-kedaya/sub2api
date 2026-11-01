<template>
  <!-- Settings section of the dashboard template: title card with the submit button, then a
       body card of horizontal form rows. Embedded drops the title card (parent provides it). -->
  <form @submit.prevent="handleChangePassword">
    <UPageCard
      v-if="!props.embedded"
      :title="t('profile.changePassword')"
      variant="naked"
      orientation="horizontal"
      class="mb-4"
    >
      <UButton type="submit" :disabled="loading" class="w-fit lg:ms-auto">
        {{ loading ? t('profile.changingPassword') : t('profile.changePasswordButton') }}
      </UButton>
    </UPageCard>

    <div :class="props.embedded ? 'flex flex-col gap-4' : 'flex flex-col gap-4 rounded-lg bg-elevated/50 p-4 ring ring-default sm:p-6'">
      <UFormField
        :label="t('profile.currentPassword')"
        required
        class="flex justify-between gap-4 max-sm:flex-col sm:items-center"
      >
        <UInput
          id="old_password"
          v-model="form.old_password"
          type="password"
          required
          autocomplete="current-password"
          class="w-full sm:w-64"
        />
      </UFormField>
      <USeparator />
      <UFormField
        :label="t('profile.newPassword')"
        :description="t('profile.passwordHint')"
        required
        class="flex justify-between gap-4 max-sm:flex-col sm:items-center"
      >
        <UInput
          id="new_password"
          v-model="form.new_password"
          type="password"
          required
          autocomplete="new-password"
          class="w-full sm:w-64"
        />
      </UFormField>
      <USeparator />
      <UFormField
        :label="t('profile.confirmNewPassword')"
        required
        class="flex justify-between gap-4 max-sm:flex-col sm:items-center"
      >
        <UInput
          id="confirm_password"
          v-model="form.confirm_password"
          type="password"
          required
          autocomplete="new-password"
          class="w-full sm:w-64"
        />
      </UFormField>

      <div v-if="props.embedded" class="flex justify-end">
        <UButton type="submit" :disabled="loading">
          {{ loading ? t('profile.changingPassword') : t('profile.changePasswordButton') }}
        </UButton>
      </div>
    </div>
  </form>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { userAPI } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const props = withDefaults(defineProps<{
  embedded?: boolean
}>(), {
  embedded: false,
})

const loading = ref(false)
const form = ref({
  old_password: '',
  new_password: '',
  confirm_password: ''
})

const handleChangePassword = async () => {
  if (form.value.new_password !== form.value.confirm_password) {
    appStore.showError(t('profile.passwordsNotMatch'))
    return
  }

  if (form.value.new_password.length < 8) {
    appStore.showError(t('profile.passwordTooShort'))
    return
  }

  loading.value = true
  try {
    await userAPI.changePassword(form.value.old_password, form.value.new_password)
    form.value = { old_password: '', new_password: '', confirm_password: '' }
    appStore.showSuccess(t('profile.passwordChangeSuccess'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('profile.passwordChangeFailed')))
  } finally {
    loading.value = false
  }
}
</script>
