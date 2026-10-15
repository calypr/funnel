import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  base: '/',
  server: { port: 3000 },
  build: {
    outDir: 'build',
    assetsDir: 'static',
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/setupTests.js',
  },
});
