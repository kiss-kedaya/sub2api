import { inject, onBeforeUnmount, onUpdated, provide, reactive, type InjectionKey } from 'vue'

/**
 * Hooks into the console frame (AppLayout): the navbar title, the navbar right side and
 * the toolbar row under the navbar. Page headers and <PageNavbar> / <PageToolbar> teleport
 * their content to the target ids below. Outside the frame (a spec that stubs AppLayout,
 * a public page) useDashboardFrame() returns null and the content renders in place.
 * The toolbar row shows only while one of its targets has content (CSS :has, AppHeader).
 */
export const FRAME_TARGETS = {
  /** Replaces the route title in the navbar (a page header's own #title slot). */
  title: 'console-navbar-title',
  /** After the title: a count badge or a short live status. */
  titleTrailing: 'console-navbar-title-trailing',
  /** Navbar right side, before notifications / theme / balance. */
  actions: 'console-navbar-actions',
  /** Toolbar row under the navbar, left and right. */
  toolbarStart: 'console-toolbar-start',
  toolbarEnd: 'console-toolbar-end'
} as const

/** Page actions sit in the navbar from this width; narrower screens get the toolbar row. */
export const NAVBAR_ACTIONS_QUERY = '(min-width: 1280px)'
/** A header's live status line sits next to the title from this width. */
export const NAVBAR_STATUS_QUERY = '(min-width: 1024px)'

export const selector = (id: string) => `#${id}`

export interface TitleClaim {
  id: number
  /** The page's own title text; empty when it only brings markup. */
  text: () => string
  /** True while the page teleports its own title markup to FRAME_TARGETS.title. */
  slot: boolean
}

export interface DashboardFrame {
  /** Newest last; the navbar shows the last one, or the route title when empty. */
  titles: TitleClaim[]
}

const FRAME_KEY: InjectionKey<DashboardFrame> = Symbol('dashboard-frame')

export function provideDashboardFrame(): DashboardFrame {
  const frame = reactive<DashboardFrame>({ titles: [] })
  provide(FRAME_KEY, frame)
  return frame
}

export function useDashboardFrame(): DashboardFrame | null {
  return inject(FRAME_KEY, null)
}

let nextClaimId = 1

/**
 * Names the page in the navbar for as long as the calling component lives. `hasSlot` is
 * read again after each render of the caller, since slot presence is not reactive.
 */
export function claimFrameTitle(frame: DashboardFrame | null, text: () => string, hasSlot: () => boolean): void {
  if (!frame) return
  const id = nextClaimId++
  frame.titles.push({ id, text, slot: hasSlot() })
  const claim = () => frame.titles.find((entry) => entry.id === id)
  onUpdated(() => {
    const entry = claim()
    if (entry && entry.slot !== hasSlot()) entry.slot = hasSlot()
  })
  onBeforeUnmount(() => {
    const index = frame.titles.findIndex((entry) => entry.id === id)
    if (index >= 0) frame.titles.splice(index, 1)
  })
}
