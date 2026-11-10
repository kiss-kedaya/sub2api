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
  syncThemeColor,
  toggleColorMode,
  useSiteColorMode,
  useSiteTheme
} from '../useSiteTheme'
import { paletteColor, paletteHex, paletteRgbChannels } from '@/theme/palettes'
import { THEME_SWITCHING_ATTR } from '@/theme/switching'

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

  it('keeps night text readable across distinct raised surfaces without overriding custom accents', () => {
    const stylesheet = readFileSync(resolve(process.cwd(), 'src/styles/nuxt-ui-theme.css'), 'utf8')
    const dark = block(stylesheet, '.dark {')
    const hex = (name: string) => {
      const value = dark.match(new RegExp(`${name}:\\s*(#[0-9a-f]{6})`, 'i'))?.[1]
      if (!value) throw new Error(`missing dark token ${name}`)
      return value
    }
    const luminance = (value: string) => {
      const channels = [1, 3, 5].map(index => {
        const channel = Number.parseInt(value.slice(index, index + 2), 16) / 255
        return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
      })
      return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
    }
    const surfaces = ['--ui-bg', '--ui-bg-muted', '--ui-bg-elevated', '--ui-bg-accented'].map(hex)
    for (let index = 1; index < surfaces.length; index += 1) {
      expect(luminance(surfaces[index])).toBeGreaterThan(luminance(surfaces[index - 1]))
    }
    for (const foreground of ['--ui-text-dimmed', '--ui-text-muted', '--ui-text-toned', '--ui-text', '--ui-text-highlighted']) {
      for (const background of [...surfaces, hex('--app-page-bg'), hex('--app-sidebar-bg')]) {
        expect((luminance(hex(foreground)) + 0.05) / (luminance(background) + 0.05), `${foreground} on ${background}`).toBeGreaterThanOrEqual(4.5)
      }
    }
    expect(luminance(hex('--app-sidebar-bg'))).toBeGreaterThan(0.01)
    expect(hex('--ui-primary')).toBe('#f2b6c6')
    expect(buildSiteThemeCss({ ...DEFAULT_SITE_THEME, theme_primary: 'green' })).toContain(`--ui-color-primary-400:${paletteColor('green', 400)};`)
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

describe('theme-color meta', () => {
  const root = document.documentElement
  const tags = () => Array.from(document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'))
  // jsdom loads no stylesheet, so --app-page-bg resolves to '' and the built-in fallbacks apply.
  const LIGHT = '#ffffff'
  const DARK = '#1b181f'

  // Earlier suites leave the module's class observer on <html>. Removing `.dark` queues one more
  // sync, which would re-create the tag after it was cleared, so let that run before clearing.
  const settleClassObserver = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

  beforeEach(async () => {
    localStorage.clear()
    root.classList.remove('dark')
    await settleClassObserver()
    tags().forEach((tag) => tag.remove())
  })

  afterEach(async () => {
    root.classList.remove('dark')
    await settleClassObserver()
    tags().forEach((tag) => tag.remove())
    vi.restoreAllMocks()
  })

  it('creates the tag when the page did not ship one', () => {
    expect(tags()).toHaveLength(0)
    syncThemeColor(false)
    expect(tags()).toHaveLength(1)
    expect(tags()[0].content).toBe(LIGHT)
  })

  it('rewrites both prefers-color-scheme variants to the one colour in force', () => {
    // index.html ships a light and a dark tag; the class, not the media query, decides what is on screen.
    for (const media of ['(prefers-color-scheme: light)', '(prefers-color-scheme: dark)']) {
      const tag = document.createElement('meta')
      tag.name = 'theme-color'
      tag.media = media
      tag.content = '#ff00ff'
      document.head.appendChild(tag)
    }
    syncThemeColor(true)
    expect(tags().map((tag) => tag.content)).toEqual([DARK, DARK])
    syncThemeColor(false)
    expect(tags().map((tag) => tag.content)).toEqual([LIGHT, LIGHT])
  })

  it('follows a day/night toggle', () => {
    const mode = useSiteColorMode()
    syncThemeColor(false)
    expect(tags()[0].content).toBe(LIGHT)

    toggleColorMode()
    expect(mode.isDark.value).toBe(true)
    expect(tags()[0].content).toBe(DARK)

    toggleColorMode()
    expect(mode.isDark.value).toBe(false)
    expect(tags()[0].content).toBe(LIGHT)
  })
})

describe('day/night switch with the View Transitions API', () => {
  type FakeTransition = { update: () => Promise<void>; finish: () => void; skip: () => void }
  type WithTransitions = { startViewTransition?: unknown }
  let transitions: FakeTransition[] = []
  const root = document.documentElement
  const doc = document as unknown as WithTransitions
  const marked = () => root.hasAttribute(THEME_SWITCHING_ATTR)
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

  function installViewTransitions() {
    doc.startViewTransition = vi.fn((update: () => Promise<void>) => {
      let finish!: () => void
      let abort!: (reason: unknown) => void
      const finished = new Promise<void>((resolve) => { finish = resolve })
      const ready = new Promise<void>((_, reject) => { abort = reject })
      transitions.push({ update, finish, skip: () => { abort(new DOMException('Transition was skipped', 'AbortError')); finish() } })
      return { ready, finished }
    })
  }

  // The global test mock answers `matches: true` to every query, which would read as "reduced motion".
  function reducedMotion(matches: boolean) {
    vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => ({ matches: query.includes('prefers-reduced-motion') && matches, media: query }) as MediaQueryList)
  }

  beforeEach(() => {
    localStorage.clear()
    root.classList.remove('dark', 'motion-reduce')
    transitions = []
    applySiteTheme(DEFAULT_SITE_THEME)
    reducedMotion(false)
    installViewTransitions()
  })

  afterEach(async () => {
    transitions.forEach((transition) => transition.finish())
    await settle()
    delete doc.startViewTransition
    root.classList.remove('dark', 'motion-reduce')
    root.removeAttribute(THEME_SWITCHING_ATTR)
    vi.restoreAllMocks()
    document.getElementById(SITE_THEME_STYLE_ID)?.remove()
  })
  it('cross-fades a visitor toggle: the class flips inside the transition and the marker clears when it ends', async () => {
    const mode = useSiteColorMode()
    toggleColorMode()
    expect(transitions).toHaveLength(1)
    expect(marked()).toBe(true)
    // A data attribute, not a class: the sakura canvas and the charts watch the class for the mode itself.
    expect(root.classList.contains(THEME_SWITCHING_ATTR)).toBe(false)
    expect(root.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('dark')

    await transitions[0].update()
    expect(root.classList.contains('dark')).toBe(true)
    expect(mode.isDark.value).toBe(true)

    transitions[0].finish()
    await settle()
    expect(marked()).toBe(false)
  })

  it('does not animate the mode applied at startup', () => {
    localStorage.setItem('theme', 'dark')
    applySiteTheme(DEFAULT_SITE_THEME, { colorMode: true })
    expect(root.classList.contains('dark')).toBe(true)
    expect(transitions).toHaveLength(0)
    expect(marked()).toBe(false)
  })

  it.each([
    ['prefers-reduced-motion', () => reducedMotion(true)],
    ['the motion-reduce class', () => root.classList.add('motion-reduce')]
  ])('falls back to a plain flip when the visitor asked for reduced motion (%s)', (_name, ask) => {
    ask()
    toggleColorMode()
    expect(transitions).toHaveLength(0)
    expect(marked()).toBe(false)
    expect(root.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')
  })

  it('falls back to a plain flip when the browser has no View Transitions or refuses to start one', () => {
    delete doc.startViewTransition
    toggleColorMode()
    expect(root.classList.contains('dark')).toBe(true)
    expect(marked()).toBe(false)

    doc.startViewTransition = vi.fn(() => { throw new Error('InvalidStateError') })
    toggleColorMode()
    expect(root.classList.contains('dark')).toBe(false)
    expect(marked()).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
  })

  it('two quick clicks flip twice instead of collapsing into one, and the marker stays until the last one ends', async () => {
    toggleColorMode()
    toggleColorMode()
    expect(transitions).toHaveLength(2)
    expect(localStorage.getItem('theme')).toBe('light')

    // The browser skips the first transition when the second starts; its update callback still runs.
    transitions[0].skip()
    await transitions[0].update()
    await transitions[1].update()
    expect(root.classList.contains('dark')).toBe(false)
    await settle()
    expect(marked()).toBe(true)

    transitions[1].finish()
    await settle()
    expect(marked()).toBe(false)
  })

  it('swallows the AbortError of a skipped transition', async () => {
    const unhandled = vi.fn()
    process.on('unhandledRejection', unhandled)
    try {
      toggleColorMode()
      transitions[0].skip()
      await settle()
      await settle()
      expect(unhandled).not.toHaveBeenCalled()
      expect(marked()).toBe(false)
    } finally {
      process.off('unhandledRejection', unhandled)
    }
  })

  it('cross-fades the admin live preview of the default mode, but restoring the saved theme does not animate', () => {
    previewSiteThemeMode('dark')
    expect(transitions).toHaveLength(1)
    restoreSiteTheme()
    expect(transitions).toHaveLength(1)
  })
})
