<template>
  <!-- Notification section of the dashboard template: a title card, then a subtle card with a
       switch row and, when enabled, the threshold and recipient rows. -->
  <section>
    <UPageCard
      :title="t('profile.balanceNotify.title')"
      :description="t('profile.balanceNotify.description')"
      variant="naked"
      class="mb-4"
    />
    <UPageCard variant="subtle">
      <UFormField
        :label="t('profile.balanceNotify.enabled')"
        class="flex items-center justify-between gap-2"
      >
        <USwitch :model-value="notifyEnabled" @update:model-value="onNotifyToggle" />
      </UFormField>

      <template v-if="notifyEnabled">
        <USeparator />
        <UFormField
          :label="t('profile.balanceNotify.threshold')"
          :description="t('profile.balanceNotify.thresholdHint')"
          class="flex justify-between gap-4 max-sm:flex-col sm:items-center"
        >
          <div class="flex items-center gap-2">
            <UInput
              v-model.number="customThreshold"
              type="number"
              min="0"
              step="0.01"
              class="w-full sm:w-48"
              :placeholder="systemDefaultThreshold > 0 ? `${t('profile.balanceNotify.systemDefault')} $${systemDefaultThreshold}` : t('profile.balanceNotify.thresholdPlaceholder')"
            >
              <template #leading><span class="text-sm text-muted">$</span></template>
            </UInput>
            <UButton :disabled="savingThreshold" @click="handleThresholdUpdate">
              {{ savingThreshold ? t('common.saving') : t('common.save') }}
            </UButton>
          </div>
        </UFormField>

        <USeparator />
        <UFormField
          :label="t('profile.balanceNotify.extraEmails')"
          :description="t('profile.balanceNotify.extraEmailsHint')"
          :ui="{ container: 'mt-3 space-y-3' }"
        >
          <div v-if="emailEntries.length > 0 || pendingEmails.length > 0" class="divide-y divide-default">
            <!-- Saved email entries -->
            <div
              v-for="(entry, idx) in emailEntries"
              :key="`${entry.primary ? 'primary' : entry.email}-${idx}`"
              class="flex flex-wrap items-center justify-between gap-2 py-2.5 first:pt-0"
            >
              <div class="flex min-w-0 flex-1 items-center gap-2">
                <USwitch
                  size="sm"
                  :model-value="!entry.disabled"
                  :aria-label="entry.email"
                  @update:model-value="handleEmailToggle(entry)"
                />
                <span class="truncate text-sm text-default">{{ entry.email }}</span>
                <UBadge v-if="entry.primary" color="neutral" variant="subtle" size="sm">
                  {{ t('profile.balanceNotify.primaryEmail') }}
                </UBadge>
              </div>
              <div class="flex shrink-0 flex-wrap items-center gap-1.5">
                <template v-if="!entry.verified">
                  <!-- Inline verify flow for saved unverified emails -->
                  <template v-if="verifyingEmail === entry.email">
                    <UInput
                      v-model="verifyCode"
                      type="text"
                      maxlength="6"
                      size="sm"
                      class="w-24"
                      :placeholder="t('profile.balanceNotify.codePlaceholder')"
                    />
                    <UButton size="sm" color="neutral" variant="outline" :disabled="!verifyCode || verifyCode.length !== 6 || verifyingSaved" @click="verifySavedEmail(entry.email)">
                      {{ t('profile.balanceNotify.verify') }}
                    </UButton>
                    <span v-if="verifyCountdown > 0" class="text-xs tabular-nums text-dimmed">{{ verifyCountdown }}s</span>
                    <UButton v-else size="sm" color="neutral" variant="ghost" :disabled="sendingSavedCode" @click="sendCodeForSaved(entry.email)">
                      {{ t('profile.balanceNotify.resend') }}
                    </UButton>
                    <UButton size="sm" color="neutral" variant="ghost" @click="verifyingEmail = ''">
                      {{ t('common.cancel') }}
                    </UButton>
                  </template>
                  <template v-else>
                    <UBadge color="warning" variant="subtle" size="sm">{{ t('profile.balanceNotify.unverified') }}</UBadge>
                    <UButton size="sm" color="neutral" variant="outline" :disabled="sendingSavedCode" @click="sendCodeForSaved(entry.email)">
                      {{ t('profile.balanceNotify.verify') }}
                    </UButton>
                  </template>
                </template>
                <UBadge v-else color="success" variant="subtle" size="sm">{{ t('profile.balanceNotify.verified') }}</UBadge>
                <UButton v-if="!entry.primary" size="sm" color="error" variant="ghost" @click="handleRemoveEmail(entry.email)">
                  {{ t('profile.balanceNotify.removeEmail') }}
                </UButton>
              </div>
            </div>

            <!-- Pending (unverified) emails in the verification flow -->
            <div
              v-for="(pe, idx) in pendingEmails"
              :key="pe.email"
              data-testid="balance-notify-pending"
              class="flex flex-wrap items-center justify-between gap-2 py-2.5 first:pt-0"
            >
              <span class="min-w-0 flex-1 truncate text-sm text-default">{{ pe.email }}</span>
              <div v-if="!pe.codeSent" class="flex items-center gap-1.5">
                <UButton size="sm" color="neutral" variant="outline" :disabled="pe.sending" @click="sendCodeFor(idx)">
                  {{ t('profile.balanceNotify.sendCode') }}
                </UButton>
                <UButton size="sm" color="error" variant="ghost" @click="pendingEmails.splice(idx, 1)">
                  {{ t('profile.balanceNotify.removeEmail') }}
                </UButton>
              </div>
              <div v-else class="flex items-center gap-1.5">
                <UInput
                  v-model="pe.code"
                  type="text"
                  maxlength="6"
                  size="sm"
                  class="w-24"
                  :placeholder="t('profile.balanceNotify.codePlaceholder')"
                />
                <UButton size="sm" color="neutral" variant="outline" :disabled="!pe.code || pe.code.length !== 6 || pe.verifying" @click="verifyPending(idx)">
                  {{ t('profile.balanceNotify.verify') }}
                </UButton>
                <span v-if="pe.countdown > 0" class="text-xs tabular-nums text-dimmed">{{ pe.countdown }}s</span>
                <UButton v-else size="sm" color="neutral" variant="ghost" :disabled="pe.sending" @click="sendCodeFor(idx)">
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
            <UButton color="neutral" variant="outline" :disabled="!newEmail" @click="addPendingEmail">
              {{ t('common.add') }}
            </UButton>
          </div>
          <p v-else class="text-sm text-muted">
            {{ t('profile.balanceNotify.maxEmailsReached') }}
          </p>
        </UFormField>
      </template>
    </UPageCard>
  </section>
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

// USwitch is controlled: set the value first, then persist it (handleToggle reverts on failure).
function onNotifyToggle(value: boolean) {
  notifyEnabled.value = value
  void handleToggle()
}

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
