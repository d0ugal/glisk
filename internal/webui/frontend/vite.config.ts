import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// During local dev, proxy the API to a running glisk instance.
// Override with VITE_API_TARGET=http://localhost:8080 npm run dev if needed.
const apiTarget = process.env.VITE_API_TARGET ?? "http://localhost:8080";

export default defineConfig({
    plugins: [react()],
    build: {
        outDir: "dist",
        emptyOutDir: true,
    },
    server: {
        proxy: {
            "/api": apiTarget,
        },
    },
});
