<template>
  <div class="flex flex-col gap-4">
    <PageToolbar>
      <template #left>
        <USelect v-model="channelId" :items="channelItems" class="min-w-48" :aria-label="text('上游', 'Upstream')" />
        <div class="flex items-center gap-1 rounded-lg border border-default p-1">
          <UButton
            v-for="option in tabs"
            :key="option.value"
            size="xs"
            color="neutral"
            :variant="tab === option.value ? 'soft' : 'ghost'"
            :label="option.label"
            @click="tab = option.value"
          />
        </div>
      </template>
      <template #right>
        <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="text('刷新', 'Refresh')" :loading="loading" :disabled="!channelId" @click="reload" />
      </template>
    </PageToolbar>

    <UAlert v-if="!channelId" color="neutral" variant="subtle" icon="i-lucide-info" :title="text('先在「渠道与凭据」里添加一个上游。', 'Add an upstream in Channels first.')" />
    <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
    <UAlert v-if="notice" color="success" variant="subtle" icon="i-lucide-check-circle-2" :title="notice" />

    <UAlert v-if="launch" color="warning" variant="subtle" icon="i-lucide-external-link" :title="text('已生成支付入口，请在有效期内完成支付', 'Payment entry created; complete it before it expires')">
      <div class="mt-1 space-y-1 text-sm">
        <p v-if="launch.pay_url" class="break-all">
          <a :href="launch.pay_url" target="_blank" rel="noopener noreferrer" class="underline">{{ launch.pay_url }}</a>
        </p>
        <p v-if="launch.qr_code" class="break-all text-xs">{{ text('二维码内容', 'QR payload') }}: {{ launch.qr_code }}</p>
        <p v-if="launch.expires_at" class="text-xs text-muted">{{ text('过期时间', 'Expires') }}: {{ formatTime(launch.expires_at) }}</p>
        <UButton size="xs" color="neutral" variant="outline" :label="text('收起', 'Hide')" @click="launch = null" />
      </div>
    </UAlert>

    <section v-if="tab === 'recharge'" class="card p-4">
      <h3 class="font-semibold">{{ text('充值', 'Recharge') }}</h3>
      <p class="mt-1 text-xs text-muted">
        {{ text(`金额范围 ${info?.min_amount ?? '—'} ~ ${info?.max_amount ?? '—'}`, `Amount range ${info?.min_amount ?? '—'} ~ ${info?.max_amount ?? '—'}`) }}
        <span v-if="info?.help_text"> · {{ info.help_text }}</span>
      </p>
      <div class="mt-3 grid gap-3 sm:grid-cols-3">
        <label class="text-xs text-muted">
          {{ text('金额', 'Amount') }}
          <UInput v-model.number="rechargeAmount" type="number" :min="info?.min_amount ?? 0" :max="info?.max_amount ?? undefined" step="0.01" class="mt-1 w-full" />
        </label>
        <label class="text-xs text-muted">
          {{ text('支付方式', 'Payment method') }}
          <USelect v-model="rechargeMethod" :items="rechargeMethodItems" class="mt-1 w-full" />
        </label>
        <label class="mt-6 inline-flex items-center gap-2 text-sm">
          <USwitch v-model="rechargeConfirm" />
          <span>{{ text('我确认这是真实消费', 'I confirm this spends real money') }}</span>
        </label>
      </div>
      <div class="mt-3 flex justify-end">
        <UButton :loading="submitting" :disabled="!rechargeConfirm || !channelId" :label="text('生成支付链接', 'Create payment')" @click="submitRecharge" />
      </div>
      <p class="mt-2 text-xs text-muted">{{ text('这笔钱付给上游站点，用于购买余额；扣的是上游账户，不是我们站内用户余额。', 'This pays the upstream site for its balance; it does not touch our own user balances.') }}</p>
    </section>

    <section v-if="tab === 'redeem'" class="card p-4">
      <h3 class="font-semibold">{{ text('兑换码', 'Redeem code') }}</h3>
      <div class="mt-3 grid gap-3 sm:grid-cols-3">
        <label class="text-xs text-muted sm:col-span-2">
          {{ text('兑换码', 'Code') }}
          <UInput v-model="redeemCode" class="mt-1 w-full" autocomplete="off" />
        </label>
        <label class="mt-6 inline-flex items-center gap-2 text-sm">
          <USwitch v-model="redeemConfirm" />
          <span>{{ text('确认兑换', 'Confirm redeem') }}</span>
        </label>
      </div>
      <div class="mt-3 flex justify-end">
        <UButton :loading="submitting" :disabled="!redeemConfirm || !redeemCode.trim() || !channelId" :label="text('兑换', 'Redeem')" @click="submitRedeem" />
      </div>
      <div v-if="redeemResult" class="mt-3 rounded-lg border border-default p-3 text-sm">
        <p class="font-medium">{{ redeemResult.message || text('兑换成功', 'Redeemed') }}</p>
        <p class="mt-1 text-xs text-muted">
          {{ redeemResult.type }} · {{ redeemResult.value }}
          <span v-if="redeemResult.new_balance !== null && redeemResult.new_balance !== undefined"> · {{ text('新余额', 'new balance') }} {{ redeemResult.new_balance }}</span>
          <span v-if="redeemResult.group_name"> · {{ redeemResult.group_name }}</span>
          <span v-if="redeemResult.validity_days"> · {{ redeemResult.validity_days }} {{ text('天', 'days') }}</span>
        </p>
      </div>
    </section>

    <template v-if="tab === 'subscription'">
      <section class="card p-4">
        <h3 class="font-semibold">{{ text('当前订阅', 'Current subscription') }}</h3>
        <div v-if="usage.length" class="mt-3 space-y-3">
          <article v-for="item in usage" :key="item.id" class="rounded-lg border border-default p-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <p class="font-medium">{{ item.group_name || `#${item.id}` }}</p>
              <UBadge :color="item.status === 'active' ? 'success' : 'neutral'" size="sm" variant="subtle">{{ item.status }}</UBadge>
            </div>
            <p class="mt-1 text-xs text-muted">
              {{ text('到期', 'expires') }}: {{ item.expires_at ? formatTime(item.expires_at) : '—' }}
              <span v-if="item.expires_in_days"> · {{ text('剩', 'in') }} {{ item.expires_in_days }} {{ text('天', 'days') }}</span>
            </p>
            <div class="mt-2 grid gap-2 sm:grid-cols-3">
              <div v-for="window in windowsOf(item)" :key="window.label" class="rounded border border-default p-2 text-xs">
                <p class="text-muted">{{ window.label }}</p>
                <p class="mt-1 tabular-nums">{{ formatMoney(window.value.used_usd) }} / {{ formatMoney(window.value.limit_usd) }}</p>
                <div class="mt-1 h-1.5 overflow-hidden rounded bg-elevated">
                  <div class="h-full bg-primary" :style="{ width: `${Math.min(100, Math.max(0, window.value.percentage))}%` }" />
                </div>
                <p class="mt-1 text-muted">{{ text('剩余', 'left') }} {{ formatMoney(window.value.remaining_usd) }}</p>
              </div>
            </div>
          </article>
        </div>
        <p v-else class="mt-2 text-sm text-muted">{{ text('没有读到订阅，或该上游没有订阅套餐。', 'No subscription found, or this upstream has no plans.') }}</p>
      </section>

      <section class="card p-4">
        <h3 class="font-semibold">{{ text('购买 / 续费', 'Buy / renew') }}</h3>
        <div v-if="subscriptionInfo?.plans?.length" class="mt-3 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <article v-for="plan in subscriptionInfo.plans" :key="plan.id" class="rounded-lg border border-default p-3">
            <p class="font-medium">{{ plan.name }}</p>
            <p class="mt-1 text-xs text-muted">{{ plan.description }}</p>
            <p class="mt-2 text-lg font-semibold tabular-nums">{{ plan.price }} {{ plan.currency }}</p>
            <p class="text-xs text-muted">{{ plan.validity }} · {{ plan.group_name }}</p>
            <ul v-if="plan.features?.length" class="mt-1 list-inside list-disc text-xs text-muted">
              <li v-for="feature in plan.features" :key="feature">{{ feature }}</li>
            </ul>
            <UButton class="mt-2" size="sm" :disabled="!channelId" :label="text('选择', 'Choose')" @click="choosePlan(plan.id)" />
          </article>
        </div>
        <p v-else class="mt-2 text-sm text-muted">{{ text('该上游没有返回可购买套餐。', 'This upstream returned no purchasable plans.') }}</p>

        <div class="mt-4 grid gap-3 sm:grid-cols-3">
          <label class="text-xs text-muted">
            {{ text('套餐', 'Plan') }}
            <USelect v-model="planId" :items="planItems" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            {{ text('支付方式', 'Payment method') }}
            <USelect v-model="subscriptionMethod" :items="subscriptionMethodItems" class="mt-1 w-full" />
          </label>
          <label class="mt-6 inline-flex items-center gap-2 text-sm">
            <USwitch v-model="subscriptionConfirm" />
            <span>{{ text('我确认这是真实消费', 'I confirm this spends real money') }}</span>
          </label>
        </div>
        <div class="mt-3 flex justify-end">
          <UButton :loading="submitting" :disabled="!subscriptionConfirm || !planId || !channelId" :label="text('购买 / 续费', 'Buy / renew')" @click="submitSubscription" />
        </div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import {
  channelRechargeInfo,
  channelSubscriptionInfo,
  channelSubscriptionUsage,
  createChannelRecharge,
  createChannelSubscription,
  listChannels,
  redeemOnChannel,
  type UpstreamChannel,
  type UpstreamPaymentLaunch,
  type UpstreamRechargeInfo,
  type UpstreamRedeemResult,
  type UpstreamSubscriptionInfo,
  type UpstreamSubscriptionUsage,
  type UpstreamSubscriptionWindow,
} from '@/api/admin/upstreamChannel'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

