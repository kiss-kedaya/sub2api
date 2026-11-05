/**
 * Marks <html> while a user-initiated day/night switch is cross-fading (see useSiteTheme.ts).
 * It is a data attribute on purpose: the sakura canvas and the chart theme observe the `class`
 * attribute for the mode itself, and must not be woken by the marker.
 */
export const THEME_SWITCHING_ATTR = 'data-theme-switching'

export function isThemeSwitching(): boolean {
  return typeof document !== 'undefined' && document.documentElement.hasAttribute(THEME_SWITCHING_ATTR)
}
