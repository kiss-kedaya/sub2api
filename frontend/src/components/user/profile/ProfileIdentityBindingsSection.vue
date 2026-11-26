<template>
  <div :class="props.embedded ? 'space-y-4' : 'overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none'">
    <div
      v-if="!props.embedded"
      class="border-b border-default px-5 py-4 sm:px-6"
    >
      <h2 class="text-base font-semibold text-highlighted">
        {{ t('profile.authBindings.title') }}
      </h2>
      <p class="mt-1 text-sm text-muted">
        {{ t('profile.authBindings.description') }}
      </p>
    </div>

    <div :class="props.embedded ? 'space-y-4' : 'divide-y divide-gray-100 dark:divide-dark-700'">
      <div v-if="props.embedded">
        <p class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('profile.authBindings.title') }}
        </p>
        <p class="mt-1 text-sm text-muted">
          {{ t('profile.authBindings.description') }}
        </p>
      </div>

      <div
        v-for="item in providerItems"
        :key="item.provider"
        :class="rowClass"
      >
        <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div class="flex min-w-0 flex-1 items-start gap-4">
            <div
              :data-testid="`profile-binding-${item.provider}-icon`"
              :class="providerIconClass(item.provider)"
              class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl"
            >
              <Icon
                v-if="item.provider === 'email'"
                name="mail"
                size="md"
                class="text-current"
              />
              <GitHubMark v-else-if="item.provider === 'github'" class="size-5" />
              <GoogleMark v-else-if="item.provider === 'google'" class="size-5" />
              <LinuxDoMark v-else-if="item.provider === 'linuxdo'" class="size-5" />
              <DingTalkMark v-else-if="item.provider === 'dingtalk'" class="size-5" />
              <OidcMark v-else-if="item.provider === 'oidc'" class="size-5" />
              <WeChatMark v-else-if="item.provider === 'wechat'" class="size-5" />
            </div>

            <div class="min-w-0 flex-1 space-y-2">
              <div
                class="flex flex-wrap items-center gap-2"
                :class="props.embedded ? 'min-h-9' : 'min-h-11'"
              >
                <h3 class="font-medium text-gray-900 dark:text-white">
                  {{ item.label }}
                </h3>
                <UBadge
                  :data-testid="`profile-binding-${item.provider}-status`"
                  :color="item.bound ? 'success' : 'neutral'"
                  variant="subtle"
                  size="sm"
                >
                  {{
                    item.bound
                      ? t('profile.authBindings.status.bound')
                      : t('profile.authBindings.status.notBound')
                  }}
                </UBadge>
              </div>

              <p
                v-if="providerSummary(item.provider)"
                class="text-sm text-gray-600 dark:text-gray-300"
              >
                {{ providerSummary(item.provider) }}
              </p>

              <div
                v-if="hasBindingDetails(item.provider, item.details)"
                class="grid gap-1 text-sm text-gray-500 dark:text-gray-400"
              >
                <p
                  v-if="item.provider !== 'email' && item.details?.display_name"
                  class="font-medium text-gray-700 dark:text-gray-200"
                >
                  {{ item.details.display_name }}
                </p>
                <p v-if="bindingCountLabel(item.details)">
                  {{ bindingCountLabel(item.details) }}
                </p>
                <p v-if="bindingNote(item.details)">
                  {{ bindingNote(item.details) }}
                </p>
              </div>

              <div
                v-if="item.provider === 'email' && showEmailForm"
                data-testid="profile-binding-email-form"
                class="grid gap-2 sm:grid-cols-[minmax(0,1.4fr)_auto]"
              >
                <UInput
                  v-model.trim="emailBindingForm.email"
                  data-testid="profile-binding-email-input"
                  type="email"
                  class="w-full"
                  :placeholder="t('profile.authBindings.emailPlaceholder')"
                  :disabled="isSendingEmailCode || isBindingEmail"
                />
                <UButton
                  data-testid="profile-binding-email-send-code"
                  type="button"
                  color="neutral"
                  variant="outline"
                  size="sm"
                  :disabled="isSendingEmailCode || isBindingEmail"
                  @click="sendEmailCode"
                >
                  {{
                    isSendingEmailCode
                      ? t('common.loading')
                      : t('profile.authBindings.sendCodeAction')
                  }}
                </UButton>
                <UInput
                  v-model.trim="emailBindingForm.verifyCode"
                  data-testid="profile-binding-email-code-input"
                  type="text"
                  inputmode="numeric"
                  maxlength="6"
                  class="w-full"
                  :placeholder="t('profile.authBindings.codePlaceholder')"
                  :disabled="isBindingEmail"
                />
                <UInput
                  v-model="emailBindingForm.password"
                  data-testid="profile-binding-email-password-input"
                  type="password"
                  class="w-full"
                  :placeholder="emailPasswordPlaceholder"
                  :disabled="isBindingEmail"
                />
                <UButton
                  data-testid="profile-binding-email-submit"
                  type="button"
                  size="sm"
                  class="sm:col-span-2"
                  :disabled="isBindingEmail"
                  @click="bindEmail"
                >
                  {{
                    isBindingEmail
                      ? t('common.loading')
                      : emailSubmitActionLabel
                  }}
                </UButton>
              </div>
            </div>
          </div>

          <div
            class="flex shrink-0 flex-wrap items-center gap-3"
            :class="props.embedded ? 'sm:min-h-9' : 'sm:min-h-11'"
          >
            <UButton
              v-if="item.provider === 'email' && compact"
              data-testid="profile-binding-email-toggle"
              type="button"
              color="neutral"
              variant="outline"
              size="sm"
              @click="toggleEmailForm"
            >
              {{
                showEmailForm
                  ? t('profile.authBindings.hideEmailFormAction')
                  : t('profile.authBindings.manageEmailAction')
              }}
            </UButton>
            <UButton
              v-if="item.canBind"
              :data-testid="`profile-binding-${item.provider}-action`"
              type="button"
              size="sm"
              @click="startBinding(item.provider)"
            >
              {{ t('profile.authBindings.bindAction', { providerName: item.label }) }}
            </UButton>
            <UButton
              v-if="item.canUnbind"
              :data-testid="`profile-binding-${item.provider}-unbind`"
              type="button"
              color="neutral"
              variant="outline"
              size="sm"
              :disabled="unbindingProvider === item.provider"
              :loading="unbindingProvider === item.provider"
              @click="handleUnbindForItem(item.provider, item.label)"
            >
              {{
                unbindingProvider === item.provider
                  ? t('common.loading')
                  : t('profile.authBindings.unbindAction')
              }}
            </UButton>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import {
  hasExplicitWeChatOAuthCapabilities,
  resolveWeChatOAuthStartStrict,
  type WeChatOAuthPublicSettings,
} from '@/api/auth'
import {
  bindEmailIdentity,
  sendEmailBindingCode,
  startOAuthBinding,
  unbindAuthIdentity,
} from '@/api/user'
import Icon from '@/components/icons/Icon.vue'
import GitHubMark from '@/components/auth/GitHubMark.vue'
import GoogleMark from '@/components/auth/GoogleMark.vue'
import LinuxDoMark from '@/components/auth/LinuxDoMark.vue'
import DingTalkMark from '@/components/auth/DingTalkMark.vue'
import OidcMark from '@/components/auth/OidcMark.vue'
import WeChatMark from '@/components/auth/WeChatMark.vue'
import { useAppStore, useAuthStore } from '@/stores'
import type { User, UserAuthBindingStatus, UserAuthProvider } from '@/types'

