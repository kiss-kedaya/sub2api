<template>
  <AppLayout>
    <div :class="['signal-payment mx-auto w-full', activeTab === 'rechargeCenter' ? 'max-w-[1440px]' : 'max-w-5xl']">
      <div v-if="loading" class="flex items-center justify-center py-20">
        <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>
      <template v-else>
        <header v-if="paymentPhase === 'select' && !selectedPlan" class="signal-payment-heading">
          <div class="signal-payment-heading__icon" aria-hidden="true">
            <Icon name="creditCard" size="lg" />
          </div>
          <div class="min-w-0">
            <p class="signal-payment-heading__index" aria-hidden="true">04 / BILLING</p>
            <h1>{{ t('nav.buySubscription') }}</h1>
          </div>
          <RouterLink to="/orders" class="signal-payment-orders btn btn-secondary">
            <Icon name="clock" size="sm" />{{ t('payment.orders.title') }}
          </RouterLink>
        </header>
        <!-- Tab Switcher (hide during payment and subscription confirm) -->
        <ConsoleTabs v-if="tabs.length > 1 && paymentPhase === 'select' && !selectedPlan"
          id="payment-view" class="signal-payment-tabs" :label="t('payment.title')"
          :model-value="activeTab" :items="tabs"
          @update:model-value="activeTab = $event === 'rechargeCenter' ? 'rechargeCenter' : 'recharge'"
        />
        <!-- Payment in progress (shared by recharge and subscription) -->
        <template v-if="paymentPhase === 'paying'">
          <PaymentStatusPanel
            :order-id="paymentState.orderId"
            :amount="paymentState.amount"
            :pay-amount="paymentState.payAmount"
            :qr-code="paymentState.qrCode"
            :expires-at="paymentState.expiresAt"
            :payment-type="paymentState.paymentType"
            :pay-url="paymentState.payUrl"
            :order-type="paymentState.orderType"
            :currency="paymentState.currency || selectedCurrency"
            :out-trade-no="paymentState.outTradeNo"
            :mobile-alipay-deep-link="paymentState.alipayMobilePrecreateDeepLink"
            @done="onPaymentDone"
            @success="onPaymentSuccess"
            @settled="onPaymentSettled"
          />
        </template>
        <!-- Tab content (select phase) -->
        <template v-else>
          <!-- Top-up Tab -->
          <section v-if="activeTab === 'recharge'" id="payment-view-panel-recharge"
            class="signal-payment-flow" role="tabpanel" :aria-labelledby="tabs.length > 1 ? 'payment-view-tab-recharge' : undefined" :aria-label="tabs.length <= 1 ? t('payment.tabTopUp') : undefined" tabindex="0">
            <div v-if="checkout.balance_disabled || enabledMethods.length === 0" class="signal-empty-state">
              <p class="text-gray-500 dark:text-gray-400">{{ t('payment.notAvailable') }}</p>
            </div>
            <div v-else class="signal-recharge-layout">
              <div class="signal-recharge-fields">
                <section class="signal-payment-section">
                  <h2 class="signal-payment-step"><span aria-hidden="true">01</span>{{ t('payment.amountLabel') }}</h2>
                  <AmountInput
                    v-model="amount"
                    :amounts="[10, 20, 50, 100, 200, 500, 1000, 2000, 5000]"
                    :min="globalMinAmount"
                    :max="globalMaxAmount"
                    :currency="selectedCurrency"
                  />
                  <p v-if="amountError" class="signal-payment-warning">{{ amountError }}</p>
                </section>
                <section v-if="enabledMethods.length >= 1" class="signal-payment-section">
                  <h2 class="signal-payment-step"><span aria-hidden="true">02</span>{{ t('payment.paymentMethod') }}</h2>
                  <PaymentMethodSelector
                    :methods="methodOptions"
                    :selected="selectedMethod"
                    @select="selectedMethod = $event"
                  />
                  <div v-if="enabledMethods.includes('epusdt')" class="signal-exchange-links" :aria-label="t('payment.usdtExchangeAccounts')">
                    <a href="https://www.mitnpkwxvfr.net/join/4274122" target="_blank" rel="noopener noreferrer" class="signal-exchange-link">
                      {{ t('payment.registerOkx') }}<Icon name="externalLink" size="xs" aria-hidden="true" />
                    </a>
                    <a href="https://www.bsmkweb.cc/register?ref=TXUH99P0" target="_blank" rel="noopener noreferrer" class="signal-exchange-link">
                      {{ t('payment.registerBinance') }}<Icon name="externalLink" size="xs" aria-hidden="true" />
                    </a>
                    <span>{{ t('payment.exchangeNetworkHint') }}</span>
                  </div>
                </section>
              </div>
              <aside class="signal-checkout-summary">
                <div class="signal-account-strip">
                  <div class="min-w-0">
                    <p class="signal-kicker">{{ t('payment.rechargeAccount') }}</p>
                    <p class="signal-account-name">{{ user?.username || '' }}</p>
                  </div>
                  <div class="signal-balance-block">
                    <span>{{ t('payment.currentBalance') }}</span>
                    <strong>${{ user?.balance?.toFixed(2) || '0.00' }}</strong>
                  </div>
                </div>
                <div class="signal-checkout-summary__header">
                  <span>{{ t('payment.actualPay') }}</span>
                  <strong>{{ formatSelectedPaymentAmount(totalAmount) }}</strong>
                </div>
                <div class="signal-checkout-summary__rows">
                  <div v-if="selectedMethod"><span>{{ t('payment.paymentMethod') }}</span><strong>{{ selectedMethodLabel }}</strong></div>
                  <div>
                    <span>{{ t('payment.paymentAmount') }}</span>
                    <strong>{{ formatSelectedPaymentAmount(validAmount) }}</strong>
                  </div>
                  <div v-if="feeRate > 0">
                    <span>{{ t('payment.fee') }} ({{ feeRate }}%)</span>
                    <strong>{{ formatSelectedPaymentAmount(feeAmount) }}</strong>
                  </div>
                  <div v-if="balanceRechargeMultiplier !== 1">
                    <span>{{ t('payment.creditedBalance') }}</span>
                    <strong>${{ creditedAmount.toFixed(2) }}</strong>
                  </div>
                </div>
                <p v-if="balanceRechargeMultiplier !== 1" class="signal-rate-note">
                  {{ t('payment.rechargeRatePreview', { currency: selectedCurrency, usd: balanceRechargeMultiplier.toFixed(2) }) }}
                </p>
                <div class="signal-checkout-action">
                <span class="signal-checkout-mobile-total"><small>{{ t('payment.actualPay') }}</small><strong>{{ formatSelectedPaymentAmount(totalAmount) }}</strong></span>
                <button :class="['btn w-full py-3 text-base font-medium', paymentButtonClass]" :disabled="!canSubmit || submitting" @click="handleSubmitRecharge">
                  <span v-if="submitting" class="flex items-center justify-center gap-2">
                    <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent"></span>
                    {{ t('common.processing') }}
                  </span>
                  <span v-else>{{ t('payment.createOrder') }} <span class="signal-checkout-button-amount">{{ formatSelectedPaymentAmount(totalAmount) }}</span></span>
                </button>
                </div>
              </aside>
            </div>
          </section>
          <!-- Recharge Center Tab: reuse the configured custom payment center instead of subscriptions -->
          <section v-else-if="activeTab === 'rechargeCenter' && rechargeCenterEnabled" id="payment-view-panel-rechargeCenter"
            role="tabpanel" :aria-labelledby="tabs.length > 1 ? 'payment-view-tab-rechargeCenter' : undefined" :aria-label="tabs.length <= 1 ? t('payment.rechargeCenterTitle') : undefined" tabindex="0">
            <div ref="rechargeCenterFrameRef" class="recharge-center-shell">
              <div class="recharge-center-toolbar">
                <div>
                  <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.rechargeCenterTitle') }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.rechargeCenterDescription') }}</p>
                  <p class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ t('payment.rechargeCenterNetworkHint') }}</p>
                </div>
                <div v-if="rechargeCenterUrl" class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    class="signal-tool-button"
                    :aria-label="isRechargeCenterFullscreen ? t('payment.rechargeCenterExitFullscreen') : t('payment.rechargeCenterFullscreen')"
                    @click="toggleRechargeCenterFullscreen"
                  >
                    <Icon :name="isRechargeCenterFullscreen ? 'x' : 'arrowsUpDown'" size="sm" aria-hidden="true" />
                    <span>{{ isRechargeCenterFullscreen ? t('payment.rechargeCenterExitFullscreen') : t('payment.rechargeCenterFullscreen') }}</span>
                  </button>
                  <a
                    :href="rechargeCenterUrl"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="signal-tool-button"
                  >
                    <Icon name="externalLink" size="sm" aria-hidden="true" />
                    {{ t('payment.rechargeCenterOpen') }}
                  </a>
                </div>
              </div>
              <div v-if="rechargeCenterUrl && rechargeCenterFailed" class="recharge-center-fallback" role="status">
                <p class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.rechargeCenterNetworkTitle') }}</p>
                <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('payment.rechargeCenterNetworkBody') }}</p>
                <a
                  :href="rechargeCenterUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="signal-tool-button mt-4"
                >
                  <Icon name="externalLink" size="sm" aria-hidden="true" />
                  {{ t('payment.rechargeCenterOpen') }}
                </a>
              </div>
              <div v-if="rechargeCenterUrl" class="recharge-center-frame">
                <iframe
                  :key="rechargeCenterUrl"
                  ref="rechargeCenterIframeRef"
                  :src="rechargeCenterUrl"
                  title="Recharge Center"
                  class="h-[clamp(720px,calc(100dvh-210px),1080px)] min-h-[720px] w-full border-0 bg-white"
                  allow="payment *; clipboard-write"
                  allowfullscreen
                  @load="onRechargeCenterLoad"
                  @error="onRechargeCenterError"
                ></iframe>
              </div>
              <div v-else class="px-5 py-16 text-center text-sm text-gray-500 dark:text-gray-400">
                {{ t('payment.rechargeCenterUnavailable') }}
              </div>
            </div>
          </section>
          <!-- Legacy subscription flow kept for compatibility with existing order recovery links. -->
          <template v-else-if="activeTab === 'subscription'">
            <!-- Subscription confirm (inline, replaces plan list) -->
            <template v-if="selectedPlan">
              <div class="card p-5">
                <!-- Header: platform badge + plan name -->
                <div class="mb-3 flex flex-wrap items-center gap-2">
                  <span :class="['rounded-md border px-2 py-0.5 text-xs font-medium', planBadgeClass]">
                    {{ platformLabel(selectedPlan.group_platform || '') }}
                  </span>
                  <h3 class="text-lg font-bold text-gray-900 dark:text-white">{{ selectedPlan.name }}</h3>
                </div>
                <!-- Price -->
                <div class="flex items-baseline gap-2">
                  <span v-if="selectedPlan.original_price" class="text-sm text-gray-400 line-through dark:text-gray-500">
                    {{ formatSelectedSubscriptionPaymentAmount(selectedPlan.original_price) }}
                  </span>
                  <span :class="['text-3xl font-bold', planTextClass]">{{ formatSelectedSubscriptionPaymentAmount(selectedPlan.price) }}</span>
                  <span class="text-sm text-gray-500 dark:text-gray-400">/ {{ planValiditySuffix }}</span>
                </div>
                <!-- Description -->
                <p v-if="selectedPlan.description" class="mt-2 text-sm leading-relaxed text-gray-500 dark:text-gray-400">
                  {{ selectedPlan.description }}
                </p>
                <!-- Rate + Limits grid -->
                <div class="mt-3 grid grid-cols-2 gap-3">
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.rate') }}</span>
                    <div class="flex items-baseline">
                      <span :class="['text-lg font-bold', planTextClass]">×{{ selectedPlan.rate_multiplier ?? 1 }}</span>
                    </div>
                  </div>
                  <div v-if="planHasPeakRate(selectedPlan)">
                    <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.peakRate') }}</span>
                    <div class="text-sm font-semibold text-amber-700 dark:text-amber-300">
                      {{ planPeakRateLabel(selectedPlan) }}
                    </div>
                  </div>
                  <div v-if="selectedPlan.daily_limit_usd != null">
                    <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.dailyLimit') }}</span>
                    <div class="text-lg font-semibold text-gray-800 dark:text-gray-200">${{ selectedPlan.daily_limit_usd }}</div>
                  </div>
                  <div v-if="selectedPlan.weekly_limit_usd != null">
                    <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.weeklyLimit') }}</span>
                    <div class="text-lg font-semibold text-gray-800 dark:text-gray-200">${{ selectedPlan.weekly_limit_usd }}</div>
                  </div>
                  <div v-if="selectedPlan.monthly_limit_usd != null">
                    <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.monthlyLimit') }}</span>
                    <div class="text-lg font-semibold text-gray-800 dark:text-gray-200">${{ selectedPlan.monthly_limit_usd }}</div>
                  </div>
                  <div v-if="selectedPlan.daily_limit_usd == null && selectedPlan.weekly_limit_usd == null && selectedPlan.monthly_limit_usd == null">
                    <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('payment.planCard.quota') }}</span>
                    <div class="text-lg font-semibold text-gray-800 dark:text-gray-200">{{ t('payment.planCard.unlimited') }}</div>
                  </div>
                </div>
              </div>
              <div v-if="enabledMethods.length >= 1" class="card p-6">
                <PaymentMethodSelector
                  :methods="subMethodOptions"
                  :selected="selectedMethod"
                  @select="selectedMethod = $event"
                />
              </div>
              <div v-if="feeRate > 0 && selectedPlan.price > 0" class="card p-6">
                <div class="space-y-2 text-sm">
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.amountLabel') }}</span>
                    <span class="text-gray-900 dark:text-white">{{ formatSelectedPaymentAmount(subPaymentAmount) }}</span>
                  </div>
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('payment.fee') }} ({{ feeRate }}%)</span>
                    <span class="text-gray-900 dark:text-white">{{ formatSelectedPaymentAmount(subFeeAmount) }}</span>
                  </div>
                  <div class="flex justify-between border-t border-gray-200 pt-2 dark:border-dark-600">
                    <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('payment.actualPay') }}</span>
                    <span class="text-lg font-bold text-primary-600 dark:text-primary-400">{{ formatSelectedPaymentAmount(subTotalAmount) }}</span>
                  </div>
                </div>
              </div>
              <button :class="['btn w-full py-3 text-base font-medium', paymentButtonClass]" :disabled="!canSubmitSubscription || submitting" @click="confirmSubscribe">
                <span v-if="submitting" class="flex items-center justify-center gap-2">
                  <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent"></span>
                  {{ t('common.processing') }}
                </span>
                <span v-else>{{ t('payment.createOrder') }} {{ formatSelectedPaymentAmount(subTotalAmount) }}</span>
              </button>
              <button class="btn btn-secondary w-full" @click="selectedPlan = null">{{ t('common.cancel') }}</button>
            </template>
            <!-- Plan list -->
            <template v-else>
              <div v-if="checkout.plans.length === 0" class="card py-16 text-center">
                <Icon name="gift" size="xl" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
                <p class="text-gray-500 dark:text-gray-400">{{ t('payment.noPlans') }}</p>
              </div>
              <div v-else :class="planGridClass">
                <SubscriptionPlanCard v-for="plan in checkout.plans" :key="plan.id" :plan="plan" :active-subscriptions="activeSubscriptions" @select="selectPlan" />
              </div>
              <!-- Active subscriptions (compact, below plan list) -->
              <div v-if="activeSubscriptions.length > 0">
                <p class="mb-2 text-xs font-medium text-gray-400 dark:text-gray-500">{{ t('payment.activeSubscription') }}</p>
                <div class="space-y-2">
                  <div v-for="sub in activeSubscriptions" :key="sub.id"
                    class="flex items-center gap-3 rounded-xl border border-gray-100 bg-white px-3 py-2 dark:border-dark-700 dark:bg-dark-800">
                    <div :class="['h-6 w-1 shrink-0 rounded-full', platformAccentBarClass(sub.group?.platform || '')]" />
                    <div class="min-w-0 flex-1">
                      <div class="flex items-center gap-1.5">
                        <span class="truncate text-xs font-semibold text-gray-900 dark:text-white">{{ sub.group?.name || t('payment.groupFallback', { id: sub.group_id }) }}</span>
                        <span :class="['shrink-0 rounded-full px-1.5 py-0.5 text-[9px] font-medium', platformBadgeLightClass(sub.group?.platform || '')]">{{ platformLabel(sub.group?.platform || '') }}</span>
                      </div>
                      <div class="flex flex-wrap gap-x-3 text-[11px] text-gray-400 dark:text-gray-500">
                        <span>{{ t('payment.planCard.rate') }}: ×{{ sub.group?.rate_multiplier ?? 1 }}</span>
                        <span v-if="subscriptionHasPeakRate(sub)">{{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(sub) }}</span>
                        <span v-if="sub.group?.daily_limit_usd == null && sub.group?.weekly_limit_usd == null && sub.group?.monthly_limit_usd == null">{{ t('payment.planCard.quota') }}: {{ t('payment.planCard.unlimited') }}</span>
                        <span v-if="sub.expires_at">{{ t('userSubscriptions.daysRemaining', { days: getDaysRemaining(sub.expires_at) }) }}</span>
                        <span v-else>{{ t('userSubscriptions.noExpiration') }}</span>
                      </div>
                    </div>
                    <span class="badge badge-success shrink-0 text-[10px]">{{ t('userSubscriptions.status.active') }}</span>
                  </div>
                </div>
              </div>
            </template>
          </template>
        </template>
        <div v-if="(checkout.help_text || checkout.help_image_url) && paymentPhase === 'select' && !selectedPlan" class="card p-4">
          <div class="flex flex-col items-center gap-3">
            <img v-if="checkout.help_image_url" :src="checkout.help_image_url" alt=""
              class="h-40 max-w-full cursor-pointer rounded-lg object-contain transition-opacity hover:opacity-80"
              @click="previewImage = checkout.help_image_url" />
            <div v-if="checkout.help_text" class="markdown-body w-full overflow-x-auto break-words" v-html="renderedHelpText"></div>
          </div>
        </div>
      </template>
    </div>
    <!-- Renewal Plan Selection Modal -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="showRenewalModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4" @click.self="closeRenewalModal">
          <div class="relative flex max-h-full w-full max-w-lg flex-col rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl dark:border-dark-700 dark:bg-dark-900">
            <!-- Close button -->
            <button class="absolute right-4 top-4 rounded-lg p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-200" @click="closeRenewalModal">
              <Icon name="x" size="md" :stroke-width="2" />
            </button>
            <h3 class="mb-4 shrink-0 text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.selectPlan') }}</h3>
            <div class="min-h-0 space-y-4 overflow-y-auto">
              <SubscriptionPlanCard v-for="plan in renewalPlans" :key="plan.id" :plan="plan" :active-subscriptions="activeSubscriptions" @select="selectPlanFromModal" />
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
    <!-- Image Preview Overlay -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="previewImage" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 backdrop-blur-sm" @click="previewImage = ''">
          <img :src="previewImage" alt="" class="max-h-[85vh] max-w-[90vw] rounded-xl object-contain shadow-2xl" />
        </div>
      </Transition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import '@/styles/announcement-markdown.css'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePaymentStore } from '@/stores/payment'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import { isMobileDevice } from '@/utils/device'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel, type PeakRateFields } from '@/utils/peak-rate'
