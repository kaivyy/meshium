import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    proxy: {
      '/api': 'http://localhost:9527',
      '/ws': {
        target: 'ws://localhost:9527',
        ws: true
      }
    }
  }
});