type BillingTab = 'recharge' | 'redeem' | 'subscription'
const tabs = computed<{ label: string; value: BillingTab }[]>(() => [
  { label: text('充值', 'Recharge'), value: 'recharge' },
  { label: text('兑换码', 'Redeem'), value: 'redeem' },
  { label: text('订阅', 'Subscription'), value: 'subscription' },
])
const tab = ref<BillingTab>('recharge')

const channels = ref<UpstreamChannel[]>([])
const channelId = ref<number | null>(null)
const channelItems = ref<{ label: string; value: number | null }[]>([])
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const notice = ref('')
const launch = ref<UpstreamPaymentLaunch | null>(null)

const info = ref<UpstreamRechargeInfo | null>(null)
const rechargeAmount = ref(0)
const rechargeMethod = ref('')
const rechargeConfirm = ref(false)

const redeemCode = ref('')
const redeemConfirm = ref(false)
const redeemResult = ref<UpstreamRedeemResult | null>(null)

const subscriptionInfo = ref<UpstreamSubscriptionInfo | null>(null)
const usage = ref<UpstreamSubscriptionUsage[]>([])
const planId = ref('')
const subscriptionMethod = ref('')
const subscriptionConfirm = ref(false)

const rechargeMethodItems = computed(() =>
  (info.value?.methods ?? []).map(method => ({ label: `${method.name || method.type} (${method.min_amount}~${method.max_amount})`, value: method.type }))
)
const subscriptionMethodItems = computed(() =>
  (subscriptionInfo.value?.methods ?? []).map(method => ({ label: method.name || method.type, value: method.type }))
)
const planItems = computed(() =>
  (subscriptionInfo.value?.plans ?? []).map(plan => ({ label: `${plan.name} · ${plan.price} ${plan.currency}`, value: plan.id }))
)

