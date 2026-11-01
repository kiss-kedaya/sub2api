<template>
  <BaseDialog
    :show="show"
    :title="t('admin.tlsFingerprintProfiles.title')"
    width="wide"
    @close="$emit('close')"
  >
    <div class="space-y-4">
      <!-- Toolbar: short description + create -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-muted">
          {{ t('admin.tlsFingerprintProfiles.description') }}
        </p>
        <UButton icon="i-lucide-plus" :label="t('admin.tlsFingerprintProfiles.createProfile')" @click="showCreateModal = true" />
      </div>

      <div v-if="loading" class="flex items-center justify-center py-8">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>

      <div v-else-if="profiles.length === 0" class="flex flex-col items-center gap-1 py-10 text-center">
        <UIcon name="i-lucide-shield" class="mb-2 size-8 text-dimmed" />
        <h4 class="text-sm font-medium text-highlighted">
          {{ t('admin.tlsFingerprintProfiles.noProfiles') }}
        </h4>
        <p class="text-sm text-muted">
          {{ t('admin.tlsFingerprintProfiles.createFirstProfile') }}
        </p>
      </div>

      <div v-else class="max-h-96 overflow-auto">
        <UTable :data="profiles" :columns="profileColumns" sticky :ui="tableUi">
          <template #name-cell="{ row }">
            <span class="text-sm font-medium text-highlighted">{{ row.original.name }}</span>
          </template>
          <template #description-cell="{ row }">
            <span v-if="row.original.description" class="block max-w-xs truncate text-sm text-muted">
              {{ row.original.description }}
            </span>
            <span v-else class="text-xs text-dimmed">—</span>
          </template>
          <template #grease-cell="{ row }">
            <UIcon
              :name="row.original.enable_grease ? 'i-lucide-check' : 'i-lucide-minus'"
              :class="['size-4', row.original.enable_grease ? 'text-success' : 'text-dimmed']"
              :aria-label="row.original.enable_grease ? t('admin.tlsFingerprintProfiles.form.enableGrease') : undefined"
            />
          </template>
          <template #alpn-cell="{ row }">
            <div v-if="row.original.alpn_protocols?.length" class="flex flex-wrap items-center gap-1">
              <UBadge
                v-for="proto in row.original.alpn_protocols.slice(0, 3)"
                :key="proto"
                color="neutral"
                variant="subtle"
                size="sm"
                class="font-mono"
              >
                {{ proto }}
              </UBadge>
              <span v-if="row.original.alpn_protocols.length > 3" class="text-xs text-muted">
                +{{ row.original.alpn_protocols.length - 3 }}
              </span>
            </div>
            <span v-else class="text-xs text-dimmed">—</span>
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
      :title="showEditModal ? t('admin.tlsFingerprintProfiles.editProfile') : t('admin.tlsFingerprintProfiles.createProfile')"
      width="wide"
      :z-index="60"
      @close="closeFormModal"
    >
      <UForm :state="form" class="space-y-4" @submit="handleSubmit">
        <!-- Paste YAML -->
        <UFormField :label="t('admin.tlsFingerprintProfiles.form.pasteYaml')">
          <UTextarea
            v-model="yamlInput"
            :rows="4"
            class="w-full font-mono"
            :placeholder="t('admin.tlsFingerprintProfiles.form.pasteYamlPlaceholder')"
            @paste="handleYamlPaste"
          />
          <template #help>
            <div class="flex flex-wrap items-center gap-2">
              <UButton color="neutral" variant="outline" size="xs" :label="t('admin.tlsFingerprintProfiles.form.parseYaml')" @click="parseYamlInput" />
              <span>
                {{ t('admin.tlsFingerprintProfiles.form.pasteYamlHint') }}
                <a href="https://tls.sub2api.org" target="_blank" rel="noopener noreferrer" class="text-primary underline">{{ t('admin.tlsFingerprintProfiles.form.openCollector') }}</a>
              </span>
            </div>
          </template>
        </UFormField>

        <USeparator />

        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('admin.tlsFingerprintProfiles.form.name')" required>
            <UInput
              v-model="form.name"
              required
              class="w-full"
              :placeholder="t('admin.tlsFingerprintProfiles.form.namePlaceholder')"
            />
          </UFormField>
          <UFormField :label="t('admin.tlsFingerprintProfiles.form.description')">
            <UInput
              :model-value="form.description ?? ''"
              class="w-full"
              @update:model-value="form.description = $event ? String($event) : null"
              :placeholder="t('admin.tlsFingerprintProfiles.form.descriptionPlaceholder')"
            />
          </UFormField>
        </div>

        <USwitch
          v-model="form.enable_grease"
          :label="t('admin.tlsFingerprintProfiles.form.enableGrease')"
          :description="t('admin.tlsFingerprintProfiles.form.enableGreaseHint')"
        />

        <UFormField :label="t('admin.tlsFingerprintProfiles.form.alpnProtocols')">
          <UTextarea v-model="fieldInputs.alpn_protocols" :rows="2" class="w-full font-mono" :placeholder="'h2, http/1.1'" />
        </UFormField>

        <!-- The raw TLS arrays are usually filled from the pasted YAML; keep them one click away. -->
        <UCollapsible v-model:open="showTlsArrays" class="space-y-4">
          <UButton
            color="neutral"
            variant="link"
            class="px-0"
            :label="t('admin.accounts.advancedOptions')"
            :trailing-icon="showTlsArrays ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
          />
          <template #content>
            <div class="grid gap-4 pt-1 sm:grid-cols-2">
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.cipherSuites')" :help="t('admin.tlsFingerprintProfiles.form.cipherSuitesHint')">
                <UTextarea v-model="fieldInputs.cipher_suites" :rows="2" class="w-full font-mono" :placeholder="'0x1301, 0x1302, 0xc02c'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.curves')" :help="t('admin.tlsFingerprintProfiles.form.curvesHint')">
                <UTextarea v-model="fieldInputs.curves" :rows="2" class="w-full font-mono" :placeholder="'29, 23, 24'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.signatureAlgorithms')">
                <UTextarea v-model="fieldInputs.signature_algorithms" :rows="2" class="w-full font-mono" :placeholder="'0x0403, 0x0804, 0x0401'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.supportedVersions')">
                <UTextarea v-model="fieldInputs.supported_versions" :rows="2" class="w-full font-mono" :placeholder="'0x0304, 0x0303'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.keyShareGroups')">
                <UTextarea v-model="fieldInputs.key_share_groups" :rows="2" class="w-full font-mono" :placeholder="'29, 23'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.extensions')">
                <UTextarea v-model="fieldInputs.extensions" :rows="2" class="w-full font-mono" :placeholder="'0x0000, 0x0005, 0x000a'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.pointFormats')">
                <UTextarea v-model="fieldInputs.point_formats" :rows="2" class="w-full font-mono" :placeholder="'0'" />
              </UFormField>
              <UFormField :label="t('admin.tlsFingerprintProfiles.form.pskModes')">
                <UTextarea v-model="fieldInputs.psk_modes" :rows="2" class="w-full font-mono" :placeholder="'1'" />
              </UFormField>
            </div>
          </template>
        </UCollapsible>
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
      :title="t('admin.tlsFingerprintProfiles.deleteProfile')"
      :message="t('admin.tlsFingerprintProfiles.deleteConfirmMessage', { name: deletingProfile?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { TLSFingerprintProfile } from '@/api/admin/tlsFingerprintProfile'
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

