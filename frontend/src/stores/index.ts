/**
 * Pinia Stores Export
 * Central export point for all application stores
 */

export { useAuthStore } from './auth'
export { useAppStore } from './app'

// Re-export types for convenience
export type { User, LoginRequest, RegisterRequest, AuthResponse } from '@/types'
export type { Toast, ToastType, AppState } from '@/types'
