<template>
  <div class="relative">
    <!-- Admin: Full version badge with dropdown -->
    <UPopover v-if="canManageSystem" v-model:open="dropdownOpen" :content="{ align: 'start' }">
      <UButton
        :color="hasUpdate ? 'warning' : 'neutral'"
        variant="soft"
        size="xs"
        class="gap-1.5 font-medium tabular-nums"
        :title="hasUpdate ? t('version.updateAvailable') : t('version.upToDate')"
      >
        <span v-if="currentVersion">{{ displayCurrentVersion }}</span>
        <USkeleton v-else class="h-3 w-12" />
        <!-- Update indicator -->
        <span v-if="hasUpdate" class="size-2 rounded-full bg-warning" aria-hidden="true"></span>
      </UButton>

      <template #content>
        <div
          class="whitespace-normal transition-[width] duration-200"
          :class="rollbackPanelOpen && isReleaseBuild ? 'w-80' : 'w-64'"
        >
          <!-- Header with refresh button -->
          <div class="flex items-center justify-between px-4 pb-1 pt-3">
            <span class="text-sm font-medium text-highlighted">{{ t('version.currentVersion') }}</span>
            <UButton
              color="neutral"
              variant="ghost"
              size="xs"
              icon="i-lucide-refresh-cw"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :disabled="loading"
              :title="t('version.refresh')"
              :aria-label="t('version.refresh')"
              @click="refreshVersion(true)"
            />
          </div>

          <div class="p-4 pt-2">
            <!-- Loading state -->
            <div v-if="loading" class="flex items-center justify-center py-6">
              <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
            </div>

            <!-- Content -->
            <template v-else>
              <!-- Version display - centered and prominent -->
              <div class="mb-4 text-center">
                <div class="inline-flex items-center gap-2">
                  <span v-if="currentVersion" class="text-2xl font-semibold text-highlighted">{{ displayCurrentVersion }}</span>
                  <span v-else class="text-2xl font-semibold text-dimmed">--</span>
                  <!-- Show check mark when up to date -->
                  <UIcon v-if="!hasUpdate" name="i-lucide-circle-check" class="size-5 text-success" />
                </div>
                <p class="mt-1 text-xs text-muted">
                  {{
                    hasUpdate
                      ? t('version.latestVersion') + ': ' + displayLatestVersion
                      : t('version.upToDate')
                  }}
                </p>
              </div>
              <!-- Priority 1: Update error (must check before hasUpdate) -->
              <div v-if="updateError" class="space-y-2">
                <UAlert
                  color="error"
                  variant="subtle"
                  icon="i-lucide-circle-x"
                  :title="t('version.updateFailed')"
                  :description="updateError"
                  :ui="{ description: 'truncate' }"
                />
                <!-- Retry button -->
                <UButton color="error" block :label="t('version.retry')" :disabled="updating" @click="handleUpdate" />
              </div>

              <!-- Priority 2: Update success - need restart -->
              <div v-else-if="updateSuccess && needRestart" class="space-y-2">
                <UAlert
                  color="success"
                  variant="subtle"
                  icon="i-lucide-check"
                  :title="successKind === 'rollback' ? t('version.rollbackComplete') : t('version.updateComplete')"
                  :description="t('version.restartRequired')"
                />
                <!-- Restart button with countdown -->
                <UButton
                  color="success"
                  block
                  icon="i-lucide-rotate-ccw"
                  :loading="restarting"
                  :disabled="restarting"
                  @click="handleRestart"
                >
                  <template v-if="restarting">
                    <span>{{ t('version.restarting') }}</span>
                    <span v-if="restartCountdown > 0" class="tabular-nums">({{ restartCountdown }}s)</span>
                  </template>
                  <span v-else>{{ t('version.restartNow') }}</span>
                </UButton>
              </div>

              <!-- Orchestrated deployments already restarted and health-checked. -->
              <div v-else-if="updateSuccess" class="space-y-2">
                <UAlert
                  color="success"
                  variant="subtle"
                  icon="i-lucide-check"
                  :title="successKind === 'rollback' ? t('version.rollbackComplete') : t('version.updateComplete')"
                  :description="t('version.upToDate')"
                />
              </div>

              <!-- Priority 3: Update available for source build - show git pull hint -->
              <div v-else-if="hasUpdate && !isReleaseBuild" class="space-y-2">
                <UButton
                  v-if="releaseInfo?.html_url && releaseInfo.html_url !== '#'"
                  :to="releaseInfo.html_url"
                  target="_blank"
                  color="warning"
                  variant="subtle"
                  block
                  icon="i-lucide-download"
                  trailing-icon="i-lucide-chevron-right"
                  class="justify-start"
                >
                  <span class="min-w-0 flex-1 text-left">
                    <span class="block text-sm font-medium">{{ t('version.updateAvailable') }}</span>
                    <span class="block text-xs opacity-80">{{ displayLatestVersion }}</span>
                  </span>
                </UButton>
                <!-- Source build hint -->
                <UAlert color="info" variant="subtle" icon="i-lucide-info" :description="t('version.sourceModeHint')" />
              </div>

              <!-- Priority 4: Update available for release build - show update button -->
              <div v-else-if="hasUpdate && isReleaseBuild" class="space-y-2">
                <!-- Update info card -->
                <UAlert
                  color="warning"
                  variant="subtle"
                  icon="i-lucide-download"
                  :title="t('version.updateAvailable')"
                  :description="displayLatestVersion"
                />
                <!-- Update button -->
                <UAlert
                  v-if="updateDisabled"
                  color="info"
                  variant="subtle"
                  icon="i-lucide-info"
                  :description="t('version.updateDisabledHint')"
                />
                <UButton
                  v-else
                  color="primary"
                  block
                  icon="i-lucide-download"
                  :loading="updating"
                  :disabled="updating"
                  :label="updating ? t('version.updating') : t('version.updateNow')"
                  @click="handleUpdate"
                />
                <!-- View release link -->
                <UButton
                  v-if="releaseInfo?.html_url && releaseInfo.html_url !== '#'"
                  :to="releaseInfo.html_url"
                  target="_blank"
                  color="neutral"
                  variant="link"
                  size="xs"
                  block
                  trailing-icon="i-lucide-external-link"
                  :label="t('version.viewChangelog')"
                />
              </div>
              <!-- Priority 5: Up to date - GitHub link + version rollback -->
              <div v-else class="space-y-2">
                <UButton
                  v-if="releaseInfo?.html_url && releaseInfo.html_url !== '#'"
                  :to="releaseInfo.html_url"
                  target="_blank"
                  color="neutral"
                  variant="ghost"
                  block
                  icon="i-lucide-github"
                  :label="t('version.viewRelease')"
                />

                <!-- Version rollback entry -->
                <UCollapsible :open="rollbackPanelOpen" class="pt-1">
                  <UButton
                    color="neutral"
                    variant="ghost"
                    size="xs"
                    block
                    icon="i-lucide-history"
                    trailing-icon="i-lucide-chevron-down"
                    :label="t('version.rollback')"
                    class="justify-between text-muted"
                    :ui="{ trailingIcon: ['transition-transform duration-200', rollbackPanelOpen ? 'rotate-180' : ''] }"
                    @click="toggleRollbackPanel"
                  />

                  <template #content>
                    <div class="mt-2 space-y-2">
                      <!-- Deployment with online update disabled: rollback also disabled -->
                      <UAlert
                        v-if="updateDisabled"
                        color="info"
                        variant="subtle"
                        icon="i-lucide-info"
                        :description="t('version.rollbackDisabledHint')"
                      />
                      <!-- Source build: online rollback unavailable, use git instead -->
                      <UAlert
                        v-else-if="!isReleaseBuild"
                        color="info"
                        variant="subtle"
                        icon="i-lucide-info"
                        :description="t('version.rollbackSourceHint')"
                      />

                      <!-- Loading versions -->
                      <div v-else-if="rollbackVersionsLoading" class="flex items-center justify-center py-4">
                        <UIcon name="i-lucide-loader-circle" class="size-5 animate-spin text-muted" />
                      </div>

                      <!-- Load error + retry -->
                      <div v-else-if="rollbackVersionsError" class="space-y-2">
                        <UAlert color="error" variant="subtle" :description="rollbackVersionsError" />
                        <UButton color="neutral" variant="subtle" size="xs" block :label="t('version.retry')" @click="loadRollbackVersions" />
                      </div>

                      <!-- No versions available -->
                      <p v-else-if="rollbackVersions.length === 0" class="py-3 text-center text-xs text-muted">
                        {{ t('version.noRollbackVersions') }}
                      </p>

                      <!-- Version list -->
                      <template v-else>
                        <p class="px-0.5 text-[11px] text-muted">{{ t('version.rollbackSelectVersion') }}</p>

                        <UButton
                          v-for="item in rollbackVersions"
                          :key="item.version"
                          :color="selectedRollbackVersion === item.version ? 'warning' : 'neutral'"
                          :variant="selectedRollbackVersion === item.version ? 'soft' : 'ghost'"
                          block
                          :icon="selectedRollbackVersion === item.version ? 'i-lucide-circle-dot' : 'i-lucide-circle'"
                          :aria-pressed="selectedRollbackVersion === item.version ? 'true' : 'false'"
                          :disabled="rollingBack"
                          class="justify-start"
                          @click="selectRollbackVersion(item.version)"
                        >
                          <span class="flex-1 text-left text-sm font-semibold">v{{ item.version }}</span>
                          <span class="text-[11px] text-muted tabular-nums">{{ formatPublishedAt(item.published_at) }}</span>
                        </UButton>
                        <!-- Selected version: manual command (per deploy method) + confirm -->
                        <div v-if="selectedRollbackVersion" class="space-y-2">
                          <p class="px-0.5 text-[11px] text-muted">{{ t('version.manualRollbackCommand') }}</p>

                          <!-- Command block with deploy-method tabs -->
                          <div class="overflow-hidden rounded-md ring ring-default">
                            <div class="flex items-center justify-between gap-2 bg-elevated/50 px-1.5 py-1">
                              <UTabs
                                v-model="manualTab"
                                :items="manualTabs"
                                value-key="key"
                                :content="false"
                                size="xs"
                                color="neutral"
                                :ui="{ root: 'gap-0', list: 'w-auto p-0.5', trigger: 'px-2 py-0.5 text-[11px]' }"
                              />
                              <UButton
                                color="neutral"
                                variant="ghost"
                                size="xs"
                                :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
                                :ui="{ leadingIcon: copied ? 'text-success' : '' }"
                                :label="copied ? t('version.copied') : t('version.copyCommand')"
                                class="text-[11px]"
                                @click="copyToClipboard(activeManualCommand)"
                              />
                            </div>
                            <code class="block select-all whitespace-pre-wrap break-all bg-muted p-2.5 font-mono text-[10px] leading-relaxed text-default">{{ activeManualCommand }}</code>
                          </div>

                          <p class="flex items-start gap-1.5 px-0.5 text-[11px] leading-4 text-warning">
                            <UIcon name="i-lucide-triangle-alert" class="mt-px size-3.5 shrink-0" />
                            {{ t('version.rollbackWarning') }}
                          </p>

                          <UAlert v-if="rollbackError" color="error" variant="subtle" :description="rollbackError" />

                          <UButton
                            color="warning"
                            block
                            icon="i-lucide-history"
                            :loading="rollingBack"
                            :disabled="rollingBack"
                            :label="rollingBack ? t('version.rollingBack') : t('version.rollbackConfirm', { version: 'v' + selectedRollbackVersion })"
                            @click="handleRollback"
                          />
                        </div>
                      </template>
                    </div>
                  </template>
                </UCollapsible>
              </div>
            </template>
          </div>
        </div>
      </template>
    </UPopover>

    <!-- Read-only users see the public version without system API requests. -->
    <span v-else-if="version" class="text-xs text-muted">
      {{ formatVersion(version) }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import {
  performUpdate,
  restartService,
  getRollbackVersions,
  rollback as rollbackAPI,
  type RollbackVersionInfo
} from '@/api/admin/system'
import { useClipboard } from '@/composables/useClipboard'

const GITHUB_REPO = 'kiss-kedaya/sub2api'
// GHCR image published by CI (tags carry no "v" prefix).
const DOCKER_IMAGE = 'ghcr.io/kiss-kedaya/sub2api'

const { t } = useI18n()

const props = defineProps<{
  version?: string
}>()

const authStore = useAuthStore()
const appStore = useAppStore()

const canManageSystem = computed(() => authStore.isAdmin && !authStore.isAdminObserver)

const dropdownOpen = ref(false)

// Use store's cached version state
const loading = computed(() => appStore.versionLoading)
const currentVersion = computed(() => appStore.currentVersion || props.version || '')
const latestVersion = computed(() => appStore.latestVersion)

// Version values may come from the embedded VERSION file or GitHub and can
// arrive with or without the conventional `v` prefix. Normalize at the UI
// boundary so versions are rendered consistently (never `vv0.1.x`).
function formatVersion(version?: string): string {
  const normalized = (version || '').trim()
  if (!normalized) return ''
  return normalized.startsWith('v') ? normalized : `v${normalized}`
}

const displayCurrentVersion = computed(() => formatVersion(currentVersion.value))
const displayLatestVersion = computed(() => formatVersion(latestVersion.value))
const hasUpdate = computed(() => appStore.hasUpdate)
// 生产多实例部署（UPDATE_STRATEGY=disabled）禁止在线更新/回退：原地换二进制与
// 按版本命名的 systemd 单元不匹配，会把当前版本跑死。
const updateDisabled = computed(() => appStore.updateStrategy === 'disabled')
const releaseInfo = computed(() => appStore.releaseInfo)
const buildType = computed(() => appStore.buildType)

// Update process states (local to this component)
const updating = ref(false)
const restarting = ref(false)
const needRestart = ref(false)
const updateError = ref('')
const updateSuccess = ref(false)
const restartCountdown = ref(0)
// Distinguishes the success + restart panel between update and rollback flows
const successKind = ref<'update' | 'rollback'>('update')

// Rollback states
const rollbackPanelOpen = ref(false)
const rollbackVersions = ref<RollbackVersionInfo[]>([])
const rollbackVersionsLoading = ref(false)
const rollbackVersionsError = ref('')
const selectedRollbackVersion = ref('')
const rollingBack = ref(false)
const rollbackError = ref('')

const { copied, copyToClipboard } = useClipboard()

// Manual rollback methods differ by deployment: script installs use install.sh,
// docker deployments pin the image tag instead
const manualTab = ref<'script' | 'docker'>('script')

const manualTabs = computed(() => [
  { key: 'script' as const, label: t('version.deployScript') },
  { key: 'docker' as const, label: t('version.deployDocker') }
])

const scriptRollbackCommand = computed(() => {
  if (!selectedRollbackVersion.value) return ''
  const tag = `v${selectedRollbackVersion.value}`
  return `curl -sSL https://raw.githubusercontent.com/${GITHUB_REPO}/${tag}/deploy/install.sh | sudo bash -s -- rollback ${tag}`
})

const dockerRollbackCommand = computed(() => {
  if (!selectedRollbackVersion.value) return ''
  return [
    `# ${t('version.dockerEditCompose')}`,
    `image: ${DOCKER_IMAGE}:${selectedRollbackVersion.value}`,
    '',
    `# ${t('version.dockerRecreate')}`,
    'docker compose up -d'
  ].join('\n')
})

const activeManualCommand = computed(() =>
  manualTab.value === 'docker' ? dockerRollbackCommand.value : scriptRollbackCommand.value
)

// Only show update check for release builds (binary/docker deployment)
const isReleaseBuild = computed(() => buildType.value === 'release')

async function refreshVersion(force = true) {
  if (!canManageSystem.value) return

  // Reset update states when refreshing
  updateError.value = ''
  updateSuccess.value = false
  needRestart.value = false
  resetRollbackState()

  await appStore.fetchVersion(force)
}

async function handleUpdate() {
  if (!canManageSystem.value) return
  if (updating.value) return

  updating.value = true
  updateError.value = ''
  updateSuccess.value = false

  try {
    const result = await performUpdate()
    successKind.value = 'update'
    updateSuccess.value = true
    needRestart.value = result.need_restart
    // Clear version cache to reflect update completed
    appStore.clearVersionCache()
    // A binary/systemd update is complete only after the new process is
    // started. Trigger that final step from the same button click; the
    // orchestrated Docker strategy reports need_restart=false because its
    // host-side rollout already performed health checks and rollback handling.
    if (result.need_restart) {
      await handleRestart()
    }
  } catch (error: unknown) {
    const err = error as { response?: { data?: { message?: string } }; message?: string }
    updateError.value = err.response?.data?.message || err.message || t('version.updateFailed')
  } finally {
    updating.value = false
  }
}

function resetRollbackState() {
  rollbackPanelOpen.value = false
  rollbackVersions.value = []
  rollbackVersionsError.value = ''
  selectedRollbackVersion.value = ''
  rollbackError.value = ''
  manualTab.value = 'script'
}

async function toggleRollbackPanel() {
  if (!canManageSystem.value) return
  rollbackPanelOpen.value = !rollbackPanelOpen.value
  // Source builds only show a hint, no version list to fetch
  if (
    rollbackPanelOpen.value &&
    isReleaseBuild.value &&
    rollbackVersions.value.length === 0 &&
    !rollbackVersionsLoading.value
  ) {
    await loadRollbackVersions()
  }
}

async function loadRollbackVersions() {
  if (!canManageSystem.value) return
  rollbackVersionsLoading.value = true
  rollbackVersionsError.value = ''
  try {
    const data = await getRollbackVersions()
    rollbackVersions.value = data.versions || []
  } catch (error: unknown) {
    const err = error as { response?: { data?: { message?: string } }; message?: string }
    rollbackVersionsError.value =
      err.response?.data?.message || err.message || t('version.loadVersionsFailed')
  } finally {
    rollbackVersionsLoading.value = false
  }
}

function selectRollbackVersion(version: string) {
  if (rollingBack.value) return
  rollbackError.value = ''
  selectedRollbackVersion.value = selectedRollbackVersion.value === version ? '' : version
}

function formatPublishedAt(publishedAt: string): string {
  if (!publishedAt) return ''
  const date = new Date(publishedAt)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleDateString()
}

async function handleRollback() {
  if (!canManageSystem.value) return
  if (rollingBack.value || !selectedRollbackVersion.value) return

  rollingBack.value = true
  rollbackError.value = ''

  try {
    const result = await rollbackAPI(selectedRollbackVersion.value)
    successKind.value = 'rollback'
    updateSuccess.value = true
    needRestart.value = result.need_restart
    rollbackPanelOpen.value = false
    // Clear version cache so the next check reflects the rolled-back version
    appStore.clearVersionCache()
  } catch (error: unknown) {
    const err = error as { response?: { data?: { message?: string } }; message?: string }
    rollbackError.value = err.response?.data?.message || err.message || t('version.rollbackFailed')
  } finally {
    rollingBack.value = false
  }
}

async function handleRestart() {
  if (!canManageSystem.value) return
  if (restarting.value) return

  restarting.value = true
  restartCountdown.value = 8

  try {
    await restartService()
    // Service will restart, page will reload automatically or show disconnected
  } catch (error) {
    // Expected - connection will be lost during restart
    console.log('Service restarting...')
  }

  // Start countdown
  const countdownInterval = setInterval(() => {
    restartCountdown.value--
    if (restartCountdown.value <= 0) {
      clearInterval(countdownInterval)
      // Try to check if service is back before reload
      checkServiceAndReload()
    }
  }, 1000)
}

async function checkServiceAndReload() {
  const maxRetries = 5
  const retryDelay = 1000

  for (let i = 0; i < maxRetries; i++) {
    try {
      const response = await fetch('/health', {
        method: 'GET',
        cache: 'no-cache'
      })
      if (response.ok) {
        // Service is back, reload page
        window.location.reload()
        return
      }
    } catch {
      // Service not ready yet
    }

    if (i < maxRetries - 1) {
      await new Promise((resolve) => setTimeout(resolve, retryDelay))
    }
  }

  // After retries, reload anyway
  window.location.reload()
}

// The popover closes itself on outside click / Escape.
onMounted(() => {
  if (canManageSystem.value) {
    // Use cached version if available, otherwise fetch
    appStore.fetchVersion(false)
  }
})
</script>