const profiles = ref<TLSFingerprintProfile[]>([])
const loading = ref(false)
const submitting = ref(false)
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const editingProfile = ref<TLSFingerprintProfile | null>(null)
const deletingProfile = ref<TLSFingerprintProfile | null>(null)
const yamlInput = ref('')
// The raw TLS arrays sit in a collapsed section; it opens when they are filled (edit / YAML).
const showTlsArrays = ref(false)

// Profile list in the template's table style: elevated header row, hairline row borders.
const profileColumns = computed<TableColumn<TLSFingerprintProfile>[]>(() => [
  { id: 'name', header: t('admin.tlsFingerprintProfiles.columns.name') },
  { id: 'description', header: t('admin.tlsFingerprintProfiles.columns.description') },
  { id: 'grease', header: t('admin.tlsFingerprintProfiles.columns.grease') },
  { id: 'alpn', header: t('admin.tlsFingerprintProfiles.columns.alpn') },
  { id: 'actions', header: t('admin.tlsFingerprintProfiles.columns.actions') }
])
const tableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 text-xs font-medium text-muted first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'py-2.5 border-b border-default',
  separator: 'h-0'
}

// Raw string inputs for array fields
const fieldInputs = reactive({
  cipher_suites: '',
  curves: '',
  point_formats: '',
  signature_algorithms: '',
  alpn_protocols: '',
  supported_versions: '',
  key_share_groups: '',
  psk_modes: '',
  extensions: ''
})

