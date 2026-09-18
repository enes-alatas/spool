import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: false },
  // Tier 1 for the web: the node environment, so a test that needs a DOM has
  // to say so per file (`// @vitest-environment jsdom`) and nothing acquires
  // one by accident (ADR-0013). The suffix admits `.tsx` because that opt-in
  // is what a component test looks like — a glob that collected only `.ts`
  // would drop the first such file silently, and a skipped test file reads
  // exactly like a passing one.
  test: { include: ['src/**/*.test.{ts,tsx}'], environment: 'node' },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
        // SSE needs unbuffered, long-lived proxying
        configure: (proxy) => {
          proxy.on('proxyReq', (proxyReq) => {
            proxyReq.setHeader('Connection', 'keep-alive')
          })
        },
      },
    },
  },
})
