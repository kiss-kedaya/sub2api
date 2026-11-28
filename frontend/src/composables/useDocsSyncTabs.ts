import { reactive, ref } from 'vue'
import type { DocsLanguage } from '@/data/apiDocs'

const STORAGE_KEY = 'sub2api-docs-sync-tabs-v1'
const SYNCABLE_HTTP_LANGS = new Set<DocsLanguage>(['curl', 'python', 'node'])

function readStoredPreference(): DocsLanguage | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (raw && SYNCABLE_HTTP_LANGS.has(raw as DocsLanguage)) {
      return raw as DocsLanguage
    }
  } catch {
    // Ignore storage failures in restricted browsing modes.
  }
  return null
}

function writeStoredPreference(lang: DocsLanguage) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(STORAGE_KEY, lang)
  } catch {
    // Ignore storage failures.
  }
}

/**
 * Starlight / Docusaurus style synced tabs for code examples.
 * Selecting cURL / Python / Node.js in one code group syncs all compatible code groups across pages.
 */
export function useDocsSyncTabs() {
  const syncedHttpLang = ref<DocsLanguage | null>(readStoredPreference())
  const perSectionOverride = reactive<Record<string, DocsLanguage>>({})

  function resolveLanguage(sectionId: string, available: DocsLanguage[]): DocsLanguage {
    if (!available.length) return 'curl'
    const direct = perSectionOverride[sectionId]
    if (direct && available.includes(direct)) return direct
    if (syncedHttpLang.value && available.includes(syncedHttpLang.value)) {
      return syncedHttpLang.value
    }
    return available[0]
  }

  function selectLanguage(sectionId: string, language: DocsLanguage, available: DocsLanguage[]) {
    if (SYNCABLE_HTTP_LANGS.has(language) && available.length > 1) {
      syncedHttpLang.value = language
      writeStoredPreference(language)
      // Clear stale per-section overrides for compatible languages so every block stays in sync.
      for (const key of Object.keys(perSectionOverride)) {
        if (SYNCABLE_HTTP_LANGS.has(perSectionOverride[key])) {
          delete perSectionOverride[key]
        }
      }
      return
    }
    perSectionOverride[sectionId] = language
  }

  return {
    syncedHttpLang,
    resolveLanguage,
    selectLanguage,
  }
}
