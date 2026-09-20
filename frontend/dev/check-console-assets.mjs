import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const directory = fileURLToPath(new URL('../../backend/internal/web/dist/assets/', import.meta.url))
const css = readdirSync(directory).filter(file => file.endsWith('.css')).map(file => readFileSync(`${directory}/${file}`, 'utf8')).join('\n')
assert(!/\/images\/console-paper(?:-dark)?\.webp/.test(css), 'Console textures must not use the gateway-reserved /images route')
const backgrounds = [...css.matchAll(/--signal-paper-image\s*:\s*url\(([^)]+)\)/g)]
assert(backgrounds.length >= 2, 'Both console themes must include a background texture')
for (const [, value] of backgrounds) assert(/^['"]?data:image\/webp;base64,/.test(value), 'Console textures must be embedded in the compiled CSS')
console.log('Console background assets verified in production CSS')
