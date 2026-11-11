import { inject, type InjectionKey, type Ref } from 'vue'

// Rows inside a SettingPanel register themselves here so the panel's summary bar can list what is on
// without the page having to compute it. A row that unmounts (v-if false) unregisters, so the summary
// always matches what the user can see.
export interface SettingPanelEntry {
  id: string
  label: () => string
  on: () => boolean
  el: Ref<HTMLElement | null>
}

export interface SettingPanelRegistry {
  register: (entry: SettingPanelEntry) => void
  unregister: (id: string) => void
}

export const settingPanelKey: InjectionKey<SettingPanelRegistry> = Symbol('setting-panel')

export function useSettingPanel(): SettingPanelRegistry | null {
  return inject(settingPanelKey, null)
}
