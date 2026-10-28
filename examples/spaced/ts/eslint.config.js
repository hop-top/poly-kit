// Same rules as sdk/ts/eslint.config.mjs; CommonJS because this package is.
const tseslint = require('typescript-eslint');

module.exports = tseslint.config(
  ...tseslint.configs.recommended,
  { files: ['eslint.config.js'], rules: { '@typescript-eslint/no-require-imports': 'off' } },
  {
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': ['error', {
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
      }],
    },
  },
  { ignores: ['node_modules/', 'dist/'] },
);
