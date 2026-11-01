<template>
  <!-- Settings section of the dashboard template: a title card, then one subtle card with a
       hairline-divided row per sign-in method. Same chrome embedded or standalone. -->
  <div>
    <UPageCard
      :title="t('profile.authBindings.title')"
      :description="t('profile.authBindings.description')"
      variant="naked"
      class="mb-4"
    />

    <UPageCard variant="subtle">
      <div class="divide-y divide-default">
        <div
          v-for="item in providerItems"
          :key="item.provider"
          class="flex flex-col gap-3 py-4 first:pt-0 last:pb-0 sm:flex-row sm:items-start sm:justify-between"
        >
          <div class="flex min-w-0 flex-1 items-start gap-3">
            <UAvatar
              :icon="item.provider === 'email' ? 'i-lucide-mail' : undefined"
              :text="item.provider === 'email' ? undefined : providerInitial(item.provider)"
              size="md"
              class="shrink-0"
            />

            <div class="min-w-0 flex-1 space-y-2">
              <div class="flex flex-wrap items-center gap-2">
                <p class="text-sm font-medium text-highlighted">
                  {{ item.label }}
                </p>
                <UBadge
                  :data-testid="`profile-binding-${item.provider}-status`"
                  :color="item.bound ? 'success' : 'neutral'"
                  variant="subtle"
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
                class="text-sm text-toned"
              >
                {{ providerSummary(item.provider) }}
              </p>

              <div
                v-if="hasBindingDetails(item.provider, item.details)"
                class="grid gap-0.5 text-sm text-muted"
              >
                <p
                  v-if="item.provider !== 'email' && item.details?.display_name"
                  class="font-medium text-toned"
                >
                  {{ item.details.display_name }}
                </p>
                <p v-if="item.provider !== 'email' && item.details?.subject_hint">
                  {{ item.details.subject_hint }}
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
                class="grid gap-2 pt-1 sm:grid-cols-2"
              >
                <UFieldGroup class="sm:col-span-2">
                  <UInput
                    v-model.trim="emailBindingForm.email"
                    data-testid="profile-binding-email-input"
                    type="email"
                    class="flex-1"
                    :placeholder="t('profile.authBindings.emailPlaceholder')"
                    :disabled="isSendingEmailCode || isBindingEmail"
                  />
                  <UButton
                    data-testid="profile-binding-email-send-code"
                    color="neutral"
                    variant="outline"
                    :disabled="isSendingEmailCode || isBindingEmail"
                    @click="sendEmailCode"
                  >
                    {{
                      isSendingEmailCode
                        ? t('common.loading')
                        : t('profile.authBindings.sendCodeAction')
                    }}
                  </UButton>
                </UFieldGroup>
                <UInput
                  v-model.trim="emailBindingForm.verifyCode"
                  data-testid="profile-binding-email-code-input"
                  type="text"
                  inputmode="numeric"
                  maxlength="6"
                  :placeholder="t('profile.authBindings.codePlaceholder')"
                  :disabled="isBindingEmail"
                />
                <UInput
                  v-model="emailBindingForm.password"
                  data-testid="profile-binding-email-password-input"
                  type="password"
                  :placeholder="emailPasswordPlaceholder"
                  :disabled="isBindingEmail"
                />
                <UButton
                  data-testid="profile-binding-email-submit"
                  class="w-fit sm:col-span-2"
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

          <div class="flex shrink-0 flex-wrap items-center gap-2 max-sm:ps-11">
            <UButton
              v-if="item.provider === 'email' && compact"
              data-testid="profile-binding-email-toggle"
              color="neutral"
              variant="outline"
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
              @click="startBinding(item.provider)"
            >
              {{ t('profile.authBindings.bindAction', { providerName: item.label }) }}
            </UButton>
            <UButton
              v-if="item.canUnbind"
              :data-testid="`profile-binding-${item.provider}-unbind`"
              color="neutral"
              variant="outline"
              :disabled="unbindingProvider === item.provider"
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
    </UPageCard>
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
import { useAppStore, useAuthStore } from '@/stores'
import type { User, UserAuthBindingStatus, UserAuthProvider } from '@/types'

type BindableProvider = Exclude<UserAuthProvider, 'email'>

const props = withDefaults(
  defineProps<{
    user: User | null
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
  return Boolean(binding.provider_subject || binding.issuer || binding.provider_key)
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

function providerInitial(provider: UserAuthProvider): string {
  if (provider === 'linuxdo') {
    return 'L'
  }
  if (provider === 'dingtalk') {
    return 'D'
  }
  if (provider === 'wechat') {
    return 'W'
  }
  if (provider === 'oidc') {
    return 'O'
  }
  return 'E'
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

  const showsProviderIdentityDetails =
    provider !== 'email' && Boolean(details.display_name || details.subject_hint)

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
