import { readonly, ref } from 'vue'
import {
  CHROMATIC_PALETTE_NAMES,
  NEUTRAL_PALETTE_NAMES,
  PALETTE_SHADES,
  paletteColor,
  paletteHex,
  paletteRgbChannels,
  type ChromaticPaletteName,
  type NeutralPaletteName,
  type PaletteName,
  type PaletteShade
} from '@/theme/palettes'

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
  theme_primary: 'black',
  theme_neutral: 'neutral',
  theme_radius: 0.375,
  theme_font: 'system',
  theme_default_mode: 'system'
})

const SYSTEM_FONT_STACK = "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif"

/** `system` writes nothing, so the stylesheet's own font stack stays in charge. */
export const THEME_FONT_STACKS: Readonly<Record<ThemeFont, string | null>> = {
  system: null,
  'public-sans': `'Public Sans', ${SYSTEM_FONT_STACK}`,
  inter: `'Inter', ${SYSTEM_FONT_STACK}`,
  'noto-sans-sc': `'Noto Sans SC', ${SYSTEM_FONT_STACK}`
}

// Origins already allowed by the default CSP (style-src / font-src).
export const THEME_FONT_STYLESHEETS: Readonly<Record<ThemeFont, string | null>> = {
  system: null,
  'public-sans': 'https://fonts.googleapis.com/css2?family=Public+Sans:wght@400;500;600;700&display=swap',
  inter: 'https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap',
  'noto-sans-sc': 'https://fonts.googleapis.com/css2?family=Noto+Sans+SC:wght@400;500;700&display=swap'
}

export const SITE_THEME_STYLE_ID = 'site-theme-vars'
export const SITE_THEME_FONT_LINK_ID = 'site-theme-font'

