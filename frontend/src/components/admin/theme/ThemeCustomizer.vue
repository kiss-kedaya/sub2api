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
    <!-- One line per theme setting, same shape as every other settings tab: name, the current value
         inline, a swatch or a quick select on the right, and the full picker under the line. -->
    <SettingPanel hide-summary>
      <SettingGroup flush>
        <SettingRow
          v-for="row in rows"
          :key="row.field"
          :label="row.label"
          :hint="row.hint"
          :summary="row.summary"
          :on="true"
          :in-summary="false"
          :wide-control="row.wide"
          persistent-summary
          :default-open="row.field === 'theme_primary'"
          :test-id="`site-theme-row-${row.field}`"
        >
          <template #control>
            <span v-if="row.swatch" class="site-theme-swatch" :style="row.swatch" aria-hidden="true" />
            <Select
              v-else
              :model-value="draft[row.field]"
              :options="plainOptions(row.options)"
              :disabled="saving || loadFailed"
              :aria-label="row.label"
              :class="row.wide ? 'w-full max-w-xs' : 'w-36'"
              @update:model-value="selectFromControl(row, $event)"
            />
          </template>
          <template #fields>
            <!-- Colours and fonts are long lists of long names: each option gets its own line of the
                 grid instead of being squeezed into a row of unreadable chips. -->
            <div
              :id="`site-theme-options-${row.field}`"
              role="radiogroup"
              :aria-label="row.label"
              class="theme-options"
            >
              <UButton
                v-for="(option, index) in row.options"
                :key="String(option.value)"
                role="radio"
                :aria-checked="option.value === draft[row.field]"
                :tabindex="option.value === draft[row.field] ? 0 : -1"
                :data-value="String(option.value)"
                :color="option.value === draft[row.field] ? 'primary' : 'neutral'"
                :variant="option.value === draft[row.field] ? 'subtle' : 'outline'"
                :disabled="saving || loadFailed"
                class="theme-option justify-start"
                @click="select(row.field, option.value)"
                @keydown="onOptionKeydown($event, row, index)"
              >
                <span v-if="option.swatch" class="site-theme-swatch" :style="option.swatch" aria-hidden="true" />
                <span class="truncate">{{ option.label }}</span>
              </UButton>
            </div>
          </template>
        </SettingRow>
      </SettingGroup>

      <div class="site-theme-preview" :style="previewStyle" data-testid="site-theme-preview">
        <p class="text-xs font-medium text-muted">{{ t('admin.settings.siteTheme.preview') }}</p>
        <p class="site-theme-preview-text mt-2 text-sm">{{ t('admin.settings.siteTheme.previewText') }}</p>
        <div class="mt-3 flex flex-wrap items-center gap-3">
          <span class="site-theme-preview-button">{{ t('admin.settings.siteTheme.previewButton') }}</span>
          <span class="site-theme-preview-badge">{{ t('admin.settings.siteTheme.previewBadge') }}</span>
        </div>
      </div>
    </SettingPanel>
  </SettingsSection>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsSection from '@/views/admin/settings/SettingsSection.vue'
import SettingPanel from '@/components/common/SettingPanel.vue'
import SettingGroup from '@/components/common/SettingGroup.vue'
import SettingRow from '@/components/common/SettingRow.vue'
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
interface Row {
  field: Field
  label: string
  hint: string
  summary: string
  swatch?: Record<string, string>
  wide: boolean
  options: Option[]
}

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
let previewed = false
let modePreviewed = false

function swatch(palette: PaletteName, lightShade: PaletteShade, darkShade: PaletteShade): Record<string, string> {
  return { '--swatch-light': paletteHex(palette, lightShade), '--swatch-dark': paletteHex(palette, darkShade) }
}

// Black keeps the sakura night accent when the visitor chooses a neutral daytime primary.
const NIGHT_ACCENT = paletteHex('sakura', 400)

