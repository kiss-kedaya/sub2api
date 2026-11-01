<template>
  <UDropdownMenu v-if="user" :items="items" :content="menuContent" :ui="{ content: 'w-60' }">
    <button
      type="button"
      data-test="user-menu"
      :class="variant === 'sidebar' ? ['user-menu-trigger', { 'user-menu-trigger-collapsed': collapsed }] : 'header-user-button flex items-center p-0.5 lg:hidden'"
      :title="collapsed ? displayName : undefined"
      :aria-label="t('common.userMenu')"
    >
      <span class="header-avatar flex h-8 w-8 shrink-0 items-center justify-center overflow-hidden text-xs font-semibold">
        <img v-if="avatarUrl" :src="avatarUrl" :alt="displayName" class="h-full w-full object-cover" />
        <span v-else>{{ userInitials }}</span>
      </span>
      <template v-if="variant === 'sidebar' && !collapsed">
        <span class="user-menu-identity">
          <span class="user-menu-name">{{ displayName }}</span>
          <span class="user-menu-role">{{ t('admin.users.roles.' + user.role) }}</span>
        </span>
        <UIcon name="i-lucide-chevrons-up-down" class="user-menu-chevron size-4 shrink-0" />
      </template>
    </button>

    <template #account>
      <span class="flex min-w-0 flex-col">
        <span class="truncate text-sm font-medium text-highlighted">{{ displayName }}</span>
        <span v-if="user.email" class="truncate text-xs font-normal text-muted">{{ user.email }}</span>
      </span>
    </template>

    <template #account-balance>
      <div class="w-full font-normal">
        <p class="text-xs text-muted">{{ t('common.balance') }}</p>
        <p class="text-sm font-semibold tabular-nums text-primary-600 dark:text-primary-400">{{ formatMoney(availableBalance) }}</p>
        <p v-if="frozenBalance > 0" class="mt-0.5 text-xs tabular-nums text-amber-600 dark:text-amber-300">
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

// One menu for the signed-in user: the sidebar footer on desktop, the header on phones.
const props = withDefaults(defineProps<{ variant?: 'sidebar' | 'header'; collapsed?: boolean }>(), {
  variant: 'sidebar',
  collapsed: false
})

const { t } = useI18n()
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
const avatarUrl = computed(() => user.value?.avatar_url?.trim() || '')
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
    : { side: 'top' as const, align: 'start' as const, sideOffset: 6 }
})

const items = computed<DropdownMenuItem[][]>(() => {
  // Name over email; the trigger right next to the menu already shows the avatar.
  const account: DropdownMenuItem[] = [{ type: 'label', label: displayName.value, slot: 'account' }]
  // Phones have no balance chip in the header, so the header menu shows it.
  if (props.variant === 'header') account.push({ type: 'label', slot: 'account-balance', class: 'sm:hidden' })

  const links: DropdownMenuItem[] = [
    { label: t('nav.profile'), icon: 'i-lucide-user', to: '/profile' },
    { label: t('nav.apiKeys'), icon: 'i-lucide-key-round', to: '/keys' },
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

<style>
/* Sidebar footer trigger: avatar, name and role, like Nuxt UI's dashboard user menu. */
.user-menu-trigger {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 8px;
  padding: 6px;
  border-radius: 6px;
  text-align: left;
  color: var(--signal-nav-text, #404040);
  transition: background-color 150ms ease, color 150ms ease;
}

.user-menu-trigger:hover,
.user-menu-trigger[data-state='open'] {
  background: var(--signal-nav-hover, #f4f4f4);
  color: var(--signal-nav-strong, #171717);
}

.user-menu-trigger:focus-visible {
  outline: 2px solid var(--ui-border-inverted);
  outline-offset: -2px;
}

.user-menu-trigger-collapsed {
  justify-content: center;
}

.user-menu-identity {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  flex-direction: column;
  line-height: 1.25;
}

.user-menu-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 500;
  color: var(--signal-text, #171717);
}

.user-menu-role {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--signal-muted, #737373);
}

.user-menu-chevron {
  color: var(--signal-muted, #737373);
}

/* By day: Nuxt UI's neutral hover instead of the warmer nav tint. */
html:not(.dark) .user-menu-trigger:hover,
html:not(.dark) .user-menu-trigger[data-state='open'] {
  background: #f5f5f5;
  color: #171717;
}
</style>
