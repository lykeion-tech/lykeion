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

/** One run of a `Y.Text` delta, as `Y.Text.toDelta()` answers it. */
export interface TextDelta {
  insert: string;
  attributes?: { ins?: string; del?: string };
}

export interface TextHunk {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  oldText: string;
  newText: string;
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

/** The researcher's own skeleton — what every new paper starts from. */
export const BLANK_SKELETON = `\\documentclass[11pt]{article}
\\usepackage[margin=1in]{geometry}

% Core packages
\\usepackage{amsmath,amssymb}
\\usepackage{tikz-cd}
\\usepackage{multicol}
\\usepackage{graphicx}
\\usepackage{natbib}

% Paragraphs
\\setlength{\\parindent}{0pt}
\\setlength{\\parskip}{1\\baselineskip}

\\title{Untitled}
\\author{Author Name}
\\date{\\today}

\\begin{document}
\\maketitle

\\section{Introduction}

\\bibliographystyle{plainnat}
\\bibliography{refs}

\\end{document}
`;

/**
 * A manuscript path, normalised, or `undefined` for one that names anywhere
 * else: relative, `/`-separated, no empty, `.` or `..` segments, no
 * backslash. A doubled slash is collapsed rather than refused — it is the
 * one malformed spelling that still names one place.
 */
export function manuscriptPath(p: string): string | undefined {
  if (p.includes("\\")) return undefined;
  const segments = p.split("/").filter((s) => s !== "");
  if (segments.length === 0) return undefined;
  if (p.startsWith("/")) return undefined;
  if (segments.some((s) => s === "." || s === "..")) return undefined;
  return segments.join("/");
}

/** The first line of a prompt, trimmed and cut at eighty characters — a chat's title until renamed. */
export function chatTitleFrom(prompt: string): string {
  const line = prompt.split("\n").map((l) => l.trim()).find((l) => l !== "") ?? "";
  if (line === "") return "New chat";
  return line.length > 80 ? line.slice(0, 80) : line;
}

/** The text with pending deletions removed — what compiles, what a chat checks out, what a read answers. */
export function acceptView(delta: TextDelta[]): string {
  let out = "";
  for (const run of delta) if (run.attributes?.del === undefined) out += run.insert;
  return out;
}

/**
 * The full-text index of an accept-view index: every `del` run before it is
 * skipped over. At a boundary that touches a deleted run, a `from` lands
 * after the run and a `to` before it, so a range never spans an older
 * hunk's deletion.
 */
export function fullIndexOf(delta: TextDelta[], acceptIndex: number, side: "from" | "to" = "from"): number {
  let accept = 0;
  let full = 0;
  for (const run of delta) {
    if (side === "to" && accept === acceptIndex) return full;
    const len = run.insert.length;
    if (run.attributes?.del !== undefined) {
      full += len;
      continue;
    }
    const reached = side === "from" ? accept + len > acceptIndex : accept + len >= acceptIndex;
    if (reached) return full + (acceptIndex - accept);
    accept += len;
    full += len;
  }
  return full;
}

/** Every hunk's full-text ranges, `[from, to)`, by kind. */
export function acceptRanges(delta: TextDelta[]): {
  ins: Map<string, [number, number][]>;
  del: Map<string, [number, number][]>;
} {
  const ins = new Map<string, [number, number][]>();
  const del = new Map<string, [number, number][]>();
  let at = 0;
  for (const run of delta) {
    const end = at + run.insert.length;
    const push = (map: Map<string, [number, number][]>, id: string) => {
      const list = map.get(id) ?? [];
      const last = list[list.length - 1];
      if (last && last[1] === at) last[1] = end;
      else list.push([at, end]);
      map.set(id, list);
    };
    if (run.attributes?.ins !== undefined) push(ins, run.attributes.ins);
    if (run.attributes?.del !== undefined) push(del, run.attributes.del);
    at = end;
  }
  return { ins, del };
}

/** Lines with their terminators kept, so a hunk's text re-joins to the original bytes. */
function linesWithEnds(text: string): string[] {
  if (text === "") return [];
  const out: string[] = [];
  let at = 0;
  while (at < text.length) {
    const nl = text.indexOf("\n", at);
    if (nl === -1) {
      out.push(text.slice(at));
      break;
    }
    out.push(text.slice(at, nl + 1));
    at = nl + 1;
  }
  return out;
}

/** One line's fate in the edit script between two texts. */
type LineOp = { kind: "same" | "del" | "ins"; line: string };

/** The longest common subsequence of two line arrays, as an edit script: kept, deleted from `a`, or inserted from `b`. */
function lineOps(a: string[], b: string[]): LineOp[] {
  const n = a.length;
  const m = b.length;
  const table: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      table[i]![j] = a[i] === b[j] ? table[i + 1]![j + 1]! + 1 : Math.max(table[i + 1]![j]!, table[i]![j + 1]!);
  const ops: LineOp[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      ops.push({ kind: "same", line: a[i]! });
      i++;
      j++;
    } else if (table[i + 1]![j]! >= table[i]![j + 1]!) {
      ops.push({ kind: "del", line: a[i]! });
      i++;
    } else {
      ops.push({ kind: "ins", line: b[j]! });
      j++;
    }
  }
  while (i < n) ops.push({ kind: "del", line: a[i++]! });
  while (j < m) ops.push({ kind: "ins", line: b[j++]! });
  return ops;
}

