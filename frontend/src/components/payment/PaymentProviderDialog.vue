<template>
  <BaseDialog
    :show="show"
    :title="editing ? t('admin.settings.payment.editProvider') : t('admin.settings.payment.createProvider')"
    width="wide"
    @close="emit('close')"
  >
    <form id="provider-form" @submit.prevent="handleSave" class="space-y-4">
      <!-- Name + Key -->
      <div class="grid grid-cols-2 gap-4">
        <UFormField :label="t('admin.settings.payment.providerName')" required>
          <UInput v-model="form.name" type="text" required class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.settings.payment.providerKey')" required>
          <Select
            v-model="form.provider_key"
            :options="(!!editing ? allKeyOptions : enabledKeyOptions) as SelectOption[]"
            :disabled="!!editing"
            @change="onKeyChange"
          />
        </UFormField>
      </div>

      <!-- Toggles + Payment mode + Supported types (single row) -->
      <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
        <ToggleSwitch :label="t('common.enabled')" :checked="form.enabled" @toggle="form.enabled = !form.enabled" />
        <ToggleSwitch
          :label="t('admin.settings.payment.refundEnabled')"
          :checked="form.refund_enabled"
          @toggle="form.refund_enabled = !form.refund_enabled; if (!form.refund_enabled) form.allow_user_refund = false"
        />
        <ToggleSwitch
          v-if="form.refund_enabled"
          :label="t('admin.settings.payment.allowUserRefund')"
          :checked="form.allow_user_refund"
          @toggle="form.allow_user_refund = !form.allow_user_refund"
        />
        <div v-if="supportsPaymentMode" class="flex items-center gap-2">
          <span class="text-xs font-medium text-muted">{{ t('admin.settings.payment.paymentMode') }}</span>
          <div class="flex gap-1.5">
            <UButton
              v-for="mode in paymentModeOptions"
              :key="mode.value"
              @click="form.payment_mode = mode.value"
              size="xs"
              :color="form.payment_mode === mode.value ? 'primary' : 'neutral'"
              :variant="form.payment_mode === mode.value ? 'subtle' : 'outline'"
            >{{ mode.label }}</UButton>
          </div>
        </div>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <UFormField
          :label="t('admin.settings.payment.instanceRechargeFeeRate')"
          :description="t('admin.settings.payment.instanceRechargeFeeRateHint')"
        >
          <UInput
              :value="form.recharge_fee_rate ?? ''"
              @input="onFeeRateInput(($event.target as HTMLInputElement).value)"
              type="number"
              step="0.01"
              min="0"
              max="100"
              :placeholder="t('admin.settings.payment.instanceRechargeFeeRatePlaceholder')"
              class="w-full"
            >
              <template #trailing><span class="text-xs text-dimmed">%</span></template>
            </UInput>
        </UFormField>
        <UFormField
          :label="t('admin.settings.payment.instanceBalanceMultiplier')"
          :description="t('admin.settings.payment.instanceBalanceMultiplierHint')"
        >
          <UInput
            :value="form.balance_recharge_multiplier ?? ''"
            @input="onMultiplierInput(($event.target as HTMLInputElement).value)"
            type="number"
            step="0.01"
            min="0.01"
            :placeholder="t('admin.settings.payment.instanceBalanceMultiplierPlaceholder')"
            class="w-full"
          />
        </UFormField>
      </div>

      <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
        <div v-if="availableTypes.length > 1" class="flex items-center gap-2">
          <span class="text-xs font-medium text-muted">{{ t('admin.settings.payment.supportedTypes') }}</span>
          <div class="flex flex-wrap gap-1.5">
            <UButton
              v-for="pt in availableTypes"
              :key="pt.value"
              @click="toggleType(pt.value)"
              size="xs"
              :color="isTypeSelected(pt.value) ? 'primary' : 'neutral'"
              :variant="isTypeSelected(pt.value) ? 'subtle' : 'outline'"
            >{{ pt.label }}</UButton>
          </div>
        </div>
      </div>

      <div v-if="form.provider_key === 'easypay'" class="space-y-3">
        <UFormField
          :label="t('admin.settings.payment.easypayCustomMethods')"
          :description="t('admin.settings.payment.easypayCustomMethodsHint')"
          class="flex max-sm:flex-col justify-between sm:items-center gap-4"
        >
          <UButton icon="i-lucide-plus" data-testid="easypay-add-custom-method" @click="addEasyPayCustomMethod" color="neutral" variant="outline" size="sm">
            {{ t('admin.settings.payment.addCustomMethod') }}
          </UButton>
        </UFormField>
        <div v-if="easyPayCustomMethods.length" class="space-y-2">
          <div
            v-for="(method, index) in easyPayCustomMethods"
            :key="index"
            class="grid grid-cols-[1fr_1fr_1fr_auto] items-end gap-2"
          >
            <UFormField :label="t('admin.settings.payment.customMethodType')">
              <UInput v-model="method.type" type="text" placeholder="credit_card" class="w-full" />
            </UFormField>
            <UFormField :label="t('admin.settings.payment.customMethodUpstreamType')">
              <UInput v-model="method.upstreamType" type="text" placeholder="credit_card" class="w-full" />
            </UFormField>
            <UFormField :label="t('admin.settings.payment.customMethodDisplayName')">
              <UInput
                v-model="method.displayName"
                type="text"
                :placeholder="t('admin.settings.payment.customMethodDisplayNamePlaceholder')"
                class="w-full"
              />
            </UFormField>
            <UButton
              icon="i-lucide-trash-2"
              color="error"
              variant="ghost"
              square
              :aria-label="t('common.delete')"
              @click="removeEasyPayCustomMethod(index)"
            />
          </div>
        </div>
      </div>

      <!-- Config fields -->
      <USeparator />
      <div>
        <div class="mb-3 flex items-center gap-2">
          <h4 class="text-sm font-medium text-highlighted">
            {{ t('admin.settings.payment.providerConfig') }}
          </h4>
          <HelpTooltip v-if="paymentGuide" trigger="click" width-class="w-80">
            <template #trigger>
              <UButton
                :aria-label="t('admin.settings.payment.paymentGuideTrigger')"
                :title="t('admin.settings.payment.paymentGuideTrigger')"
                icon="i-lucide-circle-help"
                color="neutral"
                variant="ghost"
                size="xs"
                square
              />
            </template>
            <div class="space-y-3">
              <p class="font-medium text-highlighted">{{ paymentGuide.summary }}</p>
              <div
                v-for="item in paymentGuide.items"
                :key="item.title"
                class="space-y-1.5 border-t border-default pt-2 first:border-t-0 first:pt-0"
              >
                <p class="font-medium text-highlighted">{{ item.title }}</p>
                <p><span class="text-dimmed">{{ t('admin.settings.payment.guideOpenLabel') }}</span>{{ item.open }}</p>
                <p><span class="text-dimmed">{{ t('admin.settings.payment.guideCallLabel') }}</span>{{ item.call }}</p>
                <p><span class="text-dimmed">{{ t('admin.settings.payment.guideFallbackLabel') }}</span>{{ item.fallback }}</p>
              </div>
              <p v-if="paymentGuide.note" class="border-t border-default pt-2 text-[11px] text-dimmed">
                {{ paymentGuide.note }}
              </p>
            </div>
          </HelpTooltip>
        </div>
        <p v-if="paymentGuide" class="mb-3 text-xs text-muted">
          {{ paymentGuide.summary }}
        </p>
        <div class="space-y-3">
          <UFormField v-for="field in resolvedFields" :key="field.key" required>
            <template #label>
              {{ field.label }}
              <span v-if="field.optional" class="text-xs text-dimmed">({{ t('common.optional') }})</span>
            </template>
            <template #description>
              <span v-if="field.hintKey" class="leading-relaxed">
                {{ t(field.hintKey) }}
              </span>
            </template>
            <UTextarea
              v-if="field.sensitive && field.key.toLowerCase().includes('key') && field.key !== 'pkey'"
              v-model="config[field.key]"
              :rows="3"
              autocomplete="new-password"
              data-1p-ignore
              data-lpignore="true"
              data-bwignore="true"
              spellcheck="false"
              :placeholder="editing ? t('admin.accounts.leaveEmptyToKeep') : ''"
              class="w-full font-mono"
            />
            <UInput
                v-else-if="field.sensitive"
                :type="visibleFields[field.key] ? 'text' : 'password'"
                v-model="config[field.key]"
                autocomplete="new-password"
                data-1p-ignore
                data-lpignore="true"
                data-bwignore="true"
                spellcheck="false"
                :placeholder="editing ? t('admin.accounts.leaveEmptyToKeep') : (field.defaultValue || '')"
                class="w-full"
              >
                <template #trailing>
                  <UButton
                    :icon="visibleFields[field.key] ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                    color="neutral"
                    variant="link"
                    size="sm"
                    square
                    :aria-label="field.label"
                    @click="visibleFields[field.key] = !visibleFields[field.key]"
                  />
                </template>
              </UInput>
            <Select
              v-else-if="field.options?.length"
              v-model="config[field.key]"
              :options="field.options"
              :searchable="field.options.length > 5"
              class="w-full"
            />
            <UInput
              v-else
              type="text"
              v-model="config[field.key]"
              :placeholder="field.defaultValue || ''"
              class="w-full"
            />
          </UFormField>
        </div>

        <!-- Callback URLs (each = editable URL + fixed path) -->
        <div v-if="callbackPaths" class="mt-4 space-y-3">
          <UFormField v-if="callbackPaths.notifyUrl" :label="t('admin.settings.payment.field_notifyUrl')" required>
            <UFieldGroup class="w-full">
              <UInput v-model="notifyBaseUrl" type="text" :placeholder="defaultBaseUrl" data-testid="notify-base-url" class="min-w-0 flex-1" />
              <UBadge color="neutral" variant="outline" size="lg" class="whitespace-nowrap font-mono">{{ callbackPaths.notifyUrl }}</UBadge>
            </UFieldGroup>
          </UFormField>
          <UFormField v-if="callbackPaths.returnUrl" :label="t('admin.settings.payment.field_returnUrl')" required>
            <UFieldGroup class="w-full">
              <UInput v-model="returnBaseUrl" type="text" :placeholder="defaultBaseUrl" data-testid="return-base-url" class="min-w-0 flex-1" />
              <UBadge color="neutral" variant="outline" size="lg" class="whitespace-nowrap font-mono">{{ callbackPaths.returnUrl }}</UBadge>
            </UFieldGroup>
          </UFormField>
        </div>

        <!-- 服务商 Webhook 提示 -->
        <UAlert v-if="providerWebhookUrl" color="info" variant="subtle" icon="i-lucide-info">
          <template #description>
            <p>
              {{ t(providerWebhookHint) }}
            </p>
            <code class="mt-1 block break-all rounded bg-info/10 px-2 py-1 font-mono">
              {{ providerWebhookUrl }}
            </code>
            <p v-if="form.provider_key === 'stripe'" class="mt-2 leading-relaxed">
              {{ t('admin.settings.payment.stripeWebhookApiVersionHint', { version: STRIPE_SDK_API_VERSION }) }}
            </p>
          </template>
        </UAlert>
      </div>

      <!-- Per-type limits (collapsible) -->
      <USeparator v-if="limitableTypes.length" />
      <div v-if="limitableTypes.length">
        <UButton
          :label="t('admin.settings.payment.limitsTitle')"
          :trailing-icon="limitsExpanded ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
          :aria-expanded="limitsExpanded"
          color="neutral"
          variant="link"
          class="px-0"
          @click="limitsExpanded = !limitsExpanded"
        />
        <div v-show="limitsExpanded" class="mt-3 space-y-3">
          <div v-for="lt in limitableTypes" :key="lt.value">
            <p class="mb-2 text-xs font-medium text-toned">{{ lt.label }}</p>
            <div class="grid grid-cols-3 gap-3">
              <UFormField :label="t('admin.settings.payment.limitSingleMin')">
                <UInput
                  type="number"
                  :value="getLimitVal(lt.value, 'singleMin')"
                  @input="setLimitVal(lt.value, 'singleMin', ($event.target as HTMLInputElement).value)"
                  min="1"
                  step="0.01"
                  :placeholder="limitPlaceholder(lt.value)"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.payment.limitSingleMax')">
                <UInput
                  type="number"
                  :value="getLimitVal(lt.value, 'singleMax')"
                  @input="setLimitVal(lt.value, 'singleMax', ($event.target as HTMLInputElement).value)"
                  min="1"
                  step="0.01"
                  :placeholder="limitPlaceholder(lt.value)"
                  class="w-full"
                />
              </UFormField>
              <UFormField :label="t('admin.settings.payment.limitDaily')">
                <UInput
                  type="number"
                  :value="getLimitVal(lt.value, 'dailyLimit')"
                  @input="setLimitVal(lt.value, 'dailyLimit', ($event.target as HTMLInputElement).value)"
                  min="1"
                  step="0.01"
                  :placeholder="limitPlaceholder(lt.value)"
                  class="w-full"
                />
              </UFormField>
            </div>
          </div>
          <p class="text-xs text-dimmed">{{ t('admin.settings.payment.limitsHint') }}</p>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <UButton @click="emit('close')" color="neutral" variant="outline">{{ t('common.cancel') }}</UButton>
        <UButton type="submit" form="provider-form" :loading="saving" :disabled="saving">
          {{ saving ? t('common.saving') : t('common.save') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Select from '@/components/common/Select.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import ToggleSwitch from './ToggleSwitch.vue'
import type { ProviderInstance } from '@/types/payment'
import type { EasyPayCustomMethod, TypeOption } from './providerConfig'
import {
  PROVIDER_CONFIG_FIELDS,
  PROVIDER_SUPPORTED_TYPES,
  PROVIDER_CALLBACK_PATHS,
  WEBHOOK_PATHS,
  PAYMENT_MODE_QRCODE,
  PAYMENT_MODE_POPUP,
  PAYMENT_MODE_REDIRECT,
  STRIPE_SDK_API_VERSION,
  getAvailableTypes,
  extractBaseUrl,
  parseEasyPayCustomMethods,
  serializeEasyPayCustomMethods,
} from './providerConfig'

/** Default payment_mode per provider key — "" means "no preference, use
 * provider's built-in default behavior". */
function defaultPaymentMode(providerKey: string): string {
  if (providerKey === 'easypay') return PAYMENT_MODE_QRCODE
  if (providerKey === 'epusdt') return PAYMENT_MODE_REDIRECT
  return ''
}

/** Provider keys whose admin UI exposes a payment_mode selector.
 * Other providers always send payment_mode = ''. */
function providerSupportsPaymentMode(providerKey: string): boolean {
  return providerKey === 'easypay' || providerKey === 'alipay' || providerKey === 'epusdt'
}

/** Allowed payment_mode values per provider. Used to coerce DB values
 * from a different provider (or stale data) back to the default. */
function isValidPaymentMode(providerKey: string, mode: string): boolean {
  if (providerKey === 'easypay') {
    return mode === PAYMENT_MODE_QRCODE || mode === PAYMENT_MODE_POPUP
  }
  if (providerKey === 'epusdt') {
    return mode === PAYMENT_MODE_REDIRECT
  }
  if (providerKey === 'alipay') {
    return mode === '' || mode === PAYMENT_MODE_REDIRECT
  }
  return mode === ''
}

const props = defineProps<{
  show: boolean
  saving: boolean
  editing: ProviderInstance | null
  allKeyOptions: TypeOption[]
  enabledKeyOptions: TypeOption[]
  allPaymentTypes: TypeOption[]
  redirectLabel: string
}>()

const emit = defineEmits<{
  close: []
  save: [payload: {
    provider_key: string
    name: string
    supported_types: string[]
    enabled: boolean
    payment_mode: string
    refund_enabled: boolean
    allow_user_refund: boolean
    recharge_fee_rate: number | null
    balance_recharge_multiplier: number | null
    config: Record<string, string>
    limits: string
  }]
}>()

const { t } = useI18n()

interface PaymentGuideItem {
  title: string
  open: string
  call: string
  fallback: string
}

interface PaymentGuide {
  summary: string
  items: PaymentGuideItem[]
  note?: string
}

// --- Form state ---
const form = reactive({
  name: '',
  provider_key: 'easypay',
  supported_types: [] as string[],
  enabled: true,
  payment_mode: PAYMENT_MODE_QRCODE,
  refund_enabled: false,
  allow_user_refund: false,
  recharge_fee_rate: null as number | null,
  balance_recharge_multiplier: null as number | null,
})
const config = reactive<Record<string, string>>({})
const limits = reactive<Record<string, Record<string, number>>>({})
const notifyBaseUrl = ref('')
const returnBaseUrl = ref('')
const limitsExpanded = ref(false)
const visibleFields = reactive<Record<string, boolean>>({})
const easyPayCustomMethods = reactive<EasyPayCustomMethod[]>([])

// --- Computed ---
const defaultBaseUrl = typeof window !== 'undefined' ? window.location.origin : ''

const providerWebhookHintMap: Record<string, string> = {
  stripe: 'admin.settings.payment.stripeWebhookHint',
  airwallex: 'admin.settings.payment.airwallexWebhookHint',
}

const providerWebhookUrl = computed(() => {
  const path = WEBHOOK_PATHS[form.provider_key]
  return providerWebhookHintMap[form.provider_key] && path ? defaultBaseUrl + path : ''
})

const providerWebhookHint = computed(() =>
  providerWebhookHintMap[form.provider_key] || 'admin.settings.payment.stripeWebhookHint',
)

const callbackPaths = computed(() => PROVIDER_CALLBACK_PATHS[form.provider_key] || null)

const supportsPaymentMode = computed(() => providerSupportsPaymentMode(form.provider_key))

const paymentModeOptions = computed(() => {
  if (form.provider_key === 'epusdt') {
    return [{ value: PAYMENT_MODE_REDIRECT, label: t('admin.settings.payment.modeRedirect') }]
  }
  if (form.provider_key === 'alipay') {
    // For Alipay official: "" = default (precreate → page.pay fallback);
    // "redirect" = always open the Alipay checkout page in a new tab.
    return [
      { value: '', label: t('admin.settings.payment.modeQRCode') },
      { value: PAYMENT_MODE_REDIRECT, label: t('admin.settings.payment.modeRedirect') },
    ]
  }
  return [
    { value: PAYMENT_MODE_QRCODE, label: t('admin.settings.payment.modeQRCode') },
    { value: PAYMENT_MODE_POPUP, label: t('admin.settings.payment.modePopup') },
  ]
})

const availableTypes = computed(() => {
  const base = getAvailableTypes(form.provider_key, props.allPaymentTypes, props.redirectLabel)
  if (form.provider_key === 'easypay') {
    for (const method of normalizedEasyPayCustomMethods()) {
      if (!base.some(opt => opt.value === method.type)) {
        base.push({
          value: method.type,
          label: method.displayName || method.type,
        })
      }
    }
  }
  // Resolve i18n labels for types not in allPaymentTypes (e.g. card, link inside stripe)
  return base.map(opt =>
    opt.label === opt.value
      ? { ...opt, label: t(`payment.methods.${opt.value}`, opt.value) }
      : opt,
  )
})

const resolvedFields = computed(() => {
  const fields = PROVIDER_CONFIG_FIELDS[form.provider_key] || []
  return fields.map(f => ({
    ...f,
    label: f.label || t(`admin.settings.payment.field_${f.key}`),
  }))
})

const paymentGuide = computed<PaymentGuide | null>(() => {
  if (form.provider_key === 'alipay') {
    return {
      summary: t('admin.settings.payment.alipayGuideSummary'),
      items: [
        {
          title: t('admin.settings.payment.alipayGuideFaceToFaceTitle'),
          open: t('admin.settings.payment.alipayGuideFaceToFaceOpen'),
          call: t('admin.settings.payment.alipayGuideFaceToFaceCall'),
          fallback: t('admin.settings.payment.alipayGuideFaceToFaceFallback'),
        },
        {
          title: t('admin.settings.payment.alipayGuidePagePayTitle'),
          open: t('admin.settings.payment.alipayGuidePagePayOpen'),
          call: t('admin.settings.payment.alipayGuidePagePayCall'),
          fallback: t('admin.settings.payment.alipayGuidePagePayFallback'),
        },
        {
          title: t('admin.settings.payment.alipayGuideWapTitle'),
          open: t('admin.settings.payment.alipayGuideWapOpen'),
          call: t('admin.settings.payment.alipayGuideWapCall'),
          fallback: t('admin.settings.payment.alipayGuideWapFallback'),
        },
      ],
    }
  }

  if (form.provider_key === 'wxpay') {
    return {
      summary: t('admin.settings.payment.wxpayGuideSummary'),
      note: t('admin.settings.payment.wxpayGuideNote'),
      items: [
        {
          title: t('admin.settings.payment.wxpayGuideNativeTitle'),
          open: t('admin.settings.payment.wxpayGuideNativeOpen'),
          call: t('admin.settings.payment.wxpayGuideNativeCall'),
          fallback: t('admin.settings.payment.wxpayGuideNativeFallback'),
        },
        {
          title: t('admin.settings.payment.wxpayGuideJsapiTitle'),
          open: t('admin.settings.payment.wxpayGuideJsapiOpen'),
          call: t('admin.settings.payment.wxpayGuideJsapiCall'),
          fallback: t('admin.settings.payment.wxpayGuideJsapiFallback'),
        },
        {
          title: t('admin.settings.payment.wxpayGuideH5Title'),
          open: t('admin.settings.payment.wxpayGuideH5Open'),
          call: t('admin.settings.payment.wxpayGuideH5Call'),
          fallback: t('admin.settings.payment.wxpayGuideH5Fallback'),
        },
      ],
    }
  }

  if (form.provider_key === 'airwallex') {
    return {
      summary: t('admin.settings.payment.airwallexGuideSummary'),
      note: t('admin.settings.payment.airwallexGuideNote'),
      items: [],
    }
  }

  return null
})

const limitableTypes = computed(() => {
  // Stripe: single "stripe" entry (one set of shared limits)
  if (form.provider_key === 'stripe') {
    return [{ value: 'stripe', label: 'Stripe' }]
  }
  const selected = form.supported_types.filter(t => t !== 'easypay')
  return selected.map(v => {
    const found = props.allPaymentTypes.find(pt => pt.value === v)
    return found || { value: v, label: v }
  })
})

// --- Methods ---
function isTypeSelected(type: string): boolean {
  return form.supported_types.includes(type)
}

function toggleType(type: string) {
  if (form.supported_types.includes(type)) {
    form.supported_types = form.supported_types.filter(t => t !== type)
  } else {
    form.supported_types = [...form.supported_types, type]
  }
}

function normalizedEasyPayCustomMethods(): EasyPayCustomMethod[] {
  return easyPayCustomMethods
    .map(method => ({
      type: normalizeEasyPayCustomMethodCode(method.type),
      upstreamType: normalizeEasyPayCustomMethodCode(method.upstreamType),
      displayName: method.displayName.trim(),
    }))
    .filter(method => method.type || method.upstreamType || method.displayName)
}

function normalizeEasyPayCustomMethodCode(value: string): string {
  return value.trim().toLowerCase()
}

function addEasyPayCustomMethod() {
  easyPayCustomMethods.push({ type: '', upstreamType: '', displayName: '' })
}

function removeEasyPayCustomMethod(index: number) {
  easyPayCustomMethods.splice(index, 1)
}

function onKeyChange() {
  form.supported_types = [...(PROVIDER_SUPPORTED_TYPES[form.provider_key] || [])]
  form.payment_mode = defaultPaymentMode(form.provider_key)
  clearConfig()
  applyDefaults()
}

function clearConfig() {
  Object.keys(config).forEach(k => delete config[k])
  Object.keys(limits).forEach(k => delete limits[k])
  Object.keys(visibleFields).forEach(k => delete visibleFields[k])
  notifyBaseUrl.value = ''
  returnBaseUrl.value = ''
  limitsExpanded.value = false
  easyPayCustomMethods.splice(0, easyPayCustomMethods.length)
}

function applyDefaults() {
  for (const f of PROVIDER_CONFIG_FIELDS[form.provider_key] || []) {
    if (f.defaultValue && !config[f.key]) config[f.key] = f.defaultValue
  }
}

function getLimitVal(paymentType: string, field: string): string {
  const val = limits[paymentType]?.[field]
  return val && val > 0 ? String(val) : ''
}

/** Returns true if any limit field for this payment type has a value */
function hasAnyLimit(paymentType: string): boolean {
  const l = limits[paymentType]
  if (!l) return false
  return (l.singleMin > 0) || (l.singleMax > 0) || (l.dailyLimit > 0)
}

/** Dynamic placeholder: "不限制" if sibling has value, "使用全局配置" if all empty */
function limitPlaceholder(paymentType: string): string {
  return hasAnyLimit(paymentType)
    ? t('admin.settings.payment.limitsNoLimit')
    : t('admin.settings.payment.limitsUseGlobal')
}

function setLimitVal(paymentType: string, field: string, val: string) {
  if (!limits[paymentType]) limits[paymentType] = {}
  const num = Number(val)
  // Empty → clear the field (use global); reject ≤0
  if (val === '' || isNaN(num)) {
    delete limits[paymentType][field]
    return
  }
  if (num <= 0) return
  limits[paymentType][field] = num
}

function serializeLimits(): string {
  const result: Record<string, Record<string, number>> = {}
  for (const [pt, fields] of Object.entries(limits)) {
    const clean: Record<string, number> = {}
    for (const [k, v] of Object.entries(fields)) {
      if (v > 0) clean[k] = v
    }
    if (Object.keys(clean).length > 0) result[pt] = clean
  }
  return Object.keys(result).length > 0 ? JSON.stringify(result) : ''
}

function handleSave() {
  // Validate required fields
  if (!form.name.trim()) {
    emitValidationError(t('admin.settings.payment.validationNameRequired'))
    return
  }
  if (form.provider_key === 'easypay') {
    const validationError = validateEasyPayCustomMethods()
    if (validationError) {
      emitValidationError(validationError)
      return
    }
    syncEasyPayCustomMethods()
  }
  // Validate required config fields — all non-optional fields must be filled.
  // In edit mode, sensitive fields may be left blank to preserve the stored
  // value (backend merges blanks by preserving the existing secret).
  for (const f of PROVIDER_CONFIG_FIELDS[form.provider_key] || []) {
    if (f.optional) continue
    if (props.editing && f.sensitive) continue
    const val = (config[f.key] || '').trim()
    if (!val) {
      const label = f.label || t(`admin.settings.payment.field_${f.key}`)
      emitValidationError(t('admin.settings.payment.validationFieldRequired', { field: label }))
      return
    }
  }

  const clearableConfigKeys = new Set(
    (PROVIDER_CONFIG_FIELDS[form.provider_key] || [])
      .filter(field => field.clearable)
      .map(field => field.key),
  )
  const filteredConfig: Record<string, string> = {}
  for (const [k, v] of Object.entries(config)) {
    if (!v || !v.trim()) {
      if (clearableConfigKeys.has(k)) {
        filteredConfig[k] = ''
      }
      continue
    }
    filteredConfig[k] = v
  }
  if (form.provider_key === 'easypay') {
    filteredConfig.customMethods = serializeEasyPayCustomMethods(normalizedEasyPayCustomMethods())
  }

  // Inject computed callback URLs (each URL = independent base + fixed path)
  // If base URL is empty, auto-fill with current domain
  const paths = PROVIDER_CALLBACK_PATHS[form.provider_key]
  if (paths) {
    const notifyBase = (notifyBaseUrl.value.trim() || defaultBaseUrl).replace(/\/+$/, '')
    const returnBase = (returnBaseUrl.value.trim() || defaultBaseUrl).replace(/\/+$/, '')
    notifyBaseUrl.value = notifyBase
    returnBaseUrl.value = returnBase
    if (paths.notifyUrl) filteredConfig['notifyUrl'] = notifyBase + paths.notifyUrl
    if (paths.returnUrl) filteredConfig['returnUrl'] = returnBase + paths.returnUrl
  }

  emit('save', {
    provider_key: form.provider_key,
    name: form.name,
    supported_types: form.supported_types,
    enabled: form.enabled,
    payment_mode: supportsPaymentMode.value ? form.payment_mode : '',
    refund_enabled: form.refund_enabled,
    allow_user_refund: form.refund_enabled ? form.allow_user_refund : false,
    recharge_fee_rate: form.recharge_fee_rate,
    balance_recharge_multiplier: form.balance_recharge_multiplier,
    config: filteredConfig,
    limits: serializeLimits(),
  })
}

function onFeeRateInput(raw: string) {
  const trimmed = raw.trim()
  if (!trimmed) {
    form.recharge_fee_rate = null
    return
  }
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed)) {
    form.recharge_fee_rate = null
    return
  }
  form.recharge_fee_rate = Math.min(100, Math.max(0, Math.round(parsed * 100) / 100))
}

