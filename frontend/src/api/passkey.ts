import { apiClient } from './client'
import type { ActionCaptchaRequestProof, AuthResponse } from '@/types'

export interface PasskeyCredentialSummary {
  id: number
  name: string
  created_at: string
  last_used_at?: string
  backup: boolean
}

interface CeremonyOptionsResponse {
  session_token: string
  options: {
    publicKey: Record<string, unknown>
  }
}

function isSupported(): boolean {
  return typeof window !== 'undefined' && typeof navigator !== 'undefined' &&
    Boolean(window.PublicKeyCredential && navigator.credentials)
}

function requirePasskeySupport(): void {
  if (!isSupported()) {
    throw new Error('Passkeys are not supported by this browser')
  }
}

function base64URLToBuffer(value: string): ArrayBuffer {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = normalized + '='.repeat((4 - (normalized.length % 4)) % 4)
  const binary = atob(padded)
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  return bytes.buffer
}

function bufferToBase64URL(value: ArrayBuffer | null): string | null {
  if (value === null) return null
  const bytes = new Uint8Array(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

function creationOptionsFromJSON(
  value: Record<string, unknown>
): PublicKeyCredentialCreationOptions {
  const options = { ...value } as Record<string, unknown>
  options.challenge = base64URLToBuffer(String(options.challenge))

  const user = { ...(options.user as Record<string, unknown>) }
  user.id = base64URLToBuffer(String(user.id))
  options.user = user

  if (Array.isArray(options.excludeCredentials)) {
    options.excludeCredentials = options.excludeCredentials.map((descriptor) => ({
      ...(descriptor as Record<string, unknown>),
      id: base64URLToBuffer(String((descriptor as Record<string, unknown>).id))
    }))
  }
  return options as unknown as PublicKeyCredentialCreationOptions
}

/**
 * 把后端返回的 WebAuthn 请求选项（JSON）转成浏览器 API 需要的 ArrayBuffer 结构。
 * 登录第二步（TOTP 通过后）与 Passkey step-up 共用。
 */
export function requestOptionsFromJSON(
  value: Record<string, unknown>
): PublicKeyCredentialRequestOptions {
  const options = { ...value } as Record<string, unknown>
  options.challenge = base64URLToBuffer(String(options.challenge))
  if (Array.isArray(options.allowCredentials)) {
    options.allowCredentials = options.allowCredentials.map((descriptor) => ({
      ...(descriptor as Record<string, unknown>),
      id: base64URLToBuffer(String((descriptor as Record<string, unknown>).id))
    }))
  }
  return options as unknown as PublicKeyCredentialRequestOptions
}

function serializeRegistrationCredential(credential: PublicKeyCredential): Record<string, unknown> {
  const response = credential.response as AuthenticatorAttestationResponse
  return {
    id: credential.id,
    rawId: bufferToBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      attestationObject: bufferToBase64URL(response.attestationObject),
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      transports: typeof response.getTransports === 'function' ? response.getTransports() : []
    }
  }
}

export function serializeAssertionCredential(credential: PublicKeyCredential): Record<string, unknown> {
  const response = credential.response as AuthenticatorAssertionResponse
  return {
    id: credential.id,
    rawId: bufferToBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      authenticatorData: bufferToBase64URL(response.authenticatorData),
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      signature: bufferToBase64URL(response.signature),
      userHandle: bufferToBase64URL(response.userHandle)
    }
  }
}

async function login(proof?: ActionCaptchaRequestProof): Promise<AuthResponse> {
  requirePasskeySupport()
  const { data: begin } = proof
    ? await apiClient.post<CeremonyOptionsResponse>('/auth/passkey/login/begin', proof)
    : await apiClient.post<CeremonyOptionsResponse>('/auth/passkey/login/begin')
  const credential = await navigator.credentials.get({
    publicKey: requestOptionsFromJSON(begin.options.publicKey)
  })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error('Passkey sign-in was cancelled')
  }
  const { data } = await apiClient.post<AuthResponse>('/auth/passkey/login/finish', {
    session_token: begin.session_token,
    credential: serializeAssertionCredential(credential)
  })
  return data
}

async function register(name: string, password: string): Promise<PasskeyCredentialSummary> {
  requirePasskeySupport()
  const { data: begin } = await apiClient.post<CeremonyOptionsResponse>(
    '/user/passkeys/register/begin',
    { password }
  )
  const credential = await navigator.credentials.create({
    publicKey: creationOptionsFromJSON(begin.options.publicKey)
  })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error('Passkey creation was cancelled')
  }
  const { data } = await apiClient.post<PasskeyCredentialSummary>(
    '/user/passkeys/register/finish',
    {
      session_token: begin.session_token,
      name,
      credential: serializeRegistrationCredential(credential)
    }
  )
  return data
}

async function list(): Promise<PasskeyCredentialSummary[]> {
  const { data } = await apiClient.get<PasskeyCredentialSummary[]>('/user/passkeys')
  return data
}

async function rename(id: number, name: string): Promise<void> {
  await apiClient.patch(`/user/passkeys/${id}`, { name })
}

async function remove(id: number, password: string): Promise<void> {
  await apiClient.delete(`/user/passkeys/${id}`, { data: { password } })
}

/** 双因子登录第二步：用后端下发的 assertion 选项完成一次 WebAuthn 断言。 */
async function acceptLoginChallenge(
  options: { publicKey: Record<string, unknown> }
): Promise<Record<string, unknown>> {
  requirePasskeySupport()
  const credential = await navigator.credentials.get({
    publicKey: requestOptionsFromJSON(options.publicKey)
  })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error('Passkey sign-in was cancelled')
  }
  return serializeAssertionCredential(credential)
}

export interface PasskeyStepUpResponse {
  verified: boolean
  factor: string
  expires_at: number
  expires_in: number
}

/**
 * 敏感操作 Passkey step-up：生成 assertion 挑战 → 浏览器断言 → 服务端验证。
 * 成功后服务端写入 Passkey 因子的 5 分钟 step-up 授权与不透明凭证 Cookie。
 */
async function stepUp(): Promise<PasskeyStepUpResponse> {
  requirePasskeySupport()
  const { data: begin } = await apiClient.post<CeremonyOptionsResponse>(
    '/user/step-up/passkey/begin'
  )
  const credential = await navigator.credentials.get({
    publicKey: requestOptionsFromJSON(begin.options.publicKey)
  })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error('Passkey verification was cancelled')
  }
  const { data } = await apiClient.post<PasskeyStepUpResponse>('/user/step-up/passkey/finish', {
    session_token: begin.session_token,
    credential: serializeAssertionCredential(credential)
  })
  return data
}

export const passkeyAPI = {
  isSupported,
  login,
  register,
  list,
  rename,
  remove,
  acceptLoginChallenge,
  stepUp
}
