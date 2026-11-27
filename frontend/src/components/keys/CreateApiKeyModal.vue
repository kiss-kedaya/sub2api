<template>
  <BaseDialog
    :show="show"
    :title="t('keys.createKey')"
    :description="dialogDescription"
    width="normal"
    @close="closeModal"
  >
    <form id="docs-create-key-form" class="space-y-5" data-testid="create-api-key-modal-form" @submit.prevent="handleSubmit">
      <QuietStepHead
        v-if="requiresCreateVerification"
        :current="createStep"
        :total="2"
        :label="t('auth.stepProgress', { current: createStep, total: 2 })"
        data-test="key-create-steps"
      />

      <div v-show="createStep === 1" class="key-step-pane space-y-5">
        <UFormField
          :label="t('keys.nameLabel')"
          required
          size="lg"
          :error="nameError"
          data-test="key-field-name"
        >
          <UInput
            ref="nameInputRef"
            v-model="formData.name"
            type="text"
            name="api-key-name"
            autocomplete="off"
            data-lpignore="true"
            data-1p-ignore="true"
            required
            :color="nameError ? 'error' : undefined"
            :highlight="!!nameError"
            size="lg"
            :ui="{ base: 'py-[11px]' }"
            class="w-full"
            :placeholder="t('keys.namePlaceholder')"
            data-testid="create-key-name-input"
          />
        </UFormField>

        <fieldset v-if="groups.length > 0">
          <legend class="mb-1.5 text-sm font-medium text-default">{{ t('keys.providerLabel') }}</legend>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <label
              v-for="provider in keyProviders"
              :key="provider.value"
              class="relative min-w-0"
            >
              <input
                type="radio"
                name="docs-key-provider"
                :value="provider.value"
                :checked="activeKeyProvider === provider.value"
                :disabled="!availableKeyProviders.has(provider.value)"
                class="peer sr-only"
                @change="selectKeyProvider(provider.value)"
              />
              <span class="flex min-h-20 flex-col items-center justify-center gap-1.5 rounded-lg border border-accented bg-default px-2 py-2.5 text-xs font-semibold text-highlighted transition-colors peer-enabled:cursor-pointer hover:peer-enabled:border-inverted peer-checked:border-inverted peer-checked:bg-elevated peer-checked:ring-1 peer-checked:ring-inverted peer-disabled:cursor-not-allowed peer-disabled:opacity-40 dark:border-dark-500 dark:bg-transparent dark:text-gray-100 dark:peer-checked:border-primary dark:peer-checked:bg-primary/10 dark:peer-checked:ring-primary">
                <span class="flex h-7 items-center justify-center gap-1" aria-hidden="true">
                  <span
                    v-for="platform in provider.icons"
                    :key="platform"
                    :class="['flex h-7 w-7 items-center justify-center rounded-lg', platformBadgeLightClass(platform)]"
                  >
                    <PlatformIcon :platform="platform" size="md" />
                  </span>
                </span>
                <span class="max-w-full wrap-break-word text-center">{{ provider.label }}</span>
              </span>
            </label>
          </div>
        </fieldset>

        <UFormField
          :label="t('keys.groupLabel')"
          :required="groupIsRequired"
          size="lg"
          :error="groupError"
          data-test="key-field-group"
        >
          <template #hint>
            <USwitch
              v-model="formData.smart_routing"
              :label="t('keys.smartRouting')"
              size="sm"
              data-test="key-smart-routing"
            />
          </template>
          <p v-if="formData.smart_routing" class="mb-3 text-xs text-muted">
            {{ t('keys.smartRoutingHint') }}
          </p>

          <div v-if="formData.smart_routing" class="space-y-3" data-test="smart-routing-editor">
            <div
              v-if="formData.group_ids.length > 0"
              class="flex items-center gap-2 rounded-lg bg-elevated/40 px-3 py-2 ring ring-default"
              data-test="smart-route-primary"
            >
              <span class="shrink-0 text-xs font-medium text-muted">
                {{ t('keys.smartRoutingPrimaryRoute') }}
              </span>
              <div class="min-w-0 flex-1">
                <GroupBadge
                  v-if="smartRouteBadgeProps(formData.group_ids[0])"
                  v-bind="smartRouteBadgeProps(formData.group_ids[0])!"
                />
                <span v-else class="text-sm text-muted">#{{ formData.group_ids[0] }}</span>
              </div>
              <UButton
                icon="i-lucide-arrow-down"
                type="button"
                color="neutral"
                variant="ghost"
                size="xs"
                square
                :disabled="formData.group_ids.length < 2"
                :aria-label="t('keys.smartRoutingMoveDown')"
                @click="moveSmartRoute(0, 1)"
              />
              <UButton
                icon="i-lucide-x"
                type="button"
                color="error"
                variant="ghost"
                size="xs"
                square
                :aria-label="t('keys.smartRoutingRemove')"
                @click="removeSmartRoute(0)"
              />
            </div>

            <UCollapsible v-if="formData.group_ids.length > 1" v-model:open="smartRoutesOpen">
              <UButton
                type="button"
                color="neutral"
                variant="outline"
                block
                trailing-icon="i-lucide-chevron-down"
                :label="t('keys.smartRoutingOtherRoutes', { count: formData.group_ids.length - 1 })"
                :ui="{ trailingIcon: 'transition-transform duration-200 group-data-[state=open]:rotate-180' }"
              />
              <template #content>
                <div class="mt-2 space-y-2">
                  <div
                    v-for="(groupId, offset) in formData.group_ids.slice(1)"
                    :key="`${groupId}-${offset + 1}`"
                    class="flex items-center gap-2 rounded-lg px-3 py-2 ring ring-default"
                  >
                    <span class="shrink-0 text-xs text-muted">{{ t('keys.smartRoutingRoute') }} {{ offset + 2 }}</span>
                    <div class="min-w-0 flex-1">
                      <GroupBadge
                        v-if="smartRouteBadgeProps(groupId)"
                        v-bind="smartRouteBadgeProps(groupId)!"
                      />
                      <span v-else class="text-sm text-muted">#{{ groupId }}</span>
                    </div>
                    <UButton
                      icon="i-lucide-arrow-up"
                      type="button"
                      color="neutral"
                      variant="ghost"
                      size="xs"
                      square
                      :aria-label="t('keys.smartRoutingMoveUp')"
                      @click="moveSmartRoute(offset + 1, -1)"
                    />
                    <UButton
                      icon="i-lucide-arrow-down"
                      type="button"
                      color="neutral"
                      variant="ghost"
                      size="xs"
                      square
                      :aria-label="t('keys.smartRoutingMoveDown')"
                      @click="moveSmartRoute(offset + 1, 1)"
                    />
                    <UButton
                      icon="i-lucide-x"
                      type="button"
                      color="error"
                      variant="ghost"
                      size="xs"
                      square
                      :aria-label="t('keys.smartRoutingRemove')"
                      @click="removeSmartRoute(offset + 1)"
                    />
                  </div>
                </div>
              </template>
            </UCollapsible>

            <Select
              :model-value="null"
              :options="smartRoutingAddOptions"
              :placeholder="t('keys.smartRoutingAdd')"
              :error="!!groupError"
              :searchable="true"
              :search-placeholder="t('keys.searchGroup')"
              @update:model-value="addSmartRoute"
            >
              <template #option="{ option, selected }">
                <GroupOptionItem v-bind="groupOptionRowProps(option as unknown as GroupOption, selected)" />
              </template>
            </Select>
            <p v-if="formData.group_ids.length === 0" class="text-xs text-muted">{{ t('keys.smartRoutingEmpty') }}</p>
          </div>

          <Select
            v-else
            v-model="formData.group_id"
            :options="providerGroupOptions"
            :placeholder="t('keys.selectGroup')"
            :error="!!groupError"
            :searchable="true"
            :search-placeholder="t('keys.searchGroup')"
            data-testid="create-key-group-select"
          >
            <template #selected="{ option }">
              <GroupBadge
                v-if="option"
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :subscription-type="(option as unknown as GroupOption).subscriptionType"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                :peak-start="(option as unknown as GroupOption).peakStart"
                :peak-end="(option as unknown as GroupOption).peakEnd"
                :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
              />
              <span v-else class="text-dimmed">{{ t('keys.selectGroup') }}</span>
            </template>
            <template #option="{ option, selected }">
              <GroupOptionItem v-bind="groupOptionRowProps(option as unknown as GroupOption, selected)" />
            </template>
          </Select>

          <p
            v-if="groupsLoaded && !hasAvailableGroups"
            class="mt-2 text-xs text-amber-600 dark:text-amber-400"
            data-test="key-no-available-groups-hint"
          >
            {{ t('keys.createWithoutGroupHint') }}
          </p>
        </UFormField>

        <button
          type="button"
          class="key-more-toggle"
          :aria-expanded="advancedOpen"
          aria-controls="docs-key-advanced-settings"
          data-test="key-advanced-toggle"
          @click="advancedOpen = !advancedOpen"
        >
          <UIcon
            name="i-lucide-chevron-down"
            class="key-more-chevron size-4 shrink-0"
            :class="advancedOpen ? 'rotate-180' : ''"
          />
          <span class="shrink-0 whitespace-nowrap font-medium text-highlighted">{{ t('keys.createStep.moreSettings') }}</span>
          <span class="min-w-0 truncate text-muted">{{ t('keys.createStep.moreSettingsHint') }}</span>
        </button>

        <Transition
          :css="false"
          @enter="onAdvancedEnter"
          @leave="onAdvancedLeave"
          @enter-cancelled="cancelCollapseMotion"
          @leave-cancelled="cancelCollapseMotion"
        >
          <div
            v-show="advancedOpen"
            id="docs-key-advanced-settings"
            class="space-y-4"
            data-test="key-advanced-settings"
          >
            <div class="space-y-3">
              <div class="flex items-center justify-between gap-4">
                <span id="docs-custom-key-label" class="text-sm font-medium text-default">{{ t('keys.customKeyLabel') }}</span>
                <Toggle v-model="formData.use_custom_key" aria-labelledby="docs-custom-key-label" />
              </div>
              <UFormField
                v-if="formData.use_custom_key"
                size="lg"
                :error="customKeyError || undefined"
                :help="customKeyError ? undefined : t('keys.customKeyHint')"
              >
                <UInput
                  v-model="formData.custom_key"
                  type="text"
                  size="lg"
                  class="w-full"
                  :ui="{ base: 'font-mono py-[11px]' }"
                  :placeholder="t('keys.customKeyPlaceholder')"
                />
              </UFormField>
            </div>

            <UFormField :label="t('keys.quotaLimit')" :help="t('keys.quotaAmountHint')" size="lg">
              <UInput
                v-model.number="formData.quota"
                type="number"
                step="0.01"
                min="0"
                size="lg"
                :ui="{ base: 'py-[11px]' }"
                class="w-full"
                :placeholder="t('keys.quotaAmountPlaceholder')"
              >
                <template #leading><span class="text-sm text-muted">$</span></template>
              </UInput>
            </UFormField>

            <div class="space-y-3">
              <div class="flex items-center justify-between gap-4">
                <span id="docs-expiration-label" class="text-sm font-medium text-default">{{ t('keys.expiration') }}</span>
                <Toggle v-model="formData.enable_expiration" aria-labelledby="docs-expiration-label" />
              </div>
              <div v-if="formData.enable_expiration" class="space-y-3">
                <div class="flex flex-wrap gap-2">
                  <UButton
                    v-for="days in ['7', '30', '90']"
                    :key="days"
                    type="button"
                    :color="formData.expiration_preset === days ? 'primary' : 'neutral'"
                    :variant="formData.expiration_preset === days ? 'soft' : 'outline'"
                    @click="setExpirationDays(parseInt(days, 10))"
                  >
                    {{ t('keys.expiresInDays', { days }) }}
                  </UButton>
                </div>
                <UFormField :label="t('keys.expirationDate')" :help="t('keys.expirationDateHint')" size="lg">
                  <UInput
                    v-model="formData.expiration_date"
                    type="datetime-local"
                    size="lg"
                    class="w-full"
                    :ui="{ base: 'dark:[color-scheme:dark] py-[11px]' }"
                  />
                </UFormField>
              </div>
            </div>
          </div>
        </Transition>
      </div>

      <div v-if="isCreateVerificationStep" class="key-step-pane space-y-3" data-test="key-create-verification">
        <div
          v-if="verificationFactorsLoading"
          class="rounded-lg bg-gray-50 p-3 text-xs text-muted ring ring-inset ring-default dark:bg-dark-900"
          data-test="key-create-factors-loading"
        >
          {{ t('keys.reveal.factorsLoading') }}
        </div>
        <KeyFactorVerification
          v-else
          ref="createVerificationRef"
          :factors="verificationFactors"
          mode="create"
          hide-description
          :loading="submitting"
        />
      </div>
    </form>

    <template #footer>
      <div class="flex items-center justify-between gap-3" data-test="key-create-footer">
        <button
          v-if="isCreateVerificationStep"
          type="button"
          class="auth-step-back"
          :disabled="submitting"
          data-test="key-create-back"
          @click="createStep = 1"
        >
          {{ t('keys.createStep.back') }}
        </button>
        <button
          v-else
          type="button"
          class="auth-step-back"
          data-test="key-create-cancel"
          @click="closeModal"
        >
          {{ t('common.cancel') }}
        </button>

        <UButton
          v-if="createStep === 1 && requiresCreateVerification"
          type="button"
          size="lg"
          :ui="quietPrimaryUi"
          :disabled="submitting || verificationFactorsLoading"
          data-test="key-create-next"
          @click="goToCreateVerification"
        >
          {{ t('keys.createStep.next') }}
        </UButton>
        <UButton
          v-else
          form="docs-create-key-form"
          type="submit"
          size="lg"
          :ui="quietPrimaryUi"
          :loading="submitting"
          :disabled="submitting || verificationFactorsLoading || (isCreateVerificationStep && !requiresCreateVerification)"
          data-testid="create-key-submit-btn"
        >
          {{ submitting ? t('keys.saving') : t('common.create') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import QuietStepHead from '@/components/common/QuietStepHead.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import KeyFactorVerification from '@/components/keys/KeyFactorVerification.vue'
import { quietPrimaryUi } from '@/components/keys/quietUi'
import { keysAPI, userGroupsAPI } from '@/api'
import { useAppStore, useAuthStore } from '@/stores'
import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'
import { needsKeyVerification } from '@/utils/apiKeyVerification'
import { accountLacksLoginPassword } from '@/utils/accountPassword'
import { platformBadgeLightClass } from '@/utils/platformColors'
import { cancelCollapseMotion, collapseHeight, expandHeight } from '@/utils/collapseMotion'
import type {
  ApiKey,
  ApiKeyVerificationFactors,
  ApiKeyVerificationPayload,
  Group,
  GroupPlatform,
  SubscriptionType,
} from '@/types'
import type { GroupSignalSource } from '@/utils/groupRateSignals'

export interface CreatedApiKeyPayload {
  key: ApiKey
  secret: string
  canSeal: boolean
  authFactor: string
  verifiedPassword: string
}

const props = withDefaults(
  defineProps<{
    show: boolean
    defaultName?: string
    preferredPlatform?: GroupPlatform | null
  }>(),
  {
    defaultName: '',
    preferredPlatform: null,
  },
)

const emit = defineEmits<{
  close: []
  created: [payload: CreatedApiKeyPayload]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const ephemeralKeys = useEphemeralKeyStore()

type GroupOption = {
  value: number
  label: string
  description: string | null
  rate: number
  userRate: number | null
  peakRateEnabled: boolean
  peakStart: string
  peakEnd: string
  peakRateMultiplier: number
  subscriptionType: SubscriptionType
  platform: GroupPlatform
  signals: GroupSignalSource
  showRateChange: boolean
}

type KeyProvider = 'anthropic' | 'openai' | 'cn' | 'other'

const groups = ref<Group[]>([])
const groupsLoaded = ref(false)
const userGroupRates = ref<Record<number, number>>({})
const createStep = ref<1 | 2>(1)
const advancedOpen = ref(false)
const smartRoutesOpen = ref(false)
const submitting = ref(false)
const attemptedSubmit = ref(false)
const verificationFactors = ref<ApiKeyVerificationFactors | null>(null)
const verificationFactorsLoading = ref(false)
const createVerificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
const nameInputRef = ref<{ inputRef?: HTMLInputElement | null } | null>(null)
const selectedKeyProvider = ref<KeyProvider | null>(null)

const formData = ref({
  name: '',
  group_id: null as number | null,
  group_ids: [] as number[],
  smart_routing: false,
  use_custom_key: false,
  custom_key: '',
  quota: null as number | null,
  enable_expiration: false,
  expiration_preset: '30' as '7' | '30' | '90' | 'custom',
  expiration_date: '',
})

const canSealKeys = computed(() => !!authStore.user && !accountLacksLoginPassword(authStore.user))
const isCreateVerificationStep = computed(() => props.show && createStep.value === 2)
const requiresCreateVerification = computed(() => needsKeyVerification(verificationFactors.value))
const sealAvailableForCreate = computed(
  () => verificationFactors.value?.seal_available ?? canSealKeys.value,
)
const dialogDescription = computed(() =>
  isCreateVerificationStep.value ? t('keys.createStep.verifyHint') : t('keys.createStep.detailsHint'),
)

const hasAvailableGroups = computed(() => groups.value.length > 0)
const groupIsRequired = computed(() => !(groupsLoaded.value && !hasAvailableGroups.value))
const nameMissing = computed(() => formData.value.name.trim() === '')
const groupMissing = computed(() => {
  if (!groupIsRequired.value) return false
  return formData.value.smart_routing ? formData.value.group_ids.length === 0 : formData.value.group_id === null
})
const nameError = computed(() => (attemptedSubmit.value && nameMissing.value ? t('keys.nameRequired') : undefined))
const groupError = computed(() => (attemptedSubmit.value && groupMissing.value ? t('keys.groupRequired') : undefined))

const customKeyError = computed(() => {
  if (!formData.value.use_custom_key || !formData.value.custom_key) return ''
  const key = formData.value.custom_key
  if (key.length < 16) return t('keys.customKeyTooShort')
  if (!/^[a-zA-Z0-9_-]+$/.test(key)) return t('keys.customKeyInvalidChars')
  return ''
})

const keyProviders = computed<Array<{ value: KeyProvider; label: string; icons: GroupPlatform[] }>>(() => [
  { value: 'anthropic', label: 'Anthropic', icons: ['anthropic'] },
  { value: 'openai', label: 'OpenAI', icons: ['openai'] },
  { value: 'cn', label: t('keys.providerCN'), icons: ['deepseek', 'kimi'] },
  { value: 'other', label: t('keys.providerOther'), icons: ['gemini', 'grok'] },
])

const keyProviderForPlatform = (platform: GroupPlatform): KeyProvider => {
  switch (platform) {
    case 'anthropic':
    case 'openai':
      return platform
    case 'kimi':
    case 'zhipu':
    case 'deepseek':
    case 'minimax':
      return 'cn'
    default:
      return 'other'
  }
}

const groupOptions = computed<GroupOption[]>(() =>
  groups.value.map((group) => ({
    value: group.id,
    label: group.name,
    description: group.description,
    rate: group.rate_multiplier,
    userRate: userGroupRates.value[group.id] ?? null,
    peakRateEnabled: group.peak_rate_enabled,
    peakStart: group.peak_start,
    peakEnd: group.peak_end,
    peakRateMultiplier: group.peak_rate_multiplier,
    subscriptionType: group.subscription_type,
    platform: group.platform,
    signals: group,
    showRateChange: group.subscription_type !== 'subscription',
  })),
)

const groupOptionById = (id: number) => groupOptions.value.find((opt) => opt.value === id)
const availableKeyProviders = computed(() => new Set(groups.value.map((group) => keyProviderForPlatform(group.platform))))
const activeKeyProvider = computed(() => {
  if (selectedKeyProvider.value) return selectedKeyProvider.value
  const group = groupOptions.value.find((option) => option.value === formData.value.group_id)
  if (group?.platform) return keyProviderForPlatform(group.platform)
  return keyProviders.value.find((provider) => availableKeyProviders.value.has(provider.value))?.value ?? 'anthropic'
})
const providerGroupOptions = computed(() =>
  groupOptions.value.filter((option) => keyProviderForPlatform(option.platform) === activeKeyProvider.value),
)

const selectKeyProvider = (provider: KeyProvider) => {
  selectedKeyProvider.value = provider
  if (!formData.value.smart_routing && !providerGroupOptions.value.some((option) => option.value === formData.value.group_id)) {
    formData.value.group_id = providerGroupOptions.value[0]?.value ?? null
    formData.value.group_ids = formData.value.group_id != null ? [formData.value.group_id] : []
  }
}

const groupOptionRowProps = (option: GroupOption, selected: boolean) => ({
  name: option.label,
  platform: option.platform,
  subscriptionType: option.subscriptionType,
  rateMultiplier: option.rate,
  userRateMultiplier: option.userRate,
  peakRateEnabled: option.peakRateEnabled,
  peakStart: option.peakStart,
  peakEnd: option.peakEnd,
  peakRateMultiplier: option.peakRateMultiplier,
  description: option.description,
  signals: option.signals,
  showRateChange: option.showRateChange,
  statusClass: null,
  selected,
})

const smartRouteBadgeProps = (groupId: number) => {
  const option = groupOptionById(groupId)
  if (!option) return null
  return {
    name: option.label,
    platform: option.platform,
    subscriptionType: option.subscriptionType,
    rateMultiplier: option.rate,
    userRateMultiplier: option.userRate,
    peakRateEnabled: option.peakRateEnabled,
    peakStart: option.peakStart,
    peakEnd: option.peakEnd,
    peakRateMultiplier: option.peakRateMultiplier,
  }
}

const smartRoutingAddOptions = computed(() => {
  const selected = new Set(formData.value.group_ids)
  return providerGroupOptions.value.filter((opt) => !selected.has(opt.value))
})

const addSmartRoute = (value: string | number | boolean | null) => {
  if (typeof value !== 'number' || formData.value.group_ids.includes(value)) return
  formData.value.group_ids = [...formData.value.group_ids, value]
  formData.value.group_id = formData.value.group_ids[0] ?? null
}

const removeSmartRoute = (index: number) => {
  formData.value.group_ids = formData.value.group_ids.filter((_, i) => i !== index)
  formData.value.group_id = formData.value.group_ids[0] ?? null
}

const moveSmartRoute = (index: number, delta: number) => {
  const next = index + delta
  if (next < 0 || next >= formData.value.group_ids.length) return
  const copy = [...formData.value.group_ids]
  const [item] = copy.splice(index, 1)
  copy.splice(next, 0, item)
  formData.value.group_ids = copy
  formData.value.group_id = copy[0] ?? null
}

watch(
  () => formData.value.smart_routing,
  (enabled) => {
    if (enabled) {
      if (formData.value.group_ids.length === 0 && formData.value.group_id != null) {
        formData.value.group_ids = [formData.value.group_id]
      }
      return
    }
    formData.value.group_ids = formData.value.group_id != null ? [formData.value.group_id] : []
    selectedKeyProvider.value = null
  },
)

const setExpirationDays = (days: number) => {
  formData.value.expiration_preset = days === 7 ? '7' : days === 30 ? '30' : days === 90 ? '90' : 'custom'
  const date = new Date(Date.now() + days * 86_400_000)
  const pad = (n: number) => n.toString().padStart(2, '0')
  formData.value.expiration_date = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const onAdvancedEnter = (el: Element, done: () => void) => (!props.show ? done() : expandHeight(el, done))
const onAdvancedLeave = (el: Element, done: () => void) => (!props.show ? done() : collapseHeight(el, done))

const applyBeginnerDefaults = () => {
  formData.value.name = props.defaultName.trim() || t('docs.agentSetup.createKeyDefaultName')
  selectedKeyProvider.value = null
  if (groups.value.length === 0) {
    formData.value.smart_routing = false
    formData.value.group_id = null
    formData.value.group_ids = []
    return
  }

  if (props.preferredPlatform) {
    const match = groups.value.find((g) => g.platform === props.preferredPlatform) ?? groups.value[0]
    formData.value.smart_routing = false
    formData.value.group_id = match.id
    formData.value.group_ids = [match.id]
    selectedKeyProvider.value = keyProviderForPlatform(match.platform)
    return
  }

  const seenPlatforms = new Set<string>()
  const representativeIds: number[] = []
  for (const g of groups.value) {
    if (!seenPlatforms.has(g.platform)) {
      seenPlatforms.add(g.platform)
      representativeIds.push(g.id)
    }
  }
  if (representativeIds.length > 1) {
    formData.value.smart_routing = true
    formData.value.group_ids = representativeIds
    formData.value.group_id = representativeIds[0]
  } else {
    formData.value.smart_routing = false
    formData.value.group_id = groups.value[0].id
    formData.value.group_ids = [groups.value[0].id]
  }
}

const loadModalData = async () => {
  verificationFactorsLoading.value = true
  try {
    const [availGroups, rates, factors] = await Promise.all([
      Promise.resolve(userGroupsAPI.getAvailable()).catch(() => [] as Group[]),
      typeof userGroupsAPI.getUserGroupRates === 'function'
        ? Promise.resolve(userGroupsAPI.getUserGroupRates()).catch(() => ({}))
        : Promise.resolve({}),
      Promise.resolve(keysAPI.getVerificationFactors()).catch(() => ({
        password_available: canSealKeys.value,
        totp_enabled: false,
        passkey_count: 0,
        preferred: canSealKeys.value ? 'password' : '',
        available_methods: canSealKeys.value ? (['password'] as const) : [],
        seal_available: canSealKeys.value,
        create_verification_required: false,
      })),
    ])
    groups.value = Array.isArray(availGroups) ? availGroups : []
    groupsLoaded.value = true
    userGroupRates.value = (rates && typeof rates === 'object' ? rates : {}) as Record<number, number>
    verificationFactors.value = factors as ApiKeyVerificationFactors
    applyBeginnerDefaults()
  } finally {
    verificationFactorsLoading.value = false
  }
}

const resetForm = () => {
  createStep.value = 1
  advancedOpen.value = false
  smartRoutesOpen.value = false
  attemptedSubmit.value = false
  submitting.value = false
  createVerificationRef.value?.reset()
  formData.value = {
    name: props.defaultName.trim() || t('docs.agentSetup.createKeyDefaultName'),
    group_id: null,
    group_ids: [],
    smart_routing: false,
    use_custom_key: false,
    custom_key: '',
    quota: null,
    enable_expiration: false,
    expiration_preset: '30',
    expiration_date: '',
  }
  selectedKeyProvider.value = null
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      resetForm()
      void loadModalData()
    }
  },
  { immediate: true },
)

const closeModal = () => {
  if (submitting.value) return
  emit('close')
}

const validateBasics = (): boolean => {
  attemptedSubmit.value = true
  if (nameMissing.value || groupMissing.value) return false
  if (formData.value.smart_routing && formData.value.group_ids.length > 0) {
    formData.value.group_id = formData.value.group_ids[0] ?? null
  }
  if (formData.value.use_custom_key) {
    if (!formData.value.custom_key) {
      appStore.showError(t('keys.customKeyRequired'))
      return false
    }
    if (customKeyError.value) {
      appStore.showError(customKeyError.value)
      return false
    }
  }
  return true
}

const goToCreateVerification = () => {
  if (!validateBasics()) return
  createStep.value = 2
}

const verificationErrorText = (reason?: string): string =>
  reason === 'VERIFY_PASSWORD_INVALID'
    ? t('keys.factor.passwordInvalid')
    : reason === 'VERIFY_TOTP_INVALID'
      ? t('keys.factor.totpInvalid')
      : reason === 'VERIFY_PASSKEY_FAILED'
        ? t('keys.factor.passkeyFailed')
        : reason === 'VERIFY_FACTOR_UNAVAILABLE'
          ? t('keys.factor.unavailable')
          : reason === 'VERIFY_REQUIRED'
            ? t('keys.factor.requiredAll')
            : reason === 'VERIFY_PASSWORD_RATE_LIMITED'
              ? t('keys.factor.passwordRateLimited')
              : t('keys.failedToSave')

const handleSubmit = async () => {
  if (!validateBasics()) return

  let verification: ApiKeyVerificationPayload | undefined
  if (requiresCreateVerification.value) {
    if (createStep.value !== 2) {
      createStep.value = 2
      return
    }
    const built = createVerificationRef.value?.buildVerification() ?? null
    if (!built) return
    verification = built
  }

  const quota = formData.value.quota && formData.value.quota > 0 ? formData.value.quota : 0
  let expiresInDays: number | undefined
  if (formData.value.enable_expiration && formData.value.expiration_date) {
    const expDate = new Date(formData.value.expiration_date)
    const diffDays = Math.ceil((expDate.getTime() - Date.now()) / 86_400_000)
    expiresInDays = diffDays > 0 ? diffDays : 1
  }

  submitting.value = true
  try {
    const customKey = formData.value.use_custom_key ? formData.value.custom_key : undefined
    const created = await keysAPI.create(
      formData.value.name.trim(),
      formData.value.group_id,
      customKey,
      [],
      [],
      quota,
      expiresInDays,
      { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 },
      formData.value.smart_routing ? formData.value.group_ids : undefined,
      'inherit',
      {
        deepseekToolInjectionEnabled: true,
        ...(verification ? { verification } : {}),
      },
    )

    const { key: createdSecret, ...createdMasked } = created
    const primaryGroupId =
      (Array.isArray(createdMasked.group_ids) && createdMasked.group_ids[0]) ||
      createdMasked.group_id ||
      formData.value.group_id
    const resolvedGroup =
      createdMasked.group ??
      (primaryGroupId != null ? groups.value.find((g) => g.id === primaryGroupId) : undefined)
    const normalizedKey: ApiKey = {
      ...(createdMasked as ApiKey),
      group: resolvedGroup,
      group_ids:
        Array.isArray(createdMasked.group_ids) && createdMasked.group_ids.length > 0
          ? createdMasked.group_ids
          : formData.value.smart_routing && formData.value.group_ids.length > 0
            ? formData.value.group_ids
            : primaryGroupId != null
              ? [primaryGroupId]
              : [],
    }

    if (createdSecret) {
      ephemeralKeys.remember(normalizedKey.id, createdSecret)
      appStore.showSuccess(t('keys.keyCreatedSuccess'))
      const verifiedPassword =
        verification && typeof verification === 'object' && verification.method === 'password'
          ? (verification.password ?? '')
          : ''
      emit('created', {
        key: normalizedKey,
        secret: createdSecret,
        canSeal: sealAvailableForCreate.value,
        authFactor: verification?.method ?? '',
        verifiedPassword,
      })
    } else {
      appStore.showSuccess(t('keys.keyCreatedReplaySuccess'))
    }
    emit('close')
  } catch (error: unknown) {
    const err = error as { reason?: string; response?: { data?: { detail?: string } } }
    const reason = err?.reason
    const isVerifyReason =
      reason === 'VERIFY_PASSWORD_INVALID' ||
      reason === 'VERIFY_TOTP_INVALID' ||
      reason === 'VERIFY_PASSKEY_FAILED' ||
      reason === 'VERIFY_FACTOR_UNAVAILABLE' ||
      reason === 'VERIFY_REQUIRED' ||
      reason === 'VERIFY_PASSWORD_RATE_LIMITED'
    const errorMsg = isVerifyReason
      ? verificationErrorText(reason)
      : reason === 'API_KEY_TOO_PREDICTABLE'
        ? t('keys.customKeyTooPredictable')
        : reason === 'API_KEY_GROUP_REQUIRED'
          ? t('keys.groupRequiredByServer')
          : err?.response?.data?.detail || t('keys.failedToSave')
    appStore.showError(errorMsg)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.key-more-toggle {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.5rem;
  border-radius: 0.5rem;
  padding: 0.5rem 0.25rem;
  text-align: left;
  font-size: 0.875rem;
  transition: background-color 150ms ease;
}

.key-more-toggle:hover {
  background-color: var(--ui-bg-elevated);
}

.key-more-chevron {
  transition: transform 200ms cubic-bezier(0.23, 1, 0.32, 1);
}
</style>
