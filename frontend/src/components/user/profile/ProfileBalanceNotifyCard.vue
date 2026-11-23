<template>
  <div class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none">
    <div class="border-b border-default px-5 py-4 sm:px-6">
      <h2 class="text-base font-semibold text-highlighted">
        {{ t('profile.balanceNotify.title') }}
      </h2>
      <p class="mt-1 text-sm text-muted">
        {{ t('profile.balanceNotify.description') }}
      </p>
    </div>
    <div class="space-y-6 p-5 sm:p-6">
      <!-- Enable toggle -->
      <div class="flex items-center justify-between">
        <label for="balance-notify-enabled" class="input-label mb-0">{{ t('profile.balanceNotify.enabled') }}</label>
        <USwitch
          id="balance-notify-enabled"
          v-model="notifyEnabled"
          data-test="balance-notify-enabled"
          @update:model-value="handleToggle"
        />
      </div>

      <template v-if="notifyEnabled">
        <!-- Custom threshold with save button -->
        <div>
          <label class="input-label">
            {{ t('profile.balanceNotify.threshold') }}
            <span class="text-xs text-gray-400 ml-2">{{ t('profile.balanceNotify.thresholdHint') }}</span>
          </label>
          <div class="flex items-center gap-2">
            <span class="text-gray-500">$</span>
            <UInput
              v-model.number="customThreshold"
              type="number"
              min="0"
              step="0.01"
              class="flex-1"
              :placeholder="systemDefaultThreshold > 0 ? `${t('profile.balanceNotify.systemDefault')} $${systemDefaultThreshold}` : t('profile.balanceNotify.thresholdPlaceholder')"
            />
            <UButton
              type="button"
              size="sm"
              class="whitespace-nowrap"
              :disabled="savingThreshold"
              :loading="savingThreshold"
              data-test="balance-notify-save-threshold"
              @click="handleThresholdUpdate"
            >
              {{ savingThreshold ? t('common.saving') : t('common.save') }}
            </UButton>
          </div>
        </div>

        <!-- Email list with toggles -->
        <div>
          <label class="input-label">{{ t('profile.balanceNotify.extraEmails') }}</label>
          <p class="mb-2 text-xs text-yellow-600 dark:text-yellow-400">{{ t('profile.balanceNotify.extraEmailsHint') }}</p>

          <!-- Saved email entries -->
          <div v-if="emailEntries.length > 0" class="space-y-2 mb-3">
            <div v-for="(entry, idx) in emailEntries" :key="`${entry.primary ? 'primary' : entry.email}-${idx}`"
              class="flex items-center justify-between px-3 py-2 bg-gray-50 dark:bg-dark-700 rounded-lg">
              <div class="flex items-center gap-2 min-w-0 flex-1">
                <USwitch
                  :model-value="!entry.disabled"
                  class="shrink-0"
                  :aria-label="`${t('profile.balanceNotify.enabled')} ${entry.email}`"
                  @update:model-value="handleEmailToggle(entry)"
                />
                <span class="text-sm text-gray-700 dark:text-gray-300 truncate">{{ entry.email }}</span>
                <span v-if="entry.primary" class="text-xs text-primary-600 dark:text-primary-400 shrink-0">{{ t('profile.balanceNotify.primaryEmail') }}</span>
              </div>
              <div class="flex items-center gap-2 shrink-0">
                <template v-if="!entry.verified">
                  <!-- Inline verify flow for saved unverified emails -->
                  <template v-if="verifyingEmail === entry.email">
                    <UInput
                      v-model="verifyCode"
                      type="text"
                      maxlength="6"
                      class="w-24"
                      :placeholder="t('profile.balanceNotify.codePlaceholder')"
                    />
                    <UButton variant="link" size="xs" class="p-0" :disabled="!verifyCode || verifyCode.length !== 6 || verifyingSaved" @click="verifySavedEmail(entry.email)">
                      {{ t('profile.balanceNotify.verify') }}
                    </UButton>
                    <span v-if="verifyCountdown > 0" class="text-xs text-gray-400">{{ verifyCountdown }}s</span>
                    <UButton v-else variant="link" size="xs" color="neutral" class="p-0" :disabled="sendingSavedCode" @click="sendCodeForSaved(entry.email)">
                      {{ t('profile.balanceNotify.resend') }}
                    </UButton>
                    <UButton variant="link" size="xs" color="neutral" class="p-0" @click="verifyingEmail = ''">
                      {{ t('common.cancel') }}
                    </UButton>
                  </template>
                  <template v-else>
                    <UButton variant="link" size="xs" class="p-0" :disabled="sendingSavedCode" @click="sendCodeForSaved(entry.email)">
                      {{ t('profile.balanceNotify.verify') }}
                    </UButton>
                    <span class="text-xs text-yellow-500">{{ t('profile.balanceNotify.unverified') }}</span>
                  </template>
                </template>
                <span v-else class="text-xs text-green-500">{{ t('profile.balanceNotify.verified') }}</span>
                <UButton v-if="!entry.primary" variant="link" size="xs" color="error" class="p-0" @click="handleRemoveEmail(entry.email)">
                  {{ t('profile.balanceNotify.removeEmail') }}
                </UButton>
              </div>
            </div>
          </div>

          <!-- Pending (unverified) emails in verification flow -->
          <div v-if="pendingEmails.length > 0" class="space-y-2 mb-3">
            <div v-for="(pe, idx) in pendingEmails" :key="pe.email"
              class="flex items-center gap-2 px-3 py-2 bg-yellow-50 dark:bg-yellow-900/10 rounded-lg border border-yellow-200 dark:border-yellow-800">
              <span class="flex-1 text-sm text-gray-700 dark:text-gray-300">{{ pe.email }}</span>
              <div v-if="!pe.codeSent" class="flex items-center gap-1">
                <UButton variant="link" size="xs" class="p-0" :disabled="pe.sending" @click="sendCodeFor(idx)">
                  {{ t('profile.balanceNotify.sendCode') }}
                </UButton>
                <UButton variant="link" size="xs" color="error" class="p-0 ml-1" @click="pendingEmails.splice(idx, 1)">
                  {{ t('profile.balanceNotify.removeEmail') }}
                </UButton>
              </div>
              <div v-else class="flex items-center gap-1">
                <UInput
                  v-model="pe.code"
                  type="text"
                  maxlength="6"
                  class="w-24"
                  :placeholder="t('profile.balanceNotify.codePlaceholder')"
                />
                <UButton variant="link" size="xs" class="p-0" :disabled="!pe.code || pe.code.length !== 6 || pe.verifying" @click="verifyPending(idx)">
                  {{ t('profile.balanceNotify.verify') }}
                </UButton>
                <span v-if="pe.countdown > 0" class="text-xs text-gray-400">{{ pe.countdown }}s</span>
                <UButton v-else variant="link" size="xs" color="neutral" class="p-0" :disabled="pe.sending" @click="sendCodeFor(idx)">
                  {{ t('profile.balanceNotify.resend') }}
                </UButton>
              </div>
            </div>
          </div>

          <!-- Add new email input (hidden when at limit) -->
          <div v-if="canAddMore" class="flex gap-2">
            <UInput
              v-model="newEmail"
              type="email"
              class="flex-1"
              :placeholder="t('profile.balanceNotify.emailPlaceholder')"
              @keyup.enter="addPendingEmail"
            />
            <UButton
              type="button"
              color="neutral"
              variant="outline"
              class="whitespace-nowrap"
              :disabled="!newEmail"
              data-test="balance-notify-add-email"
              @click="addPendingEmail"
            >
              {{ t('common.add') }}
            </UButton>
          </div>
          <p v-else class="text-xs text-gray-400">
            {{ t('profile.balanceNotify.maxEmailsReached') }}
          </p>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { userAPI } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { NotifyEmailEntry } from '@/types'

