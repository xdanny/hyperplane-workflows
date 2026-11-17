import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Local-first: bind loopback. The API base is injected via VITE_API_BASE so the
// dev server and container builds both point at the Go API on :4250.
export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.VITE_API_BASE || "http://127.0.0.1:4252",
        changeOrigin: true,
      },
      "/file": {
        target: process.env.VITE_API_BASE || "http://127.0.0.1:4252",
        changeOrigin: true,
      },
    },
  },
});
