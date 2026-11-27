/**
 * The paper workspace's objects and pure rules. `Manuscript*`, never
 * `Paper`: a Paper is the Library's object, a work you cite; a manuscript
 * is the work you write, and the two never share a name in this contract.
 */
import { parseBibtex } from "./references-formats";

/** A conversation on a Task. Every Task has one from birth — its home chat. */
export interface Chat {
  id: string;
  taskId: string;
  /** An experiment's conversation; a paper's editor drawer. */
  home: boolean;
  /** The first prompt's first line until renamed; a home chat's is `Conversation`. */
  title: string;
  createdBy: string;
  createdTs: number;
  updatedTs: number;
  /** A turn is live on this chat's session. */
  running: boolean;
}

export interface TexError {
  file?: string;
  line?: number;
  message: string;
}

export interface SynctexRecord {
  page: number;
  x: number;
  y: number;
  file: string;
  line: number;
}

export interface OutlineEntry {
  level: 0 | 1 | 2 | 3 | 4;
  title: string;
  line: number;
}

/** A note pinned on a passage, for the agent (the plan's annotation with a file anchor). */
export interface ManuscriptNote {
  id: string;
  anchor: { path: string; quote: string; from: string; to: string; line: number };
  kind: "comment" | "edit";
  text: string;
}

export const CITE_COMMANDS = ["cite", "citep", "citet", "citealp", "citealt", "citeauthor", "citeyear", "parencite", "textcite", "autocite", "footcite", "nocite"] as const;

/**
 * The keys of a `.bib`, each with its title for the completion's detail
 * line. `parseBibtex` rejects a title-less entry before it reaches
 * `entries` — a sound rule for importing a reference, not for completing a
 * cite key — so a draft entry's key is pulled back from `rejected` with an
 * empty title rather than vanishing from `\cite` completion. A key used
 * twice, once title-less and once titled, never reaches `parseBibtex`'s own
 * duplicate check (it `continue`s on the missing title first), so the
 * titled entry's row wins here and the title-less row for that key is
 * dropped rather than doubling it.
 */
export function bibKeysOf(text: string): { key: string; title: string }[] {
  const { entries, rejected } = parseBibtex(text);
  const keyed = entries
    .filter((e) => typeof e.key === "string" && e.key !== "")
    .map((e) => ({ key: e.key!, title: e.title }));
  const keyedKeys = new Set(keyed.map((e) => e.key));
  const titleless = rejected
    .filter((r) => r.reason === "no-title" && r.entry !== "" && !keyedKeys.has(r.entry))
    .map((r) => ({ key: r.entry, title: "" }));
  return [...keyed, ...titleless];
}

const CITE_AT = new RegExp(`\\\\(${CITE_COMMANDS.join("|")})\\*?(?:\\[[^\\]]*\\])*\\{([^}]*)$`);

/** The cite command whose argument the cursor is in, and the key being typed there. */
export function citeCommandAt(doc: string, pos: number): { command: string; from: number; partial: string } | undefined {
  const lineStart = doc.lastIndexOf("\n", pos - 1) + 1;
  const before = doc.slice(lineStart, pos);
  const m = CITE_AT.exec(before);
  if (!m) return undefined;
  const inside = m[2]!;
  const comma = inside.lastIndexOf(",");
  const partial = comma === -1 ? inside : inside.slice(comma + 1);
  return { command: m[1]!, from: pos - partial.length, partial };
}

/** Every `\label{…}` across the files, first occurrence wins. */
export function labelsOf(files: { path: string; text: string }[]): string[] {
  const seen = new Set<string>();
  for (const file of files)
    for (const m of file.text.matchAll(/\\label\{([^}]+)\}/g)) seen.add(m[1]!);
  return [...seen];
}

const SECTIONING: Record<string, OutlineEntry["level"]> = { part: 0, chapter: 1, section: 2, subsection: 3, subsubsection: 4 };

/** The sectioning commands with their 1-based lines; a `%`-commented one is not a section. */
export function outlineOf(text: string): OutlineEntry[] {
  const out: OutlineEntry[] = [];
  text.split("\n").forEach((raw, i) => {
    const line = raw.replace(/(^|[^\\])%.*$/, "$1");
    for (const m of line.matchAll(/\\(part|chapter|section|subsection|subsubsection)\*?(?:\[[^\]]*\])?\{([^}]*)\}/g))
      out.push({ level: SECTIONING[m[1]!]!, title: m[2]!, line: i + 1 });
  });
  return out;
}

