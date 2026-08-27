import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  define: {
    __SEGMENT_WRITE_KEY__: JSON.stringify(process.env.SEGMENT_WRITE_KEY || '043Lv52mAcoGOHWVq7n3bxZAVvocyqx0')
  },
  plugins: [react()],
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
      // The control plane backend does not exist yet, so the API comes from the
      // gateway. Everything this app calls today — approval rules, Session Analyzer,
      // Guardrails, Data Masking, Slack, auth — is already served there.
      //
      // webapp_v2 also proxies /js, /css, /images, /data and /icons to shadow-cljs.
      // None of that applies here: there is no ClojureScript bundle, and the assets
      // this app needs live in public/.
      '/api': {
        target: process.env.API_URL || 'http://localhost:8009',
        changeOrigin: true
      }
    }
  }
});
