<template>
  <BaseDialog
    :show="show"
    :title="t('admin.channelMonitor.template.managerTitle')"
    width="wide"
    @close="$emit('close')"
  >
    <!-- provider tabs -->
    <UTabs
      v-model="activeProvider"
      :items="providerTabItems"
      :content="false"
      variant="link"
      class="mb-4 w-full"
      :ui="{ list: 'border-b border-default overflow-x-auto', trigger: 'pb-2.5' }"
    >
      <template #trailing="{ item }">
        <UBadge v-if="countByProvider[item.value as Provider] > 0" color="neutral" variant="subtle" size="sm">{{ countByProvider[item.value as Provider] }}</UBadge>
      </template>
    </UTabs>

    <!-- active provider list -->
    <div v-if="!editing" class="flex flex-col gap-2">
      <div class="flex justify-end">
        <UButton size="sm" icon="i-lucide-plus" :label="t('admin.channelMonitor.template.createButton')" @click="openCreateForm" />
      </div>

      <div v-if="loading" class="py-8 text-center text-sm text-muted">
        {{ t('common.loading') }}
      </div>

      <div
        v-else-if="templatesForActiveProvider.length === 0"
        class="py-8 text-center text-sm text-muted"
      >
        {{ t('admin.channelMonitor.template.emptyState') }}
      </div>

      <ul v-else class="flex flex-col divide-y divide-default">
        <li
          v-for="tpl in templatesForActiveProvider"
          :key="tpl.id"
          class="flex items-start justify-between gap-3 py-3"
        >
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-1.5">
              <span class="font-medium text-highlighted">{{ tpl.name }}</span>
              <UBadge :color="modeColor(tpl.body_override_mode)" variant="subtle" size="sm">{{ modeLabel(tpl.body_override_mode) }}</UBadge>
              <UBadge v-if="tpl.provider === PROVIDER_OPENAI" color="neutral" variant="outline" size="sm">{{ apiModeLabel(tpl.api_mode) }}</UBadge>
              <span v-if="tpl.associated_monitors > 0" class="text-xs text-muted">
                {{ t('admin.channelMonitor.template.associatedCount', { n: tpl.associated_monitors }) }}
              </span>
            </div>
            <p v-if="tpl.description" class="mt-0.5 text-xs text-muted">
              {{ tpl.description }}
            </p>
            <p class="mt-1 text-xs text-dimmed">
              {{ t('admin.channelMonitor.template.headersSummary', {
                n: Object.keys(tpl.extra_headers || {}).length,
              }) }}
            </p>
          </div>
          <div class="flex flex-shrink-0 items-center gap-1">
            <UButton
              color="neutral"
              variant="outline"
              size="sm"
              icon="i-lucide-refresh-cw"
              :disabled="tpl.associated_monitors === 0"
              :title="t('admin.channelMonitor.template.applyTooltip')"
              :label="t('admin.channelMonitor.template.applyButton')"
              @click="confirmApply(tpl)"
            />
            <UButton color="neutral" variant="ghost" size="sm" square icon="i-lucide-pencil" :title="t('common.edit')" :aria-label="t('common.edit')" @click="openEditForm(tpl)" />
            <UButton color="error" variant="ghost" size="sm" square icon="i-lucide-trash-2" :title="t('common.delete')" :aria-label="t('common.delete')" @click="handleDelete(tpl)" />
          </div>
        </li>
      </ul>
    </div>

    <!-- edit / create form -->
    <div v-else class="flex flex-col gap-4">
      <UFormField :label="t('admin.channelMonitor.template.form.name')" required>
        <UInput
          v-model="form.name"
          type="text"
          required
          class="w-full"
          :placeholder="t('admin.channelMonitor.template.form.namePlaceholder')"
        />
      </UFormField>

      <UFormField v-if="editing === 'new'" :label="t('admin.channelMonitor.form.provider')" required>
        <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <UButton
            v-for="opt in providerTabs"
            :key="opt.value"
            type="button"
            :aria-pressed="form.provider === opt.value"
            :color="form.provider === opt.value ? 'primary' : 'neutral'"
            variant="outline"
            class="h-auto justify-center px-3 py-2"
            :class="form.provider === opt.value ? 'bg-primary/5' : ''"
            :label="opt.label"
            @click="form.provider = opt.value"
          />
        </div>
      </UFormField>

      <UFormField v-if="form.provider === PROVIDER_OPENAI" :label="t('admin.channelMonitor.form.apiMode')">
        <div class="grid gap-2 sm:grid-cols-2">
          <UButton
            v-for="opt in apiModeOptions"
            :key="opt.value"
            type="button"
            :aria-pressed="form.api_mode === opt.value"
            :color="form.api_mode === opt.value ? 'primary' : 'neutral'"
            variant="outline"
            class="block h-auto w-full px-3 py-2 text-left"
            :class="form.api_mode === opt.value ? 'bg-primary/5' : ''"
            @click="form.api_mode = opt.value"
          >
            <span class="block text-sm font-semibold">{{ opt.label }}</span>
            <span class="mt-0.5 block text-xs font-normal text-muted">{{ opt.hint }}</span>
          </UButton>
        </div>
      </UFormField>

      <UFormField :label="t('admin.channelMonitor.template.form.description')">
        <UInput
          v-model="form.description"
          type="text"
          class="w-full"
          :placeholder="t('admin.channelMonitor.template.form.descriptionPlaceholder')"
        />
      </UFormField>

      <MonitorAdvancedRequestConfig
        :provider="form.provider"
        :api-mode="form.api_mode"
        :extra-headers="form.extra_headers"
        :body-override-mode="form.body_override_mode"
        :body-override="form.body_override"
        @update:extra-headers="form.extra_headers = $event"
        @update:body-override-mode="form.body_override_mode = $event"
        @update:body-override="form.body_override = $event"
      />
    </div>

    <template #footer>
      <div class="flex w-full items-center justify-between">
        <!-- Left: back to list / nothing -->
        <div>
          <UButton v-if="editing" color="neutral" variant="ghost" icon="i-lucide-arrow-left" :label="t('common.back')" @click="backToList" />
        </div>
        <!-- Right: save or close -->
        <div class="flex gap-2">
          <UButton color="neutral" variant="outline" :label="t('common.close')" @click="$emit('close')" />
          <UButton
            v-if="editing"
            :loading="submitting"
            :disabled="submitting"
            :label="submitting ? t('common.submitting') : editing === 'new' ? t('common.create') : t('common.update')"
            @click="handleSubmit"
          />
        </div>
      </div>
    </template>
  </BaseDialog>

  <MonitorTemplateApplyPickerDialog
    :show="applyPicker.show"
    :template-id="applyPicker.tpl ? applyPicker.tpl.id : null"
    :template-name="applyPicker.tpl ? applyPicker.tpl.name : ''"
    @close="applyPicker.show = false"
    @applied="onApplied"
  />

  <ConfirmDialog
    :show="confirmDelete.show"
    :title="t('common.delete')"
    :message="confirmDeleteMessage"
    :confirm-text="t('common.delete')"
    :cancel-text="t('common.cancel')"
    :danger="true"
    @confirm="doDelete"
    @cancel="confirmDelete.show = false"
  />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { adminAPI } from '@/api/admin'
