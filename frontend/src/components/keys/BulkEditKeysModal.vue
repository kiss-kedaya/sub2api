<template>
  <UModal :open="open" @update:open="(value: boolean) => { if (!value) emit('close') }">
    <template #content>
      <div class="space-y-4 p-5">
        <header class="space-y-1">
          <h2 class="text-base font-semibold">
            {{ t('keys.bulkEdit.title', { count: selectedIds.length }) }}
          </h2>
          <p class="text-sm text-muted">
            {{ t('keys.bulkEdit.hint') }}
          </p>
        </header>

        <div class="max-h-[60vh] space-y-4 overflow-y-auto pr-1">
          <!-- 分组 -->
          <section>
            <UCheckbox v-model="enabled.group" :label="t('keys.bulkEdit.fields.group')" />
            <USelect
              v-if="enabled.group"
              :model-value="form.groupId === '' ? undefined : form.groupId"
              :items="groupOptions"
              class="mt-2 w-full"
              data-test="bulk-edit-group"
              @update:model-value="(value: unknown) => (form.groupId = typeof value === 'number' ? value : '')"
            />
          </section>

          <!-- 状态 -->
          <section>
            <UCheckbox v-model="enabled.status" :label="t('keys.bulkEdit.fields.status')" />
            <USelect
              v-if="enabled.status"
              v-model="form.status"
              :items="statusOptions"
              class="mt-2 w-full"
              data-test="bulk-edit-status"
            />
          </section>

          <!-- 额度金额 -->
          <section>
            <UCheckbox v-model="enabled.quota" :label="t('keys.bulkEdit.fields.quota')" />
            <UInput
              v-if="enabled.quota"
              v-model="form.quota"
              type="number"
              min="0"
              step="0.01"
              :placeholder="t('keys.bulkEdit.quotaPlaceholder')"
              class="mt-2 w-full"
              data-test="bulk-edit-quota"
            />
          </section>

          <!-- 5 小时 / 日 / 7 天限额 -->
          <section v-for="limit in limitFields" :key="limit.key">
            <UCheckbox v-model="enabled[limit.key]" :label="t(limit.label)" />
            <UInput
              v-if="enabled[limit.key]"
              v-model="form[limit.key]"
              type="number"
              min="0"
              step="0.01"
              :placeholder="t('keys.bulkEdit.limitPlaceholder')"
              class="mt-2 w-full"
              :data-test="`bulk-edit-${limit.key}`"
            />
          </section>

          <!-- 有效期 -->
          <section>
            <UCheckbox v-model="enabled.expiresAt" :label="t('keys.bulkEdit.fields.expiresAt')" />
            <UInput
              v-if="enabled.expiresAt"
              v-model="form.expiresAt"
              type="datetime-local"
              class="mt-2 w-full"
              data-test="bulk-edit-expires"
            />
            <p v-if="enabled.expiresAt" class="mt-1 text-xs text-muted">
              {{ t('keys.bulkEdit.expiresHint') }}
            </p>
          </section>

          <!-- IP 白名单 -->
          <section>
            <UCheckbox v-model="enabled.ipWhitelist" :label="t('keys.bulkEdit.fields.ipWhitelist')" />
            <UTextarea
              v-if="enabled.ipWhitelist"
              v-model="form.ipWhitelist"
              :rows="3"
              :placeholder="t('keys.bulkEdit.ipPlaceholder')"
              class="mt-2 w-full"
              data-test="bulk-edit-ip"
            />
            <IpWhitelistAssist v-if="enabled.ipWhitelist" v-model="form.ipWhitelist" class="mt-2" />
            <p v-if="enabled.ipWhitelist" class="mt-1 text-xs text-muted">
              {{ t('keys.bulkEdit.ipHint') }}
            </p>
          </section>
        </div>

        <p v-if="errorMessage" class="text-sm text-error" data-test="bulk-edit-error">
          {{ errorMessage }}
        </p>

        <footer class="flex items-center justify-end gap-2 pt-1">
          <UButton color="neutral" variant="ghost" :disabled="saving" @click="emit('close')">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            color="primary"
            :loading="saving"
            :disabled="!hasSelection"
            data-test="bulk-edit-apply"
            @click="apply"
          >
            {{ t('keys.bulkEdit.apply', { count: selectedIds.length }) }}
          </UButton>
        </footer>
      </div>
    </template>
  </UModal>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api/keys'
