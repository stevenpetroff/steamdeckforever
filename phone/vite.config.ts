import { defineConfig } from 'vitest/config';

export default defineConfig({
  // Relative asset paths: the page is embedded in the Go binary and served
  // from whatever LAN address the Deck has. Nothing is loaded from elsewhere.
  base: './',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // No inline module-preload polyfill (the server's CSP is default-src 'self').
    modulePreload: { polyfill: false },
    target: 'es2020',
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
});
