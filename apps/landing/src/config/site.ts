const repo = "Abir66/mdfly";
export const docsHref = "/docs";

export const site = {
  name: "mdFly",
  url: "https://mdfly.dev",
  tagline: "From terminal to link, for anyone or anything.",
  taglineHighlight: "anyone or anything",
  description:
    "Publish markdown and its images in one command. A rendered page for people, plain text for agents.",
  repo,
  githubUrl: `https://github.com/${repo}`,
  license: "Apache-2.0",
  licenseUrl: `https://github.com/${repo}/blob/main/LICENSE`,
  ogImage: { path: "/og-image.png", width: 1200, height: 630 },
  exampleSlug: "abc12345",
  install: [
    { id: "curl", label: "curl", command: "curl -fsSL https://mdfly.dev/install.sh | sh" },
    { id: "brew", label: "brew", command: `brew install ${repo}/mdfly` },
    { id: "go", label: "go", command: `go install github.com/${repo}/cmd/mdfly@latest` },
  ],
  limits: {
    bundleMB: 25,
    files: 50,
    fileMB: 10,
    lifetimeDays: 30,
  },
  analyticsToken: "",
  nav: [
    { label: "Docs", href: docsHref },
  ],
} as const;

export const docUrl = (slug: string = site.exampleSlug) => `${site.url}/${slug}`;
export const rawUrl = (slug: string = site.exampleSlug) => `${site.url}/raw/${slug}`;
export const rawCurl = (slug: string = site.exampleSlug) => `curl -s ${rawUrl(slug)}`;
