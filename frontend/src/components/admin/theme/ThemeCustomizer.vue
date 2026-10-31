<template>
  <section class="card" aria-labelledby="site-theme-title">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 id="site-theme-title" class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.siteTheme.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.siteTheme.description') }}</p>
    </div>
    <div class="space-y-5 p-6" :aria-busy="loading || saving">
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <template v-else>
        <div class="divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
          <div v-for="row in rows" :key="row.field" :data-testid="`site-theme-row-${row.field}`">
            <button
              type="button"
              class="flex w-full items-center gap-3 px-4 py-3 text-left text-sm"
              :aria-expanded="openField === row.field"
              :aria-controls="`site-theme-options-${row.field}`"
              :disabled="saving || loadFailed"
              @click="toggle(row.field)"
            >
              <span :id="`site-theme-label-${row.field}`" class="w-24 shrink-0 font-medium text-gray-900 dark:text-white">{{ row.label }}</span>
              <span v-if="row.current.swatch" class="site-theme-swatch" :style="row.current.swatch" aria-hidden="true" />
              <span class="flex-1 truncate text-gray-600 dark:text-gray-300">{{ row.current.label }}</span>
              <svg class="h-4 w-4 shrink-0 text-gray-400 transition-transform" :class="{ 'rotate-90': openField === row.field }" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                <path fill-rule="evenodd" d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.17 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z" clip-rule="evenodd" />
              </svg>
            </button>
            <div
              v-if="openField === row.field"
              :id="`site-theme-options-${row.field}`"
              role="radiogroup"
              :aria-labelledby="`site-theme-label-${row.field}`"
              class="flex flex-wrap gap-2 px-4 pb-4"
            >
              <button
                v-for="(option, index) in row.options"
                :key="String(option.value)"
                type="button"
                role="radio"
                :aria-checked="option.value === draft[row.field]"
                :tabindex="option.value === draft[row.field] ? 0 : -1"
                :data-value="String(option.value)"
                class="inline-flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm transition-colors"
                :class="option.value === draft[row.field]
                  ? 'border-gray-900 bg-gray-50 text-gray-900 dark:border-white dark:bg-dark-700 dark:text-white'
                  : 'border-gray-200 text-gray-600 hover:border-gray-300 dark:border-dark-600 dark:text-gray-300 dark:hover:border-dark-500'"
                :disabled="saving || loadFailed"
                @click="select(row.field, option.value)"
                @keydown="onOptionKeydown($event, row, index)"
              >
                <span v-if="option.swatch" class="site-theme-swatch" :style="option.swatch" aria-hidden="true" />
                {{ option.label }}
              </button>
            </div>
          </div>
        </div>

        <div class="site-theme-preview" :style="previewStyle" data-testid="site-theme-preview">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.settings.siteTheme.preview') }}</p>
          <p class="site-theme-preview-text mt-2 text-sm">{{ t('admin.settings.siteTheme.previewText') }}</p>
          <div class="mt-3 flex flex-wrap items-center gap-3">
            <span class="site-theme-preview-button">{{ t('admin.settings.siteTheme.previewButton') }}</span>
            <span class="site-theme-preview-badge">{{ t('admin.settings.siteTheme.previewBadge') }}</span>
          </div>
        </div>

        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.siteTheme.modeHint') }}</p>
        <p v-if="draft.theme_font !== 'system'" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.siteTheme.fontHint') }}</p>
        <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <div class="flex justify-end gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <button v-if="loadFailed" type="button" class="btn btn-secondary btn-sm" data-testid="site-theme-retry" @click="load">{{ t('admin.settings.siteTheme.retry') }}</button>
          <button v-else type="button" class="btn btn-secondary btn-sm" data-testid="site-theme-reset" :disabled="saving || isDefault" @click="resetToDefault">{{ t('admin.settings.siteTheme.reset') }}</button>
          <button type="button" class="btn btn-primary btn-sm" data-testid="site-theme-save" :disabled="saving || loadFailed || !dirty" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSiteTheme, updateSiteTheme } from '@/api/admin/siteTheme'
