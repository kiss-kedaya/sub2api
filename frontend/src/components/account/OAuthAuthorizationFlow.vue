<template>
  <div>
      <div class="flex items-start gap-3">
      <div class="flex size-8 shrink-0 items-center justify-center rounded-md bg-elevated">
        <UIcon name="i-lucide-link" class="size-4 text-muted" />
      </div>
      <div class="flex-1">
        <h4 class="mb-3 font-semibold text-highlighted">{{ oauthTitle }}</h4>

        <!-- Auth Method Selection -->
        <div v-if="showMethodSelection" class="mb-4">
          <label class="mb-2 block text-sm font-medium text-highlighted">
            {{ methodLabel }}
          </label>
          <URadioGroup
            v-model="inputMethod"
            :items="[
              ...(showManualOption ? [{ label: t('admin.accounts.oauth.manualAuth'), value: 'manual' }] : []),
              ...(showCookieOption ? [{ label: t('admin.accounts.oauth.cookieAutoAuth'), value: 'cookie' }] : []),
              ...(showRefreshTokenOption ? [{ label: t(getOAuthKey('refreshTokenAuth')), value: 'refresh_token' }] : []),
              ...(showSsoOption ? [{ label: t(getOAuthKey('ssoCookieAuth')), value: 'sso_cookie' }] : []),
              ...(emailPasswordOptionEnabled ? [{ label: t(getOAuthKey('emailPasswordAuth')), value: 'email_password' }] : []),
              ...(showMobileRefreshTokenOption ? [{ label: t('admin.accounts.oauth.openai.mobileRefreshTokenAuth'), value: 'mobile_refresh_token' }] : []),
              ...(showSessionTokenOption ? [{ label: t(getOAuthKey('sessionTokenAuth')), value: 'session_token' }] : []),
              ...(showAccessTokenOption ? [{ label: t('admin.accounts.oauth.openai.accessTokenAuth'), value: 'access_token' }] : []),
              ...(showCodexSessionImportOption ? [{ label: t('admin.accounts.oauth.openai.codexSessionAuth'), value: 'codex_session' }] : []),
              ...(showAgentIdentityOption ? [{ label: t('admin.accounts.oauth.openai.agentIdentityAuth'), value: 'agent_identity' }] : []),
              ...(showCodexPatOption ? [{ label: t('admin.accounts.oauth.openai.codexPatAuth'), value: 'codex_pat' }] : [])
            ]"
            orientation="horizontal"
            :ui="{ fieldset: 'flex-wrap gap-x-4 gap-y-2' }"
          />
        </div>

        <!-- Refresh Token Input (OpenAI / Antigravity / Mobile RT) -->
        <div v-if="inputMethod === 'refresh_token' || inputMethod === 'mobile_refresh_token'" class="space-y-4">
          <div
            class="py-1"
          >
            <p class="mb-3 text-sm text-highlighted">
              {{ t(getOAuthKey('refreshTokenDesc')) }}
            </p>

            <!-- Refresh Token Input -->
            <div class="mb-4">
              <label
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-toned"
              >
                <Icon name="key" size="sm" class="text-primary" />
                Refresh Token
                <span
                  v-if="parsedRefreshTokenCount > 1"
                  class="rounded-full bg-primary px-2 py-0.5 text-xs text-inverted"
                >
                  {{ t('admin.accounts.oauth.keysCount', { count: parsedRefreshTokenCount }) }}
                </span>
              </label>
              <UTextarea
                :rows="3"
                v-model="refreshTokenInput"
                :placeholder="t(getOAuthKey('refreshTokenPlaceholder'))"
                class="w-full resize-y font-mono text-sm"
              ></UTextarea>
              <p
                v-if="parsedRefreshTokenCount > 1"
                class="mt-1 text-xs text-primary"
              >
                {{ t('admin.accounts.oauth.batchCreateAccounts', { count: parsedRefreshTokenCount }) }}
              </p>
            </div>

            <!-- Error Message -->
            <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mb-4" />

            <!-- Validate Button -->
            <UButton
              color="primary"
              variant="solid"
              type="button"
              :disabled="loading || !refreshTokenInput.trim()"
              @click="handleValidateRefreshToken"
              class="w-full" :loading="loading" icon="i-lucide-sparkles"
            >
              {{
                loading
                  ? t(getOAuthKey('validating'))
                  : t(getOAuthKey('validateAndCreate'))
              }}
            </UButton>
          </div>
        </div>

        <!-- SSO Cookie Input (Grok Web -> Grok Build) -->
        <div v-if="inputMethod === 'sso_cookie'" class="space-y-4">
          <div
            class="py-1"
          >
            <p class="mb-3 text-sm text-highlighted">
              {{ t(getOAuthKey('ssoCookieDesc')) }}
            </p>

            <div class="mb-4">
              <label
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-toned"
              >
                <Icon name="key" size="sm" class="text-primary" />
                {{ t(getOAuthKey('ssoCookieLabel')) }}
                <span
                  v-if="parsedSSOCount > 1"
                  class="rounded-full bg-primary px-2 py-0.5 text-xs text-inverted"
                >
                  {{ t('admin.accounts.oauth.keysCount', { count: parsedSSOCount }) }}
                </span>
              </label>
              <UTextarea
                :rows="5"
                v-model="ssoCookieInput"
                :placeholder="t(getOAuthKey('ssoCookiePlaceholder'))"
                spellcheck="false"
                class="w-full resize-y font-mono text-sm"
              ></UTextarea>
              <p class="mt-1 text-xs text-primary">
                {{ t(getOAuthKey('ssoCookieHint')) }}
              </p>
            </div>

            <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mb-4" />

            <UButton
              color="primary"
              variant="solid"
              type="button"
              :disabled="loading || !ssoCookieInput.trim()"
              @click="handleImportSSO"
              class="w-full" :loading="loading" icon="i-lucide-sparkles"
            >
              {{ loading ? t(getOAuthKey('convertingSSO')) : t(getOAuthKey('convertSSOAndCreate')) }}
            </UButton>
          </div>
        </div>

        <!-- Grok email + password → ephemeral SSO → Build OAuth (password never stored) -->
        <div v-if="inputMethod === 'email_password'" class="space-y-4">
          <div
            class="py-1"
          >
            <p class="mb-3 text-sm text-highlighted">
              {{ t(getOAuthKey('emailPasswordDesc')) }}
            </p>
            <div class="mb-4">
              <label
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-toned"
              >
                <Icon name="user" size="sm" class="text-primary" />
                {{ t(getOAuthKey('emailPasswordInputLabel')) }}
                <span
                  v-if="parsedEmailPasswordCount > 1"
                  class="rounded-full bg-primary px-2 py-0.5 text-xs text-inverted"
                >
                  {{ t('admin.accounts.oauth.keysCount', { count: parsedEmailPasswordCount }) }}
                </span>
              </label>
              <UTextarea
                :rows="4"
                v-model="emailPasswordInput"
                :placeholder="t(getOAuthKey('emailPasswordPlaceholder'))"
                spellcheck="false"
                autocomplete="off"
                class="w-full resize-y font-mono text-sm"
              ></UTextarea>
              <p class="mt-1 text-xs text-primary">
                {{ t(getOAuthKey('emailPasswordHint')) }}
              </p>
            </div>
            <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mb-4" />
            <UButton
              color="primary"
              variant="solid"
              type="button"
              :disabled="loading || !emailPasswordInput.trim()"
              @click="handleAuthorizePassword"
              class="w-full" :loading="loading" icon="i-lucide-sparkles"
            >
              {{ loading ? t(getOAuthKey('validating')) : t(getOAuthKey('validateAndCreate')) }}
            </UButton>
          </div>
        </div>

        <!-- Codex auth.json / session credential batch import -->
        <div v-if="inputMethod === 'codex_session' || inputMethod === 'agent_identity'" class="space-y-4">
          <div
            class="py-1"
          >
            <p class="mb-3 text-sm text-highlighted">
              {{ t(isAgentIdentityInput ? 'admin.accounts.oauth.openai.agentIdentityDesc' : 'admin.accounts.oauth.openai.codexSessionDesc') }}
            </p>

            <div class="mb-4">
              <label
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-toned"
              >
                <Icon name="key" size="sm" class="text-primary" />
                {{ t(isAgentIdentityInput ? 'admin.accounts.oauth.openai.agentIdentityInputLabel' : 'admin.accounts.oauth.openai.codexSessionInputLabel') }}
                <span
                  v-if="parsedCodexSessionCount > 1"
                  class="rounded-full bg-primary px-2 py-0.5 text-xs text-inverted"
                >
                  {{ t('admin.accounts.oauth.keysCount', { count: parsedCodexSessionCount }) }}
                </span>
              </label>
              <UTextarea
                :rows="8"
                v-model="codexSessionInput"
                :placeholder="t(isAgentIdentityInput ? 'admin.accounts.oauth.openai.agentIdentityPlaceholder' : 'admin.accounts.oauth.openai.codexSessionPlaceholder')"
                spellcheck="false"
                class="w-full resize-y font-mono text-sm"
              ></UTextarea>
              <p class="mt-1 text-xs text-primary">
                {{ t(isAgentIdentityInput ? 'admin.accounts.oauth.openai.agentIdentityHint' : 'admin.accounts.oauth.openai.codexSessionHint') }}
              </p>
            </div>

            <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mb-4" />

            <UButton
              color="primary"
              variant="solid"
              type="button"
              :disabled="loading || !codexSessionInput.trim()"
              @click="handleImportCodexSession"
              class="w-full" :loading="loading" icon="i-lucide-sparkles"
            >
              {{
                loading
                  ? t('admin.accounts.oauth.openai.validating')
                  : t('admin.accounts.oauth.openai.codexSessionImportAndCreate')
              }}
            </UButton>
          </div>
        </div>

        <!-- Codex Personal Access Token -->
        <div v-if="inputMethod === 'codex_pat'" class="space-y-4">
          <div
            class="py-1"
          >
            <p class="mb-3 text-sm text-highlighted">
              {{ t('admin.accounts.oauth.openai.codexPatDesc') }}
            </p>

            <div class="mb-4">
              <label
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-toned"
              >
                <Icon name="key" size="sm" class="text-primary" />
                {{ t('admin.accounts.oauth.openai.codexPatInputLabel') }}
              </label>
              <UTextarea
                :rows="3"
                v-model="codexPATInput"
                :placeholder="t('admin.accounts.oauth.openai.codexPatPlaceholder')"
                spellcheck="false"
                class="w-full resize-y font-mono text-sm"
              ></UTextarea>
              <p class="mt-1 text-xs text-primary">
                {{ t('admin.accounts.oauth.openai.codexPatHint') }}
              </p>
            </div>

            <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mb-4" />

            <UButton
              color="primary"
              variant="solid"
              type="button"
              :disabled="loading || !codexPATInput.trim()"
              @click="handleImportCodexPAT"
              class="w-full" :loading="loading" icon="i-lucide-sparkles"
            >
              {{
                loading
                  ? t('admin.accounts.oauth.openai.validating')
                  : t('admin.accounts.oauth.openai.codexPatImportAndCreate')
              }}
            </UButton>
          </div>
        </div>

        <!-- Cookie Auto-Auth Form -->
        <div v-if="inputMethod === 'cookie'" class="space-y-4">
          <div
            class="py-1"
          >
            <p class="mb-3 text-sm text-highlighted">
              {{ t('admin.accounts.oauth.cookieAutoAuthDesc') }}
            </p>

            <!-- sessionKey Input -->
            <div class="mb-4">
              <label
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-toned"
              >
                <Icon name="key" size="sm" class="text-primary" />
                {{ t('admin.accounts.oauth.sessionKey') }}
                <span
                  v-if="parsedKeyCount > 1 && allowMultiple"
                  class="rounded-full bg-primary px-2 py-0.5 text-xs text-inverted"
                >
                  {{ t('admin.accounts.oauth.keysCount', { count: parsedKeyCount }) }}
                </span>
                <UButton
                  color="primary"
                  variant="link"
                  size="xs"
                  icon="i-lucide-circle-help"
                  v-if="showHelp"
                  type="button"
                  @click="showHelpDialog = !showHelpDialog"
                />
              </label>
              <UTextarea
                :rows="3"
                v-model="sessionKeyInput"
                :placeholder="
                  allowMultiple
                    ? t('admin.accounts.oauth.sessionKeyPlaceholder')
                    : t('admin.accounts.oauth.sessionKeyPlaceholderSingle')
                "
                class="w-full resize-y font-mono text-sm"
              ></UTextarea>
              <p
                v-if="parsedKeyCount > 1 && allowMultiple"
                class="mt-1 text-xs text-primary"
              >
                {{ t('admin.accounts.oauth.batchCreateAccounts', { count: parsedKeyCount }) }}
              </p>
            </div>

            <!-- Help Section -->
            <div
              v-if="showHelpDialog && showHelp"
              class="mb-4 rounded-md bg-warning/10 p-3"
            >
              <h5 class="mb-2 font-semibold text-warning">
                {{ t('admin.accounts.oauth.howToGetSessionKey') }}
              </h5>
              <ol
                class="list-inside list-decimal space-y-1 text-xs text-warning"
              >
                <li>{{ t('admin.accounts.oauth.step1') }}</li>
                <li>{{ t('admin.accounts.oauth.step2') }}</li>
                <li>{{ t('admin.accounts.oauth.step3') }}</li>
                <li>{{ t('admin.accounts.oauth.step4') }}</li>
                <li>{{ t('admin.accounts.oauth.step5') }}</li>
                <li>{{ t('admin.accounts.oauth.step6') }}</li>
              </ol>
              <p
                class="mt-2 text-xs text-warning"
                v-text="t('admin.accounts.oauth.sessionKeyFormat')"
              ></p>
            </div>

            <!-- Error Message -->
            <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mb-4" />

            <!-- Auth Button -->
            <UButton
              color="primary"
              variant="solid"
              type="button"
              :disabled="loading || !sessionKeyInput.trim()"
              @click="handleCookieAuth"
              class="w-full" :loading="loading" icon="i-lucide-sparkles"
            >
              {{
                loading
                  ? t('admin.accounts.oauth.authorizing')
                  : t('admin.accounts.oauth.startAutoAuth')
              }}
            </UButton>
          </div>
        </div>

        <!-- Manual Authorization Flow -->
        <div v-if="inputMethod === 'manual'" class="space-y-4">
          <p class="mb-4 text-sm text-highlighted">
            {{ oauthFollowSteps }}
          </p>

          <!-- Step 1: Generate Auth URL -->
          <div
            class="py-1"
          >
            <div class="flex items-start gap-3">
              <div
                class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-primary text-xs font-bold text-inverted"
              >
                1
              </div>
              <div class="flex-1">
                <p class="mb-2 font-medium text-highlighted">
                  {{ oauthStep1GenerateUrl }}
                </p>
                <div v-if="showProjectId && platform === 'gemini'" class="mb-3">
                  <label class="mb-1.5 block text-sm font-medium text-default flex items-center gap-2">
                    {{ t('admin.accounts.oauth.gemini.projectIdLabel') }}
                    <a
                      href="https://console.cloud.google.com/"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="inline-flex items-center gap-1 text-xs font-normal text-primary"
                    >
                      <UIcon name="i-lucide-circle-help" class="size-3" />
                      {{ t('admin.accounts.oauth.gemini.howToGetProjectId') }}
                    </a>
                  </label>
                  <UInput
                    v-model="projectId"
                    type="text"
                    :placeholder="t('admin.accounts.oauth.gemini.projectIdPlaceholder')"
                    class="w-full font-mono text-sm"
                  />
                  <p class="mt-1 text-xs text-muted">
                    {{ t('admin.accounts.oauth.gemini.projectIdHint') }}
                  </p>
                </div>
                <UButton
                  color="primary"
                  variant="solid"
                  v-if="!authUrl"
                  type="button"
                  :disabled="loading"
                  @click="handleGenerateUrl"
                  class="text-sm" :loading="loading" icon="i-lucide-link"
                >
                  {{ loading ? t('admin.accounts.oauth.generating') : oauthGenerateAuthUrl }}
                </UButton>
                <div v-else class="space-y-3">
                  <div class="flex items-center gap-2">
                    <UInput
                      :value="authUrl"
                      readonly
                      type="text"
                      class="flex-1 bg-elevated font-mono text-xs"
                    />
                    <UButton
                      color="neutral"
                      variant="outline"
                      type="button"
                      title="Copy URL"
                      @click="handleCopyUrl"
                      class="p-2"
                    >
                      <UIcon name="i-lucide-copy" v-if="!copied" class="size-4" />
                      <Icon
                        v-else
                        name="check"
                        size="sm"
                        class="text-success"
                        :stroke-width="2"
                      />
                    </UButton>
                  </div>
                  <UButton color="primary" variant="link" size="xs" icon="i-lucide-refresh-cw" type="button" @click="handleRegenerate">{{ t('admin.accounts.oauth.regenerate') }}</UButton>
                </div>
              </div>
            </div>
          </div>

          <!-- Step 2: Open URL and authorize -->
          <div
            class="py-1"
          >
            <div class="flex items-start gap-3">
              <div
                class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-primary text-xs font-bold text-inverted"
              >
                2
              </div>
              <div class="flex-1">
                <p class="mb-2 font-medium text-highlighted">
                  {{ oauthStep2OpenUrl }}
                </p>
                <p class="text-sm text-highlighted">
                  {{ oauthOpenUrlDesc }}
                </p>
                <!-- Local callback notice -->
                <UAlert v-if="showLocalCallbackNotice" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="oauthImportantNotice" class="mt-2" />
                <!-- Proxy Warning (for non-OpenAI) -->
                <UAlert v-else-if="showProxyWarning" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.accounts.oauth.proxyWarning')" class="mt-2" />
              </div>
            </div>
          </div>

          <!-- Step 3: Enter authorization code -->
          <div
            class="py-1"
          >
            <div class="flex items-start gap-3">
              <div
                class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-primary text-xs font-bold text-inverted"
              >
                3
              </div>
              <div class="flex-1">
                <p class="mb-2 font-medium text-highlighted">
                  {{ oauthStep3EnterCode }}
                </p>
                <p
                  class="mb-3 text-sm text-highlighted"
                  v-text="oauthAuthCodeDesc"
                ></p>
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-default">
                    <Icon name="key" size="sm" class="mr-1 inline text-primary" />
                    {{ oauthAuthCode }}
                  </label>
                  <UTextarea
                    :rows="3"
                    v-model="authCodeInput"
                    :placeholder="oauthAuthCodePlaceholder"
                    class="w-full resize-none font-mono text-sm"
                  ></UTextarea>
                  <p class="mt-2 text-xs text-muted">
                    <Icon name="infoCircle" size="xs" class="mr-1 inline" />
                    {{ oauthAuthCodeHint }}
                  </p>

                  <!-- Gemini-specific state parameter warning -->
                  <div
                    v-if="platform === 'gemini'"
                    class="mt-3 rounded-md bg-warning/10 p-3"
                  >
                    <div class="flex items-start gap-2">
                      <Icon
                        name="exclamationTriangle"
                        size="md"
                        class="flex-shrink-0 text-warning"
                        :stroke-width="2"
                      />
                      <div class="text-sm text-warning">
                        <p class="font-semibold">{{ $t('admin.accounts.oauth.gemini.stateWarningTitle') }}</p>
                        <p class="mt-1">{{ $t('admin.accounts.oauth.gemini.stateWarningDesc') }}</p>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- Error Message -->
                <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-x" :description="error" class="mt-3" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import Icon from '@/components/icons/Icon.vue'
