<template>
  <div class="space-y-6">
    <section
      data-testid="profile-overview-hero"
      class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none"
    >
      <div class="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:p-6">
        <div class="flex size-14 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-elevated text-xl font-semibold text-highlighted ring ring-default">
          <img
            v-if="avatarUrl"
            :src="avatarUrl"
            :alt="displayName"
            class="h-full w-full object-cover"
          >
          <span v-else>{{ avatarInitial }}</span>
        </div>

        <div class="min-w-0 flex-1 space-y-1.5">
          <div class="flex flex-wrap items-center gap-2">
            <h2 class="truncate text-lg font-semibold text-highlighted">
              {{ displayName }}
            </h2>
            <UBadge :color="user?.role === 'admin' ? 'primary' : 'neutral'" variant="subtle">
              {{ user?.role === 'admin' ? t('profile.administrator') : t('profile.user') }}
            </UBadge>
            <UBadge :color="user?.status === 'active' ? 'success' : 'error'" variant="subtle">
              {{
                user?.status === 'active'
                  ? t('common.active')
                  : t('common.disabled')
              }}
            </UBadge>
          </div>
          <p class="truncate text-sm text-muted">
            {{ primaryEmailDisplay }}
          </p>
          <div
            v-if="sourceHints.length"
            class="flex flex-wrap gap-1.5 pt-1"
          >
            <UBadge
              v-for="hint in sourceHints"
              :key="hint.key"
              color="neutral"
              variant="outline"
              icon="i-lucide-link"
            >
              {{ hint.text }}
            </UBadge>
          </div>
        </div>
      </div>

      <div class="grid grid-cols-1 divide-y divide-default border-t border-default sm:grid-cols-3 sm:divide-x sm:divide-y-0">
        <div data-testid="profile-overview-metric-balance" class="px-5 py-4 sm:px-6">
          <p class="text-xs font-medium text-muted">
            {{ t('profile.accountBalance') }}
          </p>
          <p class="mt-1 text-lg font-semibold tabular-nums text-highlighted">
            {{ formatCurrency(user?.balance || 0) }}
          </p>
        </div>
        <div data-testid="profile-overview-metric-concurrency" class="px-5 py-4 sm:px-6">
          <p class="text-xs font-medium text-muted">
            {{ t('profile.concurrencyLimit') }}
          </p>
          <p class="mt-1 text-lg font-semibold tabular-nums text-highlighted">
            {{ user?.concurrency || 0 }}
          </p>
        </div>
        <div data-testid="profile-overview-metric-member-since" class="px-5 py-4 sm:px-6">
          <p class="text-xs font-medium text-muted">
            {{ t('profile.memberSince') }}
          </p>
          <p class="mt-1 text-lg font-semibold tabular-nums text-highlighted">
            {{ memberSinceLabel }}
          </p>
        </div>
      </div>
    </section>

    <div class="space-y-6">
      <div data-testid="profile-main-column" class="space-y-6">
        <section
          data-testid="profile-basics-panel"
          class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none"
        >
          <header class="border-b border-default px-5 py-4 sm:px-6">
            <h3 class="text-base font-semibold text-highlighted">
              {{ t('profile.basicsTitle') }}
            </h3>
            <p class="mt-1 text-sm text-muted">
              {{ t('profile.basicsDescription') }}
            </p>
          </header>

          <div class="grid divide-y divide-default md:grid-cols-2 md:divide-x md:divide-y-0">
            <div class="p-5 sm:p-6">
              <ProfileAvatarCard
                :user="user"
                embedded
              />
            </div>

            <div class="p-5 sm:p-6">
              <ProfileEditForm
                :initial-username="user?.username || ''"
                embedded
              />
            </div>
          </div>
        </section>

        <section
          data-testid="profile-auth-bindings-panel"
          class="profile-bindings-panel rounded-lg bg-default p-5 shadow-xs ring ring-default sm:p-6 dark:shadow-none"
        >
          <ProfileIdentityBindingsSection
            :user="user"
            :linuxdo-enabled="linuxdoEnabled"
            :dingtalk-enabled="dingtalkEnabled"
            :oidc-enabled="oidcEnabled"
            :oidc-provider-name="oidcProviderName"
            :wechat-enabled="wechatEnabled"
            :wechat-open-enabled="wechatOpenEnabled"
            :wechat-mp-enabled="wechatMpEnabled"
            embedded
            compact
          />
        </section>
      </div>

      <div data-testid="profile-side-column" class="space-y-6">
        <section
          v-if="sourceHints.length"
          class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none"
        >
          <header class="border-b border-default px-5 py-4 sm:px-6">
            <h3 class="text-base font-semibold text-highlighted">
              {{ t('profile.linkedProfileSources') }}
            </h3>
            <p class="mt-1 text-sm text-muted">
              {{ t('profile.linkedProfileSourcesDescription') }}
            </p>
          </header>

          <ul class="divide-y divide-default">
            <li
              v-for="hint in sourceHints"
              :key="hint.key"
              class="flex items-start gap-3 px-5 py-3 text-sm text-default sm:px-6"
            >
              <UIcon name="i-lucide-link" class="mt-0.5 size-4 shrink-0 text-dimmed" />
              <span>{{ hint.text }}</span>
            </li>
          </ul>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ProfileAvatarCard from '@/components/user/profile/ProfileAvatarCard.vue'
import ProfileEditForm from '@/components/user/profile/ProfileEditForm.vue'
import ProfileIdentityBindingsSection from '@/components/user/profile/ProfileIdentityBindingsSection.vue'
import type { User, UserAuthBindingStatus, UserAuthProvider, UserProfileSourceContext } from '@/types'

