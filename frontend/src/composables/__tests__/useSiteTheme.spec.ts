import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_SITE_THEME,
  SITE_THEME_FONT_LINK_ID,
  SITE_THEME_STYLE_ID,
  THEME_FONT_STYLESHEETS,
  applySiteTheme,
  buildSiteThemeCss,
  normalizeSiteTheme,
  previewSiteTheme,
  previewSiteThemeMode,
  restoreSiteTheme,
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
    document.getElementById(SITE_THEME_FONT_LINK_ID)?.remove()
  })

  it('default theme: Nuxt UI tokens in light, no primary at all in dark, legacy tokens untouched', () => {
    const out = css()
    const light = block(out, 'html:root:not(.dark){')
    const dark = block(out, 'html:root.dark{')
    expect(light.startsWith(`html:root:not(.dark){--ui-color-primary-50:${paletteColor('neutral', 50)};`)).toBe(true)
    expect(light).toContain(`--ui-primary:${paletteColor('neutral', 900)};`)
    // Black keeps today's night theme: dark mode only gets the neutral scale.
    expect(dark).toContain(`--ui-color-neutral-950:${paletteColor('neutral', 950)};`)
    expect(dark).not.toContain('primary')
    for (const token of ['--c-primary-', '--signal-', '--ui-radius', '--font-sans', 'font-family']) {
      expect(out).not.toContain(token)
    }
    expect(document.getElementById(SITE_THEME_FONT_LINK_ID)).toBeNull()
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

  it('writes radius and font only when they differ from the default, and loads the web font', () => {
    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_radius: 0, theme_font: 'inter' })
    expect(css()).toContain('--ui-radius:0rem;')
    expect(css()).toContain("--font-sans:'Inter', system-ui")
    expect(css()).toContain('html:root,html:root :is(.console-signal,')
    const link = document.getElementById(SITE_THEME_FONT_LINK_ID) as HTMLLinkElement
    expect(link.rel).toBe('stylesheet')
    expect(link.getAttribute('href')).toBe(THEME_FONT_STYLESHEETS.inter)

    applySiteTheme({ ...DEFAULT_SITE_THEME, theme_font: 'noto-sans-sc' })
    expect(document.querySelectorAll(`#${SITE_THEME_STYLE_ID}`)).toHaveLength(1)
    expect(document.querySelectorAll(`#${SITE_THEME_FONT_LINK_ID}`)).toHaveLength(1)
    expect(document.getElementById(SITE_THEME_FONT_LINK_ID)?.getAttribute('href')).toBe(THEME_FONT_STYLESHEETS['noto-sans-sc'])

    applySiteTheme(DEFAULT_SITE_THEME)
    expect(document.getElementById(SITE_THEME_FONT_LINK_ID)).toBeNull()
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
    expect(document.getElementById(SITE_THEME_FONT_LINK_ID)).toBeNull()
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
})
