/**
 * Step-up error copy must never leak the backend's English reason.
 *
 * The backend returns 403 with English messages such as
 * "This operation requires recent two-factor verification" for
 * STEP_UP_REQUIRED / STEP_UP_TOTP_NOT_ENABLED / STEP_UP_ADMIN_API_KEY_FORBIDDEN /
 * STEP_UP_UNAVAILABLE. Every page funnels errors through these helpers, so the
 * shared copy lives here instead of being re-implemented per page.
 */
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import zhCommon from '@/i18n/locales/zh/common'
import enCommon from '@/i18n/locales/en/common'
import {
  extractApiErrorMessage,
  extractI18nErrorMessage,
  localizeStepUpErrorCode,
} from '../apiError'
import { StepUpCancelledError } from '@/composables/useStepUp'

const originalLocale = i18n.global.locale.value

beforeAll(() => {
  i18n.global.setLocaleMessage('zh', zhCommon)
  i18n.global.setLocaleMessage('en', enCommon)
  i18n.global.locale.value = 'en'
})

afterAll(() => {
  i18n.global.locale.value = originalLocale
})

describe('step-up error localization', () => {
  it.each([
    ['STEP_UP_REQUIRED', enCommon.stepUp.required],
    ['STEP_UP_TOTP_NOT_ENABLED', enCommon.stepUp.notEnabled],
    ['STEP_UP_ADMIN_API_KEY_FORBIDDEN', enCommon.stepUp.adminApiKeyForbidden],
    ['STEP_UP_UNAVAILABLE', enCommon.stepUp.unavailable],
  ])('maps %s to localized copy', (code, expected) => {
    const backendEnglish = 'This operation requires recent two-factor verification'
    const err = { status: 403, code, reason: code, message: backendEnglish }
    const t = (key: string) => key
    expect(localizeStepUpErrorCode(code)).toBe(expected)
    expect(extractApiErrorMessage(err)).toBe(expected)
    expect(extractI18nErrorMessage(err, t, 'payment.errors', 'fallback')).toBe(expected)
    expect(extractApiErrorMessage(err)).not.toBe(backendEnglish)
  })

  it('localizes the cancellation sentinel thrown after dismissing the TOTP dialog', () => {
    const cancelled = new StepUpCancelledError()
    expect(extractApiErrorMessage(cancelled)).toBe(enCommon.stepUp.cancelled)
    expect(localizeStepUpErrorCode(cancelled.code)).toBe(enCommon.stepUp.cancelled)
  })

  it('keeps non-step-up errors on the existing path', () => {
    expect(localizeStepUpErrorCode('PAYMENT_PROVIDER_MISCONFIGURED')).toBeNull()
    expect(extractApiErrorMessage({ status: 500, message: 'boom' })).toBe('boom')
    expect(extractI18nErrorMessage({ status: 500, message: 'boom' }, (key) => key, 'x', 'fallback')).toBe(
      'boom',
    )
  })
})
