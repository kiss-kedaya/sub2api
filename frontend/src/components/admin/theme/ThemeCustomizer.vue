<template>
  <SettingsSection
    :title="t('admin.settings.siteTheme.title')"
    :description="t('admin.settings.siteTheme.description')"
    :loading="loading"
    :aria-busy="loading || saving"
  >
    <template #actions>
      <UButton v-if="loadFailed" color="neutral" variant="outline" data-testid="site-theme-retry" @click="load">
        {{ t('admin.settings.siteTheme.retry') }}
      </UButton>
      <UButton v-else color="neutral" variant="outline" data-testid="site-theme-reset" :disabled="saving || isDefault" @click="resetToDefault">
        {{ t('admin.settings.siteTheme.reset') }}
      </UButton>
      <UButton data-testid="site-theme-save" :loading="saving" :disabled="saving || loadFailed || !dirty" @click="save">
        {{ saving ? t('common.saving') : t('common.save') }}
      </UButton>
    </template>

    <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
    <div v-for="row in rows" :key="row.field" :data-testid="`site-theme-row-${row.field}`">
      <UButton
        color="neutral"
        variant="ghost"
        class="-mx-2.5 w-[calc(100%+1.25rem)] justify-start gap-3"
        :aria-expanded="openField === row.field"
        :aria-controls="`site-theme-options-${row.field}`"
        :disabled="saving || loadFailed"
        @click="toggle(row.field)"
      >
        <span :id="`site-theme-label-${row.field}`" class="w-24 shrink-0 text-left font-medium text-highlighted">{{ row.label }}</span>
        <span v-if="row.current.swatch" class="site-theme-swatch" :style="row.current.swatch" aria-hidden="true" />
        <span class="flex-1 truncate text-left text-toned">{{ row.current.label }}</span>
        <UIcon name="i-lucide-chevron-right" class="size-4 shrink-0 text-dimmed transition-transform" :class="{ 'rotate-90': openField === row.field }" />
      </UButton>
      <div
        v-if="openField === row.field"
        :id="`site-theme-options-${row.field}`"
        role="radiogroup"
        :aria-labelledby="`site-theme-label-${row.field}`"
        class="mt-3 flex flex-wrap gap-2"
      >
        <UButton
          v-for="(option, index) in row.options"
          :key="String(option.value)"
          role="radio"
          :aria-checked="option.value === draft[row.field]"
          :tabindex="option.value === draft[row.field] ? 0 : -1"
          :data-value="String(option.value)"
          :color="option.value === draft[row.field] ? 'primary' : 'neutral'"
          :variant="option.value === draft[row.field] ? 'solid' : 'outline'"
          :disabled="saving || loadFailed"
          @click="select(row.field, option.value)"
          @keydown="onOptionKeydown($event, row, index)"
        >
          <span v-if="option.swatch" class="site-theme-swatch" :style="option.swatch" aria-hidden="true" />
          {{ option.label }}
        </UButton>
      </div>
    </div>

    <div class="site-theme-preview" :style="previewStyle" data-testid="site-theme-preview">
      <p class="text-xs font-medium text-muted">{{ t('admin.settings.siteTheme.preview') }}</p>
      <p class="site-theme-preview-text mt-2 text-sm">{{ t('admin.settings.siteTheme.previewText') }}</p>
      <div class="mt-3 flex flex-wrap items-center gap-3">
        <span class="site-theme-preview-button">{{ t('admin.settings.siteTheme.previewButton') }}</span>
        <span class="site-theme-preview-badge">{{ t('admin.settings.siteTheme.previewBadge') }}</span>
      </div>
    </div>
    <p class="text-xs text-muted">{{ t('admin.settings.siteTheme.modeHint') }}</p>
  </SettingsSection>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsSection from '@/views/admin/settings/SettingsSection.vue'
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

// Black keeps the stylesheet's night theme in dark mode, whose accent is this rose.
const NIGHT_ACCENT = '#e89ab3'

function primarySwatch(value: ThemePrimary): Record<string, string> {
  if (value === 'black') return { '--swatch-light': paletteHex(draft.value.theme_neutral, 900), '--swatch-dark': NIGHT_ACCENT }
  return swatch(value, 500, 400)
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
/* Black writes no dark primary, so fall back to the stylesheet's night rose. */
.dark .site-theme-preview-button {
  background: var(--ui-primary, rgb(var(--c-primary-400)));
  color: var(--ui-color-neutral-900, #171717);
}
.site-theme-preview-badge {
  padding: 0.125rem 0.5rem;
  font-size: 0.75rem;
  background: color-mix(in oklab, var(--ui-primary, #171717) 12%, transparent);
  color: var(--ui-primary, #171717);
}
.dark .site-theme-preview-badge {
  background: color-mix(in oklab, var(--ui-primary, rgb(var(--c-primary-400))) 16%, transparent);
  color: var(--ui-primary, rgb(var(--c-primary-400)));
}
</style>
