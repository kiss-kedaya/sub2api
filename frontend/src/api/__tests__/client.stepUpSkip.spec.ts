/**
 * The backend gate marks every sensitive request that skipped the TOTP prompt
 * because the 5-minute step-up window was still valid:
 *   X-StepUp-Skipped: 1
 *   X-StepUp-Remaining: <seconds>
 * The shared client must feed those headers into the global notice store; a
 * silent skip is a security-UX regression.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import {
  resetStepUpSkipForTests,
  stepUpSkipRemainingSeconds,
  stepUpSkipVisible,
} from '@/composables/useStepUpSkip'

function okResponse(headers: Record<string, string>) {
  return {
    status: 200,
    statusText: 'OK',
    headers,
    config: { url: '/admin/users/7', method: 'delete', headers: {} },
    data: { code: 0, message: 'ok', data: { deleted: true } },
  }
}

describe('client step-up skip headers', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetStepUpSkipForTests()
  })

  afterEach(() => {
    resetStepUpSkipForTests()
    vi.useRealTimers()
  })

  it('records the remaining grace window reported by the backend', async () => {
    apiClient.defaults.adapter = vi.fn(async () =>
      okResponse({ 'x-stepup-skipped': '1', 'x-stepup-remaining': '272' }),
    )

    const response = await apiClient.delete('/admin/users/7')

    expect(response.data).toEqual({ deleted: true })
    expect(stepUpSkipVisible.value).toBe(true)
    expect(stepUpSkipRemainingSeconds.value).toBe(272)
  })

  it('accepts a header container that only implements get()', async () => {
    const headers = new Map<string, string>([
      ['x-stepup-skipped', '1'],
      ['x-stepup-remaining', '95'],
    ])
    apiClient.defaults.adapter = vi.fn(async () => okResponse(headers as unknown as Record<string, string>))

    await apiClient.get('/admin/users/7/api-keys')

    expect(stepUpSkipVisible.value).toBe(true)
    expect(stepUpSkipRemainingSeconds.value).toBe(95)
  })

  it('stays hidden for responses that skipped no 2FA', async () => {
    apiClient.defaults.adapter = vi.fn(async () => okResponse({}))

    await apiClient.get('/admin/users')

    expect(stepUpSkipVisible.value).toBe(false)
  })

  it('stays hidden when the backend omits the remaining seconds', async () => {
    apiClient.defaults.adapter = vi.fn(async () => okResponse({ 'x-stepup-skipped': '1' }))

    await apiClient.delete('/admin/users/7')

    expect(stepUpSkipVisible.value).toBe(false)
  })
})
