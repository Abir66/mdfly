# mdFly landing site

Static Astro + Tailwind site for mdfly.dev, built on the VM and served by Caddy (see `docs/deploy/4-deploy.md`).

- `npm install`
- `npm run dev` serves http://localhost:4321
- `npm run og` regenerates `public/og-image.png`; run it when the tagline or name changes
- `npm run check` runs type checks, builds, then asserts SEO and structure
- Site copy, URLs, install commands and limits live in `src/config/site.ts`
- Build output goes to `dist/`
