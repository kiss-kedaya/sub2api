<template>
  <section
    ref="rootRef"
    data-testid="docs-apikey-bar"
    class="rounded-xl border border-slate-200/80 bg-white p-4 shadow-(--app-shadow-card) transition dark:border-dark-700/80 dark:bg-dark-900 sm:p-5"
  >
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 flex-1 items-start gap-3">
        <span
          class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl transition-colors"
          :class="hasKey
            ? 'bg-primary/10 text-primary'
            : 'bg-slate-100 text-slate-600 dark:bg-dark-800 dark:text-slate-300'"
        >
          <Icon :name="hasKey ? 'checkCircle' : 'key'" size="sm" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <span
              class="h-2 w-2 shrink-0 rounded-full"
              :class="hasKey ? 'bg-emerald-500' : 'bg-amber-500'"
              aria-hidden="true"
            />
            <h3 class="text-[15px] font-semibold text-slate-950 dark:text-slate-50">
              {{ hasKey ? t('docs.agentSetup.keyBarReadyTitle') : t('docs.agentSetup.keyBarEmptyTitle') }}
            </h3>
            <span
              v-if="hasKey && fetchedModelsCount > 0"
              data-testid="docs-apikey-models-badge"
              class="inline-flex items-center gap-1 rounded-md bg-emerald-50 px-2 py-0.5 text-[11.5px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300"
            >
              {{ t('docs.agentSetup.keyBarModelsLoaded', { count: fetchedModelsCount }) }}
            </span>
            <span
              v-else-if="hasKey && modelsLoading"
              class="inline-flex items-center gap-1 rounded-md bg-slate-100 px-2 py-0.5 text-[11.5px] text-slate-500 dark:bg-dark-800 dark:text-slate-400"
            >
              {{ t('docs.agentSetup.keyBarModelsLoading') }}
            </span>
          </div>
          <p class="mt-1 text-[13.5px] leading-6 text-slate-600 dark:text-slate-300">
            {{ hasKey ? t('docs.agentSetup.keyBarReadyDesc') : t('docs.agentSetup.keyBarEmptyDesc') }}
          </p>
        </div>
      </div>

      <!-- Top-right Action Bar: persistent primary 导入到 CCS CTA across all states -->
      <div class="flex shrink-0 flex-wrap items-center gap-2">
        <button
          v-if="hasKey || isAuthenticated"
          type="button"
          data-testid="docs-apikey-import-ccs-btn"
          class="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl bg-primary px-4 text-[13px] font-medium text-inverted shadow-xs transition hover:bg-primary/90"
          @click="triggerPersistentCcsImport"
        >
          <Icon name="bolt" size="xs" />
          <span>{{ t('docs.agentSetup.keyBarImportCcsBtn') }}</span>
        </button>
        <router-link
          v-else
          :to="loginRedirectLocation"
          data-testid="docs-apikey-import-ccs-btn"
          class="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl bg-primary px-4 text-[13px] font-medium text-inverted shadow-xs transition hover:bg-primary/90"
        >
          <Icon name="bolt" size="xs" />
          <span>{{ t('docs.agentSetup.keyBarImportCcsBtn') }}</span>
        </router-link>

        <template v-if="hasKey">
          <button
            v-if="isAuthenticated"
            type="button"
            data-testid="docs-apikey-create-btn"
            class="inline-flex h-10 items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-3.5 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-200 dark:hover:bg-dark-800"
            @click="openCreateModal"
          >
            <Icon name="plus" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarCreateNewBtn') }}</span>
          </button>
          <button
            type="button"
            data-testid="docs-apikey-toggle-mask"
            class="inline-flex h-10 items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-3.5 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-200 dark:hover:bg-dark-800"
            @click="emit('update:masked', !masked)"
          >
            <Icon :name="masked ? 'eye' : 'eyeOff'" size="xs" />
            <span>{{ masked ? t('docs.agentSetup.keyBarShowPlain') : t('docs.agentSetup.keyBarMaskDisplay') }}</span>
          </button>
          <button
            type="button"
            data-testid="docs-apikey-clear"
            class="inline-flex h-10 items-center gap-1 rounded-xl border border-slate-200 bg-white px-3.5 text-xs font-medium text-slate-600 transition hover:bg-slate-50 hover:text-slate-900 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-300 dark:hover:bg-dark-800"
            @click="clearKey"
          >
            <Icon name="x" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarClear') }}</span>
          </button>
        </template>
      </div>
    </div>

    <!-- Active key preview strip -->
    <div
      v-if="hasKey"
      class="mt-3.5 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-slate-200/80 bg-slate-50/75 px-3.5 py-2.5 dark:border-dark-700/80 dark:bg-dark-950/75"
    >
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <span v-if="activeKeyName" class="shrink-0 rounded-md bg-white px-2 py-0.5 text-xs font-medium text-slate-700 ring-1 ring-inset ring-slate-200/80 dark:bg-dark-800 dark:text-slate-200 dark:ring-dark-700">
          {{ activeKeyName }}
        </span>
        <code class="min-w-0 truncate font-mono text-[13px] text-slate-900 dark:text-slate-100" data-testid="docs-apikey-display">
          {{ masked ? maskedKeyText : modelValue }}
        </code>
      </div>
      <span class="shrink-0 text-[11.5px] text-slate-500 dark:text-slate-400">
        {{ t('docs.agentSetup.keyBarLocalOnlyNote') }}
      </span>
    </div>

    <!-- Key acquisition / unlock / paste controls when not yet filled -->
    <div v-else class="mt-4 space-y-3">
      <!-- Logged-in user with existing keys: Nuxt UI Select + unlock/create actions -->
      <div v-if="isAuthenticated && userKeys.length > 0" class="space-y-2">
        <div class="flex flex-wrap items-center gap-2.5">
          <div class="min-w-[220px] flex-1 [&_.select-menu-trigger]:h-10 [&_.select-menu-trigger]:rounded-xl">
            <label for="docs-user-key-select" class="sr-only">{{ t('docs.agentSetup.keyBarSelectLabel') }}</label>
            <Select
              id="docs-user-key-select"
              data-testid="docs-apikey-select"
              :model-value="selectedKeyId"
              :options="userKeySelectOptions"
              :placeholder="t('docs.agentSetup.keyBarSelectLabel')"
              :aria-label="t('docs.agentSetup.keyBarSelectLabel')"
              @change="onSelectKeyPicked"
            >
              <template #selected="{ option }">
                <span v-if="option" class="flex min-w-0 items-center gap-2">
                  <span class="truncate text-[13px] font-medium text-slate-900 dark:text-slate-100">
                    {{ option.label }}
                  </span>
                  <code class="hidden shrink-0 font-mono text-xs text-slate-500 dark:text-slate-400 sm:inline">
                    {{ option.keyMasked }}
                  </code>
                  <UBadge
                    :color="option.isUnlocked || option.hasSealedSecret ? 'success' : 'neutral'"
                    variant="soft"
                    size="xs"
                    :label="option.isUnlocked ? t('docs.agentSetup.keyBarUnlockedBadge') : option.hasSealedSecret ? t('docs.agentSetup.keyBarSealBadge') : t('docs.agentSetup.keyBarUnsealedBadge')"
                    class="shrink-0"
                  />
                </span>
                <span v-else class="text-dimmed">{{ t('docs.agentSetup.keyBarSelectLabel') }}</span>
              </template>
              <template #option="{ option }">
                <div v-if="option" class="flex w-full min-w-0 items-center justify-between gap-2.5">
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-[13px] font-medium text-slate-900 dark:text-slate-100">
                      {{ option.label }}
                    </div>
                    <div class="truncate font-mono text-[11.5px] text-slate-500 dark:text-slate-400">
                      {{ option.keyMasked }}
                    </div>
                  </div>
                  <UBadge
                    :color="option.isUnlocked || option.hasSealedSecret ? 'success' : 'neutral'"
                    variant="soft"
                    size="xs"
                    :label="option.isUnlocked ? t('docs.agentSetup.keyBarUnlockedBadge') : option.hasSealedSecret ? t('docs.agentSetup.keyBarSealBadge') : t('docs.agentSetup.keyBarUnsealedBadge')"
                    class="shrink-0"
                  />
                </div>
              </template>
            </Select>
          </div>
          <button
            v-if="selectedUserKey?.has_sealed_secret"
            type="button"
            data-testid="docs-apikey-unlock-btn"
            class="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-3.5 text-[13px] font-medium text-slate-800 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100 dark:hover:bg-dark-800"
            @click="openUnlockDialog"
          >
            <Icon name="lock" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarUnlockBtn') }}</span>
          </button>
          <button
            type="button"
            data-testid="docs-apikey-create-btn"
            class="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-3.5 text-[13px] font-medium text-slate-800 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100 dark:hover:bg-dark-800"
            @click="openCreateModal"
          >
            <Icon name="plus" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarCreateNewBtn') }}</span>
          </button>
          <router-link
            to="/keys"
            data-testid="docs-apikey-manage-link"
            class="inline-flex h-10 shrink-0 items-center gap-1 rounded-xl px-3 text-xs font-medium text-slate-500 transition hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
          >
            <span>{{ t('docs.agentSetup.keyBarManageLink') }}</span>
            <Icon name="arrowRight" size="xs" />
          </router-link>
        </div>
        <p
          v-if="selectedUserKey && !selectedUserKey.has_sealed_secret"
          class="text-xs text-slate-500 dark:text-slate-400"
        >
          {{ t('docs.agentSetup.keyBarUnsealedHint') }}
        </p>
      </div>

      <!-- Direct paste input (always available for both logged-in & guest users) -->
      <div class="flex flex-wrap items-center gap-2.5">
        <div class="relative min-w-[220px] flex-1">
          <!-- Use type="text" + CSS text-security instead of type="password" so browser password
               managers never classify this page as a login form and autofill the sidebar search box. -->
          <input
            ref="pasteInputRef"
            v-model="draftPasteKey"
            data-testid="docs-apikey-paste-input"
            type="text"
            name="docs-apikey-paste"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="off"
            spellcheck="false"
            data-lpignore="true"
            data-1p-ignore="true"
            data-bwignore="true"
            data-form-type="other"
            :placeholder="t('docs.agentSetup.keyBarPastePlaceholder')"
            :aria-label="t('docs.agentSetup.keyBarPastePlaceholder')"
            class="secret-mask-input h-10 w-full rounded-xl border border-slate-200 bg-slate-50/70 px-3.5 pr-20 font-mono text-[13px] text-slate-900 outline-hidden transition focus:border-primary focus:bg-white focus:ring-2 focus:ring-primary/20 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100"
            @keydown.enter.prevent="applyDraftPaste"
          />
          <button
            v-if="draftPasteKey.trim()"
            type="button"
            data-testid="docs-apikey-apply-btn"
            class="absolute right-1.5 top-1/2 h-7 -translate-y-1/2 rounded-lg bg-primary px-3 text-xs font-medium text-inverted transition hover:bg-primary/90"
            @click="applyDraftPaste"
          >
            {{ t('docs.agentSetup.keyBarApplyBtn') }}
          </button>
        </div>

        <router-link
          v-if="!isAuthenticated"
          :to="loginRedirectLocation"
          data-testid="docs-apikey-login-cta"
          class="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-4 text-[13px] font-medium text-slate-800 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100 dark:hover:bg-dark-800"
        >
          <Icon name="user" size="xs" />
          <span>{{ t('docs.agentSetup.keyBarLoginBtn') }}</span>
        </router-link>
        <template v-else-if="userKeys.length === 0 && !keysLoading">
          <button
            type="button"
            data-testid="docs-apikey-create-first-cta"
            class="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl border border-slate-200 bg-white px-4 text-[13px] font-medium text-slate-800 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100 dark:hover:bg-dark-800"
            @click="openCreateModal"
          >
            <Icon name="plus" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarCreateFirstBtn') }}</span>
          </button>
          <router-link
            to="/keys"
            data-testid="docs-apikey-manage-link"
            class="inline-flex h-10 shrink-0 items-center gap-1 rounded-xl px-3 text-xs font-medium text-slate-500 transition hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
          >
            <span>{{ t('docs.agentSetup.keyBarManageLink') }}</span>
          </router-link>
        </template>
      </div>
    </div>

    <!-- In-page local-decrypt RevealApiKeyDialog -->
    <RevealApiKeyDialog
      v-if="isAuthenticated"
      :show="showRevealDialog"
      :api-key="selectedUserKey"
      @revealed="onKeyRevealed"
      @import-ccs="onImportCcsFromReveal"
      @use="closeRevealDialog"
      @close="closeRevealDialog"
    />

    <!-- In-page CreateApiKeyModal -->
    <CreateApiKeyModal
      v-if="isAuthenticated"
      :show="showCreateModal"
      :default-name="defaultKeyName"
      :preferred-platform="preferredPlatform"
      @created="onKeyCreated"
      @close="closeCreateModal"
    />

    <!-- In-page ApiKeySecretDialog (supports sealing + Import to CCS) -->
    <ApiKeySecretDialog
      v-if="isAuthenticated"
      :show="showSecretDialog"
      :api-key="secretRow"
      :secret="secretPlaintext"
      :can-seal="secretCanSeal"
      :default-seal="false"
      :auth-factor="secretAuthFactor"
      :verified-password="secretVerifiedPassword"
      @close="closeSecretDialog"
      @use="closeSecretDialog"
      @import-ccs="onImportCcsFromSecret"
      @sealed="onSecretSealed"
    />

    <!-- Shared real ccswitch:// import modal -->
    <CcsImportModal
      :show="showCcsImportModal"
      :api-key="ccsImportSecret"
      :platform="ccsImportPlatform"
      :base-url="origin"
      :site-name="siteName"
      :default-app="ccsModalDefaultApp"
      :allowed-apps="ccsModalAllowedApps"
      @imported="onCcsImported"
      @close="closeCcsImportModal"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getActivePinia } from 'pinia'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import RevealApiKeyDialog from '@/components/keys/RevealApiKeyDialog.vue'
