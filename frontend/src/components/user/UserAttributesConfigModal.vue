<template>
  <BaseDialog :show="show" :title="t('admin.users.attributes.title')" width="wide" @close="emit('close')">
    <div class="space-y-4">
      <!-- Description with the add action -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-muted">
          {{ t('admin.users.attributes.description') }}
        </p>
        <UButton icon="i-lucide-plus" @click="openCreateModal">
          {{ t('admin.users.attributes.addAttribute') }}
        </UButton>
      </div>

      <!-- Loading State -->
      <div v-if="loading" class="flex justify-center py-12">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-dimmed" />
      </div>

      <!-- Empty State -->
      <div v-else-if="attributes.length === 0" class="flex flex-col items-center py-12 text-center">
        <UIcon name="i-lucide-tag" class="size-10 text-dimmed" />
        <p class="mt-2 text-sm text-muted">
          {{ t('admin.users.attributes.noAttributes') }}
        </p>
        <p class="text-xs text-dimmed">
          {{ t('admin.users.attributes.noAttributesHint') }}
        </p>
      </div>

      <!-- Attributes List: hairline-divided rows -->
      <div v-else class="max-h-96 divide-y divide-default overflow-y-auto rounded-lg ring ring-default">
        <div
          v-for="attr in attributes"
          :key="attr.id"
          class="flex items-center gap-3 px-3 py-2.5"
        >
          <!-- Drag Handle -->
          <span class="flex shrink-0 cursor-move text-dimmed" :title="t('admin.users.attributes.dragToReorder')">
            <UIcon name="i-lucide-grip-vertical" class="size-5" />
          </span>

          <!-- Attribute Info -->
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium text-highlighted">{{ attr.name }}</span>
              <UBadge color="neutral" variant="subtle" size="sm" class="font-mono">{{ attr.key }}</UBadge>
              <UBadge v-if="attr.required" color="error" variant="subtle" size="sm">
                {{ t('admin.users.attributes.required') }}
              </UBadge>
              <UBadge v-if="!attr.enabled" color="neutral" variant="outline" size="sm">
                {{ t('common.disabled') }}
              </UBadge>
            </div>
            <div class="mt-0.5 flex items-center gap-2 text-xs text-muted">
              <UBadge color="neutral" variant="outline" size="sm">{{ t(`admin.users.attributes.types.${attr.type}`) }}</UBadge>
              <span v-if="attr.description" class="truncate">{{ attr.description }}</span>
            </div>
          </div>

          <!-- Actions -->
          <div class="flex items-center gap-0.5">
            <UButton
              icon="i-lucide-pencil"
              color="neutral"
              variant="ghost"
              :title="t('common.edit')"
              :aria-label="t('common.edit')"
              @click="openEditModal(attr)"
            />
            <UButton
              icon="i-lucide-trash-2"
              color="error"
              variant="ghost"
              :title="t('common.delete')"
              :aria-label="t('common.delete')"
              @click="confirmDelete(attr)"
            />
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="subtle" @click="emit('close')">
          {{ t('common.close') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>

  <!-- Create/Edit Attribute Modal -->
  <BaseDialog
    :show="showEditModal"
    :title="editingAttribute ? t('admin.users.attributes.editAttribute') : t('admin.users.attributes.addAttribute')"
    width="normal"
    @close="closeEditModal"
  >
    <form id="attribute-form" @submit.prevent="handleSave" class="space-y-4">
      <UFormField :label="t('admin.users.attributes.key')" :help="t('admin.users.attributes.keyHint')" required>
        <UInput
          v-model="form.key"
          type="text"
          required
          pattern="^[a-zA-Z][a-zA-Z0-9_]*$"
          class="w-full"
          :ui="{ base: 'font-mono' }"
          :placeholder="t('admin.users.attributes.keyHint')"
          :disabled="!!editingAttribute"
        />
      </UFormField>

      <UFormField :label="t('admin.users.attributes.name')" required>
        <UInput
          v-model="form.name"
          type="text"
          required
          class="w-full"
          :placeholder="t('admin.users.attributes.nameHint')"
        />
      </UFormField>

      <UFormField :label="t('admin.users.attributes.type')">
        <Select
          v-model="form.type"
          :options="attributeTypes.map(type => ({ value: type, label: t(`admin.users.attributes.types.${type}`) }))"
        />
      </UFormField>

      <!-- Options (for select/multi_select) -->
      <UFormField
        v-if="form.type === 'select' || form.type === 'multi_select'"
        :label="t('admin.users.attributes.options')"
        :ui="{ container: 'mt-1 space-y-2' }"
      >
        <div v-for="(option, index) in form.options" :key="getOptionKey(option)" class="flex items-center gap-2">
          <UInput
            v-model="option.value"
            type="text"
            class="flex-1"
            :ui="{ base: 'font-mono' }"
            :placeholder="t('admin.users.attributes.optionValue')"
            required
          />
          <UInput
            v-model="option.label"
            type="text"
            class="flex-1"
            :placeholder="t('admin.users.attributes.optionLabel')"
            required
          />
          <UButton
            icon="i-lucide-x"
            color="neutral"
            variant="ghost"
            :aria-label="t('common.delete')"
            @click="removeOption(index)"
          />
        </div>
        <UButton icon="i-lucide-plus" color="neutral" variant="outline" size="sm" @click="addOption">
          {{ t('admin.users.attributes.addOption') }}
        </UButton>
      </UFormField>

      <UFormField :label="t('admin.users.attributes.fieldDescription')">
        <UInput
          v-model="form.description"
          type="text"
          class="w-full"
          :placeholder="t('admin.users.attributes.fieldDescriptionHint')"
        />
      </UFormField>

      <UFormField :label="t('admin.users.attributes.placeholder')">
        <UInput
          v-model="form.placeholder"
          type="text"
          class="w-full"
          :placeholder="t('admin.users.attributes.placeholderHint')"
        />
      </UFormField>

      <!-- Required & Enabled -->
      <div class="flex items-center gap-6">
        <UCheckbox v-model="form.required" :label="t('admin.users.attributes.required')" />
        <UCheckbox v-model="form.enabled" :label="t('admin.users.attributes.enabled')" />
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" @click="closeEditModal">
          {{ t('common.cancel') }}
        </UButton>
        <UButton type="submit" form="attribute-form" :loading="saving" :disabled="saving">
          {{ saving ? t('common.saving') : (editingAttribute ? t('common.update') : t('common.create')) }}
        </UButton>
      </div>
    </template>
  </BaseDialog>

  <!-- Delete Confirmation -->
  <ConfirmDialog
    :show="showDeleteDialog"
    :title="t('admin.users.attributes.deleteAttribute')"
    :message="t('admin.users.attributes.deleteConfirm', { name: deletingAttribute?.name })"
    :confirm-text="t('common.delete')"
    :cancel-text="t('common.cancel')"
    :danger="true"
    @confirm="handleDelete"
    @cancel="showDeleteDialog = false"
  />
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { UserAttributeDefinition, UserAttributeType, UserAttributeOption } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'

const { t } = useI18n()
const appStore = useAppStore()

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const attributeTypes: UserAttributeType[] = ['text', 'textarea', 'number', 'email', 'url', 'date', 'select', 'multi_select']

const loading = ref(false)
const saving = ref(false)
const attributes = ref<UserAttributeDefinition[]>([])
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const editingAttribute = ref<UserAttributeDefinition | null>(null)
const deletingAttribute = ref<UserAttributeDefinition | null>(null)
const getOptionKey = createStableObjectKeyResolver<UserAttributeOption>('user-attr-option')

const form = reactive({
  key: '',
  name: '',
  type: 'text' as UserAttributeType,
  description: '',
  placeholder: '',
  required: false,
  enabled: true,
  options: [] as UserAttributeOption[]
})

const loadAttributes = async () => {
  loading.value = true
  try {
    attributes.value = await adminAPI.userAttributes.listDefinitions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.users.attributes.failedToLoad'))
  } finally {
    loading.value = false
  }
}

const openCreateModal = () => {
  editingAttribute.value = null
  form.key = ''
  form.name = ''
  form.type = 'text'
  form.description = ''
  form.placeholder = ''
  form.required = false
  form.enabled = true
  form.options = []
  showEditModal.value = true
}

const openEditModal = (attr: UserAttributeDefinition) => {
  editingAttribute.value = attr
  form.key = attr.key
  form.name = attr.name
  form.type = attr.type
  form.description = attr.description || ''
  form.placeholder = attr.placeholder || ''
  form.required = attr.required
  form.enabled = attr.enabled
  form.options = attr.options ? attr.options.map((opt) => ({ ...opt })) : []
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editingAttribute.value = null
}

const addOption = () => {
  form.options.push({ value: '', label: '' })
}

const removeOption = (index: number) => {
  form.options.splice(index, 1)
}

const handleSave = async () => {
  if (!form.key.trim()) {
    appStore.showError(t('admin.users.attributes.keyRequired'))
    return
  }
  if (!form.name.trim()) {
    appStore.showError(t('admin.users.attributes.nameRequired'))
    return
  }
  if ((form.type === 'select' || form.type === 'multi_select') && form.options.length === 0) {
    appStore.showError(t('admin.users.attributes.optionsRequired'))
    return
  }
  saving.value = true
  try {
    const data = {
      key: form.key,
      name: form.name,
      type: form.type,
      description: form.description || undefined,
      placeholder: form.placeholder || undefined,
      required: form.required,
      enabled: form.enabled,
      options: (form.type === 'select' || form.type === 'multi_select') ? form.options : undefined
    }

    if (editingAttribute.value) {
      await adminAPI.userAttributes.updateDefinition(editingAttribute.value.id, data)
      appStore.showSuccess(t('admin.users.attributes.updated'))
    } else {
      await adminAPI.userAttributes.createDefinition(data)
      appStore.showSuccess(t('admin.users.attributes.created'))
    }

    closeEditModal()
    loadAttributes()
  } catch (error: any) {
    const msg = editingAttribute.value
      ? t('admin.users.attributes.failedToUpdate')
      : t('admin.users.attributes.failedToCreate')
    appStore.showError(error.response?.data?.detail || msg)
  } finally {
    saving.value = false
  }
}

const confirmDelete = (attr: UserAttributeDefinition) => {
  deletingAttribute.value = attr
  showDeleteDialog.value = true
}

const handleDelete = async () => {
  if (!deletingAttribute.value) return

  try {
    await adminAPI.userAttributes.deleteDefinition(deletingAttribute.value.id)
    appStore.showSuccess(t('admin.users.attributes.deleted'))
    showDeleteDialog.value = false
    deletingAttribute.value = null
    loadAttributes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.users.attributes.failedToDelete'))
  }
}

watch(() => props.show, (isShow) => {
  if (isShow) {
    loadAttributes()
  }
})
</script>