const props = withDefaults(defineProps<{
  user: User | null
  linuxdoEnabled?: boolean
  dingtalkEnabled?: boolean
  oidcEnabled?: boolean
  oidcProviderName?: string
  wechatEnabled?: boolean
  wechatOpenEnabled?: boolean
  wechatMpEnabled?: boolean
}>(), {
  linuxdoEnabled: false,
  dingtalkEnabled: false,
  oidcEnabled: false,
  oidcProviderName: 'OIDC',
  wechatEnabled: false,
  wechatOpenEnabled: undefined,
  wechatMpEnabled: undefined,
})

const { t } = useI18n()

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

function isEmailBound(user: User | null | undefined): boolean {
  if (typeof user?.email_bound === 'boolean') {
    return user.email_bound
  }

  const nested = user?.auth_bindings?.email ?? user?.identity_bindings?.email
  const normalized = normalizeBindingStatus(nested)
  return normalized ?? false
}

const avatarUrl = computed(() => props.user?.avatar_url?.trim() || '')
const displayName = computed(() => props.user?.username?.trim() || props.user?.email?.trim() || t('profile.user'))
const primaryEmailDisplay = computed(() => {
  const email = props.user?.email?.trim() || ''
  if (!email) {
    return ''
  }
  if (email.endsWith('.invalid') && !isEmailBound(props.user)) {
    return ''
  }
  return email
})
const avatarInitial = computed(() => displayName.value.charAt(0).toUpperCase() || 'U')
const memberSinceLabel = computed(() => {
  const raw = props.user?.created_at?.trim()
  if (!raw) {
    return '-'
  }

  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) {
    return '-'
  }

  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: 'short',
  }).format(date)
})

const providerLabels = computed<Record<UserAuthProvider, string>>(() => ({
  email: t('profile.authBindings.providers.email'),
  linuxdo: t('profile.authBindings.providers.linuxdo'),
  dingtalk: t('profile.authBindings.providers.dingtalk'),
  oidc: t('profile.authBindings.providers.oidc', { providerName: props.oidcProviderName }),
  wechat: t('profile.authBindings.providers.wechat'),
  github: 'GitHub',
  google: 'Google'
}))

function formatCurrency(value: number): string {
  return `$${value.toFixed(2)}`
}

function normalizeProvider(value: string): UserAuthProvider | null {
  const normalized = value.trim().toLowerCase()
  if (
    normalized === 'email' ||
    normalized === 'linuxdo' ||
    normalized === 'wechat' ||
    normalized === 'github' ||
    normalized === 'google'
  ) {
    return normalized
  }
  if (normalized === 'oidc' || normalized.startsWith('oidc:') || normalized.startsWith('oidc/')) {
    return 'oidc'
  }
  return null
}

function readObjectString(source: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = source[key]
    if (typeof value === 'string' && value.trim()) {
      return value.trim()
    }
  }
  return ''
}

function resolveThirdPartySource(
  rawSource: string | UserProfileSourceContext | null | undefined
): { provider: UserAuthProvider; label: string } | null {
  if (!rawSource) {
    return null
  }

  if (typeof rawSource === 'string') {
    const provider = normalizeProvider(rawSource)
    if (!provider || provider === 'email') {
      return null
    }
    return {
      provider,
      label: providerLabels.value[provider]
    }
  }

  const sourceRecord = rawSource as Record<string, unknown>
  const provider = normalizeProvider(
    readObjectString(sourceRecord, 'provider', 'source', 'provider_type', 'auth_provider')
  )
  if (!provider || provider === 'email') {
    return null
  }

  const explicitLabel = readObjectString(
    sourceRecord,
    'provider_label',
    'label',
    'provider_name',
    'providerName'
  )

  return {
    provider,
    label: explicitLabel || providerLabels.value[provider]
  }
}

const sourceHints = computed(() => {
  const currentUser = props.user
  if (!currentUser) {
    return []
  }

  const hints: Array<{ key: string; text: string }> = []
  const avatarSource = resolveThirdPartySource(
    currentUser.profile_sources?.avatar ?? currentUser.avatar_source
  )
  const usernameSource = resolveThirdPartySource(
    currentUser.profile_sources?.username ??
      currentUser.profile_sources?.display_name ??
      currentUser.profile_sources?.nickname ??
      currentUser.display_name_source ??
      currentUser.username_source ??
      currentUser.nickname_source
  )

  if (avatarSource) {
    hints.push({
      key: 'avatar',
      text: t('profile.authBindings.source.avatar', { providerName: avatarSource.label })
    })
  }

  if (usernameSource) {
    hints.push({
      key: 'username',
      text: t('profile.authBindings.source.username', { providerName: usernameSource.label })
    })
  }

  return hints
})
</script>

<style scoped>
/* Login methods inside the bindings card: hairline-divided rows, not nested cards. */
.profile-bindings-panel :deep(.space-y-4 > .rounded-2xl.border) { margin-top: 0; padding: 16px 0 0; border-width: 1px 0 0; border-radius: 0; background: transparent; box-shadow: none; }
.profile-bindings-panel :deep(.space-y-4 > .rounded-2xl.border:not(:last-child)) { padding-bottom: 16px; }
.profile-bindings-panel :deep(.space-y-4 > div:first-child) { margin-bottom: 16px; }
.profile-bindings-panel :deep(.space-y-4 > div:first-child > p:first-child) { font-size: 16px; line-height: 24px; }
.profile-bindings-panel :deep(.rounded-2xl.h-11.w-11) { width: 36px; height: 36px; border-radius: 8px; }
</style>
