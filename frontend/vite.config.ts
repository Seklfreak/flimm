import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  server: {
    port: 5173,
    // Same-origin API and media in production; proxy both to the Go backend in dev.
    proxy: {
      "/api": "http://localhost:8080",
      "/media": "http://localhost:8080",
    },
  },
});