type BindableProvider = Exclude<UserAuthProvider, 'email'>

const props = withDefaults(
  defineProps<{
    user: User | null
    githubEnabled?: boolean
    googleEnabled?: boolean
    linuxdoEnabled?: boolean
    dingtalkEnabled?: boolean
    oidcEnabled?: boolean
    oidcProviderName?: string
    wechatEnabled?: boolean
    wechatOpenEnabled?: boolean
    wechatMpEnabled?: boolean
    embedded?: boolean
    compact?: boolean
  }>(),
  {
    githubEnabled: false,
    googleEnabled: false,
    linuxdoEnabled: false,
    dingtalkEnabled: false,
    oidcEnabled: false,
    oidcProviderName: 'OIDC',
    wechatEnabled: false,
    wechatOpenEnabled: undefined,
    wechatMpEnabled: undefined,
    embedded: false,
    compact: false,
  }
)

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()

const localUser = ref<User | null>(null)
const isSendingEmailCode = ref(false)
const isBindingEmail = ref(false)
const isEmailFormExpanded = ref(!props.compact)
const unbindingProvider = ref<BindableProvider | null>(null)
const emailBindingForm = reactive({
  email: '',
  verifyCode: '',
  password: '',
})

watch(
  () => props.user,
  (user) => {
    localUser.value = null
    if (!user) {
      return
    }
    if (typeof user.email === 'string' && !user.email.endsWith('.invalid')) {
      emailBindingForm.email = user.email
    }
  },
  { immediate: true }
)

