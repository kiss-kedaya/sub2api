<template>
  <!-- `contents`: each block below is one section of the profile page column and takes
       the page's section gap; the wrappers only keep the test hooks. -->
  <div class="contents">
    <div data-testid="profile-overview-hero" class="flex flex-col gap-4">
      <div class="flex min-w-0 items-center gap-4">
        <UAvatar :src="avatarUrl || undefined" :alt="displayName" :text="avatarInitial" size="3xl" />
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <p class="truncate font-semibold text-highlighted">
              {{ displayName }}
            </p>
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
          <p v-if="primaryEmailDisplay" class="truncate text-sm text-muted">
            {{ primaryEmailDisplay }}
          </p>
        </div>
      </div>

      <UPageGrid class="gap-4 sm:grid-cols-3 sm:gap-px lg:grid-cols-3 lg:gap-px">
        <UPageCard
          v-for="metric in metrics"
          :key="metric.key"
          :data-testid="metric.testId"
          :icon="metric.icon"
          :title="metric.label"
          variant="subtle"
          :ui="metricCardUi"
          class="sm:rounded-none sm:first:rounded-s-lg sm:last:rounded-e-lg"
        >
          <span class="text-xl font-semibold tabular-nums text-highlighted">{{ metric.value }}</span>
        </UPageCard>
      </UPageGrid>
    </div>

    <div data-testid="profile-main-column" class="contents">
      <section data-testid="profile-basics-panel">
        <UPageCard
          :title="t('profile.basicsTitle')"
          :description="t('profile.basicsDescription')"
          variant="naked"
          class="mb-4"
        />
        <UPageCard variant="subtle">
          <ProfileAvatarCard :user="user" embedded />
          <USeparator />
          <ProfileEditForm :initial-username="user?.username || ''" embedded />
        </UPageCard>
      </section>

      <section data-testid="profile-auth-bindings-panel">
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

    <div data-testid="profile-side-column" class="contents">
      <section v-if="sourceHints.length">
        <UPageCard
          :title="t('profile.linkedProfileSources')"
          :description="t('profile.linkedProfileSourcesDescription')"
          variant="naked"
          class="mb-4"
        />
        <UPageCard variant="subtle">
          <ul class="divide-y divide-default">
            <li
              v-for="hint in sourceHints"
              :key="hint.key"
              class="flex items-start gap-3 py-3 text-sm text-default first:pt-0 last:pb-0"
            >
              <UIcon name="i-lucide-link" class="mt-0.5 size-4 shrink-0 text-dimmed" />
              <span>{{ hint.text }}</span>
            </li>
          </ul>
        </UPageCard>
      </section>
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

// Stat cards in the manner of the template's HomeStats.
const metricCardUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
}
const metrics = computed(() => [
  {
    key: 'balance',
    testId: 'profile-overview-metric-balance',
    icon: 'i-lucide-wallet',
    label: t('profile.accountBalance'),
    value: formatCurrency(props.user?.balance || 0)
  },
  {
    key: 'concurrency',
    testId: 'profile-overview-metric-concurrency',
    icon: 'i-lucide-gauge',
    label: t('profile.concurrencyLimit'),
    value: String(props.user?.concurrency || 0)
  },
  {
    key: 'member-since',
    testId: 'profile-overview-metric-member-since',
    icon: 'i-lucide-calendar',
    label: t('profile.memberSince'),
    value: memberSinceLabel.value
  }
])

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