import type { SubscriptionPlan, CheckoutInfoResponse, CreateOrderResult, OrderType } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import AmountInput from '@/components/payment/AmountInput.vue'
import ConsoleTabs from '@/components/common/ConsoleTabs.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import { METHOD_ORDER, getPaymentPopupFeatures, isBuiltInAlipayMethod, isBuiltInWxpayMethod } from '@/components/payment/providerConfig'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  buildCreateOrderPayload,
  clearPaymentRecoverySnapshot,
  decidePaymentLaunch,
  getVisibleMethods,
  normalizeVisibleMethod,
  readPaymentRecoverySnapshot,
  type PaymentRecoverySnapshot,
  writePaymentRecoverySnapshot,
} from '@/components/payment/paymentFlow'
import { platformAccentBarClass, platformBadgeLightClass, platformBadgeClass, platformTextClass, platformLabel } from '@/utils/platformColors'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import Icon from '@/components/icons/Icon.vue'
import { DEFAULT_PAYMENT_CURRENCY, formatPaymentAmount, normalizePaymentCurrency } from '@/components/payment/currency'
import { planValiditySuffix as validitySuffixOf } from '@/components/payment/validity'
import type { PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'
import { buildPaymentErrorToastMessage, describePaymentScenarioError } from './paymentUx'
import { buildEmbeddedUrl, detectTheme } from '@/utils/embedded-url'
import { hasWechatResumeQuery, parseWechatResumeRoute, stripWechatResumeQuery } from './paymentWechatResume'

const i18n = useI18n()
const { t } = i18n
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const paymentStore = usePaymentStore()
const subscriptionStore = useSubscriptionStore()
const appStore = useAppStore()

const user = computed(() => authStore.user)
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)

