import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  server: {
    fs: { allow: [".."] },
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8787",
        // Go compares Origin with Host; preserve the browser's dev-server host.
        changeOrigin: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: "../frontend",
    emptyOutDir: true,
    rollupOptions: { input: { chat: "index.html", desktop: "desktop.html" } },
  },
});
