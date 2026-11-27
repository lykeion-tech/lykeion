import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { bibKeysOf, citeCommandAt, compileManuscriptNotes, forwardSearch, labelsOf, outlineOf, parseSynctex, inverseSearch, pdfPointsToSynctex, synctexToPdfPoints, parseTexLog, proofreadPrompt } from "./manuscript";

const fixture = (name: string) => readFileSync(join(import.meta.dirname, "__fixtures__", "manuscript", name), "utf8");


describe("bibKeysOf / citeCommandAt / labelsOf / outlineOf", () => {
  it("reads keys and titles out of a .bib", () => {
    const bib = "@article{smith2020,\n title={A Title},\n year={2020}\n}\n@book{jones,title={Book}}";
    expect(bibKeysOf(bib)).toEqual([{ key: "smith2020", title: "A Title" }, { key: "jones", title: "Book" }]);
  });
  it("keeps a title-less draft entry's key, with an empty title, instead of dropping it with parseBibtex's rejection", () => {
    const bib = "@article{smith2020,title={Has Title}}\n@misc{draft2021,note={no title yet}}";
    expect(bibKeysOf(bib)).toEqual([
      { key: "smith2020", title: "Has Title" },
      { key: "draft2021", title: "" },
    ]);
  });
  it("keeps a repeated key once, with the titled entry's title, whichever entry the file lists first", () => {
    const titledFirst = "@article{smith2020,title={Real Title}}\n@article{smith2020,year={2020}}";
    const titlelessFirst = "@article{smith2020,year={2020}}\n@article{smith2020,title={Real Title}}";
    expect(bibKeysOf(titledFirst)).toEqual([{ key: "smith2020", title: "Real Title" }]);
    expect(bibKeysOf(titlelessFirst)).toEqual([{ key: "smith2020", title: "Real Title" }]);
  });
  it("finds the cite command and the partial key under the cursor, for every command in the family", () => {
    for (const cmd of ["cite", "citep", "citet", "parencite", "textcite", "citealp"]) {
      const doc = `see \\${cmd}{smi`;
      expect(citeCommandAt(doc, doc.length)).toEqual({ command: cmd, from: doc.length - 3, partial: "smi" });
    }
    expect(citeCommandAt("see \\cite{a,b", 13)).toEqual({ command: "cite", from: 12, partial: "b" });
    expect(citeCommandAt("\\cite[p.~3]{smi", 15)).toEqual({ command: "cite", from: 12, partial: "smi" });
    expect(citeCommandAt("plain text", 5)).toBeUndefined();
    expect(citeCommandAt("\\cite{done} more", 16)).toBeUndefined();
  });
  it("collects labels across files, once each", () => {
    expect(labelsOf([{ path: "main.tex", text: "\\label{eq:1} \\label{fig:a}" }, { path: "b.tex", text: "\\label{eq:1}" }])).toEqual(["eq:1", "fig:a"]);
  });
  it("outlines sectioning commands with their lines and skips comments", () => {
    const tex = "\\section{Intro}\n% \\section{Not this}\ntext\n\\subsection{Sub}\n\\chapter{Ch}";
    expect(outlineOf(tex)).toEqual([
      { level: 2, title: "Intro", line: 1 },
      { level: 3, title: "Sub", line: 4 },
      { level: 1, title: "Ch", line: 5 },
    ]);
  });
});

