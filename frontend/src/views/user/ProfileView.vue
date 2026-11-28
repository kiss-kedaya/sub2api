<template>
  <AppLayout>
    <div
      data-testid="profile-shell"
      class="signal-account-page mx-auto max-w-[950px] space-y-6"
    >
      <UserPageHeader flush :title="t('profile.title')" />

      <!-- Horizontal tab groups matching Admin SettingsView -->
      <div
        ref="tabsToolbarRef"
        data-testid="profile-tabs-toolbar"
        class="profile-tabs-toolbar sticky top-0 z-10 flex min-h-12 items-center border-b border-default bg-(--app-page-bg)/90 px-1 backdrop-blur-md"
      >
        <UTabs
          :model-value="activeTab"
          :items="profileTabItems"
          :content="false"
          variant="link"
          size="sm"
          class="min-w-0 flex-1"
          :ui="{
            list: 'overflow-x-auto overflow-y-hidden border-b-0 p-0 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
            trigger: 'shrink-0 py-3',
            indicator: 'bottom-0',
          }"
          @update:model-value="selectProfileTab($event as ProfileTab)"
        >
          <template #default="{ item }">
            <span :id="`profile-tab-${item.value}`">{{ item.label }}</span>
          </template>
        </UTabs>
      </div>

      <!-- Tab: Profile Basics -->
      <div
        v-show="activeTab === 'profile'"
        data-testid="profile-tab-panel-profile"
        class="space-y-6"
      >
        <ProfileInfoCard
          section="profile"
          :user="user"
          :github-enabled="githubOAuthEnabled"
          :google-enabled="googleOAuthEnabled"
          :linuxdo-enabled="linuxdoOAuthEnabled"
          :dingtalk-enabled="dingtalkOAuthEnabled"
          :oidc-enabled="oidcOAuthEnabled"
          :oidc-provider-name="oidcOAuthProviderName"
          :wechat-enabled="wechatOAuthEnabled"
          :wechat-open-enabled="wechatOAuthOpenEnabled"
          :wechat-mp-enabled="wechatOAuthMPEnabled"
        />

        <ProfilePersonalApiKeyCard />

        <UAlert
          v-if="contactInfo"
          color="neutral"
          variant="outline"
          icon="i-lucide-message-circle"
          class="shadow-xs dark:shadow-none"
          :title="t('common.contactSupport')"
          :description="contactInfo"
          :ui="{ description: 'font-medium break-all' }"
        />
      </div>

      <!-- Tab: Sign-In Methods & Bindings -->
      <div
        v-show="activeTab === 'bindings'"
        data-testid="profile-tab-panel-bindings"
        class="space-y-6"
      >
        <ProfileInfoCard
          section="bindings"
          :user="user"
          :github-enabled="githubOAuthEnabled"
          :google-enabled="googleOAuthEnabled"
          :linuxdo-enabled="linuxdoOAuthEnabled"
          :dingtalk-enabled="dingtalkOAuthEnabled"
          :oidc-enabled="oidcOAuthEnabled"
          :oidc-provider-name="oidcOAuthProviderName"
          :wechat-enabled="wechatOAuthEnabled"
          :wechat-open-enabled="wechatOAuthOpenEnabled"
          :wechat-mp-enabled="wechatOAuthMPEnabled"
        />
      </div>

      <!-- Tab: Account Security -->
      <div
        v-show="activeTab === 'security'"
        data-testid="profile-tab-panel-security"
        class="space-y-6"
      >
        <ProfilePasswordForm />
        <TwoFactorSuggestionBanner storage-key="profile" />
        <ProfileTotpCard />
        <ProfilePasskeyCard :enabled="passkeyEnabled" />
        <ProfileKeyVerificationCard />
      </div>

      <!-- Tab: Notifications -->
      <div
        v-show="activeTab === 'notifications'"
        data-testid="profile-tab-panel-notifications"
        class="space-y-6"
      >
        <ProfileBalanceNotifyCard
          v-if="user && balanceLowNotifyEnabled"
          :enabled="user.balance_notify_enabled ?? true"
          :threshold="user.balance_notify_threshold"
          :extra-emails="user.balance_notify_extra_emails ?? []"
          :system-default-threshold="systemDefaultThreshold"
          :user-email="user.email"
        />
        <ProfileBarkCard />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, getCurrentInstance, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TabsItem } from '@nuxt/ui'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import ProfileBalanceNotifyCard from '@/components/user/profile/ProfileBalanceNotifyCard.vue'
import ProfileInfoCard from '@/components/user/profile/ProfileInfoCard.vue'
import ProfilePasswordForm from '@/components/user/profile/ProfilePasswordForm.vue'
import ProfileTotpCard from '@/components/user/profile/ProfileTotpCard.vue'
import TwoFactorSuggestionBanner from '@/components/user/TwoFactorSuggestionBanner.vue'
import ProfilePasskeyCard from '@/components/user/profile/ProfilePasskeyCard.vue'
import ProfileKeyVerificationCard from '@/components/user/profile/ProfileKeyVerificationCard.vue'
import ProfilePersonalApiKeyCard from '@/components/user/profile/ProfilePersonalApiKeyCard.vue'
import ProfileBarkCard from '@/components/user/profile/ProfileBarkCard.vue'
import { isWeChatWebOAuthEnabled } from '@/api/auth'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

type ProfileTab = 'profile' | 'bindings' | 'security' | 'notifications'

