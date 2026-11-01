<template>
  <!-- One shared row menu for the whole table: the row's "..." button passes its rect, the menu
       anchors to it (virtual reference), so virtualised rows can unmount without closing it. -->
  <UDropdownMenu
    :open="open"
    :items="items"
    :content="contentProps"
    :ui="{ content: 'action-menu-content w-52' }"
    @update:open="onOpenChange"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'
import type { Account } from '@/types'
import { supportsCustomUsage } from '@/utils/customUsage'

const props = defineProps<{ show: boolean; account: Account | null; anchorRect: DOMRect | null }>()
const emit = defineEmits(['custom-usage', 'close', 'test', 'stats', 'schedule', 'duplicate', 'reauth', 'refresh-token', 'recover-state', 'reset-quota', 'set-privacy', 'create-spark-shadow', 'delete'])
const { t } = useI18n()

const open = computed(() => props.show && !!props.anchorRect && !!props.account)

// Menu placement: below the trigger, right edges aligned; it flips above near the bottom
// and stays 8px inside the viewport (floating-ui collision handling).
const VIEWPORT_PADDING = 8
const anchor = computed(() => {
  const rect = props.anchorRect
  return rect ? { getBoundingClientRect: () => rect } : undefined
})
const contentProps = computed(() => ({
  side: 'bottom' as const,
  align: 'end' as const,
  sideOffset: 4,
  collisionPadding: VIEWPORT_PADDING,
  avoidCollisions: true,
  reference: anchor.value
}))

// Selecting an item or dismissing (Escape, outside click) closes through update:open.
const onOpenChange = (value: boolean) => {
  if (!value && props.show) emit('close')
}

const canDuplicate = computed(() => {
  if (!props.account || props.account.parent_account_id != null) return false
  return ['apikey', 'cloudflare', 'upstream', 'bedrock', 'service_account'].includes(props.account.type)
})
const isRateLimited = computed(() => {
  if (props.account?.rate_limit_reset_at && new Date(props.account.rate_limit_reset_at) > new Date()) {
    return true
  }
  const modelLimits = (props.account?.extra as Record<string, unknown> | undefined)?.model_rate_limits as
    | Record<string, { rate_limit_reset_at: string }>
    | undefined
  if (modelLimits) {
    const now = new Date()
    return Object.values(modelLimits).some(info => new Date(info.rate_limit_reset_at) > now)
  }
  return false
})
const isOverloaded = computed(() => props.account?.overload_until && new Date(props.account.overload_until) > new Date())
const isTempUnschedulable = computed(() => props.account?.temp_unschedulable_until && new Date(props.account.temp_unschedulable_until) > new Date())
const hasRecoverableState = computed(() => {
  return props.account?.status === 'error' || Boolean(isRateLimited.value) || Boolean(isOverloaded.value) || Boolean(isTempUnschedulable.value)
})
const isAntigravityOAuth = computed(() => props.account?.platform === 'antigravity' && props.account?.type === 'oauth')
const isOpenAIOAuth = computed(() => props.account?.platform === 'openai' && props.account?.type === 'oauth')
// 影子账号(链接型,持 parent_account_id)不持凭据、type 不可变,凭据/隐私类操作对其无效。
const isShadow = computed(() => props.account?.parent_account_id != null)
// A "parent" OpenAI OAuth account is one that is NOT itself a shadow (parent_account_id == null)
const isOpenAIOAuthParent = computed(() => isOpenAIOAuth.value && !isShadow.value)
const supportsPrivacy = computed(() => (isAntigravityOAuth.value || isOpenAIOAuth.value) && !isShadow.value)
const hasQuotaLimit = computed(() => {
  return (props.account?.type === 'apikey' || props.account?.type === 'bedrock') && (
    (props.account?.quota_limit ?? 0) > 0 ||
    (props.account?.quota_daily_limit ?? 0) > 0 ||
    (props.account?.quota_weekly_limit ?? 0) > 0
  )
})

type MenuEvent = 'custom-usage' | 'test' | 'stats' | 'schedule' | 'duplicate' | 'reauth' | 'refresh-token' | 'recover-state' | 'reset-quota' | 'set-privacy' | 'create-spark-shadow' | 'delete'
const fire = (event: MenuEvent) => () => {
  if (props.account) emit(event, props.account)
}

const items = computed<DropdownMenuItem[][]>(() => {
  const account = props.account
  if (!account) return []
  const general: DropdownMenuItem[] = [
    { label: t('admin.accounts.testConnection'), icon: 'i-lucide-play', onSelect: fire('test') }
  ]
  if (supportsCustomUsage(account)) {
    general.push({ label: t('admin.accounts.customUsage.title'), icon: 'i-lucide-gauge', onSelect: fire('custom-usage') })
  }
  general.push(
    { label: t('admin.accounts.viewStats'), icon: 'i-lucide-chart-column', onSelect: fire('stats') },
    { label: t('admin.scheduledTests.schedule'), icon: 'i-lucide-calendar-clock', onSelect: fire('schedule') }
  )
  if (canDuplicate.value) {
    general.push({ label: t('admin.accounts.duplicateAccount'), icon: 'i-lucide-copy', onSelect: fire('duplicate') })
  }

  const credentials: DropdownMenuItem[] = []
  // 影子账号不持凭据:重授权/刷新 token 对其无效(后端拒绝),故隐藏(外审 G4)。
  if ((account.type === 'oauth' || account.type === 'setup-token') && !isShadow.value) {
    credentials.push(
      { label: t('admin.accounts.reAuthorize'), icon: 'i-lucide-link', onSelect: fire('reauth') },
      { label: t('admin.accounts.refreshToken'), icon: 'i-lucide-refresh-cw', onSelect: fire('refresh-token') }
    )
  }
  if (isOpenAIOAuthParent.value) {
    credentials.push({ label: t('admin.accounts.createSparkShadow'), icon: 'i-lucide-sparkles', onSelect: fire('create-spark-shadow') })
  }
  if (supportsPrivacy.value) {
    credentials.push({ label: t('admin.accounts.setPrivacy'), icon: 'i-lucide-shield', onSelect: fire('set-privacy') })
  }

  const recovery: DropdownMenuItem[] = []
  if (hasRecoverableState.value) {
    recovery.push({ label: t('admin.accounts.recoverState'), icon: 'i-lucide-heart-pulse', onSelect: fire('recover-state') })
  }
  if (hasQuotaLimit.value) {
    recovery.push({ label: t('admin.accounts.resetQuota'), icon: 'i-lucide-rotate-ccw', onSelect: fire('reset-quota') })
  }

  const danger: DropdownMenuItem[] = [
    { label: t('common.delete'), icon: 'i-lucide-trash-2', color: 'error', onSelect: fire('delete') }
  ]

  return [general, credentials, recovery, danger].filter(group => group.length > 0)
})
</script>
