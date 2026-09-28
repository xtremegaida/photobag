import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

const outDir = fileURLToPath(new URL("../internal/webui/dist", import.meta.url));

// go:embed needs at least one file in the embed directory, so the build
// recreates the tracked placeholder after emptying it.
function keepPlaceholder(): Plugin {
  return {
    name: "photobag-keep-placeholder",
    closeBundle() {
      writeFileSync(`${outDir}/.keep`, "Placeholder so go:embed always matches; the web build writes the SPA here.\n");
    },
  };
}

const api = process.env.PHOTOBAG_API ?? "http://127.0.0.1:7474";

export default defineConfig({
  plugins: [react(), keepPlaceholder()],
  build: {
    outDir,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": { target: api, changeOrigin: false },
    },
  },
});
