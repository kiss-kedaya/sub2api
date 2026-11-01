<template>
  <div v-if="entry.status === 'idle'" class="mt-0.5 text-xs">
    <UButton
      color="primary"
      variant="link"
      size="xs"
      class="p-0 text-xs underline decoration-dashed underline-offset-2"
      :label="t('usage.ipGeo.fetch')"
      @click="handleFetch"
    />
  </div>

  <div v-else-if="entry.status === 'loading'" class="mt-0.5 flex items-center gap-1 text-xs text-dimmed">
    <UIcon name="i-lucide-refresh-cw" class="size-3 animate-spin" />
    {{ t('usage.ipGeo.fetching') }}
  </div>

  <div v-else-if="entry.status === 'success'" class="mt-0.5 flex min-w-0 items-center gap-1 text-xs">
    <UButton
      color="neutral"
      variant="link"
      size="xs"
      class="min-w-0 p-0 text-xs text-muted underline decoration-dotted underline-offset-2 hover:text-primary"
      :title="tooltipText"
      @click="handleOpenDetail"
    >
      <span class="truncate">{{ entry.label }}</span>
    </UButton>
    <UButton
      color="neutral"
      variant="link"
      size="xs"
      icon="i-lucide-refresh-cw"
      class="p-0 text-dimmed hover:text-primary"
      :ui="{ leadingIcon: 'size-3' }"
      :title="t('usage.ipGeo.refreshTitle')"
      :aria-label="t('usage.ipGeo.refreshTitle')"
      @click="handleRefresh"
    />
  </div>

  <div v-else-if="entry.status === 'error'" class="mt-0.5 text-xs">
    <UButton
      color="error"
      variant="link"
      size="xs"
      class="p-0 text-xs underline decoration-dashed underline-offset-2"
      :label="t('usage.ipGeo.failed')"
      @click="handleFetch"
    />
  </div>

  <div v-else class="mt-0.5 text-xs text-dimmed">
    {{ t('usage.ipGeo.private') }}
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchOne, getEntry } from '@/utils/ipGeoLookup'

const props = defineProps<{ ip: string }>()
const { t } = useI18n()

const entry = computed(() => getEntry(props.ip))

const tooltipText = computed(() => {
  const detail = entry.value.detail
  if (!detail) return ''
  const lines = [
    detail.organization ? `${t('usage.ipGeo.detailOrg')}: ${detail.organization}` : '',
    detail.timezone ? `${t('usage.ipGeo.detailTimezone')}: ${detail.timezone}` : '',
    detail.accuracy != null ? `${t('usage.ipGeo.detailAccuracy')}: ${detail.accuracy}km` : '',
    detail.latitude && detail.longitude
      ? `${t('usage.ipGeo.detailCoordinates')}: ${detail.latitude}, ${detail.longitude}`
      : '',
  ].filter(Boolean)
  return lines.join('\n')
})

const handleFetch = () => {
  void fetchOne(props.ip)
}

const handleRefresh = () => {
  void fetchOne(props.ip, true)
}

const handleOpenDetail = () => {
  window.open(
    `https://www.iplocation.net/ip-lookup?query=${encodeURIComponent(props.ip)}`,
    '_blank',
    'noopener,noreferrer'
  )
}
</script>