import CreateApiKeyModal, { type CreatedApiKeyPayload } from '@/components/keys/CreateApiKeyModal.vue'
import ApiKeySecretDialog from '@/components/keys/ApiKeySecretDialog.vue'
import CcsImportModal from '@/components/keys/CcsImportModal.vue'
import { keysAPI } from '@/api'
import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'
import { fetchCcSwitchModels, type CcSwitchApp } from '@/utils/ccswitchImport'
import type { ApiKey, GroupPlatform } from '@/types'

interface KeySelectOption extends SelectOption {
  value: number
  label: string
  description: string
  keyMasked: string
  hasSealedSecret: boolean
  isUnlocked: boolean
}

const props = withDefaults(
  defineProps<{
    origin: string
    isAuthenticated: boolean
    modelValue: string
    masked: boolean
    agentId?: string
    siteName?: string
    preferredPlatform?: GroupPlatform | null
    defaultKeyName?: string
    defaultCcsApp?: CcSwitchApp
    allowedCcsApps?: CcSwitchApp[]
  }>(),
  {
    agentId: '',
    siteName: 'sub2api',
    preferredPlatform: null,
    defaultKeyName: '',
    defaultCcsApp: undefined,
    allowedCcsApps: undefined,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:masked': [value: boolean]
  'models-loaded': [models: string[]]
  'key-meta': [meta: { name: string; platform?: GroupPlatform }]
  'ccs-import-opened': []
  'ccs-import-cancelled': []
  'ccs-imported': [payload: { app: CcSwitchApp; model: string; deeplink: string }]
  'key-flow-settled': []
}>()

const { t } = useI18n()
const ephemeralKeys = getActivePinia() ? useEphemeralKeyStore() : null

const rootRef = ref<HTMLElement | null>(null)
const pasteInputRef = ref<HTMLInputElement | null>(null)
const userKeys = ref<ApiKey[]>([])
const keysLoading = ref(false)
const selectedKeyId = ref<number | null>(null)
const activeKeyName = ref('')
const draftPasteKey = ref('')

const showRevealDialog = ref(false)
const revealedInCurrentDialog = ref(false)
const showCreateModal = ref(false)
const showSecretDialog = ref(false)
const secretRow = ref<ApiKey | null>(null)
const secretPlaintext = ref('')
const secretCanSeal = ref(false)
const secretAuthFactor = ref('')
const secretVerifiedPassword = ref('')

const showCcsImportModal = ref(false)
const pendingCcsImport = ref(false)
const ccsImportSucceeded = ref(false)
const ccsImportSecret = ref('')
const ccsImportPlatform = ref<GroupPlatform | null>(null)
const ccsImportDefaultOverride = ref<CcSwitchApp | undefined>(undefined)
const ccsImportAllowedOverride = ref<CcSwitchApp[] | undefined>(undefined)

const modelsLoading = ref(false)
const fetchedModelsCount = ref(0)
let modelsAbort: AbortController | null = null
let keysAbort: AbortController | null = null

const hasKey = computed(() => props.modelValue.trim().length > 0)

const userKeySelectOptions = computed<KeySelectOption[]>(() =>
  userKeys.value.map((item) => ({
    value: item.id,
    label: item.name,
    description: item.key_masked,
    keyMasked: item.key_masked,
    hasSealedSecret: Boolean(item.has_sealed_secret),
    isUnlocked: Boolean(ephemeralKeys?.has(item.id)),
  })),
)

const selectedUserKey = computed<ApiKey | null>(() => {
  if (selectedKeyId.value == null) return userKeys.value[0] ?? null
  return userKeys.value.find((item) => item.id === selectedKeyId.value) ?? userKeys.value[0] ?? null
})

const ccsModalDefaultApp = computed(() => ccsImportDefaultOverride.value ?? props.defaultCcsApp)
const ccsModalAllowedApps = computed(() => ccsImportAllowedOverride.value ?? props.allowedCcsApps)

const maskedKeyText = computed(() => {
  const val = props.modelValue.trim()
  if (val.length <= 12) return '••••••••••••'
  return `${val.slice(0, 7)}••••••••${val.slice(-4)}`
})

const loginRedirectLocation = computed(() => {
  const target = props.agentId
    ? `/docs?page=agent-setup&agent=${encodeURIComponent(props.agentId)}`
    : '/docs?page=agent-setup'
  return { path: '/login', query: { redirect: target } }
})

function applyRememberedKey(item: ApiKey, secret: string) {
  selectedKeyId.value = item.id
  activeKeyName.value = item.name
  emit('update:modelValue', secret)
  emit('key-meta', {
    name: item.name,
    platform: item.group?.platform,
  })
}

function syncActiveMetaFromModelValue(currentSecret: string) {
  if (!ephemeralKeys || !currentSecret) return
  const activeId = ephemeralKeys.activeKeyId
  const activeMatch =
    activeId != null && ephemeralKeys.recall(activeId) === currentSecret
      ? userKeys.value.find((item) => item.id === activeId)
      : undefined
  const matchedItem =
    activeMatch ?? userKeys.value.find((item) => ephemeralKeys.recall(item.id) === currentSecret)
  if (matchedItem) {
    selectedKeyId.value = matchedItem.id
    activeKeyName.value = matchedItem.name
    emit('key-meta', {
      name: matchedItem.name,
      platform: matchedItem.group?.platform,
    })
  }
}

function tryAutoFillFromStore() {
  const existing = props.modelValue.trim()
  if (existing) {
    syncActiveMetaFromModelValue(existing)
    return
  }
  if (!ephemeralKeys || ephemeralKeys.autoFillSuppressed) return
  const activeId = ephemeralKeys.activeKeyId
  if (activeId != null) {
    const activeSecret = ephemeralKeys.recall(activeId)
    const activeItem = userKeys.value.find((item) => item.id === activeId)
    if (activeSecret && activeItem) {
      applyRememberedKey(activeItem, activeSecret)
      return
    }
    if (activeSecret && !props.isAuthenticated) {
      emit('update:modelValue', activeSecret)
      return
    }
  }
  const matchedItem = userKeys.value.find((item) => ephemeralKeys.has(item.id))
  if (matchedItem) {
    const secret = ephemeralKeys.recall(matchedItem.id)
    if (secret) {
      applyRememberedKey(matchedItem, secret)
      return
    }
  }
  if (ephemeralKeys.pastedPlaintext) {
    activeKeyName.value = ''
    emit('update:modelValue', ephemeralKeys.pastedPlaintext)
  }
}

function onSelectKeyPicked(value: string | number | boolean | null) {
  const nextId = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(nextId)) return
  selectedKeyId.value = nextId
  if (!ephemeralKeys) return
  ephemeralKeys.suppressAutoFill(false)
  const remembered = ephemeralKeys.recall(nextId)
  const item = userKeys.value.find((k) => k.id === nextId)
  if (remembered && item && !props.modelValue.trim()) {
    ephemeralKeys.setActiveKey(nextId)
    applyRememberedKey(item, remembered)
  }
}

