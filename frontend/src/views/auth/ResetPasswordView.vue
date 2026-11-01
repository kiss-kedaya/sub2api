<template>
  <AuthLayout>
    <!-- Link without email/token -->
    <div v-if="isInvalidLink" class="space-y-6">
      <AuthFormHeader icon="i-lucide-link-2-off" :title="t('auth.invalidResetLink')">
        {{ t('auth.invalidResetLinkHint') }}
      </AuthFormHeader>
      <UButton to="/forgot-password" block :label="t('auth.requestNewResetLink')" />
    </div>

    <!-- Done -->
    <div v-else-if="isSuccess" class="space-y-6">
      <AuthFormHeader icon="i-lucide-circle-check" :title="t('auth.passwordResetSuccess')">
        {{ t('auth.passwordResetSuccessHint') }}
      </AuthFormHeader>
      <UButton to="/login" block :label="t('auth.signIn')" />
    </div>

    <div v-else class="space-y-6">
      <AuthFormHeader icon="i-lucide-lock-keyhole" :title="t('auth.resetPasswordTitle')">
        {{ t('auth.rememberedPassword') }}
        <router-link to="/login" class="font-medium text-primary transition-colors hover:text-primary/75">
          {{ t('auth.signIn') }}
        </router-link>
      </AuthFormHeader>

      <UForm :state="formData" class="space-y-5" @submit="handleSubmit">
        <UFormField :label="t('auth.emailLabel')">
          <UInput id="email" :model-value="email" type="email" readonly disabled class="w-full" />
        </UFormField>

        <UFormField :label="t('auth.newPassword')" :error="!!errors.password">
          <UInput
            id="password"
            v-model="formData.password"
            :type="showPassword ? 'text' : 'password'"
            required
            autocomplete="new-password"
            :disabled="isLoading"
            :placeholder="t('auth.newPasswordPlaceholder')"
            class="w-full"
          >
            <template #trailing>
              <UButton
                color="neutral"
                variant="link"
                size="sm"
                :icon="showPassword ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                :aria-label="showPassword ? t('auth.hidePassword') : t('auth.showPassword')"
                :aria-pressed="showPassword"
                @click="showPassword = !showPassword"
              />
            </template>
          </UInput>
        </UFormField>

        <UFormField :label="t('auth.confirmPassword')" :error="!!errors.confirmPassword">
          <UInput
            id="confirmPassword"
            v-model="formData.confirmPassword"
            :type="showConfirmPassword ? 'text' : 'password'"
            required
            autocomplete="new-password"
            :disabled="isLoading"
            :placeholder="t('auth.confirmPasswordPlaceholder')"
            class="w-full"
          >
            <template #trailing>
              <UButton
                color="neutral"
                variant="link"
                size="sm"
                :icon="showConfirmPassword ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                :aria-label="showConfirmPassword ? t('auth.hidePassword') : t('auth.showPassword')"
                :aria-pressed="showConfirmPassword"
                @click="showConfirmPassword = !showConfirmPassword"
              />
            </template>
          </UInput>
        </UFormField>

        <UButton
          type="submit"
          block
          :loading="isLoading"
          :label="isLoading ? t('auth.resettingPassword') : t('auth.resetPassword')"
        />
      </UForm>
    </div>
  </AuthLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import AuthFormHeader from '@/components/auth/AuthFormHeader.vue'
import { useAppStore } from '@/stores'
import { resetPassword } from '@/api/auth'

const { t } = useI18n()

// ==================== Router & Stores ====================

const route = useRoute()
const appStore = useAppStore()

// ==================== State ====================

const isLoading = ref<boolean>(false)
const isSuccess = ref<boolean>(false)
const errorMessage = ref<string>('')
const showPassword = ref<boolean>(false)
const showConfirmPassword = ref<boolean>(false)

// URL parameters
const email = ref<string>('')
const token = ref<string>('')

const formData = reactive({
  password: '',
  confirmPassword: ''
})

const errors = reactive({
  password: '',
  confirmPassword: ''
})

const validationToastMessage = computed(
  () => errors.password || errors.confirmPassword || ''
)

watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// Check if the reset link is valid (has email and token)
const isInvalidLink = computed(() => !email.value || !token.value)

// ==================== Lifecycle ====================

onMounted(() => {
  // Get email and token from URL query parameters
  email.value = (route.query.email as string) || ''
  token.value = (route.query.token as string) || ''

  if (!email.value || !token.value) {
    appStore.showError(t('auth.invalidResetLink'))
  }
})

// ==================== Validation ====================

function validateForm(): boolean {
  errors.password = ''
  errors.confirmPassword = ''

  let isValid = true

  // Password validation
  if (!formData.password) {
    errors.password = t('auth.passwordRequired')
    isValid = false
  } else if (formData.password.length < 6) {
    errors.password = t('auth.passwordMinLength')
    isValid = false
  }

  // Confirm password validation
  if (!formData.confirmPassword) {
    errors.confirmPassword = t('auth.confirmPasswordRequired')
    isValid = false
  } else if (formData.password !== formData.confirmPassword) {
    errors.confirmPassword = t('auth.passwordsDoNotMatch')
    isValid = false
  }

  return isValid
}

// ==================== Form Handlers ====================

async function handleSubmit(): Promise<void> {
  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  isLoading.value = true

  try {
    await resetPassword({
      email: email.value,
      token: token.value,
      new_password: formData.password
    })

    isSuccess.value = true
    appStore.showSuccess(t('auth.passwordResetSuccess'))
  } catch (error: unknown) {
    const err = error as { message?: string; response?: { data?: { detail?: string; code?: string } } }

    // Check for invalid/expired token error
    if (err.response?.data?.code === 'INVALID_RESET_TOKEN') {
      errorMessage.value = t('auth.invalidOrExpiredToken')
    } else if (err.response?.data?.detail) {
      errorMessage.value = err.response.data.detail
    } else if (err.message) {
      errorMessage.value = err.message
    } else {
      errorMessage.value = t('auth.resetPasswordFailed')
    }

    appStore.showError(errorMessage.value)
  } finally {
    isLoading.value = false
  }
}
</script>