import type { AddMethod, AuthInputMethod } from '@/composables/useAccountOAuth'
import type { AccountPlatform } from '@/types'
import { adminAPI } from '@/api/admin'

interface Props {
  addMethod: AddMethod
  authUrl?: string
  sessionId?: string
  loading?: boolean
  error?: string
  showHelp?: boolean
  showProxyWarning?: boolean
  allowMultiple?: boolean
  methodLabel?: string
  showCookieOption?: boolean // Whether to show cookie auto-auth option
  showRefreshTokenOption?: boolean // Whether to show refresh token input option (OpenAI only)
  showMobileRefreshTokenOption?: boolean // Whether to show mobile refresh token option (OpenAI only)
  showSessionTokenOption?: boolean
  showAccessTokenOption?: boolean
  showCodexSessionImportOption?: boolean
  showAgentIdentityOption?: boolean
  showCodexPatOption?: boolean
  showSsoOption?: boolean
  /** Grok email----password login (admin; password never persisted). */
  showEmailPasswordOption?: boolean
  showManualOption?: boolean
  initialInputMethod?: AuthInputMethod
  /**
   * Prefill for Grok email----password reauth. Password is never stored;
   * pass only the email (or "email----") so the operator types the password.
   */
  initialEmailPassword?: string
  platform?: AccountPlatform // Platform type for different UI/text
  showProjectId?: boolean // New prop to control project ID visibility
}

