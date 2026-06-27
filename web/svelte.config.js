import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

const config = {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({
      pages: '../cmd/server/web/build',
      assets: '../cmd/server/web/build',
      fallback: 'index.html'
    })
  }
};

export default config;
