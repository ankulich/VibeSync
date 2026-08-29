import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // Route Connect-RPC paths to the matching backend service by proto
      // package prefix. Same-origin in dev — no CORS needed. The Connect
      // procedure paths look like /vibesync.auth.v1.AuthService/Login.
      '/vibesync.auth': { target: 'http://localhost:8080', changeOrigin: true },
      '/vibesync.user': { target: 'http://localhost:8081', changeOrigin: true },
      '/vibesync.room': { target: 'http://localhost:8082', changeOrigin: true },
      '/vibesync.sync': { target: 'http://localhost:8083', changeOrigin: true },
      '/vibesync.playback': { target: 'http://localhost:8084', changeOrigin: true },
      '/vibesync.media': { target: 'http://localhost:8085', changeOrigin: true },
      '/vibesync.provider': { target: 'http://localhost:8086', changeOrigin: true },
    },
  },
})