const props = withDefaults(defineProps<Props>(), {
  authUrl: '',
  sessionId: '',
  loading: false,
  error: '',
  showHelp: true,
  showProxyWarning: true,
  allowMultiple: false,
  methodLabel: 'Authorization Method',
  showCookieOption: true,
  showRefreshTokenOption: false,
  showMobileRefreshTokenOption: false,
  showSessionTokenOption: false,
  showAccessTokenOption: false,
  showCodexSessionImportOption: false,
  showAgentIdentityOption: false,
  showCodexPatOption: false,
  showSsoOption: false,
  showEmailPasswordOption: false,
  showManualOption: true,
  initialInputMethod: 'manual',
  initialEmailPassword: '',
  platform: 'anthropic',
  showProjectId: true
})

const emit = defineEmits<{
  'generate-url': []
  'exchange-code': [code: string]
  'cookie-auth': [sessionKey: string]
  'validate-refresh-token': [refreshToken: string]
  'validate-mobile-refresh-token': [refreshToken: string]
  'validate-session-token': [sessionToken: string]
  'import-access-token': [accessToken: string]
  'import-codex-session': [content: string]
  'import-codex-pat': [accessToken: string]
  'import-sso': [content: string]
  'authorize-password': [emailPasswordInput: string]
  'update:inputMethod': [method: AuthInputMethod]
}>()

