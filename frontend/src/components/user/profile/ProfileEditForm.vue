<template>
  <!-- Embedded: one form row inside the parent's card. Standalone: a section with its own
       title card and a body card (same surface as UPageCard variant="subtle"). -->
  <section>
    <UPageCard
      v-if="!props.embedded"
      :title="t('profile.editProfile')"
      variant="naked"
      class="mb-4"
    />
    <div :class="props.embedded ? undefined : 'rounded-lg bg-elevated/50 p-4 ring ring-default sm:p-6'">
      <form @submit.prevent="handleUpdateProfile">
        <UFormField
          :label="t('profile.username')"
          class="flex justify-between gap-4 max-sm:flex-col sm:items-center"
        >
          <div class="flex flex-wrap items-center gap-2">
            <UInput
              id="username"
              v-model="username"
              type="text"
              class="w-full sm:w-56"
              :placeholder="t('profile.enterUsername')"
            />
            <UButton type="submit" :disabled="loading">
              {{ loading ? t('profile.updating') : t('profile.updateProfile') }}
            </UButton>
          </div>
        </UFormField>
      </form>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { userAPI } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = withDefaults(defineProps<{
  initialUsername: string
  embedded?: boolean
}>(), {
  embedded: false,
})

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const username = ref(props.initialUsername)
const loading = ref(false)

watch(() => props.initialUsername, (val) => {
  username.value = val
})

const handleUpdateProfile = async () => {
  if (!username.value.trim()) {
    appStore.showError(t('profile.usernameRequired'))
    return
  }

  loading.value = true
  try {
    const updatedUser = await userAPI.updateProfile({
      username: username.value
    })
    authStore.user = updatedUser
    appStore.showSuccess(t('profile.updateSuccess'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('profile.updateFailed')))
  } finally {
    loading.value = false
  }
}
</script>
