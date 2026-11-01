<template>
  <section aria-labelledby="prompt-pool-title">
    <UPageCard variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }" class="mb-4" :description="t('admin.promptAudit.pool.description')">
      <template #title><h2 id="prompt-pool-title">{{ t('admin.promptAudit.pool.title') }}</h2></template>
      <UButton icon="i-lucide-plus" :label="t('admin.promptAudit.pool.add')" class="w-fit lg:ms-auto" data-test="add-endpoint" @click="openCreate" />
    </UPageCard>

    <UPageCard variant="subtle" :ui="{ container: 'p-0 sm:p-0 gap-y-0', wrapper: 'items-stretch' }">
      <p v-if="endpoints.length === 0" class="px-4 py-10 text-center text-sm text-muted sm:px-6">{{ t('admin.promptAudit.pool.empty') }}</p>
      <ul v-else role="list" class="divide-y divide-default">
        <li
          v-for="endpoint in endpoints"
          :key="endpoint.id"
          :data-test="`endpoint-${endpoint.id}`"
          class="grid gap-3 px-4 py-3 sm:px-6 xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_auto] xl:items-center xl:gap-5"
        >
          <div class="flex min-w-0 items-center gap-3">
            <USwitch :model-value="endpoint.enabled" :aria-label="t('admin.promptAudit.pool.toggleNode', { name: endpoint.name })" @update:model-value="toggleEndpoint(endpoint.id)" />
            <div class="min-w-0">
              <p class="truncate font-medium text-highlighted">{{ endpoint.name }}</p>
              <p class="truncate font-mono text-xs text-muted" :title="endpoint.base_url">{{ endpoint.base_url }}</p>
            </div>
          </div>

          <div class="flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
            <span class="max-w-full truncate text-sm text-toned" :title="`${t('admin.promptAudit.pool.model')}: ${endpoint.model}`">{{ endpoint.model }}</span>
            <UBadge color="neutral" variant="subtle" class="tabular-nums" :title="t('admin.promptAudit.pool.limits')">{{ endpoint.timeout_ms }} ms · {{ endpoint.input_limit }} chars</UBadge>
            <UBadge :color="credentialInvalid(endpoint) ? 'error' : hasCredential(endpoint) ? 'success' : 'neutral'" variant="subtle" :title="t('admin.promptAudit.pool.credential')">
              {{ credentialInvalid(endpoint) ? t('admin.promptAudit.pool.invalid') : hasCredential(endpoint) ? t('admin.promptAudit.pool.configured') : t('admin.promptAudit.pool.missing') }}
            </UBadge>
            <p v-if="probingIds.includes(endpoint.id)" class="w-full text-primary">{{ t('admin.promptAudit.pool.probeProgress') }}</p>
            <p v-if="probeResults[endpoint.id]" class="line-clamp-2 w-full leading-5" :class="probeResults[endpoint.id].ok ? 'text-success' : 'text-error'">
              {{ t('admin.promptAudit.pool.probeResult', { status: probeResults[endpoint.id].status, http: probeResults[endpoint.id].http_status || '—', latency: probeResults[endpoint.id].latency_ms }) }}
              · {{ probeResults[endpoint.id].message }}
            </p>
          </div>

          <div class="flex items-center justify-end gap-1">
            <UButton color="neutral" variant="outline" size="sm" :loading="probingIds.includes(endpoint.id)" :label="probingIds.includes(endpoint.id) ? t('admin.promptAudit.pool.probing') : t('admin.promptAudit.pool.probe')" @click="$emit('probe', endpoint)" />
            <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-pencil" :label="t('common.edit')" @click="openEdit(endpoint)" />
            <UButton color="error" variant="ghost" size="sm" icon="i-lucide-trash-2" :aria-label="`${t('common.delete')} ${endpoint.name}`" :title="t('common.delete')" @click="removeEndpoint(endpoint)" />
          </div>
        </li>
      </ul>
    </UPageCard>
    <BaseDialog :show="Boolean(editing)" :title="editingIndex < 0 ? t('admin.promptAudit.pool.add') : t('admin.promptAudit.pool.edit')" width="wide" @close="closeEditor">
      <form v-if="editing" class="grid gap-4 sm:grid-cols-2" @submit.prevent="saveEditor">
        <UFormField :label="t('admin.promptAudit.pool.name')" required>
          <UInput v-model="editing.name" class="w-full" required :aria-label="t('admin.promptAudit.pool.name')" />
        </UFormField>
        <UFormField :label="t('admin.promptAudit.pool.id')" required>
          <UInput v-model="editing.id" class="w-full" required :disabled="editingIndex >= 0" :aria-label="t('admin.promptAudit.pool.id')" />
        </UFormField>
        <UFormField :label="t('admin.promptAudit.pool.baseUrl')" required class="sm:col-span-2">
          <UInput v-model="editing.base_url" class="w-full font-mono" required inputmode="url" :aria-label="t('admin.promptAudit.pool.baseUrl')" />
        </UFormField>
        <UFormField :label="t('admin.promptAudit.pool.apiKey')" :help="t('admin.promptAudit.pool.secretHint')" class="sm:col-span-2">
          <UInput
            v-model="editing.token"
            class="w-full"
            type="password"
            autocomplete="new-password"
            :placeholder="editing.has_token ? (editing.token_status === 'invalid' ? t('admin.promptAudit.pool.reenterSecret') : t('admin.promptAudit.pool.keepSecret')) : ''"
            :aria-label="t('admin.promptAudit.pool.apiKey')"
          />
        </UFormField>
        <UCheckbox
          v-if="editing.has_token"
          v-model="editing.clear_token"
          color="error"
          class="sm:col-span-2"
          :label="t('admin.promptAudit.pool.clearSecret')"
          :aria-label="t('admin.promptAudit.pool.clearSecret')"
        />
        <UFormField :label="t('admin.promptAudit.pool.model')" class="sm:col-span-2">
          <UInput v-model="editing.model" class="w-full" :aria-label="t('admin.promptAudit.pool.model')" />
        </UFormField>
        <UCollapsible :unmount-on-hide="false" class="sm:col-span-2">
          <template #default="{ open }">
            <UButton color="neutral" variant="ghost" size="sm" :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" :label="t('admin.promptAudit.pool.advanced')" class="-ms-2.5" />
          </template>
          <template #content>
            <div class="mt-3 grid gap-4 sm:grid-cols-2">
              <UFormField :label="t('admin.promptAudit.pool.timeout')">
                <UInput v-model.number="editing.timeout_ms" class="w-full" type="number" min="100" max="30000" required :aria-label="t('admin.promptAudit.pool.timeout')" />
              </UFormField>
              <UFormField :label="t('admin.promptAudit.pool.inputLimit')">
                <UInput v-model.number="editing.input_limit" class="w-full" type="number" min="128" max="100000" required :aria-label="t('admin.promptAudit.pool.inputLimit')" />
              </UFormField>
            </div>
          </template>
        </UCollapsible>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeEditor" />
          <UButton :label="t('common.save')" data-test="save-endpoint" @click="saveEditor" />
        </div>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { PromptAuditEndpointDraft, PromptProbeResult } from '../types'
