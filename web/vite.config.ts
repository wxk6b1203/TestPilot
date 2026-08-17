import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
      '/copilot-api': {
        target: 'http://localhost:8100',
        rewrite: (p) => p.replace(/^\/copilot-api/, '/api'),
      },
    },
  },
  build: {
    chunkSizeWarningLimit: 1500,
    rolldownOptions: {
      output: {
        manualChunks(id: string) {
          if (id.indexOf('node_modules') === -1) return undefined
          if (id.indexOf('/antd/') !== -1 || id.indexOf('@ant-design') !== -1) return 'antd'
          if (id.indexOf('/ai/') !== -1 || id.indexOf('@ai-sdk') !== -1) return 'ai'
          if (id.indexOf('/react') !== -1 || id.indexOf('react-router') !== -1) return 'react'
          return 'vendor'
        },
      },
    },
  },
})
