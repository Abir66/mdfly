export const SAMPLE_DOC = {
  title: "Release notes",
  intro: "What shipped this week, written once and read by the team and their agents.",
  notes: ["Search covers archived tickets", "Webhooks retry with backoff", "Reports keep the viewer's timezone"],
  image: "diagram.png",
  codeLang: "yaml",
  codeKey: "webhooks",
  codeBody: ["  retries: 3", "  backoff: exponential"],
};

export const sampleDocMarkdown = () => {
  const d = SAMPLE_DOC;
  return [
    `# ${d.title}`,
    "",
    d.intro,
    "",
    ...d.notes.map((n) => `- ${n}`),
    "",
    `![diagram](${d.image})`,
    "",
    "```" + d.codeLang,
    `${d.codeKey}:`,
    ...d.codeBody,
    "```",
  ].join("\n");
};
