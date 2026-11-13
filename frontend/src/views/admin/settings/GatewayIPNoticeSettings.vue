<template>
  <SettingsSection
    :title="t('admin.settings.gatewayIPNotice.title')"
    :description="t('admin.settings.gatewayIPNotice.description')"
    :loading="loading"
    :aria-busy="loading || saving"
  >
    <template #actions>
      <UButton v-if="loadFailed" color="neutral" variant="outline" data-testid="gateway-ip-notice-retry" @click="load">
        {{ t('admin.settings.gatewayIPNotice.retry') }}
      </UButton>
      <UButton data-testid="gateway-ip-notice-save" :loading="saving" :disabled="saving || loadFailed" @click="save">
        {{ saving ? t('common.saving') : t('common.save') }}
      </UButton>
    </template>
    <SettingPanel hide-summary>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
      <SettingGroup>
        <SettingRow :label="t('admin.settings.gatewayIPNotice.enabled')" :on="form.enabled">
          <template #control>
            <Toggle id="gateway-ip-notice-enabled" v-model="form.enabled" :disabled="saving || loadFailed" />
          </template>
        </SettingRow>
        <!-- The list and the message are long text: the row shows how much is there, the editor opens on click. -->
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.ips')"
          :hint="t('admin.settings.gatewayIPNotice.ipHint')"
          :summary="t('common.settingsSummary.itemCount', { count: ipText.split(/[\s,，;；]+/).filter(Boolean).length })"
          :on="true"
          :in-summary="false"
          persistent-summary
        >
          <template #fields>
            <UTextarea
              id="gateway-ip-notice-ips"
              v-model="ipText"
              :rows="5"
              autoresize
              :disabled="saving || loadFailed"
              spellcheck="false"
              class="w-full font-mono"
            />
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.message')"
          :summary="form.message.trim() ? form.message : t('common.settingsSummary.notSet')"
          :on="true"
          :in-summary="false"
          persistent-summary
        >
          <template #fields>
            <UTextarea id="gateway-ip-notice-message" v-model="form.message" :rows="3" autoresize maxlength="2048" :disabled="saving || loadFailed" class="w-full" />
            <div v-if="form.message" class="grid gap-1">
              <span class="block text-xs font-medium text-muted">{{ t('admin.settings.gatewayIPNotice.preview') }}</span>
              <span class="block whitespace-pre-wrap wrap-break-word text-sm text-highlighted">{{ form.message }}</span>
            </div>
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.rpm')"
          :hint="[t('admin.settings.gatewayIPNotice.rpmHint'), t('admin.settings.gatewayIPNotice.effectiveHint')]"
          :on="true"
          :in-summary="false"
        >
          <template #control>
            <UInput id="gateway-ip-notice-rpm" v-model.number="form.rpm" type="number" min="1" max="100000" step="1" :disabled="saving || loadFailed" class="w-28" />
          </template>
        </SettingRow>
      </SettingGroup>

      <SettingGroup>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.autoAdd')"
          :hint="t('admin.settings.gatewayIPNotice.autoAddHint')"
          :on="form.auto_add_enabled"
        >
          <template #control>
            <Toggle id="gateway-ip-notice-auto-add" v-model="form.auto_add_enabled" :disabled="saving || loadFailed" />
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.autoThreshold')"
          :hint="t('admin.settings.gatewayIPNotice.autoThresholdHint')"
          :on="true"
          :in-summary="false"
        >
          <template #control>
            <UInput
              id="gateway-ip-notice-auto-threshold"
              v-model.number="form.sharing_threshold"
              type="number"
              min="2"
              max="100"
              step="1"
              :disabled="saving || loadFailed || !form.auto_add_enabled"
              class="w-28"
            />
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.autoWindow')"
          :hint="t('admin.settings.gatewayIPNotice.autoWindowHint')"
          :on="true"
          :in-summary="false"
        >
          <template #control>
            <UInput
              id="gateway-ip-notice-auto-window"
              v-model.number="form.sharing_window_minutes"
              type="number"
              min="1"
              max="1440"
              step="1"
              :disabled="saving || loadFailed || !form.auto_add_enabled"
              class="w-28"
            />
          </template>
        </SettingRow>
        <!-- Default off: the hit only appends the IP. Turning this on is the
             single explicit opt-in that lets a hit touch key statuses. -->
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.keyDisable')"
          :hint="t('admin.settings.gatewayIPNotice.keyDisableHint')"
          :on="form.key_disable_enabled"
        >
          <template #control>
            <Toggle
              id="gateway-ip-notice-key-disable"
              v-model="form.key_disable_enabled"
              data-testid="gateway-ip-notice-key-disable"
              :disabled="saving || loadFailed || !form.auto_add_enabled"
            />
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.autoEntries')"
          :hint="t('admin.settings.gatewayIPNotice.autoEntriesHint')"
          :summary="t('common.settingsSummary.itemCount', { count: form.auto_entries.length })"
          :on="true"
          :in-summary="false"
          persistent-summary
        >
          <template #fields>
            <UAlert
              v-if="form.auto_entries.length && !form.enabled"
              color="warning"
              variant="subtle"
              icon="i-lucide-triangle-alert"
              :description="t('admin.settings.gatewayIPNotice.autoEntriesInactive')"
            />
            <p v-if="!form.auto_entries.length" class="text-sm text-muted">
              {{ t('admin.settings.gatewayIPNotice.autoEntriesEmpty') }}
            </p>
            <ul v-else class="grid gap-2">
              <li
                v-for="entry in form.auto_entries"
                :key="entry.ip"
                class="grid gap-1 rounded-lg border border-default px-3 py-2 text-sm"
                :data-testid="`gateway-ip-notice-auto-entry-${entry.ip}`"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-mono text-highlighted">{{ entry.ip }}</span>
                  <span class="rounded-md border border-default px-1.5 py-0.5 text-xs text-muted">
                    {{ t('admin.settings.gatewayIPNotice.autoBadge') }}
                  </span>
                  <UButton
                    size="xs"
                    color="error"
                    variant="ghost"
                    :disabled="saving || loadFailed"
                    :data-testid="`gateway-ip-notice-remove-auto-${entry.ip}`"
                    @click="removeAutoEntry(entry.ip)"
                  >
                    {{ t('admin.settings.gatewayIPNotice.autoRemove') }}
                  </UButton>
                </div>
                <span class="text-xs text-muted">
                  {{
                    t('admin.settings.gatewayIPNotice.autoStats', {
                      first: entryFirstSeenUsers(entry),
                      peak: entryPeakUsers(entry),
                      last: entryLastSeen(entry),
                    })
                  }}
                </span>
                <span class="text-xs text-muted">
                  {{ t('admin.settings.gatewayIPNotice.autoWindowInfo', { window: entry.window_minutes || '—' }) }}
                </span>
                <!-- Which accounts, not how many keys: one user may hold several keys,
                     and a rotating relay shows a different prefix every time. -->
                <span
                  v-if="entryUserLabels(entry).length"
                  class="text-xs text-muted"
                  :data-testid="`gateway-ip-notice-auto-users-${entry.ip}`"
                >
                  {{ t('admin.settings.gatewayIPNotice.autoUsers', { count: entryDistinctUsers(entry) }) }}:
                  {{ entryUserLabels(entry).join('、') }}
                </span>
                <span v-else class="text-xs text-muted">
                  {{ t('admin.settings.gatewayIPNotice.autoUsersUnknown') }}
                </span>
                <span
                  v-if="entryTotalKeys(entry) > 0"
                  class="text-xs text-muted"
                  :data-testid="`gateway-ip-notice-auto-key-total-${entry.ip}`"
                >
                  {{ t('admin.settings.gatewayIPNotice.autoKeyTotal', { total: entryTotalKeys(entry) }) }}
                </span>
              </li>
            </ul>
            <p v-if="form.auto_entries.length" class="text-xs text-muted">
              {{ t('admin.settings.gatewayIPNotice.autoRemoveHint') }}
            </p>
          </template>
        </SettingRow>
      </SettingGroup>
    </SettingPanel>
  </SettingsSection>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getGatewayIPNotice, updateGatewayIPNotice, type GatewayIPNoticeAutoEntry, type GatewayIPNoticeAutoEntryUser, type GatewayIPNoticeSettings } from '@/api/admin/gatewayIpNotice'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTimeToMinute } from '@/utils/format'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import SettingPanel from '@/components/common/SettingPanel.vue'
