<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-medium text-gray-900 dark:text-white">
        {{ t('profile.bark.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('profile.bark.description') }}
      </p>
    </div>
    <div class="px-6 py-6 space-y-6">
      <div class="flex items-center gap-2 text-sm">
        <span
          class="inline-block h-2 w-2 rounded-full"
          :class="status.configured ? 'bg-green-500' : 'bg-gray-400'"
        ></span>
        <span class="text-gray-500 dark:text-gray-400">
          {{ status.configured ? t('profile.bark.configured') : t('profile.bark.notConfigured') }}
        </span>
      </div>

      <div>
        <label class="input-label">{{ t('profile.bark.deviceKey') }}</label>
        <input
          v-model="deviceKey"
          type="text"
          class="input"
          :placeholder="t('profile.bark.deviceKeyPlaceholder')"
        />
        <p class="mt-1 text-xs text-gray-400">{{ t('profile.bark.deviceKeyHint') }}</p>
      </div>

      <div class="flex items-center justify-between">
        <label class="input-label mb-0">{{ t('profile.bark.enabled') }}</label>
        <label class="relative inline-flex items-center cursor-pointer">
          <input type="checkbox" v-model="enabled" class="sr-only peer" />
          <div class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-primary-300 dark:peer-focus:ring-primary-800 rounded-full peer dark:bg-gray-700 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:after:border-gray-600 peer-checked:bg-primary-600"></div>
        </label>
      </div>

      <div class="flex items-center gap-2">
        <button class="btn btn-primary btn-sm" :disabled="saving || !deviceKey.trim()" @click="save">
          {{ saving ? t('common.saving') : t('profile.bark.save') }}
        </button>
        <button
          v-if="status.configured"
          class="btn btn-secondary btn-sm"
          :disabled="saving"
          @click="clear"
        >
          {{ t('profile.bark.clear') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { userAPI, type BarkSettings } from '@/api/user'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const status = ref<BarkSettings>({ configured: false, enabled: false })
const deviceKey = ref('')
const enabled = ref(false)
const saving = ref(false)

onMounted(async () => {
  try {
    status.value = await userAPI.getBarkSettings()
    enabled.value = status.value.enabled
  } catch {
    // The channel is optional: a failure here must not break the profile page.
  }
})

async function save() {
  saving.value = true
  try {
    await userAPI.updateBarkSettings(deviceKey.value.trim(), enabled.value)
    status.value = await userAPI.getBarkSettings()
    deviceKey.value = ''
    appStore.showSuccess(t('profile.bark.saved'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    saving.value = false
  }
}

async function clear() {
  saving.value = true
  try {
    await userAPI.updateBarkSettings('', false)
    status.value = await userAPI.getBarkSettings()
    deviceKey.value = ''
    enabled.value = false
    appStore.showSuccess(t('profile.bark.cleared'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
