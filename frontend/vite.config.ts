import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import checker from 'vite-plugin-checker'
import { resolve } from 'path'

export default defineConfig({
  plugins: [
    vue(),
    checker({
      typescript: true,
      vueTsc: true
    })
  ],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src')
    }
  },
  build: {
    outDir: '../backend/internal/web/dist',
    emptyOutDir: true
  },
  server: {
    host: '0.0.0.0',
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true
      },
      '/setup': {
        target: 'http://localhost:8080',
        changeOrigin: true
      }
    }
  }
})