const { t } = useI18n()
const passwordAuthEnabled = ref(false)
const emailPasswordOptionEnabled = computed(
  () => props.showEmailPasswordOption && props.platform === 'grok' && passwordAuthEnabled.value
)

const showLocalCallbackNotice = computed(() => props.platform === 'openai' || props.platform === 'grok')

// Get translation key based on platform
const getOAuthKey = (key: string) => {
  if (props.platform === 'openai') return `admin.accounts.oauth.openai.${key}`
  if (props.platform === 'gemini') return `admin.accounts.oauth.gemini.${key}`
  if (props.platform === 'antigravity') return `admin.accounts.oauth.antigravity.${key}`
  if (props.platform === 'grok') return `admin.accounts.oauth.grok.${key}`
  return `admin.accounts.oauth.${key}`
}

// Computed translations for current platform
const oauthTitle = computed(() => t(getOAuthKey('title')))
const oauthFollowSteps = computed(() => t(getOAuthKey('followSteps')))
const oauthStep1GenerateUrl = computed(() => t(getOAuthKey('step1GenerateUrl')))
const oauthGenerateAuthUrl = computed(() => t(getOAuthKey('generateAuthUrl')))
const oauthStep2OpenUrl = computed(() => t(getOAuthKey('step2OpenUrl')))
const oauthOpenUrlDesc = computed(() => t(getOAuthKey('openUrlDesc')))
const oauthStep3EnterCode = computed(() => t(getOAuthKey('step3EnterCode')))
const oauthAuthCodeDesc = computed(() => t(getOAuthKey('authCodeDesc')))
const oauthAuthCode = computed(() => t(getOAuthKey('authCode')))
const oauthAuthCodePlaceholder = computed(() => t(getOAuthKey('authCodePlaceholder')))
const oauthAuthCodeHint = computed(() => t(getOAuthKey('authCodeHint')))
const oauthImportantNotice = computed(() => {
  if (props.platform === 'openai') return t('admin.accounts.oauth.openai.importantNotice')
  if (props.platform === 'antigravity') return t('admin.accounts.oauth.antigravity.importantNotice')
  if (props.platform === 'grok') return t('admin.accounts.oauth.grok.importantNotice')
  return ''
})

