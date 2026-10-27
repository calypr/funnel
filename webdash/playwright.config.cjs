const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './integration',
  testMatch: '**/*.pw.cjs',
  timeout: 180000,
  workers: 1,
  use: { browserName: 'chromium', headless: true },
});
