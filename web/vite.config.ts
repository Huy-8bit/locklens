import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The Go server serves the built UI from the same origin. During development
// Vite proxies API calls to a locally running `locklens server`.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: process.env.LOCKLENS_DEV_API ?? 'http://127.0.0.1:8080', changeOrigin: false },
      '/healthz': process.env.LOCKLENS_DEV_API ?? 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: 'dist',
    rolldownOptions: { output: { codeSplitting: { groups: [{ name: 'charts', test: /node_modules\/(echarts|zrender)/ }] } } },
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
})
