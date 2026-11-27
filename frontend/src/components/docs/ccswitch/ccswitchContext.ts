import { computed, inject, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ccswitchDemoZh, type CcSwitchDemoMessages } from '@/i18n/locales/zh/ccswitchDemo'
import { ccswitchDemoEn } from '@/i18n/locales/en/ccswitchDemo'
import type { CcsLocale, CcsPageKind, CcsThemePref } from './ccswitchTypes'

export interface CcsTooltipPayload {
  text: string
  title?: string
  x: number
  y: number
  left: boolean
  up: boolean
}

export interface CcsTooltipContext {
  show: (event: MouseEvent | FocusEvent, text: string, title?: string) => void
  hide: () => void
}

export interface CcsWorkbenchContext {
  siteName: ComputedRef<string>
  visibleApps: Ref<string[]>
  installedApps: Ref<string[]>
  setAppVisible: (appId: string, visible: boolean) => void
  markAppInstalled: (appId: string) => void
  toast: (payload: { text: string; detail?: string; undo?: () => void }) => void
  openPage: (kind: CcsPageKind) => void
  themePref: Ref<CcsThemePref>
  setThemePref: (pref: CcsThemePref) => void
  effectiveThemeAttr: ComputedRef<'light' | 'dark' | undefined>
}

export const CCS_TOOLTIP_KEY: InjectionKey<CcsTooltipContext> = Symbol('ccsTooltip')
export const CCS_WORKBENCH_KEY: InjectionKey<CcsWorkbenchContext> = Symbol('ccsWorkbench')

export function useCcSwitchTooltip(): CcsTooltipContext {
  return (
    inject(CCS_TOOLTIP_KEY) ?? {
      show: () => undefined,
      hide: () => undefined,
    }
  )
}

export function useCcSwitchWorkbench(): CcsWorkbenchContext {
  const ctx = inject(CCS_WORKBENCH_KEY)
  if (!ctx) {
    throw new Error('useCcSwitchWorkbench must be used inside CcSwitchInteractiveDemo')
  }
  return ctx
}

export function useCcSwitchI18n() {
  const { locale } = useI18n()
  const lang = computed<CcsLocale>(() => (String(locale.value).toLowerCase().startsWith('zh') ? 'zh' : 'en'))
  const msg = computed<CcSwitchDemoMessages>(() => (lang.value === 'zh' ? ccswitchDemoZh : ccswitchDemoEn))
  return { lang, msg }
}

export function fmt(template: string, vars: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (full, key) => (key in vars ? String(vars[key]) : full))
}
