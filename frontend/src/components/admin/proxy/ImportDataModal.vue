<template>
  <BaseDialog
    :show="show"
    :title="t('admin.proxies.dataImportTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <UForm id="import-proxy-data-form" :state="{}" class="space-y-4" @submit="handleImport">
      <p class="text-sm text-muted">{{ t('admin.proxies.dataImportHint') }}</p>
      <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.proxies.dataImportWarning')" />

      <UFormField :label="t('admin.proxies.dataImportFile')" help="JSON (.json)">
        <div class="flex items-center gap-3">
          <UButton color="neutral" variant="outline" icon="i-lucide-file-up" :label="t('common.chooseFile')" @click="openFilePicker" />
          <span class="min-w-0 truncate text-sm text-default">{{ fileName || t('admin.proxies.dataImportSelectFile') }}</span>
        </div>
        <!-- Hidden native picker (no Nuxt UI equivalent for a programmatic picker) -->
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          @change="handleFileChange"
        />
      </UFormField>

      <div v-if="result" class="space-y-2">
        <p class="text-sm font-medium text-highlighted">{{ t('admin.proxies.dataImportResult') }}</p>
        <p class="text-sm text-default">{{ t('admin.proxies.dataImportResultSummary', result) }}</p>
        <div v-if="errorItems.length">
          <p class="text-sm font-medium text-error">{{ t('admin.proxies.dataImportErrors') }}</p>
          <div class="mt-2 max-h-48 overflow-auto rounded-md bg-elevated p-3 font-mono text-xs">
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} — {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </UForm>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :disabled="importing" :label="t('common.cancel')" @click="handleClose" />
        <UButton
          type="submit"
          form="import-proxy-data-form"
          :loading="importing"
          :label="importing ? t('admin.proxies.dataImporting') : t('admin.proxies.dataImportButton')"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminDataImportResult } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const hasImportedData = ref(false)
const file = ref<File | null>(null)
const result = ref<AdminDataImportResult | null>(null)

const fileInput = ref<HTMLInputElement | null>(null)
const fileName = computed(() => file.value?.name || '')

const errorItems = computed(() => result.value?.errors || [])

watch(
  () => props.show,
  (open) => {
    if (open) {
      file.value = null
      hasImportedData.value = false
      result.value = null
      if (fileInput.value) {
        fileInput.value.value = ''
      }
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  file.value = target.files?.[0] || null
}

const handleClose = () => {
  if (importing.value) return
  if (hasImportedData.value) {
    hasImportedData.value = false
    emit('imported')
  }
  emit('close')
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') {
    return sourceFile.text()
  }

  if (typeof sourceFile.arrayBuffer === 'function') {
    const buffer = await sourceFile.arrayBuffer()
    return new TextDecoder().decode(buffer)
  }

  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error(t('common.fileReadFailed')))
    reader.readAsText(sourceFile)
  })
}

const handleImport = async () => {
  if (!file.value) {
    appStore.showError(t('admin.proxies.dataImportSelectFile'))
    return
  }

  importing.value = true
  try {
    const text = await readFileAsText(file.value)
    const dataPayload = JSON.parse(text)

    const res = await adminAPI.proxies.importData({ data: dataPayload })

    result.value = res

    const msgParams: Record<string, unknown> = {
      proxy_created: res.proxy_created,
      proxy_reused: res.proxy_reused,
      proxy_failed: res.proxy_failed
    }

    if (res.proxy_failed > 0) {
      hasImportedData.value ||= res.proxy_created > 0 || res.proxy_reused > 0
      appStore.showError(t('admin.proxies.dataImportCompletedWithErrors', msgParams))
    } else {
      hasImportedData.value = false
      appStore.showSuccess(t('admin.proxies.dataImportSuccess', msgParams))
      emit('imported')
    }
  } catch (error: any) {
    if (error instanceof SyntaxError) {
      appStore.showError(t('admin.proxies.dataImportParseFailed'))
    } else {
      appStore.showError(error?.message || t('admin.proxies.dataImportFailed'))
    }
  } finally {
    importing.value = false
  }
}
</script>
