import type { createAppI18n } from '../i18n'
import type { AxiosAdapter, AxiosInstance } from 'axios'
import type { Router } from 'vue-router'
import type { Ref, ShallowRef } from 'vue'
import type { SiteThemeSettings, ThemeMode } from '../theme/settings'

export interface DialogLayerState { minimum: () => number }

/** The standalone preview has no server request; Nuxt replaces this module by alias. */
export interface AppRuntimeContext {
  i18n: ReturnType<typeof createAppI18n>
  apiAdapter?: AxiosAdapter
  apiClient?: AxiosInstance
  cookieSession: boolean
  router?: Router
  siteTheme?: Ref<SiteThemeSettings>
  visitorMode?: Ref<ThemeMode | null>
  dialogLayers?: ShallowRef<DialogLayerState[]>
}

export function getAppRuntimeContext(): AppRuntimeContext | undefined {
  return undefined
}
