import { defineConfig } from 'vitest/config';

/**
 * Renderer build only. The Electron main/preload processes are bundled by
 * `scripts/build-main.mjs` (esbuild) because they are CommonJS and must run
 * outside the renderer sandbox.
 */
export default defineConfig({
  root: '.',
  // Relative base so the built bundle can be loaded over file:// in production.
  base: './',
  build: {
    outDir: 'dist/renderer',
    emptyOutDir: true,
    target: 'chrome128',
    sourcemap: true,
    chunkSizeWarningLimit: 1500,
  },
  server: {
    port: 5273,
    strictPort: true,
  },
  test: {
    environment: 'node',
    include: ['test/**/*.test.ts'],
  },
});
