<template>
  <BaseDialog
    :show="show"
    :title="t('admin.errorPassthrough.title')"
    width="extra-wide"
    @close="$emit('close')"
  >
    <div class="space-y-4">
      <!-- Toolbar: short description + create -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-muted">
          {{ t('admin.errorPassthrough.description') }}
        </p>
        <UButton icon="i-lucide-plus" :label="t('admin.errorPassthrough.createRule')" @click="showCreateModal = true" />
      </div>

      <div v-if="loading" class="flex items-center justify-center py-8">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>

      <div v-else-if="rules.length === 0" class="flex flex-col items-center gap-1 py-10 text-center">
        <UIcon name="i-lucide-shield" class="mb-2 size-8 text-dimmed" />
        <h4 class="text-sm font-medium text-highlighted">
          {{ t('admin.errorPassthrough.noRules') }}
        </h4>
        <p class="text-sm text-muted">
          {{ t('admin.errorPassthrough.createFirstRule') }}
        </p>
      </div>

      <div v-else class="max-h-96 overflow-auto">
        <UTable
          :data="rules"
          :columns="ruleColumns"
          sticky
          :ui="tableUi"
        >
          <template #priority-cell="{ row }">
            <span class="tabular-nums text-default">{{ row.original.priority }}</span>
          </template>
          <template #name-cell="{ row }">
            <div class="text-sm font-medium text-highlighted">{{ row.original.name }}</div>
            <div v-if="row.original.description" class="mt-0.5 max-w-xs truncate text-xs text-muted">
              {{ row.original.description }}
            </div>
          </template>
          <template #conditions-cell="{ row }">
            <div class="flex max-w-56 flex-wrap items-center gap-1">
              <UBadge
                v-for="code in row.original.error_codes.slice(0, 3)"
                :key="code"
                color="neutral"
                variant="outline"
                size="sm"
                class="font-mono"
              >
                {{ code }}
              </UBadge>
              <span v-if="row.original.error_codes.length > 3" class="text-xs text-muted">
                +{{ row.original.error_codes.length - 3 }}
              </span>
              <UBadge
                v-for="keyword in row.original.keywords.slice(0, 1)"
                :key="keyword"
                color="neutral"
                variant="subtle"
                size="sm"
              >
                "{{ keyword.length > 10 ? keyword.substring(0, 10) + '...' : keyword }}"
              </UBadge>
              <span v-if="row.original.keywords.length > 1" class="text-xs text-muted">
                +{{ row.original.keywords.length - 1 }}
              </span>
            </div>
            <div class="mt-1 text-xs text-muted">
              {{ t('admin.errorPassthrough.matchMode.' + row.original.match_mode) }}
            </div>
          </template>
          <template #platforms-cell="{ row }">
            <div v-if="row.original.platforms.length === 0" class="text-xs text-muted">
              {{ t('admin.errorPassthrough.allPlatforms') }}
            </div>
            <div v-else class="flex flex-wrap items-center gap-1">
              <UBadge
                v-for="platform in row.original.platforms.slice(0, 2)"
                :key="platform"
                color="neutral"
                variant="subtle"
                size="sm"
              >
                {{ platform }}
              </UBadge>
              <span v-if="row.original.platforms.length > 2" class="text-xs text-muted">
                +{{ row.original.platforms.length - 2 }}
              </span>
            </div>
          </template>
          <template #behavior-cell="{ row }">
            <div class="space-y-0.5 text-xs text-muted">
              <div class="flex items-center gap-1">
                <UIcon
                  :name="row.original.passthrough_code ? 'i-lucide-circle-check' : 'i-lucide-circle-x'"
                  :class="['size-3.5', row.original.passthrough_code ? 'text-success' : 'text-dimmed']"
                />
                <span>
                  {{ t('admin.errorPassthrough.code') }}:
                  {{ row.original.passthrough_code ? t('admin.errorPassthrough.passthrough') : (row.original.response_code || '-') }}
                </span>
              </div>
              <div class="flex items-center gap-1">
                <UIcon
                  :name="row.original.passthrough_body ? 'i-lucide-circle-check' : 'i-lucide-circle-x'"
                  :class="['size-3.5', row.original.passthrough_body ? 'text-success' : 'text-dimmed']"
                />
                <span>
                  {{ t('admin.errorPassthrough.body') }}:
                  {{ row.original.passthrough_body ? t('admin.errorPassthrough.passthrough') : t('admin.errorPassthrough.custom') }}
                </span>
              </div>
              <div v-if="row.original.skip_monitoring" class="flex items-center gap-1">
                <UIcon name="i-lucide-eye-off" class="size-3.5 text-warning" />
                <span>{{ t('admin.errorPassthrough.skipMonitoring') }}</span>
              </div>
            </div>
          </template>
          <template #status-cell="{ row }">
            <USwitch
              :model-value="row.original.enabled"
              size="sm"
              :aria-label="t('admin.errorPassthrough.form.enabled')"
              @update:model-value="toggleEnabled(row.original)"
            />
          </template>
          <template #actions-cell="{ row }">
            <div class="flex items-center justify-end gap-0.5">
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-pencil"
                square
                :aria-label="t('common.edit')"
                :title="t('common.edit')"
                @click="handleEdit(row.original)"
              />
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-trash-2"
                square
                :aria-label="t('common.delete')"
                :title="t('common.delete')"
                @click="handleDelete(row.original)"
              />
            </div>
          </template>
        </UTable>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="$emit('close')" />
      </div>
    </template>

    <!-- Create/Edit Modal -->
    <BaseDialog
      :show="showCreateModal || showEditModal"
      :title="showEditModal ? t('admin.errorPassthrough.editRule') : t('admin.errorPassthrough.createRule')"
      width="wide"
      @close="closeFormModal"
    >
      <UForm :state="form" class="space-y-4" @submit="handleSubmit">
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('admin.errorPassthrough.form.name')" required>
            <UInput
              v-model="form.name"
              required
              class="w-full"
              :placeholder="t('admin.errorPassthrough.form.namePlaceholder')"
            />
          </UFormField>
          <UFormField :label="t('admin.errorPassthrough.form.priority')" :help="t('admin.errorPassthrough.form.priorityHint')">
            <UInput v-model.number="form.priority" type="number" min="0" class="w-full" />
          </UFormField>
        </div>

        <UFormField :label="t('admin.errorPassthrough.form.description')">
          <UInput
            v-model="form.description"
            class="w-full"
            :placeholder="t('admin.errorPassthrough.form.descriptionPlaceholder')"
          />
        </UFormField>

        <USeparator :label="t('admin.errorPassthrough.form.matchConditions')" :ui="{ label: 'text-xs font-medium text-muted' }" />

        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('admin.errorPassthrough.form.errorCodes')" :help="t('admin.errorPassthrough.form.errorCodesHint')">
            <UInput
              v-model="errorCodesInput"
              class="w-full"
              :placeholder="t('admin.errorPassthrough.form.errorCodesPlaceholder')"
            />
          </UFormField>
          <UFormField :label="t('admin.errorPassthrough.form.keywords')" :help="t('admin.errorPassthrough.form.keywordsHint')">
            <UTextarea
              v-model="keywordsInput"
              :rows="2"
              class="w-full font-mono"
              :placeholder="t('admin.errorPassthrough.form.keywordsPlaceholder')"
            />
          </UFormField>
        </div>

        <UFormField :label="t('admin.errorPassthrough.form.matchMode')">
          <URadioGroup v-model="form.match_mode" :items="matchModeOptions" />
        </UFormField>

        <UFormField :label="t('admin.errorPassthrough.form.platforms')" :help="t('admin.errorPassthrough.form.platformsHint')">
          <UCheckboxGroup v-model="form.platforms" :items="platformOptions" orientation="horizontal" :ui="{ fieldset: 'flex-wrap gap-x-4 gap-y-2' }" />
        </UFormField>

        <USeparator :label="t('admin.errorPassthrough.form.responseBehavior')" :ui="{ label: 'text-xs font-medium text-muted' }" />

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="space-y-3">
            <UCheckbox v-model="form.passthrough_code" :label="t('admin.errorPassthrough.form.passthroughCode')" />
            <UFormField v-if="!form.passthrough_code" :label="t('admin.errorPassthrough.form.responseCode')">
              <UInput v-model.number="form.response_code" type="number" min="100" max="599" class="w-full" placeholder="422" />
            </UFormField>
          </div>
          <div class="space-y-3">
            <UCheckbox v-model="form.passthrough_body" :label="t('admin.errorPassthrough.form.passthroughBody')" />
            <UFormField v-if="!form.passthrough_body" :label="t('admin.errorPassthrough.form.customMessage')">
              <UInput
                v-model="form.custom_message"
                class="w-full"
                :placeholder="t('admin.errorPassthrough.form.customMessagePlaceholder')"
              />
            </UFormField>
          </div>
        </div>

        <USeparator />

        <UCheckbox
          v-model="form.skip_monitoring"
          :label="t('admin.errorPassthrough.form.skipMonitoring')"
          :description="t('admin.errorPassthrough.form.skipMonitoringHint')"
        />
        <UCheckbox v-model="form.enabled" :label="t('admin.errorPassthrough.form.enabled')" />
      </UForm>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeFormModal" />
          <UButton :disabled="submitting" @click="handleSubmit">
            <UIcon v-if="submitting" name="i-lucide-loader-circle" class="size-4 animate-spin" />
            {{ showEditModal ? t('common.update') : t('common.create') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.errorPassthrough.deleteRule')"
      :message="t('admin.errorPassthrough.deleteConfirm', { name: deletingRule?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { adminAPI } from '@/api/admin'
import type { ErrorPassthroughRule } from '@/api/admin/errorPassthrough'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { TableColumn } from '@nuxt/ui'

const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

// eslint-disable-next-line @typescript-eslint/no-unused-vars
void emit // suppress unused warning - emit is used via $emit in template

const { t } = useI18n()
const appStore = useAppStore()

const rules = ref<ErrorPassthroughRule[]>([])
const loading = ref(false)
const submitting = ref(false)
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const editingRule = ref<ErrorPassthroughRule | null>(null)
const deletingRule = ref<ErrorPassthroughRule | null>(null)

// Form inputs for arrays
const errorCodesInput = ref('')
const keywordsInput = ref('')

const form = reactive({
  name: '',
  enabled: true,
  priority: 0,
  match_mode: 'any' as 'any' | 'all',
  platforms: [] as string[],
  passthrough_code: true,
  response_code: null as number | null,
  passthrough_body: true,
  custom_message: null as string | null,
  skip_monitoring: false,
  description: null as string | null
})

const matchModeOptions = computed(() => [
  { value: 'any', label: t('admin.errorPassthrough.matchMode.any'), description: t('admin.errorPassthrough.matchMode.anyHint') },
  { value: 'all', label: t('admin.errorPassthrough.matchMode.all'), description: t('admin.errorPassthrough.matchMode.allHint') }
])

const platformOptions = CONCRETE_PLATFORM_OPTIONS

// Rule list in the template's table style: elevated header row, hairline row borders.
const ruleColumns = computed<TableColumn<ErrorPassthroughRule>[]>(() => [
  { id: 'priority', header: t('admin.errorPassthrough.columns.priority') },
  { id: 'name', header: t('admin.errorPassthrough.columns.name') },
  { id: 'conditions', header: t('admin.errorPassthrough.columns.conditions') },
  { id: 'platforms', header: t('admin.errorPassthrough.columns.platforms') },
  { id: 'behavior', header: t('admin.errorPassthrough.columns.behavior') },
  { id: 'status', header: t('admin.errorPassthrough.columns.status') },
  { id: 'actions', header: t('admin.errorPassthrough.columns.actions') }
])
const tableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 text-xs font-medium text-muted first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'py-2.5 align-top border-b border-default',
  separator: 'h-0'
}

// Invalidate pending loads when visibility changes or the component unmounts.
let loadRevision = 0
const loadRules = async () => {
  const revision = ++loadRevision
  const isCurrent = () => props.show && revision === loadRevision
  loading.value = true
  try {
    const data = await adminAPI.errorPassthrough.list()
    if (isCurrent()) rules.value = data
  } catch (error) {
    if (isCurrent()) {
      appStore.showError(t('admin.errorPassthrough.failedToLoad'))
      console.error('Error loading rules:', error)
    }
  } finally {
    if (isCurrent()) loading.value = false
  }
}

watch(() => props.show, (visible, _previous, onCleanup) => {
  onCleanup(() => { loadRevision++ })
  rules.value = []
  loading.value = false
  if (visible) void loadRules()
}, { immediate: true })

const resetForm = () => {
  form.name = ''
  form.enabled = true
  form.priority = 0
  form.match_mode = 'any'
  form.platforms = []
  form.passthrough_code = true
  form.response_code = null
  form.passthrough_body = true
  form.custom_message = null
  form.skip_monitoring = false
  form.description = null
  errorCodesInput.value = ''
  keywordsInput.value = ''
}

const closeFormModal = () => {
  showCreateModal.value = false
  showEditModal.value = false
  editingRule.value = null
  resetForm()
}

const handleEdit = (rule: ErrorPassthroughRule) => {
  editingRule.value = rule
  form.name = rule.name
  form.enabled = rule.enabled
  form.priority = rule.priority
  form.match_mode = rule.match_mode
  form.platforms = [...rule.platforms]
  form.passthrough_code = rule.passthrough_code
  form.response_code = rule.response_code
  form.passthrough_body = rule.passthrough_body
  form.custom_message = rule.custom_message
  form.skip_monitoring = rule.skip_monitoring
  form.description = rule.description
  errorCodesInput.value = rule.error_codes.join(', ')
  keywordsInput.value = rule.keywords.join('\n')
  showEditModal.value = true
}

const handleDelete = (rule: ErrorPassthroughRule) => {
  deletingRule.value = rule
  showDeleteDialog.value = true
}

const parseErrorCodes = (): number[] => {
  if (!errorCodesInput.value.trim()) return []
  return errorCodesInput.value
    .split(/[,\s]+/)
    .map(s => parseInt(s.trim(), 10))
    .filter(n => !isNaN(n) && n > 0)
}

const parseKeywords = (): string[] => {
  if (!keywordsInput.value.trim()) return []
  return keywordsInput.value
    .split('\n')
    .map(s => s.trim())
    .filter(s => s.length > 0)
}

const handleSubmit = async () => {
  if (!form.name.trim()) {
    appStore.showError(t('admin.errorPassthrough.nameRequired'))
    return
  }

  const errorCodes = parseErrorCodes()
  const keywords = parseKeywords()

  if (errorCodes.length === 0 && keywords.length === 0) {
    appStore.showError(t('admin.errorPassthrough.conditionsRequired'))
    return
  }

  submitting.value = true
  try {
    const data = {
      name: form.name.trim(),
      enabled: form.enabled,
      priority: form.priority,
      error_codes: errorCodes,
      keywords: keywords,
      match_mode: form.match_mode,
      platforms: form.platforms,
      passthrough_code: form.passthrough_code,
      response_code: form.passthrough_code ? null : form.response_code,
      passthrough_body: form.passthrough_body,
      custom_message: form.passthrough_body ? null : form.custom_message,
      skip_monitoring: form.skip_monitoring,
      description: form.description?.trim() || null
    }

    if (showEditModal.value && editingRule.value) {
      await adminAPI.errorPassthrough.update(editingRule.value.id, data)
      appStore.showSuccess(t('admin.errorPassthrough.ruleUpdated'))
    } else {
      await adminAPI.errorPassthrough.create(data)
      appStore.showSuccess(t('admin.errorPassthrough.ruleCreated'))
    }

    closeFormModal()
    loadRules()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.errorPassthrough.failedToSave'))
    console.error('Error saving rule:', error)
  } finally {
    submitting.value = false
  }
}

const toggleEnabled = async (rule: ErrorPassthroughRule) => {
  try {
    await adminAPI.errorPassthrough.toggleEnabled(rule.id, !rule.enabled)
    rule.enabled = !rule.enabled
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.errorPassthrough.failedToToggle'))
    console.error('Error toggling rule:', error)
  }
}

const confirmDelete = async () => {
  if (!deletingRule.value) return

  try {
    await adminAPI.errorPassthrough.delete(deletingRule.value.id)
    appStore.showSuccess(t('admin.errorPassthrough.ruleDeleted'))
    showDeleteDialog.value = false
    deletingRule.value = null
    loadRules()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.errorPassthrough.failedToDelete'))
    console.error('Error deleting rule:', error)
  }
}
</script>
