import { CHROMATIC_PALETTE_NAMES, NEUTRAL_PALETTE_NAMES, type NeutralPaletteName } from './palettes'

export const THEME_PRIMARY_OPTIONS = ['black', ...CHROMATIC_PALETTE_NAMES] as const
export const THEME_NEUTRAL_OPTIONS = NEUTRAL_PALETTE_NAMES
export const THEME_RADIUS_OPTIONS = [0, 0.125, 0.25, 0.375, 0.5] as const
export const THEME_FONT_OPTIONS = ['system', 'public-sans', 'inter', 'noto-sans-sc'] as const
export const THEME_MODE_OPTIONS = ['system', 'light', 'dark'] as const

export type ThemePrimary = (typeof THEME_PRIMARY_OPTIONS)[number]
export type ThemeRadius = (typeof THEME_RADIUS_OPTIONS)[number]
export type ThemeFont = (typeof THEME_FONT_OPTIONS)[number]
export type ThemeMode = (typeof THEME_MODE_OPTIONS)[number]

/** Same field names as the public settings and /admin/settings/theme responses. */
export interface SiteThemeSettings {
  theme_primary: ThemePrimary
  theme_neutral: NeutralPaletteName
  theme_radius: ThemeRadius
  theme_font: ThemeFont
  theme_default_mode: ThemeMode
}

export const DEFAULT_SITE_THEME: Readonly<SiteThemeSettings> = Object.freeze({
  theme_primary: 'sakura',
  theme_neutral: 'neutral',
  theme_radius: 0.375,
  theme_font: 'system',
  theme_default_mode: 'system'
})

export function pick<T extends string | number>(options: readonly T[], value: unknown, fallback: T): T {
  const index = options.indexOf(value as T)
  return index >= 0 ? options[index] : fallback
}

/** Keeps only allowlisted values; anything else (or a missing field) becomes the default. */
export function normalizeSiteTheme(input: unknown): SiteThemeSettings {
  const source = (input !== null && typeof input === 'object' ? input : {}) as Record<string, unknown>
  return {
    theme_primary: pick(THEME_PRIMARY_OPTIONS, source.theme_primary, DEFAULT_SITE_THEME.theme_primary),
    theme_neutral: pick(THEME_NEUTRAL_OPTIONS, source.theme_neutral, DEFAULT_SITE_THEME.theme_neutral),
    theme_radius: pick(THEME_RADIUS_OPTIONS, source.theme_radius, DEFAULT_SITE_THEME.theme_radius),
    theme_font: pick(THEME_FONT_OPTIONS, source.theme_font, DEFAULT_SITE_THEME.theme_font),
    theme_default_mode: pick(THEME_MODE_OPTIONS, source.theme_default_mode, DEFAULT_SITE_THEME.theme_default_mode)
  }
}
