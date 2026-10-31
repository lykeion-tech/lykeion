/**
 * A paper's word and element counts, in texcount's summary categories:
 *
 * - `wordsInText` — words of body text. Command names (`\foo`) are not words,
 *   comments are gone before counting, math is not read for words, and the
 *   arguments that belong to the categories below are counted there instead.
 *   The arguments of formatting commands (`\emph`, `\textbf`, `\textit`, and
 *   any command not named below) ARE text.
 * - `wordsInHeaders` — words inside a sectioning command's title (`\part`,
 *   `\chapter`, `\section`, `\subsection`, `\subsubsection`, `\paragraph`,
 *   `\subparagraph`, starred or not) and inside `\title`.
 * - `wordsOutsideText` — words in `\caption{…}` and `\footnote{…}`.
 * - `words` — the sum of those three.
 * - `headers` — how many sectioning commands there are; `\title` is not one.
 * - `figures` — how many `figure`, `figure*` and `wrapfigure` environments.
 *   Tables are not figures.
 * - `mathInlines` — `$…$` and `\(…\)` spans.
 * - `mathDisplayed` — `$$…$$`, `\[…\]`, and the display environments
 *   (`equation`, `align`, `gather`, `multline`, `eqnarray`, each starred or
 *   not, and `displaymath`).
 *
 * A word is a maximal run of letters and digits (any script), which may be
 * joined into one word by an internal apostrophe or hyphen: "don't" and
 * "well-known" are one word each, "1--2" is two.
 *
 * Only the document body is read — between `\begin{document}` and
 * `\end{document}` — when a file has those markers, since the preamble is
 * configuration rather than prose; a file without them (an `\input` chapter)
 * is read whole. The one exception is `\title`, which lives in the preamble
 * by convention and is still the paper's heading, so its words are counted
 * wherever it stands.
 *
 * `%` starts a comment to the end of the line; `\%` is a literal percent
 * sign and starts nothing.
 *
 * Arguments that name things rather than say them are not words: labels and
 * references, citation keys, packages and classes, environment names, file
 * paths and URLs, lengths. `\href`'s first argument is its URL, so only its
 * second is read.
 */
export interface TexStats {
  words: number;
  wordsInText: number;
  wordsInHeaders: number;
  wordsOutsideText: number;
  headers: number;
  figures: number;
  mathInlines: number;
  mathDisplayed: number;
}

/** Sums the counts over several `.tex` files' texts — a paper split across
 *  `\input`s is still one paper. */
export function texStats(sources: string[]): TexStats {
  const out: TexStats = {
    words: 0,
    wordsInText: 0,
    wordsInHeaders: 0,
    wordsOutsideText: 0,
    headers: 0,
    figures: 0,
    mathInlines: 0,
    mathDisplayed: 0,
  };
  for (const source of sources) countFile(source, out);
  out.words = out.wordsInText + out.wordsInHeaders + out.wordsOutsideText;
  return out;
}

type Where = "text" | "header" | "outside";

const SECTIONS = new Set([
  "part",
  "chapter",
  "section",
  "subsection",
  "subsubsection",
  "paragraph",
  "subparagraph",
]);

const FIGURES = new Set(["figure", "figure*", "wrapfigure"]);

const DISPLAY_MATH = new Set([
  "equation",
  "equation*",
  "align",
  "align*",
  "gather",
  "gather*",
  "multline",
  "multline*",
  "eqnarray",
  "eqnarray*",
  "displaymath",
]);

/** Environments whose content is not prose and is not LaTeX to be read
 *  either — skipped whole, to their `\end`. */
const OPAQUE = new Set(["verbatim", "verbatim*", "lstlisting", "minted", "comment"]);

/** How many mandatory arguments an environment takes after its name that are
 *  layout (a placement, a width, a column spec) rather than words. */
const ENV_ARGS: Record<string, number> = {
  wrapfigure: 2,
  tabular: 1,
  "tabular*": 2,
  tabularx: 2,
  array: 1,
  minipage: 1,
  thebibliography: 1,
};

/** Commands whose leading mandatory arguments are names, keys, paths or
 *  lengths — never words — and how many of them there are. Any optional
 *  `[…]` arguments before each are skipped with them. */
