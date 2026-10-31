import { expect, it, vi } from "vitest";
import { EditorState } from "@codemirror/state";
import { CompletionContext, type Completion, type CompletionResult } from "@codemirror/autocomplete";
import type { ReferenceRow } from "@lykeion/api";
import { latexCompletions } from "./latex-completion";

/** A Library row as `listReferences` answers one, with only the fields the
 *  completion reads filled in. */
function row(citeKey: string, title: string): ReferenceRow {
  return {
    reference: {
      id: `ref_${citeKey}`,
      researchId: "r_1",
      paperId: `p_${citeKey}`,
      citeKey,
      addedBy: { kind: "import" },
      addedTs: 0,
      readBy: [],
    },
    paper: {
      id: `p_${citeKey}`,
      ids: {},
      title,
      authors: [],
      metadata: { source: "import", ts: 0 },
      titleKey: title,
      createdTs: 0,
      updatedTs: 0,
    },
  };
}

function completeAt(
  doc: string,
  at: number,
  opts: Partial<Parameters<typeof latexCompletions>[0]> = {},
): CompletionResult | null {
  const source = latexCompletions({
    bibKeys: () => [{ key: "smith2020", title: "On rates" }],
    libraryRows: () => [row("jones2019", "A colder run"), row("smith2020", "On rates")],
    labels: () => ["eq:rate", "fig:one"],
    onAppendBib: vi.fn(),
    ...opts,
  });
  const state = EditorState.create({ doc });
  return source(new CompletionContext(state, at, false)) as CompletionResult | null;
}

const labels = (result: CompletionResult | null) => (result?.options ?? []).map((o) => o.label);
const option = (result: CompletionResult | null, label: string): Completion | undefined =>
  result?.options.find((o) => o.label === label);

it("offers the file's own cite keys and the Library's, and says which came from the Library", () => {
  // Not on the first line: the source reads the cursor's line rather than
  // the whole paper, so where the key starts is that line's offset plus its
  // place in it.
  const doc = "\\section{Rates}\nas shown in \\cite{smi";
  const result = completeAt(doc, doc.length);

  expect(result?.from).toBe(doc.indexOf("smi"));
  expect(labels(result)).toContain("smith2020");
  expect(labels(result)).toContain("jones2019");
  expect(option(result, "jones2019")?.detail).toBe("Library");
  // Already in the `.bib`, so it is the file's row and not a second one
  // offering to add what the file has.
  expect(option(result, "smith2020")?.detail).toBe("On rates");
  expect(labels(result).filter((l) => l === "smith2020")).toHaveLength(1);
});

it("adds the Library's entry to the paper's bibliography when its key is picked", () => {
  const onAppendBib = vi.fn();
  const doc = "\\citep{jon";
  const picked = option(completeAt(doc, doc.length, { onAppendBib }), "jones2019");

  const dispatched: unknown[] = [];
  const view = { dispatch: (spec: unknown) => dispatched.push(spec) };
  expect(typeof picked?.apply).toBe("function");
  (picked!.apply as (v: unknown, c: Completion, from: number, to: number) => void)(
    view,
    picked!,
    "\\citep{".length,
    doc.length,
  );

  expect(onAppendBib).toHaveBeenCalledTimes(1);
  expect(onAppendBib.mock.calls[0]![0]).toMatchObject({ reference: { citeKey: "jones2019" } });
  expect(dispatched).toEqual([
    {
      changes: { from: "\\citep{".length, to: doc.length, insert: "jones2019" },
      selection: { anchor: "\\citep{".length + "jones2019".length },
    },
  ]);
});

it("offers the paper's labels inside a reference, and nothing outside one", () => {
  const doc = "first\nsee \\ref{eq";
  expect(labels(completeAt(doc, doc.length))).toEqual(["eq:rate", "fig:one"]);
  expect(completeAt(doc, doc.length)?.from).toBe(doc.indexOf("eq", "first\n".length));
  expect(labels(completeAt("see \\eqref{", "see \\eqref{".length))).toEqual(["eq:rate", "fig:one"]);
  expect(labels(completeAt("see \\autoref{", "see \\autoref{".length))).toEqual(["eq:rate", "fig:one"]);
  // A positive first, so the assertion below is about the position and not
  // about a source that answers nothing wherever it is asked.
  expect(completeAt("plain prose here", "plain prose here".length)).toBeNull();
});

it("offers the command snippets after a backslash, and puts the cursor where the writing goes on", () => {
  const doc = "\\sec";
  const result = completeAt(doc, doc.length);
  expect(result?.from).toBe(0);
  expect(labels(result)).toEqual([
    "\\section{}",
    "\\subsection{}",
    "\\cite{}",
    "\\ref{}",
    "\\label{}",
    "\\begin{figure}",
    "\\begin{table}",
    "\\begin{itemize}",
    "\\begin{equation}",
    "\\begin{tikzcd}",
  ]);

  const dispatched: { changes: { insert: string }; selection: { anchor: number } }[] = [];
  const view = { dispatch: (spec: never) => dispatched.push(spec) };
  const section = option(result, "\\section{}")!;
  (section.apply as (v: unknown, c: Completion, from: number, to: number) => void)(view, section, 0, doc.length);
  expect(dispatched[0]?.changes.insert).toBe("\\section{}");
  expect(dispatched[0]?.selection.anchor).toBe("\\section{".length);

  const figure = option(result, "\\begin{figure}")!;
  (figure.apply as (v: unknown, c: Completion, from: number, to: number) => void)(view, figure, 0, doc.length);
  expect(dispatched[1]?.changes.insert).toContain("\\includegraphics[width=0.9\\linewidth]{}");
  expect(dispatched[1]?.changes.insert).toContain("\\end{figure}");
  // Inside the braces of `\includegraphics`, which is the first thing a
  // figure needs and the one place the cursor is worth putting.
  expect(dispatched[1]!.changes.insert.slice(0, dispatched[1]!.selection.anchor)).toMatch(
    /\\includegraphics\[width=0\.9\\linewidth\]\{$/,
  );
});