import type { UpdateApiKeyRequest } from '@/types'
import IpWhitelistAssist from '@/components/keys/IpWhitelistAssist.vue'

const props = defineProps<{
  open: boolean
  selectedIds: number[]
  groupOptions: { value: number; label: string }[]
}>()

const emit = defineEmits<{
  close: []
  applied: [result: { updated: number; failed: number }]
}>()

const { t } = useI18n()

const saving = ref(false)
const errorMessage = ref('')

// Each field carries its own opt-in switch: a ticked field is the only way its
// value reaches the payload, which is what makes "unchecked keeps the original"
// true even when the operator typed something and changed their mind.
const enabled = reactive({
  group: false,
  status: false,
  quota: false,
  rateLimit5h: false,
  rateLimit1d: false,
  rateLimit7d: false,
  expiresAt: false,
  ipWhitelist: false
})

const form = reactive({
  groupId: '' as number | '',
  status: 'active' as 'active' | 'inactive',
  quota: '',
  rateLimit5h: '',
  rateLimit1d: '',
  rateLimit7d: '',
  expiresAt: '',
  ipWhitelist: ''
})

const statusOptions = computed(() => [
  { value: 'active', label: t('keys.bulkEdit.statusActive') },
  { value: 'inactive', label: t('keys.bulkEdit.statusInactive') }
])

const limitFields = [
  { field: 'rate_limit_5h' as const, key: 'rateLimit5h' as const, label: 'keys.bulkEdit.fields.rateLimit5h' },
  { field: 'rate_limit_1d' as const, key: 'rateLimit1d' as const, label: 'keys.bulkEdit.fields.rateLimit1d' },
  { field: 'rate_limit_7d' as const, key: 'rateLimit7d' as const, label: 'keys.bulkEdit.fields.rateLimit7d' }
]

const hasSelection = computed(() => Object.values(enabled).some(Boolean))

/** Only ticked fields become part of the request; everything else stays nil. */
function buildPayload(): UpdateApiKeyRequest {
  const payload: UpdateApiKeyRequest = {}

  if (enabled.group && form.groupId !== '') {
    payload.group_id = Number(form.groupId)
  }
  if (enabled.status) {
    payload.status = form.status
  }
  if (enabled.quota && form.quota !== '') {
    payload.quota = Number(form.quota)
  }
  for (const field of limitFields) {
    const raw = form[field.key]
    if (enabled[field.key] && raw !== '') {
      payload[field.field] = Number(raw)
    }
  }
  if (enabled.expiresAt && form.expiresAt !== '') {
    payload.expires_at = new Date(form.expiresAt).toISOString()
  }
  if (enabled.ipWhitelist) {
    const list = form.ipWhitelist
      .split(/[\s,]+/)
      .map((item) => item.trim())
      .filter(Boolean)
    // An explicitly ticked but empty box means "clear the whitelist", which the
    // API expresses as an empty array rather than nil.
    payload.ip_whitelist = list
  }

  return payload
}

async function apply() {
  const payload = buildPayload()
  if (Object.keys(payload).length === 0) {
    errorMessage.value = t('keys.bulkEdit.emptySelection')
    return
  }

  saving.value = true
  errorMessage.value = ''

  let updated = 0
  let failed = 0
  for (const id of props.selectedIds) {
    try {
      await keysAPI.update(id, payload)
      updated += 1
    } catch {
      failed += 1
    }
  }

  saving.value = false
  emit('applied', { updated, failed })
}
</script>