const NON_TEXT: Record<string, number> = {
  label: 1,
  ref: 1,
  eqref: 1,
  pageref: 1,
  autoref: 1,
  nameref: 1,
  cref: 1,
  Cref: 1,
  cite: 1,
  citep: 1,
  citet: 1,
  citealp: 1,
  citealt: 1,
  citeauthor: 1,
  citeyear: 1,
  parencite: 1,
  textcite: 1,
  autocite: 1,
  nocite: 1,
  bibitem: 1,
  usepackage: 1,
  RequirePackage: 1,
  documentclass: 1,
  includegraphics: 1,
  graphicspath: 1,
  bibliography: 1,
  bibliographystyle: 1,
  addbibresource: 1,
  input: 1,
  include: 1,
  url: 1,
  href: 1,
  hyperref: 0,
  printbibliography: 0,
  vspace: 1,
  hspace: 1,
  setlength: 2,
  addtolength: 2,
  setcounter: 2,
  color: 1,
  textcolor: 1,
  newcommand: 2,
  renewcommand: 2,
  author: 1,
  date: 1,
};

const WORD = /[\p{L}\p{N}][\p{L}\p{N}\p{M}]*(?:['’-][\p{L}\p{N}][\p{L}\p{N}\p{M}]*)*/uy;
const TITLE = /\\title(?![A-Za-z@])/g;

function countFile(source: string, out: TexStats): void {
  const text = stripComments(source);
  const begin = text.indexOf("\\begin{document}");
  if (begin < 0) {
    new Scanner(text, out).body();
    return;
  }
  const start = begin + "\\begin{document}".length;
  const end = text.indexOf("\\end{document}", start);
  new Scanner(text.slice(start, end < 0 ? undefined : end), out).body();

  const preamble = text.slice(0, begin);
  const scanner = new Scanner(preamble, out);
  for (const m of preamble.matchAll(TITLE)) scanner.commandAt(m.index);
}

/** Drops each line from its first `%` that is not escaped — an odd run of
 *  backslashes before it makes it a literal, an even run (`\\%`) is a line
 *  break followed by a comment. */
function stripComments(source: string): string {
  return source
    .split("\n")
    .map((line) => {
      for (let i = line.indexOf("%"); i >= 0; i = line.indexOf("%", i + 1)) {
        let slashes = 0;
        for (let j = i - 1; j >= 0 && line[j] === "\\"; j--) slashes++;
        if (slashes % 2 === 0) return line.slice(0, i);
      }
      return line;
    })
    .join("\n");
}

class Scanner {
  private i = 0;

  constructor(
    private readonly s: string,
    private readonly out: TexStats,
  ) {}

  body(): void {
    this.i = 0;
    this.run("text", false);
  }

  /** Reads the one command starting at `at`, with its arguments. */
  commandAt(at: number): void {
    this.i = at;
    this.command();
  }

  /** Reads until the end, or — when `group` — until the `}` that closes the
   *  group just entered. */
  private run(where: Where, group: boolean): void {
    const { s } = this;
    while (this.i < s.length) {
      const c = s[this.i];
      if (c === "}") {
        this.i++;
        if (group) return;
      } else if (c === "{") {
        this.i++;
        this.run(where, true);
      } else if (c === "\\") {
        this.command();
      } else if (c === "$") {
        this.dollarMath();
      } else {
        WORD.lastIndex = this.i;
        const m = WORD.exec(s);
        if (m) {
          this.i += m[0].length;
          this.word(where);
        } else {
          this.i++;
        }
      }
    }
  }

  private word(where: Where): void {
    if (where === "text") this.out.wordsInText++;
    else if (where === "header") this.out.wordsInHeaders++;
    else this.out.wordsOutsideText++;
  }

  private command(): void {
    const { s } = this;
    this.i++;
    if (this.i >= s.length) return;
    const first = s[this.i];
    if (!/[A-Za-z@]/.test(first)) {
      this.i++;
      if (first === "(") {
        this.skipPast("\\)");
        this.out.mathInlines++;
      } else if (first === "[") {
        this.skipPast("\\]");
        this.out.mathDisplayed++;
      } else if (first === "\\") {
        // A line break's optional spacing (`\\[2pt]`) is a length.
        this.skipOptionals();
      }
      return;
    }
    const start = this.i;
    while (this.i < s.length && /[A-Za-z@]/.test(s[this.i])) this.i++;
    const name = s.slice(start, this.i);
    if (s[this.i] === "*") this.i++;

    if (SECTIONS.has(name)) {
      this.out.headers++;
      this.skipOptionals();
      this.argument("header");
    } else if (name === "title") {
      this.skipOptionals();
      this.argument("header");
    } else if (name === "caption" || name === "footnote") {
      // A caption's optional argument is the short form for the list of
      // figures — the same words again, not more of them.
      this.skipOptionals();
      this.argument("outside");
    } else if (name === "begin") {
      this.begin(this.envName());
    } else if (name === "end") {
      this.envName();
    } else if (name === "verb") {
      const delimiter = s[this.i];
      const close = delimiter === undefined ? -1 : s.indexOf(delimiter, this.i + 1);
      this.i = close < 0 ? s.length : close + 1;
    } else if (name in NON_TEXT) {
      for (let k = 0; k < NON_TEXT[name]; k++) {
        this.skipOptionals();
        this.skipGroup();
      }
      this.skipOptionals();
    }
    // Any other command is formatting: its arguments are read as the text
    // around it, which the loop does on its own when it meets the `{`.
  }

  private begin(env: string): void {
    if (DISPLAY_MATH.has(env)) {
      this.out.mathDisplayed++;
      this.skipPast(`\\end{${env}}`);
      return;
    }
    if (OPAQUE.has(env)) {
      this.skipPast(`\\end{${env}}`);
      return;
    }
    if (FIGURES.has(env)) this.out.figures++;
    this.skipOptionals();
    for (let k = 0; k < (ENV_ARGS[env] ?? 0); k++) {
      this.skipOptionals();
      this.skipGroup();
    }
  }

  private argument(where: Where): void {
    this.skipSpace();
    if (this.s[this.i] !== "{") return;
    this.i++;
    this.run(where, true);
  }

  private envName(): string {
    this.skipSpace();
    if (this.s[this.i] !== "{") return "";
    const close = this.s.indexOf("}", this.i);
    if (close < 0) {
      this.i = this.s.length;
      return "";
    }
    const name = this.s.slice(this.i + 1, close).trim();
    this.i = close + 1;
    return name;
  }

  private dollarMath(): void {
    const { s } = this;
    if (s[this.i + 1] === "$") {
      this.i += 2;
      this.skipPastUnescaped("$$");
      this.out.mathDisplayed++;
    } else {
      this.i++;
      this.skipPastUnescaped("$");
      this.out.mathInlines++;
    }
  }

  private skipPast(marker: string): void {
    const at = this.s.indexOf(marker, this.i);
    this.i = at < 0 ? this.s.length : at + marker.length;
  }

  /** Like `skipPast`, but a `\$` inside the math is a dollar sign rather
   *  than the math's end. */
  private skipPastUnescaped(marker: string): void {
    const { s } = this;
    for (let at = s.indexOf(marker, this.i); at >= 0; at = s.indexOf(marker, at + 1)) {
      let slashes = 0;
      for (let j = at - 1; j >= 0 && s[j] === "\\"; j--) slashes++;
      if (slashes % 2 === 0) {
        this.i = at + marker.length;
        return;
      }
    }
    this.i = s.length;
  }

  private skipSpace(): void {
    while (this.i < this.s.length && /\s/.test(this.s[this.i])) this.i++;
  }

  private skipOptionals(): void {
    for (;;) {
      const save = this.i;
      this.skipSpace();
      if (this.s[this.i] !== "[") {
        this.i = save;
        return;
      }
      this.skipBalanced("[", "]");
    }
  }

  /** Skips one argument unread: a braced group, or — when the argument was
   *  written unbraced, as in `\input\chapter` — the one command token that
   *  stands for it. */
  private skipGroup(): void {
    this.skipSpace();
    const c = this.s[this.i];
    if (c === "{") this.skipBalanced("{", "}");
    else if (c === "\\") {
      this.i++;
      while (this.i < this.s.length && /[A-Za-z@]/.test(this.s[this.i])) this.i++;
    }
  }

  private skipBalanced(open: string, close: string): void {
    const { s } = this;
    let depth = 0;
    while (this.i < s.length) {
      const c = s[this.i];
      if (c === "\\") {
        this.i += 2;
        continue;
      }
      this.i++;
      if (c === open) depth++;
      else if (c === close && --depth === 0) return;
    }
  }
}
