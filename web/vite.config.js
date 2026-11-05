import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/webd/dist',
    emptyOutDir: true,
    // Never inline the voice AudioWorklet as a data: URL; webd's CSP only
    // allows same-origin scripts, so it must ship as its own file.
    assetsInlineLimit: (file) => (file.endsWith('-worklet.js') ? false : undefined),
  },
  server: {
    // Dev proxy — forwards API and WebSocket calls to a local webd instance.
    proxy: {
      '/api/realtime': { target: 'ws://localhost:3000', ws: true },
      '/api': 'http://localhost:3000',
      '/auth': 'http://localhost:3000',
      '/ws': { target: 'ws://localhost:3000', ws: true },
    },
  },
})
