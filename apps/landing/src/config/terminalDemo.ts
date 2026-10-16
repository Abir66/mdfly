export type TermLine = { kind: "cmd" | "out" | "url"; text: string };

export const TERMINAL_CWD = "~/notes";

export const TERMINAL_SCRIPT: TermLine[] = [
  { kind: "cmd", text: "mdfly publish notes.md" },
  { kind: "out", text: "uploaded 1/3" },
  { kind: "out", text: "uploaded 2/3" },
  { kind: "out", text: "uploaded 3/3" },
  { kind: "url", text: "" },
  { kind: "cmd", text: "mdfly publish notes.md | pbcopy" },
];
