<template>
  <!-- Icon + short label: the kind is readable without relying on colour, and the
       label never wraps so a column of these scans as one list. The glyph is drawn from
       local path data; resolving an icon by name at runtime made a renamed icon render
       an empty <svg> instead of failing loudly. -->
  <UBadge
    :color="UPSTREAM_KIND_COLOR[kind]"
    :variant="kind === 'unknown' ? 'outline' : 'subtle'"
    size="sm"
    class="shrink-0 whitespace-nowrap"
    :class="kind === 'unknown' ? 'text-dimmed' : ''"
    :title="t(`admin.accounts.upstreamKind.hint.${kind}`)"
    :data-upstream-kind="kind"
    data-testid="upstream-kind-badge"
  >
    <svg
      class="size-3 shrink-0"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      data-testid="upstream-kind-icon"
    >
      <template v-for="(shape, index) in UPSTREAM_KIND_SHAPES[kind]" :key="index">
        <circle v-if="'r' in shape" :cx="shape.cx" :cy="shape.cy" :r="shape.r" />
        <path v-else :d="shape.d" />
      </template>
    </svg>
    {{ t(`admin.accounts.upstreamKind.label.${kind}`) }}
  </UBadge>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { UPSTREAM_KIND_COLOR, type UpstreamKindKey } from './upstreamBalance'
import { UPSTREAM_KIND_SHAPES } from './upstreamKindIcon'

defineProps<{ kind: UpstreamKindKey }>()

const { t } = useI18n()
</script>