function getDaysRemaining(expiresAt: string): number {
  const diff = new Date(expiresAt).getTime() - Date.now()
  return Math.max(0, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

function subscriptionHasPeakRate(sub: { group?: PeakRateFields | null }): boolean {
  return hasPeakRate(sub.group)
}

function subscriptionPeakRateLabel(sub: { group?: PeakRateFields | null }): string {
  return formatPeakRateWindow(sub.group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

const loading = ref(true)
const submitting = ref(false)
const errorMessage = ref('')
const errorHintMessage = ref('')
const activeTab = ref<'recharge' | 'rechargeCenter' | 'subscription'>('recharge')
const rechargeCenterFrameRef = ref<HTMLElement | null>(null)
const isRechargeCenterFullscreen = ref(false)
const RECHARGE_CENTER_LOAD_TIMEOUT_MS = 5000
const rechargeCenterIframeRef = ref<HTMLIFrameElement | null>(null)
const rechargeCenterFailed = ref(false)
let rechargeCenterLoadTimer: ReturnType<typeof setTimeout> | null = null
const amount = ref<number | null>(null)
const selectedMethod = ref('')
const selectedMethodLabel = computed(() => methodOptions.value.find(method => method.type === selectedMethod.value)?.display_name || t(`payment.methods.${selectedMethod.value}`, selectedMethod.value))
const selectedPlan = ref<SubscriptionPlan | null>(null)
const previewImage = ref('')

const paymentPhase = ref<'select' | 'paying'>('select')

interface CreateOrderOptions {
  openid?: string
  wechatResumeToken?: string
  paymentType?: string
  isResume?: boolean
  mobileQrFallbackAttempted?: boolean
}

interface WeixinJSBridgeLike {
  invoke(
    action: string,
    payload: Record<string, unknown>,
    callback: (result: Record<string, unknown>) => void,
  ): void
}

function emptyPaymentState(): PaymentRecoverySnapshot {
  return {
    orderId: 0,
    amount: 0,
    qrCode: '',
    expiresAt: '',
    paymentType: '',
    payUrl: '',
    outTradeNo: '',
    clientSecret: '',
    intentId: '',
    currency: '',
    countryCode: '',
    paymentEnv: '',
    payAmount: 0,
    orderType: '',
    paymentMode: '',
    resumeToken: '',
    alipayMobilePrecreateDeepLink: false,
    createdAt: 0,
  }
}

function getWeixinJSBridge(): WeixinJSBridgeLike | undefined {
  return (window as Window & { WeixinJSBridge?: WeixinJSBridgeLike }).WeixinJSBridge
}

function waitForWeixinJSBridge(timeoutMs = 4000): Promise<WeixinJSBridgeLike | null> {
  const existing = getWeixinJSBridge()
  if (existing) return Promise.resolve(existing)

  return new Promise((resolve) => {
    let settled = false
    const finish = (bridge: WeixinJSBridgeLike | null) => {
      if (settled) return
      settled = true
      document.removeEventListener('WeixinJSBridgeReady', handleReady)
      document.removeEventListener('onWeixinJSBridgeReady', handleReady)
      window.clearTimeout(timer)
      resolve(bridge)
    }
    const handleReady = () => finish(getWeixinJSBridge() ?? null)
    const timer = window.setTimeout(() => finish(getWeixinJSBridge() ?? null), timeoutMs)
    document.addEventListener('WeixinJSBridgeReady', handleReady, false)
    document.addEventListener('onWeixinJSBridgeReady', handleReady, false)
  })
}

async function invokeWechatJsapiPayment(payload: Record<string, unknown>): Promise<Record<string, unknown>> {
  const bridge = await waitForWeixinJSBridge()
  if (!bridge) {
    throw new Error('WECHAT_JSAPI_UNAVAILABLE')
  }
  return new Promise((resolve) => {
    bridge.invoke('getBrandWCPayRequest', payload, (result) => resolve(result || {}))
  })
}

const paymentState = ref<PaymentRecoverySnapshot>(emptyPaymentState())

function persistRecoverySnapshot(snapshot: PaymentRecoverySnapshot) {
  if (typeof window === 'undefined' || !snapshot.orderId) return
  writePaymentRecoverySnapshot(window.localStorage, snapshot, PAYMENT_RECOVERY_STORAGE_KEY)
}

function removeRecoverySnapshot() {
  if (typeof window === 'undefined') return
  clearPaymentRecoverySnapshot(window.localStorage, PAYMENT_RECOVERY_STORAGE_KEY)
}

function resetPayment() {
  paymentPhase.value = 'select'
  paymentState.value = emptyPaymentState()
  removeRecoverySnapshot()
}

async function redirectToPaymentResult(state: PaymentRecoverySnapshot): Promise<void> {
  const query: Record<string, string | undefined> = {}
  if (state.orderId > 0) {
    query.order_id = String(state.orderId)
  }
  if (state.outTradeNo) {
    query.out_trade_no = state.outTradeNo
  }
  if (state.resumeToken) {
    query.resume_token = state.resumeToken
  }
  await router.push({
    path: '/payment/result',
    query,
  })
}

function buildWechatOAuthAuthorizeUrl(
  authorizeUrl: string,
  context: { paymentType: string; orderType: OrderType; planId?: number; orderAmount: number },
): string {
  const normalizedUrl = authorizeUrl.trim()
  if (!normalizedUrl || typeof window === 'undefined') {
    return normalizedUrl
  }

  try {
    const targetUrl = new URL(normalizedUrl, window.location.origin)
    const redirectPath = targetUrl.searchParams.get('redirect') || '/purchase'
    const redirectUrl = new URL(redirectPath, window.location.origin)
    const paymentType = normalizeVisibleMethod(context.paymentType) || context.paymentType.trim() || 'wxpay'

    redirectUrl.searchParams.set('payment_type', paymentType)
    redirectUrl.searchParams.set('order_type', context.orderType)

    if (context.planId) {
      redirectUrl.searchParams.set('plan_id', String(context.planId))
    } else {
      redirectUrl.searchParams.delete('plan_id')
    }

    if (context.orderAmount > 0) {
      redirectUrl.searchParams.set('amount', String(context.orderAmount))
    } else {
      redirectUrl.searchParams.delete('amount')
    }

    targetUrl.searchParams.set('redirect', `${redirectUrl.pathname}${redirectUrl.search}`)
    return targetUrl.toString()
  } catch {
    return normalizedUrl
  }
}

function onPaymentDone() {
  const wasSubscription = paymentState.value.orderType === 'subscription'
  resetPayment()
  selectedPlan.value = null
  if (wasSubscription) {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
}

async function onPaymentSuccess() {
  const completedPayment = { ...paymentState.value }
  removeRecoverySnapshot()
  authStore.refreshUser()
  if (paymentState.value.orderType === 'subscription') {
    subscriptionStore.fetchActiveSubscriptions(true).catch(() => {})
  }
  await redirectToPaymentResult(completedPayment)
}

function onPaymentSettled() {
  removeRecoverySnapshot()
}

// All checkout data from single API call
const checkout = ref<CheckoutInfoResponse>({
  methods: {}, global_min: 0, global_max: 0,
  plans: [], balance_disabled: false, balance_recharge_multiplier: 1, subscription_usd_to_cny_rate: 0, recharge_fee_rate: 0, help_text: '', help_image_url: '', stripe_publishable_key: '',
})

const renderedHelpText = computed(() => DOMPurify.sanitize(
  marked.parse(checkout.value.help_text || '', { async: false, gfm: true, breaks: false }),
))

const rechargeCenterEnabled = computed(() => checkout.value.recharge_center_enabled === true)

const tabs = computed(() => {
  const result: { key: 'recharge' | 'rechargeCenter'; label: string }[] = []
  if (!checkout.value.balance_disabled) result.push({ key: 'recharge', label: t('payment.tabTopUp') })
  if (rechargeCenterEnabled.value) result.push({ key: 'rechargeCenter', label: t('payment.tabRechargeCenter') })
  return result
})

const visibleMethods = computed(() => getVisibleMethods(checkout.value.methods))
const enabledMethods = computed(() => Object.keys(visibleMethods.value))
const validAmount = computed(() => amount.value ?? 0)

function inheritRechargeFeeRate(override: number | null | undefined, fallback: number): number {
  return override == null || Number.isNaN(override) ? fallback : override
}

function methodRechargeFeeRate(methodType: string): number {
  const ml = visibleMethods.value[methodType]
  return inheritRechargeFeeRate(ml?.recharge_fee_rate, checkout.value?.recharge_fee_rate ?? 0)
}

function methodBalanceRechargeMultiplier(methodType: string): number {
  const ml = visibleMethods.value[methodType]
  const override = ml?.balance_recharge_multiplier
  if (override != null && Number.isFinite(override) && override > 0) return override
  const fallback = checkout.value.balance_recharge_multiplier
  return Number.isFinite(fallback) && fallback > 0 ? fallback : 1
}

const balanceRechargeMultiplier = computed(() => methodBalanceRechargeMultiplier(selectedMethod.value))
// 订阅 CNY 换算汇率（1 USD = X CNY）。0 = 未配置，订阅保持 price 直付（与后端 opt-in 条件严格镜像）。
const subscriptionUsdToCnyRate = computed(() => {
  const rate = checkout.value.subscription_usd_to_cny_rate
  return Number.isFinite(rate) && rate > 0 ? rate : 0
})
const creditedAmount = computed(() => Math.round((validAmount.value * balanceRechargeMultiplier.value) * 100) / 100)

// Adaptive grid: center single card, 2-col for 2 plans, 3-col for 3+
const planGridClass = computed(() => {
  const n = checkout.value.plans.length
  if (n <= 2) return 'grid grid-cols-1 gap-5 sm:grid-cols-2'
  return 'grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3'
})

// Check if an amount fits a method's [min, max]. 0 = no limit.
function amountFitsMethod(amt: number, methodType: string): boolean {
  if (amt <= 0) return true
  const ml = visibleMethods.value[methodType]
  if (!ml) return false
  if (ml.single_min > 0 && amt < ml.single_min) return false
  if (ml.single_max > 0 && amt > ml.single_max) return false
  return true
}

// Visible methods decide the amount range shown to users.
const globalMinAmount = computed(() => {
  const limits = Object.values(visibleMethods.value)
  if (limits.length === 0) return 0
  if (limits.some(limit => limit.single_min <= 0)) return 0
  return Math.min(...limits.map(limit => limit.single_min))
})
const globalMaxAmount = computed(() => {
  const limits = Object.values(visibleMethods.value)
  if (limits.length === 0) return 0
  if (limits.some(limit => limit.single_max <= 0)) return 0
  return Math.max(...limits.map(limit => limit.single_max))
})

// Selected method's limits (for validation and error messages)
const selectedLimit = computed(() => visibleMethods.value[selectedMethod.value])
const selectedCurrency = computed(() => normalizePaymentCurrency(selectedLimit.value?.currency))
const localeCode = computed(() => {
  const raw = i18n.locale as unknown
  if (typeof raw === 'string') return raw
  if (raw && typeof raw === 'object' && 'value' in raw) {
    return String((raw as { value?: string }).value || '')
  }
  return undefined
})

const RECHARGE_CENTER_MENU_ID = '322273f5aaa4d036'
const rechargeCenterUrl = computed(() => {
  if (!rechargeCenterEnabled.value) return ''
  const item = appStore.cachedPublicSettings?.custom_menu_items?.find(
    candidate => candidate.id === RECHARGE_CENTER_MENU_ID,
  )
  const baseUrl = item?.url?.trim() || ''
  if (!baseUrl) return ''
  try {
    if (!['https:', 'http:'].includes(new URL(baseUrl, window.location.href).protocol)) return ''
  } catch {
    return ''
  }
  return buildEmbeddedUrl(
    baseUrl,
    authStore.user?.id,
    authStore.token,
    detectTheme(),
    localeCode.value,
  )
})

function handleRechargeCenterFullscreenChange() {
  isRechargeCenterFullscreen.value = typeof document !== 'undefined'
    && document.fullscreenElement === rechargeCenterFrameRef.value
}

async function toggleRechargeCenterFullscreen() {
  if (typeof document === 'undefined' || !rechargeCenterFrameRef.value) return
  try {
    if (document.fullscreenElement === rechargeCenterFrameRef.value) {
      await document.exitFullscreen()
    } else {
      await rechargeCenterFrameRef.value.requestFullscreen()
    }
  } catch {
    // Fullscreen can be denied by browser policy; the iframe remains usable.
  }
}

function clearRechargeCenterLoadTimer() {
  if (rechargeCenterLoadTimer == null) return
  clearTimeout(rechargeCenterLoadTimer)
  rechargeCenterLoadTimer = null
}

function startRechargeCenterLoadWatch() {
  clearRechargeCenterLoadTimer()
  rechargeCenterFailed.value = false
  if (!rechargeCenterIframeRef.value) return
  rechargeCenterLoadTimer = setTimeout(() => {
    rechargeCenterLoadTimer = null
    rechargeCenterFailed.value = true
  }, RECHARGE_CENTER_LOAD_TIMEOUT_MS)
}

function onRechargeCenterLoad(event: Event) {
  if (event.currentTarget !== rechargeCenterIframeRef.value) return
  rechargeCenterFailed.value = false
  clearRechargeCenterLoadTimer()
}

function onRechargeCenterError(event: Event) {
  if (event.currentTarget !== rechargeCenterIframeRef.value) return
  rechargeCenterFailed.value = true
  clearRechargeCenterLoadTimer()
}

watch(rechargeCenterIframeRef, startRechargeCenterLoadWatch, { flush: 'post' })

function currencyFractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
    }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

function roundPaymentAmount(value: number, currency: string): number {
  if (!Number.isFinite(value)) return 0
  const factor = 10 ** currencyFractionDigits(currency)
  return Math.round(value * factor) / factor
}

function ceilPaymentAmount(value: number, currency: string): number {
  if (!Number.isFinite(value)) return 0
  const factor = 10 ** currencyFractionDigits(currency)
  return Math.ceil(value * factor) / factor
}

function subscriptionPaymentAmountForCurrency(value: number, currency: string): number {
  const rate = subscriptionUsdToCnyRate.value
  if (rate <= 0 || currency !== DEFAULT_PAYMENT_CURRENCY) return roundPaymentAmount(value, currency)
  return roundPaymentAmount(value * rate, currency)
}

function formatSelectedPaymentAmount(value: number): string {
  return formatPaymentAmount(value, selectedCurrency.value, localeCode.value)
}

function formatSelectedSubscriptionPaymentAmount(value: number): string {
  return formatSelectedPaymentAmount(subscriptionPaymentAmountForCurrency(value, selectedCurrency.value))
}

const methodOptions = computed<PaymentMethodOption[]>(() =>
  enabledMethods.value.map((type) => {
    const ml = visibleMethods.value[type]
    return {
      type,
      display_name: ml?.display_name,
      fee_rate: methodRechargeFeeRate(type),
      available: ml?.available !== false && amountFitsMethod(validAmount.value, type),
    }
  })
)

const feeRate = computed(() => methodRechargeFeeRate(selectedMethod.value))
const feeAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.ceil(((validAmount.value * feeRate.value) / 100) * 100) / 100
    : 0
)
const totalAmount = computed(() =>
  feeRate.value > 0 && validAmount.value > 0
    ? Math.round((validAmount.value + feeAmount.value) * 100) / 100
    : validAmount.value
)

const amountError = computed(() => {
  if (validAmount.value <= 0) return ''
  // No method can handle this amount
  if (!enabledMethods.value.some((m) => amountFitsMethod(validAmount.value, m))) {
    return t('payment.amountNoMethod')
  }
  // Selected method can't handle this amount (but others can)
  const ml = selectedLimit.value
  if (ml) {
    if (ml.single_min > 0 && validAmount.value < ml.single_min) return t('payment.amountTooLow', { min: formatSelectedPaymentAmount(ml.single_min) })
    if (ml.single_max > 0 && validAmount.value > ml.single_max) return t('payment.amountTooHigh', { max: formatSelectedPaymentAmount(ml.single_max) })
  }
  return ''
})

const canSubmit = computed(() =>
  validAmount.value > 0
    && amountFitsMethod(validAmount.value, selectedMethod.value)
    && selectedLimit.value?.available !== false
)

const subPaymentAmount = computed(() => {
  const price = selectedPlan.value?.price ?? 0
  return subscriptionPaymentAmountForCurrency(price, selectedCurrency.value)
})

const subFeeAmount = computed(() => {
  if (feeRate.value <= 0 || subPaymentAmount.value <= 0) return 0
  return ceilPaymentAmount((subPaymentAmount.value * feeRate.value) / 100, selectedCurrency.value)
})

const subTotalAmount = computed(() => {
  if (feeRate.value <= 0 || subPaymentAmount.value <= 0) return subPaymentAmount.value
  return roundPaymentAmount(subPaymentAmount.value + subFeeAmount.value, selectedCurrency.value)
})

function subscriptionTotalAmountForCurrency(value: number, currency: string): number {
  const paymentAmount = subscriptionPaymentAmountForCurrency(value, currency)
  if (feeRate.value <= 0 || paymentAmount <= 0) return paymentAmount
  const fee = ceilPaymentAmount((paymentAmount * feeRate.value) / 100, currency)
  return roundPaymentAmount(paymentAmount + fee, currency)
}

// Subscription-specific: method options based on gateway pay amount
const subMethodOptions = computed<PaymentMethodOption[]>(() => {
  const price = selectedPlan.value?.price ?? 0
  return enabledMethods.value.map((type) => {
    const ml = visibleMethods.value[type]
    const currency = normalizePaymentCurrency(ml?.currency)
    return {
      type,
      display_name: ml?.display_name,
      fee_rate: methodRechargeFeeRate(type),
      available: ml?.available !== false && amountFitsMethod(subscriptionTotalAmountForCurrency(price, currency), type),
    }
  })
})

const canSubmitSubscription = computed(() =>
  selectedPlan.value !== null
    && amountFitsMethod(subTotalAmount.value, selectedMethod.value)
    && selectedLimit.value?.available !== false
)

// Auto-switch to first available method when current selection can't handle the amount
watch(() => [validAmount.value, selectedMethod.value] as const, ([amt, method]) => {
  if (amt <= 0 || amountFitsMethod(amt, method)) return
  const available = enabledMethods.value.find((m) => amountFitsMethod(amt, m))
  if (available) selectedMethod.value = available
})

// Payment button class: follows selected payment method color
const paymentButtonClass = computed(() => {
  const m = selectedMethod.value
  if (!m) return 'btn-primary'
  if (isBuiltInAlipayMethod(m)) return 'btn-alipay'
  if (isBuiltInWxpayMethod(m)) return 'btn-wxpay'
  if (m === 'stripe') return 'btn-stripe'
  if (m === 'airwallex') return 'btn-airwallex'
  return 'btn-primary'
})

// Subscription confirm: platform accent colors (clean card, no gradient)
const planBadgeClass = computed(() => platformBadgeClass(selectedPlan.value?.group_platform || ''))
const planTextClass = computed(() => platformTextClass(selectedPlan.value?.group_platform || ''))

// Renewal modal state
const showRenewalModal = ref(false)
const renewGroupId = ref<number | null>(null)
const renewalPlans = computed(() => {
  if (renewGroupId.value == null) return []
  return checkout.value.plans.filter(p => p.group_id === renewGroupId.value)
})

const planValiditySuffix = computed(() => {
  if (!selectedPlan.value) return ''
  return validitySuffixOf(selectedPlan.value, t)
})

function planHasPeakRate(plan: SubscriptionPlan): boolean {
  return hasPeakRate(plan)
}

function planPeakRateLabel(plan: SubscriptionPlan): string {
  return formatPeakRateWindow(plan, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

function selectPlan(plan: SubscriptionPlan) {
  selectedPlan.value = plan
  errorMessage.value = ''
}

function selectPlanFromModal(plan: SubscriptionPlan) {
  showRenewalModal.value = false
  renewGroupId.value = null
  selectedPlan.value = plan
  errorMessage.value = ''
}

function closeRenewalModal() {
  showRenewalModal.value = false
  renewGroupId.value = null
}

async function handleSubmitRecharge() {
  if (!canSubmit.value || submitting.value) return
  await createOrder(validAmount.value, 'balance')
}

async function confirmSubscribe() {
  if (!selectedPlan.value || submitting.value) return
  await createOrder(selectedPlan.value.price, 'subscription', selectedPlan.value.id)
}

async function createOrder(orderAmount: number, orderType: OrderType, planId?: number, options: CreateOrderOptions = {}) {
  submitting.value = true
  errorMessage.value = ''
  errorHintMessage.value = ''
  const requestType = normalizeVisibleMethod(options.paymentType || selectedMethod.value) || options.paymentType || selectedMethod.value
  try {
    const payload = buildCreateOrderPayload({
      amount: orderAmount,
      paymentType: requestType,
      orderType,
      planId,
      origin: typeof window !== 'undefined' ? window.location.origin : '',
      isMobile: isMobileDevice(),
      isWechatBrowser: typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: !!(checkout.value.alipay_force_qrcode && normalizeVisibleMethod(requestType) === 'alipay'),
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
    })
    if (options.openid) {
      payload.openid = options.openid
    }
    if (options.wechatResumeToken) {
      payload.wechat_resume_token = options.wechatResumeToken
    }

    const result = await paymentStore.createOrder(payload) as CreateOrderResult & { resume_token?: string }
    const openWindow = (url: string) => {
      const win = window.open(url, 'paymentPopup', getPaymentPopupFeatures())
      if (!win || win.closed) {
        window.location.href = url
      }
    }
    const visibleMethod = normalizeVisibleMethod(requestType) || requestType
    // When user clicks the dedicated Stripe button, leave method blank so the
    // landing page renders Stripe's full Payment Element (card/link/alipay/wxpay).
    const stripeMethod = visibleMethod === 'stripe'
      ? ''
      : visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay'
    const stripeRouteUrl = result.client_secret && visibleMethod !== 'airwallex'
      ? router.resolve({
        path: '/payment/stripe',
        query: {
          order_id: String(result.order_id),
          client_secret: result.client_secret,
          method: stripeMethod || undefined,
          resume_token: result.resume_token || undefined,
        },
      }).href
      : ''
    const airwallexRouteUrl = result.client_secret && result.intent_id
      ? router.resolve({
        path: '/payment/airwallex',
        query: {
          order_id: String(result.order_id),
          out_trade_no: result.out_trade_no || undefined,
          resume_token: result.resume_token || undefined,
        },
      }).href
      : ''
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType,
      isMobile: isMobileDevice(),
      isWechatBrowser: typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: !!(checkout.value.alipay_force_qrcode && visibleMethod === 'alipay'),
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      stripePopupUrl: stripeRouteUrl,
      stripeRouteUrl,
      airwallexRouteUrl,
    })

    if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
      window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
        paymentType: visibleMethod,
        orderType,
        planId,
        orderAmount,
      })
      return
    }

    if (decision.kind === 'unhandled') {
      applyScenarioError({ reason: 'UNHANDLED_PAYMENT_SCENARIO' }, visibleMethod)
      return
    }

    paymentState.value = decision.paymentState
    paymentPhase.value = 'paying'
    persistRecoverySnapshot(decision.recovery)

    if (decision.kind === 'stripe_popup') {
      openWindow(decision.paymentState.payUrl)
      return
    }
    if (decision.kind === 'stripe_route') {
      window.location.href = decision.paymentState.payUrl
      return
    }
    if (decision.kind === 'airwallex_route') {
      window.location.href = decision.paymentState.payUrl
      return
    }
    if (decision.kind === 'wechat_jsapi' && decision.jsapi) {
      try {
        const jsapiResult = await invokeWechatJsapiPayment(decision.jsapi as Record<string, unknown>)
        const errMsg = String(jsapiResult.err_msg || '').toLowerCase()
        if (errMsg.includes('cancel')) {
          appStore.showInfo(t('payment.qr.cancelled'))
          resetPayment()
        } else if (errMsg && !errMsg.includes('ok')) {
          resetPayment()
          const fallbackApplied = await attemptMobileQrFallback(
            { reason: 'WECHAT_JSAPI_FAILED', message: errMsg },
            {
              orderAmount,
              orderType,
              planId,
              paymentType: visibleMethod,
              attempted: options.mobileQrFallbackAttempted === true,
            },
          )
          if (!fallbackApplied) {
            applyScenarioError({ reason: 'WECHAT_JSAPI_FAILED', message: errMsg }, visibleMethod)
          }
        } else {
          const resultState = { ...decision.paymentState }
          resetPayment()
          await redirectToPaymentResult(resultState)
        }
      } catch (err: unknown) {
        resetPayment()
        const fallbackApplied = await attemptMobileQrFallback(err, {
          orderAmount,
          orderType,
          planId,
          paymentType: visibleMethod,
          attempted: options.mobileQrFallbackAttempted === true,
        })
        if (!fallbackApplied) {
          throw err
        }
      }
      return
    }
    if (decision.kind === 'redirect_waiting' && decision.paymentState.payUrl) {
      if (isMobileDevice()) {
        window.location.href = decision.paymentState.payUrl
        return
      }
      openWindow(decision.paymentState.payUrl)
    }
  } catch (err: unknown) {
    const apiErr = err as Record<string, unknown>
    if (apiErr.reason === 'TOO_MANY_PENDING') {
      const metadata = apiErr.metadata as Record<string, unknown> | undefined
      errorMessage.value = t('payment.errors.tooManyPending', { max: metadata?.max || '' })
      errorHintMessage.value = ''
    } else if (apiErr.reason === 'CANCEL_RATE_LIMITED') {
      errorMessage.value = t('payment.errors.cancelRateLimited')
      errorHintMessage.value = ''
    } else if (await attemptMobileQrFallback(err, {
      orderAmount,
      orderType,
      planId,
      paymentType: requestType,
      attempted: options.mobileQrFallbackAttempted === true,
    })) {
      return
    } else {
      const handled = applyScenarioError(
        err,
        normalizeVisibleMethod(options.paymentType || selectedMethod.value) || selectedMethod.value,
      )
      if (!handled) {
        errorMessage.value = extractI18nErrorMessage(err, t, 'payment.errors', extractApiErrorMessage(err, t('payment.result.failed')))
        errorHintMessage.value = ''
      }
      if (handled) {
        return
      }
    }
    appStore.showError(buildPaymentErrorToastMessage(errorMessage.value, errorHintMessage.value))
  } finally {
    submitting.value = false
  }
}