import type {
  APIMode,
  BodyOverrideMode,
  Provider,
} from '@/api/admin/channelMonitor'
import type { ChannelMonitorTemplate } from '@/api/admin/channelMonitorTemplate'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import MonitorAdvancedRequestConfig from '@/components/admin/monitor/MonitorAdvancedRequestConfig.vue'
import MonitorTemplateApplyPickerDialog from '@/components/admin/monitor/MonitorTemplateApplyPickerDialog.vue'
import {
  PROVIDER_ANTHROPIC,
  PROVIDER_OPENAI,
  PROVIDER_GEMINI,
  PROVIDER_GROK,
  PROVIDER_ANTIGRAVITY,
  PROVIDER_KIMI,
  PROVIDER_ZHIPU,
  PROVIDER_DEEPSEEK,
  PROVIDER_MINIMAX,
  PROVIDERS,
  API_MODE_CHAT_COMPLETIONS,
  API_MODE_RESPONSES,
} from '@/constants/channelMonitor'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  (e: 'close'): void
  /** Fired when any template changed (create / update / delete / apply). */
  (e: 'updated'): void
}>()

const { t } = useI18n()
const appStore = useAppStore()

const providerTabs = computed<{ value: Provider; label: string }[]>(() => [
  { value: PROVIDER_ANTHROPIC, label: t('monitorCommon.providers.anthropic') },
  { value: PROVIDER_OPENAI, label: t('monitorCommon.providers.openai') },
  { value: PROVIDER_GEMINI, label: t('monitorCommon.providers.gemini') },
  { value: PROVIDER_GROK, label: t('monitorCommon.providers.grok') },
  { value: PROVIDER_ANTIGRAVITY, label: t('monitorCommon.providers.antigravity') },
  { value: PROVIDER_KIMI, label: t('monitorCommon.providers.kimi') },
  { value: PROVIDER_ZHIPU, label: t('monitorCommon.providers.zhipu') },
  { value: PROVIDER_DEEPSEEK, label: t('monitorCommon.providers.deepseek') },
  { value: PROVIDER_MINIMAX, label: t('monitorCommon.providers.minimax') },
])

