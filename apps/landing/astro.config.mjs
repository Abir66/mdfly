import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";
import sitemap from "@astrojs/sitemap";

export default defineConfig({
  site: "https://mdfly.dev",
  trailingSlash: "never",
  build: { format: "file" },
  integrations: [sitemap()],
  markdown: { shikiConfig: { themes: { light: "github-light", dark: "github-dark" } } },
  vite: { plugins: [tailwindcss()] },
});
