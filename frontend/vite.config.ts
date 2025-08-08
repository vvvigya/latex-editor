import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://api-service:8080',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://api-service:8080',
        ws: true,
      },
      // The /files route is for serving the generated PDF
      '/files': {
        target: 'http://api-service:8080',
        changeOrigin: true,
      }
    }
  }
})
