import { defineConfig } from 'vitest/config'

export default defineConfig({
  cacheDir: 'dev/.cache/vitest',
  test: { environment: 'node', include: ['dev/**/*.spec.ts'], testTimeout: 15000 },
})
