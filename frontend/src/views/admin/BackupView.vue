<template>
  <div class="flex flex-col gap-4 sm:gap-6 lg:gap-12">
    <!-- S3 Storage Config -->
    <SettingsSection :title="t('admin.backup.s3.title')">
      <template #description>
        {{ t('admin.backup.s3.descriptionPrefix') }}
        <UButton variant="link" class="p-0 align-baseline" @click="showR2Guide = true">Cloudflare R2</UButton>
        {{ t('admin.backup.s3.descriptionSuffix') }}
      </template>
      <template #actions>
        <UButton color="neutral" variant="outline" :loading="testingS3" :disabled="testingS3" @click="testS3">
          {{ testingS3 ? t('common.loading') : t('admin.backup.s3.testConnection') }}
        </UButton>
        <UButton :loading="savingS3" :disabled="savingS3" @click="saveS3Config">
          {{ savingS3 ? t('common.loading') : t('common.save') }}
        </UButton>
      </template>
      <UFormField :label="t('admin.backup.s3.endpoint')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="s3Form.endpoint" placeholder="https://<account_id>.r2.cloudflarestorage.com" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.backup.s3.region')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="s3Form.region" placeholder="auto" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.backup.s3.bucket')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="s3Form.bucket" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.backup.s3.prefix')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="s3Form.prefix" placeholder="backups/" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.backup.s3.accessKeyId')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="s3Form.access_key_id" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.backup.s3.secretAccessKey')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput
          v-model="s3Form.secret_access_key"
          type="password"
          :placeholder="s3SecretConfigured ? t('admin.backup.s3.secretConfigured') : ''"
          class="w-full"
        />
      </UFormField>
      <UFormField :label="t('admin.backup.s3.forcePathStyle')" class="flex items-center justify-between gap-4">
        <Toggle v-model="s3Form.force_path_style" />
      </UFormField>
    </SettingsSection>

    <!-- Async image object storage -->
    <SettingsSection :title="t('admin.backup.imageStorage.title')" :description="t('admin.backup.imageStorage.description')">
      <template #actions>
        <UButton color="neutral" variant="outline" :loading="testingImageStorage" :disabled="testingImageStorage" @click="testImageStorage">
          {{ testingImageStorage ? t('common.loading') : t('admin.backup.s3.testConnection') }}
        </UButton>
        <UButton :loading="savingImageStorage" :disabled="savingImageStorage" @click="saveImageStorageConfig">
          {{ savingImageStorage ? t('common.loading') : t('common.save') }}
        </UButton>
      </template>
      <UFormField :label="t('admin.backup.imageStorage.enabled')" class="flex items-center justify-between gap-4">
        <Toggle v-model="imageStorageForm.enabled" />
      </UFormField>
      <UFormField :label="t('admin.backup.imageStorage.reuseBackupS3')" class="flex items-center justify-between gap-4">
        <Toggle v-model="imageStorageForm.reuse_backup_s3" />
      </UFormField>
      <UFormField :label="t('admin.backup.imageStorage.bucket')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput
          v-model="imageStorageForm.bucket"
          :placeholder="imageStorageForm.reuse_backup_s3 ? t('admin.backup.imageStorage.bucketInherited') : ''"
          class="w-full"
        />
      </UFormField>
      <UFormField :label="t('admin.backup.imageStorage.prefix')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="imageStorageForm.prefix" placeholder="images/" class="w-full" />
      </UFormField>
      <template v-if="!imageStorageForm.reuse_backup_s3">
        <UFormField :label="t('admin.backup.s3.endpoint')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
          <UInput v-model="imageStorageForm.endpoint" placeholder="https://<account_id>.r2.cloudflarestorage.com" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.backup.s3.region')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
          <UInput v-model="imageStorageForm.region" placeholder="auto" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.backup.s3.accessKeyId')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
          <UInput v-model="imageStorageForm.access_key_id" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.backup.s3.secretAccessKey')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
          <UInput
            v-model="imageStorageForm.secret_access_key"
            type="password"
            :placeholder="imageStorageSecretConfigured ? t('admin.backup.s3.secretConfigured') : ''"
            class="w-full"
          />
        </UFormField>
        <UFormField :label="t('admin.backup.s3.forcePathStyle')" class="flex items-center justify-between gap-4">
          <Toggle v-model="imageStorageForm.force_path_style" />
        </UFormField>
      </template>
      <UFormField :label="t('admin.backup.imageStorage.publicBaseUrl')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'w-full sm:max-w-sm' }">
        <UInput v-model="imageStorageForm.public_base_url" :placeholder="t('admin.backup.imageStorage.publicBaseUrlPlaceholder')" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.backup.imageStorage.presignExpiryHours')" class="flex max-sm:flex-col justify-between items-start gap-4">
        <UInput v-model.number="imageStorageForm.presign_expiry_hours" type="number" min="1" class="w-32" />
      </UFormField>
    </SettingsSection>

    <!-- Schedule Config -->
    <SettingsSection :title="t('admin.backup.schedule.title')" :description="t('admin.backup.schedule.description')">
      <template #actions>
        <UButton :loading="savingSchedule" :disabled="savingSchedule" @click="saveSchedule">
          {{ savingSchedule ? t('common.loading') : t('common.save') }}
        </UButton>
      </template>
      <UFormField :label="t('admin.backup.schedule.enabled')" class="flex items-center justify-between gap-4">
        <Toggle v-model="scheduleForm.enabled" />
      </UFormField>
      <UFormField :label="t('admin.backup.schedule.cronExpr')" :description="t('admin.backup.schedule.cronHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
        <UInput v-model="scheduleForm.cron_expr" placeholder="0 2 * * *" class="w-48 font-mono" />
      </UFormField>
      <UFormField :label="t('admin.backup.schedule.retainDays')" :description="t('admin.backup.schedule.retainDaysHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
        <UInput v-model.number="scheduleForm.retain_days" type="number" min="0" class="w-32" />
      </UFormField>
      <UFormField :label="t('admin.backup.schedule.retainCount')" :description="t('admin.backup.schedule.retainCountHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
        <UInput v-model.number="scheduleForm.retain_count" type="number" min="0" class="w-32" />
      </UFormField>
    </SettingsSection>

    <!-- Backup Operations -->
    <SettingsSection :title="t('admin.backup.operations.title')" :description="t('admin.backup.operations.description')">
      <template #actions>
        <UInput v-model.number="manualExpireDays" type="number" min="0" class="w-36" :aria-label="t('admin.backup.operations.expireDays')">
          <template #trailing><span class="text-xs text-muted">{{ t('admin.backup.operations.expireDays') }}</span></template>
        </UInput>
        <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loadingBackups" :disabled="loadingBackups" @click="loadBackups">
          {{ loadingBackups ? t('common.loading') : t('common.refresh') }}
        </UButton>
        <UButton icon="i-lucide-database-backup" :loading="creatingBackup" :disabled="creatingBackup" @click="createBackup">
          {{ creatingBackup ? t('admin.backup.operations.backing') : t('admin.backup.operations.createBackup') }}
        </UButton>
      </template>
      <UTable :data="backups" :columns="backupColumns" :empty="t('admin.backup.empty')" :ui="{ base: 'min-w-[800px]', th: 'px-3 py-2 text-xs', td: 'px-3 py-2.5 text-xs align-top' }">
        <template #id-cell="{ row }"><span class="font-mono">{{ row.original.id }}</span></template>
        <template #status-cell="{ row: { original: record } }">
          <UBadge :color="statusColor(record.status)" variant="subtle" size="sm">
            {{ record.status === 'running' && record.progress ? t(`admin.backup.progress.${record.progress}`) : t(`admin.backup.status.${record.status}`) }}
          </UBadge>
        </template>
        <template #size-cell="{ row }">{{ formatSize(row.original.size_bytes) }}</template>
        <template #parts-cell="{ row: { original: record } }">{{ record.parts?.length || (record.status === 'running' ? '-' : 1) }}</template>
        <template #expires_at-cell="{ row: { original: record } }">{{ record.expires_at ? formatDate(record.expires_at) : t('admin.backup.neverExpire') }}</template>
        <template #triggered_by-cell="{ row: { original: record } }">{{ record.triggered_by === 'scheduled' ? t('admin.backup.trigger.scheduled') : t('admin.backup.trigger.manual') }}</template>
        <template #started_at-cell="{ row }">{{ formatDate(row.original.started_at) }}</template>
        <template #actions-cell="{ row: { original: record } }">
          <div class="flex flex-wrap gap-1">
            <UButton v-if="record.status === 'completed'" color="neutral" variant="outline" size="xs" @click="downloadBackup(record.id)">
              {{ t('admin.backup.actions.download') }}
            </UButton>
            <UButton v-if="record.status === 'completed'" color="neutral" variant="outline" size="xs" :loading="restoringId === record.id" :disabled="restoringId === record.id" @click="restoreBackup(record.id)">
              {{ restoringId === record.id ? t('common.loading') : t('admin.backup.actions.restore') }}
            </UButton>
            <UButton v-if="record.status !== 'running'" color="error" variant="ghost" size="xs" @click="removeBackup(record.id)">
              {{ t('common.delete') }}
            </UButton>
          </div>
        </template>
      </UTable>
    </SettingsSection>
  </div>

  <!-- Cloudflare R2 Setup Guide -->
  <BaseDialog :show="showR2Guide" :title="t('admin.backup.r2Guide.title')" width="wide" @close="showR2Guide = false">
    <div class="space-y-5 text-sm">
      <p class="text-muted">{{ t('admin.backup.r2Guide.intro') }}</p>
      <section>
        <p class="mb-2 flex items-center gap-2 font-medium text-highlighted">
          <UBadge color="primary" variant="subtle" size="sm" class="size-5 justify-center rounded-full p-0">1</UBadge>
          {{ t('admin.backup.r2Guide.step1.title') }}
        </p>
        <ol class="ml-7 list-decimal space-y-1 text-toned">
          <li>{{ t('admin.backup.r2Guide.step1.line1') }}</li>
          <li>{{ t('admin.backup.r2Guide.step1.line2') }}</li>
          <li>{{ t('admin.backup.r2Guide.step1.line3') }}</li>
        </ol>
      </section>
      <section>
        <p class="mb-2 flex items-center gap-2 font-medium text-highlighted">
          <UBadge color="primary" variant="subtle" size="sm" class="size-5 justify-center rounded-full p-0">2</UBadge>
          {{ t('admin.backup.r2Guide.step2.title') }}
        </p>
        <ol class="ml-7 list-decimal space-y-1 text-toned">
          <li>{{ t('admin.backup.r2Guide.step2.line1') }}</li>
          <li>{{ t('admin.backup.r2Guide.step2.line2') }}</li>
          <li>{{ t('admin.backup.r2Guide.step2.line3') }}</li>
          <li>{{ t('admin.backup.r2Guide.step2.line4') }}</li>
        </ol>
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" class="mt-2 ml-7 w-auto" :description="t('admin.backup.r2Guide.step2.warning')" />
      </section>
      <section>
        <p class="mb-2 flex items-center gap-2 font-medium text-highlighted">
          <UBadge color="primary" variant="subtle" size="sm" class="size-5 justify-center rounded-full p-0">3</UBadge>
          {{ t('admin.backup.r2Guide.step3.title') }}
        </p>
        <p class="ml-7 text-toned">{{ t('admin.backup.r2Guide.step3.desc') }}</p>
        <code class="ml-7 mt-1 block rounded bg-elevated px-3 py-2 font-mono text-xs text-highlighted">https://&lt;{{ t('admin.backup.r2Guide.step3.accountId') }}&gt;.r2.cloudflarestorage.com</code>
      </section>
      <section>
        <p class="mb-2 flex items-center gap-2 font-medium text-highlighted">
          <UBadge color="primary" variant="subtle" size="sm" class="size-5 justify-center rounded-full p-0">4</UBadge>
          {{ t('admin.backup.r2Guide.step4.title') }}
        </p>
        <dl class="ml-7 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
          <template v-for="(row, i) in r2ConfigRows" :key="i">
            <dt class="whitespace-nowrap text-muted">{{ row.field }}</dt>
            <dd class="text-toned"><code class="font-mono text-xs">{{ row.value }}</code></dd>
          </template>
        </dl>
      </section>
      <UAlert color="success" variant="subtle" icon="i-lucide-circle-check" :description="t('admin.backup.r2Guide.freeTier')" />
    </div>
    <template #footer>
      <div class="flex justify-end">
        <UButton @click="showR2Guide = false">{{ t('common.close') }}</UButton>
      </div>
    </template>
  </BaseDialog>

  <!-- 分卷下载链接 -->
  <BaseDialog :show="downloadPartsModalOpen" :title="t('admin.backup.actions.downloadParts')" @close="closeDownloadParts">
    <p class="mb-3 text-sm text-muted">{{ t('admin.backup.actions.downloadPartsHint') }}</p>
    <div class="divide-y divide-default">
      <div v-for="part in downloadParts" :key="part.index" class="flex items-center justify-between gap-3 py-2">
        <span class="text-sm text-toned">
          {{ t('admin.backup.actions.partLabel', { index: part.index }) }}
          <span class="ml-2 text-xs text-muted">{{ formatSize(part.size_bytes) }}</span>
        </span>
        <UButton :to="part.url" rel="noopener" color="neutral" variant="outline" size="xs" icon="i-lucide-download">
          {{ t('admin.backup.actions.download') }}
        </UButton>
      </div>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <UButton @click="closeDownloadParts">{{ t('common.close') }}</UButton>
      </div>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="backupStepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import type {
  BackupS3Config,
  BackupScheduleConfig,
  BackupRecord,
  BackupDownloadPart,
  ImageStorageConfig,
} from '@/api/admin/backup'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import SettingsSection from '@/views/admin/settings/SettingsSection.vue'
import type { TableColumn } from '@nuxt/ui'

