<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import type { CustomEndpoint } from '@/types'

const props = defineProps<{
  apiBaseUrl: string
  customEndpoints: CustomEndpoint[]
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const copiedEndpoint = ref<string | null>(null)

let copiedResetTimer: number | undefined

const allEndpoints = computed(() => {
  const items: Array<{ name: string; endpoint: string; description: string; isDefault: boolean }> = []
  if (props.apiBaseUrl) {
    items.push({
      name: t('keys.endpoints.title'),
      endpoint: props.apiBaseUrl,
      description: '',
      isDefault: true,
    })
  }
  for (const ep of props.customEndpoints) {
    items.push({ ...ep, isDefault: false })
  }
  return items
})

async function copy(url: string) {
  const success = await copyToClipboard(url, t('keys.endpoints.copied'))
  if (!success) return

  copiedEndpoint.value = url
  if (copiedResetTimer !== undefined) {
    window.clearTimeout(copiedResetTimer)
  }
  copiedResetTimer = window.setTimeout(() => {
    if (copiedEndpoint.value === url) {
      copiedEndpoint.value = null
    }
  }, 1800)
}

function tooltipHint(endpoint: string): string {
  return copiedEndpoint.value === endpoint
    ? t('keys.endpoints.copiedHint')
    : t('keys.endpoints.clickToCopy')
}

function speedTestUrl(endpoint: string): string {
  return `https://www.tcptest.cn/http/${encodeURIComponent(endpoint)}`
}

onBeforeUnmount(() => {
  if (copiedResetTimer !== undefined) {
    window.clearTimeout(copiedResetTimer)
  }
})
</script>

<template>
  <div v-if="allEndpoints.length > 0" class="flex flex-wrap gap-1.5">
    <div
      v-for="(item, index) in allEndpoints"
      :key="index"
      class="flex items-center gap-1.5 rounded-md py-0.5 pl-2.5 pr-0.5 text-xs ring ring-inset ring-default"
    >
      <span class="font-medium text-toned">{{ item.name }}</span>
      <UBadge v-if="item.isDefault" color="neutral" variant="soft" size="sm">{{ t('keys.endpoints.default') }}</UBadge>

      <div class="group/endpoint relative flex items-center gap-0.5">
        <!-- Hover / focus hint above the URL, styled like UTooltip. It stays in the DOM so the
             endpoint description is readable without hovering a separate icon. -->
        <div
          class="pointer-events-none absolute bottom-full left-1/2 z-20 mb-1.5 w-max max-w-[24rem] -translate-x-1/2 rounded-sm bg-default px-2.5 py-1.5 text-left opacity-0 shadow-sm ring ring-default transition-opacity duration-150 group-hover/endpoint:opacity-100 group-focus-within/endpoint:opacity-100"
        >
          <p v-if="item.description" class="max-w-[24rem] wrap-break-word text-xs leading-5 text-highlighted">
            {{ item.description }}
          </p>
          <p class="text-xs text-muted" :class="item.description ? 'mt-1' : ''">
            {{ tooltipHint(item.endpoint) }}
          </p>
        </div>

        <code
          class="cursor-pointer rounded-sm px-1 font-mono text-muted hover:text-highlighted focus:text-highlighted focus:outline-hidden focus-visible:ring-2 focus-visible:ring-primary"
          role="button"
          tabindex="0"
          @click="copy(item.endpoint)"
          @keydown.enter.prevent="copy(item.endpoint)"
          @keydown.space.prevent="copy(item.endpoint)"
        >{{ item.endpoint }}</code>

        <UButton
          color="neutral"
          variant="ghost"
          size="xs"
          :icon="copiedEndpoint === item.endpoint ? 'i-lucide-check' : 'i-lucide-copy'"
          :class="copiedEndpoint === item.endpoint ? 'text-success' : ''"
          :aria-label="tooltipHint(item.endpoint)"
          @click="copy(item.endpoint)"
        />

        <UButton
          color="neutral"
          variant="ghost"
          size="xs"
          icon="i-lucide-zap"
          :to="speedTestUrl(item.endpoint)"
          target="_blank"
          rel="noopener noreferrer"
          :title="t('keys.endpoints.speedTest')"
          :aria-label="t('keys.endpoints.speedTest')"
        />
      </div>
    </div>
  </div>
</template>