// Local state
const inputMethod = ref<AuthInputMethod>(props.initialInputMethod)
const isAgentIdentityInput = computed(() => inputMethod.value === 'agent_identity')
const authCodeInput = ref('')
const sessionKeyInput = ref('')
const refreshTokenInput = ref('')
const sessionTokenInput = ref('')
const codexSessionInput = ref('')
const codexPATInput = ref('')
const ssoCookieInput = ref('')
const emailPasswordInput = ref(props.initialEmailPassword || '')
const showHelpDialog = ref(false)
const oauthState = ref('')
const projectId = ref('')

watch(
  () => [props.platform, props.showEmailPasswordOption] as const,
  async ([platform, requested]) => {
    passwordAuthEnabled.value = false
    if (platform !== 'grok' || !requested) return
    try {
      const capabilities = await adminAPI.grok.getCapabilities()
      passwordAuthEnabled.value = capabilities.password_auth_enabled
    } catch {
      // Fail closed; the backend enforces the same capability.
    }
  },
  { immediate: true }
)

watch(emailPasswordOptionEnabled, (enabled) => {
  if (!enabled && inputMethod.value === 'email_password') inputMethod.value = 'manual'
})

// Computed: show method selection only when there is something to choose.
const methodOptionCount = computed(() => [
  props.showManualOption,
  props.showCookieOption,
  props.showRefreshTokenOption,
  props.showMobileRefreshTokenOption,
  props.showSessionTokenOption,
  props.showAccessTokenOption,
  props.showCodexSessionImportOption,
  props.showAgentIdentityOption,
  props.showCodexPatOption,
  props.showSsoOption,
  emailPasswordOptionEnabled.value
].filter(Boolean).length)
const showMethodSelection = computed(() => methodOptionCount.value > 1)