async function loadUserKeys() {
  if (!props.isAuthenticated) {
    userKeys.value = []
    selectedKeyId.value = null
    tryAutoFillFromStore()
    return
  }
  keysAbort?.abort()
  keysAbort = new AbortController()
  keysLoading.value = true
  try {
    const res = await keysAPI.list(1, 20, { status: 'active' }, { signal: keysAbort.signal })
    const items = Array.isArray(res?.items) ? res.items : []
    userKeys.value = items
    tryAutoFillFromStore()
    if (selectedKeyId.value == null) {
      const sealedFirst = items.find((item) => item.has_sealed_secret)
      selectedKeyId.value = sealedFirst?.id ?? items[0]?.id ?? null
    }
  } catch {
    // Public docs degrade silently if key fetch fails or is canceled.
  } finally {
    keysLoading.value = false
  }
}

async function fetchModelsForKey(secret: string) {
  modelsAbort?.abort()
  const trimmed = secret.trim()
  if (!trimmed || trimmed.length < 8) {
    fetchedModelsCount.value = 0
    emit('models-loaded', [])
    return
  }
  modelsAbort = new AbortController()
  modelsLoading.value = true
  try {
    const models = await fetchCcSwitchModels(props.origin, trimmed, modelsAbort.signal)
    fetchedModelsCount.value = models.length
    emit('models-loaded', models)
  } catch {
    fetchedModelsCount.value = 0
    emit('models-loaded', [])
  } finally {
    modelsLoading.value = false
  }
}

