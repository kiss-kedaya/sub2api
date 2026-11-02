import { computed, readonly, ref } from 'vue'
import {
  PALETTE_SHADES,
  paletteColor,
  paletteHex,
  paletteRgbChannels,
  type ChromaticPaletteName,
  type PaletteName,
  type PaletteShade
} from '@/theme/palettes'
import { DEFAULT_SITE_THEME, THEME_MODE_OPTIONS, normalizeSiteTheme, pick, type SiteThemeSettings, type ThemeFont, type ThemeMode } from '@/theme/settings'
import { getAppRuntimeContext } from '@runtime/context'
export * from '@/theme/settings'

// Same fallbacks as tailwind.config.js, ending in PingFang SC / Microsoft YaHei / sans-serif.
const SYSTEM_FONT_STACK = "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif"

/**
 * Font-family stacks only; this module writes no @font-face and fetches nothing
 * (Google Fonts is blocked for mainland China visitors). Public Sans and Inter
 * are self-hosted, latin only, by src/styles/fonts.css; Chinese text falls
 * through to PingFang SC / Microsoft YaHei. Noto Sans SC is too large to bundle,
 * so it is used only where it is installed. `system` writes nothing, so the
 * stylesheet's own font stack stays in charge.
 */
export const THEME_FONT_STACKS: Readonly<Record<ThemeFont, string | null>> = {
  system: null,
  'public-sans': `'Public Sans', ${SYSTEM_FONT_STACK}`,
  inter: `'Inter', ${SYSTEM_FONT_STACK}`,
  'noto-sans-sc': `'Noto Sans SC', ${SYSTEM_FONT_STACK}`
}

export const SITE_THEME_STYLE_ID = 'site-theme-vars'
const THEME_TRANSITION_CLASS = 'theme-transitioning'
const THEME_TRANSITION_MS = 240
let themeTransitionTimer: ReturnType<typeof setTimeout> | null = null