interface MobileQrFallbackContext {
  orderAmount: number
  orderType: OrderType
  planId?: number
  paymentType: string
  attempted: boolean
}

function shouldFallbackToDesktopQr(err: unknown, paymentMethod: string, attempted: boolean): boolean {
  if (attempted || !isMobileDevice()) {
    return false
  }

  const normalizedMethod = normalizeVisibleMethod(paymentMethod) || paymentMethod
  const reason = typeof err === 'object' && err && 'reason' in err && typeof err.reason === 'string'
    ? err.reason
    : ''
  const message = err instanceof Error
    ? err.message
    : (typeof err === 'object' && err && 'message' in err && typeof err.message === 'string'
      ? err.message
      : '')
  const normalizedMessage = message.toLowerCase()

  if (normalizedMethod === 'wxpay') {
    return reason === 'WECHAT_H5_NOT_AUTHORIZED'
      || reason === 'WECHAT_PAYMENT_MP_NOT_CONFIGURED'
      || reason === 'WECHAT_JSAPI_FAILED'
      || reason === 'PAYMENT_GATEWAY_ERROR'
      || reason === 'UNHANDLED_PAYMENT_SCENARIO'
      || normalizedMessage.includes('weixinjsbridge is unavailable')
      || normalizedMessage.includes('wechat_jsapi_unavailable')
  }

  if (normalizedMethod === 'alipay') {
    return reason === 'PAYMENT_GATEWAY_ERROR' || reason === 'UNHANDLED_PAYMENT_SCENARIO'
  }

  return false
}

