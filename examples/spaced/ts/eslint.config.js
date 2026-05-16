const tseslint = require('typescript-eslint');

module.exports = tseslint.config(
  ...tseslint.configs.recommended,
  { files: ['eslint.config.js'], rules: { '@typescript-eslint/no-require-imports': 'off' } },
  { rules: { '@typescript-eslint/no-explicit-any': 'off' } },
  { ignores: ['node_modules/', 'dist/'] },
);
