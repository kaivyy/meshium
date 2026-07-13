import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';
import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';

const libPath = fileURLToPath(new URL('./src/lib', import.meta.url));

export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  resolve: {
    alias: {
      $lib: libPath,
      $app: fileURLToPath(new URL('./.svelte-kit/types', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.{svelte,ts}'],
    globals: true
  }
});
