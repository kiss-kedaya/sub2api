import { apiClient } from '../client'
import type { SiteThemeSettings } from '@/composables/useSiteTheme'

export type { SiteThemeSettings }

export async function getSiteTheme(): Promise<SiteThemeSettings> {
  const { data } = await apiClient.get<SiteThemeSettings>('/admin/settings/theme')
  return data
}

/** Partial update: fields left out keep their stored value. Primary administrator only. */
export async function updateSiteTheme(settings: Partial<SiteThemeSettings>): Promise<SiteThemeSettings> {
  const { data } = await apiClient.put<SiteThemeSettings>('/admin/settings/theme', settings)
  return data
}
