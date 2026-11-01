import { inject, type InjectionKey, type Ref } from 'vue'
import type { AdminUser } from '@/types'

/** What the /admin/users/:id parent shares with its sub-pages (they reuse its dialogs). */
export interface UserDetailContext {
  user: Ref<AdminUser | null>
  /** Bumped after every change, so sub-pages with their own data refetch. */
  version: Ref<number>
  reload: () => Promise<void>
  openEdit: () => void
  openBalance: (operation: 'add' | 'subtract') => void
  openBalanceHistory: () => void
  openApiKeys: () => void
  openAllowedGroups: () => void
  openPlatformQuota: () => void
}

export const userDetailKey: InjectionKey<UserDetailContext> = Symbol('admin-user-detail')

export function useUserDetail(): UserDetailContext {
  const context = inject(userDetailKey)
  if (!context) throw new Error('useUserDetail() needs the AdminUserDetailView parent')
  return context
}
