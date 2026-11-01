<template>
  <div class="bg-default">
    <UError
      :error="{ statusCode: 404, statusMessage: t('errors.pageNotFound'), message: t('notFound.description') }"
      :ui="{ root: 'min-h-screen px-4', statusCode: 'text-sm tracking-widest' }"
    >
      <template #links>
        <div class="flex flex-col items-center gap-8">
          <div class="flex flex-col justify-center gap-3 sm:flex-row">
            <UButton
              :label="t('notFound.goBack')"
              icon="i-lucide-arrow-left"
              color="neutral"
              variant="outline"
              size="lg"
              @click="goBack"
            />
            <UButton to="/dashboard" :label="t('notFound.goToDashboard')" icon="i-lucide-house" size="lg" />
          </div>
          <p v-if="contactInfo" class="text-sm text-muted">
            {{ t('notFound.needHelp') }}
            <span class="text-default">{{ t('common.contactSupport') }}: {{ contactInfo }}</span>
          </p>
        </div>
      </template>
    </UError>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

// The admin's contact line (Settings → contact info); hidden when none is set.
const contactInfo = computed(() => appStore.contactInfo)

function goBack(): void {
  router.back()
}
</script>