function openUnlockDialog() {
  if (!selectedUserKey.value) return
  revealedInCurrentDialog.value = false
  showRevealDialog.value = true
}

function openCreateModal() {
  if (!props.isAuthenticated) return
  showCreateModal.value = true
}

function closeCreateModal() {
  showCreateModal.value = false
  if (!hasKey.value && !showSecretDialog.value) {
    pendingCcsImport.value = false
    emit('ccs-import-cancelled')
  }
}

function triggerPersistentCcsImport() {
  if (hasKey.value) {
    openImportCcsModal()
    return
  }
  if (props.isAuthenticated) {
    pendingCcsImport.value = true
    requestFill()
  }
}

function onKeyCreated(payload: CreatedApiKeyPayload) {
  if (!userKeys.value.some((k) => k.id === payload.key.id)) {
    userKeys.value = [payload.key, ...userKeys.value]
  }
  selectedKeyId.value = payload.key.id
  activeKeyName.value = payload.key.name
  ephemeralKeys?.remember(payload.key.id, payload.secret)
  emit('update:modelValue', payload.secret)
  emit('key-meta', {
    name: payload.key.name,
    platform: payload.key.group?.platform,
  })
  secretRow.value = payload.key
  secretPlaintext.value = payload.secret
  secretCanSeal.value = payload.canSeal
  secretAuthFactor.value = payload.authFactor
  secretVerifiedPassword.value = payload.verifiedPassword
  showSecretDialog.value = true
}

