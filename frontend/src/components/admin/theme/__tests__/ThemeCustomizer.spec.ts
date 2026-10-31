import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ThemeCustomizer from '../ThemeCustomizer.vue'
import { DEFAULT_SITE_THEME, SITE_THEME_STYLE_ID, applySiteTheme } from '@/composables/useSiteTheme'
import { paletteColor } from '@/theme/palettes'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'

const { getTheme, updateTheme, showSuccess } = vi.hoisted(() => ({ getTheme: vi.fn(), updateTheme: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/siteTheme', () => ({ getSiteTheme: getTheme, updateSiteTheme: updateTheme }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('vue-i18n', async (original) => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))

const stored = { theme_primary: 'green', theme_neutral: 'zinc', theme_radius: 0.25, theme_font: 'inter', theme_default_mode: 'light' }

function css(): string {
  return document.getElementById(SITE_THEME_STYLE_ID)?.textContent ?? ''
}

async function render() {
  const wrapper = mount(ThemeCustomizer)
  await flushPromises()
  return wrapper
}

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('ThemeCustomizer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    applySiteTheme(DEFAULT_SITE_THEME)
    getTheme.mockResolvedValue({ ...stored })
    updateTheme.mockImplementation(async (value) => value)
  })

  afterEach(() => {
    document.getElementById(SITE_THEME_STYLE_ID)?.remove()
  })

  it('renders every option row with the stored values and labels present in both locales', async () => {
    const wrapper = await render()
    expect(wrapper.get('[data-testid="site-theme-row-theme_primary"] [data-value="sakura"]').text()).toBe('admin.settings.siteTheme.colors.sakura')
    const counts = { theme_primary: 19, theme_neutral: 5, theme_font: 4, theme_radius: 5, theme_default_mode: 3 }
    const labelKeys = new Set<string>()
    for (const [field, count] of Object.entries(counts)) {
      const row = wrapper.get(`[data-testid="site-theme-row-${field}"]`)
      if (field !== 'theme_primary') await row.get('button[aria-expanded]').trigger('click')
      const options = row.findAll('[role="radio"]')
      expect(options, field).toHaveLength(count)
      const checked = options.filter((option) => option.attributes('aria-checked') === 'true')
      expect(checked.map((option) => option.attributes('data-value'))).toEqual([String(stored[field as keyof typeof stored])])
      options.forEach((option) => labelKeys.add(option.text()))
    }
    expect(labelKeys.size).toBeGreaterThan(30)
    for (const key of [...labelKeys].filter((label) => label.startsWith('admin.'))) {
      expect(typeof lookup(en, key), `en ${key}`).toBe('string')
      expect(typeof lookup(zh, key), `zh ${key}`).toBe('string')
    }
    expect(wrapper.get('[data-testid="site-theme-save"]').attributes('disabled')).toBeDefined()
  })

  it('previews a selection live and saves the whole draft', async () => {
    const wrapper = await render()
    expect(css()).toContain(paletteColor('green', 500))
    await wrapper.get('[data-value="sakura"]').trigger('click')
    expect(css()).toContain(`--ui-color-primary-400:${paletteColor('sakura', 400)};`)
    expect(wrapper.get('[data-value="sakura"]').attributes('aria-checked')).toBe('true')

    await wrapper.get('[data-testid="site-theme-save"]').trigger('click')
    await flushPromises()
    const theme = { ...stored, theme_primary: 'sakura' }
    expect(updateTheme).toHaveBeenCalledWith(theme)
    expect(wrapper.emitted('save')?.[0]).toEqual([theme])
    expect(showSuccess).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-testid="site-theme-save"]').attributes('disabled')).toBeDefined()
  })

  it('reset only changes the draft until saved', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="site-theme-reset"]').trigger('click')
    expect(updateTheme).not.toHaveBeenCalled()
    expect(wrapper.get('[data-value="black"]').attributes('aria-checked')).toBe('true')
    expect(css()).toContain(`--ui-color-neutral-500:${paletteColor('neutral', 500)};`)
    await wrapper.get('[data-testid="site-theme-save"]').trigger('click')
    await flushPromises()
    expect(updateTheme).toHaveBeenCalledWith({ ...DEFAULT_SITE_THEME })
  })

  it('cannot save after a failed load', async () => {
    getTheme.mockRejectedValueOnce(new Error('load failed'))
    const wrapper = await render()
    expect(wrapper.get('[role="alert"]').text()).toContain('load failed')
    expect(wrapper.get('[data-testid="site-theme-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-value="sakura"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="site-theme-save"]').trigger('click')
    expect(updateTheme).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="site-theme-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('previews the default mode and puts the page back on unmount without saving', async () => {
    localStorage.setItem('theme', 'light')
    const wrapper = await render()
    const mode = wrapper.get('[data-testid="site-theme-row-theme_default_mode"]')
    await mode.get('button[aria-expanded]').trigger('click')
    await mode.get('[data-value="dark"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    await wrapper.get('[data-testid="site-theme-row-theme_primary"] button[aria-expanded]').trigger('click')
    await wrapper.get('[data-value="violet"]').trigger('click')
    expect(css()).toContain(paletteColor('violet', 500))
    wrapper.unmount()
    expect(css()).not.toContain(paletteColor('violet', 500))
    expect(css()).not.toContain(paletteColor('green', 500))
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(updateTheme).not.toHaveBeenCalled()
  })
})
