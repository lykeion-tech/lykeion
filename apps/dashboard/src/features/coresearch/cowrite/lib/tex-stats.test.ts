import { describe, expect, it } from "vitest";
import { texStats, type TexStats } from "./tex-stats";

const zero: TexStats = {
  words: 0,
  wordsInText: 0,
  wordsInHeaders: 0,
  wordsOutsideText: 0,
  headers: 0,
  figures: 0,
  mathInlines: 0,
  mathDisplayed: 0,
};

const stats = (...sources: string[]) => texStats(sources);

describe("texStats", () => {
  it("is all zeros for no files and for an empty file", () => {
    expect(texStats([])).toEqual(zero);
    expect(stats("")).toEqual(zero);
  });

  it("counts body words, joining internal apostrophes and hyphens but not dashes", () => {
    // The | don't | stop | a | well-known | cat | pages | 1 | 2
    expect(stats("The   don't stop, a well-known cat; pages 1--2.").wordsInText).toBe(9);
  });

  it("counts letters of any script", () => {
    expect(stats("Ελληνικά λόγια über naïve").wordsInText).toBe(4);
  });

  it("does not count command names, but counts the arguments of formatting commands", () => {
    const s = stats("A \\emph{very} \\textbf{bold} \\textit{claim}\\noindent here");
    expect(s.wordsInText).toBe(5);
  });

  it("puts sectioning titles in headers, starred or not, and counts each one", () => {
    const s = stats(
      [
        "\\section{Intro and Aims}",
        "Body text here.",
        "\\subsection*{Two Words}",
        "\\section[Short]{The Long Title}",
        "\\paragraph{Para}",
      ].join("\n"),
    );
    expect(s.headers).toBe(4);
    // Intro and Aims (3) + Two Words (2) + The Long Title (3; the short form is skipped) + Para (1)
    expect(s.wordsInHeaders).toBe(9);
    expect(s.wordsInText).toBe(3);
    expect(s.words).toBe(12);
  });

  it("counts \\title words as header words without counting it as a header", () => {
    const s = stats("\\title{A Paper Title}\nText.");
    expect(s.wordsInHeaders).toBe(3);
    expect(s.headers).toBe(0);
    expect(s.wordsInText).toBe(1);
  });

  it("puts captions and footnotes outside the text", () => {
    const s = stats(
      "Main words\\footnote{a side note} continue.\n" +
        "\\begin{figure}[htbp]\\centering\\includegraphics[width=0.5\\textwidth]{plots/loss.pdf}" +
        "\\caption[Loss]{Training loss over time}\\label{fig:loss}\\end{figure}",
    );
    expect(s.wordsInText).toBe(3);
    // a side note (3) + Training loss over time (4)
    expect(s.wordsOutsideText).toBe(7);
    expect(s.figures).toBe(1);
    expect(s.words).toBe(10);
  });

  it("counts figure, figure* and wrapfigure, but not tables", () => {
    const s = stats(
      [
        "\\begin{figure}x\\end{figure}",
        "\\begin{figure*}x\\end{figure*}",
        "\\begin{wrapfigure}{r}{0.4\\textwidth}x\\end{wrapfigure}",
        "\\begin{table}x\\end{table}",
      ].join("\n"),
    );
    expect(s.figures).toBe(3);
    // One "x" in each environment; wrapfigure's placement and width are not words.
    expect(s.wordsInText).toBe(4);
  });

  it("counts inline math as $…$ and \\(…\\), and does not read words inside it", () => {
    const s = stats("Let $x in X$ and \\(y leq z\\) hold, costing \\$5.");
    expect(s.mathInlines).toBe(2);
    expect(s.mathDisplayed).toBe(0);
    // Let | and | hold | costing | 5
    expect(s.wordsInText).toBe(5);
  });

  it("counts displayed math as $$…$$, \\[…\\] and the display environments", () => {
    const s = stats(
      [
        "$$a + b$$",
        "\\[ c \\]",
        "\\begin{equation} e = mc^2 \\label{eq:e} \\end{equation}",
        "\\begin{align*} f &= g \\\\ h &= i \\end{align*}",
        "\\begin{gather}j\\end{gather}",
        "\\begin{multline*}k\\end{multline*}",
        "\\begin{eqnarray}l\\end{eqnarray}",
        "\\begin{displaymath}m\\end{displaymath}",
        "Words only.",
      ].join("\n"),
    );
    expect(s.mathDisplayed).toBe(8);
    expect(s.mathInlines).toBe(0);
    expect(s.wordsInText).toBe(2);
  });

  it("strips % comments but keeps \\% as a literal", () => {
    const s = stats("Kept words % dropped words here\n50\\% more % gone\n% whole line gone");
    // Kept | words | 50 | more
    expect(s.wordsInText).toBe(4);
  });

  it("treats \\\\% as a line break followed by a comment", () => {
    expect(stats("one\\\\% two three").wordsInText).toBe(1);
  });

  it("does not count math or sections that are commented out", () => {
    const s = stats("% \\section{Hidden} $x$ \\[y\\]\nVisible");
    expect(s).toEqual({ ...zero, words: 1, wordsInText: 1 });
  });

  it("reads only the document body when it is marked, except for \\title", () => {
    const s = stats(
      [
        "\\documentclass[11pt]{article}",
        "\\usepackage{amsmath}",
        "\\newcommand{\\R}{\\mathbb{R}}",
        "Stray preamble words $z$",
        "\\title{Deep Things}",
        "\\author{Ada Lovelace}",
        "\\begin{document}",
        "\\maketitle",
        "\\section{Start}",
        "Only these count.",
        "\\end{document}",
        "Trailing words after the end.",
      ].join("\n"),
    );
    expect(s.wordsInText).toBe(3);
    // Deep Things (2) + Start (1)
    expect(s.wordsInHeaders).toBe(3);
    expect(s.headers).toBe(1);
    expect(s.mathInlines).toBe(0);
    expect(s.words).toBe(6);
  });

  it("reads a file without document markers whole", () => {
    expect(stats("\\section{Chapter}\nThree words here.").words).toBe(4);
  });

  it("does not count the arguments of commands that name things", () => {
    const s = stats(
      [
        "See Figure~\\ref{fig:one} and Eq.~\\eqref{eq:two}",
        "as shown \\cite{smith2020} and \\citep[p.~5]{jones} or \\citet{doe}.",
        "\\label{sec:intro}\\input{chapters/intro}\\include{appendix}",
        "\\url{https://example.org} \\href{https://example.org/page}{the site}",
        "\\bibliographystyle{plain}\\bibliography{refs}",
        "\\begin{itemize}\\item one\\end{itemize}",
      ].join("\n"),
    );
    // See Figure and Eq | as shown and or | the site | one
    expect(s.wordsInText).toBe(4 + 4 + 2 + 1);
  });

  it("sums several files", () => {
    const s = stats(
      "\\begin{document}\\section{Intro}One two $a$ \\input{body}\\end{document}",
      "Three four five\\footnote{six}\n\\begin{figure}\\caption{seven}\\end{figure}\n\\[b\\]",
    );
    expect(s).toEqual({
      words: 8,
      wordsInText: 5,
      wordsInHeaders: 1,
      wordsOutsideText: 2,
      headers: 1,
      figures: 1,
      mathInlines: 1,
      mathDisplayed: 1,
    });
  });

  it("counts header words inside formatting and footnotes inside headers outside the text", () => {
    const s = stats("\\section{The \\emph{Big} Idea\\footnote{an aside}\\label{sec:big}}");
    expect(s.wordsInHeaders).toBe(3);
    expect(s.wordsOutsideText).toBe(2);
    expect(s.wordsInText).toBe(0);
  });

  it("does not read verbatim content", () => {
    const s = stats("Before \\verb|not $counted$| and\n\\begin{verbatim}\n$x$ words\n\\end{verbatim}\nafter");
    expect(s.wordsInText).toBe(3);
    expect(s.mathInlines).toBe(0);
  });
});
