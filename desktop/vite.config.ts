import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Tauri dev server settings: fixed port, no auto-open, ignore src-tauri churn.
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 1430,
    strictPort: true,
    watch: {
      ignored: ["**/src-tauri/**"],
    },
  },
  build: {
    target: "safari15",
    outDir: "dist",
  },
});