function formatTime(value?: string | null): string {
  return value ? new Date(value).toLocaleString() : '—'
}
function formatMoney(value?: number | null): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—'
  return value.toFixed(2)
}
function reason(err: unknown, fallback: string): string {
  const candidate = err as { message?: string }
  return candidate?.message || fallback
}
function windowsOf(item: UpstreamSubscriptionUsage): { label: string; value: UpstreamSubscriptionWindow }[] {
  const windows: { label: string; value: UpstreamSubscriptionWindow }[] = []
  if (item.daily) windows.push({ label: text('每日', 'Daily'), value: item.daily })
  if (item.weekly) windows.push({ label: text('每周', 'Weekly'), value: item.weekly })
  if (item.monthly) windows.push({ label: text('每月', 'Monthly'), value: item.monthly })
  return windows
}

async function loadChannels() {
  try {
    const result = await listChannels({ page: 1, pageSize: 100 })
    channels.value = result.items ?? []
    channelItems.value = channels.value.map(channel => ({ label: channel.name, value: channel.id }))
    if (!channelId.value && channels.value.length) channelId.value = channels.value[0].id
  } catch (err) {
    error.value = reason(err, text('读取上游列表失败', 'Failed to load upstreams'))
  }
}

async function reload() {
  if (!channelId.value) return
  loading.value = true
  error.value = ''
  try {
    if (tab.value === 'recharge') {
      info.value = await channelRechargeInfo(channelId.value)
      rechargeAmount.value = info.value.min_amount || 0
      rechargeMethod.value = info.value.methods?.[0]?.type ?? ''
    } else if (tab.value === 'subscription') {
      const [subInfo, subUsage] = await Promise.all([
        channelSubscriptionInfo(channelId.value),
        channelSubscriptionUsage(channelId.value),
      ])
      subscriptionInfo.value = subInfo
      usage.value = subUsage.items ?? []
      subscriptionMethod.value = subInfo.methods?.[0]?.type ?? ''
      if (!planId.value && subInfo.plans?.length) planId.value = subInfo.plans[0].id
    }
  } catch (err) {
    error.value = reason(err, text('读取上游计费信息失败，先确认该上游已登录', 'Failed to load billing info; sign in to the upstream first'))
  } finally {
    loading.value = false
  }
}

