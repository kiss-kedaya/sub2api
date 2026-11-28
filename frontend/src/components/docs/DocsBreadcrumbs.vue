<template>
  <nav
    data-testid="docs-breadcrumbs"
    :aria-label="t('docs.breadcrumbsLabel')"
    class="mb-4 flex flex-wrap items-center justify-between gap-2 text-[13px] text-slate-500 dark:text-slate-400"
  >
    <ol class="flex min-w-0 flex-wrap items-center gap-1.5">
      <li class="inline-flex items-center">
        <router-link
          :to="homeLocation"
          class="inline-flex items-center gap-1 rounded-md px-1 py-0.5 text-slate-500 transition-colors hover:text-slate-900 dark:text-slate-400 dark:hover:text-white"
          :aria-label="t('docs.title')"
        >
          <Icon name="home" size="xs" />
        </router-link>
      </li>
      <li class="inline-flex items-center gap-1.5">
        <Icon name="chevronRight" size="xs" class="text-slate-300 dark:text-dark-600" aria-hidden="true" />
        <router-link
          :to="categoryLocation"
          data-testid="docs-breadcrumb-category"
          class="rounded-md px-1 py-0.5 font-medium text-blue-600 transition-colors hover:bg-blue-50/80 dark:text-blue-400 dark:hover:bg-blue-500/10"
        >
          {{ categoryLabel }}
        </router-link>
      </li>
      <li v-if="pageTitle" class="inline-flex min-w-0 items-center gap-1.5">
        <Icon name="chevronRight" size="xs" class="shrink-0 text-slate-300 dark:text-dark-600" aria-hidden="true" />
        <router-link
          v-if="subPageLabel && pageLocation"
          :to="pageLocation"
          class="truncate rounded-md px-1 py-0.5 text-slate-600 transition-colors hover:text-slate-950 dark:text-slate-300 dark:hover:text-white"
        >
          {{ pageTitle }}
        </router-link>
        <span
          v-else
          aria-current="page"
          class="truncate font-medium text-slate-800 dark:text-slate-200"
        >
          {{ pageTitle }}
        </span>
      </li>
      <li v-if="subPageLabel" class="inline-flex min-w-0 items-center gap-1.5">
        <Icon name="chevronRight" size="xs" class="shrink-0 text-slate-300 dark:text-dark-600" aria-hidden="true" />
        <span aria-current="page" class="truncate font-medium text-slate-800 dark:text-slate-200">
          {{ subPageLabel }}
        </span>
      </li>
    </ol>

    <div
      v-if="readingMinutes > 0"
      class="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-slate-200/80 bg-slate-50/75 px-2.5 py-0.5 text-[11.5px] tabular-nums text-slate-500 dark:border-dark-700 dark:bg-dark-900/70 dark:text-slate-400"
    >
      <Icon name="clock" size="xs" />
      <span>{{ t('docs.readingTime', { minutes: readingMinutes }) }}</span>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { RouteLocationRaw } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'

withDefaults(
  defineProps<{
    homeLocation: RouteLocationRaw
    categoryLocation: RouteLocationRaw
    categoryLabel: string
    pageTitle: string
    pageLocation?: RouteLocationRaw
    subPageLabel?: string
    readingMinutes?: number
  }>(),
  {
    pageLocation: undefined,
    subPageLabel: '',
    readingMinutes: 0,
  },
)

const { t } = useI18n()
</script>
