<template>
  <a
    class="inline-flex max-w-full shrink-0 items-center gap-1 truncate text-xs font-normal text-primary-600 transition-colors hover:underline focus:outline-hidden focus:ring-2 focus:ring-primary-400 focus:ring-offset-2 dark:text-primary-400 dark:focus:ring-offset-dark-800"
    href="https://sub2api.io/proxyip"
    target="_blank"
    rel="noopener noreferrer"
  >
    <span class="truncate">{{ t('admin.proxies.ad.inline') }}</span>
    <UIcon name="i-lucide-external-link" class="size-3 shrink-0" />
  </a>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>
