import { site } from "./site";

export const homeJsonLd = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: site.name,
  description: site.description,
  url: site.url,
  applicationCategory: "DeveloperApplication",
  operatingSystem: "macOS, Linux, Windows",
  offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
  license: site.licenseUrl,
  codeRepository: site.githubUrl,
};
