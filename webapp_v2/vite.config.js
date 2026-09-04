import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import fs from 'fs';
import path from 'path';

// Static assets the React shell shares with the ClojureScript app. They are
// not built by shadow-cljs: /images and /icons are committed under
// webapp/resources/public, and /data/connections-metadata.json is downloaded
// there by `npm --prefix ../webapp run download-connection-metadata`. Serving
// them from disk means `npm run dev` works without shadow-cljs (the control
// plane never loads the CLJS bundle); only /js and /css still go to :8280.
const CLJS_PUBLIC_DIR = path.resolve(__dirname, '../webapp/resources/public');
const CLJS_STATIC_PREFIXES = ['/images/', '/icons/', '/data/'];
const CONTENT_TYPES = {
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.json': 'application/json',
};
const METADATA_HINT =
  'connections-metadata.json is not downloaded yet. Run: npm --prefix ../webapp run download-connection-metadata';

function cljsStaticAssets() {
  return {
    name: 'hoop-cljs-static-assets',
    // configureServer middlewares run before Vite's internal ones, the proxy
    // included, so a hit here never reaches :8280.
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const pathname = (req.url || '').split('?')[0];
        if (!CLJS_STATIC_PREFIXES.some((prefix) => pathname.startsWith(prefix))) return next();

        let decoded;
        try {
          decoded = decodeURIComponent(pathname);
        } catch {
          res.statusCode = 400;
          res.setHeader('Content-Type', 'text/plain');
          res.end(`Malformed path: ${pathname}`);
          return;
        }
        const relative = path.normalize(decoded).replace(/^([/\\])+/, '');
        const file = path.join(CLJS_PUBLIC_DIR, relative);
        if (!file.startsWith(CLJS_PUBLIC_DIR + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
          res.statusCode = 404;
          res.setHeader('Content-Type', 'text/plain');
          res.end(pathname === '/data/connections-metadata.json' ? METADATA_HINT : `Not found: ${pathname}`);
          return;
        }

        res.setHeader('Content-Type', CONTENT_TYPES[path.extname(file).toLowerCase()] || 'application/octet-stream');
        fs.createReadStream(file).pipe(res);
      });
    },
  };
}

export default defineConfig({
  define: {
    __BUILD_ID__: JSON.stringify(Date.now().toString()),
    __SEGMENT_WRITE_KEY__: JSON.stringify(process.env.SEGMENT_WRITE_KEY || '043Lv52mAcoGOHWVq7n3bxZAVvocyqx0')
  },
  plugins: [react(), cljsStaticAssets()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src')
    }
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    emptyOutDir: true
  },
  server: {
    proxy: {
      // API requests → gateway backend.
      // Reads API_URL from .env — same variable the CLJS build uses via shadow-cljs closure-defines.
      '/api': {
        target: process.env.API_URL || 'http://localhost:8009',
        changeOrigin: true
      },
      // ClojureScript build output (JS bundle, CSS) → shadow-cljs dev server.
      // /images, /icons and /data are served from disk by cljsStaticAssets().
      '/js': {
        target: process.env.VITE_CLJS_URL || 'http://localhost:8280',
        changeOrigin: true
      },
      '/css': {
        target: process.env.VITE_CLJS_URL || 'http://localhost:8280',
        changeOrigin: true
      }
    }
  }
});