async function attemptMobileQrFallback(err: unknown, context: MobileQrFallbackContext): Promise<boolean> {
  if (!shouldFallbackToDesktopQr(err, context.paymentType, context.attempted)) {
    return false
  }

  try {
    const visibleMethod = normalizeVisibleMethod(context.paymentType) || context.paymentType
    const payload = buildCreateOrderPayload({
      amount: context.orderAmount,
      paymentType: visibleMethod,
      orderType: context.orderType,
      planId: context.planId,
      origin: typeof window !== 'undefined' ? window.location.origin : '',
      isMobile: false,
      isWechatBrowser: false,
    })
    const result = await paymentStore.createOrder(payload) as CreateOrderResult & { resume_token?: string }
    const stripeMethod = visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay'
    const stripeRouteUrl = result.client_secret
      ? router.resolve({
        path: '/payment/stripe',
        query: {
          order_id: String(result.order_id),
          client_secret: result.client_secret,
          method: stripeMethod,
          resume_token: result.resume_token || undefined,
        },
      }).href
      : ''
    const decision = decidePaymentLaunch(result, {
      visibleMethod,
      orderType: context.orderType,
      isMobile: false,
      isWechatBrowser: false,
      stripePopupUrl: stripeRouteUrl,
      stripeRouteUrl,
    })

    if (decision.kind !== 'qr_waiting' || !decision.paymentState.qrCode) {
      return false
    }

    errorMessage.value = ''
    errorHintMessage.value = ''
    paymentState.value = decision.paymentState
    paymentPhase.value = 'paying'
    persistRecoverySnapshot(decision.recovery)
    appStore.showWarning(t('payment.errors.mobilePaymentFallbackToQr'))
    return true
  } catch {
    return false
  }
}

