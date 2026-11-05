/** 将严格类型和异步生命周期规则应用于生产代码与测试。 */
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import hooks from 'eslint-plugin-react-hooks'
export default tseslint.config(
  { ignores: ['dist/**', 'coverage/**', 'playwright-report/**', 'test-results/**', 'src/contract/**'] },
  js.configs.recommended, ...tseslint.configs.recommendedTypeChecked,
  { files: ['**/*.{ts,tsx}'], languageOptions: { parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname } },
    plugins: { 'react-hooks': hooks }, rules: { ...hooks.configs.recommended.rules, '@typescript-eslint/no-floating-promises': 'error', '@typescript-eslint/no-misused-promises': 'error', 'no-console': 'error' } },
  { files: ['**/*.mjs'], extends: [tseslint.configs.disableTypeChecked], languageOptions: { globals: { process: 'readonly', URL: 'readonly' } } }
)
