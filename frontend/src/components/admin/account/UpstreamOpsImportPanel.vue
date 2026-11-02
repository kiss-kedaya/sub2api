<template>
  <UCard>
    <template #header>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-highlighted">{{ copy.title }}</h2>
          <p class="mt-1 text-xs leading-5 text-muted">{{ copy.subtitle }}</p>
        </div>
        <UBadge color="neutral" variant="subtle" :label="copy.version" />
      </div>
    </template>

    <div class="space-y-4">
      <UAlert
        color="info"
        variant="subtle"
        icon="i-lucide-shield-check"
        :title="copy.authTitle"
        :description="copy.authBody"
      />

      <div class="space-y-2">
        <label class="block text-sm font-medium text-default" for="upstream-ops-snapshot">{{ copy.inputLabel }}</label>
        <UTextarea
          id="upstream-ops-snapshot"
          v-model="rawText"
          :rows="8"
          :placeholder="copy.inputPlaceholder"
          class="w-full font-mono text-xs"
          spellcheck="false"
        />
        <p class="text-xs leading-5 text-muted">{{ copy.inputHint }}</p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <UButton icon="i-lucide-scan-search" :label="copy.previewButton" :disabled="rawText.trim() === ''" @click="handlePreview" />
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-users"
          :loading="loadingExisting"
          :label="copy.loadExistingButton"
          @click="loadExistingAccounts"
        />
        <UButton
          color="neutral"
          variant="ghost"
          icon="i-lucide-eraser"
          :label="copy.clearButton"
          :disabled="rawText === '' && !snapshot"
          @click="handleClear"
        />
      </div>

      <UAlert v-if="issues.length > 0" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="copy.issuesTitle">
        <template #description>
          <ul class="list-inside list-disc space-y-1 text-xs">
            <li v-for="issue in issues" :key="issue">{{ issue }}</li>
          </ul>
        </template>
      </UAlert>

      <template v-if="preview">
        <div class="flex flex-wrap items-center gap-2">
          <UBadge color="neutral" variant="subtle" :label="copy.summaryTotal(preview.summary.total)" />
          <UBadge color="warning" variant="subtle" :label="copy.summaryNeedsCredential(preview.summary.needsCredential)" />
          <UBadge color="info" variant="subtle" :label="copy.summaryNeedsPlatform(preview.summary.needsPlatform)" />
          <UBadge color="neutral" variant="subtle" :label="copy.summaryDuplicate(preview.summary.duplicate)" />
          <UBadge color="neutral" variant="subtle" :label="copy.summaryUnsupported(preview.summary.unsupported)" />
          <UBadge v-if="preview.summary.invalid > 0" color="error" variant="subtle" :label="copy.summaryInvalid(preview.summary.invalid)" />
        </div>

        <UTable :data="displayRows" :columns="columns" class="w-full">
          <template #name-cell="{ row }">
            <div class="min-w-0">
              <p class="truncate text-sm font-medium text-highlighted" :title="row.original.name">
                {{ row.original.name || copy.unnamed }}
              </p>
              <p class="truncate text-xs text-muted" :title="row.original.baseUrl">{{ row.original.baseUrl || copy.noAddress }}</p>
            </div>
          </template>
          <template #source-cell="{ row }">
            <div class="text-xs text-muted">
              <p>{{ row.original.kind === 'providers' ? copy.sourceProvider : copy.sourceChannel }}</p>
              <p class="font-mono">{{ row.original.sourceType || '-' }}</p>
            </div>
          </template>
          <template #status-cell="{ row }">
            <div class="space-y-1">
              <UBadge :color="statusColor(row.original.status)" variant="subtle" :label="statusLabel(row.original.status)" />
              <p v-if="reasonText(row.original)" class="text-xs text-muted">{{ reasonText(row.original) }}</p>
            </div>
          </template>
          <template #action-cell="{ row }">
            <div
              v-if="row.original.kind === 'providers' && row.original.status !== 'duplicate' && row.original.status !== 'invalid'"
              class="space-y-2"
            >
              <USelect
                v-if="!row.original.platform"
                :model-value="platformOverrides[row.original.key]"
                :items="platformItems"
                :placeholder="copy.choosePlatform"
                size="xs"
                class="w-40"
                @update:model-value="setPlatform(row.original.key, $event)"
              />
              <UInput
                v-model="credentials[row.original.key]"
                type="password"
                size="xs"
                autocomplete="off"
                :placeholder="copy.apiKeyPlaceholder"
                :aria-label="copy.apiKeyPlaceholder"
                class="w-48"
              />
              <p v-if="row.original.apiKeyHint" class="font-mono text-xs text-muted">
                {{ copy.hintPrefix }} {{ row.original.apiKeyHint }}
              </p>
            </div>
            <span v-else class="text-xs text-muted">{{ copy.notImportable }}</span>
          </template>
        </UTable>

        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-3">
          <p class="text-xs leading-5 text-muted">{{ copy.footerHint }}</p>
          <UButton
            icon="i-lucide-clipboard-copy"
            :disabled="importableCount === 0"
            :label="copy.copyButton(importableCount)"
            @click="copyPayload"
          />
        </div>
      </template>
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AccountPlatform } from '@/types'
import {
  buildUpstreamOpsDataPayload,
  buildUpstreamOpsPreview,
  parseUpstreamOpsSnapshot,
  type UpstreamOpsExistingAccountKey,
  type UpstreamOpsPreviewRow,
  type UpstreamOpsReasonCode,
  type UpstreamOpsRowStatus,
  type UpstreamOpsSnapshot
} from '@/utils/upstreamOpsCompat'