function applyScenarioError(err: unknown, paymentMethod: string): boolean {
  const descriptor = describePaymentScenarioError(err, {
    paymentMethod,
    isMobile: isMobileDevice(),
    isWechatBrowser: typeof window !== 'undefined' && /MicroMessenger/i.test(window.navigator.userAgent),
  })
  if (!descriptor) {
    errorMessage.value = ''
    errorHintMessage.value = ''
    return false
  }
  errorMessage.value = t(descriptor.messageKey)
  errorHintMessage.value = descriptor.hintKey ? t(descriptor.hintKey) : ''
  appStore.showError(buildPaymentErrorToastMessage(errorMessage.value, errorHintMessage.value))
  return true
}

async function resumeWechatPaymentFromQuery() {
  const resume = parseWechatResumeRoute(route.query, checkout.value.plans, validAmount.value)
  if (!resume) {
    return
  }

  selectedMethod.value = resume.paymentType
  if (resume.orderType === 'balance' && resume.orderAmount > 0) {
    amount.value = resume.orderAmount
  }
  if (resume.orderType === 'subscription' && resume.planId) {
    selectedPlan.value = checkout.value.plans.find(plan => plan.id === resume.planId) ?? null
  }

  await router.replace({ path: route.path, query: stripWechatResumeQuery(route.query) })

  if (resume.wechatResumeToken) {
    await createOrder(0, resume.orderType, resume.planId, {
      wechatResumeToken: resume.wechatResumeToken,
      paymentType: resume.paymentType,
      isResume: true,
    })
    return
  }

  if (resume.orderAmount > 0 && resume.openid) {
    await createOrder(resume.orderAmount, resume.orderType, resume.planId, {
      openid: resume.openid,
      paymentType: resume.paymentType,
      isResume: true,
    })
  }
}

