---
name: error-hunt
description: attached analyses are re-run or attached work is read for errors
---
# Re-running and finding errors

Re-running an attached analysis:
- Run it through the door with `run_script` (a saved script from start to finish); each answer
  starts with `run:<key>`. Compare each number it prints with the number the work reports; put
  each value you report in a `value` field next to `"cites": ["run:KEY"]`.
- If it cannot run (missing files, packages, data), say exactly what failed; do not patch it into
  a different analysis and report that as the original.

Reading work for errors:
- Read section by section. Check that numbers agree across text, tables and figures' captions;
  that equations follow from the previous step; that the stated method matches what was done;
  and that conclusions do not go beyond the results.
- Report each error with an exact quote of the passage that contains it, its location (section,
  table, line), why it is wrong, and the correction where you can tell.
- List only errors you can quote; say which parts you could not check. Never pad the list.