interface Props {
  /** Optional override; the panel otherwise loads accounts on demand for dedupe. */
  existingAccounts?: UpstreamOpsExistingAccountKey[]
}

const props = defineProps<Props>()

const { locale } = useI18n()
const appStore = useAppStore()

interface PanelCopy {
  title: string
  subtitle: string
  version: string
  authTitle: string
  authBody: string
  inputLabel: string
  inputPlaceholder: string
  inputHint: string
  previewButton: string
  loadExistingButton: string
  clearButton: string
  issuesTitle: string
  sourceProvider: string
  sourceChannel: string
  unnamed: string
  noAddress: string
  choosePlatform: string
  apiKeyPlaceholder: string
  hintPrefix: string
  notImportable: string
  footerHint: string
  copyButton: (count: number) => string
  summaryTotal: (count: number) => string
  summaryNeedsCredential: (count: number) => string
  summaryNeedsPlatform: (count: number) => string
  summaryDuplicate: (count: number) => string
  summaryUnsupported: (count: number) => string
  summaryInvalid: (count: number) => string
  colName: string
  colSource: string
  colStatus: string
  colCredential: string
  status: Record<UpstreamOpsRowStatus, string>
  reasons: Record<UpstreamOpsReasonCode, string>
  parseFailed: string
  emptySnapshot: string
  copied: string
  copyFailed: string
  existingLoaded: (count: number) => string
  existingFailed: string
}

const COPY_ZH: PanelCopy = {
  title: 'UpstreamOps 导入',
  subtitle: '粘贴 upstream-ops 的渠道或网关通道 JSON，先预览、校验、去重，再交给账号页现有「导入数据」流程。',
  version: 'adapter v1',
  authTitle: '认证契约：本站浏览器会话是 session_managed Cookie',
  authBody:
    '本站登录响应不会把 access_token / refresh_token 交给浏览器，会话在 HttpOnly Cookie 里。upstream-ops 的 sub2api connector 只认 Bearer access_token，没有 Cookie jar、CSRF 或 session_managed 支持；本面板不导出任何凭据，也不要求关闭 2FA。',
  inputLabel: 'upstream-ops JSON 快照',
  inputPlaceholder: '{"data":[ ... ]}  —— 支持 /api/channels 或 /api/gateway/providers 的响应体',
  inputHint:
    '只读取 name / base_url|site_url / upstream_protocol / api_key_hint 等非密字段；password、token_credential、api_key、cookie 一律丢弃。',
  previewButton: '解析并预览',
  loadExistingButton: '载入本站账号用于去重',
  clearButton: '清空',
  issuesTitle: '快照里有无法解析的内容',
  sourceProvider: '网关通道',
  sourceChannel: '上游渠道',
  unnamed: '（未命名）',
  noAddress: '（无地址）',
  choosePlatform: '选择平台',
  apiKeyPlaceholder: '手动填写 API Key',
  hintPrefix: '上游提示：',
  notImportable: '不导入',
  footerHint:
    '导出的 JSON 走现有「导入数据」流程（POST /api/v1/admin/accounts/data）。余额、倍率、支付与删除动作都不会被触发。',
  copyButton: (count) => `复制导入 JSON（${count} 条）`,
  summaryTotal: (count) => `共 ${count} 条`,
  summaryNeedsCredential: (count) => `待填 Key ${count}`,
  summaryNeedsPlatform: (count) => `待选平台 ${count}`,
  summaryDuplicate: (count) => `重复 ${count}`,
  summaryUnsupported: (count) => `不适用 ${count}`,
  summaryInvalid: (count) => `无效 ${count}`,
  colName: '名称 / 地址',
  colSource: '来源',
  colStatus: '状态',
  colCredential: '凭据',
  status: {
    'needs-credential': '待填 Key',
    'needs-platform': '待选平台',
    duplicate: '重复',
    unsupported: '不适用',
    invalid: '无效'
  },
  reasons: {
    channel_snapshot_has_no_credential: '渠道快照不含凭据与模型平台',
    missing_name: '缺少名称',
    missing_base_url: '缺少地址',
    unknown_upstream_protocol: '协议为 auto/未知',
    provider_disabled: '上游已停用',
    duplicate_existing: '本站已存在',
    duplicate_batch: '批内重复'
  },
  parseFailed: 'JSON 解析失败',
  emptySnapshot: '没有可识别的行',
  copied: '导入 JSON 已复制',
  copyFailed: '复制失败',
  existingLoaded: (count) => `已载入 ${count} 个本站账号用于去重`,
  existingFailed: '载入本站账号失败，去重只按本批次判断'
}

