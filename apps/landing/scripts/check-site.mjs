import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { site } from "../src/config/site.ts";

const DIST = fileURLToPath(new URL("../dist/", import.meta.url));
const REQUIRED_FILES = ["sitemap-index.xml", "robots.txt", "favicon.svg", "og-image.png"];
const REQUIRED_TAGS = [
  /<title>[^<]+<\/title>/,
  /<meta name="description" content="[^"]+"/,
  new RegExp(`<link rel="canonical" href="${site.url}[^"]*"`),
  /<meta property="og:image" content="[^"]+"/,
];
const FORBIDDEN = [/\/llm\//];

const failures = [];
for (const f of REQUIRED_FILES) if (!existsSync(join(DIST, f))) failures.push(`missing dist/${f}`);

const pages = readdirSync(DIST).filter((f) => f.endsWith(".html"));
if (pages.length === 0) failures.push("no html pages built");
for (const page of pages) {
  const html = readFileSync(join(DIST, page), "utf8");
  for (const re of REQUIRED_TAGS) if (!re.test(html)) failures.push(`${page}: missing ${re}`);
  const h1s = html.match(/<h1[\s>]/g)?.length ?? 0;
  if (h1s !== 1) failures.push(`${page}: expected 1 <h1>, found ${h1s}`);
  for (const re of FORBIDDEN) if (re.test(html)) failures.push(`${page}: forbidden ${re}`);
}

const robots = existsSync(join(DIST, "robots.txt")) ? readFileSync(join(DIST, "robots.txt"), "utf8") : "";
const sitemap = existsSync(join(DIST, "sitemap-0.xml")) ? readFileSync(join(DIST, "sitemap-0.xml"), "utf8") : "";
for (const [, loc] of sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)) {
  const path = new URL(loc).pathname;
  if (!robots.includes(`Allow: ${path}$`)) failures.push(`robots.txt: no "Allow: ${path}$" for sitemap page`);
}
if (!robots.includes(`Sitemap: ${site.url}/sitemap-index.xml`)) failures.push("robots.txt: sitemap URL does not match site.url");

if (failures.length) {
  console.error(failures.map((f) => `✗ ${f}`).join("\n"));
  process.exit(1);
}
console.log(`✓ ${pages.length} pages passed site checks`);
