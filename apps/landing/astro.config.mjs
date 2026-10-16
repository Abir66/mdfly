import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";
import sitemap from "@astrojs/sitemap";

export default defineConfig({
  site: "https://mdfly.dev",
  trailingSlash: "never",
  build: { format: "file" },
  integrations: [sitemap()],
  vite: { plugins: [tailwindcss()] },
});
