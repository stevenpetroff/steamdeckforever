import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { defineConfig, type Plugin } from 'vitest/config';

const fontsDir = new URL('./src/fonts/', import.meta.url);

// Ship each font's SIL OFL license next to the built page (required by the
// OFL when redistributing the fonts). Licenses are copied verbatim.
function fontLicenses(): Plugin {
  return {
    name: 'deckchat-font-licenses',
    apply: 'build',
    generateBundle() {
      for (const family of readdirSync(fontsDir)) {
        const src = join(fontsDir.pathname, family, 'OFL.txt');
        this.emitFile({
          type: 'asset',
          fileName: `licenses/${family}-OFL.txt`,
          source: readFileSync(src),
        });
      }
    },
  };
}

export default defineConfig({
  // Relative asset paths: the page is embedded in the Go binary and served
  // from whatever LAN address the Deck has. Nothing is loaded from elsewhere.
  base: './',
  plugins: [fontLicenses()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Keep fonts as separate files instead of base64 data URIs.
    assetsInlineLimit: 0,
    // No inline module-preload polyfill (the server's CSP is default-src 'self').
    modulePreload: { polyfill: false },
    target: 'es2020',
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
});