import SettingGroup from '@/components/common/SettingGroup.vue'
import SettingRow from '@/components/common/SettingRow.vue'
import SettingsSection from './SettingsSection.vue'

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const loading = ref(true)
const saving = ref(false)
const loadFailed = ref(false)
const error = ref('')
const ipText = ref('')
const emptyForm = (): GatewayIPNoticeSettings => ({
  enabled: false,
  ips: [],
  message: '',
  rpm: 100,
  auto_add_enabled: true,
  sharing_window_minutes: 10,
  sharing_threshold: 3,
  key_disable_enabled: false,
  auto_entries: [],
})
const form = reactive<GatewayIPNoticeSettings>(emptyForm())

function applyPolicy(policy: GatewayIPNoticeSettings) {
  Object.assign(form, policy)
  // Older stored policies predate these fields; keep the UI usable.
  form.auto_add_enabled = policy.auto_add_enabled ?? true
  form.sharing_window_minutes = policy.sharing_window_minutes || 10
  form.sharing_threshold = policy.sharing_threshold || 3
  // Observe-only is the product default: an older policy has no field at all.
  form.key_disable_enabled = policy.key_disable_enabled ?? false
  form.auto_entries = [...(policy.auto_entries ?? [])]
  ipText.value = form.ips.join('\n')
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    applyPolicy(await getGatewayIPNotice())
    loadFailed.value = false
  } catch (err) {
    loadFailed.value = true
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}

