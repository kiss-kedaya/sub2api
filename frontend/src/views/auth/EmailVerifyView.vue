<template>
  <AuthLayout>
    <!-- The verification card is shared with the in-card registration step
         (RegisterView) so the send/verify/countdown logic exists only once. -->
    <EmailVerifyCard />

    <!-- Footer -->
    <template #footer>
      <button
        type="button"
        @click="handleBack"
        class="inline-flex min-h-11 items-center gap-2 rounded-md px-2 text-muted transition-colors hover:text-highlighted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary sm:min-h-0"
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