const COPY_EN: PanelCopy = {
  ...COPY_ZH,
  title: 'UpstreamOps import',
  subtitle:
    'Paste an upstream-ops channel or gateway-provider snapshot, preview / validate / dedupe it, then hand the result to the existing account import flow.',
  authTitle: 'Auth contract: this app is session_managed cookie',
  authBody:
    'Browser login responses never carry access_token / refresh_token; the session lives in an HttpOnly cookie. The upstream-ops sub2api connector is bearer-token only (no cookie jar, CSRF or session_managed support). This panel exports no credentials and never asks to disable 2FA.',
  inputLabel: 'upstream-ops JSON snapshot',
  inputPlaceholder: '{"data":[ ... ]}  — the /api/channels or /api/gateway/providers response body',
  inputHint:
    'Only name / base_url|site_url / upstream_protocol / api_key_hint are read. password, token_credential, api_key and cookie are dropped.',
  previewButton: 'Parse and preview',
  loadExistingButton: 'Load existing accounts for dedupe',
  clearButton: 'Clear',
  issuesTitle: 'The snapshot contains unparseable rows',
  sourceProvider: 'Gateway provider',
  sourceChannel: 'Upstream channel',
  unnamed: '(unnamed)',
  noAddress: '(no address)',
  choosePlatform: 'Choose platform',
  apiKeyPlaceholder: 'Enter API key',
  hintPrefix: 'Upstream hint:',
  notImportable: 'Not imported',
  footerHint:
    'The JSON feeds the existing import flow (POST /api/v1/admin/accounts/data). No balance, rate, payment or delete action is triggered.',
  copyButton: (count) => `Copy import JSON (${count})`,
  summaryTotal: (count) => `${count} rows`,
  summaryNeedsCredential: (count) => `${count} need a key`,
  summaryNeedsPlatform: (count) => `${count} need a platform`,
  summaryDuplicate: (count) => `${count} duplicate`,
  summaryUnsupported: (count) => `${count} unsupported`,
  summaryInvalid: (count) => `${count} invalid`,
  colName: 'Name / address',
  colSource: 'Source',
  colStatus: 'Status',
  colCredential: 'Credential',
  status: {
    'needs-credential': 'Needs key',
    'needs-platform': 'Needs platform',
    duplicate: 'Duplicate',
    unsupported: 'Unsupported',
    invalid: 'Invalid'
  },
  reasons: {
    channel_snapshot_has_no_credential: 'Channel snapshot has no credential or model platform',
    missing_name: 'Missing name',
    missing_base_url: 'Missing address',
    unknown_upstream_protocol: 'Protocol is auto/unknown',
    provider_disabled: 'Provider disabled upstream',
    duplicate_existing: 'Already exists here',
    duplicate_batch: 'Duplicate in batch'
  },
  parseFailed: 'Invalid JSON',
  emptySnapshot: 'No recognizable rows',
  copied: 'Import JSON copied',
  copyFailed: 'Copy failed',
  existingLoaded: (count) => `Loaded ${count} existing accounts for dedupe`,
  existingFailed: 'Could not load existing accounts; dedupe is batch-only'
}

