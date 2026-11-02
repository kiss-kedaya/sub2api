import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_SITE_THEME,
  SITE_THEME_STYLE_ID,
  THEME_FONT_STACKS,
  applySiteTheme,
  buildSiteThemeCss,
  normalizeSiteTheme,
  previewSiteTheme,
  previewSiteThemeMode,
  restoreSiteTheme,
  toggleColorMode,
  useSiteColorMode,
  useSiteTheme
} from '../useSiteTheme'
import { paletteColor, paletteHex, paletteRgbChannels } from '@/theme/palettes'

function css(): string {
  return document.getElementById(SITE_THEME_STYLE_ID)?.textContent ?? ''
}

/** One rule of the generated stylesheet, from its selector to its closing brace. */
function block(out: string, selector: string): string {
  const start = out.indexOf(selector)
  expect(start, selector).toBeGreaterThanOrEqual(0)
  return out.slice(start, out.indexOf('}', start) + 1)
}

/** Nothing may be fetched for fonts: no <link>, @import or url() anywhere. */
function expectNoFontDownload(out: string) {
  expect(document.querySelectorAll('link')).toHaveLength(0)
  for (const fragment of ['googleapis', 'gstatic', '@import', 'url(', '@font-face']) {
    expect(out).not.toContain(fragment)
  }
}

function prefersDark(matches: boolean) {
  vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => ({ matches, media: query }) as MediaQueryList)
}

