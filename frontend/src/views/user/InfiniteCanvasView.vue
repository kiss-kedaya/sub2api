<template>
  <AppLayout>
    <div class="canvas-page-layout">
      <div class="card flex-1 min-h-0 overflow-hidden">
        <div v-if="loading" class="flex h-full flex-col items-center justify-center gap-3 py-12">
          <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('infiniteCanvas.preparing') }}</p>
        </div>

        <!--
          创建 / 轮换都要用户明确点击确认：
          - 轮换会让旧密钥立即失效，所以进入页面、刷新页面都不会自动轮换（只有点按钮才发请求）；
          - 两者都会下发新明文，账号有可用因子时服务端要求三选一身份校验，这里接入同一个校验组件。
        -->
        <div
          v-else-if="pendingAction"
          class="flex h-full items-center justify-center overflow-auto p-6"
          data-testid="canvas-confirm"
        >
          <div class="w-full max-w-md">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
              <Icon
                :name="pendingAction === 'rotate' ? 'exclamationTriangle' : 'plus'"
                size="lg"
                :class="pendingAction === 'rotate' ? 'text-amber-500' : 'text-primary-500'"
              />
            </div>

            <template v-if="pendingAction === 'rotate'">
              <h3 class="text-center text-lg font-semibold text-gray-900 dark:text-white" data-testid="canvas-rotate-title">
                {{ t('infiniteCanvas.rotateTitle') }}
              </h3>
              <p class="mt-2 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('infiniteCanvas.rotateBody') }}</p>
              <p v-if="rotationTarget" class="mt-3 break-all text-center text-sm text-gray-700 dark:text-dark-200">
                {{ t('infiniteCanvas.rotateTarget', { key: rotationTarget.keyMasked, count: rotationTarget.groupCount }) }}
              </p>
            </template>
            <h3 v-else class="text-center text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('infiniteCanvas.notConfigured') }}
            </h3>

            <div
              v-if="factorsLoading"
              class="mt-5 text-center text-sm text-gray-500 dark:text-dark-400"
              data-testid="canvas-factors-loading"
            >
              {{ t('infiniteCanvas.verifyLoading') }}
            </div>
            <div v-else-if="needsVerification" class="mt-5 text-left" data-testid="canvas-verification">
              <p class="mb-2 text-xs text-gray-500 dark:text-dark-400">{{ t('infiniteCanvas.verifyHint') }}</p>
              <KeyFactorVerification ref="verificationRef" :factors="factors" mode="create" />
            </div>

            <p
              v-if="verifyError"
              class="mt-3 text-center text-sm text-red-600 dark:text-red-400"
              role="alert"
              data-testid="canvas-verify-error"
            >
              {{ verifyError }}
            </p>

            <div class="mt-6 flex flex-wrap items-center justify-center gap-3">
              <RouterLink v-if="pendingAction === 'rotate'" to="/dashboard" class="btn btn-secondary" data-testid="canvas-rotate-cancel">
                {{ t('infiniteCanvas.rotateCancel') }}
              </RouterLink>
              <button
                type="button"
                class="btn btn-primary"
                :disabled="factorsLoading"
                data-testid="canvas-confirm-submit"
                @click="submitPending"
              >
                <Icon v-if="pendingAction === 'create'" name="plus" size="sm" class="mr-2" />
                {{ pendingAction === 'rotate' ? t('infiniteCanvas.rotateConfirm') : t('infiniteCanvas.createAndEnter') }}
              </button>
            </div>
          </div>
        </div>

        <div v-else-if="errorMessage" class="flex h-full items-center justify-center p-10 text-center">
          <div class="max-w-md">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
              <Icon name="exclamationTriangle" size="lg" class="text-amber-500" />
            </div>
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('infiniteCanvas.errorTitle') }}</h3>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ errorMessage }}</p>
            <RouterLink v-if="errorCode === 'key-unavailable'" to="/keys" class="btn btn-primary mt-6">
              {{ t('infiniteCanvas.manageKeys') }}
            </RouterLink>
            <button v-else type="button" class="btn btn-primary mt-6" @click="prepareSession()">
              {{ t('infiniteCanvas.retry') }}
            </button>
          </div>
        </div>

        <div v-else class="canvas-embed-shell">
          <iframe
            v-if="embedUrl"
            :src="embedUrl"
            class="canvas-embed-frame"
            allow="clipboard-read; clipboard-write; fullscreen"
            :title="t('infiniteCanvas.title')"
          ></iframe>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import KeyFactorVerification from '@/components/keys/KeyFactorVerification.vue'
import { keysAPI } from '@/api'
import { useAppStore } from '@/stores'
import type { ApiKeyVerificationFactors, ApiKeyVerificationPayload } from '@/types'
import { detectTheme } from '@/utils/embedded-url'
import { buildInfiniteCanvasImportUrl, resolveHttpBaseUrl, resolveInfiniteCanvasBaseUrl } from '@/utils/infiniteCanvas'
import {
  InfiniteCanvasSetupError,
  ensureInfiniteCanvasApiKey,
  type CanvasRotationTarget,
} from '@/utils/infiniteCanvasSession'

type PendingAction = 'create' | 'rotate'

const { t, locale } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const errorCode = ref<InfiniteCanvasSetupError['code'] | null>(null)
const pendingAction = ref<PendingAction | null>(null)
const rotationTarget = ref<CanvasRotationTarget | null>(null)
const factors = ref<ApiKeyVerificationFactors | null>(null)
const factorsLoading = ref(false)
const verifyError = ref('')
const verificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
const embedUrl = ref('')
let disposed = false