const copy = computed<PanelCopy>(() => (locale.value.startsWith('zh') ? COPY_ZH : COPY_EN))

const rawText = ref('')
const snapshot = ref<UpstreamOpsSnapshot | null>(null)
const issues = ref<string[]>([])
const credentials = ref<Record<string, string>>({})
const platformOverrides = ref<Record<string, AccountPlatform>>({})
const loadedExisting = ref<UpstreamOpsExistingAccountKey[]>([])
const loadingExisting = ref(false)

const existingAccounts = computed<UpstreamOpsExistingAccountKey[]>(
  () => props.existingAccounts ?? loadedExisting.value
)

const preview = computed(() => {
  if (!snapshot.value) return null
  return buildUpstreamOpsPreview(snapshot.value, {
    existingAccounts: existingAccounts.value,
    platformOverrides: platformOverrides.value
  })
})

const payloadResult = computed(() => {
  if (!preview.value) return null
  const operatorCredentials: Record<string, { apiKey: string }> = {}
  for (const [key, apiKey] of Object.entries(credentials.value)) {
    if (apiKey.trim()) operatorCredentials[key] = { apiKey }
  }
  return buildUpstreamOpsDataPayload(preview.value.rows, operatorCredentials)
})

const importableCount = computed(() => payloadResult.value?.payload.accounts.length ?? 0)

const platformItems = [
  'openai',
  'anthropic',
  'gemini',
  'grok',
  'deepseek',
  'kimi',
  'zhipu',
  'minimax',
  'cline',
  'command_code',
  'antigravity',
  'typesafe'
].map((value) => ({ label: value, value }))

const columns = computed<TableColumn<UpstreamOpsPreviewRow>[]>(() => [
  { id: 'name', accessorKey: 'name', header: () => copy.value.colName },
  { id: 'source', accessorKey: 'sourceType', header: () => copy.value.colSource },
  { id: 'status', accessorKey: 'status', header: () => copy.value.colStatus },
  { id: 'action', header: () => copy.value.colCredential }
])

const displayRows = computed(() => preview.value?.rows ?? [])

function statusLabel(status: UpstreamOpsRowStatus): string {
  return copy.value.status[status] ?? status
}

function statusColor(status: UpstreamOpsRowStatus): 'neutral' | 'warning' | 'info' | 'error' {
  switch (status) {
    case 'needs-credential':
      return 'warning'
    case 'needs-platform':
      return 'info'
    case 'invalid':
      return 'error'
    default:
      return 'neutral'
  }
}

function reasonText(row: UpstreamOpsPreviewRow): string {
  return row.reasons.map((reason) => copy.value.reasons[reason] ?? reason).join(' · ')
}

function handlePreview() {
  issues.value = []
  snapshot.value = null
  credentials.value = {}
  platformOverrides.value = {}
  try {
    const parsed = JSON.parse(rawText.value)
    const next = parseUpstreamOpsSnapshot(parsed)
    issues.value = next.issues
    if (next.entries.length === 0) {
      appStore.showError(copy.value.emptySnapshot)
      return
    }
    snapshot.value = next
  } catch {
    appStore.showError(copy.value.parseFailed)
  }
}

function handleClear() {
  rawText.value = ''
  snapshot.value = null
  issues.value = []
  credentials.value = {}
  platformOverrides.value = {}
}

function setPlatform(key: string, value: unknown) {
  if (typeof value !== 'string' || !value) return
  platformOverrides.value = { ...platformOverrides.value, [key]: value as AccountPlatform }
}

async function loadExistingAccounts() {
  loadingExisting.value = true
  try {
    const response = await adminAPI.accounts.list(1, 1000, { lite: '1' })
    loadedExisting.value = (response.items ?? []).map((account) => ({
      name: account.name,
      custom_base_url: account.custom_base_url ?? null
    }))
    appStore.showSuccess(copy.value.existingLoaded(loadedExisting.value.length))
  } catch {
    appStore.showError(copy.value.existingFailed)
  } finally {
    loadingExisting.value = false
  }
}

async function copyPayload() {
  const payload = payloadResult.value?.payload
  if (!payload || payload.accounts.length === 0) return
  try {
    await navigator.clipboard.writeText(JSON.stringify(payload, null, 2))
    appStore.showSuccess(copy.value.copied)
  } catch {
    appStore.showError(copy.value.copyFailed)
  }
}
</script>
