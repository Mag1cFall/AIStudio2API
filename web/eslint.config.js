import tsParser from '@typescript-eslint/parser'
import skipFormatting from 'eslint-config-prettier/flat'
import oxlint from 'eslint-plugin-oxlint'
import pluginVue from 'eslint-plugin-vue'

// eslintConfig 只运行 eslint-plugin-vue 的模板规则，格式规则由 eslint-config-prettier 关闭、交给 Oxfmt
export default [
  {
    ignores: ['node_modules'],
  },
  ...pluginVue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tsParser,
      },
    },
    rules: {
      'vue/multi-word-component-names': 'off',
      'vue/no-v-html': 'off',
    },
  },
  ...oxlint.configs['flat/vue'],
  skipFormatting,
]
