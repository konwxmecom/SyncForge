import { defineConfig } from "vite";

const syncTarget = process.env.VITE_SYNC_PROXY_URL ?? "ws://127.0.0.1:8080";

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/sync": {
        target: syncTarget,
        ws: true
      }
    }
  },
  preview: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: true
  }
});