function primarySwatch(value: ThemePrimary): Record<string, string> {
  if (value === 'black') return { '--swatch-light': paletteHex(draft.value.theme_neutral, 900), '--swatch-dark': NIGHT_ACCENT }
  return swatch(value, 500, 400)
}

function colorLabel(name: string): string {
  return t(`admin.settings.siteTheme.colors.${name}`)
}

function makeOptions<T extends OptionValue>(values: readonly T[], optionLabel: (value: T) => string, optionSwatch?: (value: T) => Record<string, string>): Option[] {
  return values.map((value) => ({ value, label: optionLabel(value), swatch: optionSwatch?.(value) }))
}

const rows = computed<Row[]>(() => {
  const primary = makeOptions(THEME_PRIMARY_OPTIONS, colorLabel, primarySwatch)
  const neutral = makeOptions(THEME_NEUTRAL_OPTIONS, colorLabel, (value) => swatch(value, 500, 500))
  const font = makeOptions(THEME_FONT_OPTIONS, (value) => t(`admin.settings.siteTheme.fonts.${FONT_LABEL_KEYS[value]}`))
  const radius = makeOptions(THEME_RADIUS_OPTIONS, (value) => String(value))
  const mode = makeOptions(THEME_MODE_OPTIONS, (value) => t(`admin.settings.siteTheme.modes.${value}`))
  const current = (options: Option[], field: Field): Option =>
    options.find((option) => option.value === draft.value[field]) ?? options[0]
  return [
    {
      field: 'theme_primary',
      label: t('admin.settings.siteTheme.primary'),
      hint: t('admin.settings.siteTheme.primaryHint'),
      summary: current(primary, 'theme_primary').label,
      swatch: current(primary, 'theme_primary').swatch,
      wide: false,
      options: primary,
    },
    {
      field: 'theme_neutral',
      label: t('admin.settings.siteTheme.neutral'),
      hint: t('admin.settings.siteTheme.neutralHint'),
      summary: current(neutral, 'theme_neutral').label,
      swatch: current(neutral, 'theme_neutral').swatch,
      wide: false,
      options: neutral,
    },
    {
      field: 'theme_font',
      label: t('admin.settings.siteTheme.font'),
      hint: t('admin.settings.siteTheme.fontHint'),
      summary: current(font, 'theme_font').label,
      wide: true,
      options: font,
    },
    {
      field: 'theme_radius',
      label: t('admin.settings.siteTheme.radius'),
      hint: t('admin.settings.siteTheme.radiusHint'),
      summary: current(radius, 'theme_radius').label,
      wide: false,
      options: radius,
    },
    {
      field: 'theme_default_mode',
      label: t('admin.settings.siteTheme.mode'),
      hint: t('admin.settings.siteTheme.modeHint'),
      summary: current(mode, 'theme_default_mode').label,
      wide: false,
      options: mode,
    },
  ]
})

function plainOptions(options: Option[]): { value: OptionValue; label: string }[] {
  return options.map((option) => ({ value: option.value, label: option.label }))
}

const dirty = computed(() => FIELDS.some((field) => draft.value[field] !== saved.value[field]))
const isDefault = computed(() => FIELDS.every((field) => draft.value[field] === DEFAULT_SITE_THEME[field]))
const previewStyle = computed(() => ({ '--site-theme-preview-radius': `${draft.value.theme_radius}rem` }))

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

// The quick select in the row header and the picker under it write the same draft.
function selectFromControl(row: Row, value: OptionValue) {
  select(row.field, value)
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

<style scoped>
/* The expanded picker: options share the row's width and wrap onto several lines, so a colour name or
   a font name is never cut down to a chip. */
.theme-options {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
  gap: 8px;
  min-width: 0;
}

.theme-option {
  justify-content: flex-start;
  min-width: 0;
}
</style>

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
/* Neutral daytime primary keeps the night preview on sakura. */
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
