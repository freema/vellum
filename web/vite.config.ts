import { defineConfig, type ProxyOptions } from 'vite'
import react from '@vitejs/plugin-react'

// Dev-only proxy to a locally running vellum backend. The Origin header is
// stripped because the dev server runs on a different port than the backend;
// in production the SPA is same-origin (embedded) and the header passes the
// backend's origin check as-is.
function backend(target: string): ProxyOptions {
  return {
    target,
    configure(proxy) {
      proxy.on('proxyReq', (proxyReq) => proxyReq.removeHeader('origin'))
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': backend('http://localhost:8099'),
      '/healthz': backend('http://localhost:8099'),
      '/token': backend('http://localhost:8099'),
      '/mcp': backend('http://localhost:8099'),
    },
  },
})
