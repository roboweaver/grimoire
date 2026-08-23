import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The admin SPA is served by the Go binary under /admin, so hashed asset URLs
// must resolve under that prefix (base). The build writes into the embedded
// package internal/admin/dist, which //go:embed all:dist bakes into the binary.
export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  build: {
    outDir: "../../internal/admin/dist",
    emptyOutDir: true,
    // Deterministic, hashed asset names so the immutable Cache-Control the Go
    // handler sets under assets/ is always safe.
    assetsDir: "assets",
  },
});