watch(
  () => props.compact,
  (value) => {
    if (!value) {
      isEmailFormExpanded.value = true
    }
  },
  { immediate: true }
)

const currentUser = computed(() => localUser.value ?? props.user)
const compact = computed(() => props.compact)
const rowClass = computed(() =>
  props.embedded
    ? compact.value
      ? 'rounded-2xl border border-gray-100 bg-white p-4 shadow-xs dark:border-dark-700 dark:bg-dark-900/40'
      : 'rounded-2xl border border-gray-100 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-900/30'
    : 'px-6 py-5'
)
const emailBound = computed(() => getBindingStatus('email'))
const showEmailForm = computed(() => !compact.value || isEmailFormExpanded.value)
const emailPasswordPlaceholder = computed(() =>
  emailBound.value
    ? t('profile.authBindings.replaceEmailPasswordPlaceholder')
    : t('profile.authBindings.passwordPlaceholder')
)
const emailSubmitActionLabel = computed(() =>
  emailBound.value
    ? t('profile.authBindings.confirmEmailReplaceAction')
    : t('profile.authBindings.confirmEmailBindAction')
)
const legacyBindingNoteKeys: Record<string, string> = {
  'Primary account email is managed from the profile form.':
    'profile.authBindings.notes.emailManagedFromProfile',
  'You can unbind this sign-in method.': 'profile.authBindings.notes.canUnbind',
  'Bind another sign-in method before unbinding.':
    'profile.authBindings.notes.bindAnotherBeforeUnbind',
}

function resolveLegacyCompatibleWeChatSettings(
  settings: WeChatOAuthPublicSettings | null | undefined
): (WeChatOAuthPublicSettings & {
  wechat_oauth_open_enabled: boolean
  wechat_oauth_mp_enabled: boolean
}) | null {
  if (!settings) {
    return null
  }

  if (hasExplicitWeChatOAuthCapabilities(settings)) {
    return settings
  }

  if (typeof settings.wechat_oauth_enabled !== 'boolean') {
    return null
  }

  return {
    ...settings,
    wechat_oauth_open_enabled: settings.wechat_oauth_enabled,
    wechat_oauth_mp_enabled: settings.wechat_oauth_enabled,
  }
}

const wechatOAuthSettings = computed<WeChatOAuthPublicSettings | null>(() => {
  const cachedSettings = resolveLegacyCompatibleWeChatSettings(appStore.cachedPublicSettings)
  if (cachedSettings) {
    return cachedSettings
  }

  return resolveLegacyCompatibleWeChatSettings({
    wechat_oauth_enabled: props.wechatEnabled,
    wechat_oauth_open_enabled: props.wechatOpenEnabled,
    wechat_oauth_mp_enabled: props.wechatMpEnabled,
  })
})