const { t } = useI18n()
const appStore = useAppStore()
const backupStepUp = useStepUp()

// 敏感操作被 2FA 门控拦截时的统一提示。
function reportStepUpBlocked(error: unknown): boolean {
  if (!isStepUpBlocked(error)) return false
  appStore.showError(
    stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
      ? t('stepUp.adminApiKeyForbidden')
      : t('stepUp.notEnabled')
  )
  return true
}

// S3 config
const s3Form = ref<BackupS3Config>({
  endpoint: '',
  region: 'auto',
  bucket: '',
  access_key_id: '',
  secret_access_key: '',
  prefix: 'backups/',
  force_path_style: false,
})
const s3SecretConfigured = ref(false)
const savingS3 = ref(false)
const testingS3 = ref(false)

// Async image object storage. Shares the S3 client with backups, so the default is
// to reuse the credentials configured above and only differ by prefix.
const imageStorageForm = ref<ImageStorageConfig>({
  enabled: false,
  reuse_backup_s3: true,
  bucket: '',
  prefix: 'images/',
  public_base_url: '',
  presign_expiry_hours: 24,
  max_download_bytes: 33554432,
  endpoint: '',
  region: 'auto',
  access_key_id: '',
  secret_access_key: '',
  force_path_style: false,
})
const imageStorageSecretConfigured = ref(false)
const savingImageStorage = ref(false)
const testingImageStorage = ref(false)