const hasOpenKeyDialog = computed(
  () => showCreateModal.value || showSecretDialog.value || showRevealDialog.value,
)

function closeRevealDialog() {
  const wasRevealed = revealedInCurrentDialog.value
  revealedInCurrentDialog.value = false
  showRevealDialog.value = false
  const latestSecret = wasRevealed
    ? props.modelValue.trim() ||
      (selectedUserKey.value?.id != null ? ephemeralKeys?.recall(selectedUserKey.value.id) ?? '' : '')
    : ''
  if (wasRevealed && latestSecret && !showCcsImportModal.value) {
    if (pendingCcsImport.value) {
      pendingCcsImport.value = false
      openImportCcsModal({ secret: latestSecret })
      return
    }
    emit('key-flow-settled')
  } else if (!wasRevealed || !latestSecret) {
    pendingCcsImport.value = false
    emit('ccs-import-cancelled')
  }
}

function closeSecretDialog() {
  const closedSecret = secretPlaintext.value.trim() || props.modelValue.trim()
  showSecretDialog.value = false
  secretPlaintext.value = ''
  secretRow.value = null
  secretAuthFactor.value = ''
  secretVerifiedPassword.value = ''
  if (closedSecret && !showCcsImportModal.value) {
    if (pendingCcsImport.value) {
      pendingCcsImport.value = false
      openImportCcsModal({ secret: closedSecret })
      return
    }
    emit('key-flow-settled')
  } else if (!closedSecret) {
    pendingCcsImport.value = false
    emit('ccs-import-cancelled')
  }
}