// Clipboard
const { copied, copyToClipboard } = useClipboard()

// Computed
const parsedKeyCount = computed(() => {
  return sessionKeyInput.value
    .split('\n')
    .map((k) => k.trim())
    .filter((k) => k).length
})

// Computed: count of refresh tokens entered
const parsedRefreshTokenCount = computed(() => {
  return refreshTokenInput.value
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt).length
})

const parsedCodexSessionCount = computed(() => {
  const trimmed = codexSessionInput.value.trim()
  if (!trimmed) return 0
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) return 1
  return trimmed
    .split('\n')
    .map((item) => item.trim())
    .filter((item) => item).length
})

const parsedSSOCount = computed(() => {
  return ssoCookieInput.value
    .split('\n')
    .map((item) => item.trim())
    .filter((item) => item).length
})

const parsedEmailPasswordCount = computed(() => {
  return emailPasswordInput.value
    .split('\n')
    .map((item) => item.trim())
    .filter((item) => item && item.includes('----')).length
})

const handleAuthorizePassword = () => {
  if (emailPasswordInput.value.trim()) {
    emit('authorize-password', emailPasswordInput.value)
  }
}

// Watchers
watch(() => props.initialInputMethod, (newVal) => {
  inputMethod.value = newVal
})

watch(
  () => props.initialEmailPassword,
  (newVal) => {
    // Only prefill when the field is empty so we never overwrite operator input.
    if (newVal && !emailPasswordInput.value.trim()) {
      emailPasswordInput.value = newVal
    }
  }
)