function pick<T extends string | number>(options: readonly T[], value: unknown, fallback: T): T {
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

// The console's --signal-* tokens live on these elements (styles/console-studio.css),
// so they are overridden there rather than on :root.
const CONSOLE_SCOPE = ':is(.console-signal, body:has(.console-signal) > .modal-overlay, body:has(.console-signal) > .dialog-overlay, body:has(.console-signal) > .select-dropdown-portal, body:has(.console-signal) > .account-popover)'
// html:root.* outranks every :root / .dark / html:not(.dark) rule in the stylesheets
// regardless of load order (Vite may inject route CSS after this element).
const LIGHT = 'html:root:not(.dark)'
const DARK = 'html:root.dark'

type Vars = Record<string, string>

function rule(selector: string, vars: Vars): string {
  return `${selector}{${Object.entries(vars).map(([name, value]) => `${name}:${value};`).join('')}}`
}

function scale(prefix: string, palette: PaletteName, value: (palette: PaletteName, shade: PaletteShade) => string): Vars {
  return Object.fromEntries(PALETTE_SHADES.map((shade) => [`${prefix}-${shade}`, value(palette, shade)]))
}

/**
 * Builds the theme stylesheet from allowlisted settings. Default values for
 * radius, font and the black primary write nothing that the existing
 * stylesheets define, so the default theme keeps the current look.
 */
export function buildSiteThemeCss(input: unknown): string {
  const theme = normalizeSiteTheme(input)
  const neutral = theme.theme_neutral
  const chromatic: ChromaticPaletteName | null = theme.theme_primary === 'black' ? null : theme.theme_primary
  const primary: PaletteName = chromatic ?? neutral
  const shared: Vars = {
    ...scale('--ui-color-primary', primary, paletteColor),
    ...scale('--ui-color-neutral', neutral, paletteColor)
  }
  const light: Vars = { ...shared, '--ui-primary': chromatic ? 'var(--ui-color-primary-500)' : paletteColor(neutral, 900) }
  const dark: Vars = { ...shared, '--ui-primary': chromatic ? 'var(--ui-color-primary-400)' : paletteColor(neutral, 100) }
  if (theme.theme_radius !== DEFAULT_SITE_THEME.theme_radius) {
    light['--ui-radius'] = dark['--ui-radius'] = `${theme.theme_radius}rem`
  }
  const fontStack = THEME_FONT_STACKS[theme.theme_font]
  if (fontStack) light['--font-sans'] = dark['--font-sans'] = fontStack

  const rules: string[] = []
  if (chromatic) {
    const hex = (shade: PaletteShade) => paletteHex(chromatic, shade)
    const rgb = (shade: PaletteShade, alpha: number) => `rgb(${paletteRgbChannels(chromatic, shade)} / ${alpha})`
    const legacy = scale('--c-primary', chromatic, paletteRgbChannels)
    Object.assign(light, legacy, {
      '--ui-sakura': hex(400),
      '--ui-halo': `0 0 0 3px ${rgb(400, 0.5)}`,
      '--ui-btn': `linear-gradient(180deg, ${hex(600)} 0%, ${hex(700)} 100%)`,
      '--ui-btn-hover': `linear-gradient(180deg, ${hex(500)} 0%, ${hex(600)} 100%)`
    })
    Object.assign(dark, legacy)
    rules.push(rule(`${LIGHT} ${CONSOLE_SCOPE}`, {
      '--signal-accent': hex(600),
      '--signal-command': hex(600),
      '--signal-command-hover': hex(700),
      '--signal-sakura': hex(400),
      '--signal-hero-mark': hex(500)
    }))
    rules.push(rule(`${DARK} ${CONSOLE_SCOPE}`, {
      '--signal-accent': hex(400),
      '--signal-on-accent': hex(950),
      '--signal-accent-soft': rgb(400, 0.16),
      '--signal-command': hex(400),
      '--signal-command-text': hex(950),
      '--signal-command-hover': hex(300),
      '--signal-nav-active': rgb(400, 0.16),
      '--signal-nav-active-text': hex(200),
      '--signal-nav-brand': hex(200),
      '--signal-hero-mark': hex(300),
      '--signal-sakura': hex(400)
    }))
  }
  rules.unshift(rule(LIGHT, light), rule(DARK, dark))
  if (fontStack) rules.push(`html:root,html:root ${CONSOLE_SCOPE}{font-family:var(--font-sans);}`)
  return rules.join('\n')
}
const appliedTheme = ref<SiteThemeSettings>({ ...DEFAULT_SITE_THEME })

function writeTheme(theme: SiteThemeSettings): void {
  if (typeof document === 'undefined') return
  let style = document.getElementById(SITE_THEME_STYLE_ID)
  if (!(style instanceof HTMLStyleElement)) {
    style?.remove()
    style = document.createElement('style')
    style.id = SITE_THEME_STYLE_ID
    document.head.appendChild(style)
  }
  style.textContent = buildSiteThemeCss(theme)

  const href = THEME_FONT_STYLESHEETS[theme.theme_font]
  const link = document.getElementById(SITE_THEME_FONT_LINK_ID)
  if (!href) {
    link?.remove()
  } else if (!(link instanceof HTMLLinkElement) || link.getAttribute('href') !== href) {
    link?.remove()
    const next = document.createElement('link')
    next.id = SITE_THEME_FONT_LINK_ID
    next.rel = 'stylesheet'
    next.href = href
    document.head.appendChild(next)
  }
}

function savedVisitorMode(): 'light' | 'dark' | null {
  try {
    const saved = localStorage.getItem('theme')
    return saved === 'light' || saved === 'dark' ? saved : null
  } catch {
    return null
  }
}

function modeIsDark(mode: ThemeMode): boolean {
  if (mode !== 'system') return mode === 'dark'
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches
}

/** A visitor's own toggle (localStorage "theme") wins over the site default mode. */
export function resolveSiteThemeDark(theme: SiteThemeSettings): boolean {
  const saved = savedVisitorMode()
  return saved ? saved === 'dark' : modeIsDark(theme.theme_default_mode)
}

function setDark(dark: boolean): void {
  if (typeof document !== 'undefined') document.documentElement.classList.toggle('dark', dark)
}

export interface ApplySiteThemeOptions {
  /** Also set the html.dark class from theme_default_mode (use once at startup). */
  colorMode?: boolean
}

/** Applies and remembers the site theme, e.g. from appStore.cachedPublicSettings. */
export function applySiteTheme(input: unknown, options: ApplySiteThemeOptions = {}): SiteThemeSettings {
  const theme = normalizeSiteTheme(input)
  appliedTheme.value = theme
  writeTheme(theme)
  if (options.colorMode) setDark(resolveSiteThemeDark(theme))
  return theme
}

/** Live preview: writes the CSS without changing the remembered theme. */
export function previewSiteTheme(input: unknown): SiteThemeSettings {
  const theme = normalizeSiteTheme(input)
  writeTheme(theme)
  return theme
}

/** Live preview of a default mode, ignoring the visitor's saved toggle. */
export function previewSiteThemeMode(mode: unknown): void {
  setDark(modeIsDark(pick(THEME_MODE_OPTIONS, mode, DEFAULT_SITE_THEME.theme_default_mode)))
}

/** Undoes previews by re-applying the remembered theme. */
export function restoreSiteTheme(options: ApplySiteThemeOptions = {}): SiteThemeSettings {
  return applySiteTheme(appliedTheme.value, options)
}

export function useSiteTheme() {
  return {
    theme: readonly(appliedTheme),
    apply: applySiteTheme,
    preview: previewSiteTheme,
    previewMode: previewSiteThemeMode,
    restore: restoreSiteTheme
  }
}