function onSecretSealed() {
  const id = secretRow.value?.id
  if (!id) return
  userKeys.value = userKeys.value.map((k) => (k.id === id ? { ...k, has_sealed_secret: true } : k))
}

function onKeyRevealed(secret: string) {
  const trimmed = secret.trim()
  if (!trimmed) return
  revealedInCurrentDialog.value = true
  if (selectedUserKey.value?.id) {
    ephemeralKeys?.remember(selectedUserKey.value.id, trimmed)
  }
  activeKeyName.value = selectedUserKey.value?.name || ''
  emit('update:modelValue', trimmed)
  emit('key-meta', {
    name: activeKeyName.value,
    platform: selectedUserKey.value?.group?.platform,
  })
}

function openImportCcsModal(options?: {
  secret?: string
  platform?: GroupPlatform | null
  defaultApp?: CcSwitchApp
  allowedApps?: CcSwitchApp[]
}) {
  const secret = (options?.secret ?? props.modelValue).trim()
  if (!secret) return
  pendingCcsImport.value = false
  ccsImportSucceeded.value = false
  ccsImportSecret.value = secret
  ccsImportPlatform.value =
    options?.platform ?? selectedUserKey.value?.group?.platform ?? props.preferredPlatform ?? 'anthropic'
  ccsImportDefaultOverride.value = options?.defaultApp
  ccsImportAllowedOverride.value = options?.allowedApps
  showCcsImportModal.value = true
  emit('ccs-import-opened')
}