/** The file kinds a `(` on the log's stack may be opening. `bbl` is one of
 *  them although nobody writes it: BibTeX does, and a bibliography LaTeX
 *  refuses is an error inside that file rather than inside the manuscript
 *  that pulled it in. */
const TEX_LOG_FILE_EXT = /^\.?\/?([^\s()]+\.(?:tex|sty|cls|bib|bst|bbl|def|ltx))/;

/**
 * Errors and warnings out of a TeX log. A `!` line is an error; its line
 * number is the next `l.<n>` anywhere later in the log, however many lines
 * of prompt or recovery text — even a second `!` — Tectonic interleaves
 * before echoing the source line. A `(./path` opens a file on the log's
 * parenthesis stack; a bare `)` closes whichever paren opened last, file or
 * not, so a same-line aside like `(12.3pt too wide)` never pops a real file
 * off the stack. An error's file is the innermost file still open when it
 * is printed, whatever its extension — a package or class file left open
 * (`elsarticle.cls`) is where the error is, not the `.tex` that pulled it
 * in. `LaTeX Warning:`, `Overfull` and `Underfull` lines are warnings.
 * Duplicates (TeX repeats an error it recovers from) collapse by file, line
 * and message.
 */
export function parseTexLog(log: string): { errors: TexError[]; warnings: number } {
  const lines = log.split("\n");
  const errors: TexError[] = [];
  const seen = new Set<string>();
  const stack: (string | null)[] = [];
  let warnings = 0;

  const walk = (line: string) => {
    for (const m of line.matchAll(/\(|\)/g)) {
      if (m[0] === ")") {
        stack.pop();
        continue;
      }
      const opened = TEX_LOG_FILE_EXT.exec(line.slice(m.index! + 1));
      stack.push(opened ? opened[1]! : null);
    }
  };

  const openFile = (): string | undefined => {
    for (let k = stack.length - 1; k >= 0; k--) {
      const f = stack[k];
      if (f !== null && f !== undefined) return f;
    }
    return undefined;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    if (line.startsWith("!")) {
      const message = line.replace(/^!+\s*/, "").trim();
      let at: number | undefined;
      for (let j = i + 1; j < lines.length; j++) {
        const m = /^l\.(\d+)/.exec(lines[j]!);
        if (m) {
          at = Number(m[1]);
          break;
        }
      }
      const file = openFile();
      const error: TexError = { message };
      if (file !== undefined) error.file = file;
      if (at !== undefined) error.line = at;
      const key = `${error.file ?? ""}:${error.line ?? ""}:${message}`;
      if (!seen.has(key)) {
        seen.add(key);
        errors.push(error);
      }
      continue;
    }
    if (/^LaTeX Warning:|^Overfull \\|^Underfull \\/.test(line)) warnings++;
    walk(line);
  }
  return { errors, warnings };
}

/**
 * Every record of a SyncTeX file that places a source line at a point on a
 * page, with the `Input:` table resolving the file numbers.
 *
 * TeX writes most of a page as horizontal boxes `(`, glue `g`, kerns `k`,
 * math `$` and rules `r`, besides the `x`, `h` and `v` a reader might expect
 * — a section heading can be nothing but `(` and `g`. A vertical box `[` is
 * skipped: it is a region, often the whole page, filed under whichever line
 * closed it, and its point is its bottom edge. So is everything that is not
 * a record (the preamble, the postamble, the `)`/`]` that close boxes).
 */