import {
  DEFAULT_SITE_THEME,
  THEME_FONT_OPTIONS,
  THEME_MODE_OPTIONS,
  THEME_NEUTRAL_OPTIONS,
  THEME_PRIMARY_OPTIONS,
  THEME_RADIUS_OPTIONS,
  applySiteTheme,
  normalizeSiteTheme,
  previewSiteTheme,
  previewSiteThemeMode,
  restoreSiteTheme,
  type SiteThemeSettings,
  type ThemeFont,
  type ThemePrimary
} from '@/composables/useSiteTheme'
import { paletteHex, type PaletteName, type PaletteShade } from '@/theme/palettes'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

type Field = keyof SiteThemeSettings
type OptionValue = string | number
interface Option { value: OptionValue; label: string; swatch?: Record<string, string> }
interface Row { field: Field; label: string; options: Option[]; current: Option }

const FIELDS: Field[] = ['theme_primary', 'theme_neutral', 'theme_font', 'theme_radius', 'theme_default_mode']
const FONT_LABEL_KEYS: Record<ThemeFont, string> = { system: 'system', 'public-sans': 'publicSans', inter: 'inter', 'noto-sans-sc': 'notoSansSc' }

const emit = defineEmits<{ save: [theme: SiteThemeSettings] }>()

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const loadFailed = ref(false)
const error = ref('')
const saved = ref<SiteThemeSettings>({ ...DEFAULT_SITE_THEME })
const draft = ref<SiteThemeSettings>({ ...DEFAULT_SITE_THEME })
const openField = ref<Field | null>('theme_primary')
let previewed = false
let modePreviewed = false

function swatch(palette: PaletteName, lightShade: PaletteShade, darkShade: PaletteShade): Record<string, string> {
  return { '--swatch-light': paletteHex(palette, lightShade), '--swatch-dark': paletteHex(palette, darkShade) }
}

function primarySwatch(value: ThemePrimary): Record<string, string> {
  return value === 'black' ? swatch(draft.value.theme_neutral, 900, 100) : swatch(value, 500, 400)
}

function colorLabel(name: string): string {
  return t(`admin.settings.siteTheme.colors.${name}`)
}

function makeRow<T extends OptionValue>(field: Field, label: string, values: readonly T[], optionLabel: (value: T) => string, optionSwatch?: (value: T) => Record<string, string>): Row {
  const options: Option[] = values.map((value) => ({ value, label: optionLabel(value), swatch: optionSwatch?.(value) }))
  return { field, label, options, current: options.find((option) => option.value === draft.value[field]) ?? options[0] }
}

const rows = computed<Row[]>(() => [
  makeRow('theme_primary', t('admin.settings.siteTheme.primary'), THEME_PRIMARY_OPTIONS, colorLabel, primarySwatch),
  makeRow('theme_neutral', t('admin.settings.siteTheme.neutral'), THEME_NEUTRAL_OPTIONS, colorLabel, (value) => swatch(value, 500, 500)),
  makeRow('theme_font', t('admin.settings.siteTheme.font'), THEME_FONT_OPTIONS, (value) => t(`admin.settings.siteTheme.fonts.${FONT_LABEL_KEYS[value]}`)),
  makeRow('theme_radius', t('admin.settings.siteTheme.radius'), THEME_RADIUS_OPTIONS, (value) => String(value)),
  makeRow('theme_default_mode', t('admin.settings.siteTheme.mode'), THEME_MODE_OPTIONS, (value) => t(`admin.settings.siteTheme.modes.${value}`))
])

const dirty = computed(() => FIELDS.some((field) => draft.value[field] !== saved.value[field]))
const isDefault = computed(() => FIELDS.every((field) => draft.value[field] === DEFAULT_SITE_THEME[field]))
const previewStyle = computed(() => ({ '--site-theme-preview-radius': `${draft.value.theme_radius}rem` }))