const form = reactive({
  name: '',
  description: null as string | null,
  enable_grease: false
})

// Invalidate pending loads when visibility changes or the component unmounts.
let loadRevision = 0
const loadProfiles = async () => {
  const revision = ++loadRevision
  const isCurrent = () => props.show && revision === loadRevision
  loading.value = true
  try {
    const data = await adminAPI.tlsFingerprintProfiles.list()
    if (isCurrent()) profiles.value = data
  } catch (error) {
    if (isCurrent()) {
      appStore.showError(t('admin.tlsFingerprintProfiles.loadFailed'))
      console.error('Error loading TLS fingerprint profiles:', error)
    }
  } finally {
    if (isCurrent()) loading.value = false
  }
}

watch(() => props.show, (visible, _previous, onCleanup) => {
  onCleanup(() => { loadRevision++ })
  profiles.value = []
  loading.value = false
  if (visible) void loadProfiles()
}, { immediate: true })

const resetForm = () => {
  form.name = ''
  form.description = null
  form.enable_grease = false
  fieldInputs.cipher_suites = ''
  fieldInputs.curves = ''
  fieldInputs.point_formats = ''
  fieldInputs.signature_algorithms = ''
  fieldInputs.alpn_protocols = ''
  fieldInputs.supported_versions = ''
  fieldInputs.key_share_groups = ''
  fieldInputs.psk_modes = ''
  fieldInputs.extensions = ''
  yamlInput.value = ''
  showTlsArrays.value = false
}

/**
 * Parse YAML output from tls-fingerprint-web and fill form fields.
 * Expected format:
 *   # comment lines
 *   profile_key:
 *     name: "Profile Name"
 *     enable_grease: false
 *     cipher_suites: [4866, 4867, ...]
 *     alpn_protocols: ["h2", "http/1.1"]
 *     ...
 */