export function parseSynctex(text: string): SynctexRecord[] {
  const inputs = new Map<number, string>();
  const out: SynctexRecord[] = [];
  let page = 0;
  for (const line of text.split("\n")) {
    const input = /^Input:(\d+):(.*)$/.exec(line);
    if (input) {
      inputs.set(Number(input[1]), input[2]!.replace(/^\.\//, ""));
      continue;
    }
    const open = /^\{(\d+)$/.exec(line);
    if (open) {
      page = Number(open[1]);
      continue;
    }
    const rec = /^[xhvgk$r(](\d+),(\d+):(-?\d+),(-?\d+)/.exec(line);
    if (rec) {
      const file = inputs.get(Number(rec[1]));
      if (file === undefined) continue;
      out.push({ page, x: Number(rec[3]), y: Number(rec[4]), file, line: Number(rec[2]) });
    }
  }
  return out;
}

/**
 * Where Go to PDF lands for `line` of `file`: the nearest line at or after it
 * that put anything on a page — TeX files a paragraph under the line that
 * ends it, and a preamble line puts nothing anywhere — at the top of what
 * that line typeset, on the first page it reached.
 *
 * A record's file is whatever the SyncTeX file named it, and Tectonic names
 * each input by the absolute path of the tree it compiled — a directory on
 * the machine that built the paper, which nothing asking for a record
 * knows. So a record matches when its file IS the path asked for or ends
 * at it, on a separator: `sections/intro.tex` finds the build tree's copy
 * of it, and `in.tex` finds neither that nor `main.tex`.
 */
export function forwardSearch(records: SynctexRecord[], file: string, line: number): SynctexRecord | undefined {
  const names = (r: SynctexRecord) => r.file === file || r.file.endsWith(`/${file}`);
  return records
    .filter((r) => names(r) && r.line >= line)
    .sort((a, b) => a.line - b.line || a.page - b.page || a.y - b.y)[0];
}

/**
 * The source line under a point of the PDF — the way back from the page to
 * the LaTeX: the record on `page` nearest to depth `y` (scaled points, as
 * the records are), the higher line on a tie. `undefined` for a page with
 * nothing recorded on it.
 */
export function inverseSearch(records: SynctexRecord[], page: number, y: number): SynctexRecord | undefined {
  return records
    .filter((r) => r.page === page && r.file !== "")
    .sort((a, b) => Math.abs(a.y - y) - Math.abs(b.y - y) || a.line - b.line)[0];
}

/** Scaled points to PDF points: TeX's point is 1/72.27 in, the PDF's 1/72. */
export const synctexToPdfPoints = (sp: number): number => (sp / 65536) * (72 / 72.27);
/** PDF points to scaled points, for asking the records about a point on the page. */
export const pdfPointsToSynctex = (pt: number): number => (pt * 72.27 * 65536) / 72;

/** The quick-pick chips inside a Comment: id, chip text, the sentence it fills in. */
export const MANUSCRIPT_LABELS: { id: string; text: string; sentence: string }[] = [
  { id: "unclear", text: "unclear", sentence: "This passage is unclear; say what it means in plain terms." },
  { id: "needs-citation", text: "needs a citation", sentence: "This claim needs a citation from refs.bib or the Library." },
  { id: "too-long", text: "too long", sentence: "This is too long; cut it to what carries the argument." },
  { id: "check-math", text: "check the math", sentence: "Check this derivation step by step and fix what does not follow." },
  { id: "wrong-tone", text: "wrong tone", sentence: "The register is wrong here; make it read like the rest of the paper." },
  { id: "rewrite", text: "rewrite this", sentence: "Rewrite this passage, keeping its meaning and its citations." },
  { id: "move", text: "move elsewhere", sentence: "This belongs in another section; move it where it fits and leave a sentence behind if one is needed." },
];

/**
 * The text a send carries when notes are pinned: the plan's `compileReason`
 * in the paper's voice — numbered, in file-and-line order, a comment as
 * itself and an edit as `edit: …`, the free text last. Notes alone, free text
 * alone, or both; nothing at all is the empty string, which a caller refuses.
 */
export function compileManuscriptNotes(notes: ManuscriptNote[], freeText: string): string {
  const free = freeText.trim();
  const ordered = [...notes].sort(
    (a, b) => a.anchor.path.localeCompare(b.anchor.path) || a.anchor.line - b.anchor.line,
  );
  if (ordered.length === 0) return free;
  const lines: string[] = [`${ordered.length} ${ordered.length === 1 ? "note" : "notes"} on the paper.`, ""];
  ordered.forEach((note, i) => {
    const head = `${i + 1}. ${note.anchor.path}:${note.anchor.line} — on "${note.anchor.quote}"`;
    lines.push(note.kind === "edit" ? `${head} — edit: ${note.text}` : `${head} — ${note.text}`);
  });
  if (free !== "") lines.push("", free);
  return lines.join("\n");
}

/** The selection toolbar's Proofread: fixes grammar, spelling and clarity in place, nothing else. */
export function proofreadPrompt(file: string, line: number, quote: string): string {
  return (
    `Proofread this passage of \`${file}\` (line ${line}): "${quote}". ` +
    "Fix grammar, spelling and clarity; keep the meaning, the notation and the citations; change nothing outside it."
  );
}