describe('useSiteTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    applySiteTheme(DEFAULT_SITE_THEME)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    document.getElementById(SITE_THEME_STYLE_ID)?.remove()
  })

  it('default theme: Nuxt UI tokens in light and sakura primary in dark', () => {
    const out = css()
    const light = block(out, 'html:root:not(.dark){')
    const dark = block(out, 'html:root.dark{')
    expect(light.startsWith(`html:root:not(.dark){--ui-color-primary-50:${paletteColor('sakura', 50)};`)).toBe(true)
    expect(light).toContain('--ui-primary:var(--ui-color-primary-500);')
    // The default night theme is visibly sakura even when the daytime primary is neutral.
    expect(dark).toContain(`--ui-color-neutral-950:${paletteColor('neutral', 950)};`)
    expect(dark).toContain(`--ui-color-primary-400:${paletteColor('sakura', 400)};`)
    for (const token of ['--ui-radius', '--font-sans', 'font-family']) {
      expect(out).not.toContain(token)
    }
    expectNoFontDownload(out)
  })

  it('applies a chromatic primary to both modes: Nuxt UI, the legacy --c-primary scale and the console tokens', () => {
    const out = buildSiteThemeCss({ ...DEFAULT_SITE_THEME, theme_primary: 'sakura', theme_neutral: 'zinc' })
    const light = block(out, 'html:root:not(.dark){')
    const dark = block(out, 'html:root.dark{')
    expect(light).toContain('--ui-primary:var(--ui-color-primary-500);')
    expect(dark).toContain('--ui-primary:var(--ui-color-primary-400);')
    for (const rule of [light, dark]) {
      expect(rule).toContain(`--ui-color-primary-400:${paletteColor('sakura', 400)};`)
      expect(rule).toContain(`--ui-color-neutral-500:${paletteColor('zinc', 500)};`)
      expect(rule).toContain(`--c-primary-600:${paletteRgbChannels('sakura', 600)};`)
    }
    expect(block(out, 'html:root:not(.dark) :is(.console-signal,')).toContain(`--signal-accent:${paletteHex('sakura', 600)};`)
    expect(block(out, 'html:root.dark :is(.console-signal,')).toContain(`--signal-accent:${paletteHex('sakura', 400)};`)
  })

  it('writes radius and a font stack only when they differ from the default, without downloading fonts', () => {
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_radius: 0, theme_font: 'inter' })
    expect(css()).toContain('--ui-radius:0rem;')
    expect(css()).toContain("--font-sans:'Inter', system-ui")
    expect(css()).toContain("'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif;")
    expect(css()).toContain('html:root,html:root :is(.console-signal,')
    expectNoFontDownload(css())

    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_font: 'noto-sans-sc' })
    expect(document.querySelectorAll(`#${SITE_THEME_STYLE_ID}`)).toHaveLength(1)
    expect(css()).toContain("--font-sans:'Noto Sans SC', system-ui")
    expectNoFontDownload(css())

    applySiteTheme(DEFAULT_SITE_THEME)
    expect(css()).not.toContain('--font-sans')
  })

  it('self-hosts the latin Public Sans and Inter faces the font stacks name, from bundled packages only', () => {
    const fonts = readFileSync(resolve(process.cwd(), 'src/styles/fonts.css'), 'utf8')
    const references = [...fonts.matchAll(/(?:@import\s+|url\()\s*['"]?([^'")\s]+)/g)].map((match) => match[1])
    expect(references.length).toBeGreaterThan(0)
    for (const reference of references) {
      expect(reference).toMatch(/^@fontsource(-variable)?\/[\w-]+\/(files\/)?[\w-]*latin-[\w-]+\.(css|woff2)$/)
    }
    expect(fonts).not.toMatch(/https?:|\/\//)
    expect(fonts).toContain("font-family: 'Inter';")
    expect(fonts).toContain('@fontsource/public-sans/latin-400.css')
    expect(THEME_FONT_STACKS.inter?.startsWith("'Inter', ")).toBe(true)
    expect(THEME_FONT_STACKS['public-sans']?.startsWith("'Public Sans', ")).toBe(true)
    for (const stack of [THEME_FONT_STACKS.inter, THEME_FONT_STACKS['public-sans']]) {
      expect(stack).toContain("'PingFang SC'")
      expect(stack).toContain("'Microsoft YaHei'")
    }
  })

  it('never writes a value outside the allowlist', () => {
    const hostile = {
      theme_primary: 'red;}body{display:none',
      theme_neutral: 'url(javascript:alert(1))',
      theme_radius: '0.5',
      theme_font: 'https://evil.example/font.css',
      theme_default_mode: 'dark;color:red'
    }
    expect(normalizeSiteTheme(hostile)).toEqual(DEFAULT_SITE_THEME)
    expect(normalizeSiteTheme({ theme_radius: 99 }).theme_radius).toBe(0.375)
    expect(normalizeSiteTheme(null)).toEqual(DEFAULT_SITE_THEME)
    expect(normalizeSiteTheme('green')).toEqual(DEFAULT_SITE_THEME)
    applySiteTheme(hostile)
    const out = css()
    for (const fragment of ['display:none', 'javascript', 'evil.example', 'color:red']) {
      expect(out).not.toContain(fragment)
    }
    expectNoFontDownload(out)
  })

  it('previews without changing the remembered theme, and restore puts it back', () => {
    const { theme } = useSiteTheme()
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_primary: 'green' })
    previewSiteTheme({ ...DEFAULT_SITE_THEME, theme_primary: 'violet' })
    expect(css()).toContain(paletteColor('violet', 500))
    expect(theme.value.theme_primary).toBe('green')
    restoreSiteTheme()
    expect(css()).toContain(paletteColor('green', 500))
    expect(css()).not.toContain(paletteColor('violet', 500))
  })

  it('applies the default mode at startup unless the visitor chose light or dark', () => {
    prefersDark(false)
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_default_mode: 'dark' }, { colorMode: true })
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_default_mode: 'system' }, { colorMode: true })
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    localStorage.setItem('theme', 'light')
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_default_mode: 'dark' }, { colorMode: true })
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_default_mode: 'dark' })
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    previewSiteThemeMode('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    previewSiteThemeMode('nonsense')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('lets a visitor choose light, dark or system; system follows the OS over the site default', () => {
    prefersDark(false)
    const mode = useSiteColorMode()
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_default_mode: 'dark' }, { colorMode: true })
    expect(mode.preference.value).toBe('dark')
    expect(mode.isDark.value).toBe(true)

    mode.setPreference('system')
    expect(localStorage.getItem('theme')).toBe('system')
    expect(mode.preference.value).toBe('system')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(mode.isDark.value).toBe(false)
    // A reload keeps following the OS, not the dark site default.
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_default_mode: 'dark' }, { colorMode: true })
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    mode.setPreference('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(mode.isDark.value).toBe(true)
  })

  it('toggles from the mode actually on <html> and mirrors class changes made elsewhere', async () => {
    const mode = useSiteColorMode()
    document.documentElement.classList.add('dark')
    await Promise.resolve()
    expect(mode.isDark.value).toBe(true)

    toggleColorMode()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    expect(mode.isDark.value).toBe(false)

    // Another page's own toggle writes the class and localStorage directly.
    document.documentElement.classList.add('dark')
    localStorage.setItem('theme', 'dark')
    await Promise.resolve()
    expect(mode.isDark.value).toBe(true)
    expect(useSiteColorMode().preference.value).toBe('dark')
  })
})
