<template>
  <aside
    :data-testid="testId"
    class="docs-callout my-6 overflow-hidden rounded-xl border border-l-[3px] px-4.5 py-3.5 text-[14.5px] leading-7"
    :class="toneClasses.container"
    :role="tone === 'warning' || tone === 'danger' ? 'alert' : 'note'"
  >
    <div class="mb-1 flex items-center gap-2 text-[12.5px] font-semibold uppercase tracking-[0.04em]" :class="toneClasses.heading">
      <Icon :name="toneClasses.icon" size="xs" class="shrink-0" />
      <span>{{ resolvedTitle }}</span>
    </div>
    <div class="text-[14.5px] leading-7" :class="toneClasses.body">
      <slot />
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

export type DocsCalloutTone = 'info' | 'tip' | 'warning' | 'danger'

const props = withDefaults(
  defineProps<{
    tone?: DocsCalloutTone
    title?: string
    testId?: string
  }>(),
  {
    tone: 'info',
    title: '',
    testId: undefined,
  },
)

const { t } = useI18n()

const toneClasses = computed(() => {
  switch (props.tone) {
    case 'tip':
      return {
        icon: 'lightbulb' as const,
        container: 'border-emerald-200/90 border-l-emerald-500 bg-emerald-50/55 dark:border-emerald-500/25 dark:border-l-emerald-400 dark:bg-emerald-500/[0.08]',
        heading: 'text-emerald-800 dark:text-emerald-300',
        body: 'text-emerald-950/90 dark:text-emerald-100/90',
        defaultKey: 'docs.calloutTip',
      }
    case 'warning':
      return {
        icon: 'exclamationTriangle' as const,
        container: 'border-amber-200/90 border-l-amber-500 bg-amber-50/65 dark:border-amber-500/30 dark:border-l-amber-400 dark:bg-amber-500/[0.09]',
        heading: 'text-amber-800 dark:text-amber-300',
        body: 'text-amber-950/90 dark:text-amber-100/90',
        defaultKey: 'docs.calloutWarning',
      }
    case 'danger':
      return {
        icon: 'exclamationCircle' as const,
        container: 'border-rose-200/90 border-l-rose-500 bg-rose-50/60 dark:border-rose-500/30 dark:border-l-rose-400 dark:bg-rose-500/[0.09]',
        heading: 'text-rose-800 dark:text-rose-300',
        body: 'text-rose-950/90 dark:text-rose-100/90',
        defaultKey: 'docs.calloutDanger',
      }
    case 'info':
    default:
      return {
        icon: 'infoCircle' as const,
        container: 'border-blue-200/85 border-l-blue-500 bg-blue-50/55 dark:border-blue-500/25 dark:border-l-blue-400 dark:bg-blue-500/[0.08]',
        heading: 'text-blue-700 dark:text-blue-300',
        body: 'text-slate-700 dark:text-slate-200',
        defaultKey: 'docs.calloutNote',
      }
  }
})

const resolvedTitle = computed(() => props.title || t(toneClasses.value.defaultKey))
</script>
