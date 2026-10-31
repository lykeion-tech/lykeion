/**
 * What the LaTeX in a paper is painted with.
 *
 * `StreamLanguage.define(stex)` produces the tokens, but a token nothing
 * styles is drawn in the editor's own colour — which is how a manuscript came
 * to read as one flat wall of grey while every other code surface in the lab
 * was highlighted. A highlight style is the missing half, and it has to be
 * asked for: the composed extension list here does not include `basicSetup`,
 * which is where a default one would otherwise have come from.
 *
 * The style names classes rather than colours. A colour written here would be
 * one colour, and this lab is themed — four seeds, light and dark — so the
 * palette belongs in the stylesheet beside every other themed thing, and this
 * module's job is only to say which token is which kind of thing.
 *
 * Every rule below names a tag the INSTALLED `stex` mode actually emits:
 * `tag`, `keyword`, `atom`, `string`, `bracket`, `comment`, `number`,
 * `builtin` and `error`, which `StreamLanguage` maps onto the lezer tags
 * used here. A rule for a tag the mode never produces is a silent no-op that
 * reads, to the next person, like a decision — so there are none.
 */
import { HighlightStyle } from "@codemirror/language";
import { tags } from "@lezer/highlight";

export const latexHighlight = HighlightStyle.define([
  // `\section`, `\usepackage`, `\begin` — the commands, which is what a
  // reader scans a source for. The mode calls the general case `tag` and
  // keeps `keyword` for the ones it knows by name, including the maths
  // delimiters: `$` and `\[` arrive here rather than under a tag of their
  // own, so mathematics takes the command colour and does not get one.
  { tag: tags.tagName, class: "cm-tex-command" },
  { tag: tags.keyword, class: "cm-tex-command" },
  // `{amsmath}`, `[utf8]`, `{Introduction}` — what a command is pointed at.
  { tag: tags.string, class: "cm-tex-arg" },
  { tag: tags.atom, class: "cm-tex-arg" },
  // The braces and brackets themselves, held back so the words inside them
  // read first.
  { tag: tags.bracket, class: "cm-tex-bracket" },
  // A `%` line is the one thing in a source that is not the paper.
  { tag: tags.comment, class: "cm-tex-comment" },
  // `11pt`, `0pt` — the measurements a preamble is full of.
  { tag: tags.number, class: "cm-tex-number" },
  // What the mode recognises as a name of its own rather than a command.
  { tag: tags.standard(tags.variableName), class: "cm-tex-builtin" },
  // What it could not make sense of. Said quietly rather than in the refusal
  // colour: a half-typed command is the ordinary state of writing, not an
  // error to be shouted at.
  { tag: tags.invalid, class: "cm-tex-unknown" },
]);
