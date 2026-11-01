#!/usr/bin/env node
/**
 * Nuxt 4.6.3 refuses to build on Node < 22.22.3 (or < 24.15). Fail early with a
 * readable message instead of a cryptic ESM/nitro error when CI pins an older node.
 */
const raw = process.versions.node.split('.').map(Number)
const [major, minor, patch] = raw
const ok =
  (major === 22 && (minor > 22 || (minor === 22 && patch >= 3))) ||
  (major === 24 && minor >= 15) ||
  major >= 26
if (!ok) {
  console.error(
    `error: Nuxt 4.6.3 needs Node ^22.22.3 or ^24.15 (current ${process.version}). ` +
    'CI pins 24.22.x; use the same locally for build:ssr.',
  )
  process.exit(1)
}
