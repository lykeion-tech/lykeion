/**
 * The readers an import takes and the writer an export gives.
 * Pure, and in the contract, so every caller parses the same file the same
 * way. No YAML/BibTeX library: the shapes
 * a bibliography manager emits are regular enough to read in a page each,
 * and a dependency here would land in the browser bundle.
 */
import {
  normaliseIds,
  type ImportRejection,
  type Paper,
  type PaperAuthor,
  type PaperIds,
} from "./references";

export interface ParsedEntry {
  /** The file's own key, when it had one (BibTeX and CSL-JSON do). */
  key?: string;
  ids: PaperIds;
  title: string;
  authors: PaperAuthor[];
  year?: number;
  venue?: string;
  type?: string;
  abstract?: string;
}

export interface ParsedReferences {
  entries: ParsedEntry[];
  rejected: ImportRejection[];
}

// ---- shared -------------------------------------------------------------

const ACCENTS: [RegExp, string][] = [
  [/\{\\"([aeiouyAEIOUY])\}|\\"\{([aeiouyAEIOUY])\}|\\"([aeiouyAEIOUY])/g, "$1$2$3̈"],
  [/\{\\'([a-zA-Z])\}|\\'\{([a-zA-Z])\}|\\'([a-zA-Z])/g, "$1$2$3́"],
  [/\{\\`([a-zA-Z])\}|\\`\{([a-zA-Z])\}|\\`([a-zA-Z])/g, "$1$2$3̀"],
  [/\{\\\^([a-zA-Z])\}|\\\^\{([a-zA-Z])\}|\\\^([a-zA-Z])/g, "$1$2$3̂"],
  [/\{\\~([a-zA-Z])\}|\\~\{([a-zA-Z])\}|\\~([a-zA-Z])/g, "$1$2$3̃"],
  [/\{\\c\{([cC])\}\}|\\c\{([cC])\}/g, "$1$2̧"],
  [/\{\\ss\}|\\ss\b/g, "ß"],
  [/\{\\o\}|\\o\b/g, "ø"],
  [/\{\\O\}|\\O\b/g, "Ø"],
  [/\{\\ae\}|\\ae\b/g, "æ"],
  [/\{\\aa\}|\\aa\b/g, "å"],
];

/** LaTeX accent macros to their letters, an escaped special character
 *  (`\&`, `\%`, `\#`, `\_` — what `emitBibtex` writes) to the character,
 *  then the braces and the macros a reader does not know are dropped —
 *  `{\emph{x}}` reads as `x`. */
function delatex(value: string): string {
  let out = value;
  for (const [re, to] of ACCENTS) out = out.replace(re, to);
  out = out.normalize("NFC");
  out = out.replace(/\\([&%#_])/g, "$1");
  out = out.replace(/\\[a-zA-Z]+\s*/g, "");
  out = out.replace(/[{}]/g, "");
  return out.replace(/\s+/g, " ").trim();
}

/** `Last, First` → `{ family, given }`; `First Last` → last word is the
 *  family unless a lower-case particle (`de`, `van`, `von`, `della`, `la`)
 *  begins it; a single word is a family. */
function readAuthor(raw: string): PaperAuthor | undefined {
  const text = delatex(raw).trim();
  if (text === "") return undefined;
  const comma = text.indexOf(",");
  if (comma !== -1) {
    const family = text.slice(0, comma).trim();
    const given = text.slice(comma + 1).trim();
    return given === "" ? { family } : { family, given };
  }
  const words = text.split(" ");
  if (words.length === 1) return { family: words[0]! };
  const particle = words.findIndex((w, i) => i > 0 && /^(de|del|della|der|den|van|von|la|le|du|da|di)$/i.test(w));
  const at = particle === -1 ? words.length - 1 : particle;
  return { family: words.slice(at).join(" "), given: words.slice(0, at).join(" ") };
}

function splitAuthors(value: string): PaperAuthor[] {
  // `and` outside braces only.
  const parts: string[] = [];
  let depth = 0;
  let current = "";
  const tokens = value.split(/(\s+and\s+)/i);
  for (const token of tokens) {
    if (/^\s+and\s+$/i.test(token) && depth === 0) {
      parts.push(current);
      current = "";
      continue;
    }
    for (const ch of token) {
      if (ch === "{") depth++;
      if (ch === "}") depth--;
    }
    current += token;
  }
  parts.push(current);
  return parts.map(readAuthor).filter((a): a is PaperAuthor => a !== undefined);
}

/** A year is the first four digits of the value; a value with none is a
 *  `bad-year`, not a placeholder. */
function readYear(value: string | undefined): { year?: number } | { bad: string } {
  if (value === undefined || value.trim() === "") return {};
  const m = /(\d{4})/.exec(value);
  if (!m) return { bad: value.trim() };
  return { year: Number(m[1]) };
}

function arxivFromUrl(url: string | undefined): string | undefined {
  if (url === undefined) return undefined;
  const m = /arxiv\.org\/(?:abs|pdf)\/([^\s?#]+)/i.exec(url);
  return m ? m[1] : undefined;
}

// ---- BibTeX -------------------------------------------------------------

const BIBTEX_TYPES: Record<string, string> = {
  article: "journal-article",
  inproceedings: "proceedings-article",
  conference: "proceedings-article",
  book: "book",
  incollection: "book-chapter",
  phdthesis: "thesis",
  mastersthesis: "thesis",
  techreport: "report",
  misc: "other",
  unpublished: "other",
};

/** One `@type{key, field = value, …}` read off `text` from `at`; the index
 *  after it, or `undefined` when the braces never close. */
function readBibtexEntry(text: string, at: number): { type: string; key: string; fields: Map<string, string>; end: number } | undefined {
  const head = /^@([a-zA-Z]+)\s*[{(]\s*([^,\s]*)\s*,?/.exec(text.slice(at));
  if (!head) return undefined;
  const type = head[1]!.toLowerCase();
  const key = head[2]!;
  let i = at + head[0].length;
  const fields = new Map<string, string>();
  let depth = 1;
  while (i < text.length && depth > 0) {
    const rest = text.slice(i);
    const ws = /^\s*/.exec(rest)![0].length;
    i += ws;
    if (text[i] === "}" || text[i] === ")") { depth--; i++; break; }
    if (text[i] === ",") { i++; continue; }
    const name = /^([a-zA-Z_-]+)\s*=\s*/.exec(text.slice(i));
    if (!name) return undefined;
    i += name[0].length;
    // eslint-disable-next-line no-useless-assignment -- copied from L2 as is
    let value = "";
    if (text[i] === "{") {
      let d = 0;
      const start = i;
      for (; i < text.length; i++) {
        if (text[i] === "{") d++;
        else if (text[i] === "}") { d--; if (d === 0) { i++; break; } }
      }
      if (d !== 0) return undefined;
      value = text.slice(start + 1, i - 1);
    } else if (text[i] === '"') {
      const close = text.indexOf('"', i + 1);
      if (close === -1) return undefined;
      value = text.slice(i + 1, close);
      i = close + 1;
    } else {
      const bare = /^[^,}\n]+/.exec(text.slice(i));
      if (!bare) return undefined;
      value = bare[0].trim();
      i += bare[0].length;
    }
    fields.set(name[1]!.toLowerCase(), value);
  }
  if (depth !== 0) return undefined;
  return { type, key, fields, end: i };
}

export function parseBibtex(text: string): ParsedReferences {
  const entries: ParsedEntry[] = [];
  const rejected: ImportRejection[] = [];
  const seen = new Set<string>();
  let i = 0;
  while (i < text.length) {
    const at = text.indexOf("@", i);
    if (at === -1) break;
    const kind = /^@([a-zA-Z]+)/.exec(text.slice(at))?.[1]?.toLowerCase();
    if (kind === "comment" || kind === "preamble" || kind === "string") {
      const read = readBibtexEntry(text, at);
      i = read === undefined ? at + 1 : read.end;
      continue;
    }
    const read = readBibtexEntry(text, at);
    if (read === undefined) {
      const line = text.slice(at).split("\n")[0]!.trim();
      rejected.push({ entry: line, reason: "unparseable" });
      i = at + 1;
      continue;
    }
    i = read.end;
    const { type, key, fields } = read;
    const title = fields.has("title") ? delatex(fields.get("title")!) : "";
    if (title === "") { rejected.push({ entry: key, reason: "no-title" }); continue; }
    const year = readYear(fields.get("year"));
    if ("bad" in year) { rejected.push({ entry: key, reason: "bad-year", detail: year.bad }); continue; }
    if (seen.has(key)) { rejected.push({ entry: key, reason: "duplicate-in-file" }); continue; }
    seen.add(key);
    const ids = normaliseIds({
      doi: fields.get("doi"),
      pmid: fields.get("pmid"),
      pmcid: fields.get("pmcid"),
      arxiv: fields.get("eprinttype")?.toLowerCase() === "arxiv" ? fields.get("eprint") : arxivFromUrl(fields.get("url")),
    });
    const venue = fields.get("journal") ?? fields.get("booktitle") ?? fields.get("publisher");
    entries.push({
      key,
      ids,
      title,
      authors: fields.has("author") ? splitAuthors(fields.get("author")!) : [],
      ...(year.year === undefined ? {} : { year: year.year }),
      ...(venue === undefined ? {} : { venue: delatex(venue) }),
      type: BIBTEX_TYPES[type] ?? "other",
      ...(fields.has("abstract") ? { abstract: delatex(fields.get("abstract")!) } : {}),
    });
  }
  return { entries, rejected };
}

// ---- writers ------------------------------------------------------------

export interface ExportRow {
  citeKey: string;
  paper: Paper;
}

const BIBTEX_ENTRY: Record<string, string> = {
  "journal-article": "article",
  "proceedings-article": "inproceedings",
  book: "book",
  "book-chapter": "incollection",
  thesis: "phdthesis",
  report: "techreport",
};

/** A value as BibTeX can carry it: `&`, `%`, `#` and `_` are syntax there
 *  (`%` opens a comment, `&` a tabular column) and are escaped; balanced
 *  braces — a writer's own case-protection — are kept, and an unbalanced
 *  one, which would swallow the rest of the entry, is dropped. */
function bibtexText(value: string): string {
  // Code points on both sides: an astral character (a mathematical italic
  // letter, an emoji) is two UTF-16 units but one entry here, so a mask
  // built over `value.length` would drift past it and misplace the strip.
  const chars = [...value];
  const keep = new Array<boolean>(chars.length).fill(true);
  const open: number[] = [];
  for (let i = 0; i < chars.length; i++) {
    if (chars[i] === "{") open.push(i);
    else if (chars[i] === "}") {
      if (open.length === 0) keep[i] = false;
      else open.pop();
    }
  }
  for (const i of open) keep[i] = false;
  return chars
    .filter((_, i) => keep[i])
    .join("")
    .replace(/[&%#_]/g, (c) => `\\${c}`);
}

const bibtexAuthor = (a: PaperAuthor) => bibtexText(a.given === undefined ? a.family : `${a.family}, ${a.given}`);

/** Entries sorted by cite key so a re-export diffs cleanly; the title braced
 *  as written; `journal` for an article, `booktitle` for proceedings,
 *  `publisher` for a book. */
export function emitBibtex(rows: ExportRow[]): string {
  const sorted = [...rows].sort((x, y) => x.citeKey.localeCompare(y.citeKey));
  const out: string[] = [];
  for (const { citeKey, paper } of sorted) {
    const type = BIBTEX_ENTRY[paper.type ?? ""] ?? "misc";
    const lines = [`@${type}{${citeKey},`, `  title = {${bibtexText(paper.title)}},`];
    if (paper.authors.length > 0) lines.push(`  author = {${paper.authors.map(bibtexAuthor).join(" and ")}},`);
    if (paper.year !== undefined) lines.push(`  year = {${paper.year}},`);
    if (paper.venue !== undefined)
      lines.push(`  ${type === "article" ? "journal" : type === "inproceedings" || type === "incollection" ? "booktitle" : "publisher"} = {${bibtexText(paper.venue)}},`);
    if (paper.ids.doi !== undefined) lines.push(`  doi = {${paper.ids.doi}},`);
    if (paper.ids.pmid !== undefined) lines.push(`  pmid = {${paper.ids.pmid}},`);
    if (paper.ids.pmcid !== undefined) lines.push(`  pmcid = {${paper.ids.pmcid}},`);
    if (paper.ids.arxiv !== undefined) lines.push(`  eprint = {${paper.ids.arxiv}},`, `  eprinttype = {arxiv},`);
    lines.push("}", "");
    out.push(lines.join("\n"));
  }
  return out.join("\n");
}
