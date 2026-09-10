<template>
  <AppLayout>
    <div class="mx-auto max-w-2xl space-y-6">
      <div class="card p-6">
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('cfAllowlist.title') }}</h1>
        <p class="mt-2 text-sm text-gray-600 dark:text-dark-300">{{ t('cfAllowlist.intro') }}</p>
      </div>

      <div v-if="loading" class="card p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>

      <div v-else-if="status" class="space-y-6">
        <div class="card p-6">
          <p class="text-sm text-gray-500">{{ t('cfAllowlist.recharged') }}</p>
          <p class="mt-1 text-3xl font-bold text-gray-900 dark:text-white">
            ¥{{ status.total_recharged.toFixed(2) }}
          </p>
          <p class="mt-2 text-sm" :class="status.eligible ? 'text-emerald-600' : 'text-amber-600'">
            {{
              status.eligible
                ? t('cfAllowlist.slots', { used: status.used_slots, max: status.max_slots })
                : t('cfAllowlist.needRecharge', { amount: status.threshold })
            }}
          </p>
        </div>

        <div class="card p-6 space-y-4">
          <p class="text-sm text-gray-600 dark:text-dark-300">
            {{ t('cfAllowlist.detectedIP') }}:
            <span class="font-mono">{{ status.detected_ip || t('cfAllowlist.unknownIP') }}</span>
          </p>
          <div>
            <label class="input-label" for="ip">{{ t('cfAllowlist.ipLabel') }}</label>
            <input
              id="ip"
              v-model="ipInput"
              class="input mt-1 w-full font-mono"
              :placeholder="status.detected_ip || '1.2.3.4'"
              :disabled="submitting || !status.eligible"
            />
          </div>
          <p v-if="!status.configured" class="text-sm text-amber-600">{{ t('cfAllowlist.notConfigured') }}</p>
          <button
            type="button"
            class="btn btn-primary w-full py-3"
            :disabled="!status.eligible || !status.configured || submitting"
            @click="submitIP"
          >
            {{ submitting ? t('cfAllowlist.submitting') : t('cfAllowlist.submit') }}
          </button>
        </div>

        <div class="card p-6">
          <h2 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('cfAllowlist.current') }}</h2>
          <ul v-if="status.items.length" class="mt-3 space-y-2">
            <li
              v-for="item in status.items"
              :key="item.id"
              class="flex items-center justify-between rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-600"
            >
              <span class="font-mono text-sm">{{ item.ip }}</span>
              <button type="button" class="text-sm text-red-600" :disabled="removingId === item.id" @click="removeIP(item.id)">
                {{ t('common.delete') }}
              </button>
            </li>
          </ul>
          <p v-else class="mt-3 text-sm text-gray-500">{{ t('cfAllowlist.empty') }}</p>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { cfAllowlistAPI, type CFAllowlistStatus } from '@/api/cfAllowlist'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const submitting = ref(false)
const removingId = ref<number | null>(null)
const status = ref<CFAllowlistStatus | null>(null)
const ipInput = ref('')

async function load() {
  loading.value = true
  try {
    status.value = await cfAllowlistAPI.getStatus()
    if (!ipInput.value && status.value.detected_ip) {
      ipInput.value = status.value.detected_ip
    }
  } catch (err: any) {
    appStore.showError(err?.message || t('cfAllowlist.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function submitIP() {
  submitting.value = true
  try {
    await cfAllowlistAPI.add(ipInput.value.trim())
    appStore.showSuccess(t('cfAllowlist.submitOk'))
    await load()
  } catch (err: any) {
    appStore.showError(err?.message || t('cfAllowlist.submitFailed'))
  } finally {
    submitting.value = false
  }
}

async function removeIP(id: number) {
  removingId.value = id
  try {
    await cfAllowlistAPI.remove(id)
    appStore.showSuccess(t('cfAllowlist.removed'))
    await load()
  } catch (err: any) {
    appStore.showError(err?.message || t('cfAllowlist.submitFailed'))
  } finally {
    removingId.value = null
  }
}

onMounted(load)
</script>
