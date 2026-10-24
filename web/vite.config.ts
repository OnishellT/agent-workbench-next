import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

const here = import.meta.dirname;
// Dev proxy target: the Workbench binary. Override with WORKBENCH_ORIGIN.
const gateway = process.env.WORKBENCH_ORIGIN || "http://127.0.0.1:18888";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { dedupe: ["react", "react-dom", "@base-ui/react"], alias: { "@": path.resolve(here, "src") } },
  build: { outDir: "dist", emptyOutDir: true, target: "es2022", sourcemap: false },
  server: {
    host: "127.0.0.1",
    proxy: { "/api": { target: gateway, changeOrigin: true, headers: { Origin: gateway }, ws: true } },
  },
});
