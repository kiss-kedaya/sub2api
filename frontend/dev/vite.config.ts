import { defineConfig } from 'vite'
import { consolePreviewConfig, PREVIEW_MODE } from './console-preview'

export default defineConfig(({ mode, command, isPreview }) => {
  if (mode !== PREVIEW_MODE || command !== 'serve' || isPreview) {
    throw new Error('console-preview is local dev only; build and preview are disabled')
  }
  return consolePreviewConfig()
})
