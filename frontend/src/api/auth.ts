/**
 * Authentication API endpoints
 * Handles user login, registration, and logout operations
 */

import { apiClient } from './client'
import type {
  LoginRequest,
  RegisterRequest,
  AuthResponse,
  User,
  SendVerifyCodeRequest,
  SendVerifyCodeResponse,
  PublicSettings
} from '@/types'

/**
 * Store authentication token in localStorage
 */
export function setAuthToken(token: string): void {
  localStorage.setItem('auth_token', token)
}

/**
 * Get authentication token from localStorage
 */
export function getAuthToken(): string | null {
  return localStorage.getItem('auth_token')
}

/**
 * Clear authentication token from localStorage
 */
export function clearAuthToken(): void {
  localStorage.removeItem('auth_token')
  localStorage.removeItem('auth_user')
}

/**
 * User login
 * @param credentials - Username and password
 * @returns Authentication response with token and user data
 */
export async function login(credentials: LoginRequest): Promise<AuthResponse> {
  const { data } = await apiClient.post<AuthResponse>('/auth/login', credentials)

  // Store token and user data
  setAuthToken(data.access_token)
  localStorage.setItem('auth_user', JSON.stringify(data.user))

  return data
}

/**
 * User registration
 * @param userData - Registration data (username, email, password)
 * @returns Authentication response with token and user data
 */
export async function register(userData: RegisterRequest): Promise<AuthResponse> {
  const { data } = await apiClient.post<AuthResponse>('/auth/register', userData)

  // Store token and user data
  setAuthToken(data.access_token)
  localStorage.setItem('auth_user', JSON.stringify(data.user))

  return data
}

/**
 * Get current authenticated user
 * @returns User profile data
 */
export async function getCurrentUser(): Promise<User> {
  const { data } = await apiClient.get<User>('/auth/me')
  return data
}

/**
 * User logout
 * Clears authentication token and user data from localStorage
 */
export function logout(): void {
  clearAuthToken()
  // Optionally redirect to login page
  // window.location.href = '/login';
}

/**
 * Check if user is authenticated
 * @returns True if user has valid token
 */
export function isAuthenticated(): boolean {
  return getAuthToken() !== null
}

/**
 * Get public settings (no auth required)
 * @returns Public settings including registration and Turnstile config
 */
export async function getPublicSettings(): Promise<PublicSettings> {
  const { data } = await apiClient.get<PublicSettings>('/settings/public')
  return data
}

/**
 * Send verification code to email
 * @param request - Email and optional Turnstile token
 * @returns Response with countdown seconds
 */
export async function sendVerifyCode(
  request: SendVerifyCodeRequest
): Promise<SendVerifyCodeResponse> {
  const { data } = await apiClient.post<SendVerifyCodeResponse>('/auth/send-verify-code', request)
  return data
}

export const authAPI = {
  login,
  register,
  getCurrentUser,
  logout,
  isAuthenticated,
  setAuthToken,
  getAuthToken,
  clearAuthToken,
  getPublicSettings,
  sendVerifyCode
}

export default authAPI
