<template>
  <div class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none">
    <div class="flex items-start justify-between gap-4 border-b border-default px-5 py-4 sm:px-6">
      <div>
        <h2 class="text-base font-semibold text-highlighted">
          {{ t('profile.passkey.title') }}
        </h2>
        <p class="mt-1 text-sm text-muted">
          {{ t('profile.passkey.description') }}
        </p>
      </div>
      <UButton
        v-if="enabled && supported && !showAddForm"
        type="button"
        :disabled="busy"
        data-test="passkey-add"
        @click="showAddForm = true"
      >
        {{ t('profile.passkey.add') }}
      </UButton>
    </div>

    <div class="p-5 sm:p-6">
      <div v-if="!enabled" class="mb-5 text-sm text-muted">
        {{ t('profile.passkey.featureDisabled') }}
      </div>
      <div v-if="enabled && !supported" class="mb-5 text-sm text-amber-600 dark:text-amber-400">
        {{ t('profile.passkey.unsupported') }}
      </div>
      <div>
        <form
          v-if="enabled && supported && showAddForm"
          class="mb-5 flex flex-col gap-3 rounded-lg border border-default p-4"
          @submit.prevent="addPasskey"
        >
          <p
            v-if="totpEnabled"
            class="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
          >
            {{ t('profile.passkey.stepUpHint') }}
          </p>
          <div class="grid gap-3 sm:grid-cols-2">
            <div>
              <label for="passkey-name" class="input-label">{{ t('profile.passkey.name') }}</label>
              <UInput
                id="passkey-name"
                v-model="newName"
                class="w-full"
                maxlength="100"
                :placeholder="t('profile.passkey.namePlaceholder')"
                autofocus
              />
            </div>
            <div>
              <label for="passkey-add-password" class="input-label">{{
                t('profile.currentPassword')
              }}</label>
              <UInput
                id="passkey-add-password"
                v-model="newPassword"
                type="password"
                autocomplete="current-password"
                class="w-full"
                :placeholder="t('profile.passkey.passwordPlaceholder')"
              />
            </div>
          </div>
          <div class="flex justify-end gap-2">
            <UButton
              type="button"
              color="neutral"
              variant="outline"
              :disabled="busy"
              data-test="passkey-cancel-add"
              @click="cancelAdd"
            >
              {{ t('common.cancel') }}
            </UButton>
            <UButton
              type="submit"
              :disabled="busy || newPassword.length === 0"
              :loading="busy"
              data-test="passkey-continue"
            >
              {{ busy ? t('common.processing') : t('profile.passkey.continue') }}
            </UButton>
          </div>
        </form>

        <div v-if="loading" class="flex justify-center py-6">
          <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-500"></div>
        </div>

        <div
          v-else-if="loadError"
          class="flex flex-col items-center gap-3 rounded-lg border border-red-200 px-4 py-6 text-center dark:border-red-900/40"
          data-testid="passkey-load-error"
        >
          <p class="text-sm text-red-600 dark:text-red-300">
            {{ t('profile.passkey.loadFailed') }}
          </p>
          <UButton
            type="button"
            color="neutral"
            variant="outline"
            size="sm"
            :disabled="busy"
            data-test="passkey-retry"
            @click="loadCredentials"
          >
            {{ t('profile.passkey.retry') }}
          </UButton>
        </div>

        <div
          v-else-if="credentials.length === 0"
          class="rounded-lg border border-dashed border-default px-4 py-8 text-center text-sm text-muted"
          data-testid="passkey-empty"
        >
          {{ t('profile.passkey.empty') }}
        </div>

        <div v-else class="divide-y divide-default">
          <div
            v-for="credential in credentials"
            :key="credential.id"
            class="flex items-center justify-between gap-4 py-4 first:pt-0 last:pb-0"
          >
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <Icon name="key" size="md" class="shrink-0 text-primary-500" />
                <p class="truncate font-medium text-highlighted">
                  {{ credential.name }}
                </p>
                <UBadge
                  v-if="credential.backup"
                  color="success"
                  variant="subtle"
                  size="sm"
                  class="rounded-full"
                >
                  {{ t('profile.passkey.synced') }}
                </UBadge>
              </div>
              <p class="mt-1 text-xs tabular-nums text-muted">
                {{ t('profile.passkey.createdAt', { date: formatDate(credential.created_at) }) }}
                <template v-if="credential.last_used_at">
                  · {{ t('profile.passkey.lastUsed', { date: formatDate(credential.last_used_at) }) }}
                </template>
              </p>
            </div>
            <div class="flex shrink-0 gap-2">
              <UButton
                type="button"
                color="neutral"
                variant="outline"
                size="sm"
                :disabled="busy"
                @click="renamePasskey(credential)"
              >
                {{ t('common.edit') }}
              </UButton>
              <UButton
                type="button"
                color="error"
                variant="ghost"
                size="sm"
                :disabled="busy"
                @click="deletePasskey(credential)"
              >
                {{ t('common.delete') }}
              </UButton>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 删除确认：吊销凭据需验证当前密码，防止被窃会话静默移除 Passkey -->
    <BaseDialog
      v-if="clientMounted"
      :show="!!deleteTarget"
      :title="t('profile.passkey.deleteTitle')"
      width="narrow"
      :close-on-click-outside="!busy"
      :close-on-escape="!busy"
      @close="closeDeleteDialog"
    >
      <div class="space-y-4">
        <p class="text-sm text-muted">
          {{ t('profile.passkey.deleteConfirm', { name: deleteTarget?.name ?? '' }) }}
        </p>
        <p
          v-if="totpEnabled"
          class="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t('profile.passkey.stepUpHint') }}
        </p>
        <form id="passkey-delete-form" class="space-y-4" @submit.prevent="confirmDelete">
          <div>
            <label for="passkey-delete-password" class="input-label">{{
              t('profile.currentPassword')
            }}</label>
            <UInput
              id="passkey-delete-password"
              v-model="deletePassword"
              type="password"
              autocomplete="current-password"
              class="w-full"
              :placeholder="t('profile.passkey.passwordPlaceholder')"
              autofocus
            />
          </div>
        </form>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <UButton type="button" color="neutral" variant="outline" :disabled="busy" data-test="passkey-delete-cancel" @click="closeDeleteDialog">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            type="submit"
            form="passkey-delete-form"
            color="error"
            :disabled="busy || deletePassword.length === 0"
            :loading="busy"
            data-test="passkey-delete-confirm"
          >
            {{ busy ? t('common.processing') : t('common.delete') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { passkeyAPI, totpAPI, type PasskeyCredentialSummary } from '@/api'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { Icon } from '@/components/icons'
import { useAppStore } from '@/stores/app'

const props = defineProps<{ enabled: boolean }>()

const { t } = useI18n()
const appStore = useAppStore()
// Keep the server and first hydration render identical; WebAuthn and Teleport dialogs are browser-only.
const clientMounted = ref(false)
const supported = ref(false)
// 账号自身的 TOTP 状态只用于提示「创建/删除前要输动态码」，门禁在服务端。
const totpEnabled = ref(false)
const loading = ref(false)
const loadError = ref(false)
const busy = ref(false)
const showAddForm = ref(false)
const newName = ref('')
const newPassword = ref('')
const deleteTarget = ref<PasskeyCredentialSummary | null>(null)
const deletePassword = ref('')
const credentials = ref<PasskeyCredentialSummary[]>([])

// 后端 WebAuthn 错误码 → 用户能看懂的文案。其余错误回退到 message/通用文案，
// 绝不静默：老板实测过「手机提示保存成功、页面没任何反馈」的那种失败必须可见。
const PASSKEY_ERROR_KEYS: Record<string, string> = {
  PASSKEY_CHALLENGE_EXPIRED: 'profile.passkey.challengeExpired',
  PASSKEY_SESSION_INVALID: 'profile.passkey.challengeExpired',
  PASSKEY_CHALLENGE_MISMATCH: 'profile.passkey.challengeMismatch',
  PASSKEY_ORIGIN_MISMATCH: 'profile.passkey.originMismatch',
  PASSKEY_RP_ID_MISMATCH: 'profile.passkey.rpIdMismatch',
  PASSKEY_VERIFICATION_FAILED: 'profile.passkey.verificationFailed',
  PASSWORD_REQUIRED: 'profile.passkey.passwordRequired',
  PASSWORD_INCORRECT: 'profile.passkey.passwordIncorrect',
  STEP_UP_REQUIRED: 'profile.passkey.stepUpRequired',
  STEP_UP_CANCELLED: 'profile.passkey.stepUpCancelled'
}

interface PasskeyErrorLike {
  code?: string | number
  reason?: string
  message?: string
}

function passkeyErrorReason(error: unknown): string {
  const candidate = error as PasskeyErrorLike | null
  const reasonCandidates = [candidate?.reason, typeof candidate?.code === 'string' ? candidate.code : '']
  for (const reason of reasonCandidates) {
    if (typeof reason === 'string' && Object.prototype.hasOwnProperty.call(PASSKEY_ERROR_KEYS, reason)) {
      return reason
    }
  }
  return ''
}

// apiClient 拦截器把错误规范化为 { code, reason, message }；优先按机器码展示
// 具体原因，其次透出后端 message，最后回退通用文案。
function passkeyErrorMessage(error: unknown, fallback: string): string {
  const reason = passkeyErrorReason(error)
  if (reason) return t(PASSKEY_ERROR_KEYS[reason])
  const message = (error as PasskeyErrorLike | null)?.message
  return typeof message === 'string' && message.length > 0 ? message : fallback
}

async function loadCredentials(): Promise<void> {
  if (!props.enabled) {
    credentials.value = []
    loadError.value = false
    return
  }
  loading.value = true
  loadError.value = false
  try {
    credentials.value = await passkeyAPI.list()
  } catch (error) {
    // 设置变更竞态下后端仍可能返回 PASSKEY_DISABLED，按空列表静默处理
    const reason = (error as PasskeyErrorLike | null)?.reason
    if (reason === 'PASSKEY_DISABLED') {
      credentials.value = []
      return
    }
    // 加载失败与「确实没有 Passkey」必须区分：前者给错误文案 + 重试按钮。
    credentials.value = []
    loadError.value = true
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
    // 立即重新拉列表，避免「提示成功但列表还是空的」。
    await loadCredentials()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'NotAllowedError') {
      // 用户取消 / 设备没完成：OS 可能已经生成了凭据，但服务器没有收到 finish，
      // 必须明确提示「未保存」，不能像以前一样静默。
      appStore.showWarning(t('profile.passkey.cancelled'))
    } else {
      appStore.showError(passkeyErrorMessage(error, t('profile.passkey.addFailed')))
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
  } catch (error) {
    appStore.showError(passkeyErrorMessage(error, t('profile.passkey.renameFailed')))
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
    // 以服务端列表为准，避免多端并发删除/新增后本地列表过期。
    await loadCredentials()
  } catch (error) {
    // 密码错误 / 需要 2FA 等失败保持对话框打开，允许重试
    appStore.showError(passkeyErrorMessage(error, t('profile.passkey.deleteFailed')))
  } finally {
    busy.value = false
  }
}

async function loadTotpStatus(): Promise<void> {
  if (!props.enabled) return
  try {
    const status = await totpAPI.getStatus()
    totpEnabled.value = status?.enabled === true
  } catch {
    // 2FA 状态读不到时只是少一个提示，不影响 Passkey 操作（门禁在服务端）。
    totpEnabled.value = false
  }
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  }).format(new Date(value))
}

onMounted(() => {
  clientMounted.value = true
  supported.value = passkeyAPI.isSupported()
  void loadCredentials()
  void loadTotpStatus()
})

watch(
  () => props.enabled,
  () => {
    void loadCredentials()
    void loadTotpStatus()
  }
)
</script>
