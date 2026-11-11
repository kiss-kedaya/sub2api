<template>
  <!-- Icon + short label: the kind is readable without relying on colour, and the
       label never wraps so a column of these scans as one list. -->
  <UBadge
    :color="UPSTREAM_KIND_COLOR[kind]"
    :variant="kind === 'unknown' ? 'outline' : 'subtle'"
    size="sm"
    :icon="UPSTREAM_KIND_ICON[kind]"
    class="shrink-0 whitespace-nowrap"
    :class="kind === 'unknown' ? 'text-dimmed' : ''"
    :title="t(`admin.accounts.upstreamKind.hint.${kind}`)"
    :data-upstream-kind="kind"
    data-testid="upstream-kind-badge"
  >
    {{ t(`admin.accounts.upstreamKind.label.${kind}`) }}
  </UBadge>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { UPSTREAM_KIND_COLOR, UPSTREAM_KIND_ICON, type UpstreamKindKey } from './upstreamBalance'

defineProps<{ kind: UpstreamKindKey }>()

const { t } = useI18n()
</script>