function animateThemeChange(): void {
  if (typeof document === 'undefined' || document.documentElement.classList.contains('motion-reduce')) return
  const root = document.documentElement
  root.classList.remove(THEME_TRANSITION_CLASS)
  // Force a new animation window when a user changes mode twice quickly.
  void root.offsetWidth
  root.classList.add(THEME_TRANSITION_CLASS)
  if (themeTransitionTimer) clearTimeout(themeTransitionTimer)
  themeTransitionTimer = setTimeout(() => {
    root.classList.remove(THEME_TRANSITION_CLASS)
    themeTransitionTimer = null
  }, THEME_TRANSITION_MS)
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
 * radius and font are optional overrides. The shipped primary is sakura. If an
 * administrator selects black for daytime, dark mode still keeps sakura as
 * the night accent unless another chromatic palette is explicitly selected.
 */
export function buildSiteThemeCss(input: unknown): string {
  const theme = normalizeSiteTheme(input)
  const neutral = theme.theme_neutral
  const chromatic: ChromaticPaletteName | null = theme.theme_primary === 'black' ? null : theme.theme_primary
  const primary: PaletteName = chromatic ?? neutral
  const darkChromatic: ChromaticPaletteName = chromatic ?? 'sakura'
  const neutralScale = scale('--ui-color-neutral', neutral, paletteColor)
  const light: Vars = {
    ...scale('--ui-color-primary', primary, paletteColor),
    ...neutralScale,
    '--ui-primary': chromatic ? 'var(--ui-color-primary-500)' : paletteColor(neutral, 900)
  }
  const dark: Vars = { ...scale('--ui-color-primary', darkChromatic, paletteColor), ...neutralScale, '--ui-primary': 'var(--ui-color-primary-400)' }
  if (theme.theme_radius !== DEFAULT_SITE_THEME.theme_radius) {
    light['--ui-radius'] = dark['--ui-radius'] = `${theme.theme_radius}rem`
  }
  const fontStack = THEME_FONT_STACKS[theme.theme_font]
  if (fontStack) light['--font-sans'] = dark['--font-sans'] = fontStack

  const rules: string[] = []
  {
    const darkHex = (shade: PaletteShade) => paletteHex(darkChromatic, shade)
    const darkRgb = (shade: PaletteShade, alpha: number) => `rgb(${paletteRgbChannels(darkChromatic, shade)} / ${alpha})`
    Object.assign(dark, scale('--c-primary', darkChromatic, paletteRgbChannels))
    if (chromatic) {
      const lightHex = (shade: PaletteShade) => paletteHex(chromatic, shade)
      const lightRgb = (shade: PaletteShade, alpha: number) => `rgb(${paletteRgbChannels(chromatic, shade)} / ${alpha})`
      Object.assign(light, scale('--c-primary', chromatic, paletteRgbChannels), {
        '--ui-sakura': lightHex(400),
        '--ui-halo': `0 0 0 3px ${lightRgb(400, 0.5)}`,
        '--ui-btn': `linear-gradient(180deg, ${lightHex(600)} 0%, ${lightHex(700)} 100%)`,
        '--ui-btn-hover': `linear-gradient(180deg, ${lightHex(500)} 0%, ${lightHex(600)} 100%)`
      })
      rules.push(rule(`${LIGHT} ${CONSOLE_SCOPE}`, {
        '--signal-accent': lightHex(600),
        '--signal-command': lightHex(600),
        '--signal-command-hover': lightHex(700),
        '--signal-sakura': lightHex(400),
        '--signal-hero-mark': lightHex(500)
      }))
    }
    rules.push(rule(`${DARK} ${CONSOLE_SCOPE}`, {
      '--signal-accent': darkHex(400),
      '--signal-on-accent': darkHex(950),
      '--signal-accent-soft': darkRgb(400, 0.16),
      '--signal-command': darkHex(400),
      '--signal-command-text': darkHex(950),
      '--signal-command-hover': darkHex(300),
      '--signal-nav-active': darkRgb(400, 0.16),
      '--signal-nav-active-text': darkHex(200),
      '--signal-nav-brand': darkHex(200),
      '--signal-hero-mark': darkHex(300),
      '--signal-sakura': darkHex(400)
    }))
  }
  rules.unshift(rule(LIGHT, light), rule(DARK, dark))
  if (fontStack) rules.push(`html:root,html:root ${CONSOLE_SCOPE}{font-family:var(--font-sans);}`)
  return rules.join('\n')
}
const standaloneTheme = ref<SiteThemeSettings>({ ...DEFAULT_SITE_THEME })
const standaloneVisitorMode = ref<ThemeMode | null>(savedVisitorMode())

function currentTheme() {
  const runtime = getAppRuntimeContext()
  return runtime ? (runtime.siteTheme ??= ref({ ...DEFAULT_SITE_THEME })) : standaloneTheme
}

function currentVisitorMode() {
  const runtime = getAppRuntimeContext()
  return runtime ? (runtime.visitorMode ??= ref<ThemeMode | null>(savedVisitorMode())) : standaloneVisitorMode
}

function writeTheme(theme: SiteThemeSettings): void {
  if (typeof document === 'undefined') return
  animateThemeChange()
  let style = document.getElementById(SITE_THEME_STYLE_ID)
  if (!(style instanceof HTMLStyleElement)) {
    style?.remove()
    style = document.createElement('style')
    style.id = SITE_THEME_STYLE_ID
    document.head.appendChild(style)
  }
  style.textContent = buildSiteThemeCss(theme)
}

/** The visitor's own choice, kept in localStorage "theme"; null means none yet. */
function savedVisitorMode(): ThemeMode | null {
  try {
    const saved = localStorage.getItem('theme')
    return saved === 'light' || saved === 'dark' || saved === 'system' ? saved : null
  } catch {
    return null
  }
}

const DARK_QUERY = '(prefers-color-scheme: dark)'

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(DARK_QUERY).matches
}

function modeIsDark(mode: ThemeMode): boolean {
  return mode === 'system' ? systemPrefersDark() : mode === 'dark'
}

