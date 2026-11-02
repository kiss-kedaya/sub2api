import { defineConfig } from 'vitest/config'
import { fileURLToPath } from 'node:url'

// Server-side suite for the SSR BFF. Kept separate from the src/ suite so the
// app tests keep their jsdom setup and the server tests run in plain Node.
export default defineConfig({
  root: fileURLToPath(new URL('..', import.meta.url)),
  test: {
    include: ['server/**/*.spec.ts'],
    environment: 'node',
    globals: false,
    testTimeout: 10000,
  },
})