const maxExtraEmails = 3

interface PendingEmail {
  email: string
  codeSent: boolean
  code: string
  sending: boolean
  verifying: boolean
  countdown: number
  timer: ReturnType<typeof setInterval> | null
}

const props = defineProps<{
  enabled: boolean
  threshold: number | null
  extraEmails: NotifyEmailEntry[]
  systemDefaultThreshold: number
  userEmail: string
}>()

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const notifyEnabled = ref(props.enabled)
const customThreshold = ref<number | null>(props.threshold)
const emailEntries = ref<NotifyEmailEntry[]>([...props.extraEmails])
const pendingEmails = ref<PendingEmail[]>([])
const newEmail = ref('')
const savingThreshold = ref(false)

// State for verifying saved unverified emails
const verifyingEmail = ref('')
const verifyCode = ref('')
const verifyingSaved = ref(false)
const sendingSavedCode = ref(false)
const verifyCountdown = ref(0)
let verifyTimer: ReturnType<typeof setInterval> | null = null

const canAddMore = computed(() => {
  const extraCount = emailEntries.value.filter(entry => !entry.primary).length
  return extraCount + pendingEmails.value.length < maxExtraEmails
})

watch(() => props.enabled, (val) => { notifyEnabled.value = val })
watch(() => props.threshold, (val) => { customThreshold.value = val })
watch(() => props.extraEmails, (val) => { emailEntries.value = [...val] })

// When list is empty on mount, pre-fill the add input with user's email
onMounted(() => {
  if (emailEntries.value.length === 0 && props.userEmail) {
    newEmail.value = props.userEmail
  }
})