/**
 * A visitor's own choice (localStorage "theme": light, dark or system) wins over
 * the site default mode; "system" follows the operating system.
 */
export function resolveSiteThemeDark(theme: SiteThemeSettings): boolean {
  return modeIsDark(savedVisitorMode() ?? theme.theme_default_mode)
}

// Mirror of the real `dark` class on <html>, so toggles show the mode actually in
// force, whoever set it (startup, a preview, or a page with its own toggle).
const htmlDark = ref(typeof document !== 'undefined' && document.documentElement.classList.contains('dark'))
let colorModeWatched = false

function setDark(dark: boolean): void {
  if (typeof document === 'undefined') return
  if (document.documentElement.classList.contains('dark') !== dark) animateThemeChange()
  document.documentElement.classList.toggle('dark', dark)
  htmlDark.value = dark
}

function watchColorMode(): void {
  if (colorModeWatched || typeof document === 'undefined') return
  colorModeWatched = true
  const root = document.documentElement
  htmlDark.value = root.classList.contains('dark')
  if (typeof MutationObserver === 'function') {
    new MutationObserver(() => {
      htmlDark.value = root.classList.contains('dark')
    }).observe(root, { attributes: true, attributeFilter: ['class'] })
  }
  // With "system" in force, follow the OS switching between light and dark.
  const query = typeof window.matchMedia === 'function' ? window.matchMedia(DARK_QUERY) : null
  query?.addEventListener?.('change', () => {
    if ((savedVisitorMode() ?? currentTheme().value.theme_default_mode) === 'system') setDark(systemPrefersDark())
  })
}

export interface ApplySiteThemeOptions {
  /**
   * Also set the html.dark class: the visitor's saved choice, else theme_default_mode.
   * Use at startup and when the site default mode changes, not for every refresh.
   */
  colorMode?: boolean
}

/** Applies and remembers the site theme, e.g. from appStore.cachedPublicSettings. */
export function applySiteTheme(input: unknown, options: ApplySiteThemeOptions = {}): SiteThemeSettings {
  const theme = normalizeSiteTheme(input)
  currentTheme().value = theme
  writeTheme(theme)
  if (options.colorMode) {
    watchColorMode()
    currentVisitorMode().value = savedVisitorMode()
    setDark(resolveSiteThemeDark(theme))
  }
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
  return applySiteTheme(currentTheme().value, options)
}

export function useSiteTheme() {
  return {
    theme: readonly(currentTheme()),
    apply: applySiteTheme,
    preview: previewSiteTheme,
    previewMode: previewSiteThemeMode,
    restore: restoreSiteTheme
  }
}

/** Saves the visitor's light / dark / system choice and applies it at once. */
export function setColorModePreference(mode: ThemeMode): void {
  const next = pick(THEME_MODE_OPTIONS, mode, DEFAULT_SITE_THEME.theme_default_mode)
  try {
    localStorage.setItem('theme', next)
    document.cookie = `theme=${next}; Path=/; SameSite=Lax; Max-Age=31536000`
  } catch {
    // Private mode: the choice still applies to this page.
  }
  currentVisitorMode().value = next
  setDark(modeIsDark(next))
}

/** Flips whatever mode is in force now (read from <html>) and saves the result. */
export function toggleColorMode(): void {
  const dark = typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
  setColorModePreference(dark ? 'light' : 'dark')
}

/**
 * Visitor color mode for toggles and menus. `isDark` follows the real `dark` class
 * on <html>; `preference` is the saved choice, or the site default when none.
 */
export function useSiteColorMode() {
  watchColorMode()
  // Pages with their own toggle write localStorage directly; pick that up here.
  currentVisitorMode().value = savedVisitorMode()
  return {
    isDark: readonly(htmlDark),
    preference: computed<ThemeMode>(() => currentVisitorMode().value ?? currentTheme().value.theme_default_mode),
    setPreference: setColorModePreference,
    toggle: toggleColorMode
  }
}