function closeCcsImportModal() {
  showCcsImportModal.value = false
  if (!ccsImportSucceeded.value) {
    emit('ccs-import-cancelled')
  }
  ccsImportSucceeded.value = false
}

function onImportCcsFromSecret() {
  if (!secretPlaintext.value) return
  pendingCcsImport.value = false
  openImportCcsModal({
    secret: secretPlaintext.value,
    platform: secretRow.value?.group?.platform ?? props.preferredPlatform ?? 'anthropic',
  })
}

function onImportCcsFromReveal(secret: string) {
  const trimmed = secret.trim()
  if (!trimmed) return
  pendingCcsImport.value = false
  openImportCcsModal({
    secret: trimmed,
    platform: selectedUserKey.value?.group?.platform ?? props.preferredPlatform ?? 'anthropic',
  })
}

function onCcsImported(payload: { app: CcSwitchApp; model: string; deeplink: string }) {
  ccsImportSucceeded.value = true
  emit('ccs-imported', payload)
}

function applyDraftPaste() {
  const trimmed = draftPasteKey.value.trim()
  if (!trimmed) return
  activeKeyName.value = ''
  pendingCcsImport.value = false
  ephemeralKeys?.setPastedPlaintext(trimmed)
  emit('update:modelValue', trimmed)
  emit('key-meta', { name: '' })
  draftPasteKey.value = ''
}

// Auto-apply immediately when a full sk-... style key is pasted so beginners don't even have to press Enter.
watch(draftPasteKey, (val) => {
  const trimmed = val.trim()
  if (trimmed.length >= 24 && /^sk-[A-Za-z0-9_-]+$/.test(trimmed)) {
    applyDraftPaste()
  }
})

function clearKey() {
  activeKeyName.value = ''
  draftPasteKey.value = ''
  fetchedModelsCount.value = 0
  pendingCcsImport.value = false
  ephemeralKeys?.suppressAutoFill(true)
  ephemeralKeys?.setActiveKey(null)
  ephemeralKeys?.setPastedPlaintext('')
  emit('update:modelValue', '')
  emit('key-meta', { name: '' })
  emit('models-loaded', [])
  emit('ccs-import-cancelled')
}

function requestFill() {
  if (props.isAuthenticated) {
    if (selectedUserKey.value?.has_sealed_secret) {
      openUnlockDialog()
      return
    }
    openCreateModal()
    return
  }
  rootRef.value?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  setTimeout(() => {
    pasteInputRef.value?.focus()
  }, 150)
}

watch(
  () => props.isAuthenticated,
  () => {
    void loadUserKeys()
  },
  { immediate: true },
)

watch(
  [() => props.modelValue, () => props.origin],
  ([nextKey]) => {
    if (nextKey.trim()) {
      void fetchModelsForKey(nextKey)
    } else {
      fetchedModelsCount.value = 0
      emit('models-loaded', [])
    }
  },
)

onMounted(() => {
  tryAutoFillFromStore()
  if (props.modelValue.trim()) {
    void fetchModelsForKey(props.modelValue)
  }
})

onBeforeUnmount(() => {
  modelsAbort?.abort()
  keysAbort?.abort()
})

defineExpose({
  requestFill,
  openCreateModal,
  openImportCcsModal,
  hasOpenKeyDialog,
})
</script>
