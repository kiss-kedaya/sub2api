<template>
  <AppLayout>
    <div class="signal-account-page signal-allowlist mx-auto max-w-3xl space-y-6">
      <header class="signal-account-heading">
        <span class="signal-account-index">NETWORK</span>
        <h1>{{ t('cfAllowlist.title') }}</h1>
        <p>{{ t('cfAllowlist.intro') }}</p>
      </header>

      <div v-if="loading" class="signal-account-section text-sm text-gray-500">{{ t('common.loading') }}</div>

      <div v-else-if="status" class="space-y-6">
        <div class="signal-account-section signal-allowlist-summary">
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

        <div class="signal-account-section space-y-4">
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

        <div class="signal-account-section">
          <h2 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('cfAllowlist.current') }}</h2>
          <ul v-if="status.items.length" class="mt-3 space-y-2">
            <li
              v-for="item in status.items"
              :key="item.id"
              class="flex items-center justify-between rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-600"
            >
              <span class="font-mono text-sm">{{ item.ip }}</span>
              <button type="button" class="btn btn-ghost btn-icon text-red-600" :title="t('common.delete')" :aria-label="t('common.delete')" :disabled="removingId === item.id" @click="removeIP(item.id)">
                <Icon name="trash" size="sm" />
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
import Icon from '@/components/icons/Icon.vue'
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