const parseYamlInput = () => {
  const text = yamlInput.value.trim()
  if (!text) return

  // Simple YAML parser for flat key-value structure
  // Extracts "key: value" lines, handling arrays like [1, 2, 3] and ["h2", "http/1.1"]
  const lines = text.split('\n')

  let foundName = false

  for (const line of lines) {
    const trimmed = line.trim()
    // Skip comments and empty lines
    if (!trimmed || trimmed.startsWith('#')) continue

    // Match "key: value" pattern (must have at least 2 leading spaces to be a property)
    const match = trimmed.match(/^(\w+):\s*(.+)$/)
    if (!match) continue

    const [, key, rawValue] = match
    const value = rawValue.trim()

    switch (key) {
      case 'name': {
        // Remove surrounding quotes
        const unquoted = value.replace(/^["']|["']$/g, '')
        if (unquoted) {
          form.name = unquoted
          foundName = true
        }
        break
      }
      case 'enable_grease':
        form.enable_grease = value === 'true'
        break
      case 'cipher_suites':
      case 'curves':
      case 'point_formats':
      case 'signature_algorithms':
      case 'supported_versions':
      case 'key_share_groups':
      case 'psk_modes':
      case 'extensions': {
        // Parse YAML array: [1, 2, 3] — values are decimal integers from tls-fingerprint-web
        const arrMatch = value.match(/^\[(.*)?\]$/)
        if (arrMatch) {
          const inner = arrMatch[1] || ''
          fieldInputs[key as keyof typeof fieldInputs] = inner
            .split(',')
            .map(s => s.trim())
            .filter(s => s.length > 0)
            .join(', ')
        }
        break
      }
      case 'alpn_protocols': {
        // Parse string array: ["h2", "http/1.1"]
        const arrMatch = value.match(/^\[(.*)?\]$/)
        if (arrMatch) {
          const inner = arrMatch[1] || ''
          fieldInputs.alpn_protocols = inner
            .split(',')
            .map(s => s.trim().replace(/^["']|["']$/g, ''))
            .filter(s => s.length > 0)
            .join(', ')
        }
        break
      }
    }
  }

  showTlsArrays.value = true
  if (foundName) {
    appStore.showSuccess(t('admin.tlsFingerprintProfiles.form.yamlParsed'))
  } else {
    appStore.showError(t('admin.tlsFingerprintProfiles.form.yamlParseFailed'))
  }
}

// Auto-parse on paste event
const handleYamlPaste = () => {
  // Use nextTick to ensure v-model has updated
  setTimeout(() => parseYamlInput(), 50)
}

const closeFormModal = () => {
  showCreateModal.value = false
  showEditModal.value = false
  editingProfile.value = null
  resetForm()
}

// Parse a comma-separated string of numbers supporting both hex (0x...) and decimal
const parseNumericArray = (input: string): number[] => {
  if (!input.trim()) return []
  return input
    .split(',')
    .map(s => s.trim())
    .filter(s => s.length > 0)
    .map(s => s.startsWith('0x') || s.startsWith('0X') ? parseInt(s, 16) : parseInt(s, 10))
    .filter(n => !isNaN(n))
}

// Parse a comma-separated string of string values
const parseStringArray = (input: string): string[] => {
  if (!input.trim()) return []
  return input
    .split(',')
    .map(s => s.trim())
    .filter(s => s.length > 0)
}

// Format a number as hex with 0x prefix and 4-digit padding
const formatHex = (n: number): string => '0x' + n.toString(16).padStart(4, '0')

// Format numeric arrays for display in textarea (null-safe)
const formatNumericArray = (arr: number[] | null | undefined): string => (arr ?? []).map(formatHex).join(', ')

// For point_formats and psk_modes (uint8), show as plain numbers (null-safe)
const formatPlainNumericArray = (arr: number[] | null | undefined): string => (arr ?? []).join(', ')

const handleEdit = (profile: TLSFingerprintProfile) => {
  editingProfile.value = profile
  form.name = profile.name
  form.description = profile.description
  form.enable_grease = profile.enable_grease
  fieldInputs.cipher_suites = formatNumericArray(profile.cipher_suites)
  fieldInputs.curves = formatPlainNumericArray(profile.curves)
  fieldInputs.point_formats = formatPlainNumericArray(profile.point_formats)
  fieldInputs.signature_algorithms = formatNumericArray(profile.signature_algorithms)
  fieldInputs.alpn_protocols = (profile.alpn_protocols ?? []).join(', ')
  fieldInputs.supported_versions = formatNumericArray(profile.supported_versions)
  fieldInputs.key_share_groups = formatPlainNumericArray(profile.key_share_groups)
  fieldInputs.psk_modes = formatPlainNumericArray(profile.psk_modes)
  fieldInputs.extensions = formatNumericArray(profile.extensions)
  showTlsArrays.value = true
  showEditModal.value = true
}

const handleDelete = (profile: TLSFingerprintProfile) => {
  deletingProfile.value = profile
  showDeleteDialog.value = true
}

const handleSubmit = async () => {
  if (!form.name.trim()) {
    appStore.showError(t('admin.tlsFingerprintProfiles.form.name') + ' ' + t('common.required'))
    return
  }

  submitting.value = true
  try {
    const data = {
      name: form.name.trim(),
      description: form.description?.trim() || null,
      enable_grease: form.enable_grease,
      cipher_suites: parseNumericArray(fieldInputs.cipher_suites),
      curves: parseNumericArray(fieldInputs.curves),
      point_formats: parseNumericArray(fieldInputs.point_formats),
      signature_algorithms: parseNumericArray(fieldInputs.signature_algorithms),
      alpn_protocols: parseStringArray(fieldInputs.alpn_protocols),
      supported_versions: parseNumericArray(fieldInputs.supported_versions),
      key_share_groups: parseNumericArray(fieldInputs.key_share_groups),
      psk_modes: parseNumericArray(fieldInputs.psk_modes),
      extensions: parseNumericArray(fieldInputs.extensions)
    }

    if (showEditModal.value && editingProfile.value) {
      await adminAPI.tlsFingerprintProfiles.update(editingProfile.value.id, data)
      appStore.showSuccess(t('admin.tlsFingerprintProfiles.updateSuccess'))
    } else {
      await adminAPI.tlsFingerprintProfiles.create(data)
      appStore.showSuccess(t('admin.tlsFingerprintProfiles.createSuccess'))
    }

    closeFormModal()
    loadProfiles()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.tlsFingerprintProfiles.saveFailed'))
    console.error('Error saving TLS fingerprint profile:', error)
  } finally {
    submitting.value = false
  }
}

const confirmDelete = async () => {
  if (!deletingProfile.value) return

  try {
    await adminAPI.tlsFingerprintProfiles.delete(deletingProfile.value.id)
    appStore.showSuccess(t('admin.tlsFingerprintProfiles.deleteSuccess'))
    showDeleteDialog.value = false
    deletingProfile.value = null
    loadProfiles()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.tlsFingerprintProfiles.deleteFailed'))
    console.error('Error deleting TLS fingerprint profile:', error)
  }
}
</script>
