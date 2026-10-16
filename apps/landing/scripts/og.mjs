import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import satori from "satori";
import { Resvg } from "@resvg/resvg-js";
import { site } from "../src/config/site.ts";

const WIDTH = 1200;
const HEIGHT = 630;
const PADDING = 80;
const GRID_SIZE = 48;
const HIGHLIGHT = "anyone or anything";
const COMMAND = "$ mdfly publish notes.md";

const COLOR_BG = "#ffffff";
const COLOR_FG = "#0b0b12";
const COLOR_MUTED = "#5b5b6b";
const COLOR_LINE = "#e7e7ee";
const COLOR_ACCENT = "#7c3aed";
const COLOR_ACCENT_SOFT = "rgba(124, 58, 237, 0.16)";
const COLOR_GRID = "rgba(15, 15, 30, 0.05)";
const COLOR_SURFACE = "#fafafa";

const root = (path) => fileURLToPath(new URL(`../${path}`, import.meta.url));
const font = (pkg, file) => readFileSync(root(`node_modules/@fontsource/${pkg}/files/${file}`));

const fonts = [
  { name: "Bricolage", data: font("bricolage-grotesque", "bricolage-grotesque-latin-800-normal.woff"), weight: 800, style: "normal" },
  { name: "Mono", data: font("jetbrains-mono", "jetbrains-mono-latin-400-normal.woff"), weight: 400, style: "normal" },
];

const logo = `data:image/svg+xml;base64,${readFileSync(root("public/logo.svg")).toString("base64")}`;
const [before, after] = site.tagline.split(HIGHLIGHT);

const h = (type, style, children) => ({ type, props: { style: { display: "flex", ...style }, children } });

const brand = h("div", { alignItems: "center", gap: 16 }, [
  { type: "img", props: { src: logo, width: 51, height: 42 } },
  h("div", { fontFamily: "Bricolage", fontWeight: 800, fontSize: 44, letterSpacing: -1.5, color: COLOR_FG }, site.name),
]);

const headline = h(
  "div",
  { display: "block", fontFamily: "Bricolage", fontWeight: 800, fontSize: 80, lineHeight: 1, letterSpacing: -3, width: WIDTH - PADDING * 2, color: COLOR_FG },
  [
    before,
    { type: "span", props: { style: { color: COLOR_ACCENT }, children: HIGHLIGHT } },
    after,
  ],
);

const command = h(
  "div",
  { alignItems: "center", fontFamily: "Mono", fontSize: 30, color: COLOR_FG, background: COLOR_SURFACE, border: `2px solid ${COLOR_LINE}`, borderRadius: 14, padding: "16px 28px" },
  [{ type: "span", props: { style: { color: COLOR_ACCENT, marginRight: 16 }, children: "$" } }, COMMAND.slice(2)],
);

const tree = h(
  "div",
  {
    flexDirection: "column",
    justifyContent: "space-between",
    boxSizing: "border-box",
    width: WIDTH,
    height: HEIGHT,
    padding: PADDING,
    background: COLOR_BG,
    backgroundImage: `radial-gradient(circle at 100% 0%, ${COLOR_ACCENT_SOFT} 0%, rgba(124, 58, 237, 0) 55%), linear-gradient(${COLOR_GRID} 1px, transparent 1px), linear-gradient(90deg, ${COLOR_GRID} 1px, transparent 1px)`,
    backgroundSize: `100% 100%, ${GRID_SIZE}px ${GRID_SIZE}px, ${GRID_SIZE}px ${GRID_SIZE}px`,
  },
  [
    brand,
    headline,
    h("div", { alignItems: "center", justifyContent: "space-between" }, [
      command,
      h("div", { fontFamily: "Mono", fontSize: 26, color: COLOR_MUTED }, new URL(site.url).host),
    ]),
  ],
);

const svg = await satori(tree, { width: WIDTH, height: HEIGHT, fonts });
const png = new Resvg(svg, { fitTo: { mode: "width", value: WIDTH } }).render().asPng();
writeFileSync(root("public/og-image.png"), png);
