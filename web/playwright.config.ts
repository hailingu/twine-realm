/** 真实后端浏览器验收配置；失败保留追踪，产物由根检查所有者清理。 */
import { defineConfig, devices } from '@playwright/test'
export default defineConfig({
  testDir: './e2e', fullyParallel: false, workers: 1, timeout: 45000,
  expect: { timeout: 8000 }, reporter: 'list',
  outputDir: process.env.TWINE_TEST_ARTIFACTS ?? 'test-results',
  use: { trace: 'retain-on-failure', screenshot: 'only-on-failure', video: 'retain-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }]
})
