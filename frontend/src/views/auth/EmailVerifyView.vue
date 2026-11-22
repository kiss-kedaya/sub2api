<template>
  <AuthLayout>
    <!-- The verification card is shared with the in-card registration step
         (RegisterView) so the send/verify/countdown logic exists only once. -->
    <EmailVerifyCard />

    <!-- Footer -->
    <template #footer>
      <button
        @click="handleBack"
        class="flex items-center gap-2 text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-gray-300"
      >
        <Icon name="arrowLeft" size="sm" />
        {{ t('auth.backToRegistration') }}
      </button>
    </template>
  </AuthLayout>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import Icon from '@/components/icons/Icon.vue'
import EmailVerifyCard from '@/components/auth/EmailVerifyCard.vue'

const { t } = useI18n()
const router = useRouter()

function handleBack(): void {
  // Clear session data
  sessionStorage.removeItem('register_data')

  // Go back to registration
  router.push('/register')
}
</script>
