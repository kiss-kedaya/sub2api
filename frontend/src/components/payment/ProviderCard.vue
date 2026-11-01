<template>
  <div
    :class="['flex flex-wrap items-center justify-between gap-x-4 gap-y-2', !enabled && 'opacity-50']"
    :title="!enabled ? t('admin.settings.payment.typeDisabled') + ' — ' + t('admin.settings.payment.enableTypesFirst') : undefined"
  >
    <!-- Left: status icon + name + key / mode + type chips -->
    <div :class="['flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5', !enabled && 'pointer-events-none']">
      <UIcon
        name="i-lucide-server"
        :class="['size-4 shrink-0', provider.enabled && enabled ? 'text-success' : 'text-dimmed']"
      />
      <span class="text-sm font-medium text-highlighted">{{ provider.name }}</span>
      <span class="text-xs text-muted">{{ keyLabel }}<template v-if="provider.payment_mode"> · {{ modeLabel }}</template></span>
      <div v-if="enabled && availableTypes.length" class="flex flex-wrap items-center gap-1">
        <UButton
          v-for="pt in availableTypes"
          :key="pt.value"
          size="xs"
          :color="isSelected(pt.value) ? 'primary' : 'neutral'"
          :variant="isSelected(pt.value) ? 'subtle' : 'outline'"
          @click="emit('toggleType', pt.value)"
        >
          {{ pt.label }}
        </UButton>
      </div>
    </div>

    <!-- Right: toggles + actions -->
    <div :class="['flex flex-wrap items-center gap-4', !enabled && 'pointer-events-none']">
      <ToggleSwitch :label="t('common.enabled')" :checked="provider.enabled" @toggle="emit('toggleField', 'enabled')" />
      <ToggleSwitch :label="t('admin.settings.payment.refundEnabled')" :checked="provider.refund_enabled" @toggle="emit('toggleField', 'refund_enabled')" />
      <ToggleSwitch v-if="provider.refund_enabled" :label="t('admin.settings.payment.allowUserRefund')" :checked="provider.allow_user_refund" @toggle="emit('toggleField', 'allow_user_refund')" />
      <div class="flex items-center gap-1">
        <UTooltip :text="t('common.edit')">
          <UButton
            icon="i-lucide-pencil"
            color="neutral"
            variant="ghost"
            size="sm"
            square
            :aria-label="t('common.edit')"
            data-testid="provider-edit"
            @click="emit('edit')"
          />
        </UTooltip>
        <UTooltip :text="t('common.delete')">
          <UButton
            icon="i-lucide-trash-2"
            color="error"
            variant="ghost"
            size="sm"
            square
            :aria-label="t('common.delete')"
            @click="emit('delete')"
          />
        </UTooltip>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ToggleSwitch from './ToggleSwitch.vue'
import type { ProviderInstance } from '@/types/payment'
import type { TypeOption } from './providerConfig'
import { PAYMENT_MODE_QRCODE, PAYMENT_MODE_POPUP, PAYMENT_MODE_REDIRECT } from './providerConfig'

const PROVIDER_KEY_LABELS: Record<string, string> = {
  easypay: 'admin.settings.payment.providerEasypay',
  alipay: 'admin.settings.payment.providerAlipay',
  wxpay: 'admin.settings.payment.providerWxpay',
  stripe: 'admin.settings.payment.providerStripe',
  airwallex: 'admin.settings.payment.providerAirwallex',
  epusdt: 'admin.settings.payment.providerEpusdt',
}

const props = defineProps<{
  provider: ProviderInstance
  enabled: boolean
  availableTypes: TypeOption[]
}>()

const emit = defineEmits<{
  toggleField: [field: 'enabled' | 'refund_enabled' | 'allow_user_refund']
  toggleType: [type: string]
  edit: []
  delete: []
}>()

const { t } = useI18n()

const keyLabel = computed(() => t(PROVIDER_KEY_LABELS[props.provider.provider_key] || props.provider.provider_key))

const modeLabel = computed(() => {
  if (props.provider.payment_mode === PAYMENT_MODE_QRCODE) return t('admin.settings.payment.modeQRCode')
  if (props.provider.payment_mode === PAYMENT_MODE_POPUP) return t('admin.settings.payment.modePopup')
  if (props.provider.payment_mode === PAYMENT_MODE_REDIRECT) return t('admin.settings.payment.modeRedirect')
  return ''
})

function isSelected(type: string): boolean {
  return Array.isArray(props.provider.supported_types) && props.provider.supported_types.includes(type)
}
</script>
