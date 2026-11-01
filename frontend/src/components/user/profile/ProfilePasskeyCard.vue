<template>
  <!-- Security section of the dashboard template: title card with the add action, then a
       subtle card holding the add form and the hairline-divided passkey list. -->
  <section>
    <UPageCard
      :title="t('profile.passkey.title')"
      :description="t('profile.passkey.description')"
      variant="naked"
      :orientation="enabled && supported && !showAddForm ? 'horizontal' : 'vertical'"
      class="mb-4"
    >
      <UButton
        v-if="enabled && supported && !showAddForm"
        class="w-fit lg:ms-auto"
        :disabled="busy"
        @click="showAddForm = true"
      >
        {{ t('profile.passkey.add') }}
      </UButton>
    </UPageCard>

    <UPageCard variant="subtle">
      <UAlert
        v-if="!enabled"
        color="neutral"
        variant="subtle"
        icon="i-lucide-info"
        :description="t('profile.passkey.featureDisabled')"
      />
      <UAlert
        v-if="enabled && !supported"
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :description="t('profile.passkey.unsupported')"
      />

      <form
        v-if="enabled && supported && showAddForm"
        class="flex flex-col gap-4"
        @submit.prevent="addPasskey"
      >
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('profile.passkey.name')">
            <UInput
              id="passkey-name"
              v-model="newName"
              maxlength="100"
              class="w-full"
              :placeholder="t('profile.passkey.namePlaceholder')"
              autofocus
            />
          </UFormField>
          <UFormField :label="t('profile.currentPassword')" required>
            <UInput
              id="passkey-add-password"
              v-model="newPassword"
              type="password"
              autocomplete="current-password"
              class="w-full"
              :placeholder="t('profile.passkey.passwordPlaceholder')"
            />
          </UFormField>
        </div>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :disabled="busy" @click="cancelAdd">
            {{ t('common.cancel') }}
          </UButton>
          <UButton type="submit" :disabled="busy || newPassword.length === 0">
            {{ busy ? t('common.processing') : t('profile.passkey.continue') }}
          </UButton>
        </div>
        <USeparator />
      </form>

      <div v-if="loading" class="flex justify-center py-4">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>

      <p v-else-if="credentials.length === 0 && enabled" class="py-2 text-center text-sm text-muted">
        {{ t('profile.passkey.empty') }}
      </p>

      <div v-else-if="credentials.length > 0" class="divide-y divide-default">
        <div
          v-for="credential in credentials"
          :key="credential.id"
          class="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0"
        >
          <div class="flex min-w-0 items-start gap-3">
            <UIcon name="i-lucide-key-round" class="mt-0.5 size-5 shrink-0 text-dimmed" />
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <p class="truncate text-sm font-medium text-highlighted">
                  {{ credential.name }}
                </p>
                <UBadge v-if="credential.backup" color="success" variant="subtle" size="sm">
                  {{ t('profile.passkey.synced') }}
                </UBadge>
              </div>
              <p class="mt-0.5 text-xs text-muted">
                {{ t('profile.passkey.createdAt', { date: formatDate(credential.created_at) }) }}
                <template v-if="credential.last_used_at">
                  · {{ t('profile.passkey.lastUsed', { date: formatDate(credential.last_used_at) }) }}
                </template>
              </p>
            </div>
          </div>
          <div class="flex shrink-0 gap-1">
            <UButton
              color="neutral"
              variant="ghost"
              :disabled="busy"
              @click="renamePasskey(credential)"
            >
              {{ t('common.edit') }}
            </UButton>
            <UButton
              color="error"
              variant="ghost"
              :disabled="busy"
              @click="deletePasskey(credential)"
            >
              {{ t('common.delete') }}
            </UButton>
          </div>
        </div>
      </div>
    </UPageCard>

    <!-- 删除确认：吊销凭据需验证当前密码，防止被窃会话静默移除 Passkey -->
    <BaseDialog
      :show="!!deleteTarget"
      :title="t('profile.passkey.deleteTitle')"
      width="narrow"
      close-on-click-outside
      @close="closeDeleteDialog"
    >
      <form id="passkey-delete-form" class="space-y-4" @submit.prevent="confirmDelete">
        <p class="text-sm text-muted">
          {{ t('profile.passkey.deleteConfirm', { name: deleteTarget?.name }) }}
        </p>
        <UFormField :label="t('profile.currentPassword')" required>
          <UInput
            id="passkey-delete-password"
            v-model="deletePassword"
            type="password"
            autocomplete="current-password"
            class="w-full"
            :placeholder="t('profile.passkey.passwordPlaceholder')"
            autofocus
          />
        </UFormField>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :disabled="busy" @click="closeDeleteDialog">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            type="submit"
            form="passkey-delete-form"
            color="error"
            :disabled="busy || deletePassword.length === 0"
          >
            {{ busy ? t('common.processing') : t('common.delete') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { passkeyAPI, type PasskeyCredentialSummary } from '@/api'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ enabled: boolean }>()

const { t } = useI18n()
const appStore = useAppStore()
const supported = passkeyAPI.isSupported()
const loading = ref(false)
const busy = ref(false)
const showAddForm = ref(false)
const newName = ref('')
const newPassword = ref('')
const deleteTarget = ref<PasskeyCredentialSummary | null>(null)
const deletePassword = ref('')
const credentials = ref<PasskeyCredentialSummary[]>([])

// apiClient 拦截器把错误规范化为 { code, reason, message }；
// 透出后端消息（如密码错误），否则回退到通用文案。
function extractErrorMessage(error: unknown, fallback: string): string {
  const message = (error as { message?: string }).message
  return typeof message === 'string' && message.length > 0 ? message : fallback
}

async function loadCredentials(): Promise<void> {
  if (!props.enabled) {
    credentials.value = []
    return
  }
  loading.value = true
  try {
    credentials.value = await passkeyAPI.list()
  } catch (error) {
    // 字符串错误码在 reason 字段（code 是数字状态码）；
    // 设置变更竞态下后端仍可能返回 PASSKEY_DISABLED，静默处理
    const reason = (error as { reason?: string }).reason
    if (reason !== 'PASSKEY_DISABLED') {
      appStore.showError(t('profile.passkey.loadFailed'))
    }
  } finally {
    loading.value = false
  }
}

async function addPasskey(): Promise<void> {
  if (newPassword.value.length === 0) return
  busy.value = true
  try {
    await passkeyAPI.register(newName.value.trim(), newPassword.value)
    appStore.showSuccess(t('profile.passkey.added'))
    cancelAdd()
    await loadCredentials()
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'NotAllowedError')) {
      appStore.showError(extractErrorMessage(error, t('profile.passkey.addFailed')))
    }
  } finally {
    busy.value = false
  }
}