function choosePlan(id: string) {
  planId.value = id
  subscriptionConfirm.value = false
}

async function submitRecharge() {
  if (!channelId.value) return
  submitting.value = true
  error.value = ''
  notice.value = ''
  try {
    launch.value = await createChannelRecharge(channelId.value, {
      amount: rechargeAmount.value,
      payment_method: rechargeMethod.value,
      confirm: true,
    })
    notice.value = text('支付入口已生成', 'Payment entry created')
    rechargeConfirm.value = false
  } catch (err) {
    error.value = reason(err, text('创建充值失败', 'Recharge failed'))
  } finally {
    submitting.value = false
  }
}

async function submitRedeem() {
  if (!channelId.value) return
  submitting.value = true
  error.value = ''
  redeemResult.value = null
  try {
    redeemResult.value = await redeemOnChannel(channelId.value, { code: redeemCode.value.trim(), confirm: true })
    notice.value = text('兑换完成', 'Redeemed')
    redeemConfirm.value = false
    redeemCode.value = ''
    if (tab.value === 'subscription') await reload()
  } catch (err) {
    error.value = reason(err, text('兑换失败', 'Redeem failed'))
  } finally {
    submitting.value = false
  }
}

async function submitSubscription() {
  if (!channelId.value) return
  submitting.value = true
  error.value = ''
  notice.value = ''
  try {
    launch.value = await createChannelSubscription(channelId.value, {
      plan_id: planId.value,
      payment_method: subscriptionMethod.value,
      confirm: true,
    })
    notice.value = text('订阅支付入口已生成', 'Subscription payment entry created')
    subscriptionConfirm.value = false
  } catch (err) {
    error.value = reason(err, text('创建订阅失败', 'Subscription purchase failed'))
  } finally {
    submitting.value = false
  }
}

watch(channelId, () => {
  launch.value = null
  redeemResult.value = null
  void reload()
})
watch(tab, () => {
  launch.value = null
  void reload()
})

onMounted(async () => {
  await loadChannels()
  await reload()
})
</script>
