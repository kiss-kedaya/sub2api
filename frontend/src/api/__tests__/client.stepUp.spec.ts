/**
 * Behavioural contract for the global step-up gate in the shared Axios client.
 *
 * Regression: a page that forgot `useStepUp` used to toast the backend's English
 * "This operation requires recent two-factor verification". The client now:
 *   - opens the app-wide TOTP gate on 403 STEP_UP_REQUIRED,
 *   - retries the original request exactly once after verification,
 *   - shares one prompt for concurrent sensitive calls (batch saves),
 *   - rejects with StepUpCancelledError when the operator cancels,
 *   - localizes non-retryable STEP_UP_* codes.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { i18n } from '@/i18n'
import enCommon from '@/i18n/locales/en/common'
import {
  isStepUpHostAvailable,
  registerStepUpHost,
  resetStepUpGateForTests,
  stepUpGate,
} from '@/composables/useStepUpGate'
import { isStepUpCancelled } from '@/utils/stepUpErrors'
import { getBatchTodayStats, getById } from '@/api/admin/accounts'
import { getAll as getAllProxies } from '@/api/admin/proxies'

const originalLocale = i18n.global.locale.value

function stepUpError(code = 'STEP_UP_REQUIRED', message = 'This operation requires recent two-factor verification') {
  return {
    response: { status: 403, data: { code, message } },
    code: 'ERR_BAD_REQUEST',
    isAxiosError: true,
  }
}

function okResponse(data: unknown = { ok: true }) {
  return {
    status: 200,
    statusText: 'OK',
    headers: {},
    config: { url: '/admin/payment/providers/7', method: 'put', headers: {} },
    data: { code: 0, message: 'ok', data },
  }
}

let releaseHost: (() => void) | null = null

beforeEach(() => {
  vi.restoreAllMocks()
  resetStepUpGateForTests()
  releaseHost = registerStepUpHost()
  // Load the real locale bundle so localization assertions are meaningful.
  i18n.global.setLocaleMessage('en', enCommon)
  i18n.global.locale.value = 'en'
})

afterEach(() => {
  releaseHost?.()
  releaseHost = null
  resetStepUpGateForTests()
  i18n.global.locale.value = originalLocale
})

describe('shared client step-up gate', () => {
  it('prompts once and retries the original admin request after verification', async () => {
    let calls = 0
    const adapter = vi.fn(async (config: any) => {
      calls += 1
      if (calls === 1) throw { ...stepUpError(), config }
      return okResponse()
    })
    apiClient.defaults.adapter = adapter

    const pending = apiClient.put('/admin/payment/providers/7', { name: 'x' })
    await vi.waitFor(() => expect(stepUpGate.visible.value).toBe(true))
    stepUpGate.onVerified()

    const response = await pending
    expect(response.data).toEqual({ ok: true })
    expect(adapter).toHaveBeenCalledTimes(2)
    // Retry re-sends the same payload (the 403 happened before the handler ran).
    expect(adapter.mock.calls[1][0].data).toBe(adapter.mock.calls[0][0].data)
    expect(adapter.mock.calls[0][0].data).toBeTruthy()
  })

  // 绑定 TOTP 与 Passkey 的账号：403 的 metadata 声明「任选其一」（step_up_mode=any）。
  // 客户端照常按 STEP_UP_REQUIRED 处理：弹一次窗，验证任意一个因子后只重试一次。
  it('treats the any-one-factor challenge (step_up_mode metadata) like any STEP_UP_REQUIRED: prompts once, retries once', async () => {
    const challenge = { step_up_mode: 'any', factors: 'totp,passkey' }
    let calls = 0
    const adapter = vi.fn(async (config: any) => {
      calls += 1
      if (calls === 1) {
        throw {
          response: { status: 403, data: { code: 'STEP_UP_REQUIRED', message: 'verify', metadata: challenge } },
          code: 'ERR_BAD_REQUEST',
          isAxiosError: true,
          config,
        }
      }
      return okResponse()
    })
    apiClient.defaults.adapter = adapter

    const pending = apiClient.put('/admin/payment/providers/7', { name: 'x' })
    await vi.waitFor(() => expect(stepUpGate.visible.value).toBe(true))
    // 弹窗里验证了其中一个因子就会走到这里（不再要求另一个因子）。
    stepUpGate.onVerified()

    await expect(pending).resolves.toMatchObject({ data: { ok: true } })
    expect(adapter).toHaveBeenCalledTimes(2)
  })

  it('hands the challenge metadata to the caller when the retry is still rejected (no endless prompts)', async () => {
    const challenge = { step_up_mode: 'any', factors: 'totp,passkey' }
    const adapter = vi.fn(async (config: any) => {
      throw {
        response: { status: 403, data: { code: 'STEP_UP_REQUIRED', message: 'verify', metadata: challenge } },
        code: 'ERR_BAD_REQUEST',
        isAxiosError: true,
        config,
      }
    })
    apiClient.defaults.adapter = adapter

    const pending = apiClient.put('/admin/payment/providers/7', { name: 'x' })
    const rejection = expect(pending).rejects.toMatchObject({ code: 'STEP_UP_REQUIRED', metadata: challenge })
    await vi.waitFor(() => expect(stepUpGate.visible.value).toBe(true))
    stepUpGate.onVerified()

    await rejection
    expect(adapter).toHaveBeenCalledTimes(2)
    expect(stepUpGate.visible.value).toBe(false)
  })

  it('shares a single prompt for concurrent sensitive calls (batch save)', async () => {
    const rejections: Array<() => void> = []
    const successes: Array<() => void> = []
    const adapter = vi.fn((config: any) => {
      if (rejections.length < 2) {
        return new Promise((_resolve, reject) => {
          rejections.push(() => reject({ ...stepUpError(), config }))
        })
      }
      return new Promise((resolve) => {
        successes.push(() => resolve(okResponse()))
      })
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    const batch = Promise.all([
      apiClient.put('/admin/payment/providers/1', { sort_order: 1 }),
      apiClient.put('/admin/payment/providers/2', { sort_order: 2 }),
    ])
    await vi.waitFor(() => expect(rejections).toHaveLength(2))
    // Both 403s land in the same tick: only one TOTP dialog may be requested.
    rejections.forEach((reject) => reject())
    await vi.waitFor(() => expect(promptSpy).toHaveBeenCalledTimes(2))
    // Single-flight: both callers received the exact same pending prompt, so the
    // operator sees one dialog and one verification unlocks both retries.
    const [first, second] = promptSpy.mock.results.map((result) => result.value)
    expect(first).toBe(second)
    expect(stepUpGate.visible.value).toBe(true)

    stepUpGate.onVerified()
    await vi.waitFor(() => expect(successes).toHaveLength(2))
    successes.forEach((resolve) => resolve())

    const responses = await batch
    expect(responses).toHaveLength(2)
    expect(adapter).toHaveBeenCalledTimes(4)
  })

  it('degrades quietly when the prompt is dismissed: localized plain rejection, no StepUpCancelledError', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config }
    })
    apiClient.defaults.adapter = adapter

    const pending = apiClient.put('/admin/payment/providers/7', { name: 'x' })
    await vi.waitFor(() => expect(stepUpGate.visible.value).toBe(true))
    stepUpGate.onCancel()

    const err = await pending.then(
      () => { throw new Error('expected rejection') },
      (rejection) => rejection,
    )
    expect(err).toMatchObject({
      status: 403,
      code: 'STEP_UP_CANCELLED',
      cancelled: true,
      message: enCommon.stepUp.cancelled,
    })
    // 不是 Error 实例：控制台不会出现 StepUpCancelledError 堆栈。
    expect(err).not.toBeInstanceOf(Error)
    // 页面既有的「取消」处理仍能识别它（统一从 "未执行" 分支降级）。
    expect(isStepUpCancelled(err)).toBe(true)
    expect(adapter).toHaveBeenCalledTimes(1)
  })

  it('rejects every concurrent call of one cancelled prompt with the same quiet code', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config }
    })
    apiClient.defaults.adapter = adapter

    const first = apiClient.put('/admin/payment/providers/1', {})
    const second = apiClient.put('/admin/payment/providers/2', {})
    await vi.waitFor(() => expect(stepUpGate.visible.value).toBe(true))
    stepUpGate.onCancel()

    const results = await Promise.allSettled([first, second])
    expect(
      results.every(
        (result) => result.status === 'rejected' && (result.reason as any).code === 'STEP_UP_CANCELLED',
      ),
    ).toBe(true)
  })

  it('never opens the TOTP dialog for a silent background read (explicit silentStepUp marker)', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config }
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    await expect(
      apiClient.post('/admin/accounts/usage/batch', { account_ids: [30772] }, { silentStepUp: true }),
    ).rejects.toMatchObject({
      status: 403,
      code: 'STEP_UP_REQUIRED',
      silentStepUp: true,
      message: enCommon.stepUp.required,
    })
    expect(promptSpy).not.toHaveBeenCalled()
  })

  it('keeps page-load accounts/proxies reads silent end-to-end (API layer marker)', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config }
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    for (const load of [
      () => getById(30772),
      () => getBatchTodayStats([30772]),
      () => getAllProxies(),
    ]) {
      await expect(load()).rejects.toMatchObject({ code: 'STEP_UP_REQUIRED', silentStepUp: true })
    }
    expect(promptSpy).not.toHaveBeenCalled()
  })

  it('localizes non-retryable step-up codes instead of toasting the backend English', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw {
        ...stepUpError(
          'STEP_UP_TOTP_NOT_ENABLED',
          'This operation requires two-factor authentication; please enable TOTP first',
        ),
        config,
      }
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    await expect(apiClient.put('/admin/payment/providers/7', {})).rejects.toMatchObject({
      status: 403,
      code: 'STEP_UP_TOTP_NOT_ENABLED',
      message: enCommon.stepUp.notEnabled,
    })
    expect(promptSpy).not.toHaveBeenCalled()
  })

  it('does not prompt on the TOTP verification endpoint itself (no self-deadlock)', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config: { ...config, url: '/user/totp/step-up' } }
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    await expect(apiClient.post('/user/totp/step-up', { code: '123456' })).rejects.toMatchObject({
      code: 'STEP_UP_REQUIRED',
    })
    expect(promptSpy).not.toHaveBeenCalled()
    expect(adapter).toHaveBeenCalledTimes(1)
  })

  it('retries only once when the grant still does not unlock the endpoint', async () => {
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config }
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    const pending = apiClient.put('/admin/payment/providers/7', {})
    await vi.waitFor(() => expect(stepUpGate.visible.value).toBe(true))
    stepUpGate.onVerified()

    await expect(pending).rejects.toMatchObject({ code: 'STEP_UP_REQUIRED' })
    expect(promptSpy).toHaveBeenCalledTimes(1)
    expect(adapter).toHaveBeenCalledTimes(2)
  })

  it('rejects without hanging when no TOTP dialog host is mounted (SSR/pre-shell)', async () => {
    releaseHost?.()
    releaseHost = null
    expect(isStepUpHostAvailable()).toBe(false)
    const adapter = vi.fn(async (config: any) => {
      throw { ...stepUpError(), config }
    })
    apiClient.defaults.adapter = adapter
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')

    await expect(apiClient.put('/admin/payment/providers/7', {})).rejects.toMatchObject({
      code: 'STEP_UP_REQUIRED',
      message: enCommon.stepUp.required,
    })
    expect(promptSpy).not.toHaveBeenCalled()
  })
})