// Schedule config
const scheduleForm = ref<BackupScheduleConfig>({
  enabled: false,
  cron_expr: '0 2 * * *',
  retain_days: 14,
  retain_count: 10,
})
const savingSchedule = ref(false)

// Backups
const backups = ref<BackupRecord[]>([])
const loadingBackups = ref(false)
const creatingBackup = ref(false)
const restoringId = ref('')
const manualExpireDays = ref(14)
const downloadParts = ref<BackupDownloadPart[]>([])
const downloadPartsModalOpen = ref(false)

// Polling
const pollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const restoringPollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const MAX_POLL_COUNT = 900

function updateRecordInList(updated: BackupRecord) {
  const idx = backups.value.findIndex(r => r.id === updated.id)
  if (idx >= 0) {
    backups.value[idx] = updated
  }
}

function startPolling(backupId: string) {
  stopPolling()
  let count = 0
  pollingTimer.value = setInterval(async () => {
    if (count++ >= MAX_POLL_COUNT) {
      stopPolling()
      creatingBackup.value = false
      appStore.showWarning(t('admin.backup.operations.backupRunning'))
      return
    }
    try {
      const record = await adminAPI.backup.getBackup(backupId)
      updateRecordInList(record)
      if (record.status === 'completed' || record.status === 'failed') {
        stopPolling()
        creatingBackup.value = false
        if (record.status === 'completed') {
          appStore.showSuccess(t('admin.backup.operations.backupCreated'))
        } else {
          appStore.showError(record.error_message || t('admin.backup.operations.backupFailed'))
        }
        await loadBackups()
      }
    } catch {
      // 轮询失败时不中断
    }
  }, 2000)
}

