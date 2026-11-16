import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import vue from 'eslint-plugin-vue';

export default tseslint.config(
  {
    ignores: [
      'dist/',
      'apps/desktop/out/',
      'test-results/',
      'playwright-report/',
      'docs/',
      'research/',
      'science/',
      'apps/web/',
      'apps/dashboard/src/features/coresearch/viewers/vendor/',
      'apps/dashboard/src/features/coresearch/viewers/targeted-hdf/hdf-reader.js',
      'server/internal/workflow/bundle/',
      'apps/dashboard/src/data/emoji-data.ts',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: { parserOptions: { parser: tseslint.parser } },
  },
  {
    files: ['**/*.test.ts', 'tests/e2e/**/*.ts'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unsafe-function-type': 'off',
      '@typescript-eslint/no-this-alias': 'off',
    },
  },
  {
    files: ['apps/dashboard/src/features/coresearch/viewers/**/*.ts'],
    rules: { 'no-control-regex': 'off' },
  },
  {
    files: ['**/*.cjs'],
    rules: { '@typescript-eslint/no-require-imports': 'off' },
  },
  {
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    rules: {
      // Formatting belongs to Prettier.
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/multiline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      'vue/html-indent': 'off',
      'vue/html-closing-bracket-newline': 'off',
      'vue/html-quotes': 'off',
      'vue/multi-word-component-names': 'off',
      // TS-optional props are undefined by design.
      'vue/require-default-prop': 'off',
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
    },
  },
);