// 账号没有任何可用因子时服务端显式放行，这里不展示校验步骤。
const needsVerification = computed(() => (factors.value?.available_methods.length ?? 0) > 0)

const errorMessage = computed(() => {
  if (errorCode.value === 'no-groups') return t('infiniteCanvas.noGroups')
  if (errorCode.value === 'key-unavailable') return t('infiniteCanvas.keyUnavailable')
  if (errorCode.value === 'missing-key') return t('infiniteCanvas.missingKey')
  if (errorCode.value === 'request-failed') return t('infiniteCanvas.requestFailed')
  return ''
})

function canvasLang(value: string): string {
  return value.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

function verificationErrorText(reason?: string): string {
  switch (reason) {
    case 'VERIFY_PASSWORD_INVALID': return t('keys.factor.passwordInvalid')
    case 'VERIFY_TOTP_INVALID': return t('keys.factor.totpInvalid')
    case 'VERIFY_PASSKEY_FAILED': return t('keys.factor.passkeyFailed')
    case 'VERIFY_FACTOR_UNAVAILABLE': return t('keys.factor.unavailable')
    case 'VERIFY_REQUIRED': return t('keys.factor.requiredAll')
    default: return t('infiniteCanvas.requestFailed')
  }
}

async function loadFactors(): Promise<boolean> {
  factorsLoading.value = true
  try {
    factors.value = await keysAPI.getVerificationFactors()
    return true
  } catch {
    factors.value = null
    return false
  } finally {
    factorsLoading.value = false
  }
}

// allowRotate 只会在用户点了「轮换并进入画布」时为 true；进入页面/刷新页面永远是 false。
// verification 只在本次调用里使用，不保存、不写日志。
async function prepareSession(createIfMissing = false, allowRotate = false, verification?: ApiKeyVerificationPayload) {
  const retryAction: PendingAction | null = allowRotate ? 'rotate' : createIfMissing ? 'create' : null
  loading.value = true
  errorCode.value = null
  pendingAction.value = null
  embedUrl.value = ''
  try {
    if (!appStore.publicSettingsLoaded) {
      await appStore.fetchPublicSettings()
    }
    if (disposed) return
    const session = await ensureInfiniteCanvasApiKey(undefined, {
      createIfMissing,
      allowRotate,
      ...(verification ? { verification } : {}),
    })
    if (disposed) return
    const pageOrigin = window.location.origin
    const openaiBaseUrl = resolveHttpBaseUrl(pageOrigin)
    const canvasBaseUrl = resolveInfiniteCanvasBaseUrl(pageOrigin)
    embedUrl.value = buildInfiniteCanvasImportUrl({
      canvasBaseUrl,
      apiKey: session.apiKey,
      openaiBaseUrl,
      pageOrigin,
      theme: detectTheme(),
      lang: canvasLang(String(locale.value || '')),
    })
    rotationTarget.value = null
    verifyError.value = ''
    if (session.created) {
      appStore.showSuccess(t('infiniteCanvas.keyCreated'))
    }
    if (session.rotated) {
      appStore.showSuccess(t('infiniteCanvas.keyRotated'))
    }
    if (session.truncated) {
      appStore.showWarning(t('infiniteCanvas.groupsTruncated'))
    }
  } catch (error) {
    if (disposed) return
    if (error instanceof InfiniteCanvasSetupError) {
      if (error.code === 'rotation-required' || error.code === 'not-configured') {
        // 需要用户确认：展示确认卡（含身份校验），不发任何轮换/创建请求。
        rotationTarget.value = error.code === 'rotation-required' ? (error.rotationTarget ?? null) : null
        pendingAction.value = error.code === 'rotation-required' ? 'rotate' : 'create'
        verifyError.value = ''
        if (!(await loadFactors())) {
          pendingAction.value = null
          errorCode.value = 'request-failed'
        }
      } else if (error.code === 'verification-failed' && retryAction) {
        // 身份校验没通过：留在确认卡上让用户重试，并给出具体原因。
        pendingAction.value = retryAction
        verifyError.value = verificationErrorText(error.reason)
        if (error.reason === 'VERIFY_REQUIRED' || error.reason === 'VERIFY_FACTOR_UNAVAILABLE') {
          await loadFactors()
        }
      } else {
        errorCode.value = error.code === 'verification-failed' ? 'request-failed' : error.code
      }
    } else {
      errorCode.value = 'request-failed'
    }
  } finally {
    loading.value = false
  }
}

async function submitPending() {
  const action = pendingAction.value
  if (!action || factorsLoading.value) return
  let verification: ApiKeyVerificationPayload | undefined
  if (needsVerification.value) {
    const built = verificationRef.value?.buildVerification() ?? null
    // 校验材料不完整：组件内已经展示了具体提示，这里不发请求。
    if (!built) return
    verification = built
  }
  verifyError.value = ''
  await prepareSession(action === 'create', action === 'rotate', verification)
}

onMounted(() => {
  void prepareSession()
})

onUnmounted(() => { disposed = true })
</script>

<style scoped>
@reference "../../style.css";
.canvas-page-layout {
  @apply flex flex-col;
  height: calc(100vh - 64px - 4rem);
}

.canvas-embed-shell {
  @apply relative h-full min-h-0 overflow-hidden bg-gray-50 dark:bg-dark-900;
}

.canvas-embed-frame {
  @apply h-full w-full border-0 bg-white dark:bg-dark-900;
}
</style>