const activeProvider = ref<Provider>(PROVIDER_ANTHROPIC)
const providerTabItems = computed(() => providerTabs.value.map(tab => ({ value: tab.value, label: tab.label })))
const templates = ref<ChannelMonitorTemplate[]>([])
const loading = ref(false)

const templatesForActiveProvider = computed(() =>
  templates.value.filter((t) => t.provider === activeProvider.value),
)

const countByProvider = computed<Record<Provider, number>>(() => {
  const out = Object.fromEntries(PROVIDERS.map((p) => [p, 0])) as Record<Provider, number>
  for (const t of templates.value) out[t.provider]++
  return out
})

// --- form state ---
interface TemplateForm {
  id: number | null
  name: string
  provider: Provider
  api_mode: APIMode
  description: string
  extra_headers: Record<string, string>
  body_override_mode: BodyOverrideMode
  body_override: Record<string, unknown> | null
}

const editing = ref<null | 'new' | number>(null) // null = list view; 'new' = create; <id> = edit
const submitting = ref(false)
const form = reactive<TemplateForm>(emptyForm(PROVIDER_ANTHROPIC))

function emptyForm(provider: Provider): TemplateForm {
  return {
    id: null,
    name: '',
    provider,
    api_mode: API_MODE_CHAT_COMPLETIONS,
    description: '',
    extra_headers: {},
    body_override_mode: 'off',
    body_override: null,
  }
}

function loadForm(tpl: ChannelMonitorTemplate) {
  form.id = tpl.id
  form.name = tpl.name
  form.provider = tpl.provider
  form.api_mode = normalizeAPIMode(tpl.api_mode)
  form.description = tpl.description
  form.extra_headers = { ...(tpl.extra_headers || {}) }
  form.body_override_mode = tpl.body_override_mode
  form.body_override = tpl.body_override ? { ...tpl.body_override } : null
}

function openCreateForm() {
  Object.assign(form, emptyForm(activeProvider.value))
  editing.value = 'new'
}

function openEditForm(tpl: ChannelMonitorTemplate) {
  loadForm(tpl)
  editing.value = tpl.id
}

function backToList() {
  editing.value = null
}

// --- data fetch ---
async function fetchTemplates() {
  loading.value = true
  try {
    const { items } = await adminAPI.channelMonitorTemplate.list()
    templates.value = items
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      editing.value = null
      fetchTemplates()
    }
  },
  { immediate: true },
)

