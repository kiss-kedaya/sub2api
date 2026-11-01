<template>
  <SettingsSection
    :title="t('admin.settings.payment.providerManagement')"
    :description="t('admin.settings.payment.providerManagementDesc')"
  >
    <template #actions>
      <UButton
        icon="i-lucide-refresh-cw"
        color="neutral"
        variant="outline"
        square
        :loading="loading"
        :disabled="loading"
        :title="t('common.refresh')"
        :aria-label="t('common.refresh')"
        @click="emit('refresh')"
      />
      <UButton icon="i-lucide-plus" :disabled="!canCreate" data-testid="provider-create" @click="emit('create')">
        {{ t('admin.settings.payment.createProvider') }}
      </UButton>
    </template>

    <!-- Loading -->
    <div v-if="loading && !providers.length" class="flex items-center justify-center py-4">
      <UIcon name="i-lucide-loader-circle" class="size-5 animate-spin text-muted" />
    </div>

    <!-- Provider rows (draggable) -->
    <VueDraggable
      v-if="providers.length"
      v-model="localProviders"
      :animation="200"
      handle=".drag-handle"
      class="divide-y divide-default"
      @end="onDragEnd"
    >
      <div v-for="p in localProviders" :key="p.id" class="flex items-center gap-2 py-3 first:pt-0 last:pb-0">
        <UIcon
          name="i-lucide-grip-vertical"
          class="drag-handle size-4 shrink-0 cursor-grab text-dimmed hover:text-muted active:cursor-grabbing"
        />
        <div class="min-w-0 flex-1">
          <ProviderCard
            :provider="p"
            :enabled="isEnabled(p.provider_key)"
            :available-types="getTypes(p.provider_key)"
            @toggle-field="(field) => emit('toggleField', p, field)"
            @toggle-type="(type) => emit('toggleType', p, type)"
            @edit="emit('edit', p)"
            @delete="emit('delete', p)"
          />
        </div>
      </div>
    </VueDraggable>

    <!-- Empty -->
    <p v-else-if="!loading" class="py-2 text-center text-sm text-muted">
      {{ canCreate ? t('admin.settings.payment.noProviders') : t('admin.settings.payment.enableTypesFirst') }}
    </p>
  </SettingsSection>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { VueDraggable } from 'vue-draggable-plus'
import SettingsSection from '@/views/admin/settings/SettingsSection.vue'
import ProviderCard from './ProviderCard.vue'
import type { ProviderInstance } from '@/types/payment'
import type { TypeOption } from './providerConfig'
import { getAvailableTypes } from './providerConfig'

const props = defineProps<{
  providers: ProviderInstance[]
  loading: boolean
  canCreate: boolean
  enabledPaymentTypes: string[]
  allPaymentTypes: TypeOption[]
  redirectLabel: string
}>()

const emit = defineEmits<{
  refresh: []
  create: []
  edit: [provider: ProviderInstance]
  delete: [provider: ProviderInstance]
  toggleField: [provider: ProviderInstance, field: 'enabled' | 'refund_enabled' | 'allow_user_refund']
  toggleType: [provider: ProviderInstance, type: string]
  reorder: [providers: { id: number; sort_order: number }[]]
}>()

const { t } = useI18n()

const localProviders = ref<ProviderInstance[]>([])

watch(() => props.providers, (val) => {
  localProviders.value = [...val]
}, { immediate: true })

function onDragEnd() {
  const updates = localProviders.value.map((p, idx) => ({
    id: p.id,
    sort_order: idx,
  }))
  emit('reorder', updates)
}

function isEnabled(providerKey: string): boolean {
  return props.enabledPaymentTypes.includes(providerKey)
}

function getTypes(providerKey: string): TypeOption[] {
  return getAvailableTypes(providerKey, props.allPaymentTypes, props.redirectLabel)
    .map(opt => opt.label === opt.value
      ? { ...opt, label: t(`payment.methods.${opt.value}`, opt.value) }
      : opt,
    )
}
</script>
