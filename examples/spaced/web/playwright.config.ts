import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout:  30_000,
  retries:  0,
  use: {
    baseURL: 'http://localhost:3131',
    headless: true,
  },
  webServer: {
    // Build bundle then serve via npx serve.
    command: 'npm run build && npx serve . --listen 3131 --no-clipboard',
    url: 'http://localhost:3131',
    reuseExistingServer: false,
    timeout: 30_000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
});