onMounted(async () => {
  document.addEventListener('fullscreenchange', handleRechargeCenterFullscreenChange)
  try {
    const res = await paymentAPI.getCheckoutInfo()
    checkout.value = res.data
    if (enabledMethods.value.length) {
      const order: readonly string[] = METHOD_ORDER
      const sorted = [...enabledMethods.value].sort((a, b) => {
        const ai = order.indexOf(a)
        const bi = order.indexOf(b)
        return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
      })
      selectedMethod.value = sorted[0]
    }
    if (typeof window !== 'undefined') {
      if (hasWechatResumeQuery(route.query)) {
        removeRecoverySnapshot()
      }
      const routeResumeToken = typeof route.query.resume_token === 'string'
        ? route.query.resume_token
        : typeof route.query.wechat_resume_token === 'string'
          ? route.query.wechat_resume_token
          : undefined
      const restored = readPaymentRecoverySnapshot(
        window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY),
        { resumeToken: routeResumeToken },
      )
      if (restored) {
        paymentState.value = restored
        paymentPhase.value = 'paying'
        const restoredMethod = normalizeVisibleMethod(restored.paymentType)
          || (visibleMethods.value[restored.paymentType] ? restored.paymentType : '')
        if (restoredMethod) {
          selectedMethod.value = restoredMethod
        }
      } else {
        removeRecoverySnapshot()
      }
    }
    await resumeWechatPaymentFromQuery()
    if (checkout.value.balance_disabled && rechargeCenterEnabled.value) {
      activeTab.value = 'rechargeCenter'
    }
    // Preserve legacy subscription deep links for payment callbacks and old renewal URLs.
    // The visible navigation no longer exposes this tab; normal users use Recharge Center.
    if (route.query.tab === 'subscription') {
      activeTab.value = 'subscription'
      if (route.query.group) {
        const groupId = Number(route.query.group)
        const groupPlans = checkout.value.plans.filter(p => p.group_id === groupId)
        if (groupPlans.length === 1) {
          selectedPlan.value = groupPlans[0]
        } else if (groupPlans.length > 1) {
          renewGroupId.value = groupId
          showRenewalModal.value = true
        }
      }
    }
  } catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { loading.value = false }
  // Fetch active subscriptions (uses cache, non-blocking)
  subscriptionStore.fetchActiveSubscriptions().catch(() => {})
})

onUnmounted(() => {
  document.removeEventListener('fullscreenchange', handleRechargeCenterFullscreenChange)
  clearRechargeCenterLoadTimer()
})
</script>

<style scoped>
.signal-exchange-links {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
  color: var(--payment-muted);
  font-size: 12px;
}

.signal-exchange-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 32px;
  padding: 5px 10px;
  border: 1px solid currentColor;
  border-radius: 6px;
  color: var(--payment-accent);
}

.signal-exchange-link:hover {
  background: var(--payment-accent-soft);
}

@media (max-width: 639px) {
  .signal-exchange-link { min-height: 40px; }
}

