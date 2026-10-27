<!-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later. -->
<template>
  <div class="flex w-full flex-wrap items-end gap-3 lg:w-auto">
    <div ref="userRoot" class="relative w-full sm:w-60">
      <label class="input-label">{{ t('admin.usage.userFilter') }}</label>
      <input v-model="userKeyword" class="input pr-8" :placeholder="t('admin.usage.searchUserPlaceholder')" @input="scheduleUsers" @focus="userOpen = true" />
      <button v-if="modelValue.user_id" type="button" class="absolute right-2 top-9 text-gray-400" :aria-label="t('common.clear')" @click="clearUser">×</button>
      <div v-if="userOpen && userResults.length" class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800">
        <button v-for="user in userResults" :key="user.id" type="button" class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700" @click="selectUser(user)">{{ user.email }} <span class="text-xs text-gray-400">#{{ user.id }}</span></button>
      </div>
      <p v-if="userError" role="alert" class="mt-1 text-xs text-red-600">{{ t('traffic.identityFailed') }}</p>
    </div>
    <div ref="keyRoot" class="relative w-full sm:w-60">
      <label class="input-label">{{ t('usage.apiKeyFilter') }}</label>
      <input v-model="keyKeyword" class="input pr-8" :placeholder="t('admin.usage.searchApiKeyPlaceholder')" @input="scheduleKeys" @focus="openKeys" />
      <button v-if="modelValue.api_key_id" type="button" class="absolute right-2 top-9 text-gray-400" :aria-label="t('common.clear')" @click="clearKey">×</button>
      <div v-if="keyOpen && keyResults.length" class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800">
        <button v-for="key in keyResults" :key="key.id" type="button" class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700" @click="selectKey(key)">{{ key.name || `#${key.id}` }} <span v-if="key.deleted" class="text-xs text-gray-400">({{ t('traffic.deletedKey') }})</span></button>
      </div>
      <p v-if="keyError" role="alert" class="mt-1 text-xs text-red-600">{{ t('traffic.keysFailed') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { SimpleUser } from '@/api/admin/usage'
import type { TrafficKeyOption } from '@/api/traffic'
import trafficZh from '@/i18n/locales/zh/traffic'
import trafficEn from '@/i18n/locales/en/traffic'

type Filters = { user_id?: number; api_key_id?: number | null }
const props = defineProps<{
  modelValue: Filters
  apiKeySearch: (userId: number | undefined, query: string) => Promise<TrafficKeyOption[]>
}>()
const emit = defineEmits<{ 'update:modelValue': [value: Filters]; change: [] }>()
const { t } = useI18n({ useScope: 'local', messages: { zh: { traffic: trafficZh }, en: { traffic: trafficEn } } })
const userKeyword = ref(''), keyKeyword = ref('')
const userResults = ref<SimpleUser[]>([]), keyResults = ref<TrafficKeyOption[]>([])
const userRoot = ref<HTMLElement | null>(null), keyRoot = ref<HTMLElement | null>(null)
const userOpen = ref(false), keyOpen = ref(false), userError = ref(false), keyError = ref(false)
let userTimer: ReturnType<typeof setTimeout> | undefined, keyTimer: ReturnType<typeof setTimeout> | undefined
let userSequence = 0, keySequence = 0, alive = true

function update(value: Filters) {
  emit('update:modelValue', value)
  emit('change')
}
function scheduleUsers() {
  clearTimeout(userTimer)
  const sequence = ++userSequence, query = userKeyword.value.trim()
  userResults.value = []; userError.value = false
  if (!query) return
  userTimer = setTimeout(async () => {
    try {
      const result = await adminAPI.usage.searchUsers(query)
      if (alive && sequence === userSequence) { userResults.value = result; userOpen.value = true }
    } catch { if (alive && sequence === userSequence) userError.value = true }
  }, 300)
}
async function lookupKeys(query: string) {
  const sequence = ++keySequence, userId = props.modelValue.user_id
  keyResults.value = []; keyError.value = false
  try {
    const result = await props.apiKeySearch(userId, query)
    if (alive && sequence === keySequence && props.modelValue.user_id === userId) keyResults.value = result
  } catch { if (alive && sequence === keySequence) keyError.value = true }
}
function scheduleKeys() {
  clearTimeout(keyTimer); keySequence++
  const query = keyKeyword.value.trim()
  keyResults.value = []
  keyTimer = setTimeout(() => { keyOpen.value = true; void lookupKeys(query) }, 300)
}
function openKeys() { keyOpen.value = true; void lookupKeys(props.modelValue.api_key_id ? '' : keyKeyword.value.trim()) }
function selectUser(user: SimpleUser) {
  clearTimeout(userTimer); userSequence++; clearTimeout(keyTimer); keySequence++
  userKeyword.value = user.email; userOpen.value = false; userResults.value = []
  keyKeyword.value = ''; keyResults.value = []
  update({ user_id: user.id, api_key_id: null })
  void lookupKeys('')
}
function clearUser() {
  clearTimeout(userTimer); userSequence++; clearTimeout(keyTimer); keySequence++
  userKeyword.value = ''; keyKeyword.value = ''; userResults.value = []; keyResults.value = []
  update({ api_key_id: null })
}
function selectKey(key: TrafficKeyOption) { keyKeyword.value = key.name; keyOpen.value = false; update({ ...props.modelValue, api_key_id: key.id }) }
function clearKey() { clearTimeout(keyTimer); keySequence++; keyKeyword.value = ''; keyResults.value = []; update({ ...props.modelValue, api_key_id: null }) }
function outside(event: MouseEvent) {
  if (!userRoot.value?.contains(event.target as Node)) userOpen.value = false
  if (!keyRoot.value?.contains(event.target as Node)) keyOpen.value = false
}
onMounted(() => document.addEventListener('click', outside))
onUnmounted(() => { alive = false; userSequence++; keySequence++; clearTimeout(userTimer); clearTimeout(keyTimer); document.removeEventListener('click', outside) })
</script>
