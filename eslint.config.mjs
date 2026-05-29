import js from '@eslint/js';

export default [
  js.configs.recommended,
  {
    files: ['src/**/*.mjs', 'test/**/*.mjs'],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: {
        process: 'readonly', console: 'readonly', fetch: 'readonly', URL: 'readonly',
        Buffer: 'readonly', globalThis: 'readonly', setTimeout: 'readonly', URLSearchParams: 'readonly',
      },
    },
    rules: {
      'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': 'off',
    },
  },
];
