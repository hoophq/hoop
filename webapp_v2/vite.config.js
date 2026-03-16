import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      // API requests → gateway backend
      '/api': {
        target: process.env.VITE_GATEWAY_URL || 'http://localhost:8009',
        changeOrigin: true,
      },
      // ClojureScript assets (JS bundle, CSS, images) → shadow-cljs dev server
      '/js': {
        target: process.env.VITE_CLJS_URL || 'http://localhost:8280',
        changeOrigin: true,
      },
      '/css': {
        target: process.env.VITE_CLJS_URL || 'http://localhost:8280',
        changeOrigin: true,
      },
      '/images': {
        target: process.env.VITE_CLJS_URL || 'http://localhost:8280',
        changeOrigin: true,
      },
      '/data': {
        target: process.env.VITE_CLJS_URL || 'http://localhost:8280',
        changeOrigin: true,
      },
    },
  },
});