function stopPolling() {
  if (pollingTimer.value) {
    clearInterval(pollingTimer.value)
    pollingTimer.value = null
  }
}

function startRestorePolling(backupId: string) {
  stopRestorePolling()
  let count = 0
  restoringPollingTimer.value = setInterval(async () => {
    if (count++ >= MAX_POLL_COUNT) {
      stopRestorePolling()
      restoringId.value = ''
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
      return
    }
    try {
      const record = await adminAPI.backup.getBackup(backupId)
      updateRecordInList(record)
      if (record.restore_status === 'completed' || record.restore_status === 'failed') {
        stopRestorePolling()
        restoringId.value = ''
        if (record.restore_status === 'completed') {
          appStore.showSuccess(t('admin.backup.actions.restoreSuccess'))
        } else {
          appStore.showError(record.restore_error || t('admin.backup.operations.restoreFailed'))
        }
        await loadBackups()
      }
    } catch {
      // 轮询失败时不中断
    }
  }, 2000)
}

function stopRestorePolling() {
  if (restoringPollingTimer.value) {
    clearInterval(restoringPollingTimer.value)
    restoringPollingTimer.value = null
  }
}

function handleVisibilityChange() {
  if (document.hidden) {
    stopPolling()
    stopRestorePolling()
  } else {
    // 标签页恢复时刷新列表，检查是否仍有活跃操作
    loadBackups().then(() => {
      const running = backups.value.find(r => r.status === 'running')
      if (running) {
        creatingBackup.value = true
        startPolling(running.id)
      }
      const restoring = backups.value.find(r => r.restore_status === 'running')
      if (restoring) {
        restoringId.value = restoring.id
        startRestorePolling(restoring.id)
      }
    })
  }
}