const resolvedWeChatBinding = computed(() => resolveWeChatOAuthStartStrict(wechatOAuthSettings.value))

function normalizeBindingStatus(binding: boolean | UserAuthBindingStatus | undefined): boolean | null {
  if (typeof binding === 'boolean') {
    return binding
  }
  if (!binding) {
    return null
  }
  if (typeof binding.bound === 'boolean') {
    return binding.bound
  }
  return Boolean(binding.issuer || binding.provider_key || binding.subject_hint)
}

function getBindingStatus(provider: UserAuthProvider): boolean {
  return getBindingStatusForUser(currentUser.value, provider)
}

function getBindingStatusForUser(user: User | null | undefined, provider: UserAuthProvider): boolean {
  if (provider === 'email') {
    if (typeof user?.email_bound === 'boolean') {
      return user.email_bound
    }
    const nested = user?.auth_bindings?.email ?? user?.identity_bindings?.email
    const normalized = normalizeBindingStatus(nested)
    return normalized ?? false
  }

  const directFlag = user?.[`${provider}_bound` as keyof User]
  if (typeof directFlag === 'boolean') {
    return directFlag
  }

  const nested = user?.auth_bindings?.[provider] ?? user?.identity_bindings?.[provider]
  const normalized = normalizeBindingStatus(nested)
  return normalized ?? false
}

function getBindingDetails(provider: UserAuthProvider): UserAuthBindingStatus | null {
  const binding = currentUser.value?.auth_bindings?.[provider] ?? currentUser.value?.identity_bindings?.[provider]
  if (!binding || typeof binding === 'boolean') {
    return null
  }
  return binding
}

function getDisplayableEmail(user: User | null | undefined): string {
  const email = user?.email?.trim() || ''
  if (!email) {
    return ''
  }
  if (email.endsWith('.invalid') && !getBindingStatusForUser(user, 'email')) {
    return ''
  }
  return email
}

function isProviderEnabledForBinding(provider: BindableProvider): boolean {
  if (provider === 'github') {
    return props.githubEnabled
  }
  if (provider === 'google') {
    return props.googleEnabled
  }
  if (provider === 'linuxdo') {
    return props.linuxdoEnabled
  }
  if (provider === 'dingtalk') {
    return props.dingtalkEnabled
  }
  if (provider === 'oidc') {
    return props.oidcEnabled
  }
  return resolvedWeChatBinding.value.mode !== null
}

