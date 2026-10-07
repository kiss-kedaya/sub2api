import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AxiosError, type AxiosInstance } from 'axios'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))

describe('plugin raw config response', () => {
  let client: AxiosInstance
  let plugins: typeof import('@/api/admin/plugins')
  beforeEach(async () => {
    vi.resetModules()
    localStorage.clear()
    client = (await import('@/api/client')).apiClient
    plugins = await import('@/api/admin/plugins')
  })

  it.each([{ code: 0, data: 'real config' }, { code: 7, message: 'real config' }, { ordinary: true }])('preserves config fields: %j', async (config) => {
    localStorage.setItem('auth_token', 'local-test-token')
    const adapter = vi.fn(async (request) => ({ data: JSON.stringify(config), status: 200, statusText: 'OK', headers: {}, config: request }))
    client.defaults.adapter = adapter
    expect(await plugins.getConfig(1)).toEqual(config)
    expect(await plugins.saveConfig(1, config)).toEqual(config)
    expect(adapter.mock.calls[0][0].headers.get('Authorization')).toBe('Bearer local-test-token')
  })

  it('preserves the step-up error contract', async () => {
    client.defaults.adapter = async (config) => {
      throw new AxiosError('step-up required', 'ERR_BAD_REQUEST', config, undefined, {
        status: 403, statusText: 'Forbidden', headers: {}, config,
        data: JSON.stringify({ code: 403, reason: 'STEP_UP_REQUIRED', message: 'Verify first' })
      })
    }
    await expect(plugins.saveConfig(1, { code: 1 })).rejects.toMatchObject({ status: 403, reason: 'STEP_UP_REQUIRED', message: 'Verify first' })
  })
})
