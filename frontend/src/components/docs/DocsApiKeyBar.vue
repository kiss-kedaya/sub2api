<template>
  <section
    ref="rootRef"
    data-testid="docs-apikey-bar"
    class="rounded-2xl border p-4.5 transition sm:p-5"
    :class="hasKey
      ? 'border-emerald-200/90 bg-emerald-50/55 dark:border-emerald-500/30 dark:bg-emerald-500/[0.08]'
      : 'border-amber-200/90 bg-amber-50/60 dark:border-amber-500/30 dark:bg-amber-500/[0.08]'"
  >
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 items-start gap-3">
        <span
          class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border"
          :class="hasKey
            ? 'border-emerald-200 bg-white text-emerald-600 dark:border-emerald-500/30 dark:bg-dark-950 dark:text-emerald-400'
            : 'border-amber-200 bg-white text-amber-600 dark:border-amber-500/30 dark:bg-dark-950 dark:text-amber-400'"
        >
          <Icon :name="hasKey ? 'checkCircle' : 'key'" size="sm" />
        </span>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <h3 class="text-[15px] font-semibold text-slate-950 dark:text-slate-50">
              {{ hasKey ? t('docs.agentSetup.keyBarReadyTitle') : t('docs.agentSetup.keyBarEmptyTitle') }}
            </h3>
            <span
              v-if="hasKey && fetchedModelsCount > 0"
              data-testid="docs-apikey-models-badge"
              class="inline-flex items-center gap-1 rounded-full bg-emerald-100/80 px-2.5 py-0.5 text-[11.5px] font-medium text-emerald-800 dark:bg-emerald-500/20 dark:text-emerald-300"
            >
              {{ t('docs.agentSetup.keyBarModelsLoaded', { count: fetchedModelsCount }) }}
            </span>
            <span
              v-else-if="hasKey && modelsLoading"
              class="inline-flex items-center gap-1 rounded-full bg-white/80 px-2.5 py-0.5 text-[11.5px] text-slate-500 dark:bg-dark-900 dark:text-slate-400"
            >
              {{ t('docs.agentSetup.keyBarModelsLoading') }}
            </span>
          </div>
          <p class="mt-1 text-[13.5px] leading-6 text-slate-600 dark:text-slate-300">
            {{ hasKey ? t('docs.agentSetup.keyBarReadyDesc') : t('docs.agentSetup.keyBarEmptyDesc') }}
          </p>
        </div>
      </div>

      <!-- Actions when key is loaded -->
      <div v-if="hasKey" class="flex shrink-0 flex-wrap items-center gap-2">
        <button
          type="button"
          data-testid="docs-apikey-toggle-mask"
          class="inline-flex min-h-9 items-center gap-1.5 rounded-full border border-slate-200 bg-white px-3.5 py-1.5 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
          @click="emit('update:masked', !masked)"
        >
          <Icon :name="masked ? 'eye' : 'eyeOff'" size="xs" />
          <span>{{ masked ? t('docs.agentSetup.keyBarShowPlain') : t('docs.agentSetup.keyBarMaskDisplay') }}</span>
        </button>
        <button
          type="button"
          data-testid="docs-apikey-clear"
          class="inline-flex min-h-9 items-center gap-1 rounded-full border border-slate-200 bg-white px-3.5 py-1.5 text-xs font-medium text-slate-600 transition hover:bg-slate-50 hover:text-slate-900 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-300 dark:hover:bg-dark-800"
          @click="clearKey"
        >
          <Icon name="x" size="xs" />
          <span>{{ t('docs.agentSetup.keyBarClear') }}</span>
        </button>
      </div>
    </div>

    <!-- Active key preview strip -->
    <div
      v-if="hasKey"
      class="mt-3.5 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-emerald-200/80 bg-white px-3.5 py-2.5 dark:border-emerald-500/25 dark:bg-dark-950/90"
    >
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <span v-if="activeKeyName" class="shrink-0 rounded-md bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-700 dark:bg-dark-800 dark:text-slate-300">
          {{ activeKeyName }}
        </span>
        <code class="min-w-0 truncate font-mono text-[13px] text-slate-900 dark:text-slate-100" data-testid="docs-apikey-display">
          {{ masked ? maskedKeyText : modelValue }}
        </code>
      </div>
      <span class="shrink-0 text-[11.5px] text-emerald-700 dark:text-emerald-400">
        {{ t('docs.agentSetup.keyBarLocalOnlyNote') }}
      </span>
    </div>

    <!-- Key acquisition / unlock / paste controls when not yet filled -->
    <div v-else class="mt-4 space-y-3">
      <!-- Logged-in user with existing keys: pick & unlock right here -->
      <div
        v-if="isAuthenticated && userKeys.length > 0"
        class="space-y-2 rounded-xl border border-slate-200/90 bg-white p-3 dark:border-dark-700 dark:bg-dark-950/90"
      >
        <div class="flex flex-wrap items-center gap-2.5">
          <label for="docs-user-key-select" class="sr-only">{{ t('docs.agentSetup.keyBarSelectLabel') }}</label>
          <select
            id="docs-user-key-select"
            v-model="selectedKeyId"
            data-testid="docs-apikey-select"
            class="min-w-[200px] flex-1 rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 text-[13px] text-slate-900 outline-hidden focus:border-blue-500 focus:bg-white dark:border-dark-700 dark:bg-dark-900 dark:text-slate-100"
          >
            <option v-for="item in userKeys" :key="item.id" :value="item.id">
              {{ item.name }} ({{ item.key_masked }})
            </option>
          </select>
          <button
            v-if="selectedUserKey?.has_sealed_secret"
            type="button"
            data-testid="docs-apikey-unlock-btn"
            class="inline-flex min-h-9 shrink-0 items-center gap-1.5 rounded-full bg-blue-600 px-4 py-1.5 text-[13px] font-medium text-white shadow-xs transition hover:bg-blue-700"
            @click="openUnlockDialog"
          >
            <Icon name="lock" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarUnlockBtn') }}</span>
          </button>
          <router-link
            v-else
            to="/keys?action=create"
            class="inline-flex min-h-9 shrink-0 items-center gap-1.5 rounded-full bg-blue-600 px-4 py-1.5 text-[13px] font-medium text-white shadow-xs transition hover:bg-blue-700"
          >
            <Icon name="plus" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarCreateNewBtn') }}</span>
          </router-link>
          <router-link
            v-if="selectedUserKey?.has_sealed_secret"
            to="/keys?action=create"
            class="inline-flex min-h-9 shrink-0 items-center gap-1 rounded-full border border-slate-200 bg-white px-3.5 py-1.5 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
          >
            <Icon name="plus" size="xs" />
            <span>{{ t('docs.agentSetup.keyBarCreateNewBtn') }}</span>
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
          <input
            ref="pasteInputRef"
            v-model="draftPasteKey"
            data-testid="docs-apikey-paste-input"
            type="password"
            autocomplete="off"
            spellcheck="false"
            :placeholder="t('docs.agentSetup.keyBarPastePlaceholder')"
            :aria-label="t('docs.agentSetup.keyBarPastePlaceholder')"
            class="w-full rounded-full border border-slate-300/90 bg-white px-4 py-2 pr-20 font-mono text-[13px] text-slate-900 outline-hidden transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 dark:border-dark-600 dark:bg-dark-950 dark:text-slate-100"
            @keydown.enter.prevent="applyDraftPaste"
          />
          <button
            v-if="draftPasteKey.trim()"
            type="button"
            data-testid="docs-apikey-apply-btn"
            class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded-full bg-blue-600 px-3 py-1 text-xs font-medium text-white transition hover:bg-blue-700"
            @click="applyDraftPaste"
          >
            {{ t('docs.agentSetup.keyBarApplyBtn') }}
          </button>
        </div>

        <router-link
          v-if="!isAuthenticated"
          :to="loginRedirectLocation"
          data-testid="docs-apikey-login-cta"
          class="inline-flex min-h-9.5 shrink-0 items-center gap-1.5 rounded-full bg-slate-950 px-4 py-2 text-[13px] font-medium text-white transition hover:bg-slate-800 dark:bg-white dark:text-slate-950 dark:hover:bg-slate-200"
        >
          <Icon name="user" size="xs" />
          <span>{{ t('docs.agentSetup.keyBarLoginBtn') }}</span>
        </router-link>
        <router-link
          v-else-if="userKeys.length === 0 && !keysLoading"
          to="/keys?action=create"
          data-testid="docs-apikey-create-first-cta"
          class="inline-flex min-h-9.5 shrink-0 items-center gap-1.5 rounded-full bg-blue-600 px-4 py-2 text-[13px] font-medium text-white transition hover:bg-blue-700"
        >
          <Icon name="plus" size="xs" />
          <span>{{ t('docs.agentSetup.keyBarCreateFirstBtn') }}</span>
        </router-link>
      </div>
    </div>

    <!-- Reuse existing local-decrypt RevealApiKeyDialog so users never leave the docs page -->
    <RevealApiKeyDialog
      v-if="isAuthenticated"
      :show="showRevealDialog"
      :api-key="selectedUserKey"
      hide-ccs-import
      @revealed="onKeyRevealed"
      @close="showRevealDialog = false"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import RevealApiKeyDialog from '@/components/keys/RevealApiKeyDialog.vue'
