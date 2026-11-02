<template>
  <div>
    <!-- Multi-select Dropdown -->
    <div v-if="!syncOnly" class="relative mb-3">
      <div
        @click="toggleDropdown"
        class="cursor-pointer rounded-md bg-default px-3 py-2 ring ring-inset ring-accented"
      >
        <div class="grid grid-cols-2 gap-1.5">
          <span
            v-for="model in modelValue"
            :key="model"
            class="inline-flex items-center justify-between gap-1 rounded-md bg-elevated px-2 py-1 text-xs text-default"
          >
            <span class="flex items-center gap-1 truncate">
              <ModelIcon :model="model" size="14px" />
              <span class="truncate">{{ model }}</span>
            </span>
            <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-x" square class="-me-1 size-4 rounded-full p-0" :aria-label="t('common.remove')" @click.stop="removeModel(model)" />
          </span>
        </div>
        <div class="mt-2 flex items-center justify-between border-t border-default pt-2">
          <span class="text-xs text-dimmed">{{ t('admin.accounts.modelCount', { count: modelValue.length }) }}</span>
          <svg class="h-5 w-5 text-dimmed" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </div>
      <!-- Dropdown List -->
      <div
        v-if="showDropdown"
        class="absolute left-0 right-0 top-full z-50 mt-1 rounded-md bg-default shadow-lg ring ring-default"
      >
        <div class="sticky top-0 border-b border-default bg-default p-2">
          <UInput v-model="searchQuery" icon="i-lucide-search" class="w-full" :placeholder="t('admin.accounts.searchModels')" @click.stop />
        </div>
        <div class="max-h-52 overflow-auto">
          <div
            v-for="model in filteredModels"
            :key="model.value"
            data-testid="model-option"
            class="group flex items-center hover:bg-elevated"
          >
            <UButton
              data-testid="select-model"
              color="neutral"
              variant="ghost"
              class="min-w-0 flex-1 justify-start gap-2 rounded-none px-3 py-2"
              :aria-pressed="modelValue.includes(model.value)"
              @click="toggleModel(model.value)"
            >
              <UCheckbox :model-value="modelValue.includes(model.value)" tabindex="-1" aria-hidden="true" class="pointer-events-none" />
              <ModelIcon :model="model.value" size="18px" />
              <span class="truncate text-highlighted">{{ model.value }}</span>
            </UButton>
            <UButton
              data-testid="copy-model-id"
              color="neutral"
              variant="ghost"
              size="xs"
              icon="i-lucide-copy"
              square
              class="me-2 opacity-70 group-hover:opacity-100 focus-visible:opacity-100"
              :title="`${t('common.copy')} ${model.value}`"
              :aria-label="`${t('common.copy')} ${model.value}`"
              @click="copyModelId(model.value)"
            />
          </div>
          <div v-if="filteredModels.length === 0" class="px-3 py-4 text-center text-sm text-muted">
            {{ t('admin.accounts.noMatchingModels') }}
          </div>
        </div>
      </div>
    </div>

    <!-- Quick Actions -->
    <div class="mb-4 flex flex-wrap gap-2" :class="{ 'mb-3': syncOnly }">
      <UButton v-if="!syncOnly" color="neutral" variant="outline" size="sm" :label="t('admin.accounts.fillRelatedModels')" @click="fillRelated" />
      <UButton v-if="canSyncUpstream" color="neutral" variant="outline" size="sm" icon="i-lucide-refresh-cw" data-testid="sync-upstream-models" :disabled="isSyncingUpstream" @click="syncUpstreamModels">{{ isSyncingUpstream ? t('admin.accounts.syncUpstreamModelsLoading') : t('admin.accounts.syncUpstreamModels') }}</UButton>
      <UButton v-if="!syncOnly" color="error" variant="ghost" size="sm" :label="t('admin.accounts.clearAllModels')" @click="clearAll" />
    </div>

    <!-- Custom Model Input -->
    <div v-if="!syncOnly" class="mb-3">
      <label class="mb-1.5 block text-sm font-medium text-default">{{ t('admin.accounts.customModelName') }}</label>
      <div class="flex gap-2">
        <UInput
          v-model="customModel"
          class="flex-1"
          :placeholder="t('admin.accounts.enterCustomModelName')"
          @keydown.enter.prevent="handleEnter"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />
        <UButton color="neutral" variant="outline" icon="i-lucide-plus" :label="t('admin.accounts.addModel')" @click="addCustom" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { accountsAPI } from '@/api/admin/accounts'
import type { SyncUpstreamPreviewParams } from '@/api/admin/accounts'
import { useClipboard } from '@/composables/useClipboard'
import ModelIcon from '@/components/common/ModelIcon.vue'
import { allModels, getModelsByPlatform } from '@/composables/useModelWhitelist'

const { t } = useI18n()