// Removal reuses the same save path as the hand-written list: the entry
// disappears from the effective list once the form is saved.
function removeAutoEntry(ip: string) {
  form.auto_entries = form.auto_entries.filter((entry) => entry.ip !== ip)
}

// The four evidence counters are all optional on the wire because entries
// written before they existed only carry distinct_users and key_prefixes. Each
// helper degrades to the legacy value instead of rendering NaN or "undefined".
function entryFirstSeenUsers(entry: GatewayIPNoticeAutoEntry): number {
  return entry.first_seen_users ?? entry.distinct_users ?? 0
}

function entryPeakUsers(entry: GatewayIPNoticeAutoEntry): number {
  return entry.peak_users ?? entry.distinct_users ?? 0
}

function entryTotalKeys(entry: GatewayIPNoticeAutoEntry): number {
  return entry.total_keys ?? entry.key_prefixes?.length ?? 0
}

// entryDistinctUsers is the headline number: how many ACCOUNTS share this IP.
// entryTotalKeys below is context, never the entry's reason for existing.
function entryDistinctUsers(entry: GatewayIPNoticeAutoEntry): number {
  return entryPeakUsers(entry)
}

// entryUserLabels renders the roster as "email" (or "#id" when the gateway never
// saw the key's user loaded), capped so one long-lived entry cannot flood the page.
function entryUserLabels(entry: GatewayIPNoticeAutoEntry): string[] {
  const users = entry.users ?? []
  return users.slice(0, 8).map(inspectUserLabel)
}

function inspectUserLabel(user: GatewayIPNoticeAutoEntryUser): string {
  const email = user.email?.trim()
  return email || `#${user.id}`
}

function entryLastSeen(entry: GatewayIPNoticeAutoEntry): string {
  return formatDateTimeToMinute(entry.last_seen_at || entry.first_seen_at) || '—'
}

async function save() {
  if (saving.value || loadFailed.value) return
  const ips = [...new Set(ipText.value.split(/[\s,，;；]+/).filter(Boolean))]
  const validThreshold = Number.isInteger(form.sharing_threshold) && form.sharing_threshold >= 2 && form.sharing_threshold <= 100
  const validWindow = Number.isInteger(form.sharing_window_minutes) && form.sharing_window_minutes >= 1 && form.sharing_window_minutes <= 1440
  if (
    !Number.isInteger(form.rpm) ||
    form.rpm < 1 ||
    form.rpm > 100000 ||
    !validThreshold ||
    !validWindow ||
    ips.length + form.auto_entries.length > 256 ||
    (form.enabled && ((!ips.length && !form.auto_entries.length) || !form.message.trim()))
  ) {
    error.value = t('admin.settings.gatewayIPNotice.invalid')
    return
  }
  saving.value = true
  error.value = ''
  try {
    applyPolicy(await stepUp.run(() => updateGatewayIPNotice({ ...form, ips, auto_entries: [...form.auto_entries] })))
    appStore.showSuccess(t('admin.settings.gatewayIPNotice.saved'))
  } catch (err) {
    if (isStepUpCancelled(err)) return
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
