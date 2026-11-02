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

    <!-- 使用人数（活跃会话） -->
    <CapacityMeterBadge
      v-if="showSessions"
      :label="t('admin.groups.capacityLabels.sessions')"
      :hint="meterHint('sessions')"
      :used="runtimeKnown ? sessionsUsed : null"
      :max="sessionsMax"
      icon="i-lucide-users"
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
  rpmUsed: number
  rpmMax: number
  runtimeKnown?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  concurrencyUsed: 0,
  concurrencyMax: 0,
  sessionsUsed: 0,
  sessionsMax: 0,
  rpmUsed: 0,
  rpmMax: 0,
  runtimeKnown: true
})

// A meter row only exists when the group actually configures that limit; otherwise the
// column would carry three permanent "0 / 0" lines.
const showSessions = computed(() => props.sessionsMax > 0 || props.sessionsUsed > 0)
const showRpm = computed(() => props.rpmMax > 0 || props.rpmUsed > 0)

const meterHint = (meter: 'concurrency' | 'sessions' | 'rpm') => {
  const hint = t(`admin.groups.capacityHints.${meter}`)
  return props.runtimeKnown ? hint : `${hint} ${t('admin.groups.capacityHints.unknown')}`
}
</script>