function onMultiplierInput(raw: string) {
  const trimmed = raw.trim()
  if (!trimmed) {
    form.balance_recharge_multiplier = null
    return
  }
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed <= 0) {
    form.balance_recharge_multiplier = null
    return
  }
  form.balance_recharge_multiplier = parsed
}

function syncEasyPayCustomMethods(): string[] {
  if (form.provider_key !== 'easypay') return []
  const baseTypes = new Set(PROVIDER_SUPPORTED_TYPES.easypay || [])
  const customTypes: string[] = []
  const seen = new Set<string>()
  for (const method of normalizedEasyPayCustomMethods()) {
    if (!method.type || !method.upstreamType) continue
    if (seen.has(method.type)) continue
    seen.add(method.type)
    customTypes.push(method.type)
  }
  form.supported_types = form.supported_types
    .map(type => normalizeEasyPayCustomMethodCode(type))
    .filter(type => baseTypes.has(type) || customTypes.includes(type))
  for (const customType of customTypes) {
    if (!form.supported_types.includes(customType)) {
      form.supported_types.push(customType)
    }
  }
  return customTypes
}

function validateEasyPayCustomMethods(): string | null {
  const seen = new Set<string>()
  for (const method of normalizedEasyPayCustomMethods()) {
    const hasAnyValue = Boolean(method.type || method.upstreamType || method.displayName)
    if (!hasAnyValue) continue
    if (!method.type || !method.upstreamType) {
      return t('admin.settings.payment.validationEasyPayCustomMethodRequired')
    }
    if (!/^[a-z0-9_-]+$/.test(method.type)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodTypeInvalid')
    }
    if (!/^[a-z0-9_.-]+$/.test(method.upstreamType)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodUpstreamTypeInvalid')
    }
    if ((PROVIDER_SUPPORTED_TYPES.easypay || []).includes(method.type)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodReserved')
    }
    if (method.type.startsWith('alipay') || method.type.startsWith('wxpay')) {
      return t('admin.settings.payment.validationEasyPayCustomMethodPrefixReserved')
    }
    if (seen.has(method.type)) {
      return t('admin.settings.payment.validationEasyPayCustomMethodDuplicate')
    }
    seen.add(method.type)
  }
  return null
}

