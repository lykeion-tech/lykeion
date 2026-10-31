---
name: reproducible-analysis
description: analysis code is written or run for the study
---
# Running an analysis that can be repeated

- Tools through the door: `run_code` (a live Python, R or Julia session, for exploring) and
  `run_script` (a saved script run start to finish, for results). Each answer starts with
  `run:<key>`; `lyk runs` lists them.
- Results come only from `run_script`. The script loads the data from where it is recorded,
  fixes random seeds, prints every number it reports with a label, and writes any table or
  figure to a file it names.
- Check before reporting: row counts after loading and filtering, missing values, units, and
  one sanity check (a known total, a simple baseline, a shuffled-label control) where cheap.
- Record each result: `lyk record put result --cites run:KEY --content '{"text": "...",
  "values": [{"name": "...", "value": 0.0}], "cites": ["run:KEY"]}'`.
- If a package or file is missing, say so; do not swap in a different method silently.
- Never edit numbers by hand between the run and the record.