const profileTabs: ReadonlyArray<{ key: ProfileTab; icon: string }> = [
  { key: 'profile', icon: 'i-lucide-user' },
  { key: 'bindings', icon: 'i-lucide-link-2' },
  { key: 'security', icon: 'i-lucide-shield-check' },
  { key: 'notifications', icon: 'i-lucide-bell' },
]
const knownProfileTabs = new Set<ProfileTab>(profileTabs.map((tab) => tab.key))

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const user = computed(() => authStore.user)
const activeTab = ref<ProfileTab>('profile')
const tabsToolbarRef = ref<HTMLElement | null>(null)

function scrollActiveTabIntoView(): void {
  const activeTrigger = tabsToolbarRef.value?.querySelector<HTMLElement>(
    '[role="tab"][data-state="active"], [role="tab"][aria-selected="true"]',
  )
  if (activeTrigger && typeof activeTrigger.scrollIntoView === 'function') {
    activeTrigger.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }
}

const profileTabItems = computed<TabsItem[]>(() =>
  profileTabs.map((tab) => ({
    label: t(`profile.tabs.${tab.key}`),
    icon: tab.icon,
    value: tab.key,
  })),
)

// Deep links (/profile?tab=security) and query sync; stays a no-op when tests mount without vue-router.
const instance = getCurrentInstance()

function currentRoute():
  | { query?: Record<string, unknown>; fullPath?: string }
  | undefined {
  return instance?.appContext.config.globalProperties.$route as
    | { query?: Record<string, unknown>; fullPath?: string }
    | undefined
}

function currentRouter():
  | { replace?: (location: { query: Record<string, unknown> }) => unknown }
  | undefined {
  return instance?.appContext.config.globalProperties.$router as
    | { replace?: (location: { query: Record<string, unknown> }) => unknown }
    | undefined
}

function syncTabFromRoute(): void {
  const route = currentRoute()
  if (!route) return
  const queryTab = route.query?.tab
  const rawTab = typeof queryTab === 'string'
    ? queryTab
    : Array.isArray(queryTab) && typeof queryTab[0] === 'string'
      ? queryTab[0]
      : ''
  activeTab.value = knownProfileTabs.has(rawTab as ProfileTab)
    ? (rawTab as ProfileTab)
    : 'profile'
}

function selectProfileTab(tab: ProfileTab): void {
  if (!knownProfileTabs.has(tab)) return
  activeTab.value = tab
  const route = currentRoute()
  const router = currentRouter()
  if (!route || !router?.replace) return
  const nextQuery: Record<string, unknown> = { ...(route.query ?? {}) }
  if (tab === 'profile') {
    delete nextQuery.tab
  } else {
    nextQuery.tab = tab
  }
  const currentTabQuery = typeof route.query?.tab === 'string' ? route.query.tab : ''
  const nextTabQuery = typeof nextQuery.tab === 'string' ? nextQuery.tab : ''
  if (currentTabQuery !== nextTabQuery) {
    void router.replace({ query: nextQuery })
  }
}

syncTabFromRoute()
watch(
  () => currentRoute()?.fullPath,
  () => syncTabFromRoute(),
)
watch(
  activeTab,
  async () => {
    await nextTick()
    scrollActiveTabIntoView()
  },
  { flush: 'post' },
)

const contactInfo = ref('')
const balanceLowNotifyEnabled = ref(false)
const systemDefaultThreshold = ref(0)
const githubOAuthEnabled = ref(false)
const googleOAuthEnabled = ref(false)
const linuxdoOAuthEnabled = ref(false)
const dingtalkOAuthEnabled = ref(false)
const wechatOAuthEnabled = ref(false)
const wechatOAuthOpenEnabled = ref<boolean | undefined>(undefined)
const wechatOAuthMPEnabled = ref<boolean | undefined>(undefined)
const oidcOAuthEnabled = ref(false)
const oidcOAuthProviderName = ref('OIDC')
const passkeyEnabled = ref(false)

onMounted(async () => {
  syncTabFromRoute()
  await nextTick()
  scrollActiveTabIntoView()

  const profileRefresh = authStore.refreshUser({ force: true }).catch((error) => {
    console.error('Failed to refresh profile:', error)
  })

  const settingsLoad = appStore.fetchPublicSettings()
    .then((settings) => {
      if (!settings) {
        return
      }
      contactInfo.value = settings.contact_info || ''
      balanceLowNotifyEnabled.value = settings.balance_low_notify_enabled ?? false
      systemDefaultThreshold.value = settings.balance_low_notify_threshold ?? 0
      githubOAuthEnabled.value = settings.github_oauth_enabled ?? false
      googleOAuthEnabled.value = settings.google_oauth_enabled ?? false
      linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled ?? false
      dingtalkOAuthEnabled.value = settings.dingtalk_oauth_enabled ?? false
      wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
      wechatOAuthOpenEnabled.value = typeof settings.wechat_oauth_open_enabled === 'boolean'
        ? settings.wechat_oauth_open_enabled
        : undefined
      wechatOAuthMPEnabled.value = typeof settings.wechat_oauth_mp_enabled === 'boolean'
        ? settings.wechat_oauth_mp_enabled
        : undefined
      oidcOAuthEnabled.value = settings.oidc_oauth_enabled ?? false
      oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
      passkeyEnabled.value = settings.passkey_enabled === true
    })
    .catch((error) => {
      console.error('Failed to load settings:', error)
    })

  await Promise.all([profileRefresh, settingsLoad])
})
</script>