const providerItems = computed(() => [
  {
    provider: 'email' as const,
    label: t('profile.authBindings.providers.email'),
    bound: getBindingStatus('email'),
    canBind: false,
    canUnbind: false,
    details: getBindingDetails('email'),
  },
  {
    provider: 'github' as const,
    label: t('profile.authBindings.providers.github'),
    bound: getBindingStatus('github'),
    canBind:
      !getBindingStatus('github') &&
      isProviderEnabledForBinding('github') &&
      (getBindingDetails('github')?.can_bind ?? true),
    canUnbind: Boolean(getBindingStatus('github') && getBindingDetails('github')?.can_unbind),
    details: getBindingDetails('github'),
  },
  {
    provider: 'google' as const,
    label: t('profile.authBindings.providers.google'),
    bound: getBindingStatus('google'),
    canBind:
      !getBindingStatus('google') &&
      isProviderEnabledForBinding('google') &&
      (getBindingDetails('google')?.can_bind ?? true),
    canUnbind: Boolean(getBindingStatus('google') && getBindingDetails('google')?.can_unbind),
    details: getBindingDetails('google'),
  },
  {
    provider: 'linuxdo' as const,
    label: t('profile.authBindings.providers.linuxdo'),
    bound: getBindingStatus('linuxdo'),
    canBind:
      !getBindingStatus('linuxdo') &&
      isProviderEnabledForBinding('linuxdo') &&
      (getBindingDetails('linuxdo')?.can_bind ?? true),
    canUnbind: Boolean(getBindingStatus('linuxdo') && getBindingDetails('linuxdo')?.can_unbind),
    details: getBindingDetails('linuxdo'),
  },
  {
    provider: 'dingtalk' as const,
    label: t('profile.authBindings.providers.dingtalk'),
    bound: getBindingStatus('dingtalk'),
    canBind:
      !getBindingStatus('dingtalk') &&
      isProviderEnabledForBinding('dingtalk') &&
      (getBindingDetails('dingtalk')?.can_bind ?? true),
    canUnbind: Boolean(getBindingStatus('dingtalk') && getBindingDetails('dingtalk')?.can_unbind),
    details: getBindingDetails('dingtalk'),
  },
  {
    provider: 'oidc' as const,
    label: t('profile.authBindings.providers.oidc', { providerName: props.oidcProviderName }),
    bound: getBindingStatus('oidc'),
    canBind:
      !getBindingStatus('oidc') &&
      isProviderEnabledForBinding('oidc') &&
      (getBindingDetails('oidc')?.can_bind ?? true),
    canUnbind: Boolean(getBindingStatus('oidc') && getBindingDetails('oidc')?.can_unbind),
    details: getBindingDetails('oidc'),
  },
  {
    provider: 'wechat' as const,
    label: t('profile.authBindings.providers.wechat'),
    bound: getBindingStatus('wechat'),
    canBind:
      !getBindingStatus('wechat') &&
      isProviderEnabledForBinding('wechat') &&
      (getBindingDetails('wechat')?.can_bind ?? true),
    canUnbind: Boolean(getBindingStatus('wechat') && getBindingDetails('wechat')?.can_unbind),
    details: getBindingDetails('wechat'),
  },
])

function providerIconClass(provider: UserAuthProvider): string {
  if (provider === 'github') {
    return 'bg-gray-100 text-gray-900 dark:bg-gray-800 dark:text-gray-100'
  }
  if (provider === 'google') {
    return 'bg-white ring-1 ring-inset ring-gray-200 dark:bg-dark-800 dark:ring-dark-700'
  }
  if (provider === 'linuxdo') {
    return 'bg-amber-50 ring-1 ring-inset ring-amber-200/70 dark:bg-amber-950/30 dark:ring-amber-800/40'
  }
  if (provider === 'dingtalk') {
    return 'bg-blue-50 text-[#1677FF] dark:bg-blue-950/40 dark:text-[#40A9FF]'
  }
  if (provider === 'wechat') {
    return 'bg-emerald-50 text-[#07C160] dark:bg-emerald-950/40 dark:text-[#2BD978]'
  }
  if (provider === 'oidc') {
    return 'bg-sky-50 text-sky-600 dark:bg-sky-950/40 dark:text-sky-400'
  }
  return 'bg-primary-100 text-primary-600 dark:bg-primary-900/20 dark:text-primary-300'
}

function providerSummary(provider: UserAuthProvider): string {
  if (provider === 'email') {
    return getDisplayableEmail(currentUser.value)
  }
  return ''
}

function bindingCountLabel(details: UserAuthBindingStatus | null): string {
  if (!details || typeof details.bound_count !== 'number' || details.bound_count <= 1) {
    return ''
  }
  return t('profile.authBindings.boundCount', { count: details.bound_count })
}

function bindingNote(details: UserAuthBindingStatus | null): string {
  if (!details) {
    return ''
  }

  const noteKey = details.note_key?.trim() || legacyBindingNoteKeys[details.note?.trim() || ''] || ''
  if (noteKey) {
    const translated = t(noteKey)
    if (translated !== noteKey) {
      return translated
    }
  }

  return details.note?.trim() || ''
}

