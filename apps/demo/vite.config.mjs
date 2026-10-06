import { defineConfig } from "vite";

const syncTarget = process.env.VITE_SYNC_PROXY_URL ?? "ws://127.0.0.1:8080";

export default defineConfig({
  server: {
    host: "0.0.0.0",
    port: 5173,
    strictPort: true,
    proxy: {
      "/sync": {
        target: syncTarget,
        ws: true,
        secure: false
      }
    }
  },
  preview: {
    host: "0.0.0.0",
    port: 4173,
    strictPort: true
  }
});