const props = defineProps<{
  modelValue: string[]
  platform?: string
  platforms?: string[]
  accountId?: number
  syncOnly?: boolean
  syncCredentials?: {
    platform: string
    type: string
    base_url?: string
    account_id?: string
    api_key: string
    api_protocol?: string
    api_base_urls?: Record<string, string>
  }
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
  'upstream-models': [value: string[]]
  'upstream-synced': []
}>()

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const showDropdown = ref(false)
const searchQuery = ref('')
const customModel = ref('')
const isComposing = ref(false)
const isSyncingUpstream = ref(false)
const normalizedPlatforms = computed(() => {
  const rawPlatforms =
    props.platforms && props.platforms.length > 0
      ? props.platforms
      : props.platform
        ? [props.platform]
        : []

  return Array.from(
    new Set(
      rawPlatforms
        .map(platform => platform?.trim())
        .filter((platform): platform is string => Boolean(platform))
    )
  )
})

const upstreamSyncPlatforms = new Set([
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'minimax'
])
const canSyncUpstream = computed(() => {
  if (props.accountId) {
    if (normalizedPlatforms.value.length === 0) return true
    return normalizedPlatforms.value.some(platform => upstreamSyncPlatforms.has(platform.toLowerCase()))
  }
  if (props.syncCredentials) {
    return upstreamSyncPlatforms.has(props.syncCredentials.platform.toLowerCase())
  }
  return false
})

const availableOptions = computed(() => {
  if (normalizedPlatforms.value.length === 0) {
    return allModels
  }

  const allowedModels = new Set<string>()
  for (const platform of normalizedPlatforms.value) {
    for (const model of getModelsByPlatform(platform)) {
      allowedModels.add(model)
    }
  }

  return allModels.filter(model => allowedModels.has(model.value))
})

const filteredModels = computed(() => {
  const query = searchQuery.value.toLowerCase().trim()
  if (!query) return availableOptions.value
  return availableOptions.value.filter(
    m => m.value.toLowerCase().includes(query) || m.label.toLowerCase().includes(query)
  )
})

const toggleDropdown = () => {
  showDropdown.value = !showDropdown.value
  if (!showDropdown.value) searchQuery.value = ''
}

const removeModel = (model: string) => {
  emit('update:modelValue', props.modelValue.filter(m => m !== model))
}

const toggleModel = (model: string) => {
  if (props.modelValue.includes(model)) {
    removeModel(model)
  } else {
    emit('update:modelValue', [...props.modelValue, model])
  }
}

const copyModelId = async (model: string) => {
  await copyToClipboard(model)
}

const addCustom = () => {
  const model = customModel.value.trim()
  if (!model) return
  if (props.modelValue.includes(model)) {
    appStore.showInfo(t('admin.accounts.modelExists'))
    return
  }
  emit('update:modelValue', [...props.modelValue, model])
  customModel.value = ''
}

const handleEnter = () => {
  if (!isComposing.value) addCustom()
}

const fillRelated = () => {
  const newModels = [...props.modelValue]
  for (const platform of normalizedPlatforms.value) {
    for (const model of getModelsByPlatform(platform)) {
      if (!newModels.includes(model)) {
        newModels.push(model)
      }
    }
  }
  emit('update:modelValue', newModels)
}

const syncUpstreamModels = async () => {
  if (isSyncingUpstream.value) return
  if (!props.accountId && !props.syncCredentials) return

  isSyncingUpstream.value = true
  try {
    let result
    if (props.accountId) {
      result = await accountsAPI.syncUpstreamModels(props.accountId)
    } else if (props.syncCredentials) {
      result = await accountsAPI.syncUpstreamModelsPreview(props.syncCredentials as SyncUpstreamPreviewParams)
    } else {
      return
    }

    const upstreamModels = result.models.map(model => model.trim()).filter(Boolean)
    if (upstreamModels.length === 0) {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsEmpty'))
      return
    }

    if (!props.accountId) {
      emit('upstream-synced')
    }

    const newModels = [...props.modelValue]
    let addedCount = 0
    for (const model of upstreamModels) {
      if (!newModels.includes(model)) {
        newModels.push(model)
        addedCount += 1
      }
    }

    emit('update:modelValue', newModels)
    emit('upstream-models', upstreamModels)
    const warnings = result.warnings ?? []
    const hasPartialMetadata = warnings.some(
      warning => warning.code === 'upstream_model_metadata_partial'
    )
    const hasIncompleteMetadata = warnings.some(
      warning => warning.code === 'upstream_model_metadata_incomplete'
    )
    if (hasIncompleteMetadata) {
      appStore.showWarning(t('admin.accounts.syncUpstreamModelsMetadataIncomplete'))
      return
    }
    if (addedCount > 0) {
      appStore.showSuccess(t('admin.accounts.syncUpstreamModelsSuccess', { count: addedCount, total: upstreamModels.length }))
    } else {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsNoChanges', { count: upstreamModels.length }))
    }
    if (hasPartialMetadata) {
      appStore.showWarning(t('admin.accounts.syncUpstreamModelsMetadataPartial'))
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : t('admin.accounts.syncUpstreamModelsFailed')
    appStore.showError(t('admin.accounts.syncUpstreamModelsError', { message }))
  } finally {
    isSyncingUpstream.value = false
  }
}

const clearAll = () => {
  emit('update:modelValue', [])
}

</script>