onUnmounted(() => {
  for (const pe of pendingEmails.value) {
    if (pe.timer) clearInterval(pe.timer)
  }
  if (verifyTimer) clearInterval(verifyTimer)
})

const handleToggle = async () => {
  try {
    const updated = await userAPI.updateProfile({ balance_notify_enabled: notifyEnabled.value })
    authStore.user = updated
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
    notifyEnabled.value = !notifyEnabled.value
  }
}

const handleThresholdUpdate = async () => {
  savingThreshold.value = true
  try {
    const threshold = customThreshold.value && customThreshold.value > 0 ? customThreshold.value : 0
    const updated = await userAPI.updateProfile({ balance_notify_threshold: threshold })
    authStore.user = updated
    appStore.showSuccess(t('common.saved'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    savingThreshold.value = false
  }
}

async function handleEmailToggle(entry: NotifyEmailEntry) {
  const newDisabled = !entry.disabled
  try {
    const updated = await userAPI.toggleNotifyEmail(entry.email, newDisabled)
    authStore.user = updated
    emailEntries.value = [...updated.balance_notify_extra_emails]
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

function addPendingEmail() {
  const email = newEmail.value.trim()
  if (!email) return
  // Check duplicates
  const isDuplicate = emailEntries.value.some(e => e.email.toLowerCase() === email.toLowerCase())
    || pendingEmails.value.some(p => p.email.toLowerCase() === email.toLowerCase())
  if (isDuplicate) {
    appStore.showError(t('profile.balanceNotify.emailDuplicate'))
    return
  }
  pendingEmails.value.push({ email, codeSent: false, code: '', sending: false, verifying: false, countdown: 0, timer: null })
  newEmail.value = ''
}

async function sendCodeFor(idx: number) {
  const pe = pendingEmails.value[idx]
  if (!pe) return
  pe.sending = true
  try {
    await userAPI.sendNotifyEmailCode(pe.email)
    pe.codeSent = true
    pe.countdown = 60
    pe.timer = setInterval(() => {
      pe.countdown--
      if (pe.countdown <= 0 && pe.timer) {
        clearInterval(pe.timer)
        pe.timer = null
      }
    }, 1000)
    appStore.showSuccess(t('profile.balanceNotify.codeSent'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    pe.sending = false
  }
}

async function verifyPending(idx: number) {
  const pe = pendingEmails.value[idx]
  if (!pe || !pe.code || pe.code.length !== 6) return
  pe.verifying = true
  try {
    await userAPI.verifyNotifyEmail(pe.email, pe.code)
    if (pe.timer) clearInterval(pe.timer)
    pendingEmails.value = pendingEmails.value.filter(entry => entry !== pe)
    appStore.showSuccess(t('profile.balanceNotify.verifySuccess'))
    const updated = await userAPI.getProfile()
    authStore.user = updated
    emailEntries.value = [...updated.balance_notify_extra_emails]
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    pe.verifying = false
  }
}

const handleRemoveEmail = async (email: string) => {
  if (emailEntries.value.some(entry => entry.primary && entry.email.toLowerCase() === email.toLowerCase())) {
    return
  }
  try {
    await userAPI.removeNotifyEmail(email)
    appStore.showSuccess(t('profile.balanceNotify.removeSuccess'))
    const updated = await userAPI.getProfile()
    authStore.user = updated
    emailEntries.value = [...updated.balance_notify_extra_emails]
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

// Verify saved unverified emails
async function sendCodeForSaved(email: string) {
  sendingSavedCode.value = true
  try {
    await userAPI.sendNotifyEmailCode(email)
    verifyingEmail.value = email
    verifyCode.value = ''
    verifyCountdown.value = 60
    if (verifyTimer) clearInterval(verifyTimer)
    verifyTimer = setInterval(() => {
      verifyCountdown.value--
      if (verifyCountdown.value <= 0 && verifyTimer) {
        clearInterval(verifyTimer)
        verifyTimer = null
      }
    }, 1000)
    appStore.showSuccess(t('profile.balanceNotify.codeSent'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    sendingSavedCode.value = false
  }
}

async function verifySavedEmail(email: string) {
  if (!verifyCode.value || verifyCode.value.length !== 6) return
  verifyingSaved.value = true
  try {
    await userAPI.verifyNotifyEmail(email, verifyCode.value)
    verifyingEmail.value = ''
    verifyCode.value = ''
    if (verifyTimer) { clearInterval(verifyTimer); verifyTimer = null }
    appStore.showSuccess(t('profile.balanceNotify.verifySuccess'))
    const updated = await userAPI.getProfile()
    authStore.user = updated
    emailEntries.value = [...updated.balance_notify_extra_emails]
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    verifyingSaved.value = false
  }
}
</script>