// --- submit ---
async function handleSubmit() {
  if (submitting.value) return
  if (!form.name.trim()) {
    appStore.showError(t('admin.channelMonitor.template.missingName'))
    return
  }
  submitting.value = true
  try {
    if (editing.value === 'new') {
      await adminAPI.channelMonitorTemplate.create({
        name: form.name.trim(),
        provider: form.provider,
        api_mode: form.provider === PROVIDER_OPENAI ? form.api_mode : API_MODE_CHAT_COMPLETIONS,
        description: form.description.trim(),
        extra_headers: form.extra_headers,
        body_override_mode: form.body_override_mode,
        body_override: form.body_override,
      })
      appStore.showSuccess(t('admin.channelMonitor.template.createSuccess'))
    } else if (typeof editing.value === 'number') {
      await adminAPI.channelMonitorTemplate.update(editing.value, {
        name: form.name.trim(),
        api_mode: form.provider === PROVIDER_OPENAI ? form.api_mode : API_MODE_CHAT_COMPLETIONS,
        description: form.description.trim(),
        extra_headers: form.extra_headers,
        body_override_mode: form.body_override_mode,
        body_override: form.body_override,
      })
      appStore.showSuccess(t('admin.channelMonitor.template.updateSuccess'))
    }
    await fetchTemplates()
    emit('updated')
    editing.value = null
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    submitting.value = false
  }
}

// --- apply to monitors (picker 流程) ---
const applyPicker = reactive<{ show: boolean; tpl: ChannelMonitorTemplate | null }>({
  show: false,
  tpl: null,
})

function confirmApply(tpl: ChannelMonitorTemplate) {
  applyPicker.tpl = tpl
  applyPicker.show = true
}

// picker 提交后触发：刷新模板列表（拿最新 associated_monitors）+ 通知父组件
async function onApplied(_affected: number) {
  await fetchTemplates()
  emit('updated')
}

// --- delete ---
const confirmDelete = reactive<{ show: boolean; tpl: ChannelMonitorTemplate | null }>({
  show: false,
  tpl: null,
})

function handleDelete(tpl: ChannelMonitorTemplate) {
  confirmDelete.tpl = tpl
  confirmDelete.show = true
}

const confirmDeleteMessage = computed(() => {
  const tpl = confirmDelete.tpl
  if (!tpl) return ''
  return t('admin.channelMonitor.template.deleteConfirm', {
    name: tpl.name,
    n: tpl.associated_monitors,
  })
})

async function doDelete() {
  const tpl = confirmDelete.tpl
  confirmDelete.show = false
  if (!tpl) return
  try {
    await adminAPI.channelMonitorTemplate.del(tpl.id)
    appStore.showSuccess(t('admin.channelMonitor.template.deleteSuccess'))
    await fetchTemplates()
    emit('updated')
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

// --- misc ---
function modeColor(mode: BodyOverrideMode): 'warning' | 'info' | 'neutral' {
  if (mode === 'merge') return 'warning'
  if (mode === 'replace') return 'info'
  return 'neutral'
}

function modeLabel(mode: BodyOverrideMode): string {
  return t(`admin.channelMonitor.advanced.bodyMode${mode.charAt(0).toUpperCase()}${mode.slice(1)}`)
}

const apiModeOptions = computed<{ value: APIMode; label: string; hint: string }[]>(() => [
  {
    value: API_MODE_CHAT_COMPLETIONS,
    label: t('admin.channelMonitor.form.apiModeChatCompletions'),
    hint: t('admin.channelMonitor.form.apiModeChatCompletionsHint'),
  },
  {
    value: API_MODE_RESPONSES,
    label: t('admin.channelMonitor.form.apiModeResponses'),
    hint: t('admin.channelMonitor.form.apiModeResponsesHint'),
  },
])

watch(() => form.provider, (provider) => {
  if (provider !== PROVIDER_OPENAI) {
    form.api_mode = API_MODE_CHAT_COMPLETIONS
  }
})

function normalizeAPIMode(mode: APIMode | undefined | null): APIMode {
  return mode === API_MODE_RESPONSES ? API_MODE_RESPONSES : API_MODE_CHAT_COMPLETIONS
}

function apiModeLabel(mode: APIMode): string {
  return normalizeAPIMode(mode) === API_MODE_RESPONSES
    ? t('admin.channelMonitor.form.apiModeResponses')
    : t('admin.channelMonitor.form.apiModeChatCompletions')
}

</script>
