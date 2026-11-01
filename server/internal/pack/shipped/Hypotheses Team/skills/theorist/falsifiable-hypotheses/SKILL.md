---
name: falsifiable-hypotheses
description: hypotheses or a prediction of an experiment's outcome are being written
---
# Writing hypotheses and predictions

For each hypothesis:
- **Statement**: one sentence, about the named system, intervention and outcome.
- **Prediction**: what will be observed if it holds, with direction and, where possible, size and
  units.
- **Falsifier**: the observation that would prove it wrong.
- **Mechanism**: why it should hold, in a few steps.
- **Rivals**: other explanations of the same observation and what would tell them apart.
- **Rests on**: the records it rests on, in the hypothesis's `cites` (at least one): a Grounding
  record by its key (`r3`) or a retrieved source's `call:KEY`; reasoning from background
  knowledge is labelled in the text, never cited.

Declare each hypothesis as a `hypothesis` item (`lyk record put hypothesis`), its prediction in
one of three shapes: a direction, a choice between named outcomes, or a numeric range with its
uncertainty; and its control, or `none` with the reason there is none.

Predicting a stated experiment's outcome:
- Restate the setup, what is measured and in which units.
- Reason from mechanism to direction, then to magnitude: anchor on known scales, adjust for the
  specific conditions, and sanity-check the order of magnitude and units.
- For a choice among options, check every option against the reasoning, rule out the ones it
  contradicts, and pick the best-supported one (or several, if the question allows several and
  more than one holds).
- For a number, give one value in the units asked, plus a plausible range.
- State confidence (low / medium / high) and the main way the prediction could be wrong.

Summary: list the hypotheses or the prediction, then what could not be determined. If the
researcher asked for a fixed final line, end with exactly that, once.
