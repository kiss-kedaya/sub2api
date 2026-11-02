import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const { getSettings, getPaymentConfig } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  getPaymentConfig: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    settings: { getSettings },
    payment: { getConfig: getPaymentConfig },
  },
}))

import { useAdminSettingsStore } from '@/stores/adminSettings'

function settingsPayload(overrides: Record<string, unknown> = {}) {
  return {
    ops_monitoring_enabled: true,
    ops_realtime_monitoring_enabled: true,
    ops_query_mode_default: 'auto',
    custom_menu_items: [],
    ...overrides,
  }
}

describe('useAdminSettingsStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    getSettings.mockReset()
    getPaymentConfig.mockReset()
    getPaymentConfig.mockResolvedValue({ data: { enabled: true } })
  })

  it('fetch 并发调用只发一次请求', async () => {
    let resolveSettings!: (value: unknown) => void
    getSettings.mockImplementation(() => new Promise((resolve) => { resolveSettings = resolve }))

    const store = useAdminSettingsStore()
    const first = store.fetch()
    const second = store.fetch()

    await Promise.resolve()
    expect(getSettings).toHaveBeenCalledTimes(1)

    resolveSettings(settingsPayload({ ops_query_mode_default: 'native' }))
    await Promise.all([first, second])

    expect(store.opsQueryModeDefault).toBe('native')
    expect(getSettings).toHaveBeenCalledTimes(1)
  })

  it('加载完成后再 fetch 不再回源，force 才重新请求', async () => {
    getSettings.mockResolvedValue(settingsPayload())
    const store = useAdminSettingsStore()

    await store.fetch()
    await store.fetch()
    expect(getSettings).toHaveBeenCalledTimes(1)

    await store.fetch(true)
    expect(getSettings).toHaveBeenCalledTimes(2)
  })

  it('patchFromSettings 直接写入状态与本地缓存，不再发请求', async () => {
    getSettings.mockResolvedValue(settingsPayload({ ops_monitoring_enabled: false, ops_query_mode_default: 'native' }))
    const store = useAdminSettingsStore()
    await store.fetch()
    const callsAfterLoad = getSettings.mock.calls.length

    store.patchFromSettings({
      ops_monitoring_enabled: true,
      ops_query_mode_default: 'auto',
      custom_menu_items: [{ id: 1, title: 'Menu', visibility: 'user' }],
    } as never)

    expect(store.opsMonitoringEnabled).toBe(true)
    expect(store.opsQueryModeDefault).toBe('auto')
    expect(store.customMenuItems).toEqual([{ id: 1, title: 'Menu', visibility: 'user' }])
    expect(localStorage.getItem('ops_monitoring_enabled_cached')).toBe('true')
    expect(localStorage.getItem('ops_query_mode_default_cached')).toBe('auto')
    expect(getSettings.mock.calls.length).toBe(callsAfterLoad)
  })

  it('patchFromSettings 不认识的字段保持原值', async () => {
    getSettings.mockResolvedValue(settingsPayload({ ops_monitoring_enabled: true }))
    const store = useAdminSettingsStore()
    await store.fetch()

    store.patchFromSettings({ theme_primary: 'sakura' } as never)

    expect(store.opsMonitoringEnabled).toBe(true)
    expect(store.customMenuItems).toEqual([])
  })
})