.signal-payment {
  --payment-bg: var(--signal-bg, #f5f6f5);
  --payment-surface: var(--signal-surface, #ffffff);
  --payment-line: var(--signal-line, #dce2df);
  --payment-text: var(--signal-text, #202423);
  --payment-muted: var(--signal-muted, #65716a);
  --payment-accent: var(--signal-accent, #087f68);
  --payment-accent-soft: var(--signal-accent-soft, #e5f3ee);
  --payment-amber: var(--signal-amber, #a86610);
  display: grid;
  gap: 18px;
  min-width: 0;
  color: var(--payment-text);
  letter-spacing: 0;
}

.signal-payment-heading {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--payment-line);
}

.signal-payment-heading__icon {
  display: grid;
  width: 38px;
  height: 38px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--payment-line);
  border-radius: 6px;
  color: var(--payment-accent);
  background: var(--payment-surface);
}

.signal-payment-heading__index,
.signal-kicker {
  margin: 0;
  color: var(--payment-muted);
  font-size: 10px;
  font-weight: 700;
  line-height: 1.4;
  letter-spacing: 0;
}

.signal-payment-heading h1 {
  margin: 2px 0 0;
  color: var(--payment-text);
  font-size: 24px;
  font-weight: 700;
  line-height: 1.2;
}

.signal-payment-heading p:last-child {
  margin: 4px 0 0;
  color: var(--payment-muted);
  font-size: 13px;
  line-height: 1.5;
}

.signal-payment-orders { flex: 0 0 auto; margin-left: auto; gap: 8px; }
.signal-payment-flow { padding-top: 14px; }
.signal-payment-step { display: flex; align-items: baseline; gap: 12px; margin-bottom: 18px; font-size: 15px; font-weight: 600; }
.signal-payment-step > span { font-size: 11px; color: var(--payment-muted); font-variant-numeric: tabular-nums; }
.signal-payment-section :deep(.payment-amount-input__entry > span:first-child),
.signal-payment-section :deep(.payment-method-selector > label) { display: none; }

.signal-account-strip {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  min-width: 0;
  padding: 18px;
  border-bottom: 1px solid var(--payment-line);
}

.signal-account-name {
  margin: 3px 0 0;
  overflow: hidden;
  color: var(--payment-text);
  font-size: 15px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.signal-balance-block {
  display: grid;
  flex: 0 0 auto;
  gap: 2px;
  text-align: right;
}

.signal-balance-block span {
  color: var(--payment-muted);
  font-size: 11px;
}

.signal-balance-block strong {
  color: var(--payment-accent);
  font-size: 16px;
  font-variant-numeric: tabular-nums;
  line-height: 1.1;
}

.signal-recharge-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(280px, 320px);
  gap: 40px;
  align-items: start;
}

.signal-recharge-fields {
  display: grid;
  gap: 24px;
  min-width: 0;
}

.signal-checkout-summary,
.signal-empty-state {
  border: 1px solid var(--payment-line);
  border-radius: 8px;
  background: var(--payment-surface);
}

.signal-payment-section {
  min-width: 0;
}

.signal-payment-section + .signal-payment-section {
  padding-top: 24px;
  border-top: 1px solid var(--payment-line);
}

.signal-empty-state {
  padding: 56px 20px;
  text-align: center;
}

.signal-payment-warning {
  margin: 10px 0 0;
  color: var(--payment-amber);
  font-size: 12px;
}

.signal-checkout-summary {
  position: sticky;
  top: 82px;
  overflow: hidden;
}

.signal-checkout-summary__header {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
  padding: 22px 18px;
  border-bottom: 1px solid var(--payment-line);
  background: var(--payment-bg);
}

.signal-checkout-summary__header span {
  color: var(--payment-muted);
  font-size: 12px;
  font-weight: 600;
}

.signal-checkout-summary__header strong {
  color: var(--payment-accent);
  font-size: 30px;
  font-weight: 550;
  overflow-wrap: anywhere;
  max-width: 100%;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.signal-checkout-summary__rows {
  display: grid;
  gap: 10px;
  padding: 16px 18px;
}

.signal-checkout-summary__rows > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  color: var(--payment-muted);
  font-size: 13px;
}

.signal-checkout-summary__rows strong {
  color: var(--payment-text);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.signal-rate-note {
  margin: 0 18px 16px;
  padding: 10px 0 0;
  border-top: 1px solid var(--payment-line);
  color: var(--payment-amber);
  font-size: 11px;
  line-height: 1.5;
}

.signal-checkout-action { padding: 0 18px 18px; }
.signal-checkout-mobile-total { display: none; }

.recharge-center-shell {
  overflow: hidden;
  border: 1px solid var(--payment-line);
  border-radius: 8px;
  background: var(--payment-surface);
}

.recharge-center-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--payment-line);
  background: var(--payment-bg);
}

.signal-tool-button {
  display: inline-flex;
  min-height: 38px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 7px 10px;
  border: 1px solid var(--payment-line);
  border-radius: 6px;
  color: var(--payment-text);
  background: var(--payment-surface);
  font-size: 12px;
  font-weight: 600;
  transition: border-color 120ms ease, color 120ms ease, background-color 120ms ease;
}

.signal-tool-button:hover {
  border-color: var(--payment-accent);
  color: var(--payment-accent);
  background: var(--payment-accent-soft);
}

.recharge-center-frame {
  padding: 8px;
  background: var(--payment-bg);
}
.recharge-center-fallback {
  display: flex;
  min-height: 320px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48px 24px;
  text-align: center;
  background: var(--payment-bg);
}

.signal-payment :deep(.payment-amount-input),
.signal-payment :deep(.payment-method-selector) {
  color: var(--payment-text);
}

.signal-payment :deep(.payment-amount-input__grid) {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}

.signal-payment :deep(.payment-amount-input__option) {
  min-width: 0;
  min-height: 46px;
  padding: 9px 10px;
  border-width: 1px;
  border-radius: 6px;
  font-variant-numeric: tabular-nums;
  font-size: 17px;
  font-weight: 550;
}

.signal-payment :deep(.payment-method-selector__grid) {
  grid-template-columns: minmax(0, 1fr);
  gap: 8px;
}

.signal-payment :deep(.payment-method-selector__option) {
  min-height: 66px;
  height: auto;
  padding: 12px 46px 12px 16px;
  border-radius: 6px;
  box-shadow: none;
}

.signal-payment :deep(.payment-method-selector__option > span) { justify-content: flex-start; }
.signal-payment :deep(.payment-method-selector__option::after) { content: ''; position: absolute; right: 18px; top: calc(50% - 7px); width: 14px; height: 14px; border: 1px solid var(--signal-control-line); border-radius: 50%; }
.signal-payment :deep(.payment-method-selector__option[aria-pressed='true']::after) { border: 4px solid var(--payment-accent); background: var(--payment-surface); }

.signal-payment :deep(.payment-method-selector__option[aria-pressed='true']) {
  border-color: var(--payment-accent);
  color: var(--payment-text);
  background: var(--payment-accent-soft);
  box-shadow: inset 0 -2px var(--payment-accent);
}

.signal-payment > :deep(.card),
.signal-payment :deep(.payment-status-card),
.signal-payment :deep(.payment-subscription-plan) {
  border: 1px solid var(--payment-line);
  border-radius: 8px;
  background: var(--payment-surface);
  box-shadow: none;
}

.signal-payment :deep(.payment-status-panel) {
  width: min(100%, 620px);
  margin-inline: auto;
}

.signal-payment :deep(.payment-status-card .rounded-xl) {
  border: 1px solid var(--payment-line);
  border-radius: 6px;
  background: var(--payment-bg);
}

.signal-payment :deep(.payment-subscription-plan) {
  transform: none;
}

.signal-payment :deep(.payment-subscription-plan:hover) {
  transform: none;
  border-color: var(--payment-accent);
  box-shadow: none;
}

.signal-payment :deep(.payment-subscription-plan > div:first-child) {
  height: 3px;
  background: var(--payment-accent);
}

.signal-payment :deep(.payment-subscription-plan__quota) {
  border: 1px solid var(--payment-line);
  border-radius: 6px;
  background: var(--payment-bg);
}

.signal-payment :deep(.payment-subscription-plan__action) {
  border-radius: 6px;
  box-shadow: none;
  transform: none;
}

.recharge-center-shell:fullscreen {
  display: flex;
  height: 100dvh;
  width: 100vw;
  flex-direction: column;
  border-radius: 0;
}

.recharge-center-shell:fullscreen .recharge-center-frame {
  min-height: 0;
  flex: 1;
}

.recharge-center-shell:fullscreen iframe {
  height: 100%;
  min-height: 0;
}

@media (max-width: 767px) {
  .signal-payment {
    gap: 14px;
  }

  .signal-payment-heading {
    padding-bottom: 14px;
    flex-wrap: wrap;
  }

  .signal-payment-heading h1 {
    font-size: 21px;
  }

  .signal-recharge-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .signal-checkout-summary {
    position: static;
    overflow: visible;
  }

  .signal-payment:has(.signal-checkout-action) { padding-bottom: 98px; }
  .signal-payment :is(input, .payment-method-selector__option) { scroll-margin-bottom: 110px; }
  .signal-payment-orders { font-size: 12px; }
  .signal-checkout-action { position: fixed; bottom: 0; left: 0; right: 0; z-index: 25; display: flex; align-items: center; gap: 16px; padding: 14px 16px max(14px, env(safe-area-inset-bottom)); border-top: 1px solid var(--payment-line); background: var(--payment-surface); box-shadow: 0 -6px 24px #0000000a; }
  .signal-checkout-action > .btn { flex: 1; min-width: 0; width: auto; font-size: 14px; }
  .signal-checkout-mobile-total { display: grid; flex: 1; min-width: 0; font-variant-numeric: tabular-nums; }
  .signal-checkout-mobile-total small { font-size: 11px; color: var(--payment-muted); }
  .signal-checkout-mobile-total strong { font-size: 22px; overflow-wrap: anywhere; }
  .signal-checkout-button-amount { display: none; }

  .recharge-center-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (max-width: 374px) {
  .signal-payment-heading__icon {
    display: none;
  }

  .signal-account-strip {
    align-items: flex-start;
    flex-direction: column;
    gap: 12px;
  }

  .signal-balance-block {
    width: 100%;
    padding-top: 10px;
    border-top: 1px solid var(--payment-line);
    text-align: left;
  }

  .signal-payment :deep(.payment-amount-input__grid) {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .recharge-center-toolbar > div:last-child {
    display: grid;
    width: 100%;
    grid-template-columns: 1fr;
  }
}
</style>
