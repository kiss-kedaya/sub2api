<template>
  <BaseDialog
    :show="visible"
    :title="t('adminCompliance.title')"
    width="wide"
    :close-on-escape="false"
    :close-on-click-outside="false"
    :show-close-button="false"
    :z-index="80"
    @close="noop"
  >
    <div class="space-y-5">
      <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('adminCompliance.blockingNotice')" :description="t('adminCompliance.riskNotice')" :ui="{ description: 'leading-6' }" />

      <div class="grid gap-4 md:grid-cols-[minmax(0,1fr)_220px]">
        <section class="max-h-[46vh] min-h-[320px] overflow-y-auto rounded-lg border border-default p-5">
          <div class="legal-document-content" v-html="renderedDocument"></div>
        </section>

        <aside class="space-y-3 text-sm">
          <div>
            <p class="text-xs text-muted">{{ t('adminCompliance.version') }}</p>
            <p class="mt-1 break-all font-mono text-highlighted">{{ complianceStore.status?.version || 'v2026.06.10' }}</p>
          </div>
          <ULink
            :to="documentUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1.5 font-medium text-primary hover:underline"
          >
            <UIcon name="i-lucide-external-link" class="size-4" />
            {{ t('adminCompliance.openDocument') }}
          </ULink>
          <p class="leading-6 text-muted">{{ t('adminCompliance.documentSource') }}</p>
        </aside>
      </div>

      <div class="space-y-2">
        <label for="admin-compliance-phrase" class="block text-sm font-medium text-default">
          {{ t('adminCompliance.inputLabel') }}
        </label>
        <p class="rounded-md bg-elevated px-3 py-2 font-mono text-sm text-highlighted">{{ expectedPhrase }}</p>
        <Input
          id="admin-compliance-phrase"
          v-model="typedPhrase"
          :placeholder="t('adminCompliance.inputPlaceholder')"
          autocomplete="off"
          :disabled="complianceStore.submitting"
          :error="inputError"
          @enter="submit"
        />
      </div>

      <p class="text-xs leading-5 text-muted">
        {{ t('adminCompliance.legalNote') }}
      </p>
    </div>

    <template #footer>
      <div class="flex flex-col gap-2 sm:flex-row sm:justify-end">
        <UButton
          color="neutral"
          variant="subtle"
          icon="i-lucide-log-out"
          :label="t('adminCompliance.logout')"
          :disabled="complianceStore.submitting"
          class="justify-center"
          @click="logout"
        />
        <UButton
          :label="complianceStore.submitting ? t('common.submitting') : t('adminCompliance.accept')"
          :loading="complianceStore.submitting"
          :disabled="!canSubmit || complianceStore.submitting"
          class="justify-center"
          @click="submit"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Input from '@/components/common/Input.vue'
import { useAdminComplianceStore, useAppStore, useAuthStore } from '@/stores'
import { getLocale } from '@/i18n'
import zhDocument from '../../../../docs/legal/admin-compliance.zh.md?raw'
import enDocument from '../../../../docs/legal/admin-compliance.en.md?raw'

const { t } = useI18n()
const complianceStore = useAdminComplianceStore()
const authStore = useAuthStore()
const appStore = useAppStore()
const typedPhrase = ref('')
const attemptedSubmit = ref(false)

marked.setOptions({
  breaks: true,
  gfm: true,
})

const visible = computed(() => authStore.isAuthenticated && authStore.isAdmin && complianceStore.shouldShow)
const expectedPhrase = computed(() => complianceStore.expectedPhrase)
const canSubmit = computed(() => typedPhrase.value.trim() === expectedPhrase.value)
const currentDocument = computed(() => getLocale() === 'zh' ? zhDocument : enDocument)
const documentUrl = computed(() => {
  if (getLocale() === 'zh') {
    return complianceStore.status?.document_url_zh || 'https://github.com/Wei-Shaw/sub2api/blob/main/docs/legal/admin-compliance.zh.md'
  }
  return complianceStore.status?.document_url_en || 'https://github.com/Wei-Shaw/sub2api/blob/main/docs/legal/admin-compliance.en.md'
})
const inputError = computed(() => {
  if (!attemptedSubmit.value || canSubmit.value) {
    return ''
  }
  return t('adminCompliance.inputMismatch')
})
const renderedDocument = computed(() => {
  const html = marked.parse(currentDocument.value) as string
  return DOMPurify.sanitize(html)
})

watch(expectedPhrase, () => {
  typedPhrase.value = ''
  attemptedSubmit.value = false
})

watch(visible, (isVisible) => {
  if (isVisible) {
    typedPhrase.value = ''
    attemptedSubmit.value = false
  }
})

function noop(): void {
  // 强制确认弹窗不允许通过关闭按钮绕过。
}

async function submit(): Promise<void> {
  attemptedSubmit.value = true
  if (!canSubmit.value) {
    return
  }

  try {
    const status = await complianceStore.accept(typedPhrase.value.trim())
    if (!status.required) {
      appStore.showSuccess(t('adminCompliance.accepted'))
      typedPhrase.value = ''
      attemptedSubmit.value = false
    }
  } catch (error) {
    const message = (error as { message?: string })?.message || t('adminCompliance.acceptFailed')
    appStore.showError(message)
  }
}

async function logout(): Promise<void> {
  await authStore.logout()
  window.location.href = '/login'
}
</script>

<style scoped>
/* Rendered markdown of the legal document, coloured from Nuxt UI tokens (light and dark). */
.legal-document-content {
  line-height: 1.75;
  overflow-wrap: anywhere;
  color: var(--ui-text-toned);
  font-size: 0.875rem;
}

.legal-document-content :deep(h1) {
  margin-bottom: 1rem;
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--ui-text-highlighted);
}

.legal-document-content :deep(h2) {
  margin: 1.5rem 0 0.75rem;
  font-size: 1rem;
  font-weight: 600;
  color: var(--ui-text-highlighted);
}

.legal-document-content :deep(p) {
  margin-bottom: 1rem;
}

.legal-document-content :deep(ul),
.legal-document-content :deep(ol) {
  margin-bottom: 1rem;
  padding-left: 1.5rem;
}

.legal-document-content :deep(ul) {
  list-style: disc;
}

.legal-document-content :deep(ol) {
  list-style: decimal;
}

.legal-document-content :deep(li) {
  margin-bottom: 0.25rem;
}

.legal-document-content :deep(strong) {
  font-weight: 600;
  color: var(--ui-text-highlighted);
}
</style>