function toggle(field: Field) {
  openField.value = openField.value === field ? null : field
}

function preview(theme: SiteThemeSettings) {
  previewSiteTheme(theme)
  previewed = true
}

function select(field: Field, value: OptionValue) {
  draft.value = normalizeSiteTheme({ ...draft.value, [field]: value })
  preview(draft.value)
  if (field === 'theme_default_mode') {
    previewSiteThemeMode(draft.value.theme_default_mode)
    modePreviewed = true
  }
}

async function onOptionKeydown(event: KeyboardEvent, current: Row, index: number) {
  const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0
  if (!step) return
  event.preventDefault()
  const group = (event.currentTarget as HTMLElement).parentElement
  const nextIndex = (index + step + current.options.length) % current.options.length
  select(current.field, current.options[nextIndex].value)
  await nextTick()
  ;(group?.children[nextIndex] as HTMLElement | undefined)?.focus()
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const theme = normalizeSiteTheme(await getSiteTheme())
    saved.value = theme
    draft.value = { ...theme }
    loadFailed.value = false
    preview(theme)
  } catch (err) {
    loadFailed.value = true
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}

function resetToDefault() {
  draft.value = { ...DEFAULT_SITE_THEME }
  preview(draft.value)
  if (modePreviewed) previewSiteThemeMode(draft.value.theme_default_mode)
}

async function save() {
  if (saving.value || loadFailed.value) return
  saving.value = true
  error.value = ''
  try {
    const theme = normalizeSiteTheme(await updateSiteTheme({ ...draft.value }))
    saved.value = theme
    draft.value = { ...theme }
    applySiteTheme(theme)
    appStore.showSuccess(t('admin.settings.siteTheme.saved'))
    emit('save', theme)
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
// Leaving without saving puts back the theme the page had (and, if the mode was
// previewed, the visitor's own light/dark choice).
onBeforeUnmount(() => {
  if (previewed || modePreviewed) restoreSiteTheme({ colorMode: modePreviewed })
})
</script>

<style>
.site-theme-swatch {
  display: inline-block;
  width: 0.75rem;
  height: 0.75rem;
  flex-shrink: 0;
  border-radius: 9999px;
  background: var(--swatch-light);
  box-shadow: inset 0 0 0 1px rgb(0 0 0 / 0.1);
}
.dark .site-theme-swatch {
  background: var(--swatch-dark);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.12);
}
.site-theme-preview {
  border: 1px solid var(--ui-color-neutral-200, #e5e5e5);
  border-radius: calc(var(--site-theme-preview-radius) * 2);
  background: #ffffff;
  padding: 1rem;
}
.dark .site-theme-preview {
  border-color: var(--ui-color-neutral-800, #262626);
  background: var(--ui-color-neutral-900, #171717);
}
.site-theme-preview-text {
  color: var(--ui-color-neutral-700, #404040);
}
.dark .site-theme-preview-text {
  color: var(--ui-color-neutral-200, #e5e5e5);
}
.site-theme-preview-button,
.site-theme-preview-badge {
  display: inline-flex;
  align-items: center;
  border-radius: calc(var(--site-theme-preview-radius) * 1.5);
  font-weight: 500;
}
.site-theme-preview-button {
  padding: 0.375rem 0.75rem;
  font-size: 0.875rem;
  background: var(--ui-primary, #171717);
  color: #ffffff;
}
.dark .site-theme-preview-button {
  background: var(--ui-primary, #f5f5f5);
  color: var(--ui-color-neutral-900, #171717);
}
.site-theme-preview-badge {
  padding: 0.125rem 0.5rem;
  font-size: 0.75rem;
  background: color-mix(in oklab, var(--ui-primary, #171717) 12%, transparent);
  color: var(--ui-primary, #171717);
}
.dark .site-theme-preview-badge {
  background: color-mix(in oklab, var(--ui-primary, #f5f5f5) 16%, transparent);
  color: var(--ui-primary, #f5f5f5);
}
</style>