describe("parseTexLog", () => {
  it("a missing package names the file, the line and the message", () => {
    const { errors } = parseTexLog(fixture("missing-package.log.txt"));
    expect(errors[0]).toEqual({ file: "main.tex", line: 4, message: "LaTeX Error: File `nosuch.sty' not found." });
    expect(errors.some((e) => e.message === "Emergency stop.")).toBe(true);
  });
  it("an undefined control sequence has its l. line, and warnings are counted", () => {
    const parsed = parseTexLog(fixture("undefined-cs.log.txt"));
    expect(parsed.errors).toEqual([{ file: "main.tex", line: 14, message: "Undefined control sequence." }]);
    expect(parsed.warnings).toBe(2);
  });
  it("an overfull box and an underfull one are warnings, not errors", () => {
    const parsed = parseTexLog(fixture("overfull.log.txt"));
    expect(parsed.errors).toEqual([]);
    expect(parsed.warnings).toBe(3);
  });
  it("an error inside a nested \\input is attributed to the inner file", () => {
    const { errors } = parseTexLog(fixture("nested-input.log.txt"));
    expect(errors).toEqual([{ file: "sections/intro.tex", line: 3, message: "Missing $ inserted." }]);
  });
  it("an error while a non-.tex file is open is attributed to that file, not the enclosing .tex", () => {
    const log = "(./main.tex (./elsarticle.cls\n! Undefined control sequence.\nl.512 \\foo\n";
    const { errors } = parseTexLog(log);
    expect(errors).toEqual([{ file: "elsarticle.cls", line: 512, message: "Undefined control sequence." }]);
  });

  it("names the bibliography BibTeX wrote when the error is in it, not the manuscript that pulled it in", () => {
    // Captured from a real compile of the ACL skeleton against an empty
    // `refs.bib`: BibTeX writes a `thebibliography` with no entries and
    // LaTeX refuses it. The error is at line 4 of `main.bbl` — a generated
    // file — and reading it as line 4 of `main.tex` sends a reader to a
    // line of their own manuscript that has nothing to do with it.
    const { errors } = parseTexLog(fixture("empty-bibliography.log.txt"));
    expect(errors).toEqual([
      { file: "main.bbl", line: 4, message: "LaTeX Error: Something's wrong--perhaps a missing \\item." },
    ]);
  });
});

describe("parseSynctex / forwardSearch", () => {
  it("reads x/h/v records with their file and line, and finds the first record at or after a line", () => {
    const records = parseSynctex(fixture("sample.synctex"));
    expect(records).toHaveLength(4);
    expect(records[2]).toEqual({ page: 1, x: 4736286, y: 6341104, file: "main.tex", line: 7 });
    expect(records[3]!.file).toBe("sections/intro.tex");
    expect(forwardSearch(records, "main.tex", 5)!.line).toBe(7);
    expect(forwardSearch(records, "main.tex", 8)).toBeUndefined();
    expect(forwardSearch(records, "sections/intro.tex", 1)!.line).toBe(3);
  });

  it("finds a manuscript path in a real Tectonic file, whose inputs are the build tree's absolute paths", () => {
    // Captured from a real compile. Tectonic names each input by the
    // absolute path of the tree it compiled — the machine's own build
    // directory, which nothing outside that machine knows — where the
    // paper, and everything asking for a record, knows only `main.tex`.
    // So a search matches a record whose file IS the path asked for or
    // ends at it, and never a file that merely ends in the same letters.
    const records = parseSynctex(fixture("tectonic.synctex"));
    const tree = "/private/var/folders/m0/v4ppt1zx44lcjjqn2zbyjgrm0000gn/T/lyk-cap-work-s5FPRc/studies/s_1/tasks/t_1/builds/build_1/manuscript";
    expect(records[0]).toEqual({ page: 1, x: 4736288, y: 3097887, file: `${tree}/main.tex`, line: 32 });

    // The lowest line at or after the one asked for, not the first record
    // in file order — the page lays the title block (line 32) before the
    // body it typesets from line 20.
    expect(forwardSearch(records, "main.tex", 1)!.line).toBe(20);
    expect(forwardSearch(records, "main.tex", 21)!.line).toBe(32);
    expect(forwardSearch(records, "main.tex", 33)).toBeUndefined();
    // A file reached through `\input`, named by the path the paper holds.
    expect(forwardSearch(records, "sections/intro.tex", 1)!.line).toBe(2);
    // The absolute path itself still matches, since that is what the
    // record says, and answers exactly what the paper's own name does.
    expect(forwardSearch(records, `${tree}/main.tex`, 1)).toEqual(forwardSearch(records, "main.tex", 1));
    // A tail of the path that is not a whole segment matches nothing: the
    // boundary is a separator, never a substring — so `in.tex` finds
    // neither `main.tex` nor `intro.tex`, while the file's own bare name
    // does find it, being the last segment of a path that ends there.
    expect(forwardSearch(records, "in.tex", 1)).toBeUndefined();
    expect(forwardSearch(records, "tro.tex", 1)).toBeUndefined();
    expect(forwardSearch(records, "intro.tex", 1)!.line).toBe(2);
  });
});

