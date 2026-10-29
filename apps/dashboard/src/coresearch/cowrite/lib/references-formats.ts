/**
 * The three readers an import takes and the two writers an export gives
 * Pure, and in the contract, so every caller parses the same file the same
 * way. No YAML/BibTeX library: the shapes
 * a bibliography manager emits are regular enough to read in a page each,
 * and a dependency here would land in the browser bundle.
 */
import {
  normaliseIds,
  type ImportFormat,
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

// ---- RIS ----------------------------------------------------------------

const RIS_TYPES: Record<string, string> = {
  JOUR: "journal-article",
  EJOUR: "journal-article",
  CONF: "proceedings-article",
  CPAPER: "proceedings-article",
  BOOK: "book",
  CHAP: "book-chapter",
  THES: "thesis",
  RPRT: "report",
  UNPB: "preprint",
};

export function parseRis(text: string): ParsedReferences {
  const entries: ParsedEntry[] = [];
  const rejected: ImportRejection[] = [];
  const records = text.split(/\r?\n/).reduce<string[][]>((acc, line) => {
    if (/^TY\s{2}-/.test(line)) acc.push([line]);
    else if (acc.length > 0) acc[acc.length - 1]!.push(line);
    return acc;
  }, []);
  records.forEach((lines, n) => {
    const fields = new Map<string, string[]>();
    for (const line of lines) {
      const m = /^([A-Z][A-Z0-9])\s{2}-\s?(.*)$/.exec(line);
      if (!m) continue;
      const list = fields.get(m[1]!) ?? [];
      list.push(m[2]!.trim());
      fields.set(m[1]!, list);
    }
    const first = (...tags: string[]) => tags.map((t) => fields.get(t)?.[0]).find((v) => v !== undefined && v !== "");
    const title = first("TI", "T1") ?? "";
    const label = `record ${n + 1}`;
    if (title === "") { rejected.push({ entry: label, reason: "no-title" }); return; }
    const year = readYear(first("PY", "Y1"));
    if ("bad" in year) { rejected.push({ entry: label, reason: "bad-year", detail: year.bad }); return; }
    const authors = [...(fields.get("AU") ?? []), ...(fields.get("A1") ?? [])]
      .map(readAuthor)
      .filter((a): a is PaperAuthor => a !== undefined);
    const venue = first("JO", "JF", "T2");
    const ty = first("TY") ?? "";
    entries.push({
      ids: normaliseIds({ doi: first("DO"), arxiv: arxivFromUrl(first("UR")) }),
      title,
      authors,
      ...(year.year === undefined ? {} : { year: year.year }),
      ...(venue === undefined ? {} : { venue }),
      type: RIS_TYPES[ty] ?? "other",
      ...(first("AB") === undefined ? {} : { abstract: first("AB")! }),
    });
  });
  return { entries, rejected };
}

// ---- CSL-JSON -----------------------------------------------------------

const CSL_TO_TYPE: Record<string, string> = {
  "article-journal": "journal-article",
  "paper-conference": "proceedings-article",
  book: "book",
  chapter: "book-chapter",
  thesis: "thesis",
  report: "report",
  article: "article",
};

const TYPE_TO_CSL: Record<string, string> = {
  "journal-article": "article-journal",
  "proceedings-article": "paper-conference",
  book: "book",
  "book-chapter": "chapter",
  thesis: "thesis",
  report: "report",
  preprint: "article",
};

export function parseCslJson(text: string): ParsedReferences {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (err) {
    return { entries: [], rejected: [{ entry: "(file)", reason: "unparseable", detail: err instanceof Error ? err.message : String(err) }] };
  }
  if (!Array.isArray(parsed)) return { entries: [], rejected: [{ entry: "(file)", reason: "unparseable", detail: "not a JSON array" }] };
  const entries: ParsedEntry[] = [];
  const rejected: ImportRejection[] = [];
  const seen = new Set<string>();
  parsed.forEach((item, n) => {
    const it = (item ?? {}) as Record<string, unknown>;
    const key = typeof it.id === "string" ? it.id : typeof it.id === "number" ? String(it.id) : undefined;
    const label = key ?? `item ${n + 1}`;
    const title = typeof it.title === "string" ? it.title.trim() : "";
    if (title === "") { rejected.push({ entry: label, reason: "no-title" }); return; }
    const issued = (it.issued as { "date-parts"?: unknown } | undefined)?.["date-parts"];
    const rawYear = Array.isArray(issued) && Array.isArray(issued[0]) ? issued[0][0] : undefined;
    let year: number | undefined;
    if (rawYear !== undefined) {
      const y = readYear(String(rawYear));
      if ("bad" in y) { rejected.push({ entry: label, reason: "bad-year", detail: y.bad }); return; }
      year = y.year;
    }
    if (key !== undefined) {
      if (seen.has(key)) { rejected.push({ entry: key, reason: "duplicate-in-file" }); return; }
      seen.add(key);
    }
    const authors = (Array.isArray(it.author) ? it.author : [])
      .map((a) => {
        const au = (a ?? {}) as Record<string, unknown>;
        if (typeof au.family === "string") return { family: au.family, ...(typeof au.given === "string" ? { given: au.given } : {}) };
        if (typeof au.literal === "string") return readAuthor(au.literal);
        return undefined;
      })
      .filter((a): a is PaperAuthor => a !== undefined);
    const type = typeof it.type === "string" ? CSL_TO_TYPE[it.type] ?? "other" : undefined;
    entries.push({
      ...(key === undefined ? {} : { key }),
      ids: normaliseIds({
        doi: typeof it.DOI === "string" ? it.DOI : undefined,
        pmid: typeof it.PMID === "string" ? it.PMID : undefined,
        pmcid: typeof it.PMCID === "string" ? it.PMCID : undefined,
        arxiv: arxivFromUrl(typeof it.URL === "string" ? it.URL : undefined),
      }),
      title,
      authors,
      ...(year === undefined ? {} : { year }),
      ...(typeof it["container-title"] === "string" ? { venue: it["container-title"] } : {}),
      ...(type === undefined ? {} : { type }),
      ...(typeof it.abstract === "string" ? { abstract: it.abstract } : {}),
    });
  });
  return { entries, rejected };
}

export function parseReferences(format: ImportFormat, text: string): ParsedReferences {
  if (format === "bibtex") return parseBibtex(text);
  if (format === "ris") return parseRis(text);
  return parseCslJson(text);
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

export function emitCslJson(rows: ExportRow[]): string {
  const sorted = [...rows].sort((x, y) => x.citeKey.localeCompare(y.citeKey));
  const items = sorted.map(({ citeKey, paper }) => ({
    id: citeKey,
    type: TYPE_TO_CSL[paper.type ?? ""] ?? "article",
    title: paper.title,
    ...(paper.authors.length > 0 ? { author: paper.authors.map((a) => ({ family: a.family, ...(a.given === undefined ? {} : { given: a.given }) })) } : {}),
    ...(paper.year === undefined ? {} : { issued: { "date-parts": [[paper.year]] } }),
    ...(paper.venue === undefined ? {} : { "container-title": paper.venue }),
    ...(paper.ids.doi === undefined ? {} : { DOI: paper.ids.doi }),
    ...(paper.ids.pmid === undefined ? {} : { PMID: paper.ids.pmid }),
    ...(paper.ids.pmcid === undefined ? {} : { PMCID: paper.ids.pmcid }),
    ...(paper.ids.arxiv === undefined ? {} : { URL: `https://arxiv.org/abs/${paper.ids.arxiv}` }),
    ...(paper.abstract === undefined ? {} : { abstract: paper.abstract }),
  }));
  return JSON.stringify(items, null, 2);
}
