import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./vitest.setup.tsx'],
    include: ['__tests__/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      include: ['app/**', 'components/**', 'lib/**'],
      exclude: ['app/api/**', '**/*.d.ts'],
    },
  },
  resolve: {
    alias: { '@': path.resolve(__dirname, '.') },
  },
})