describe("forwardSearch on a whole Tectonic file", () => {
  // An untrimmed file from a real compile of this paper:
  //   19 \begin{document}   20 \maketitle   22 \section{Introduction}
  //   23 Hello              24 (blank — ends Hello's paragraph)   28 \end{document}
  // TeX writes most of a page as box, glue and kern records — `(`, `g`,
  // `k`, `$` — and files a paragraph under the line that ENDS it. A reader
  // of `x`/`h`/`v` alone sees two lines of this paper, 20 and 28.
  const records = parseSynctex(fixture("tectonic-body.synctex"));
  const pt = (sp: number) => sp / 65536;

  it("reads every positioned record, not only x/h/v", () => {
    const lines = new Set(records.map((r) => r.line));
    expect([...lines].sort((a, b) => a - b)).toEqual(expect.arrayContaining([20, 22, 24, 28]));
    // Every record line but the vertical boxes `[`, which are regions (the
    // page itself among them), not points.
    const text = fixture("tectonic-body.synctex");
    const placed = text.split("\n").filter((l) => /^[xhvgk$r(]1,\d+:/.test(l)).length;
    expect(records.filter((r) => r.file.endsWith("/main.tex"))).toHaveLength(placed);
    // Line 28 also owns the page-number box at the foot of the page; the
    // search lands at the top of its material all the same.
    expect(records.some((r) => r.line === 28 && pt(r.y) > 700)).toBe(true);
    expect(pt(forwardSearch(records, "main.tex", 25)!.y)).toBeLessThan(100);
  });

  it("lands on the section heading from its own line, and on the paragraph from a line inside it", () => {
    const heading = forwardSearch(records, "main.tex", 22)!;
    expect(heading.line).toBe(22);
    expect(pt(heading.y)).toBeCloseTo(273.2, 0);
    // Hello's paragraph ends at the blank line 24, and that is its record.
    const hello = forwardSearch(records, "main.tex", 23)!;
    expect(hello.line).toBe(24);
    expect(pt(hello.y)).toBeCloseTo(311.2, 0);
  });

  it("takes the top of the line's material, so a title block is marked where it starts", () => {
    // \maketitle is a stack of lines; its first record down the page is
    // where it begins, not whichever record the file happens to list first.
    const title = forwardSearch(records, "main.tex", 20)!;
    const ys = records.filter((r) => r.line === 20 && r.page === title.page).map((r) => r.y);
    expect(title.y).toBe(Math.min(...ys));
    // The preamble has no material; its nearest typeset line is the title.
    expect(forwardSearch(records, "main.tex", 17)!.line).toBe(20);
  });

  it("finds the source line under a point of the page, the way back from the PDF", () => {
    // Just under the heading and just over the paragraph: each answers its own.
    expect(inverseSearch(records, 1, pdfPointsToSynctex(270))!.line).toBe(22);
    expect(inverseSearch(records, 1, pdfPointsToSynctex(312))!.line).toBe(24);
    expect(inverseSearch(records, 9, pdfPointsToSynctex(300))).toBeUndefined();
  });

  it("converts between TeX's points and the PDF's, which differ by 72.27 to 72", () => {
    expect(synctexToPdfPoints(65536 * 72.27)).toBeCloseTo(72, 6);
    expect(synctexToPdfPoints(pdfPointsToSynctex(311))).toBeCloseTo(311, 6);
  });
});

describe("the prompts", () => {
  it("the proofread prompt names the file and the line", () => {
    expect(proofreadPrompt("main.tex", 41, "teh cat")).toContain('`main.tex` (line 41): "teh cat"');
  });
  it("compiles notes in file-and-line order, comment and edit each in their own shape, free text last", () => {
    const text = compileManuscriptNotes(
      [
        { id: "b", anchor: { path: "main.tex", quote: "second", from: "", to: "", line: 58 }, kind: "edit", text: "make this formal" },
        { id: "a", anchor: { path: "main.tex", quote: "the square below commutes", from: "", to: "", line: 41 }, kind: "comment", text: "needs a citation" },
      ],
      "Also fix the title.",
    );
    expect(text).toBe(
      [
        "2 notes on the paper.",
        "",
        '1. main.tex:41 — on "the square below commutes" — needs a citation',
        '2. main.tex:58 — on "second" — edit: make this formal',
        "",
        "Also fix the title.",
      ].join("\n"),
    );
    expect(compileManuscriptNotes([], "just this")).toBe("just this");
  });
});
