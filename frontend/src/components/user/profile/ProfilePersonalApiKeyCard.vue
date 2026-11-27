<template>
  <div
    class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none"
    data-test="personal-api-key-card"
  >
    <!-- Header -->
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-default px-5 py-4 sm:px-6">
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold text-highlighted">
            {{ t('profile.personalApiKey.title') }}
          </h2>
          <UBadge
            v-if="!loading && status"
            :color="status.exists ? 'success' : 'neutral'"
            variant="soft"
            size="sm"
            data-test="personal-api-key-badge"
          >
            {{ status.exists ? t('profile.personalApiKey.badgeActive') : t('profile.personalApiKey.badgeInactive') }}
          </UBadge>
        </div>
        <p class="mt-1 text-sm text-muted">
          {{ t('profile.personalApiKey.description') }}
        </p>
      </div>
    </div>

    <!-- Body -->
    <div class="space-y-4 p-5 sm:p-6">
      <div
        v-if="loading"
        class="flex items-center justify-center py-6"
        data-test="personal-api-key-loading"
      >
        <div class="h-7 w-7 animate-spin rounded-full border-b-2 border-primary-500"></div>
      </div>

      <div
        v-else-if="loadError"
        role="alert"
        class="space-y-2 text-sm text-red-600 dark:text-red-400"
        data-test="personal-api-key-load-error"
      >
        <p>{{ loadError }}</p>
        <UButton
          type="button"
          color="neutral"
          variant="outline"
          size="sm"
          data-test="personal-api-key-refresh"
          @click="load"
        >
          {{ t('common.refresh') }}
        </UButton>
      </div>

      <!-- Empty state: not created yet -->
      <div
        v-else-if="status && !status.exists"
        class="space-y-4"
        data-test="personal-api-key-empty"
      >
        <div class="rounded-lg bg-elevated/60 p-4 ring ring-inset ring-default">
          <div class="flex items-start gap-3">
            <div class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-500/10 text-primary-600 dark:text-primary-400">
              <UIcon name="i-lucide-key-round" class="size-4.5" aria-hidden="true" />
            </div>
            <div class="min-w-0 flex-1 space-y-2">
              <p class="text-sm font-medium text-highlighted">
                {{ t('profile.personalApiKey.emptyTitle') }}
              </p>
              <p class="text-xs leading-5 text-muted">
                {{ t('profile.personalApiKey.emptyHint') }}
              </p>
              <ul class="space-y-1 text-xs leading-5 text-muted">
                <li class="flex items-start gap-1.5">
                  <UIcon name="i-lucide-shield-check" class="mt-0.5 size-3.5 shrink-0 text-primary-500" aria-hidden="true" />
                  <span>{{ t('profile.personalApiKey.bulletOnce') }}</span>
                </li>
                <li class="flex items-start gap-1.5">
                  <UIcon name="i-lucide-terminal" class="mt-0.5 size-3.5 shrink-0 text-primary-500" aria-hidden="true" />
                  <span>{{ t('profile.personalApiKey.bulletHeaders') }}</span>
                </li>
                <li class="flex items-start gap-1.5">
                  <UIcon name="i-lucide-lock" class="mt-0.5 size-3.5 shrink-0 text-primary-500" aria-hidden="true" />
                  <span>{{ t('profile.personalApiKey.bulletScope') }}</span>
                </li>
                <li class="flex items-start gap-1.5">
                  <UIcon name="i-lucide-gauge" class="mt-0.5 size-3.5 shrink-0 text-primary-500" aria-hidden="true" />
                  <span>
                    {{
                      t('profile.personalApiKey.bulletRateLimit', {
                        read: status.rate_limit_rpm,
                        write: status.write_rate_limit_rpm,
                      })
                    }}
                  </span>
                </li>
              </ul>
            </div>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <UButton
            type="button"
            color="primary"
            icon="i-lucide-plus"
            :loading="submitting"
            :disabled="submitting"
            data-test="personal-api-key-create"
            @click="startCreateOrRegenerate(false)"
          >
            {{ t('profile.personalApiKey.createAction') }}
          </UButton>
        </div>
      </div>

      <!-- Active state: key exists (masked prefix + metadata) -->
      <div
        v-else-if="status && status.exists"
        class="space-y-4"
        data-test="personal-api-key-active"
      >
        <div class="grid gap-3 rounded-lg bg-elevated/60 p-4 ring ring-inset ring-default sm:grid-cols-2">
          <div class="space-y-1">
            <div class="text-xs text-muted">{{ t('profile.personalApiKey.keyPrefixLabel') }}</div>
            <div class="flex items-center gap-2">
              <code
                class="rounded bg-default px-2 py-0.5 font-mono text-xs font-medium text-highlighted ring ring-inset ring-default"
                data-test="personal-api-key-prefix"
              >{{ status.key_prefix }}</code>
            </div>
          </div>

          <div class="space-y-1">
            <div class="text-xs text-muted">{{ t('profile.personalApiKey.rateLimitLabel') }}</div>
            <div class="text-xs font-medium text-highlighted" data-test="personal-api-key-rate-limit">
              {{
                t('profile.personalApiKey.rateLimitValue', {
                  read: status.rate_limit_rpm,
                  write: status.write_rate_limit_rpm,
                })
              }}
            </div>
          </div>

          <div class="space-y-1">
            <div class="text-xs text-muted">{{ t('profile.personalApiKey.createdAtLabel') }}</div>
            <div class="text-xs text-highlighted" data-test="personal-api-key-created-at">
              {{ formatTimestamp(status.created_at) }}
            </div>
          </div>

          <div class="space-y-1">
            <div class="text-xs text-muted">{{ t('profile.personalApiKey.lastUsedLabel') }}</div>
            <div class="text-xs text-highlighted" data-test="personal-api-key-last-used">
              {{ lastUsedDisplay }}
            </div>
          </div>
        </div>

        <p class="flex items-start gap-1.5 text-xs leading-5 text-amber-600 dark:text-amber-400">
          <UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span>{{ t('profile.personalApiKey.securityNote') }}</span>
        </p>

        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex flex-wrap items-center gap-2">
            <UButton
              type="button"
              color="neutral"
              variant="outline"
              size="sm"
              icon="i-lucide-refresh-cw"
              :loading="submitting"
              :disabled="submitting || deleting"
              data-test="personal-api-key-regenerate"
              @click="startCreateOrRegenerate(true)"
            >
              {{ t('profile.personalApiKey.regenerateAction') }}
            </UButton>
            <UButton
              type="button"
              color="error"
              variant="soft"
              size="sm"
              icon="i-lucide-trash-2"
              :loading="deleting"
              :disabled="submitting || deleting"
              data-test="personal-api-key-delete"
              @click="showDeleteConfirm = true"
            >
              {{ t('profile.personalApiKey.deleteAction') }}
            </UButton>
          </div>

          <UButton
            type="button"
            color="neutral"
            variant="ghost"
            size="sm"
            :icon="showExamples ? 'i-lucide-chevron-up' : 'i-lucide-code-2'"
            data-test="personal-api-key-toggle-examples"
            @click="showExamples = !showExamples"
          >
            {{
              showExamples
                ? t('profile.personalApiKey.examplesToggleHide')
                : t('profile.personalApiKey.examplesToggleShow')
            }}
          </UButton>
        </div>

        <!-- Collapsible integration examples -->
        <div
          v-if="showExamples"
          class="space-y-3 rounded-lg border border-default bg-elevated/40 p-4"
          data-test="personal-api-key-examples"
        >
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <div class="text-xs font-medium text-highlighted">
                {{ t('profile.personalApiKey.examplesTitle') }}
              </div>
              <p class="mt-0.5 text-xs text-muted">
                {{ t('profile.personalApiKey.examplesHint') }}
              </p>
            </div>
            <div class="inline-flex rounded-md bg-default p-0.5 ring ring-inset ring-default">
              <button
                type="button"
                class="rounded px-2.5 py-1 text-xs font-medium transition-colors"
                :class="
                  headerMode === 'bearer'
                    ? 'bg-primary-500/15 text-primary-600 dark:text-primary-400'
                    : 'text-muted hover:text-highlighted'
                "
                data-test="personal-api-key-mode-bearer"
                @click="headerMode = 'bearer'"
              >
                {{ t('profile.personalApiKey.headerModeBearer') }}
              </button>
              <button
                type="button"
                class="rounded px-2.5 py-1 text-xs font-medium transition-colors"
                :class="
                  headerMode === 'apikey'
                    ? 'bg-primary-500/15 text-primary-600 dark:text-primary-400'
                    : 'text-muted hover:text-highlighted'
                "
                data-test="personal-api-key-mode-apikey"
                @click="headerMode = 'apikey'"
              >
                {{ t('profile.personalApiKey.headerModeApiKey') }}
              </button>
            </div>
          </div>

          <div class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-medium text-muted">
                {{ t('profile.personalApiKey.exampleProfileLabel') }}
              </span>
              <UButton
                type="button"
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-copy"
                @click="copySnippet(profileCurlExample('uak_YOUR_PERSONAL_KEY'))"
              >
                {{ t('profile.personalApiKey.copyExample') }}
              </UButton>
            </div>
            <pre class="overflow-x-auto rounded-md bg-default p-3 font-mono text-xs leading-5 text-highlighted ring ring-inset ring-default">{{ profileCurlExample('uak_YOUR_PERSONAL_KEY') }}</pre>
          </div>

          <div class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-medium text-muted">
                {{ t('profile.personalApiKey.exampleCreateKeyLabel') }}
              </span>
              <UButton
                type="button"
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-copy"
                @click="copySnippet(createKeyCurlExample('uak_YOUR_PERSONAL_KEY'))"
              >
                {{ t('profile.personalApiKey.copyExample') }}
              </UButton>
            </div>
            <pre class="overflow-x-auto rounded-md bg-default p-3 font-mono text-xs leading-5 text-highlighted ring ring-inset ring-default">{{ createKeyCurlExample('uak_YOUR_PERSONAL_KEY') }}</pre>
          </div>
        </div>
      </div>
    </div>

    <!-- Step 1: Identity verification / Regenerate confirmation dialog -->
    <BaseDialog
      :show="showVerifyDialog"
      :title="verifyDialogTitle"
      @close="closeVerifyDialog"
    >
      <div class="space-y-3" data-test="personal-api-key-verify-dialog">
        <p class="text-sm text-muted">
          {{ verifyDialogHint }}
        </p>
        <KeyFactorVerification
          v-if="requiresFactorVerification && factors"
          ref="verificationRef"
          :factors="factors"
          mode="create"
          hide-description
          :disabled="submitting"
        />
        <p
          v-if="verifyError"
          role="alert"
          class="text-xs text-red-600 dark:text-red-400"
          data-test="personal-api-key-verify-error"
        >
          {{ verifyError }}
        </p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            type="button"
            color="neutral"
            variant="outline"
            :disabled="submitting"
            data-test="personal-api-key-verify-cancel"
            @click="closeVerifyDialog"
          >
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            type="button"
            :color="pendingRegenerate ? 'warning' : 'primary'"
            :loading="submitting"
            :disabled="submitting"
            data-test="personal-api-key-verify-confirm"
            @click="confirmCreateOrRegenerate"
          >
            {{ verifyConfirmLabel }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Step 2: One-time plaintext secret reveal dialog -->
    <BaseDialog
      :show="showRevealDialog"
      :title="t('profile.personalApiKey.revealTitle')"
      width="normal"
      :close-on-click-outside="canCloseReveal"
      :close-on-escape="canCloseReveal"
      :close-button-disabled="!canCloseReveal"
      @close="closeRevealDialog"
    >
      <template #description>
        <span class="inline-flex items-center gap-1.5 text-amber-600 dark:text-amber-400" data-test="personal-api-key-reveal-warning">
          <UIcon name="i-lucide-triangle-alert" class="size-3.5 shrink-0" aria-hidden="true" />
          {{ t('profile.personalApiKey.revealWarning') }}
        </span>
      </template>

      <div class="space-y-4" data-test="personal-api-key-reveal-dialog">
        <div class="flex items-start gap-2 rounded-lg bg-elevated/70 p-3 ring ring-inset ring-default">
          <code
            class="min-w-0 flex-1 break-all font-mono text-xs leading-5 text-highlighted"
            data-test="personal-api-key-reveal-value"
          >{{ revealedSecret }}</code>
          <UButton
            type="button"
            color="primary"
            variant="soft"
            size="xs"
            :icon="copiedSecret ? 'i-lucide-check' : 'i-lucide-copy'"
            :label="copiedSecret ? t('profile.personalApiKey.copiedKey') : t('profile.personalApiKey.copyKey')"
            class="shrink-0"
            data-test="personal-api-key-reveal-copy"
            @click="copyRevealedSecret"
          />
        </div>

        <!-- Ready-to-paste server command -->
        <div class="space-y-2">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="text-xs font-medium text-muted">
              {{ t('profile.personalApiKey.exampleCreateKeyLabel') }}
            </span>
            <div class="flex items-center gap-2">
              <div class="inline-flex rounded-md bg-default p-0.5 ring ring-inset ring-default">
                <button
                  type="button"
                  class="rounded px-2 py-0.5 text-[11px] font-medium transition-colors"
                  :class="
                    headerMode === 'bearer'
                      ? 'bg-primary-500/15 text-primary-600 dark:text-primary-400'
                      : 'text-muted hover:text-highlighted'
                  "
                  @click="headerMode = 'bearer'"
                >
                  Bearer
                </button>
                <button
                  type="button"
                  class="rounded px-2 py-0.5 text-[11px] font-medium transition-colors"
                  :class="
                    headerMode === 'apikey'
                      ? 'bg-primary-500/15 text-primary-600 dark:text-primary-400'
                      : 'text-muted hover:text-highlighted'
                  "
                  @click="headerMode = 'apikey'"
                >
                  X-API-Key
                </button>
              </div>
              <UButton
                type="button"
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-copy"
                @click="copySnippet(createKeyCurlExample(revealedSecret))"
              >
                {{ t('profile.personalApiKey.copyExample') }}
              </UButton>
            </div>
          </div>
          <pre class="overflow-x-auto whitespace-pre-wrap break-all rounded-md bg-elevated/60 p-3 font-mono text-xs leading-5 text-highlighted ring ring-inset ring-default">{{ createKeyCurlExample(revealedSecret) }}</pre>
        </div>

        <p v-if="!canCloseReveal" class="sr-only" role="status">
          {{ t('profile.personalApiKey.revealWaitHint') }}
        </p>
      </div>

      <template #footer>
        <div class="flex justify-end">
          <UButton
            type="button"
            color="primary"
            icon="i-lucide-check"
            :disabled="!canCloseReveal"
            data-test="personal-api-key-reveal-done"
            @click="closeRevealDialog"
          >
            {{ t('profile.personalApiKey.revealDone') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Step 3: Delete confirmation dialog -->
    <BaseDialog
      :show="showDeleteConfirm"
      :title="t('profile.personalApiKey.deleteTitle')"
      @close="showDeleteConfirm = false"
    >
      <p class="text-sm text-muted" data-test="personal-api-key-delete-dialog">
        {{ t('profile.personalApiKey.deleteConfirm') }}
      </p>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            type="button"
            color="neutral"
            variant="outline"
            :disabled="deleting"
            data-test="personal-api-key-delete-cancel"
            @click="showDeleteConfirm = false"
          >
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            type="button"
            color="error"
            :loading="deleting"
            :disabled="deleting"
            data-test="personal-api-key-delete-confirm"
            @click="confirmDelete"
          >
            {{ t('profile.personalApiKey.confirmDeleteAction') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI, userAPI } from '@/api'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KeyFactorVerification from '@/components/keys/KeyFactorVerification.vue'
import { useAppStore } from '@/stores/app'
import type {
  ApiKeyVerificationFactors,
  ApiKeyVerificationPayload,
  UserPersonalApiKeyStatus,
} from '@/types'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const loadError = ref('')
const status = ref<UserPersonalApiKeyStatus | null>(null)
const factors = ref<ApiKeyVerificationFactors | null>(null)

const submitting = ref(false)
const deleting = ref(false)
const showVerifyDialog = ref(false)
const pendingRegenerate = ref(false)
const verifyError = ref('')
const verificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)

const showRevealDialog = ref(false)
const revealedSecret = ref('')
const copiedSecret = ref(false)
const canCloseReveal = ref(false)
let revealUnlockTimer: ReturnType<typeof setTimeout> | null = null

const showDeleteConfirm = ref(false)
const showExamples = ref(false)
const headerMode = ref<'bearer' | 'apikey'>('bearer')
let disposed = false

const requiresFactorVerification = computed(() =>
  Boolean(factors.value && Array.isArray(factors.value.available_methods) && factors.value.available_methods.length > 0),
)

const apiOrigin = computed(() => {
  if (typeof window !== 'undefined' && window.location?.origin) {
    return window.location.origin
  }
  return 'https://your-site.example.com'
})

const lastUsedDisplay = computed(() => {
  const s = status.value
  if (!s?.last_used_at) {
    return t('profile.personalApiKey.lastUsedNever')
  }
  const timeText = formatTimestamp(s.last_used_at)
  if (s.last_used_ip) {
    return t('profile.personalApiKey.lastUsedWithIp', { time: timeText, ip: s.last_used_ip })
  }
  return timeText
})

const verifyDialogTitle = computed(() => {
  if (!requiresFactorVerification.value) {
    return t('profile.personalApiKey.confirmRegenerateTitle')
  }
  return pendingRegenerate.value
    ? t('profile.personalApiKey.verifyRegenerateTitle')
    : t('profile.personalApiKey.verifyCreateTitle')
})

const verifyDialogHint = computed(() => {
  if (!requiresFactorVerification.value) {
    return t('profile.personalApiKey.confirmRegenerateHint')
  }
  return pendingRegenerate.value
    ? t('profile.personalApiKey.verifyRegenerateHint')
    : t('profile.personalApiKey.verifyCreateHint')
})

const verifyConfirmLabel = computed(() => {
  if (!requiresFactorVerification.value) {
    return t('profile.personalApiKey.confirmRegeneratePlainAction')
  }
  return pendingRegenerate.value
    ? t('profile.personalApiKey.confirmRegenerateAction')
    : t('profile.personalApiKey.confirmCreateAction')
})

function formatTimestamp(iso?: string | null): string {
  if (!iso) return '—'
  return formatDateTime(iso)
}

function authHeaderFlag(token: string): string {
  if (headerMode.value === 'apikey') {
    return `-H "X-API-Key: ${token}"`
  }
  return `-H "Authorization: Bearer ${token}"`
}

function profileCurlExample(token: string): string {
  return `curl -s ${apiOrigin.value}/api/v1/auth/me \\\n  ${authHeaderFlag(token)}`
}

function createKeyCurlExample(token: string): string {
  return `curl -s -X POST ${apiOrigin.value}/api/v1/keys \\\n  ${authHeaderFlag(token)} \\\n  -H "Content-Type: application/json" \\\n  -d '{"name":"channel-auto-key","group_id":1}'`
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const [keyStatus, verifyFactors] = await Promise.all([
      userAPI.getPersonalApiKey(),
      keysAPI.getVerificationFactors().catch(() => null),
    ])
    if (disposed) return
    status.value = keyStatus
    factors.value = verifyFactors
  } catch {
    if (disposed) return
    status.value = null
    loadError.value = t('profile.personalApiKey.loadFailed')
  } finally {
    if (!disposed) {
      loading.value = false
    }
  }
}

onMounted(load)
onUnmounted(() => {
  disposed = true
  if (revealUnlockTimer) {
    clearTimeout(revealUnlockTimer)
    revealUnlockTimer = null
  }
  revealedSecret.value = ''
})

async function startCreateOrRegenerate(isRegenerate: boolean) {
  if (submitting.value) return
  pendingRegenerate.value = isRegenerate
  verifyError.value = ''

  if (!factors.value) {
    try {
      factors.value = await keysAPI.getVerificationFactors()
    } catch {
      factors.value = null
      appStore.showError(t('profile.personalApiKey.loadFailed'))
      return
    }
  }

  // 有可用验证因子时，创建与重置都必须经过身份校验；
  // 无任何验证因子（纯第三方登录）且为首次创建时直接生成，重置时仍弹二次确认避免误操作。
  if (requiresFactorVerification.value || isRegenerate) {
    showVerifyDialog.value = true
    return
  }

  await submitCreateOrRegenerate()
}

function closeVerifyDialog() {
  if (submitting.value) return
  showVerifyDialog.value = false
  verifyError.value = ''
  verificationRef.value?.reset()
}

async function confirmCreateOrRegenerate() {
  if (submitting.value) return
  let payload: ApiKeyVerificationPayload | undefined
  if (requiresFactorVerification.value) {
    const built = verificationRef.value?.buildVerification() ?? null
    if (!built) return
    payload = built
  }
  await submitCreateOrRegenerate(payload)
}

function verificationErrorText(reason: string): string {
  switch (reason) {
    case 'VERIFY_PASSWORD_INVALID':
      return t('keys.factor.passwordInvalid')
    case 'VERIFY_PASSWORD_RATE_LIMITED':
      return t('keys.factor.passwordRateLimited')
    case 'VERIFY_TOTP_INVALID':
      return t('keys.factor.totpInvalid')
    case 'VERIFY_PASSKEY_FAILED':
      return t('keys.factor.passkeyFailed')
    case 'VERIFY_FACTOR_UNAVAILABLE':
      return t('keys.factor.factorUnavailable')
    case 'VERIFY_REQUIRED':
      return t('keys.factor.requiredAll')
    default:
      return ''
  }
}

function openRevealDialog(secret: string) {
  revealedSecret.value = secret
  copiedSecret.value = false
  canCloseReveal.value = false
  showRevealDialog.value = true
  if (revealUnlockTimer) {
    clearTimeout(revealUnlockTimer)
  }
  revealUnlockTimer = setTimeout(() => {
    canCloseReveal.value = true
    revealUnlockTimer = null
  }, 800)
}

function closeRevealDialog() {
  if (!canCloseReveal.value) return
  if (revealUnlockTimer) {
    clearTimeout(revealUnlockTimer)
    revealUnlockTimer = null
  }
  showRevealDialog.value = false
  revealedSecret.value = ''
  copiedSecret.value = false
}

async function submitCreateOrRegenerate(verification?: ApiKeyVerificationPayload) {
  submitting.value = true
  verifyError.value = ''
  try {
    const res = await userAPI.createOrRegeneratePersonalApiKey(verification)
    if (disposed) return
    status.value = res.status
    showVerifyDialog.value = false
    verificationRef.value?.reset()
    openRevealDialog(res.key)
    appStore.showSuccess(
      t(res.regenerated ? 'profile.personalApiKey.regeneratedSuccess' : 'profile.personalApiKey.createdSuccess'),
    )
  } catch (err: unknown) {
    const reason = extractApiErrorCode(err) ?? ''
    const mapped = verificationErrorText(reason)
    const message = mapped || extractApiErrorMessage(err) || t('profile.personalApiKey.createFailed')
    if (showVerifyDialog.value) {
      verifyError.value = message
      verificationRef.value?.reset()
    } else if (!disposed) {
      appStore.showError(message)
    }
  } finally {
    if (!disposed) {
      submitting.value = false
    }
  }
}

async function confirmDelete() {
  if (deleting.value) return
  deleting.value = true
  try {
    await userAPI.deletePersonalApiKey()
    if (disposed) return
    showDeleteConfirm.value = false
    showExamples.value = false
    status.value = {
      exists: false,
      rate_limit_rpm: status.value?.rate_limit_rpm ?? 120,
      write_rate_limit_rpm: status.value?.write_rate_limit_rpm ?? 30,
    }
    appStore.showSuccess(t('profile.personalApiKey.deletedSuccess'))
  } catch (err: unknown) {
    if (!disposed) {
      appStore.showError(extractApiErrorMessage(err) || t('profile.personalApiKey.deleteFailed'))
    }
  } finally {
    if (!disposed) {
      deleting.value = false
    }
  }
}

async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // fall through to execCommand fallback
    }
  }
  if (typeof document !== 'undefined') {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    textarea.style.pointerEvents = 'none'
    document.body.appendChild(textarea)
    try {
      textarea.select()
      return document.execCommand('copy')
    } catch {
      return false
    } finally {
      document.body.removeChild(textarea)
    }
  }
  return false
}

async function copyRevealedSecret() {
  if (!revealedSecret.value) return
  const ok = await copyText(revealedSecret.value)
  if (ok) {
    copiedSecret.value = true
    canCloseReveal.value = true
    appStore.showSuccess(t('profile.personalApiKey.copiedKey'))
  } else {
    appStore.showError(t('profile.personalApiKey.copyFailed'))
  }
}

async function copySnippet(snippet: string) {
  const ok = await copyText(snippet)
  if (ok) {
    appStore.showSuccess(t('profile.personalApiKey.copiedExample'))
  } else {
    appStore.showError(t('profile.personalApiKey.copyFailed'))
  }
}
</script>