function emitValidationError(msg: string) {
  // Use a custom event or inject appStore — for now use window alert fallback
  // The parent handles this via the save event validation
  import('@/stores').then(m => m.useAppStore().showError(msg))
}

// --- Public API for parent to call ---
function reset(defaultKey: string) {
  form.name = ''
  form.provider_key = defaultKey
  form.supported_types = [...(PROVIDER_SUPPORTED_TYPES[defaultKey] || [])]
  form.enabled = true
  form.payment_mode = defaultPaymentMode(defaultKey)
  form.refund_enabled = false
  form.allow_user_refund = false
  form.recharge_fee_rate = null
  form.balance_recharge_multiplier = null
  clearConfig()
  applyDefaults()
}

function loadProvider(provider: ProviderInstance) {
  form.name = provider.name
  form.provider_key = provider.provider_key
  form.supported_types = Array.isArray(provider.supported_types)
    ? [...provider.supported_types]
    : []
  form.enabled = provider.enabled
  // Coerce to a valid value for this provider. Guards against stale data
  // (e.g. "popup" written by an older client) showing up as an unselected
  // button in the dialog.
  form.payment_mode = isValidPaymentMode(provider.provider_key, provider.payment_mode || '')
    ? (provider.payment_mode || '')
    : defaultPaymentMode(provider.provider_key)
  form.refund_enabled = provider.refund_enabled
  form.allow_user_refund = provider.allow_user_refund
  form.recharge_fee_rate = provider.recharge_fee_rate ?? null
  form.balance_recharge_multiplier = provider.balance_recharge_multiplier ?? null
  clearConfig()
  // Pre-fill config from API response. Backend omits sensitive fields entirely,
  // so those inputs stay blank — submitting blank preserves the stored secret.
  if (provider.config) {
    for (const [k, v] of Object.entries(provider.config)) {
      // Skip notifyUrl/returnUrl — they are derived from callbackBaseUrl
      if (k === 'notifyUrl' || k === 'returnUrl') continue
      if (k === 'customMethods' && provider.provider_key === 'easypay') {
        easyPayCustomMethods.push(...parseEasyPayCustomMethods(v))
        continue
      }
      config[k] = v
    }
    // Extract base URLs from existing callback URLs
    const paths = PROVIDER_CALLBACK_PATHS[provider.provider_key]
    if (paths?.notifyUrl && provider.config['notifyUrl']) {
      notifyBaseUrl.value = extractBaseUrl(provider.config['notifyUrl'], paths.notifyUrl)
    }
    if (paths?.returnUrl && provider.config['returnUrl']) {
      returnBaseUrl.value = extractBaseUrl(provider.config['returnUrl'], paths.returnUrl)
    }
  }
  applyDefaults()
  // Parse existing limits
  if (provider.limits) {
    try {
      const parsed = JSON.parse(provider.limits)
      for (const [pt, fields] of Object.entries(parsed as Record<string, Record<string, number>>)) {
        limits[pt] = { ...fields }
      }
      limitsExpanded.value = Object.keys(limits).length > 0
    } catch { /* ignore */ }
  }
}

defineExpose({ reset, loadProvider })
</script>
