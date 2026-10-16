import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";
import sitemap from "@astrojs/sitemap";
import { site } from "./src/config/site.ts";

export default defineConfig({
  site: site.url,
  trailingSlash: "never",
  build: { format: "file" },
  integrations: [sitemap({ filter: (p) => !p.endsWith("/404") })],
  markdown: { shikiConfig: { themes: { light: "github-light", dark: "github-dark" } } },
  vite: { plugins: [tailwindcss()] },
});
