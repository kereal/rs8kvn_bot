import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// The panel is served by the Go binary under /admin/, so every asset URL is
// prefixed with that base. In dev, Vite proxies /admin/api to the Go listener
// (ADMIN_LISTEN, default 127.0.0.1:8881).
export default defineConfig({
  base: '/admin/',
  plugins: [svelte()],
  server: {
    port: 5173,
    proxy: {
      '/admin/api': 'http://127.0.0.1:8881'
    }
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022'
  }
})
