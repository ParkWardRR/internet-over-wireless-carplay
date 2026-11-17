import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/**
 * Static site. `BASE_PATH` lets the same build serve from a project
 * subdirectory (GitHub Pages serves this repo at /<repo-name>/) without
 * editing anything: BASE_PATH=/internet-over-wireless-carplay npm run build
 *
 * @type {import('@sveltejs/kit').Config}
 */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter({ fallback: undefined, strict: true }),
		paths: {
			base: process.env.BASE_PATH ?? ''
		}
	}
};

export default config;
