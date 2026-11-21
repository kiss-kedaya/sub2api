<template>
  <div class="space-y-2" data-test="ip-whitelist-assist">
    <div class="flex flex-wrap items-center gap-2">
      <UButton
        size="xs"
        color="neutral"
        variant="subtle"
        icon="i-lucide-map-pin"
        :loading="loading"
        data-test="ip-assist-fill-current"
        @click="fillCurrentIP"
      >
        {{ t('keys.ipWhitelistAssist.fillCurrent') }}
      </UButton>
      <UButton
        size="xs"
        color="neutral"
        variant="ghost"
        icon="i-lucide-history"
        :loading="loading"
        data-test="ip-assist-recent-toggle"
        @click="toggleRecentIPs"
      >
        {{ t('keys.ipWhitelistAssist.commonIPs') }}
      </UButton>
      <span v-if="loading" class="text-xs text-muted">{{ t('keys.ipWhitelistAssist.loading') }}</span>
    </div>

    <p v-if="loadError" class="text-xs text-error" role="alert" data-test="ip-assist-load-error">
      {{ loadError }}
    </p>

    <!-- 待确认的 IP：必须等 3 秒倒计时结束才能写入，避免代理/VPN 手快锁死自己 -->
    <div
      v-if="candidate"
      class="rounded-lg border border-default bg-elevated/40 p-3"
      data-test="ip-assist-candidate"
    >
      <p class="break-all font-mono text-sm text-highlighted" data-test="ip-assist-candidate-ip">
        {{ describeIP(candidate.ip, candidate.geo) }}
      </p>
      <p
        v-if="candidate.geo.hosting"
        class="mt-1 text-xs text-error"
        data-test="ip-assist-hosting-warning"
      >
        {{ t('keys.ipWhitelistAssist.hostingWarning') }}
      </p>
      <p class="mt-1 text-xs text-muted">{{ t('keys.ipWhitelistAssist.confirmHint') }}</p>
      <div class="mt-2 flex items-center gap-2">
        <UButton
          size="xs"
          color="primary"
          :disabled="countdown > 0"
          data-test="ip-assist-confirm"
          @click="confirmCandidate"
        >
          {{ countdown > 0 ? t('keys.ipWhitelistAssist.confirmCounting', { seconds: countdown }) : t('keys.ipWhitelistAssist.confirm') }}
        </UButton>
        <UButton
          size="xs"
          color="neutral"
          variant="ghost"
          data-test="ip-assist-cancel"
          @click="cancelCandidate"
        >
          {{ t('common.cancel') }}
        </UButton>
        <span
          v-if="countdown > 0"
          class="text-xs text-muted"
          aria-live="polite"
          data-test="ip-assist-countdown"
        >
          {{ t('keys.ipWhitelistAssist.countdownHint') }}
        </span>
      </div>
    </div>

    <!-- 常用 IP：后端按最近 30 天出现次数聚合，最多 5 条 -->
    <div
      v-if="recentOpen"
      class="rounded-lg border border-default p-2"
      data-test="ip-assist-recent-list"
    >
      <p class="px-1 pb-1 text-xs text-muted">{{ t('keys.ipWhitelistAssist.recentHint') }}</p>
      <p v-if="recentIPs.length === 0" class="px-1 py-1 text-xs text-muted" data-test="ip-assist-recent-empty">
        {{ t('keys.ipWhitelistAssist.recentEmpty') }}
      </p>
      <button
        v-for="item in recentIPs"
        :key="item.ip"
        type="button"
        class="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-elevated/60"
        :data-test="`ip-assist-recent-${item.ip}`"
        @click="chooseIP(item.ip, item.geo)"
      >
        <span class="break-all font-mono text-default">{{ describeIP(item.ip, item.geo) }}</span>
        <span class="shrink-0 text-muted">{{ t('keys.ipWhitelistAssist.usedCount', { count: item.count }) }}</span>
      </button>
    </div>

    <p
      v-if="feedback"
      class="text-xs"
      :class="feedback.duplicate ? 'text-warning' : 'text-success'"
      role="status"
      data-test="ip-assist-feedback"
    >
      {{ feedback.duplicate ? t('keys.ipWhitelistAssist.duplicate') : t('keys.ipWhitelistAssist.added', { ip: feedback.ip }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getIPWhitelistAssist,
  type IPWhitelistAssistGeo,
  type IPWhitelistAssistResponse
} from '@/api/keys'
import { appendIPEntry } from '@/utils/ipWhitelist'

const props = withDefaults(
  defineProps<{
    /** Current textarea content; the component only ever appends to it. */
    modelValue: string
    /** Seconds the user must wait before the confirm button unlocks. */
    confirmSeconds?: number
  }>(),
  { confirmSeconds: 3 }
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const { t } = useI18n()

const assist = ref<IPWhitelistAssistResponse | null>(null)
const loading = ref(false)
const loadError = ref('')
const recentOpen = ref(false)
const candidate = ref<{ ip: string; geo: IPWhitelistAssistGeo } | null>(null)
const countdown = ref(0)
const feedback = ref<{ ip: string; duplicate: boolean } | null>(null)

let timer: ReturnType<typeof setInterval> | null = null
let inflight: Promise<boolean> | null = null

const recentIPs = computed(() => assist.value?.recent_ips ?? [])

const EMPTY_GEO: IPWhitelistAssistGeo = { country: '', region: '', city: '', isp: '', hosting: false }

function describeIP(ip: string, geo: IPWhitelistAssistGeo | undefined): string {
  const location = [geo?.country, geo?.region, geo?.city].filter(Boolean).join(' ')
  const region = location || geo?.isp || t('keys.ipWhitelistAssist.unknownRegion')
  const isp = geo?.isp && location ? ` · ${geo.isp}` : ''
  return `${ip} · ${region}${isp}`
}

/**
 * Load the assist payload.
 *
 * `force` bypasses the component cache: "fill in current IP" must reflect the
 * network the user is on right now, so a modal that stayed open while the user
 * switched to a VPN must not keep offering the previous address (whitelisting
 * it would lock them out). Concurrent clicks share one in-flight request.
 */
async function loadAssist(force = false): Promise<boolean> {
  if (!force && assist.value) return true
  if (inflight) return inflight
  loading.value = true
  loadError.value = ''
  inflight = (async () => {
    try {
      assist.value = await getIPWhitelistAssist()
      return true
    } catch {
      loadError.value = t('keys.ipWhitelistAssist.loadFailed')
      return false
    } finally {
      loading.value = false
      inflight = null
    }
  })()
  return inflight
}

async function fillCurrentIP(): Promise<void> {
  if (!(await loadAssist(true))) return
  const currentIP = assist.value?.current_ip?.trim() ?? ''
  if (!currentIP) {
    loadError.value = t('keys.ipWhitelistAssist.noCurrentIP')
    return
  }
  chooseIP(currentIP, assist.value?.current_geo ?? EMPTY_GEO)
}

async function toggleRecentIPs(): Promise<void> {
  if (recentOpen.value) {
    recentOpen.value = false
    return
  }
  if (!(await loadAssist())) return
  recentOpen.value = true
}

function chooseIP(ip: string, geo: IPWhitelistAssistGeo): void {
  stopTimer()
  feedback.value = null
  candidate.value = { ip, geo: { ...EMPTY_GEO, ...geo } }
  countdown.value = Math.max(0, Math.floor(props.confirmSeconds))
  if (countdown.value === 0) return
  timer = setInterval(() => {
    countdown.value -= 1
    if (countdown.value <= 0) {
      countdown.value = 0
      stopTimer()
    }
  }, 1000)
}

function stopTimer(): void {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

function cancelCandidate(): void {
  stopTimer()
  countdown.value = 0
  candidate.value = null
}

function confirmCandidate(): void {
  if (!candidate.value || countdown.value > 0) return
  const { value, duplicate } = appendIPEntry(props.modelValue, candidate.value.ip)
  if (!duplicate) {
    emit('update:modelValue', value)
  }
  feedback.value = { ip: candidate.value.ip, duplicate }
  candidate.value = null
  countdown.value = 0
  stopTimer()
}

onBeforeUnmount(stopTimer)
</script>
