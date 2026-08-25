/// <reference types="vitest/config" />
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
  // Test runner for milestone 06's component suite (Req 7, Req 9). Vitest
  // reuses this Vite config as-is (same plugin/resolve pipeline as the real
  // build), so there is no separate bundler config to keep in sync.
  test: {
    environment: "jsdom",
    setupFiles: ["./src/testSetup.ts"],
    css: false,
    clearMocks: true,
    server: {
      // @adobe/react-spectrum (and its @react-spectrum/@spectrum-icons/
      // @react-aria/@react-stately dependents) ship CSS imports inside their
      // own source, which only Vite's transform pipeline understands.
      // Vitest externalizes node_modules by default and loads them with
      // Node's raw module loader instead, which chokes on a bare `.css`
      // import — so every Spectrum-adjacent package must be inlined
      // (processed through Vite) rather than externalized.
      deps: {
        inline: [
          /@adobe\/react-spectrum/,
          /@react-spectrum\//,
          /@react-aria\//,
          /@react-stately\//,
          /@react-types\//,
          /@spectrum-icons\//,
        ],
      },
    },
  },
});
