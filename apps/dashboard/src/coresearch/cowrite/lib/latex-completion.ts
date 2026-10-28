/**
 * What the editor offers as a researcher types LaTeX.
 *
 * Three sources in one, because they are told apart by what is under the
 * cursor rather than by three separate extensions asking the same question:
 * a `\cite`-family argument completes cite keys, a `\ref`-family argument
 * completes the paper's labels, and a bare backslash anywhere else offers
 * the commands a paper is written with.
 *
 * The cite keys come from two places at once. The paper's own `.bib` is the
 * first group, and the Library — the Research's references — is the second:
 * picking one of those is a citation of something this Research has read but
 * this paper has not cited yet, so the pick also appends the entry to the
 * `.bib`, which is what `onAppendBib` is for.
 *
 * Every source is read through a function rather than taken as a value: the
 * extension is built once for the life of an editor, and the paper's keys,
 * rows and labels all change under it.
 */
import type { Completion, CompletionContext, CompletionResult, CompletionSource } from "@codemirror/autocomplete";
import { citeCommandAt, type ReferenceRow } from "@lykeion/api";

/** The `\ref` family, as the argument a label completes inside. */
const REF_AT = /\\(?:ref|eqref|autoref)\*?\{([^}]*)$/;

/** Where a snippet leaves the cursor. */
const CARET = "\u2038";

/** A command being typed: the backslash and whatever letters follow it. */
const COMMAND_AT = /\\[A-Za-z]*$/;

/** The key being typed, as far as it may run before it is another key or the
 *  end of the argument — what keeps a completion open while it is typed. */
const KEY_VALID = /^[^{},\s]*$/;

/**
 * A command snippet, with `\u2038` marking where the cursor lands. The marker
 * is not part of what is inserted, and it is a character no LaTeX source can
 * hold, so nothing a snippet really writes can be mistaken for it; a snippet
 * without one leaves the cursor at the end.
 */
function snippet(label: string, text: string): Completion {
  const caret = text.indexOf(CARET);
  const insert = caret === -1 ? text : text.replace(CARET, "");
  return {
    label,
    type: "keyword",
    apply: (view, _completion, from, to) => {
      view.dispatch({
        changes: { from, to, insert },
        selection: { anchor: from + (caret === -1 ? insert.length : caret) },
      });
    },
  };
}

/** What a paper is written with, in the order a writer reaches for them. */
const COMMANDS: Completion[] = [
  snippet("\\section{}", "\\section{\u2038}"),
  snippet("\\subsection{}", "\\subsection{\u2038}"),
  snippet("\\cite{}", "\\cite{\u2038}"),
  snippet("\\ref{}", "\\ref{\u2038}"),
  snippet("\\label{}", "\\label{\u2038}"),
  snippet(
    "\\begin{figure}",
    "\\begin{figure}[t]\n\\centering\n\\includegraphics[width=0.9\\linewidth]{\u2038}\n\\caption{}\n\\label{fig:}\n\\end{figure}\n",
  ),
  snippet(
    "\\begin{table}",
    "\\begin{table}[t]\n\\centering\n\\begin{tabular}{\u2038}\n\\end{tabular}\n\\caption{}\n\\label{tab:}\n\\end{table}\n",
  ),
  snippet("\\begin{itemize}", "\\begin{itemize}\n\\item \u2038\n\\end{itemize}\n"),
  snippet("\\begin{equation}", "\\begin{equation}\n\u2038\n\\label{eq:}\n\\end{equation}\n"),
  snippet("\\begin{tikzcd}", "\\begin{tikzcd}\n\u2038\n\\end{tikzcd}\n"),
];

export interface LatexCompletionSources {
  /** The keys of the paper's own `.bib` files, each with its title. */
  bibKeys: () => { key: string; title: string }[];
  /** The Research's references, for the keys this paper has not cited yet. */
  libraryRows: () => ReferenceRow[];
  /** Every `\label{…}` across the paper's text files. */
  labels: () => string[];
  /** A Library row was cited: its entry belongs in the paper's `.bib` now. */
  onAppendBib: (row: ReferenceRow) => void;
}

export function latexCompletions(opts: LatexCompletionSources): CompletionSource {
  return (context: CompletionContext): CompletionResult | null => {
    // The cursor's own line, never the whole paper: both of the commands
    // looked for are written on one line, and this is asked on every
    // keystroke.
    const line = context.state.doc.lineAt(context.pos);
    const at = context.pos - line.from;

    const cite = citeCommandAt(line.text, at);
    if (cite !== undefined)
      return { from: line.from + cite.from, options: citeOptions(opts), validFor: KEY_VALID };

    const ref = refCommandAt(line.text, at);
    if (ref !== undefined)
      return {
        from: line.from + ref.from,
        options: opts.labels().map((label) => ({ label, type: "variable" })),
        validFor: KEY_VALID,
      };

    const command = context.matchBefore(COMMAND_AT);
    if (command === null) return null;
    return { from: command.from, options: COMMANDS, validFor: COMMAND_AT };
  };
}

/** The paper's own keys first, then what the Library holds beyond them. */
function citeOptions(opts: LatexCompletionSources): Completion[] {
  const own = opts.bibKeys();
  const cited = new Set(own.map((entry) => entry.key));
  const options: Completion[] = own.map((entry) => ({
    label: entry.key,
    type: "variable",
    // A draft entry a writer has not titled yet completes all the same; it
    // simply has no detail line to carry.
    ...(entry.title === "" ? {} : { detail: entry.title }),
    boost: 1,
  }));
  for (const row of opts.libraryRows()) {
    const key = row.reference.citeKey;
    if (cited.has(key)) continue;
    options.push({
      label: key,
      type: "variable",
      detail: "Library",
      info: row.paper.title,
      apply: (view, _completion, from, to) => {
        view.dispatch({ changes: { from, to, insert: key }, selection: { anchor: from + key.length } });
        opts.onAppendBib(row);
      },
    });
  }
  return options;
}

/**
 * The `\ref`-family argument the cursor is in, and where the label being
 * typed starts. Read over the one line, the same bound `citeCommandAt`
 * reads a cite argument by.
 */
function refCommandAt(line: string, pos: number): { from: number } | undefined {
  const match = REF_AT.exec(line.slice(0, pos));
  if (match === null) return undefined;
  return { from: pos - match[1]!.length };
}
