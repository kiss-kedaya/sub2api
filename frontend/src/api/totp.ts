/**
 * TOTP (2FA) API endpoints
 * Handles Two-Factor Authentication with Google Authenticator
 */

import { apiClient } from './client'
import type {
  TwoFactorPolicy,
  TotpStatus,
  TotpSetupRequest,
  TotpSetupResponse,
  TotpEnableRequest,
  TotpEnableResponse,
  TotpDisableRequest,
  TotpVerificationMethod
} from '@/types'

/**
 * Get TOTP status for current user
 * @returns TOTP status including enabled state and feature availability
 */
export async function getStatus(): Promise<TotpStatus> {
  const { data } = await apiClient.get<TotpStatus>('/user/totp/status')
  return data
}

/**
 * Get verification method for TOTP operations
 * @param operation - Optional operation; disable verifies the current TOTP for every user
 * @returns Email/password for setup, or current TOTP verification for disable
 */
export async function getVerificationMethod(operation?: 'enable' | 'disable'): Promise<TotpVerificationMethod> {
  const { data } = await apiClient.get<TotpVerificationMethod>('/user/totp/verification-method',
    operation ? { params: { operation } } : undefined)
  return data
}

/**
 * Send email verification code for TOTP operations
 * @returns Success response
 */
export async function sendVerifyCode(): Promise<{ success: boolean }> {
  const { data } = await apiClient.post<{ success: boolean }>('/user/totp/send-code')
  return data
}

/**
 * Initiate TOTP setup - generates secret and QR code
 * @param request - Email code or password
 * @returns Setup response with secret, QR code URL, and setup token
 */
export async function initiateSetup(request?: TotpSetupRequest): Promise<TotpSetupResponse> {
  const { data } = await apiClient.post<TotpSetupResponse>('/user/totp/setup', request || {})
  return data
}

/**
 * Complete TOTP setup by verifying the code
 * @param request - TOTP code and setup token
 * @returns Enable response with success status and enabled timestamp
 */
export async function enable(request: TotpEnableRequest): Promise<TotpEnableResponse> {
  const { data } = await apiClient.post<TotpEnableResponse>('/user/totp/enable', request)
  return data
}

/**
 * Disable TOTP for current user
 * @param request - Current TOTP for every user; administrators may instead use server recovery authorization
 * @returns Success response
 */
export async function disable(request: TotpDisableRequest): Promise<{ success: boolean }> {
  const { data } = await apiClient.post<{ success: boolean }>('/user/totp/disable', request)
  return data
}

/**
 * Step-up verification response.
 *
 * The grace window is 5 minutes (same TTL as the on-chain payment crypto
 * grant). Every sensitive request that skips the TOTP prompt while the window
 * is valid reports `X-StepUp-Remaining`; the UI shows it as a countdown.
 */
export interface TotpStepUpResponse {
  verified: boolean
  /** Absolute expiry of the grace window (Unix seconds). */
  expires_at?: number
  /** Remaining grace window in seconds. */
  expires_in: number
}

/**
 * Verify a TOTP code to grant the current session a short-lived step-up
 * (sudo) window for sensitive operations (account export, DB backup download...).
 * @param code - 6-digit TOTP code
 */
export async function stepUp(code: string): Promise<TotpStepUpResponse> {
  const { data } = await apiClient.post<TotpStepUpResponse>('/user/totp/step-up', { code })
  return data
}

/**
 * 读取当前账号的双因子策略：前端 step-up 弹窗前据此决定依次弹哪些因子
 * （先 TOTP、后 Passkey；两者都要求时必须两个都通过才重试原请求）。
 */
export async function getTwoFactorPolicy(): Promise<TwoFactorPolicy> {
  const { data } = await apiClient.get<TwoFactorPolicy>('/user/2fa/policy')
  return data
}

export const totpAPI = {
  getStatus,
  getVerificationMethod,
  sendVerifyCode,
  initiateSetup,
  enable,
  disable,
  stepUp,
  getTwoFactorPolicy
}

export default totpAPI
