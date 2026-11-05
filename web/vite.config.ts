/** 前端构建与组件测试配置；开发代理保持 API 的本机来源校验。 */
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { createWarningFreeLogger } from './tooling/warningLogger.ts'
export default defineConfig({
  customLogger: createWarningFreeLogger(),
  plugins: [react()],
  server: { host: '127.0.0.1', proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true } } },
  test: { environment: 'jsdom', include: ['src/**/*.test.{ts,tsx}'], setupFiles: ['src/testSetup.ts'],
    coverage: { provider: 'v8', reporter: ['text', 'lcov'], include: ['src/**/*.{ts,tsx}', 'tooling/**/*.ts'], exclude: ['src/**/*.test.*', 'src/main.tsx', 'src/testSetup.ts', 'src/contract/**', 'src/testing/**'] } }
})
