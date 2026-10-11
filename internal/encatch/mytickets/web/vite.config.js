// encatch: build for the My Tickets customer app, served by Go at /my-tickets.
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// In development the API, sign-in, uploads and fonts come from the local preview
// (go run ./internal/encatch/mytickets/devpreview) or a local libredesk.
const backend = process.env.MY_TICKETS_BACKEND || 'http://localhost:8790'

// Sign-in redirects to /my-tickets (no slash), which Go serves; the dev server only
// knows /my-tickets/.
const bareBase = {
  name: 'my-tickets-bare-base',
  configureServer(server) {
    server.middlewares.use((req, _res, next) => {
      if (req.url === '/my-tickets' || req.url.startsWith('/my-tickets?')) req.url = '/my-tickets/' + req.url.slice(11)
      next()
    })
  },
}

export default defineConfig({
  base: '/my-tickets/',
  plugins: [vue(), tailwindcss(), bareBase],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 8791,
    strictPort: true,
    proxy: {
      // Keep the Host header, so the backend's redirects come back to this dev server.
      ...Object.fromEntries(
        ['/my-tickets/api', '/my-tickets/login', '/preview-login', '/preview-uploads', '/uploads', '/static'].map((p) => [p, { target: backend, changeOrigin: false }]),
      ),
      // The dev server puts the base in front of index.html's /static links (the build
      // doesn't): send those to libredesk's /static too.
      '/my-tickets/static': { target: backend, changeOrigin: false, rewrite: (p) => p.replace(/^\/my-tickets/, '') },
    },
  },
  build: {
    // Go embeds this (spa.go); see build/README.md.
    outDir: 'build/app',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
  },
})