import { keysAPI } from '@/api'
import { fetchCcSwitchModels } from '@/utils/ccswitchImport'
import type { ApiKey, GroupPlatform } from '@/types'

const props = withDefaults(
  defineProps<{
    origin: string
    isAuthenticated: boolean
    modelValue: string
    masked: boolean
    agentId?: string
  }>(),
  {
    agentId: '',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:masked': [value: boolean]
  'models-loaded': [models: string[]]
  'key-meta': [meta: { name: string; platform?: GroupPlatform }]
}>()

const { t } = useI18n()

const rootRef = ref<HTMLElement | null>(null)
const pasteInputRef = ref<HTMLInputElement | null>(null)
const userKeys = ref<ApiKey[]>([])
const keysLoading = ref(false)
const selectedKeyId = ref<number | null>(null)
const activeKeyName = ref('')
const draftPasteKey = ref('')
const showRevealDialog = ref(false)
const modelsLoading = ref(false)
const fetchedModelsCount = ref(0)
let modelsAbort: AbortController | null = null
let keysAbort: AbortController | null = null

const hasKey = computed(() => props.modelValue.trim().length > 0)

const selectedUserKey = computed<ApiKey | null>(() => {
  if (selectedKeyId.value == null) return userKeys.value[0] ?? null
  return userKeys.value.find((item) => item.id === selectedKeyId.value) ?? userKeys.value[0] ?? null
})

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

async function loadUserKeys() {
  if (!props.isAuthenticated) {
    userKeys.value = []
    selectedKeyId.value = null
    return
  }
  keysAbort?.abort()
  keysAbort = new AbortController()
  keysLoading.value = true
  try {
    const res = await keysAPI.list(1, 20, { status: 'active' }, { signal: keysAbort.signal })
    const items = Array.isArray(res?.items) ? res.items : []
    userKeys.value = items
    const sealedFirst = items.find((item) => item.has_sealed_secret)
    selectedKeyId.value = sealedFirst?.id ?? items[0]?.id ?? null
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
  showRevealDialog.value = true
}

function onKeyRevealed(secret: string) {
  const trimmed = secret.trim()
  if (!trimmed) return
  activeKeyName.value = selectedUserKey.value?.name || ''
  emit('update:modelValue', trimmed)
  emit('key-meta', {
    name: activeKeyName.value,
    platform: selectedUserKey.value?.group?.platform,
  })
  showRevealDialog.value = false
}

function applyDraftPaste() {
  const trimmed = draftPasteKey.value.trim()
  if (!trimmed) return
  activeKeyName.value = ''
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
  emit('update:modelValue', '')
  emit('models-loaded', [])
}

function requestFill() {
  if (props.isAuthenticated && selectedUserKey.value?.has_sealed_secret) {
    openUnlockDialog()
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
})
</script>
