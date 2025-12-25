/**
 * Admin Gemini API endpoints
 * Handles Gemini OAuth flows for administrators
 */

import { apiClient } from '../client'

export interface GeminiAuthUrlResponse {
  auth_url: string
  session_id: string
  state: string
}

export interface GeminiAuthUrlRequest {
  redirect_uri: string
  proxy_id?: number
}

export interface GeminiExchangeCodeRequest {
  session_id: string
  state: string
  code: string
  redirect_uri: string
  proxy_id?: number
}

export type GeminiTokenInfo = Record<string, unknown>

export async function generateAuthUrl(
  payload: GeminiAuthUrlRequest
): Promise<GeminiAuthUrlResponse> {
  const { data } = await apiClient.post<GeminiAuthUrlResponse>(
    '/admin/gemini/oauth/auth-url',
    payload
  )
  return data
}

export async function exchangeCode(payload: GeminiExchangeCodeRequest): Promise<GeminiTokenInfo> {
  const { data } = await apiClient.post<GeminiTokenInfo>(
    '/admin/gemini/oauth/exchange-code',
    payload
  )
  return data
}

export default { generateAuthUrl, exchangeCode }
