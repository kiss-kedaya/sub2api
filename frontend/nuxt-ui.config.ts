/**
 * Shared Nuxt UI theme for the Vite app and for Vitest.
 *
 * Both entrypoints call the same `ui()` plugin, so the theme lives here once:
 * a spec mounts the real component and asserts the same classes production ships.
 *
 * Switch state matrix (tokens only, no raw Tailwind grays):
 *
 * | state            | light                          | dark                            |
 * | ---------------- | ------------------------------ | ------------------------------- |
 * | unchecked track  | `--ui-bg-accented` #e5e5e5     | `--ui-bg-accented` #3a3441      |
 * | unchecked edge   | `--ui-border-accented`         | `--ui-border-accented`          |
 * | unchecked thumb  | `--ui-bg` (white)              | `--ui-bg-inverted` (near-white) |
 * | checked track    | `--ui-primary` (sakura-500)    | `--ui-primary` (sakura-400)     |
 * | checked edge     | `primary-600` (3.61:1 on card) | `--ui-primary` (same as fill)   |
 * | checked thumb    | `--ui-bg` (white)              | `--ui-bg` (ink)                 |
 * | focus ring       | `primary-600` (3.61:1 on white) | `primary/60` (4.33:1 on card)  |
 *
 * The dark thumb intentionally flips when the track flips: the thumb is always
 * the high-contrast element against the track, so both states stay readable
 * (checked 9.62:1, unchecked near-white on #3a3441). No hover recolour: the track
 * colour *is* the on/off state, and a second hover colour would compete with it.
 * `prefers-reduced-motion` drops the track cross-fade; Nuxt UI already drops the
 * thumb slide.
 */
export const nuxtUiOptions = {
  colorMode: false,
  ui: {
    // The site theme (useSiteTheme) owns the actual palettes through --ui-primary
    // and --ui-color-*; these aliases only keep Nuxt UI's variant generation happy.
    colors: { primary: 'neutral', neutral: 'neutral' },
    switch: {
      slots: {
        base: 'focus-visible:outline-offset-2 data-[state=unchecked]:border-accented motion-reduce:transition-none',
        // `bg-default` is white in light and ink in dark; the dark unchecked state
        // needs the inverted surface or the thumb disappears into the dark track.
        thumb: 'shadow-sm ring-1 ring-black/5 dark:shadow-none dark:ring-white/10 dark:data-[state=unchecked]:bg-inverted',
      },
      variants: {
        color: {
          primary: {
            // Same --ui-primary token as every other checked control. Sakura-500 on
            // white is 2.39:1, so the 2px edge takes the contrast-bearing step
            // (primary-600 = 3.61:1); in dark the fill is already 9.62:1 and the
            // edge stays the fill colour. One focus ring step per theme: 3.61:1 in
            // light, 4.33:1 in dark, instead of Nuxt UI's `outline-primary/25`.
            base: 'data-[state=checked]:bg-primary data-[state=checked]:border-primary-600 dark:data-[state=checked]:border-primary outline-primary-600 dark:outline-primary/60',
          },
        },
      },
      defaultVariants: { color: 'primary', size: 'md' },
    },
  },
  icon: { clientBundle: { scan: true } },
  components: { globs: [] },
}