import { cloneData, createDefaultEndpoint } from '../viewModel'
import { confirm } from '@/composables/useConfirmation'

const props = defineProps<{
  endpoints: PromptAuditEndpointDraft[]
  probeResults: Record<string, PromptProbeResult>
  probingIds: string[]
}>()
const emit = defineEmits<{
  (event: 'update:endpoints', value: PromptAuditEndpointDraft[]): void
  (event: 'probe', endpoint: PromptAuditEndpointDraft): void
}>()
const { t } = useI18n()
const editing = ref<PromptAuditEndpointDraft | null>(null)
const editingIndex = ref(-1)

function openCreate() {
  editingIndex.value = -1
  editing.value = createDefaultEndpoint(props.endpoints.length + 1)
}
function openEdit(endpoint: PromptAuditEndpointDraft) {
  editingIndex.value = props.endpoints.findIndex((item) => item.id === endpoint.id)
  editing.value = cloneData(endpoint)
}
function closeEditor() {
  editing.value = null
  editingIndex.value = -1
}
function saveEditor() {
  if (!editing.value?.id.trim() || !editing.value.name.trim() || !editing.value.base_url.trim()) return
  const next = props.endpoints.map((item) => cloneData(item))
  const value = cloneData(editing.value)
  if (value.token.trim()) value.clear_token = false
  if (editingIndex.value < 0) next.push(value)
  else next.splice(editingIndex.value, 1, value)
  emit('update:endpoints', next)
  closeEditor()
}
function toggleEndpoint(id: string) {
  emit('update:endpoints', props.endpoints.map((item) => item.id === id ? { ...item, enabled: !item.enabled } : cloneData(item)))
}
async function removeEndpoint(endpoint: PromptAuditEndpointDraft) {
  if (!(await confirm(t('admin.promptAudit.pool.deleteConfirm', { name: endpoint.name })))) return
  emit('update:endpoints', props.endpoints.filter((item) => item.id !== endpoint.id).map((item) => cloneData(item)))
}
function hasCredential(endpoint: PromptAuditEndpointDraft): boolean {
  return Boolean(endpoint.token.trim() || (endpoint.has_token && !endpoint.clear_token))
}
function credentialInvalid(endpoint: PromptAuditEndpointDraft): boolean {
  return endpoint.token_status === 'invalid' && !endpoint.token.trim() && !endpoint.clear_token
}
</script>