watch(inputMethod, (newVal) => {
  emit('update:inputMethod', newVal)
})

// Auto-extract code from callback URL (OpenAI/Gemini/Antigravity/Grok)
// e.g., http://localhost:8085/callback?code=xxx...&state=...
watch(authCodeInput, (newVal) => {
  if (props.platform !== 'openai' && props.platform !== 'gemini' && props.platform !== 'antigravity' && props.platform !== 'grok') return

  const trimmed = newVal.trim()
  // Check if it looks like a URL with code parameter
  if (trimmed.includes('code=')) {
    try {
      // Try to parse as URL
      const url = trimmed.includes('?') ? new URL(trimmed) : new URL(`http://localhost/callback?${trimmed.replace(/^\?/, '')}`)
      const code = url.searchParams.get('code')
      const stateParam = url.searchParams.get('state')
      if ((props.platform === 'openai' || props.platform === 'gemini' || props.platform === 'antigravity' || props.platform === 'grok') && stateParam) {
        oauthState.value = stateParam
      }
      if (code && code !== trimmed) {
        // Replace the input with just the code
        authCodeInput.value = code
      }
    } catch {
      // If URL parsing fails, try regex extraction
      const match = trimmed.match(/[?&]code=([^&]+)/)
      const stateMatch = trimmed.match(/[?&]state=([^&]+)/)
      if ((props.platform === 'openai' || props.platform === 'gemini' || props.platform === 'antigravity' || props.platform === 'grok') && stateMatch && stateMatch[1]) {
        oauthState.value = stateMatch[1]
      }
      if (match && match[1] && match[1] !== trimmed) {
        authCodeInput.value = match[1]
      }
    }
  }
})