function cancelAdd(): void {
  showAddForm.value = false
  newName.value = ''
  newPassword.value = ''
}

async function renamePasskey(credential: PasskeyCredentialSummary): Promise<void> {
  const name = window.prompt(t('profile.passkey.renamePrompt'), credential.name)?.trim()
  if (!name || name === credential.name) return
  busy.value = true
  try {
    await passkeyAPI.rename(credential.id, name)
    credential.name = name
    appStore.showSuccess(t('profile.passkey.renamed'))
  } catch {
    appStore.showError(t('profile.passkey.renameFailed'))
  } finally {
    busy.value = false
  }
}

function deletePasskey(credential: PasskeyCredentialSummary): void {
  deleteTarget.value = credential
  deletePassword.value = ''
}

function closeDeleteDialog(): void {
  deleteTarget.value = null
  deletePassword.value = ''
}

async function confirmDelete(): Promise<void> {
  const credential = deleteTarget.value
  if (!credential || deletePassword.value.length === 0) return
  busy.value = true
  try {
    await passkeyAPI.remove(credential.id, deletePassword.value)
    credentials.value = credentials.value.filter((item) => item.id !== credential.id)
    appStore.showSuccess(t('profile.passkey.deleted'))
    closeDeleteDialog()
  } catch (error) {
    // 密码错误等失败保持对话框打开，允许重试
    appStore.showError(extractErrorMessage(error, t('profile.passkey.deleteFailed')))
  } finally {
    busy.value = false
  }
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  }).format(new Date(value))
}

watch(
  () => props.enabled,
  () => {
    void loadCredentials()
  },
  { immediate: true }
)
</script>