// R2 guide
const showR2Guide = ref(false)
const r2ConfigRows = computed(() => [
  { field: t('admin.backup.s3.endpoint'), value: 'https://<account_id>.r2.cloudflarestorage.com' },
  { field: t('admin.backup.s3.region'), value: 'auto' },
  { field: t('admin.backup.s3.bucket'), value: t('admin.backup.r2Guide.step4.bucketValue') },
  { field: t('admin.backup.s3.prefix'), value: 'backups/' },
  { field: 'Access Key ID', value: t('admin.backup.r2Guide.step4.fromStep2') },
  { field: 'Secret Access Key', value: t('admin.backup.r2Guide.step4.fromStep2') },
  { field: t('admin.backup.s3.forcePathStyle'), value: t('admin.backup.r2Guide.step4.unchecked') },
])

async function loadS3Config() {
  try {
    const cfg = await adminAPI.backup.getS3Config()
    s3Form.value = {
      endpoint: cfg.endpoint || '',
      region: cfg.region || 'auto',
      bucket: cfg.bucket || '',
      access_key_id: cfg.access_key_id || '',
      secret_access_key: '',
      prefix: cfg.prefix || 'backups/',
      force_path_style: cfg.force_path_style,
    }
    s3SecretConfigured.value = Boolean(cfg.access_key_id)
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveS3Config() {
  savingS3.value = true
  try {
    await backupStepUp.run(() => adminAPI.backup.updateS3Config(s3Form.value))
    appStore.showSuccess(t('admin.backup.s3.saved'))
    await loadS3Config()
  } catch (error) {
    if (isStepUpCancelled(error)) {
      savingS3.value = false
      return
    }
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingS3.value = false
  }
}

async function loadImageStorageConfig() {
  try {
    const { config, secret_configured } = await adminAPI.backup.getImageStorageConfig()
    imageStorageForm.value = {
      ...config,
      prefix: config.prefix || 'images/',
      region: config.region || 'auto',
      secret_access_key: '',
    }
    imageStorageSecretConfigured.value = secret_configured
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveImageStorageConfig() {
  savingImageStorage.value = true
  try {
    await backupStepUp.run(() => adminAPI.backup.updateImageStorageConfig(imageStorageForm.value))
    appStore.showSuccess(t('admin.backup.imageStorage.saved'))
    await loadImageStorageConfig()
  } catch (error) {
    if (isStepUpCancelled(error)) {
      savingImageStorage.value = false
      return
    }
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingImageStorage.value = false
  }
}

async function testImageStorage() {
  testingImageStorage.value = true
  try {
    const result = await adminAPI.backup.testImageStorageConnection(imageStorageForm.value)
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.backup.s3.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.backup.s3.testFailed'))
    }
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    testingImageStorage.value = false
  }
}

async function testS3() {
  testingS3.value = true
  try {
    const result = await adminAPI.backup.testS3Connection(s3Form.value)
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.backup.s3.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.backup.s3.testFailed'))
    }
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    testingS3.value = false
  }
}

async function loadSchedule() {
  try {
    const cfg = await adminAPI.backup.getSchedule()
    scheduleForm.value = {
      enabled: cfg.enabled,
      cron_expr: cfg.cron_expr || '0 2 * * *',
      retain_days: cfg.retain_days || 14,
      retain_count: cfg.retain_count || 10,
    }
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveSchedule() {
  savingSchedule.value = true
  try {
    await adminAPI.backup.updateSchedule(scheduleForm.value)
    appStore.showSuccess(t('admin.backup.schedule.saved'))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingSchedule.value = false
  }
}

async function loadBackups() {
  loadingBackups.value = true
  try {
    const result = await adminAPI.backup.listBackups()
    backups.value = result.items || []
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    loadingBackups.value = false
  }
}

async function createBackup() {
  creatingBackup.value = true
  try {
    const record = await backupStepUp.run(() => adminAPI.backup.createBackup({ expire_days: manualExpireDays.value }))
    // 插入到列表顶部
    backups.value.unshift(record)
    startPolling(record.id)
  } catch (error: any) {
    if (isStepUpCancelled(error)) {
      creatingBackup.value = false
      return
    }
    if (reportStepUpBlocked(error)) {
      creatingBackup.value = false
      return
    }
    if (error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.alreadyInProgress'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
    creatingBackup.value = false
  }
}

async function downloadBackup(id: string) {
  try {
    const result = await backupStepUp.run(() => adminAPI.backup.getDownloadURL(id))
    if (result.parts && result.parts.length > 0) {
      downloadParts.value = result.parts
      downloadPartsModalOpen.value = true
      return
    }
    if (!result.url) {
      throw new Error(t('admin.backup.actions.downloadFailed'))
    }
    // 预签名 URL 带 attachment disposition，同页 anchor 导航直接触发下载；
    // 不用 window.open：step-up 弹窗 await 会耗尽瞬态用户激活，新标签页会被浏览器拦截。
    const link = document.createElement('a')
    link.href = result.url
    link.rel = 'noopener'
    link.click()
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

function closeDownloadParts() {
  downloadPartsModalOpen.value = false
  downloadParts.value = []
}

async function restoreBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.restoreConfirm'))) return
  const password = window.prompt(t('admin.backup.actions.restorePasswordPrompt'))
  if (!password) return
  restoringId.value = id
  try {
    const record = await backupStepUp.run(() => adminAPI.backup.restoreBackup(id, password))
    updateRecordInList(record)
    startRestorePolling(id)
  } catch (error: any) {
    restoringId.value = ''
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    // apiClient 拦截器把 HTTP 错误归一化为顶层 { status } 平面对象（无 response 字段）
    if (error?.status === 409 || error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
  }
}

async function removeBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.deleteConfirm'))) return
  try {
    await adminAPI.backup.deleteBackup(id)
    appStore.showSuccess(t('admin.backup.actions.deleted'))
    await loadBackups()
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

function statusColor(status: string): 'success' | 'info' | 'error' | 'neutral' {
  if (status === 'completed') return 'success'
  if (status === 'running') return 'info'
  if (status === 'failed') return 'error'
  return 'neutral'
}

const backupColumns = computed<TableColumn<BackupRecord>[]>(() => [
  { accessorKey: 'id', header: 'ID' },
  { accessorKey: 'status', header: t('admin.backup.columns.status') },
  { accessorKey: 'file_name', header: t('admin.backup.columns.fileName') },
  { id: 'size', header: t('admin.backup.columns.size') },
  { id: 'parts', header: t('admin.backup.columns.parts') },
  { accessorKey: 'expires_at', header: t('admin.backup.columns.expiresAt') },
  { accessorKey: 'triggered_by', header: t('admin.backup.columns.triggeredBy') },
  { accessorKey: 'started_at', header: t('admin.backup.columns.startedAt') },
  { id: 'actions', header: t('admin.backup.columns.actions') },
])

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDate(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(async () => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  await Promise.all([loadS3Config(), loadImageStorageConfig(), loadSchedule(), loadBackups()])

  // 如果有正在 running 的备份，恢复轮询
  const runningBackup = backups.value.find(r => r.status === 'running')
  if (runningBackup) {
    creatingBackup.value = true
    startPolling(runningBackup.id)
  }
  const restoringBackup = backups.value.find(r => r.restore_status === 'running')
  if (restoringBackup) {
    restoringId.value = restoringBackup.id
    startRestorePolling(restoringBackup.id)
  }
})

onBeforeUnmount(() => {
  stopPolling()
  stopRestorePolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