// Methods
const handleGenerateUrl = () => {
  emit('generate-url')
}

const handleCopyUrl = () => {
  if (props.authUrl) {
    copyToClipboard(props.authUrl, 'URL copied to clipboard')
  }
}

const handleRegenerate = () => {
  authCodeInput.value = ''
  emit('generate-url')
}

const handleCookieAuth = () => {
  if (sessionKeyInput.value.trim()) {
    emit('cookie-auth', sessionKeyInput.value)
  }
}

const handleValidateRefreshToken = () => {
  if (refreshTokenInput.value.trim()) {
    if (inputMethod.value === 'mobile_refresh_token') {
      emit('validate-mobile-refresh-token', refreshTokenInput.value.trim())
    } else {
      emit('validate-refresh-token', refreshTokenInput.value.trim())
    }
  }
}

const handleImportCodexSession = () => {
  if (codexSessionInput.value.trim()) {
    emit('import-codex-session', codexSessionInput.value.trim())
  }
}

const handleImportCodexPAT = () => {
  if (codexPATInput.value.trim()) {
    emit('import-codex-pat', codexPATInput.value.trim())
  }
}

const handleImportSSO = () => {
  if (ssoCookieInput.value.trim()) {
    emit('import-sso', ssoCookieInput.value.trim())
  }
}

// Expose methods and state
defineExpose({
  authCode: authCodeInput,
  oauthState,
  projectId,
  sessionKey: sessionKeyInput,
  refreshToken: refreshTokenInput,
  sessionToken: sessionTokenInput,
  codexSession: codexSessionInput,
  codexPAT: codexPATInput,
  ssoCookie: ssoCookieInput,
  emailPassword: emailPasswordInput,
  inputMethod,
  reset: () => {
    authCodeInput.value = ''
    oauthState.value = ''
    projectId.value = ''
    sessionKeyInput.value = ''
    refreshTokenInput.value = ''
    sessionTokenInput.value = ''
    codexSessionInput.value = ''
    codexPATInput.value = ''
    ssoCookieInput.value = ''
    emailPasswordInput.value = ''
    inputMethod.value = props.initialInputMethod
    showHelpDialog.value = false
  }
})
</script>
