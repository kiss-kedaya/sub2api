<template>
  <!-- Terms checkbox above the submit button -->
  <div v-if="mode === 'checkbox' && documents.length > 0" class="flex items-start gap-2 text-sm">
    <UCheckbox
      id="login-agreement-consent"
      :model-value="accepted"
      class="mt-0.5"
      @update:model-value="handleCheckboxChange"
    />
    <p class="min-w-0 flex-1 leading-5 text-muted">
      <label for="login-agreement-consent" class="cursor-pointer text-default">
        {{ t('legal.loginAgreementPrompt.checkboxPrefix') }}
      </label>
      <template v-for="(doc, index) in documents" :key="doc.id || doc.title">
        <RouterLink
          :to="documentRoute(doc)"
          target="_blank"
          rel="noopener noreferrer"
          class="font-medium text-primary underline-offset-4 hover:underline"
        >
          {{ doc.title }}
        </RouterLink>
        <span v-if="index < documents.length - 1">{{ t('legal.loginAgreementPrompt.documentSeparator') }}</span>
      </template>
    </p>
  </div>

  <!-- Terms not accepted yet (dialog closed): a quiet note with a way back to the dialog -->
  <UAlert
    v-else-if="!accepted && documents.length > 0"
    color="neutral"
    variant="soft"
    icon="i-lucide-shield"
    orientation="horizontal"
    :title="t('legal.loginAgreementPrompt.noticeTitle')"
    :description="t('legal.loginAgreementPrompt.noticeDescription')"
    :actions="[{ label: t('legal.loginAgreementPrompt.viewTerms'), size: 'xs', onClick: () => emit('open') }]"
    :ui="{ title: 'text-highlighted', description: 'text-muted opacity-100' }"
  />

  <Teleport to="body">
    <Transition name="agreement-fade">
      <!-- Terms dialog, laid out like Nuxt UI's UModal: header, list, two buttons -->
      <div
        v-if="dialogVisible"
        class="fixed inset-0 z-140 flex items-center justify-center overflow-y-auto bg-elevated/75 p-4"
      >
        <div role="dialog" aria-modal="true" class="flex w-full max-w-lg flex-col divide-y divide-default rounded-lg bg-default shadow-lg ring ring-default">
          <div class="space-y-1 p-4 sm:px-6">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="font-semibold text-highlighted">{{ t('legal.loginAgreementPrompt.dialogTitle') }}</h2>
              <UBadge v-if="updatedAt" color="neutral" variant="soft" size="sm" :label="updatedAt" />
            </div>
            <p class="text-sm text-muted">
              {{ t('legal.loginAgreementPrompt.dialogDescription', { date: updatedAt || t('legal.loginAgreementPrompt.recently') }) }}
            </p>
          </div>

          <div class="max-h-[58vh] space-y-1 overflow-y-auto p-4 sm:px-6">
            <p class="pb-1 text-sm font-medium text-highlighted">{{ t('legal.loginAgreementPrompt.relatedDocuments') }}</p>
            <RouterLink
              v-for="(doc, index) in documents"
              :key="doc.id || doc.title"
              :to="documentRoute(doc)"
              target="_blank"
              rel="noopener noreferrer"
              class="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm text-default transition-colors hover:bg-elevated"
            >
              <UIcon :name="documentIcon(index, doc.title)" class="size-4 shrink-0 text-muted" />
              <span class="min-w-0 flex-1 truncate">{{ doc.title }}</span>
              <UIcon name="i-lucide-external-link" class="size-4 shrink-0 text-dimmed" />
            </RouterLink>
          </div>

          <div class="flex justify-end gap-2 p-4 sm:px-6">
            <UButton color="neutral" variant="subtle" :label="t('legal.loginAgreementPrompt.reject')" @click="emit('reject')" />
            <UButton :label="t('legal.loginAgreementPrompt.accept')" @click="emit('accept')" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LoginAgreementDocument } from '@/types'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  accepted: boolean
  documents: LoginAgreementDocument[]
  mode: 'modal' | 'checkbox' | string
  updatedAt?: string
  visible: boolean
}>(), {
  updatedAt: ''
})

const emit = defineEmits<{
  accept: []
  reject: []
  open: []
}>()

const dialogVisible = computed(() => props.visible && documents.value.length > 0)
const documents = computed(() => props.documents.filter((doc) => doc.title.trim()))
const updatedAt = computed(() => props.updatedAt || '')
const accepted = computed(() => props.accepted)
const mode = computed(() => props.mode === 'checkbox' ? 'checkbox' : 'modal')

function documentRoute(doc: LoginAgreementDocument) {
  return {
    name: 'LegalDocument',
    params: {
      documentId: doc.id || doc.title,
    },
  }
}

function handleCheckboxChange(checked: boolean | 'indeterminate'): void {
  if (checked === true) {
    emit('accept')
  } else {
    emit('reject')
  }
}

function documentIcon(index: number, title: string): string {
  const normalizedTitle = title.toLowerCase()
  if (
    normalizedTitle.includes('policy') ||
    normalizedTitle.includes('privacy') ||
    title.includes('政策') ||
    title.includes('隐私')
  ) {
    return 'i-lucide-shield'
  }
  if (
    normalizedTitle.includes('country') ||
    normalizedTitle.includes('region') ||
    title.includes('国家') ||
    title.includes('地区')
  ) {
    return 'i-lucide-globe'
  }
  if (index === 3) {
    return 'i-lucide-settings'
  }
  return 'i-lucide-file-text'
}
</script>

<style scoped>
.agreement-fade-enter-active,
.agreement-fade-leave-active {
  transition: opacity 0.18s ease;
}

.agreement-fade-enter-from,
.agreement-fade-leave-to {
  opacity: 0;
}

.agreement-fade-enter-active > div,
.agreement-fade-leave-active > div {
  transition: transform 0.18s ease, opacity 0.18s ease;
}

.agreement-fade-enter-from > div,
.agreement-fade-leave-to > div {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}
</style>
