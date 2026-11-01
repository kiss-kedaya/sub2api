<template>
  <UDropdownMenu
    v-if="user"
    :items="items"
    :content="menuContent"
    :ui="{ content: variant === 'header' || collapsed ? 'w-60' : 'w-(--reka-dropdown-menu-trigger-width) min-w-60' }"
  >
    <!-- Sidebar footer: avatar and name, like the template's UserMenu. -->
    <UButton
      v-if="variant === 'sidebar'"
      data-test="user-menu"
      color="neutral"
      variant="ghost"
      block
      :square="collapsed"
      :avatar="avatar"
      :label="collapsed ? undefined : displayName"
      :trailing-icon="collapsed ? undefined : 'i-lucide-chevrons-up-down'"
      :title="collapsed ? displayName : undefined"
      :aria-label="t('common.userMenu')"
      class="user-menu-trigger data-[state=open]:bg-elevated"
      :ui="{ trailingIcon: 'text-dimmed', label: 'truncate' }"
    />
    <!-- Phones: just the avatar, in the navbar. -->
    <UButton
      v-else
      data-test="user-menu"
      color="neutral"
      variant="ghost"
      square
      :avatar="avatar"
      :aria-label="t('common.userMenu')"
      class="header-user-button lg:hidden"
    />

    <template #account>
      <span class="flex min-w-0 flex-col">
        <span class="flex min-w-0 items-center gap-1.5">
          <span class="truncate text-sm font-medium text-highlighted">{{ displayName }}</span>
          <UBadge color="neutral" variant="subtle" size="sm" class="shrink-0">{{ t('admin.users.roles.' + user.role) }}</UBadge>
        </span>
        <span v-if="user.email" class="truncate text-xs font-normal text-muted">{{ user.email }}</span>
      </span>
    </template>

    <template #account-balance>
      <div class="w-full font-normal">
        <p class="text-xs text-muted">{{ t('common.balance') }}</p>
        <p class="text-sm font-semibold tabular-nums text-highlighted">{{ formatMoney(availableBalance) }}</p>
        <p v-if="frozenBalance > 0" class="mt-0.5 text-xs tabular-nums text-warning">
          {{ t('common.frozenBalance') }} {{ formatMoney(frozenBalance) }}
        </p>
      </div>
    </template>

    <template #contact-label>
      <span class="flex min-w-0 items-center gap-1.5 text-xs font-normal text-muted">
        <UIcon name="i-lucide-headset" class="size-3.5 shrink-0" />
        <span class="shrink-0">{{ t('common.contactSupport') }}:</span>
        <span class="truncate font-medium text-default">{{ contactInfo }}</span>
      </span>
    </template>
  </UDropdownMenu>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'
import { useLocale } from '@nuxt/ui/composables/useLocale'
import { useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import { THEME_MODE_OPTIONS, useSiteColorMode, type ThemeMode } from '@/composables/useSiteTheme'
import { availableLocales, setLocale } from '@/i18n'

// One menu for the signed-in user: the sidebar footer on desktop, the navbar on phones.
const props = withDefaults(defineProps<{ variant?: 'sidebar' | 'header'; collapsed?: boolean }>(), {
  variant: 'sidebar',
  collapsed: false
})

const { t, locale } = useI18n()
const { t: uiT } = useLocale()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const colorMode = useSiteColorMode()

const user = computed(() => authStore.user)
const displayName = computed(() => user.value?.username || user.value?.email?.split('@')[0] || '')
const userInitials = computed(() => {
  const source = user.value?.username || user.value?.email?.split('@')[0] || ''
  return source.substring(0, 2).toUpperCase()
})
const avatar = computed(() => ({
  src: user.value?.avatar_url?.trim() || undefined,
  alt: displayName.value,
  text: userInitials.value
}))
const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const contactInfo = computed(() => appStore.contactInfo)
// The onboarding tour exists for admins in standard mode only.
const showOnboardingButton = computed(() => !authStore.isSimpleMode && user.value?.role === 'admin')

const MODE_ICONS: Record<ThemeMode, string> = {
  system: 'i-lucide-monitor',
  light: 'i-lucide-sun',
  dark: 'i-lucide-moon'
}

const menuContent = computed(() => {
  if (props.variant === 'header') return { side: 'bottom' as const, align: 'end' as const }
  return props.collapsed
    ? { side: 'right' as const, align: 'end' as const, sideOffset: 8 }
    : { align: 'center' as const, collisionPadding: 12 }
})

const items = computed<DropdownMenuItem[][]>(() => {
  const account: DropdownMenuItem[] = [{ type: 'label', label: displayName.value, slot: 'account' }]
  // Phones have no balance chip in the navbar, so the navbar menu shows it.
  if (props.variant === 'header') account.push({ type: 'label', slot: 'account-balance', class: 'sm:hidden' })

  const links: DropdownMenuItem[] = [
    { label: t('nav.profile'), icon: 'i-lucide-user', to: '/profile' },
    { label: t('nav.apiKeys'), icon: 'i-lucide-key-round', to: '/keys' }
  ]

  const preferences: DropdownMenuItem[] = [
    {
      label: uiT('dashboardSearch.theme'),
      icon: MODE_ICONS[colorMode.preference.value],
      children: THEME_MODE_OPTIONS.map((mode) => ({
        type: 'checkbox' as const,
        label: t(`admin.settings.siteTheme.modes.${mode}`),
        icon: MODE_ICONS[mode],
        checked: colorMode.preference.value === mode,
        onSelect: (event: Event) => {
          event.preventDefault()
          colorMode.setPreference(mode)
        }
      }))
    },
    {
      label: t('nav.language'),
      icon: 'i-lucide-languages',
      children: availableLocales.map((option) => ({
        type: 'checkbox' as const,
        label: option.name,
        checked: locale.value === option.code,
        onSelect: () => {
          if (locale.value !== option.code) void setLocale(option.code)
        }
      }))
    }
  ]

  const extras: DropdownMenuItem[] = []
  if (authStore.isAdmin) {
    extras.push({ label: t('nav.github'), icon: 'i-lucide-github', to: 'https://github.com/Wei-Shaw/sub2api', target: '_blank' })
  }
  if (contactInfo.value) extras.push({ type: 'label', slot: 'contact' })
  if (showOnboardingButton.value) {
    extras.push({ label: t('onboarding.restartTour'), icon: 'i-lucide-circle-help', onSelect: () => onboardingStore.replay() })
  }

  return [
    account,
    links,
    preferences,
    ...(extras.length ? [extras] : []),
    [{ label: t('nav.logout'), icon: 'i-lucide-log-out', color: 'error', onSelect: handleLogout }]
  ]
})

async function handleLogout() {
  try {
    await authStore.logout()
  } catch (error) {
    // Ignore logout errors - still redirect to login
    console.error('Logout error:', error)
  }
  await router.push('/login')
}

function formatMoney(value: number) {
  if (!Number.isFinite(value)) return '$0.00'
  return `$${value.toFixed(2)}`
}
</script>