function hasBindingDetails(
  provider: UserAuthProvider,
  details: UserAuthBindingStatus | null
): boolean {
  if (!details) {
    return false
  }

  // subject_hint 已不再展示（第三方身份 subject 哈希化后只是盲索引前缀，对用户无意义）。
  const showsProviderIdentityDetails = provider !== 'email' && Boolean(details.display_name)

  return Boolean(showsProviderIdentityDetails || bindingCountLabel(details) || bindingNote(details))
}

function toggleEmailForm(): void {
  isEmailFormExpanded.value = !isEmailFormExpanded.value
}

function startBinding(provider: UserAuthProvider): void {
  if (provider === 'email') {
    return
  }
  startOAuthBinding(provider, {
    redirectTo: route.fullPath || '/profile',
    wechatOAuthSettings: provider === 'wechat' ? wechatOAuthSettings.value : null,
  })
}

function applyUpdatedUser(user: User): void {
  localUser.value = user
  authStore.user = user
}

async function handleUnbind(provider: BindableProvider, providerLabel: string): Promise<void> {
  unbindingProvider.value = provider
  try {
    const user = await unbindAuthIdentity(provider)
    applyUpdatedUser(user)
    appStore.showSuccess(t('profile.authBindings.unbindSuccess', { providerName: providerLabel }))
  } catch (error) {
    appStore.showError((error as { message?: string }).message || t('common.tryAgain'))
  } finally {
    unbindingProvider.value = null
  }
}

function handleUnbindForItem(provider: UserAuthProvider, providerLabel: string): void {
  if (provider === 'email') {
    return
  }
  void handleUnbind(provider, providerLabel)
}

function validateEmailBindingForm(requireCode: boolean): boolean {
  if (!emailBindingForm.email) {
    appStore.showError(t('auth.emailRequired'))
    return false
  }
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(emailBindingForm.email)) {
    appStore.showError(t('auth.invalidEmail'))
    return false
  }
  if (requireCode && !emailBindingForm.verifyCode) {
    appStore.showError(t('auth.codeRequired'))
    return false
  }
  if (requireCode && !emailBindingForm.password) {
    appStore.showError(t('auth.passwordRequired'))
    return false
  }
  if (requireCode && !emailBound.value && emailBindingForm.password.length < 6) {
    appStore.showError(t('auth.passwordMinLength'))
    return false
  }
  return true
}

async function sendEmailCode(): Promise<void> {
  if (!validateEmailBindingForm(false)) {
    return
  }

  isSendingEmailCode.value = true
  try {
    await sendEmailBindingCode(emailBindingForm.email)
    appStore.showSuccess(t('profile.authBindings.codeSentTo', { email: emailBindingForm.email }))
  } catch (error) {
    appStore.showError((error as { message?: string }).message || t('auth.sendCodeFailed'))
  } finally {
    isSendingEmailCode.value = false
  }
}

async function bindEmail(): Promise<void> {
  if (!validateEmailBindingForm(true)) {
    return
  }

  isBindingEmail.value = true
  try {
    const user = await bindEmailIdentity({
      email: emailBindingForm.email,
      verify_code: emailBindingForm.verifyCode,
      password: emailBindingForm.password,
    })
    const replacingBoundEmail = emailBound.value
    applyUpdatedUser(user)
    emailBindingForm.verifyCode = ''
    emailBindingForm.password = ''
    if (compact.value) {
      isEmailFormExpanded.value = false
    }
    appStore.showSuccess(
      replacingBoundEmail
        ? t('profile.authBindings.replaceSuccess')
        : t('profile.authBindings.bindSuccess')
    )
  } catch (error) {
    appStore.showError((error as { message?: string }).message || t('common.tryAgain'))
  } finally {
    isBindingEmail.value = false
  }
}
</script>
