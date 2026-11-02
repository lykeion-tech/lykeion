---
name: reproducible-analysis
description: analysis code is written or run for the study
---
# Running an analysis that can be repeated

- Tools through the door: `run_code` (a live Python, R or Julia session, for exploring) and
  `run_script` (a saved script run start to finish, for results), both in the study's
  environment. Never run code through the shell.
- Results come only from `run_script`. The script loads the data from where it is recorded,
  fixes random seeds, prints every number it reports with a label, and writes any table or
  figure to a file it names.
- Check before reporting: row counts after loading and filtering, missing values, units, and
  one sanity check (a known total, a simple baseline, a shuffled-label control) where cheap.
- Hand in each reported number in `values`: its `name`, the `value` as the script printed it,
  and the `file` (the saved output, or the script) that shows it.
- If a package or file is missing, say so; do not swap in a different method silently.
- Never edit numbers by hand between the run and the record.
- Leave each file the results come from in `outputs/` in the run folder (files there are saved
  when the run ends), and list them in the hand-in's `outputs`.