/**
 * The hunks between two texts, by line. Two changes fewer than three
 * unchanged lines apart are one hunk, so a paragraph reworded in two places
 * is kept or undone as one thing. An addition- or deletion-only hunk names
 * the line it sits after rather than a line of its own, `0` at the top.
 */
export function hunksOf(oldText: string, newText: string): TextHunk[] {
  const ops = lineOps(linesWithEnds(oldText), linesWithEnds(newText));
  const hunks: TextHunk[] = [];
  let oldLine = 0;
  let newLine = 0;
  let open: TextHunk | undefined;
  let gap: string[] = [];
  const close = () => {
    if (open === undefined) return;
    hunks.push(open);
    open = undefined;
    gap = [];
  };
  for (const op of ops) {
    if (op.kind === "same") {
      oldLine++;
      newLine++;
      if (open !== undefined) {
        gap.push(op.line);
        if (gap.length >= 3) close();
      }
      continue;
    }
    if (open === undefined) {
      open = { oldStart: oldLine + 1, oldLines: 0, newStart: newLine + 1, newLines: 0, oldText: "", newText: "" };
    } else if (gap.length > 0) {
      const between = gap.join("");
      open.oldText += between;
      open.newText += between;
      open.oldLines += gap.length;
      open.newLines += gap.length;
      gap = [];
    }
    if (op.kind === "del") {
      open.oldText += op.line;
      open.oldLines++;
      oldLine++;
    } else {
      open.newText += op.line;
      open.newLines++;
      newLine++;
    }
  }
  close();
  return hunks.map((h) => ({
    ...h,
    oldStart: h.oldLines === 0 ? h.oldStart - 1 : h.oldStart,
    newStart: h.newLines === 0 ? h.newStart - 1 : h.newStart,
  }));
}

/**
 * A hunk's two texts with the unchanged line on either side of it in the
 * new text — what an undo on plain text finds the hunk by, since a hunk
 * that only deleted has no new text of its own to look for, and a hunk of
 * one blank line would otherwise match the first blank line in the file.
 * `before` is absent for a hunk at the top of the file, `after` for one at
 * the bottom.
 */
export interface AnchoredHunk {
  oldText: string;
  newText: string;
  before?: string;
  after?: string;
}

/** `hunk`, one of `hunksOf`'s over `newText`, with its anchors read out of `newText`. */
export function anchorHunk(newText: string, hunk: TextHunk): AnchoredHunk {
  const lines = linesWithEnds(newText);
  // Where the hunk sits in the new text's lines, as `hunksOf` numbers them:
  // a hunk with new lines starts on `newStart`; one that only deleted sits
  // after the line `newStart` names.
  const at = hunk.newLines > 0 ? hunk.newStart - 1 : hunk.newStart;
  const before = lines[at - 1];
  const after = lines[at + hunk.newLines];
  return {
    oldText: hunk.oldText,
    newText: hunk.newText,
    ...(before === undefined ? {} : { before }),
    ...(after === undefined ? {} : { after }),
  };
}

/**
 * `text` with the hunk put back, or `undefined` when the hunk's new text,
 * between its anchor lines, is no longer in `text` — something wrote over
 * it since, which is what a `stale` hunk records. Found by index and
 * spliced, never by `String.replace`, whose replacement patterns would
 * mangle the `$` LaTeX is full of.
 */
export function undoHunk(text: string, hunk: AnchoredHunk): string | undefined {
  const anchored = (middle: string) => (hunk.before ?? "") + middle + (hunk.after ?? "");
  const find = anchored(hunk.newText);
  const at = text.indexOf(find);
  if (at === -1) return undefined;
  return text.slice(0, at) + anchored(hunk.oldText) + text.slice(at + find.length);
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

/**
 * A pasted or dropped image: one sentence folding per-mode
 * vision instructions into a single choice the agent makes by looking at
 * the image, rather than a mode the researcher picks first.
 */
export function transcribePrompt(path: string, file: string, line: number): string {
  return (
    `Transcribe \`${path}\` into LaTeX at \`${file}\` line ${line}: a formula becomes an \`equation\`, ` +
    "a table becomes a `tabular` inside a `table` float, an algorithm becomes `algorithm`/`algorithmic`, " +
    "a commutative diagram becomes `tikz-cd`, printed text becomes clean text, and any other picture becomes " +
    "an `\\includegraphics` figure with a caption and a label."
  );
}
