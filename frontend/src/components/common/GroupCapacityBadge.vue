<template>
  <div class="flex min-w-0 flex-col items-start gap-1">
    <!-- 并发槽位 -->
    <CapacityMeterBadge
      :label="t('admin.groups.capacityLabels.concurrency')"
      :hint="meterHint('concurrency')"
      :used="runtimeKnown ? concurrencyUsed : null"
      :max="concurrencyMax"
      icon="i-lucide-layout-grid"
    />

    <!-- 活跃用户：最近窗口内真正发过请求的独立用户数。
         会话数只对配置了 max_sessions 的账号存在，因此这里单独一行，
         不再把“没有会话数据”误读成“0 个用户”。 -->
    <CapacityMeterBadge
      :label="t('admin.groups.capacityLabels.activeUsers')"
      :hint="activeUsersHint"
      :used="activeUsersKnown ? activeUsers : null"
      :show-max="false"
      icon="i-lucide-users"
    />

    <!-- 使用人数（活跃会话） -->
    <CapacityMeterBadge
      v-if="showSessions"
      :label="t('admin.groups.capacityLabels.sessions')"
      :hint="meterHint('sessions')"
      :used="runtimeKnown ? sessionsUsed : null"
      :max="sessionsMax"
      icon="i-lucide-user-check"
    />

    <!-- RPM -->
    <CapacityMeterBadge
      v-if="showRpm"
      :label="t('admin.groups.capacityLabels.rpm')"
      :hint="meterHint('rpm')"
      :used="runtimeKnown ? rpmUsed : null"
      :max="rpmMax"
      icon="i-lucide-clock"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import CapacityMeterBadge from '@/components/common/CapacityMeterBadge.vue'

const { t } = useI18n()
interface Props {
  concurrencyUsed: number
  concurrencyMax: number
  sessionsUsed: number
  sessionsMax: number
  sessionsTracked?: boolean
  rpmUsed: number
  rpmMax: number
  activeUsers?: number
  activeUsersKnown?: boolean
  activeUsersWindowSeconds?: number
  runtimeKnown?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  concurrencyUsed: 0,
  concurrencyMax: 0,
  sessionsUsed: 0,
  sessionsMax: 0,
  sessionsTracked: undefined,
  rpmUsed: 0,
  rpmMax: 0,
  activeUsers: 0,
  activeUsersKnown: false,
  activeUsersWindowSeconds: 300,
  runtimeKnown: true
})

// A meter row only exists when the group actually configures that limit; otherwise the
// column would carry permanent "0 / 0" lines. sessions_tracked from the backend is
// authoritative; older payloads fall back to the previous max/used heuristic.
const showSessions = computed(() => props.sessionsTracked ?? (props.sessionsMax > 0 || props.sessionsUsed > 0))
const showRpm = computed(() => props.rpmMax > 0 || props.rpmUsed > 0)

const activeUsersHint = computed(() => t('admin.groups.capacityHints.activeUsers', {
  minutes: Math.max(1, Math.round((props.activeUsersWindowSeconds || 300) / 60)),
}))

const meterHint = (meter: 'concurrency' | 'sessions' | 'rpm') => {
  const hint = t(`admin.groups.capacityHints.${meter}`)
  return props.runtimeKnown ? hint : `${hint} ${t('admin.groups.capacityHints.unknown')}`
}
</script>
